// Package engine evaluates a push against a compiled policy.
//
// Phase 5 scope: repository scope, ref-update limit, identity (mandatory and
// scoped user/group rules, PHASE-2 §3.2), exceptions (§3.4) and resolution of
// the effective content rules per project/ref (§3.3). Content inspection
// itself (traversal, extensions, sizes) is added in Phases 6-8.
package engine

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/soroush67/git-policy/internal/policy"
)

// RefUpdate is one ref update of the push.
type RefUpdate struct {
	Old, New, Ref string
}

// Membership is the pushing user's group view, derived from the local cache.
type Membership struct {
	Available    bool     // any cache data loaded
	GrantsUsable bool     // cache young enough to honour group allows / exceptions
	Groups       []string // lower-cased full group paths of the pushing user
	State        string   // fresh | stale | expired | unavailable (for audit)
}

// Request is everything the engine needs about one push.
type Request struct {
	User       string // GL_USERNAME ("" => @unknown)
	Project    string // GL_PROJECT_PATH
	Repository string // GL_REPOSITORY (project-N, wiki-N, ...)
	Updates    []RefUpdate
	Membership Membership
	Now        time.Time
}

// Violation is one broken rule. Source names the policy entry (internal: for
// audit and explain, never shown to developers).
type Violation struct {
	Code      string
	Ref       string
	Old, New  string
	Path      string // content rules (Phase 7+)
	Size      int64  // FILE_TOO_LARGE (Phase 8)
	Mandatory bool
	Source    string
	Detail    string
}

// Waiver is a violation an exception waived.
type Waiver struct {
	Violation
	ExceptionID string
}

// Decision is the result of Evaluate.
type Decision struct {
	User       string // normalised subject (@unknown when empty)
	Project    string // normalised project path
	RepoType   string // project | wiki | snippet | design | unknown
	RepoMode   string // all | mandatory_only | none
	Membership string
	Violations []Violation // enforced: the push is rejected
	Waived     []Waiver
}

// Rejected reports whether the push must be rejected.
func (d *Decision) Rejected() bool { return len(d.Violations) > 0 }

// Engine evaluates pushes against one compiled policy.
type Engine struct {
	P            *policy.Compiled
	globs        map[string]*regexp.Regexp
	hasGroupDeny bool
}

// New prepares an engine for a compiled policy.
func New(p *policy.Compiled) *Engine {
	e := &Engine{P: p, globs: map[string]*regexp.Regexp{}}
	e.hasGroupDeny = len(p.Mandatory.DenyGroups) > 0
	for _, id := range p.Groups {
		if identityHasDeny(id) {
			e.hasGroupDeny = true
		}
	}
	return e
}

// NeedsMembership reports whether pushes are rejected with
// MEMBERSHIP_UNAVAILABLE when no membership cache is loadable.
func (e *Engine) NeedsMembership() bool {
	return e.hasGroupDeny && e.P.Settings.Membership.OnUnavailable == policy.OnUnavailableDeny
}

func identityHasDeny(id policy.Identity) bool {
	if id.Push == "deny" {
		return true
	}
	for _, a := range id.Refs {
		if a == "deny" {
			return true
		}
	}
	for _, s := range id.Namespaces {
		if scopedHasDeny(s) {
			return true
		}
	}
	for _, s := range id.Projects {
		if scopedHasDeny(s) {
			return true
		}
	}
	return false
}

func scopedHasDeny(s policy.ScopedIdentityC) bool {
	if s.Push == "deny" {
		return true
	}
	for _, a := range s.Refs {
		if a == "deny" {
			return true
		}
	}
	return false
}

// match tests s against a glob (compiled once per engine; the validator
// guarantees syntax, so a compile error simply never matches).
func (e *Engine) match(glob, s string, fold bool) bool {
	key := strconv.FormatBool(fold) + "\x00" + glob
	re, ok := e.globs[key]
	if !ok {
		re, _ = policy.CompileGlob(glob, fold)
		e.globs[key] = re
	}
	return re != nil && re.MatchString(s)
}

// RepoType derives the repository type from GL_REPOSITORY.
func RepoType(glRepository string) string {
	switch {
	case glRepository == "", strings.HasPrefix(glRepository, "project-"):
		return "project"
	case strings.HasPrefix(glRepository, "wiki-"), strings.HasSuffix(glRepository, "-wiki"):
		return "wiki"
	case strings.HasPrefix(glRepository, "snippet-"):
		return "snippet"
	case strings.HasPrefix(glRepository, "design-"):
		return "design"
	}
	return "unknown"
}

// NormalizeProject case-folds GL_PROJECT_PATH (GitLab paths are
// case-insensitive) and maps a wiki repository to its project.
func NormalizeProject(path, repoType string) string {
	p := strings.Trim(strings.ToLower(path), "/")
	if repoType == "wiki" {
		p = strings.TrimSuffix(p, ".wiki")
	}
	return p
}

// NormalizeUser case-folds GL_USERNAME; empty becomes @unknown.
func NormalizeUser(u string) string {
	if u == "" {
		return policy.UnknownUser
	}
	return strings.ToLower(u)
}

// namespaces returns the namespace chain of a project, least specific first:
// "a/b/c" -> ["a", "a/b"].
func namespaces(project string) []string {
	segs := strings.Split(project, "/")
	var out []string
	for i := 1; i < len(segs); i++ {
		out = append(out, strings.Join(segs[:i], "/"))
	}
	return out
}

// underNamespace reports whether project is inside ns (component-wise).
func underNamespace(project, ns string) bool {
	return strings.HasPrefix(project, ns+"/")
}

// Evaluate decides one push (identity, limits, exceptions in Phase 5).
func (e *Engine) Evaluate(r Request) *Decision {
	d := &Decision{
		User:       NormalizeUser(r.User),
		RepoType:   RepoType(r.Repository),
		Membership: r.Membership.State,
	}
	d.Project = NormalizeProject(r.Project, d.RepoType)
	d.RepoMode = e.P.Settings.RepositoryTypes[d.RepoType]
	if d.RepoMode == "" {
		d.RepoMode = policy.RepoMandatoryOnly // unknown repository types: security floor only
	}
	if d.RepoMode == policy.RepoNone {
		return d
	}
	groups := lowerAll(r.Membership.Groups)

	if !r.Membership.Available && e.hasGroupDeny && e.P.Settings.Membership.OnUnavailable == policy.OnUnavailableDeny {
		d.Violations = append(d.Violations, Violation{
			Code: policy.MembershipUnavailable, Source: "settings.membership.on_unavailable",
			Detail: "policy has group deny rules but no membership cache is loadable",
		})
		return d
	}

	if max := e.P.Settings.Limits.MaxRefUpdates; len(r.Updates) > max {
		v := Violation{Code: policy.LimitRefUpdates, Source: "settings.limits.max_ref_updates",
			Detail: fmt.Sprintf("%d ref updates, limit %d", len(r.Updates), max)}
		if !e.violate(d, r, groups, v) {
			return d
		}
	}

	for _, u := range r.Updates {
		e.identity(d, r, groups, u)
	}
	return d
}

// violate records v unless an exception waives it; reports whether waived.
func (e *Engine) violate(d *Decision, r Request, groups []string, v Violation) bool {
	if x := e.findException(d, r, groups, v); x != nil {
		d.Waived = append(d.Waived, Waiver{Violation: v, ExceptionID: x.ID})
		return true
	}
	d.Violations = append(d.Violations, v)
	return false
}

// findException returns the first exception (document order) waiving v.
func (e *Engine) findException(d *Decision, r Request, groups []string, v Violation) *policy.CompiledException {
	for i := range e.P.Exceptions {
		x := &e.P.Exceptions[i]
		if !contains(x.Rules, v.Code) || (v.Mandatory && !x.Mandatory) {
			continue
		}
		if exp, err := time.Parse(time.RFC3339, x.ExpiresAt); err != nil || r.Now.After(exp) {
			continue
		}
		if len(x.Users)+len(x.Groups) > 0 {
			ok := contains(x.Users, d.User)
			if !ok && r.Membership.GrantsUsable {
				ok = intersects(x.Groups, groups)
			}
			if !ok {
				continue
			}
		}
		if len(x.Namespaces) > 0 && !anyNamespace(d.Project, x.Namespaces) {
			continue
		}
		if len(x.Projects) > 0 && !contains(x.Projects, d.Project) {
			continue
		}
		if len(x.Refs) > 0 && (v.Ref == "" || !e.anyGlob(x.Refs, v.Ref, false)) {
			continue
		}
		if len(x.Paths) > 0 && (v.Path == "" || !e.anyGlob(x.Paths, v.Path, true)) {
			continue
		}
		if x.MaxFileSize > 0 && v.Size > x.MaxFileSize {
			continue
		}
		return x
	}
	return nil
}

func (e *Engine) anyGlob(globs []string, s string, fold bool) bool {
	for _, g := range globs {
		if e.match(g, s, fold) {
			return true
		}
	}
	return false
}

func anyNamespace(project string, nss []string) bool {
	for _, ns := range nss {
		if underNamespace(project, ns) {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func intersects(a, b []string) bool {
	for _, x := range a {
		if contains(b, x) {
			return true
		}
	}
	return false
}

func lowerAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, strings.ToLower(s))
	}
	sort.Strings(out)
	return out
}

func quote(s string) string { return strconv.Quote(s) }
