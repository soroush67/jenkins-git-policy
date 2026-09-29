package gitscan

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// repo is a scratch repository. "Pushed" objects are made reachable only
// from refs/incoming/* (neither heads nor tags), which is exactly the
// pre-receive situation: objects exist, refs are not updated yet.
type repo struct {
	t   *testing.T
	dir string
	git string
}

func newRepo(t *testing.T, args ...string) *repo {
	t.Helper()
	git, err := FindGit()
	if err != nil {
		t.Skip("git not available")
	}
	r := &repo{t: t, dir: t.TempDir(), git: git}
	r.run(append([]string{"init", "-q", "-b", "main"}, args...)...)
	r.run("config", "user.email", "t@t")
	r.run("config", "user.name", "t")
	r.run("config", "commit.gpgsign", "false")
	return r
}

func (r *repo) run(args ...string) string {
	r.t.Helper()
	cmd := exec.Command(r.git, args...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+r.dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *repo) write(path, content string) {
	r.t.Helper()
	full := filepath.Join(r.dir, path)
	os.MkdirAll(filepath.Dir(full), 0o755)
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// commit writes files ("" content = delete) and commits them.
func (r *repo) commit(msg string, files map[string]string) string {
	r.t.Helper()
	for p, c := range files {
		if c == "" {
			r.run("rm", "-q", "--", p)
		} else {
			r.write(p, c)
			r.run("add", "--", p)
		}
	}
	r.run("commit", "-q", "--allow-empty", "-m", msg)
	return r.run("rev-parse", "HEAD")
}

func (r *repo) head() string { return r.run("rev-parse", "HEAD") }

// sameClass: every ref in one class (no branch-scoped content rules).
func sameClass() (func(string) int, func(int, int) bool) {
	return func(string) int { return 0 }, func(a, b int) bool { return a == b }
}

func (r *repo) scan(o Options, ups ...Update) (*Result, error) {
	r.t.Helper()
	o.Git, o.Dir = r.git, r.dir
	if o.ClassOf == nil {
		o.ClassOf, o.Covers = sameClass()
	}
	return Scan(context.Background(), o, ups)
}

func zeroOf(oid string) string { return strings.Repeat("0", len(oid)) }

func paths(res *Result) []string {
	var out []string
	for _, e := range res.Entries {
		out = append(out, e.Path)
	}
	sort.Strings(out)
	return out
}

func mustScan(t *testing.T, r *repo, o Options, ups ...Update) *Result {
	t.Helper()
	res, err := r.scan(o, ups...)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestAddThenDeleteIsFound(t *testing.T) { // TEST 09
	r := newRepo(t)
	base := r.commit("base", map[string]string{"a.txt": "a"})
	r.run("checkout", "-q", "-b", "work")
	a := r.commit("add dll", map[string]string{"Lib/Mic.Caching.dll": "MZ..."})
	r.commit("delete dll", map[string]string{"Lib/Mic.Caching.dll": ""})
	tip := r.head()
	r.run("checkout", "-q", "main")
	r.run("branch", "-D", "work")
	r.run("update-ref", "refs/incoming/1", tip)

	res := mustScan(t, r, Options{}, Update{Old: base, New: tip, Ref: "refs/heads/main"})
	if res.Stats.Commits != 2 {
		t.Fatalf("want 2 new commits, got %d", res.Stats.Commits)
	}
	found := false
	for _, e := range res.Entries {
		if e.Path == "Lib/Mic.Caching.dll" && e.Commit == a && e.Ref == "refs/heads/main" {
			found = true
		}
	}
	if !found {
		t.Fatalf("dll added in an intermediate commit not found: %+v", res.Entries)
	}
	if final := r.run("ls-tree", "-r", "--name-only", tip); strings.Contains(final, ".dll") {
		t.Fatal("test setup: the tip must not contain the dll")
	}
}

func TestRenameExistingBlobIsFound(t *testing.T) {
	r := newRepo(t)
	base := r.commit("base", map[string]string{"readme.txt": "same content"})
	r.run("mv", "readme.txt", "evil.DLL")
	r.run("commit", "-q", "-m", "rename")
	tip := r.head()
	r.run("reset", "-q", "--hard", base)
	r.run("update-ref", "refs/incoming/1", tip)
	res := mustScan(t, r, Options{}, Update{Old: base, New: tip, Ref: "refs/heads/main"})
	if got := paths(res); len(got) != 1 || got[0] != "evil.DLL" {
		t.Fatalf("renamed path not reported: %v", got)
	}
}

func TestExistingHistoryIsNotRescanned(t *testing.T) { // TEST 10 (clean part)
	r := newRepo(t)
	r.commit("1", map[string]string{"a": "1"})
	tip := r.commit("2", map[string]string{"b": "2"})
	res := mustScan(t, r, Options{}, Update{Old: zeroOf(tip), New: tip, Ref: "refs/heads/feature"})
	if res.Stats.Commits != 0 || len(res.Entries) != 0 {
		t.Fatalf("new branch at an existing commit must introduce nothing: %+v", res.Stats)
	}
}

func TestNewBranchWithNewCommits(t *testing.T) { // TEST 10
	r := newRepo(t)
	base := r.commit("1", map[string]string{"a": "1"})
	r.run("checkout", "-q", "-b", "tmp")
	tip := r.commit("x", map[string]string{"tools/setup.exe": "MZ"})
	r.run("checkout", "-q", "main")
	r.run("branch", "-D", "tmp")
	r.run("update-ref", "refs/incoming/1", tip)
	res := mustScan(t, r, Options{}, Update{Old: zeroOf(tip), New: tip, Ref: "refs/heads/feature"})
	if got := paths(res); len(got) != 1 || got[0] != "tools/setup.exe" || res.Stats.Commits != 1 {
		t.Fatalf("got %v (%+v), base %s", got, res.Stats, base)
	}
}

func TestForcePush(t *testing.T) { // TEST 11
	r := newRepo(t)
	base := r.commit("base", map[string]string{"a": "1"})
	old := r.commit("old work", map[string]string{"old.dll": "x"}) // already on main (was accepted earlier)
	r.run("reset", "-q", "--hard", base)
	tip := r.commit("rewritten", map[string]string{"new.txt": "y"})
	r.run("update-ref", "refs/heads/main", old) // server still has old
	r.run("update-ref", "refs/incoming/1", tip)
	res := mustScan(t, r, Options{}, Update{Old: old, New: tip, Ref: "refs/heads/main"})
	if got := paths(res); len(got) != 1 || got[0] != "new.txt" {
		t.Fatalf("force push must inspect only the new commits: %v", got)
	}
}

func TestDeletionIntroducesNothing(t *testing.T) { // TEST 12
	r := newRepo(t)
	tip := r.commit("1", map[string]string{"a.dll": "x"})
	res := mustScan(t, r, Options{}, Update{Old: tip, New: zeroOf(tip), Ref: "refs/heads/old"})
	if len(res.Entries) != 0 || res.Stats.Commits != 0 {
		t.Fatalf("%+v", res)
	}
}

func TestTags(t *testing.T) { // TEST 13
	r := newRepo(t)
	base := r.commit("1", map[string]string{"a": "1"})
	r.run("checkout", "-q", "-b", "tmp")
	tip := r.commit("2", map[string]string{"bin/x.exe": "MZ"})
	r.run("checkout", "-q", "main")
	r.run("tag", "-a", "-m", "annotated", "v1", tip) // annotated tag object on a new commit
	tagObj := r.run("rev-parse", "refs/tags/v1")
	r.run("update-ref", "-d", "refs/tags/v1")
	r.run("branch", "-D", "tmp")
	r.run("update-ref", "refs/incoming/tag", tagObj)

	res := mustScan(t, r, Options{}, Update{Old: zeroOf(tip), New: tagObj, Ref: "refs/tags/v1"})
	if got := paths(res); len(got) != 1 || got[0] != "bin/x.exe" {
		t.Fatalf("annotated tag: %v", got)
	}
	// Lightweight tag on an existing commit: nothing new.
	if res := mustScan(t, r, Options{}, Update{Old: zeroOf(base), New: base, Ref: "refs/tags/v0"}); len(res.Entries) != 0 {
		t.Fatalf("lightweight tag on existing commit: %+v", res.Entries)
	}
	// Tag pointing directly at a tree and at a blob.
	r.write("t/evil.dll", "x")
	r.run("add", "t/evil.dll")
	tree := r.run("write-tree")
	blob := r.run("hash-object", "-w", filepath.Join(r.dir, "t/evil.dll"))
	res = mustScan(t, r, Options{},
		Update{Old: zeroOf(tree), New: tree, Ref: "refs/tags/tree-tag"},
		Update{Old: zeroOf(blob), New: blob, Ref: "refs/tags/blob-tag"})
	var sawTreePath, sawBlob bool
	for _, e := range res.Entries {
		sawTreePath = sawTreePath || (e.Ref == "refs/tags/tree-tag" && e.Path == "t/evil.dll")
		sawBlob = sawBlob || (e.Ref == "refs/tags/blob-tag" && e.Blob == blob && e.Path == "")
	}
	if !sawTreePath || !sawBlob {
		t.Fatalf("tree/blob tags not inspected: %+v", res.Entries)
	}
}

func TestMultipleRefsAttributionAndDedup(t *testing.T) { // TEST 14
	r := newRepo(t)
	base := r.commit("1", map[string]string{"a": "1"})
	r.run("checkout", "-q", "-b", "tmp")
	c1 := r.commit("shared", map[string]string{"shared.txt": "s"})
	c2 := r.commit("only-b", map[string]string{"b.txt": "b"})
	r.run("checkout", "-q", "main")
	r.run("branch", "-D", "tmp")
	r.run("update-ref", "refs/incoming/1", c2)
	res := mustScan(t, r, Options{},
		Update{Old: zeroOf(c1), New: c1, Ref: "refs/heads/a"},
		Update{Old: base, New: c2, Ref: "refs/heads/main"})
	if res.Stats.Commits != 2 || res.RefCommits["refs/heads/a"] != 1 || res.RefCommits["refs/heads/main"] != 1 {
		t.Fatalf("each commit scanned once and attributed to the first ref: %+v %v", res.Stats, res.RefCommits)
	}
}

func TestMerges(t *testing.T) {
	r := newRepo(t)
	base := r.commit("base", map[string]string{"a": "1"})
	r.run("checkout", "-q", "-b", "side")
	r.commit("side", map[string]string{"side.txt": "s"})
	r.run("checkout", "-q", "main")
	r.commit("main", map[string]string{"m.txt": "m"})
	// Evil merge: the merge commit itself adds a file present in no parent.
	r.run("merge", "-q", "--no-ff", "--no-commit", "side")
	r.write("merge-only.dll", "x")
	r.run("add", "merge-only.dll")
	r.run("commit", "-q", "-m", "merge")
	tip := r.head()
	r.run("update-ref", "refs/heads/main", base)
	r.run("branch", "-D", "side")
	r.run("update-ref", "refs/incoming/1", tip)
	res := mustScan(t, r, Options{}, Update{Old: base, New: tip, Ref: "refs/heads/main"})
	got := strings.Join(paths(res), ",")
	if got != "m.txt,merge-only.dll,side.txt" {
		t.Fatalf("merge handling: %s", got)
	}
}

// TestInternalRefsAreNotTrusted: a commit reachable only from a GitLab
// internal ref (e.g. a fork MR head) is still scanned when pushed or merged
// into a branch — the fork/MR bypass of PHASE-1 R1-1.
func TestInternalRefsAreNotTrusted(t *testing.T) {
	r := newRepo(t)
	base := r.commit("base", map[string]string{"a": "1"})
	r.run("checkout", "-q", "-b", "fork")
	evil := r.commit("from fork", map[string]string{"vendor/x.dll": "x"})
	r.run("checkout", "-q", "main")
	r.run("branch", "-D", "fork")
	r.run("update-ref", "refs/merge-requests/1/head", evil)
	r.run("update-ref", "refs/keep-around/"+evil, evil)
	res := mustScan(t, r, Options{}, Update{Old: base, New: evil, Ref: "refs/heads/main"})
	if got := paths(res); len(got) != 1 || got[0] != "vendor/x.dll" {
		t.Fatalf("content reachable only from internal refs must be scanned: %v", got)
	}
}

// TestPolicyClasses: pushing develop's history to a new release branch
// (stricter class) scans it; to another branch of the same class, not.
func TestPolicyClasses(t *testing.T) {
	r := newRepo(t)
	r.commit("base", map[string]string{"a": "1"})
	r.run("checkout", "-q", "-b", "develop")
	tip := r.commit("dev", map[string]string{"tool.exe": "MZ"})
	classOf := func(ref string) int {
		if strings.HasPrefix(ref, "refs/heads/release/") {
			return 1 // stricter
		}
		return 0
	}
	covers := func(a, b int) bool { return a == b || (a == 1 && b == 0) }
	o := Options{ClassOf: classOf, Covers: covers}

	res := mustScan(t, r, o, Update{Old: zeroOf(tip), New: tip, Ref: "refs/heads/release/1.0"})
	if got := paths(res); res.Stats.Commits != 2 || strings.Join(got, ",") != "a,tool.exe" {
		t.Fatalf("new release branch must scan history not vetted under the release class: %v %+v", got, res.Stats)
	}
	res = mustScan(t, r, o, Update{Old: zeroOf(tip), New: tip, Ref: "refs/heads/feature/x"})
	if res.Stats.Commits != 0 {
		t.Fatalf("same class: nothing new, got %+v", res.Stats)
	}
	// Once a release branch exists, the next one only scans what is new to the class.
	r.run("update-ref", "refs/heads/release/1.0", tip)
	next := r.commit("more", map[string]string{"n.txt": "n"})
	res = mustScan(t, r, o, Update{Old: zeroOf(next), New: next, Ref: "refs/heads/release/1.1"})
	if got := paths(res); len(got) != 1 || got[0] != "n.txt" {
		t.Fatalf("release/1.1: %v", got)
	}
}

func TestLimits(t *testing.T) {
	r := newRepo(t)
	base := r.commit("base", map[string]string{"a": "1"})
	for i := 0; i < 6; i++ {
		r.commit("c", map[string]string{"f" + string(rune('0'+i)): "content-" + string(rune('0'+i))})
	}
	tip := r.head()
	r.run("update-ref", "refs/heads/main", base)
	r.run("update-ref", "refs/incoming/1", tip)
	u := Update{Old: base, New: tip, Ref: "refs/heads/main"}

	_, err := r.scan(Options{MaxCommits: 3}, u)
	if le, ok := err.(*LimitError); !ok || le.Code != "LIMIT_COMMITS" {
		t.Fatalf("want LIMIT_COMMITS, got %v", err)
	}
	var calls []string
	res, err := r.scan(Options{MaxCommits: 3, MaxBlobs: 2, OnLimit: func(code string, n, l int) bool {
		calls = append(calls, code)
		return code == "LIMIT_COMMITS" // waive commits only
	}}, u)
	if le, ok := err.(*LimitError); !ok || le.Code != "LIMIT_OBJECTS" || res != nil {
		t.Fatalf("want LIMIT_OBJECTS after waived commit limit, got %v", err)
	}
	if strings.Join(calls, ",") != "LIMIT_COMMITS,LIMIT_OBJECTS" {
		t.Fatalf("OnLimit calls: %v", calls)
	}
}

func TestTimeout(t *testing.T) {
	r := newRepo(t)
	tip := r.commit("1", map[string]string{"a": "1"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	cls, cov := sameClass()
	_, err := Scan(ctx, Options{Git: r.git, Dir: r.dir, ClassOf: cls, Covers: cov},
		[]Update{{Old: zeroOf(tip), New: tip, Ref: "refs/heads/x"}})
	if err != context.DeadlineExceeded {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
}

func TestSpecialPathsAndGitlinks(t *testing.T) {
	r := newRepo(t)
	base := r.commit("base", map[string]string{"a": "1"})
	r.write("dir with space/fé\nline.DLL", "x")
	r.run("add", "--", "dir with space/fé\nline.DLL")
	// A gitlink (submodule pointer) is a commit id, not repository content.
	r.run("update-index", "--add", "--cacheinfo", "160000,"+base+",sub")
	r.run("commit", "-q", "-m", "odd")
	tip := r.head()
	r.run("update-ref", "refs/heads/main", base)
	r.run("update-ref", "refs/incoming/1", tip)
	res := mustScan(t, r, Options{}, Update{Old: base, New: tip, Ref: "refs/heads/main"})
	if got := paths(res); len(got) != 1 || got[0] != "dir with space/fé\nline.DLL" {
		t.Fatalf("raw path bytes must survive, gitlinks skipped: %q", got)
	}
}

func TestSHA256Repository(t *testing.T) {
	r := newRepo(t, "--object-format=sha256")
	base := r.commit("base", map[string]string{"a": "1"})
	if len(base) != 64 {
		t.Skip("sha256 repositories not supported by this git")
	}
	r.run("checkout", "-q", "-b", "tmp")
	tip := r.commit("x", map[string]string{"x.dll": "x"})
	r.run("checkout", "-q", "main")
	r.run("branch", "-D", "tmp")
	r.run("update-ref", "refs/incoming/1", tip)
	res := mustScan(t, r, Options{}, Update{Old: base, New: tip, Ref: "refs/heads/main"})
	if got := paths(res); len(got) != 1 || got[0] != "x.dll" {
		t.Fatalf("sha256: %v", got)
	}
}

func TestParseDiffTreeRejectsGarbage(t *testing.T) {
	for _, in := range []string{
		"not-a-commit\x00",
		strings.Repeat("a", 40) + "\x00:100644 100644 abc\x00path\x00",
		":100644 100644 " + strings.Repeat("a", 40) + " " + strings.Repeat("b", 40) + " M\x00p\x00", // record before commit
	} {
		if err := ParseDiffTree(strings.NewReader(in), func(string, Entry) {}); err == nil {
			t.Errorf("accepted %q", in)
		}
	}
}

func TestSizesAndHeads(t *testing.T) {
	r := newRepo(t)
	r.commit("c", map[string]string{"small.txt": "hello", "big.bin": strings.Repeat("A", 200000)})
	small := r.run("rev-parse", "HEAD:small.txt")
	big := r.run("rev-parse", "HEAD:big.bin")
	sizes, err := Sizes(context.Background(), r.git, r.dir, []string{small, big, small})
	if err != nil || sizes[small] != 5 || sizes[big] != 200000 {
		t.Fatalf("sizes %v %v", sizes, err)
	}
	heads, err := Heads(context.Background(), r.git, r.dir, []string{big, small}, 16)
	if err != nil || string(heads[small]) != "hello" || string(heads[big]) != strings.Repeat("A", 16) {
		t.Fatalf("heads %q %v", heads, err)
	}
	if _, err := Sizes(context.Background(), r.git, r.dir, []string{strings.Repeat("e", 40)}); err == nil {
		t.Fatal("missing object must be an error (fail closed)")
	}
	tree := r.run("rev-parse", "HEAD^{tree}")
	if _, err := Sizes(context.Background(), r.git, r.dir, []string{tree}); err == nil {
		t.Fatal("non-blob must be an error")
	}
}
