package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/soroush67/git-policy/internal/layout"
)

var now = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func newLayout(t *testing.T) layout.Layout {
	t.Helper()
	l := layout.New(t.TempDir())
	unlockTree(t, l.Root)
	for _, d := range l.Dirs() {
		if err := os.MkdirAll(d.Path, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

func policyRev(rev int) []byte {
	return []byte(fmt.Sprintf(`apiVersion: git-policy/v1
kind: GitPolicy
metadata: {name: test, revision: %d}
mandatory:
  blocked_extensions: [dll]
`, rev))
}

func apply(t *testing.T, l layout.Layout, src []byte) *ApplyResult {
	t.Helper()
	res, err := Apply(l, ApplyRequest{Source: src, Actor: "test", Now: now})
	if err != nil {
		t.Fatalf("apply: %v %v", err, res.Findings)
	}
	return res
}

func TestApplyActivatesAndRecordsPrevious(t *testing.T) {
	l := newLayout(t)
	if _, err := LoadActive(l); err != ErrNoPolicy {
		t.Fatalf("empty store: want ErrNoPolicy, got %v", err)
	}
	r1 := apply(t, l, policyRev(1))
	r2 := apply(t, l, policyRev(2))
	if r1.Meta.Version != 1 || r2.Meta.Version != 2 || r2.Previous != 1 {
		t.Fatalf("versions: %+v %+v", r1.Meta, r2)
	}
	act, err := LoadActive(l)
	if err != nil || act.Version != 2 || act.Meta.Revision != 2 || act.FellBack {
		t.Fatalf("active: %+v %v", act, err)
	}
	for _, name := range []string{"policy.yaml", "compiled.json", "meta.json"} {
		st, err := os.Stat(filepath.Join(l.VersionDir(2), name))
		if err != nil || st.Mode().Perm() != 0o440 {
			t.Errorf("%s: %v %v", name, st.Mode(), err)
		}
	}
	if st, _ := os.Stat(l.VersionDir(2)); st.Mode().Perm() != 0o550 {
		t.Errorf("version dir mode %v", st.Mode())
	}
	if entries, _ := os.ReadDir(l.TmpDir()); len(entries) != 0 {
		t.Errorf("tmp not cleaned: %v", entries)
	}
}

func TestInvalidPolicyNeverReplacesActive(t *testing.T) {
	l := newLayout(t)
	apply(t, l, policyRev(1))
	for name, src := range map[string][]byte{
		"invalid":        []byte("apiVersion: git-policy/v1\nkind: GitPolicy\nmetadata: {name: test, revision: 2}\ndefaults: {max_file_size: 20MB}\n"),
		"same revision":  policyRev(1),
		"older revision": policyRev(0 + 1),
	} {
		res, err := Apply(l, ApplyRequest{Source: src, Actor: "test", Now: now})
		if err != ErrInvalidPolicy {
			t.Fatalf("%s: want ErrInvalidPolicy, got %v", name, err)
		}
		if len(res.Findings) == 0 {
			t.Errorf("%s: no findings reported", name)
		}
		if v, _ := ActiveVersion(l); v != 1 {
			t.Fatalf("%s: active changed to %d", name, v)
		}
	}
	if vs, _ := Versions(l); len(vs) != 1 {
		t.Fatalf("refused applies must not store versions: %v", vs)
	}
}

func TestRollback(t *testing.T) {
	l := newLayout(t)
	apply(t, l, policyRev(1))
	apply(t, l, policyRev(2))
	from, to, err := Rollback(l, 0, nil)
	if err != nil || from != 2 || to != 1 {
		t.Fatalf("rollback: %d->%d %v", from, to, err)
	}
	if p, _ := PreviousVersion(l); p != 2 {
		t.Fatalf("previous after rollback = %d", p)
	}
	// Rolling back again returns to 2 (PREVIOUS swaps).
	if _, to, err := Rollback(l, 0, nil); err != nil || to != 2 {
		t.Fatalf("second rollback: %d %v", to, err)
	}
	if _, _, err := Rollback(l, 2, nil); err == nil {
		t.Fatal("rollback to the active version must fail")
	}
	if _, _, err := Rollback(l, 7, nil); err == nil {
		t.Fatal("rollback to a missing version must fail")
	}
	// A new apply after rollback must be newer than the ACTIVE revision.
	if _, err := Apply(l, ApplyRequest{Source: policyRev(2), Now: now}); err != ErrInvalidPolicy {
		t.Fatalf("stale revision after rollback accepted: %v", err)
	}
}

func TestFallbackToPreviousOnCorruption(t *testing.T) {
	l := newLayout(t)
	apply(t, l, policyRev(1))
	apply(t, l, policyRev(2))
	corrupt(t, filepath.Join(l.VersionDir(2), "compiled.json"))

	act, err := LoadActive(l)
	if err != nil || !act.FellBack || act.Version != 1 || act.ActiveError == nil {
		t.Fatalf("want fallback to 1, got %+v %v", act, err)
	}
	corrupt(t, filepath.Join(l.VersionDir(1), "compiled.json"))
	if _, err := LoadActive(l); err == nil {
		t.Fatal("both versions corrupt: want error (fail closed)")
	}
	// Recovery: a fresh apply works even with nothing loadable.
	r := apply(t, l, policyRev(3))
	if act, err := LoadActive(l); err != nil || act.Version != r.Meta.Version {
		t.Fatalf("recovery apply: %+v %v", act, err)
	}
}

func TestSymlinkedVersionDirRejected(t *testing.T) {
	l := newLayout(t)
	apply(t, l, policyRev(1))
	evil := t.TempDir()
	if err := os.Symlink(evil, l.VersionDir(2)); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadVersion(l, 2); err == nil {
		t.Fatal("symlinked version dir accepted")
	}
}

// TestConcurrentApplyAndRead is the Phase-1 §27 guarantee: readers see the
// old or the new policy, never a partial one, while writers switch versions.
func TestConcurrentApplyAndRead(t *testing.T) {
	l := newLayout(t)
	apply(t, l, policyRev(1))
	var wg sync.WaitGroup
	stop := make(chan struct{})
	errs := make(chan error, 100)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				act, err := LoadActive(l)
				if err != nil {
					errs <- err
					return
				}
				if act.FellBack || act.Policy.Revision != act.Meta.Revision || act.Policy.SourceSHA256 != act.Meta.SourceSHA256 {
					errs <- fmt.Errorf("inconsistent view: %+v", act.Meta)
					return
				}
			}
		}()
	}
	for rev := 2; rev <= 40; rev++ {
		apply(t, l, policyRev(rev))
	}
	close(stop)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func corrupt(t *testing.T, path string) {
	t.Helper()
	os.Chmod(filepath.Dir(path), 0o750)
	os.Chmod(path, 0o640)
	if err := os.WriteFile(path, []byte("{broken"), 0o640); err != nil {
		t.Fatal(err)
	}
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
