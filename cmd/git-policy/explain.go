package main

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/soroush67/git-policy/internal/engine"
	"github.com/soroush67/git-policy/internal/layout"
	"github.com/soroush67/git-policy/internal/membership"
	"github.com/soroush67/git-policy/internal/policy"
	"github.com/soroush67/git-policy/internal/store"
)

// explainResult is the machine-readable output of `git-policy explain`.
type explainResult struct {
	PolicySource string                 `json:"policy_source"`
	User         string                 `json:"user"`
	Groups       []string               `json:"groups"`
	GroupsFrom   string                 `json:"groups_from"`
	Project      string                 `json:"project"`
	Ref          string                 `json:"ref"`
	RepoType     string                 `json:"repository_type"`
	RepoMode     string                 `json:"repository_mode"`
	Identity     engine.IdentityTrace   `json:"identity"`
	Waived       []engine.Waiver        `json:"waived"`
	Violations   []engine.Violation     `json:"violations"`
	Verdict      string                 `json:"verdict"`
	Content      engine.Content         `json:"content"`
	Membership   map[string]interface{} `json:"membership"`
}

// cmdExplain shows how the policy decides a hypothetical push, without
// pushing. Used by operators and by the Jenkins EXPLAIN action.
func cmdExplain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("explain", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", layout.DefaultRoot, "installation root (active policy and membership cache)")
	policyFile := fs.String("policy", "", "explain against this policy file instead of the active one")
	user := fs.String("user", "", "GitLab username (empty = @unknown)")
	groupsFlag := fs.String("groups", "", "comma-separated group paths (default: from the membership cache)")
	project := fs.String("project", "", "project path, e.g. finance/payment-api")
	ref := fs.String("ref", "refs/heads/main", "full ref name")
	repo := fs.String("repository", "", "GL_REPOSITORY value (project-N, wiki-N, ...); empty = project")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *project == "" {
		fmt.Fprintln(stderr, "usage: git-policy explain --project PATH [--user NAME] [--groups a,b] [--ref REF] [--policy FILE] [--json]")
		return exitUsage
	}
	l := layout.New(*root)
	now := time.Now()

	var pol *policy.Compiled
	res := explainResult{User: *user, Ref: *ref}
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
			fmt.Fprintln(stderr, "git-policy: policy is invalid")
			return exitInvalid
		}
		pol, res.PolicySource = c, *policyFile
	} else {
		act, err := store.LoadActive(l)
		if err != nil {
			fmt.Fprintf(stderr, "git-policy: no active policy: %v (use --policy FILE)\n", err)
			return exitInvalid
		}
		pol, res.PolicySource = act.Policy, "active version "+layout.VersionName(act.Version)
	}

	mv := engine.Membership{Available: true, GrantsUsable: true, State: "given"}
	if *groupsFlag != "" {
		for _, g := range strings.Split(*groupsFlag, ",") {
			if g = strings.TrimSpace(g); g != "" {
				mv.Groups = append(mv.Groups, strings.ToLower(g))
			}
		}
		res.GroupsFrom = "--groups"
	} else {
		set := pol.Settings.Membership
		mem := membership.Load(l, time.Duration(set.SoftMaxAgeSeconds)*time.Second, time.Duration(set.HardMaxAgeSeconds)*time.Second, now)
		mv = engine.Membership{Available: mem.Available(), GrantsUsable: mem.GrantsUsable(),
			Groups: mem.GroupsOf(*user), State: string(mem.Freshness)}
		res.GroupsFrom = "membership cache (" + string(mem.Freshness) + ")"
	}
	res.Groups = mv.Groups
	res.Membership = map[string]interface{}{"available": mv.Available, "grants_usable": mv.GrantsUsable, "state": mv.State}

	eng := engine.New(pol)
	d := eng.Evaluate(engine.Request{
		User: *user, Project: *project, Repository: *repo, Now: now, Membership: mv,
		Updates: []engine.RefUpdate{{Old: strings.Repeat("0", 40), New: strings.Repeat("1", 40), Ref: *ref}},
	})
	res.User, res.Project, res.RepoType, res.RepoMode = d.User, d.Project, d.RepoType, d.RepoMode
	res.Identity = eng.TraceIdentity(d.User, mv.Groups, mv.GrantsUsable, d.Project, *ref, d.RepoMode)
	res.Waived, res.Violations = d.Waived, d.Violations
	res.Content = eng.ResolveContent(d.Project, *ref, d.RepoMode)
	res.Verdict = "ALLOW"
	if d.Rejected() {
		res.Verdict = "REJECT"
	}
	if d.RepoMode == policy.RepoNone {
		res.Verdict = "ALLOW (repository type not evaluated)"
	}

	if *asJSON {
		writeJSON(stdout, res)
	} else {
		printExplain(stdout, res)
	}
	return exitOK
}

func printExplain(w io.Writer, r explainResult) {
	p := func(f string, a ...any) { fmt.Fprintf(w, f+"\n", a...) }
	p("Policy:      %s", r.PolicySource)
	p("Push:        user=%s project=%s ref=%s", r.User, r.Project, r.Ref)
	p("Repository:  type=%s mode=%s", r.RepoType, r.RepoMode)
	groups := strings.Join(r.Groups, ", ")
	if groups == "" {
		groups = "-"
	}
	p("Groups:      %s  [%s]", groups, r.GroupsFrom)

	p("\nIdentity")
	t := r.Identity
	if t.Mandatory != "" {
		p("  mandatory deny matched: %s (%s)", t.Mandatory, t.Source)
	}
	if len(t.Candidates) == 0 && t.Mandatory == "" {
		p("  no identity rule matches -> allowed (GitLab authorization applies)")
	}
	for _, c := range t.Candidates {
		kind := "group"
		if c.IsUser {
			kind = "user "
		}
		mark := "  "
		for _, dc := range t.Deciding {
			if dc == c {
				mark = "=>"
			}
		}
		p("  %s %-24s %s %-5s %s", mark, engine.LevelName(c.Level), kind, c.Action, c.Source)
	}
	for _, c := range t.Ignored {
		p("     ignored (membership cache expired): group allow %s", c.Source)
	}
	for _, wv := range r.Waived {
		p("  waived by exception %s: %s (%s)", wv.ExceptionID, wv.Code, wv.Source)
	}

	p("\nVerdict:     %s", r.Verdict)
	for _, v := range r.Violations {
		p("  %s  (%s)", v.Code, v.Source)
	}

	c := r.Content
	p("\nEffective content rules (enforced from Phases 7-8)")
	if !c.Enabled {
		p("  scoped rules disabled for this project/repository: mandatory rules only")
	}
	list := func(name string, m map[string]engine.Entry) {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			src := m[k].Source
			if m[k].Mandatory {
				src = "MANDATORY"
			}
			parts = append(parts, k+" ("+src+")")
		}
		if len(parts) == 0 {
			parts = []string{"-"}
		}
		p("  %-19s %s", name+":", strings.Join(parts, ", "))
	}
	list("blocked extensions", c.Extensions)
	list("blocked paths", c.Paths)
	list("blocked signatures", c.Signatures)
	size := "unlimited"
	if c.MaxFileSize > 0 {
		size = policy.FormatSize(c.MaxFileSize) + " (" + c.SizeSource + ")"
	}
	p("  %-19s %s", "max file size:", size)
	p("  %-19s %s (%s); mandatory rules: %s", "mode:", c.Mode, c.ModeSource, c.MandatoryMode)
}
