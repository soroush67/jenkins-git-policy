package engine

import (
	"strings"
	"testing"

	"github.com/soroush67/git-policy/internal/policy"
)

func TestNormalizeAndExtensions(t *testing.T) {
	cases := []struct {
		path string
		want []string // must be contained in Extensions(path)
		not  []string
	}{
		{"Lib/Mic.Caching.dll", []string{"dll", "caching.dll"}, nil},
		{"TEST.DLL", []string{"dll"}, nil},
		{"Test.Dll", []string{"dll"}, nil},
		{"evil.dll.", []string{"dll"}, nil},           // NTFS drops trailing dots
		{"evil.dll ", []string{"dll"}, nil},           // ... and trailing spaces
		{"evil.dll. . ", []string{"dll"}, nil},        // ... repeatedly
		{"evil.dll::$DATA", []string{"dll"}, nil},     // NTFS default stream
		{"evil.dll:hidden.txt", []string{"dll"}, nil}, // named stream: raw form still yields txt
		{"a.tar.gz", []string{"gz", "tar.gz"}, nil},
		{".dll", []string{"dll"}, nil}, // dot-file named like the extension
		{"dir.dll/readme", nil, []string{"dll"}},
		{"README", nil, []string{""}},
		{"archive.zip.txt", []string{"txt", "zip.txt"}, []string{"zip"}},
	}
	for _, c := range cases {
		got := strings.Join(Extensions(c.path), ",")
		for _, w := range c.want {
			if !contains(Extensions(c.path), w) {
				t.Errorf("Extensions(%q) = %s; missing %q", c.path, got, w)
			}
		}
		for _, n := range c.not {
			if contains(Extensions(c.path), n) {
				t.Errorf("Extensions(%q) = %s; must not contain %q", c.path, got, n)
			}
		}
	}
	if n := Normalize("Bin. /Debug:x/App.EXE. "); n != "bin/debug/app.exe" {
		t.Errorf("Normalize = %q", n)
	}
}

func checkPaths(e *Engine, user, project, ref string, m Membership, paths ...string) *Decision {
	r := push(user, project, ref, m)
	d := e.Evaluate(r)
	var es []PathEntry
	for _, p := range paths {
		es = append(es, PathEntry{Ref: ref, Commit: oid, Path: p})
	}
	e.CheckPaths(d, r, e.Classifier(d.Project, d.RepoMode), es)
	return d
}

func summary(d *Decision) string {
	var out []string
	for _, v := range d.Violations {
		out = append(out, "REJECT:"+v.Code)
	}
	for _, v := range d.WouldReject {
		out = append(out, "AUDIT:"+v.Code)
	}
	for _, w := range d.Waived {
		out = append(out, "WAIVED:"+w.ExceptionID)
	}
	return strings.Join(out, ",")
}

// Content worked examples of PHASE-2-SCHEMA.md §7.
func TestWorkedExamplesPaths(t *testing.T) {
	e := example(t)
	cases := []struct {
		name, user, project, ref, path, want string
	}{
		{"E06 upper-case DLL", "bob", "finance/payment-api", "refs/heads/main", "Lib/Mic.Caching.DLL", "REJECT:BLOCKED_EXTENSION"},
		{"E07 trailing dot", "bob", "finance/payment-api", "refs/heads/main", "Lib/evil.dll.", "REJECT:BLOCKED_EXTENSION"},
		{"normal source file (TEST 01)", "bob", "finance/payment-api", "refs/heads/main", "src/Program.cs", ""},
		{"E10 zip unblocked for reporting", "alex", "finance/reporting", "refs/heads/main", "templates/q3.zip", ""},
		{"zip blocked elsewhere", "bob", "finance/accounting", "refs/heads/main", "templates/q3.zip", "REJECT:BLOCKED_EXTENSION"},
		{"E11 audit mode in finance/legacy", "bob", "finance/legacy/billing", "refs/heads/main", "bin/x.pdb", "AUDIT:BLOCKED_EXTENSION"},
		{"E12 mandatory still enforced there", "bob", "finance/legacy/billing", "refs/heads/main", "x.dll", "REJECT:BLOCKED_EXTENSION"},
		{"E13 exception: vendored SDK on main", "bob", "finance/legacy-erp", "refs/heads/main", "vendor/VendorSdk/Sdk.dll", "WAIVED:EXC-2026-001"},
		{"E14 same file on dev branch", "bob", "finance/legacy-erp", "refs/heads/dev", "vendor/VendorSdk/Sdk.dll", "REJECT:BLOCKED_EXTENSION"},
		{"exception path is exact", "bob", "finance/legacy-erp", "refs/heads/main", "vendor/Other/x.dll", "REJECT:BLOCKED_EXTENSION"},
		{"E15 sandbox: zip allowed", "bob", "sandbox/playground", "refs/heads/main", "a.zip", ""},
		{"E16 sandbox: exe still blocked", "bob", "sandbox/playground", "refs/heads/main", "a.exe", "REJECT:BLOCKED_EXTENSION"},
		{"blocked path (finance obj/)", "bob", "finance/accounting", "refs/heads/main", "src/obj/Debug/app.cache", "REJECT:BLOCKED_PATH"},
		{"blocked path case-insensitive", "bob", "finance/accounting", "refs/heads/main", "App/BIN/Debug/app.pdb2", "REJECT:BLOCKED_PATH"},
		{"blocked path outside finance allowed", "bob", "other/app", "refs/heads/main", "src/obj/x", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := checkPaths(e, c.user, c.project, c.ref, fresh(), c.path)
			if got := summary(d); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestMandatoryAuditMode(t *testing.T) {
	e := New(compile(t, `apiVersion: git-policy/v1
kind: GitPolicy
metadata: {name: t, revision: 1}
mandatory:
  mode: audit
  blocked_extensions: [dll]
defaults:
  blocked_extensions: [zip]
`))
	d := checkPaths(e, "bob", "a/b", "refs/heads/main", fresh(), "x.dll", "y.zip")
	if got := summary(d); got != "REJECT:BLOCKED_EXTENSION,AUDIT:BLOCKED_EXTENSION" {
		t.Fatalf("mandatory audit + scoped enforce: %s", got)
	}
	if d.WouldReject[0].Path != "x.dll" || !d.WouldReject[0].Mandatory || d.Violations[0].Path != "y.zip" {
		t.Fatalf("%+v %+v", d.WouldReject, d.Violations)
	}
}

func TestFindingsAreCapped(t *testing.T) {
	e := New(compile(t, "apiVersion: git-policy/v1\nkind: GitPolicy\nmetadata: {name: t, revision: 1}\nmandatory: {blocked_extensions: [dll]}\n"))
	var ps []string
	for i := 0; i < maxRecorded+50; i++ {
		ps = append(ps, "lib/"+strings.Repeat("x", i%7+1)+string(rune('a'+i%26))+".dll")
	}
	d := checkPaths(e, "bob", "a/b", "refs/heads/main", fresh(), ps...)
	if len(d.Violations) != maxRecorded || !d.Truncated {
		t.Fatalf("violations %d truncated %v", len(d.Violations), d.Truncated)
	}
	_ = policy.BlockedExtension
}
