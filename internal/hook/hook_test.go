package hook

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/soroush67/git-policy/internal/audit"
	"github.com/soroush67/git-policy/internal/layout"
	"github.com/soroush67/git-policy/internal/state"
	"github.com/soroush67/git-policy/internal/store"
)

const (
	zero  = "0000000000000000000000000000000000000000"
	oidA  = "c6dd86fbc422e0d233a89e44a229f82181aa2a6c"
	stdin = zero + " " + oidA + " refs/heads/main\n"
)

var env = Env{Username: "alex", ProjectPath: "finance/payment-api", Protocol: "ssh"}

func setup(t *testing.T) layout.Layout {
	t.Helper()
	l := layout.New(t.TempDir())
	unlockTree(t, l.Root)
	for _, d := range l.Dirs() {
		os.MkdirAll(d.Path, 0o750)
	}
	return l
}

func deploy(t *testing.T, l layout.Layout) {
	t.Helper()
	src := []byte("apiVersion: git-policy/v1\nkind: GitPolicy\nmetadata: {name: t, revision: 1}\n")
	if _, err := store.Apply(l, store.ApplyRequest{Source: src, Actor: "t", Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

func setState(t *testing.T, l layout.Layout, s state.State) {
	t.Helper()
	if err := state.Save(l.StateFile(), s, nil); err != nil {
		t.Fatal(err)
	}
}

func run(l layout.Layout, in string) (int, string) {
	var errOut bytes.Buffer
	code := Run(l, env, strings.NewReader(in), &errOut, time.Now())
	return code, errOut.String()
}

func TestDisabledAllowsAndAudits(t *testing.T) {
	l := setup(t)
	exp := time.Now().Add(time.Hour)
	setState(t, l, state.State{Enabled: false, ExpiresAt: &exp})
	if code, out := run(l, stdin); code != 0 || out != "" {
		t.Fatalf("disabled: code=%d out=%q", code, out)
	}
	if !strings.Contains(readAudit(t, l), audit.PushAllowedDisabled) {
		t.Error("disabled push not audited")
	}
}

func TestExpiredDisableEnforces(t *testing.T) {
	l := setup(t)
	exp := time.Now().Add(-time.Minute)
	setState(t, l, state.State{Enabled: false, ExpiresAt: &exp})
	// Enforcing without a policy => fail closed.
	if code, out := run(l, stdin); code != 1 || !strings.Contains(out, "POLICY_UNAVAILABLE") {
		t.Fatalf("expired disable must enforce: code=%d out=%q", code, out)
	}
}

func TestFailClosed(t *testing.T) {
	t.Run("missing state means enabled", func(t *testing.T) {
		l := setup(t)
		if code, _ := run(l, stdin); code != 1 {
			t.Fatal("no state + no policy must reject")
		}
	})
	t.Run("malformed state", func(t *testing.T) {
		l := setup(t)
		deploy(t, l)
		os.WriteFile(l.StateFile(), []byte("{not json"), 0o640)
		code, out := run(l, stdin)
		if code != 1 || !strings.Contains(out, "GL-HOOK-ERR: Rule: POLICY_UNAVAILABLE") {
			t.Fatalf("code=%d out=%q", code, out)
		}
		if strings.Contains(out, l.Root) {
			t.Error("rejection message leaks internal paths")
		}
	})
	t.Run("malformed ref update", func(t *testing.T) {
		l := setup(t)
		deploy(t, l)
		setState(t, l, state.State{Enabled: true})
		if code, out := run(l, "garbage line\n"); code != 1 || !strings.Contains(out, "INVALID_REF_UPDATE") {
			t.Fatalf("code=%d out=%q", code, out)
		}
	})
}

func TestEnabledWithPolicyAccepts(t *testing.T) {
	l := setup(t)
	deploy(t, l)
	setState(t, l, state.State{Enabled: true})
	if code, out := run(l, stdin); code != 0 || out != "" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestParseUpdates(t *testing.T) {
	sha256a := strings.Repeat("a", 64)
	good := zero + " " + oidA + " refs/heads/main\n" + oidA + " " + zero + " refs/tags/v1\n" +
		strings.Repeat("0", 64) + " " + sha256a + " refs/heads/x\n"
	ups, err := ParseUpdates(strings.NewReader(good))
	if err != nil || len(ups) != 3 || !IsZero(ups[0].Old) || !IsZero(ups[1].New) {
		t.Fatalf("%v %+v", err, ups)
	}
	for _, bad := range []string{
		"x y z\n",
		zero + " " + oidA + "\n",
		zero + " " + sha256a + " refs/heads/mixed-length\n",
		zero + " " + oidA + " HEAD\n",
		zero + " " + oidA + " refs/heads/a\x1b[31m\n",
		zero + " " + strings.ToUpper(oidA) + " refs/heads/upper\n",
	} {
		if _, err := ParseUpdates(strings.NewReader(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestConcurrentAuditWrites(t *testing.T) {
	l := setup(t)
	exp := time.Now().Add(time.Hour)
	setState(t, l, state.State{Enabled: false, ExpiresAt: &exp})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); run(l, stdin) }()
	}
	wg.Wait()
	sc := bufio.NewScanner(strings.NewReader(readAudit(t, l)))
	n := 0
	for sc.Scan() {
		var ev map[string]any
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatalf("interleaved/corrupt audit line %q: %v", sc.Text(), err)
		}
		n++
	}
	if n != 50 {
		t.Fatalf("want 50 audit lines, got %d", n)
	}
}

func readAudit(t *testing.T, l layout.Layout) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(audit.FileName(l.LogsDir(), time.Now())))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// unlockTree lets t.TempDir cleanup delete the deliberately read-only version dirs.
func unlockTree(t *testing.T, root string) {
	t.Cleanup(func() {
		filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err == nil && info.IsDir() {
				os.Chmod(p, 0o750)
			}
			return nil
		})
	})
}

func deployPolicy(t *testing.T, l layout.Layout, src string) {
	t.Helper()
	if _, err := store.Apply(l, store.ApplyRequest{Source: []byte(src), Actor: "t", Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptEventsAndAuditRequired(t *testing.T) {
	l := setup(t)
	deployPolicy(t, l, "apiVersion: git-policy/v1\nkind: GitPolicy\nmetadata: {name: t, revision: 1}\nsettings: {audit: {log_accepted: true, required: true}}\n")
	setState(t, l, state.State{Enabled: true})
	if code, out := run(l, stdin); code != 0 || out != "" {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if a := readAudit(t, l); !strings.Contains(a, `"action":"ACCEPT"`) || !strings.Contains(a, `"refs":[{`) {
		t.Fatalf("ACCEPT event missing: %s", a)
	}
	// Audit log not writable + required: the push must be rejected.
	f := audit.FileName(l.LogsDir(), time.Now())
	os.Chmod(f, 0o440)
	defer os.Chmod(f, 0o640)
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes")
	}
	code, out := run(l, stdin)
	if code != 1 || !strings.Contains(out, "Rule: AUDIT_UNAVAILABLE") {
		t.Fatalf("required audit: code=%d out=%q", code, out)
	}
}

func TestAuditNotRequiredKeepsAccepting(t *testing.T) {
	l := setup(t)
	deployPolicy(t, l, "apiVersion: git-policy/v1\nkind: GitPolicy\nmetadata: {name: t, revision: 1}\nsettings: {audit: {log_accepted: true}}\n")
	setState(t, l, state.State{Enabled: true})
	run(l, stdin) // creates today's file
	f := audit.FileName(l.LogsDir(), time.Now())
	os.Chmod(f, 0o440)
	defer os.Chmod(f, 0o640)
	if code, _ := run(l, stdin); code != 0 {
		t.Fatal("audit.required=false: a failing audit write must not reject")
	}
}
