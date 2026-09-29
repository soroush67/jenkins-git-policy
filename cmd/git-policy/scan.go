package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/soroush67/git-policy/internal/engine"
	"github.com/soroush67/git-policy/internal/hook"
	"github.com/soroush67/git-policy/internal/layout"
	"github.com/soroush67/git-policy/internal/policy"
	"github.com/soroush67/git-policy/internal/store"
)

// cmdScan runs the anti-bypass traversal for ref updates read from stdin
// ("<old> <new> <ref>" lines, as pre-receive receives them) and prints what
// the push introduces. It is a diagnostic tool: run it inside a repository
// (or from a test pre-receive hook, where the quarantine environment applies).
func cmdScan(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", layout.DefaultRoot, "installation root (active policy)")
	policyFile := fs.String("policy", "", "use this policy file instead of the active one")
	project := fs.String("project", os.Getenv("GL_PROJECT_PATH"), "project path (default: $GL_PROJECT_PATH)")
	user := fs.String("user", os.Getenv("GL_USERNAME"), "username (default: $GL_USERNAME)")
	dir := fs.String("repo", "", "repository directory (default: current directory)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: git-policy scan [--policy FILE] [--project PATH] [--repo DIR] [--json] < ref-updates")
		return exitUsage
	}
	var pol *policy.Compiled
	if *policyFile != "" {
		src, err := readPolicy(*policyFile, nil)
		if err != nil {
			fmt.Fprintf(stderr, "git-policy: %v\n", err)
			return exitUsage
		}
		c, findings := policy.Build(src, policy.Options{})
		if c == nil {
			for _, f := range findings {
				fmt.Fprintf(stderr, "  %s\n", f)
			}
			return exitInvalid
		}
		pol = c
	} else {
		act, err := store.LoadActive(layout.New(*root))
		if err != nil {
			fmt.Fprintf(stderr, "git-policy: no active policy: %v (use --policy FILE)\n", err)
			return exitInvalid
		}
		pol = act.Policy
	}
	ups, err := hook.ParseUpdates(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "git-policy: %v\n", err)
		return exitUsage
	}
	req := engine.Request{User: *user, Project: *project, Repository: os.Getenv("GL_REPOSITORY"), Now: time.Now(),
		Membership: engine.Membership{Available: true, GrantsUsable: true, State: "not used by scan"}}
	for _, u := range ups {
		req.Updates = append(req.Updates, engine.RefUpdate{Old: u.Old, New: u.New, Ref: u.Ref})
	}
	eng := engine.New(pol)
	d := &engine.Decision{User: engine.NormalizeUser(*user), RepoType: engine.RepoType(req.Repository)}
	d.Project = engine.NormalizeProject(*project, d.RepoType)
	d.RepoMode = pol.Settings.RepositoryTypes[d.RepoType]
	if d.RepoMode == "" {
		d.RepoMode = policy.RepoMandatoryOnly
	}
	out := hook.ScanPush(eng, d, req, *dir)

	type report struct {
		Skipped    bool               `json:"skipped"`
		SkipReason string             `json:"skip_reason,omitempty"`
		DurationMS int64              `json:"duration_ms"`
		Stats      any                `json:"stats,omitempty"`
		RefCommits map[string]int     `json:"ref_commits,omitempty"`
		Entries    any                `json:"entries"`
		Violations []engine.Violation `json:"violations"`
	}
	r := report{Skipped: out.Skipped, SkipReason: out.SkipReason, DurationMS: out.Duration.Milliseconds(),
		Violations: d.Violations, Entries: []any{}}
	if out.Result != nil {
		r.Stats, r.RefCommits = out.Result.Stats, out.Result.RefCommits
		if out.Result.Entries != nil {
			r.Entries = out.Result.Entries
		}
	}
	if r.Violations == nil {
		r.Violations = []engine.Violation{}
	}
	if *asJSON {
		writeJSON(stdout, r)
	} else {
		if out.Skipped {
			fmt.Fprintf(stdout, "scan skipped: %s\n", out.SkipReason)
		}
		if res := out.Result; res != nil {
			fmt.Fprintf(stdout, "commits=%d blobs=%d entries=%d exclusion-tips=%d duration=%s\n",
				res.Stats.Commits, res.Stats.Blobs, res.Stats.Entries, res.Stats.Exclusions, out.Duration.Round(time.Millisecond))
			for _, e := range res.Entries {
				c := e.Commit
				if len(c) > 12 {
					c = c[:12]
				}
				fmt.Fprintf(stdout, "  %-30s %-12s %s %q\n", e.Ref, c, e.Mode, e.Path)
			}
		}
		for _, v := range d.Violations {
			fmt.Fprintf(stdout, "VIOLATION %s: %s\n", v.Code, v.Detail)
		}
	}
	if len(d.Violations) > 0 {
		return exitInvalid
	}
	return exitOK
}
