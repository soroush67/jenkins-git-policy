package engine

import (
	"encoding/binary"
	"strings"
	"testing"
)

// fakePE builds a minimal header: "MZ", e_lfanew at 0x3C, "PE\0\0" there.
func fakePE(lfanew uint32, total int) []byte {
	b := make([]byte, total)
	copy(b, "MZ")
	binary.LittleEndian.PutUint32(b[0x3C:], lfanew)
	if int(lfanew)+4 <= total {
		copy(b[lfanew:], "PE\x00\x00")
	}
	return b
}

func TestIsPE(t *testing.T) {
	if !IsPE(fakePE(0x80, 512)) {
		t.Error("valid PE header not detected")
	}
	if !IsPE(fakePE(0x3F00, 0x4000)) {
		t.Error("PE header far into the file not detected")
	}
	notPE := map[string][]byte{
		"empty":            nil,
		"text starting MZ": []byte("MZ is the initials of Mark Zbikowski, not an executable header............."),
		"MZ without PE":    func() []byte { b := fakePE(0x80, 512); copy(b[0x80:], "XX"); return b }(),
		"lfanew too small": fakePE(0x10, 512),
		"lfanew past head": fakePE(0x1000, 512),
		"ELF":              append([]byte("\x7fELF"), make([]byte, 100)...),
	}
	for name, b := range notPE {
		if IsPE(b) {
			t.Errorf("%s: false positive", name)
		}
	}
}

func checkBlob(e *Engine, user, project, ref, path string, size int64, head []byte, m Membership) *Decision {
	r := push(user, project, ref, m)
	d := e.Evaluate(r)
	e.CheckBlobs(d, r, e.Classifier(d.Project, d.RepoMode), []BlobEntry{{
		PathEntry: PathEntry{Ref: ref, Commit: oid, Path: path}, Blob: oid, Size: size, Head: head}})
	return d
}

// Size and signature worked examples of PHASE-2-SCHEMA.md §7.
func TestWorkedExamplesBlobs(t *testing.T) {
	e := example(t)
	const mib = 1 << 20
	pe := fakePE(0x80, 4096)
	cases := []struct {
		name, user, project, ref, path string
		size                           int64
		head                           []byte
		m                              Membership
		want                           string
	}{
		{"E08 8MiB in payment-api (5MiB)", "bob", "finance/payment-api", "refs/heads/main", "data.bin", 8 * mib, nil, fresh(), "REJECT:FILE_TOO_LARGE"},
		{"E09 8MiB in accounting (10MiB)", "bob", "finance/accounting", "refs/heads/main", "data.bin", 8 * mib, nil, fresh(), ""},
		{"exactly at the limit is allowed", "bob", "finance/payment-api", "refs/heads/main", "data.bin", 5 * mib, nil, fresh(), ""},
		{"one byte over", "bob", "finance/payment-api", "refs/heads/main", "data.bin", 5*mib + 1, nil, fresh(), "REJECT:FILE_TOO_LARGE"},
		{"E17 PE named .txt on release", "bob", "finance/accounting", "refs/heads/release/2.0", "readme.txt", 4096, pe, fresh(), "REJECT:BLOCKED_SIGNATURE"},
		{"E18 same file on feature branch", "bob", "finance/accounting", "refs/heads/feature/x", "readme.txt", 4096, pe, fresh(), ""},
		{"release: normal text file", "bob", "finance/accounting", "refs/heads/release/2.0", "notes.txt", 20, []byte("just some notes here"), fresh(), ""},
		{"E20 120MiB model waived (group exception, mandatory)", "ds1", "analytics/forecast", "refs/heads/main", "model.bin", 120 * mib, nil, fresh("data-science"), "WAIVED:EXC-2026-003"},
		{"E21 300MiB above the exception's max", "ds1", "analytics/forecast", "refs/heads/main", "model.bin", 300 * mib, nil, fresh("data-science"), "REJECT:FILE_TOO_LARGE"},
		{"group exception ignored when cache expired", "ds1", "analytics/forecast", "refs/heads/main", "model.bin", 120 * mib, nil, expired("data-science"), "REJECT:FILE_TOO_LARGE"},
		{"above mandatory cap in sandbox (enabled: false)", "bob", "sandbox/playground", "refs/heads/main", "big.iso", 60 * mib, nil, fresh(), "REJECT:FILE_TOO_LARGE"},
		{"30MiB in sandbox: only the 50MiB cap applies", "bob", "sandbox/playground", "refs/heads/main", "big.bin", 30 * mib, nil, fresh(), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := summary(checkBlob(e, c.user, c.project, c.ref, c.path, c.size, c.head, c.m)); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestSizeViolationIsMandatoryOnlyAboveCap(t *testing.T) {
	e := New(compile(t, `apiVersion: git-policy/v1
kind: GitPolicy
metadata: {name: t, revision: 1}
mandatory: {max_file_size: 50MiB}
defaults: {max_file_size: 10MiB}
exceptions:
  - id: EXC-MEDIA
    rules: [FILE_TOO_LARGE]
    subjects: {users: [designer]}
    max_file_size: 40MiB
    reason: marketing media assets, non-mandatory waiver
    expires: 2026-10-20
`))
	const mib = 1 << 20
	d := checkBlob(e, "designer", "a/b", "refs/heads/main", "video.mp4", 30*mib, nil, fresh())
	if got := summary(d); got != "WAIVED:EXC-MEDIA" {
		t.Fatalf("scoped violation must be waivable without mandatory flag: %s", got)
	}
	d = checkBlob(e, "designer", "a/b", "refs/heads/main", "video.mp4", 60*mib, nil, fresh())
	if got := summary(d); got != "REJECT:FILE_TOO_LARGE" || !d.Violations[0].Mandatory {
		t.Fatalf("above the mandatory cap needs mandatory: true: %s %+v", got, d.Violations)
	}
	if !strings.Contains(d.Violations[0].Detail, "60.0 MiB (limit 10.0 MiB)") {
		t.Errorf("detail: %s", d.Violations[0].Detail)
	}
}
