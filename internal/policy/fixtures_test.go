package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixed reference date so exception expiry checks are deterministic.
var testNow = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func repoPath(p string) string { return filepath.Join("..", "..", p) }

func build(t *testing.T, file string) (*Compiled, []Finding) {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return Build(src, Options{Now: testNow})
}

// expectedCode reads "# expect: V020 (...)" from the first line of a fixture.
func expectedCode(t *testing.T, file string) string {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(string(src), "\n")
	rest, ok := strings.CutPrefix(first, "# expect: ")
	if !ok {
		t.Fatalf("%s: missing '# expect: <code>' header", file)
	}
	code, _, _ := strings.Cut(rest, " ")
	return code
}

func TestExamplesAreValid(t *testing.T) {
	files, _ := filepath.Glob(repoPath("examples/*.yaml"))
	if len(files) == 0 {
		t.Fatal("no examples found")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			c, fs := build(t, f)
			if c == nil {
				t.Fatalf("expected valid policy, got findings:\n%s", dump(fs))
			}
			for _, x := range fs {
				t.Logf("warning: %s", x)
			}
		})
	}
}

func TestInvalidFixtures(t *testing.T) {
	for _, dir := range []string{"schema-invalid", "semantic-invalid"} {
		files, _ := filepath.Glob(repoPath("testdata/policies/" + dir + "/*.yaml"))
		if len(files) == 0 {
			t.Fatalf("no fixtures in %s", dir)
		}
		for _, f := range files {
			t.Run(dir+"/"+filepath.Base(f), func(t *testing.T) {
				want := expectedCode(t, f)
				c, fs := build(t, f)
				if c != nil {
					t.Fatalf("expected %s, but policy compiled", want)
				}
				for _, x := range fs {
					if x.Code == want && x.Severity == SevError {
						return
					}
				}
				t.Fatalf("expected error %s, got:\n%s", want, dump(fs))
			})
		}
	}
}

func TestWarnings(t *testing.T) {
	src := `apiVersion: git-policy/v1
kind: GitPolicy
metadata: {name: t, revision: 1}
mandatory:
  blocked_extensions: [.dll, exe, EXE]
  deny_users: [gone.user]
defaults:
  blocked_extensions: [zip]
namespaces:
  finance:
    unblock_extensions: [rar]
projects:
  finance/reporting:
    unblock_extensions: [zip]
users:
  gone.user: {push: allow}
exceptions:
  - id: EXC-OLD
    rules: [FILE_TOO_LARGE]
    subjects: {users: [a]}
    max_file_size: 1GiB
    reason: expired exception kept for history
    expires: 2026-01-01
`
	c, fs := Build([]byte(src), Options{Now: testNow})
	if c == nil {
		t.Fatalf("expected valid policy:\n%s", dump(fs))
	}
	got := map[string]int{}
	for _, f := range fs {
		got[f.Code]++
	}
	for code, n := range map[string]int{"W001": 1, "W002": 1, "W003": 1, "W004": 1, "W011": 1} {
		if got[code] != n {
			t.Errorf("%s: want %d, got %d\n%s", code, n, got[code], dump(fs))
		}
	}
	// zip is blocked by defaults, so the project unblock is legitimate (no W003 for it).
	for _, f := range fs {
		if f.Code == "W003" && strings.Contains(f.Path, "reporting") {
			t.Errorf("unexpected W003 on project unblock: %s", f)
		}
	}
}

func TestParseRejectsYAMLFeatures(t *testing.T) {
	cases := map[string]string{
		"empty":          "",
		"multi-document": "apiVersion: git-policy/v1\n---\nkind: GitPolicy\n",
		"alias":          "apiVersion: &a git-policy/v1\nkind: *a\n",
		"not a mapping":  "- a\n- b\n",
		"syntax":         "apiVersion: [unclosed\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			doc, fs := Parse([]byte(src))
			if doc != nil || len(fs) == 0 || fs[0].Code != "V001" {
				t.Fatalf("want V001, got doc=%v findings=%v", doc != nil, fs)
			}
		})
	}
}

func dump(fs []Finding) string {
	var b strings.Builder
	for _, f := range fs {
		b.WriteString("  " + f.String() + "\n")
	}
	return b.String()
}
