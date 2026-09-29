package policy

import (
	"bytes"
	"os"
	"reflect"
	"testing"
)

func TestCompileExample(t *testing.T) {
	c, fs := build(t, repoPath("examples/policy.example.yaml"))
	if c == nil {
		t.Fatalf("example invalid:\n%s", dump(fs))
	}
	if c.Schema != CompiledSchema || c.Name != "org-git-policy" || c.Revision != 1 {
		t.Errorf("header: %+v", c)
	}
	if got := c.Mandatory.Block.Extensions; !reflect.DeepEqual(got, []string{"dll", "exe"}) {
		t.Errorf("mandatory extensions = %v", got)
	}
	if c.Mandatory.MaxFileSize != 50<<20 {
		t.Errorf("mandatory size = %d", c.Mandatory.MaxFileSize)
	}
	if c.Namespaces["finance"].MaxFileSize != 10<<20 || c.Projects["finance/payment-api"].MaxFileSize != 5<<20 {
		t.Errorf("scoped sizes not compiled")
	}
	if c.Projects["sandbox/playground"].Enabled || !c.Projects["finance/reporting"].Enabled {
		t.Errorf("enabled flags wrong")
	}
	if got := c.Namespaces["finance"].Refs["refs/heads/release/**"].Block.Signatures; !reflect.DeepEqual(got, []string{"pe"}) {
		t.Errorf("release signatures = %v", got)
	}
	if got := c.Namespaces["finance"].Block.Paths; got[0] != "**/bin/debug/**" {
		t.Errorf("paths must be lower-cased: %v", got)
	}
	if c.Users["alex"].Projects["finance/payment-api"].Push != "deny" {
		t.Errorf("identity not compiled")
	}
	if len(c.Exceptions) != 3 || c.Exceptions[0].ID != "EXC-2026-001" || c.Exceptions[0].ExpiresAt != "2026-12-31T23:59:59Z" {
		t.Errorf("exceptions: %+v", c.Exceptions)
	}
	if c.Exceptions[2].MaxFileSize != 200<<20 {
		t.Errorf("exception size = %d", c.Exceptions[2].MaxFileSize)
	}
	if c.Settings.Limits.EvaluationTimeoutSeconds != 45 || c.Settings.Membership.HardMaxAgeSeconds != 86400 {
		t.Errorf("settings: %+v", c.Settings)
	}
	if c.Settings.Messages.Remediation[BlockedPath] == "" {
		t.Errorf("built-in remediation missing")
	}
}

func TestCompileIsDeterministic(t *testing.T) {
	src, err := os.ReadFile(repoPath("examples/policy.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var prev []byte
	for i := 0; i < 20; i++ { // map iteration order is random; output must not be
		c, fs := Build(src, Options{Now: testNow})
		if c == nil {
			t.Fatalf("invalid: %s", dump(fs))
		}
		b, err := MarshalCompiled(c)
		if err != nil {
			t.Fatal(err)
		}
		if prev != nil && !bytes.Equal(prev, b) {
			t.Fatal("compiled output differs between runs")
		}
		prev = b
	}
}

func TestCaseInsensitiveKeysAreFolded(t *testing.T) {
	src := `apiVersion: git-policy/v1
kind: GitPolicy
metadata: {name: t, revision: 1}
namespaces:
  Finance: {max_file_size: 10MiB}
users:
  Alex:
    projects:
      Finance/Payment-API: {push: deny}
`
	c, fs := Build([]byte(src), Options{Now: testNow})
	if c == nil {
		t.Fatalf("invalid: %s", dump(fs))
	}
	if _, ok := c.Namespaces["finance"]; !ok {
		t.Error("namespace key not case-folded")
	}
	if c.Users["alex"].Projects["finance/payment-api"].Push != "deny" {
		t.Error("identity keys not case-folded")
	}
}
