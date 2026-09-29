package engine

import (
	"fmt"
	"sort"

	"github.com/soroush67/git-policy/internal/policy"
)

// Specificity levels (PHASE-2 §3.2). Scope dominates; a ref-bound rule ranks
// just above the unbound rule of the same scope.
const (
	levelGlobal  = 0
	levelProject = 2000
)

func namespaceLevel(depth int) int { return 2 * depth }

// Candidate is one identity rule that matched the push.
type Candidate struct {
	Level   int
	IsUser  bool
	Subject string // user name or group path
	Action  string // allow | deny
	Source  string // policy location
}

// IdentityTrace explains an identity decision (used by explain and tests).
type IdentityTrace struct {
	Mandatory  string      // mandatory rule that denied, if any
	Candidates []Candidate // all matching scoped rules, most specific first
	Deciding   []Candidate // the rules at the deciding level after user>group
	Decision   string      // allow | deny | none (no rule matched => allow)
	Code       string      // rule code when denied
	Source     string      // deciding rule
	Ignored    []Candidate // group allows ignored because the cache expired
}

// identity evaluates mandatory and scoped identity rules for one ref update.
func (e *Engine) identity(d *Decision, r Request, groups []string, u RefUpdate) {
	t := e.TraceIdentity(d.User, groups, r.Membership.GrantsUsable, d.Project, u.Ref, d.RepoMode)
	if t.Decision != "deny" {
		return
	}
	v := Violation{Code: t.Code, Ref: u.Ref, Old: u.Old, New: u.New, Source: t.Source,
		Mandatory: t.Code == policy.MandatoryUserDenied || t.Code == policy.MandatoryGroupDenied}
	if e.violate(d, r, groups, v) && v.Mandatory {
		// A waived mandatory deny still leaves the scoped rules to decide.
		t = e.TraceIdentity(d.User, groups, r.Membership.GrantsUsable, d.Project, u.Ref, d.RepoMode, skipMandatory)
		if t.Decision == "deny" {
			e.violate(d, r, groups, Violation{Code: t.Code, Ref: u.Ref, Old: u.Old, New: u.New, Source: t.Source})
		}
	}
}

type traceOpt int

const skipMandatory traceOpt = 1

// TraceIdentity resolves the identity decision for user/groups on project/ref.
func (e *Engine) TraceIdentity(user string, groups []string, grantsUsable bool, project, ref, repoMode string, opts ...traceOpt) IdentityTrace {
	t := IdentityTrace{Decision: "none"}
	skip := len(opts) > 0 && opts[0] == skipMandatory

	if !skip {
		if contains(e.P.Mandatory.DenyUsers, user) {
			t.Mandatory, t.Decision, t.Code, t.Source = user, "deny", policy.MandatoryUserDenied, "mandatory.deny_users"
			return t
		}
		for _, g := range groups {
			if contains(e.P.Mandatory.DenyGroups, g) {
				t.Mandatory, t.Decision, t.Code, t.Source = g, "deny", policy.MandatoryGroupDenied, "mandatory.deny_groups"
				return t
			}
		}
	}
	if repoMode == policy.RepoMandatoryOnly {
		return t
	}

	var cands []Candidate
	add := func(isUser bool, subject string, id policy.Identity) {
		base := "users[" + quote(subject) + "]"
		if !isUser {
			base = "groups[" + quote(subject) + "]"
		}
		push := func(level int, action, source string) {
			if action == "" {
				return
			}
			c := Candidate{Level: level, IsUser: isUser, Subject: subject, Action: action, Source: source}
			if !isUser && action == "allow" && !grantsUsable {
				t.Ignored = append(t.Ignored, c) // stale cache may only restrict
				return
			}
			cands = append(cands, c)
		}
		refs := func(level int, m map[string]string, src string) {
			for glob, action := range m {
				if e.match(glob, ref, false) {
					push(level+1, action, src+".refs["+quote(glob)+"]")
				}
			}
		}
		push(levelGlobal, id.Push, base+".push")
		refs(levelGlobal, id.Refs, base)
		for i, ns := range namespaces(project) {
			if s, ok := id.Namespaces[ns]; ok {
				src := base + ".namespaces[" + quote(ns) + "]"
				push(namespaceLevel(i+1), s.Push, src+".push")
				refs(namespaceLevel(i+1), s.Refs, src)
			}
		}
		if s, ok := id.Projects[project]; ok {
			src := base + ".projects[" + quote(project) + "]"
			push(levelProject, s.Push, src+".push")
			refs(levelProject, s.Refs, src)
		}
	}
	if id, ok := e.P.Users[user]; ok {
		add(true, user, id)
	}
	for _, g := range groups {
		if id, ok := e.P.Groups[g]; ok {
			add(false, g, id)
		}
	}
	if len(cands) == 0 {
		return t
	}
	sort.Slice(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.Level != b.Level {
			return a.Level > b.Level
		}
		if a.IsUser != b.IsUser {
			return a.IsUser
		}
		if a.Action != b.Action {
			return a.Action == "deny"
		}
		return a.Source < b.Source
	})
	t.Candidates = cands

	top := cands[0].Level
	userAtTop := cands[0].IsUser
	for _, c := range cands {
		if c.Level == top && c.IsUser == userAtTop {
			t.Deciding = append(t.Deciding, c)
		}
	}
	// Sorted: deny first within the deciding set, then by source.
	decider := t.Deciding[0]
	t.Decision, t.Source = decider.Action, decider.Source
	if decider.Action == "deny" {
		t.Code = policy.GroupPushDenied
		if decider.IsUser {
			t.Code = policy.UserPushDenied
		}
	}
	return t
}

// LevelName renders a specificity level for humans.
func LevelName(level int) string {
	switch {
	case level == levelGlobal:
		return "global"
	case level == levelGlobal+1:
		return "global+ref"
	case level == levelProject:
		return "project"
	case level == levelProject+1:
		return "project+ref"
	case level%2 == 0:
		return fmt.Sprintf("namespace(depth %d)", level/2)
	}
	return fmt.Sprintf("namespace(depth %d)+ref", level/2)
}
