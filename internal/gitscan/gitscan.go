// Package gitscan finds everything a push introduces, using Git plumbing only
// (PHASE-1 §7). It never checks out, clones or writes to the repository.
//
// Anti-bypass guarantees:
//   - every new commit is inspected, not only the tip: a file added in commit
//     A and deleted in commit B is still found (in A);
//   - paths come from per-commit diffs, so renaming an existing blob to
//     evil.dll is found although no new blob exists;
//   - merges are diffed combined (-c): content introduced by the merge itself
//     is found, content inherited from a parent is found in that parent's
//     own (new) commits;
//   - "new" is relative to existing refs/heads and refs/tags of an equal or
//     stricter policy class only — never refs/merge-requests/*, keep-around or
//     other GitLab-internal refs (fork MR bypass), and never refs of a laxer
//     branch class (develop -> release bypass);
//   - tags pointing directly at trees or blobs are inspected too.
//
// The Gitaly quarantine environment (GIT_OBJECT_DIRECTORY,
// GIT_ALTERNATE_OBJECT_DIRECTORIES, GIT_QUARANTINE_PATH) is inherited
// untouched by every git child process, so new objects are visible before
// they are accepted.
package gitscan

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// Update is one ref update of the push.
type Update struct {
	Old, New, Ref string
}

// Entry is one path (or bare blob) introduced by the push.
type Entry struct {
	Ref    string // first updated ref through which it was reached
	Commit string // introducing commit ("" for tags pointing at a tree/blob)
	Path   string // repository path, raw bytes ("" for a tag pointing at a blob)
	Blob   string // new blob object id
	Mode   string // new file mode (100644, 100755, 120000)
}

// Stats summarises the work done.
type Stats struct {
	Commits    int // new commits inspected
	Blobs      int // unique new-side blobs
	Entries    int
	Exclusions int // existing ref tips used as the "already vetted" boundary
}

// Result of a scan.
type Result struct {
	Entries    []Entry
	Stats      Stats
	RefCommits map[string]int // new commits attributed to each ref
}

// LimitError reports an exceeded, unwaived limit.
type LimitError struct {
	Code  string // LIMIT_COMMITS | LIMIT_OBJECTS
	N     int
	Limit int
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("%s: more than %d (limit %d)", e.Code, e.N, e.Limit)
}

// Options configure a scan.
type Options struct {
	Git string // git executable
	Dir string // repository directory ("" = current directory, as in a hook)

	MaxCommits int
	MaxBlobs   int
	// OnLimit is called once per exceeded limit; returning true (waived by an
	// exception) continues the scan without that limit.
	OnLimit func(code string, n, limit int) bool

	// ClassOf maps a ref to its policy class; Covers(existing, target)
	// reports whether content vetted under class `existing` is vetted for
	// class `target` (existing is at least as strict).
	ClassOf func(ref string) int
	Covers  func(existing, target int) bool
}

var hexRe = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func isZero(oid string) bool { return strings.Trim(oid, "0") == "" }

// FindGit locates the git executable: PATH first (the Gitaly hook
// environment), then the Omnibus location.
func FindGit() (string, error) {
	if p, err := exec.LookPath("git"); err == nil {
		return p, nil
	}
	const omnibus = "/opt/gitlab/embedded/bin/git"
	if st, err := os.Stat(omnibus); err == nil && st.Mode().IsRegular() {
		return omnibus, nil
	}
	return "", errors.New("git executable not found in PATH or /opt/gitlab/embedded/bin")
}

type scanner struct {
	ctx            context.Context
	o              Options
	res            *Result
	seen           map[string]bool // commits already collected
	byC            map[string]string
	list           []string // commits in discovery order
	commitLimitHit bool
}

// Scan inspects all objects introduced by updates.
func Scan(ctx context.Context, o Options, updates []Update) (*Result, error) {
	s := &scanner{ctx: ctx, o: o, res: &Result{RefCommits: map[string]int{}},
		seen: map[string]bool{}, byC: map[string]string{}}

	var live []Update
	for _, u := range updates {
		if !hexRe.MatchString(u.New) || !hexRe.MatchString(u.Old) {
			return nil, fmt.Errorf("invalid object id in update of %q", u.Ref)
		}
		if !isZero(u.New) { // deletions introduce nothing
			live = append(live, u)
		}
	}
	if len(live) == 0 {
		return s.res, nil
	}

	kinds, err := s.peel(live)
	if err != nil {
		return nil, err
	}
	var commitTips []Update
	for i, u := range live {
		switch kinds[i].typ {
		case "commit":
			commitTips = append(commitTips, Update{Old: u.Old, New: kinds[i].oid, Ref: u.Ref})
		case "tree":
			if err := s.tree(u.Ref, kinds[i].oid); err != nil {
				return nil, err
			}
		case "blob":
			s.res.Entries = append(s.res.Entries, Entry{Ref: u.Ref, Blob: kinds[i].oid, Mode: "100644"})
		default:
			return nil, fmt.Errorf("ref %q points to unsupported object type %q", u.Ref, kinds[i].typ)
		}
	}

	if len(commitTips) > 0 {
		existing, err := s.existingRefs()
		if err != nil {
			return nil, err
		}
		var done []Update
		for _, u := range commitTips {
			excl := s.exclusions(u.Ref, existing, done)
			s.res.Stats.Exclusions += len(excl)
			if err := s.revList(u, excl); err != nil {
				return nil, err
			}
			done = append(done, u)
		}
		if err := s.diffTree(); err != nil {
			return nil, err
		}
	}

	blobs := map[string]bool{}
	for _, e := range s.res.Entries {
		blobs[e.Blob] = true
	}
	s.res.Stats.Blobs = len(blobs)
	s.res.Stats.Entries = len(s.res.Entries)
	if s.o.MaxBlobs > 0 && len(blobs) > s.o.MaxBlobs {
		if s.o.OnLimit == nil || !s.o.OnLimit("LIMIT_OBJECTS", len(blobs), s.o.MaxBlobs) {
			return nil, &LimitError{"LIMIT_OBJECTS", len(blobs), s.o.MaxBlobs}
		}
	}
	return s.res, nil
}

// command builds a git invocation. The environment is inherited unchanged
// (quarantine!); only the locale is pinned for stable output.
func (s *scanner) command(args ...string) *exec.Cmd {
	cmd := exec.CommandContext(s.ctx, s.o.Git, args...)
	cmd.Dir = s.o.Dir
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
	return cmd
}

func (s *scanner) wrap(what string, err error, stderr *bytes.Buffer) error {
	if ctxErr := s.ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	msg := strings.TrimSpace(stderr.String())
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return fmt.Errorf("git %s: %v: %s", what, err, msg)
}

type kind struct{ oid, typ string }

// peel resolves each new tip through annotated tags to its final object.
func (s *scanner) peel(us []Update) ([]kind, error) {
	var in bytes.Buffer
	for _, u := range us {
		fmt.Fprintf(&in, "%s^{}\n", u.New)
	}
	var out, errb bytes.Buffer
	cmd := s.command("cat-file", "--batch-check=%(objectname) %(objecttype)")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = &in, &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, s.wrap("cat-file", err, &errb)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != len(us) {
		return nil, fmt.Errorf("git cat-file: expected %d results, got %d", len(us), len(lines))
	}
	ks := make([]kind, len(us))
	for i, l := range lines {
		f := strings.Fields(l)
		if len(f) != 2 || !hexRe.MatchString(f[0]) {
			return nil, fmt.Errorf("object %s of %q is missing or unreadable", us[i].New, us[i].Ref)
		}
		ks[i] = kind{f[0], f[1]}
	}
	return ks, nil
}

type refTip struct{ ref, commit string }

// existingRefs lists branch and tag tips (peeled to commits). Only these are
// trusted as "already vetted"; GitLab-internal refs are deliberately ignored.
func (s *scanner) existingRefs() ([]refTip, error) {
	var out, errb bytes.Buffer
	cmd := s.command("for-each-ref", "--format=%(objectname) %(objecttype) %(*objectname) %(*objecttype) %(refname)", "refs/heads/", "refs/tags/")
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, s.wrap("for-each-ref", err, &errb)
	}
	var tips []refTip
	for _, l := range strings.Split(out.String(), "\n") {
		f := strings.SplitN(l, " ", 5)
		if len(f) != 5 {
			continue
		}
		switch {
		case f[1] == "commit":
			tips = append(tips, refTip{f[4], f[0]})
		case f[1] == "tag" && f[3] == "commit":
			tips = append(tips, refTip{f[4], f[2]})
		}
	}
	return tips, nil
}

// exclusions returns the commits whose history counts as vetted for ref:
// existing refs of a class covering ref's class, and tips of this push
// already scanned under a covering class.
func (s *scanner) exclusions(ref string, existing []refTip, done []Update) []string {
	target := s.o.ClassOf(ref)
	set := map[string]bool{}
	for _, t := range existing {
		if s.o.Covers(s.o.ClassOf(t.ref), target) {
			set[t.commit] = true
		}
	}
	for _, d := range done {
		if s.o.Covers(s.o.ClassOf(d.Ref), target) {
			set[d.New] = true
		}
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	return out
}

// revList collects commits reachable from u.New and not from excl.
func (s *scanner) revList(u Update, excl []string) error {
	var in bytes.Buffer
	fmt.Fprintf(&in, "%s\n", u.New)
	for _, c := range excl {
		fmt.Fprintf(&in, "^%s\n", c)
	}
	var errb bytes.Buffer
	cmd := s.command("rev-list", "--stdin")
	cmd.Stdin, cmd.Stderr = &in, &errb
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(stdout)
	var limitErr error
	for sc.Scan() {
		c := sc.Text()
		if !hexRe.MatchString(c) {
			limitErr = fmt.Errorf("git rev-list: unexpected output %q", c)
			break
		}
		if s.seen[c] {
			continue
		}
		s.seen[c] = true
		s.byC[c] = u.Ref
		s.list = append(s.list, c)
		s.res.RefCommits[u.Ref]++
		s.res.Stats.Commits++
		if s.o.MaxCommits > 0 && s.res.Stats.Commits > s.o.MaxCommits && !s.commitLimitHit {
			s.commitLimitHit = true
			if s.o.OnLimit == nil || !s.o.OnLimit("LIMIT_COMMITS", s.res.Stats.Commits, s.o.MaxCommits) {
				limitErr = &LimitError{"LIMIT_COMMITS", s.res.Stats.Commits, s.o.MaxCommits}
				break
			}
		}
	}
	if limitErr != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return limitErr
	}
	if err := cmd.Wait(); err != nil {
		return s.wrap("rev-list", err, &errb)
	}
	return sc.Err()
}

// diffTree lists added/modified paths of every new commit in one process.
func (s *scanner) diffTree() error {
	if len(s.list) == 0 {
		return nil
	}
	var in bytes.Buffer
	for _, c := range s.list {
		in.WriteString(c + "\n")
	}
	var errb bytes.Buffer
	cmd := s.command("diff-tree", "--stdin", "-r", "-z", "--raw", "--root", "--no-renames", "-c", "--no-ext-diff", "--no-textconv")
	cmd.Stdin, cmd.Stderr = &in, &errb
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	perr := ParseDiffTree(stdout, func(commit string, e Entry) {
		e.Commit, e.Ref = commit, s.byC[commit]
		s.res.Entries = append(s.res.Entries, e)
	})
	if perr != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return perr
	}
	if err := cmd.Wait(); err != nil {
		return s.wrap("diff-tree", err, &errb)
	}
	return nil
}

// tree lists every blob of a tree a tag points to directly.
func (s *scanner) tree(ref, oid string) error {
	var out, errb bytes.Buffer
	cmd := s.command("ls-tree", "-r", "-z", "--full-tree", oid)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return s.wrap("ls-tree", err, &errb)
	}
	for _, rec := range bytes.Split(out.Bytes(), []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		meta, path, ok := bytes.Cut(rec, []byte{'\t'})
		f := strings.Fields(string(meta))
		if !ok || len(f) != 3 || f[1] != "blob" {
			continue // sub-trees are recursed (-r); gitlinks are commits, not content
		}
		s.res.Entries = append(s.res.Entries, Entry{Ref: ref, Path: string(path), Blob: f[2], Mode: f[0]})
	}
	return nil
}

// ParseDiffTree parses `git diff-tree --stdin -r -z --raw -c` output and
// calls emit for every path whose new side is a blob (added, modified or
// type-changed). Deletions and gitlinks are skipped.
//
// Record formats (NUL-separated tokens):
//
//	<commit-oid>
//	:<old-mode> <new-mode> <old-oid> <new-oid> <status> , <path>
//	::<p1-mode> <p2-mode> <new-mode> <p1-oid> <p2-oid> <new-oid> <statuses> , <path>   (merges, -c)
func ParseDiffTree(r io.Reader, emit func(commit string, e Entry)) error {
	br := bufio.NewReaderSize(r, 64<<10)
	next := func() (string, error) {
		tok, err := br.ReadString(0)
		if err == io.EOF && tok == "" {
			return "", io.EOF
		}
		if err != nil && err != io.EOF {
			return "", err
		}
		return strings.TrimSuffix(tok, "\x00"), nil
	}
	commit := ""
	for {
		tok, err := next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		tok = strings.TrimPrefix(tok, "\n")
		if tok == "" {
			continue
		}
		if !strings.HasPrefix(tok, ":") {
			if !hexRe.MatchString(tok) {
				return fmt.Errorf("diff-tree: unexpected token %q", truncate(tok))
			}
			commit = tok
			continue
		}
		n := len(tok) - len(strings.TrimLeft(tok, ":")) // number of parents
		f := strings.Fields(tok[n:])
		// n+1 modes, n+1 oids, 1 status field
		if len(f) != 2*(n+1)+1 {
			return fmt.Errorf("diff-tree: malformed record %q", truncate(tok))
		}
		newMode, newOID, status := f[n], f[2*n+1], f[2*n+2]
		path, err := next()
		if err != nil {
			return fmt.Errorf("diff-tree: missing path: %v", err)
		}
		if commit == "" {
			return errors.New("diff-tree: record before commit id")
		}
		if isZero(newOID) || newMode == "000000" || strings.HasPrefix(status, "D") && n == 1 {
			continue // deleted
		}
		if newMode == "160000" {
			continue // gitlink (submodule commit), not repository content
		}
		emit(commit, Entry{Path: path, Blob: newOID, Mode: newMode})
	}
}

func truncate(s string) string {
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}
