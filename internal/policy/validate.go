package policy

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Options control validation.
type Options struct {
	// Now is the reference time for exception expiry checks; zero means time.Now().
	Now time.Time
	// Inventory, when set, enables W010 (names unknown to GitLab).
	Inventory *Inventory
}

// Validate runs the semantic checks of PHASE-2-SCHEMA.md §5 on a parsed
// document. A policy is valid when no finding has SevError.
func Validate(doc *Document, opts Options) []Finding {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	v := &validator{doc: doc, now: now.UTC(), eff: doc.Effective(), mand: newSets()}
	v.header()
	v.settings()
	v.mandatory()
	v.content()
	v.identities()
	v.exceptions()
	if opts.Inventory != nil {
		v.inventoryWarnings(opts.Inventory)
	}
	return v.out
}

// sets holds normalised extension / path / signature entries.
type sets struct{ ext, path, sig map[string]bool }

func newSets() sets {
	return sets{map[string]bool{}, map[string]bool{}, map[string]bool{}}
}

func (s sets) add(o sets) {
	for k := range o.ext {
		s.ext[k] = true
	}
	for k := range o.path {
		s.path[k] = true
	}
	for k := range o.sig {
		s.sig[k] = true
	}
}

type validator struct {
	doc  *Document
	now  time.Time
	eff  EffectiveSettings
	out  []Finding
	mand sets
	cap  int64 // mandatory.max_file_size in bytes, 0 = none
}

func (v *validator) err(code, path, format string, a ...any) {
	v.out = append(v.out, Finding{Code: code, Severity: SevError, Path: path, Message: fmt.Sprintf(format, a...)})
}

func (v *validator) warn(code, path, format string, a ...any) {
	v.out = append(v.out, Finding{Code: code, Severity: SevWarning, Path: path, Message: fmt.Sprintf(format, a...)})
}

// ---- header & settings ------------------------------------------------------

func (v *validator) header() {
	d := v.doc
	if d.APIVersion != APIVersion {
		v.err("V004", "apiVersion", "must be %q, got %q", APIVersion, d.APIVersion)
	}
	if d.Kind != Kind {
		v.err("V004", "kind", "must be %q, got %q", Kind, d.Kind)
	}
	m := d.Metadata
	if m == nil {
		v.err("V005", "metadata", "metadata (name, revision) is required")
		return
	}
	if !nameRe.MatchString(m.Name) {
		v.err("V005", "metadata.name", "%q must match [a-z0-9][a-z0-9-]*", m.Name)
	}
	if m.Revision < 1 {
		v.err("V005", "metadata.revision", "must be an integer >= 1")
	}
	if !safeText(m.Description) {
		v.err("V005", "metadata.description", "contains control characters or is longer than %d characters", maxTextLen)
	}
}

func (v *validator) enum(path, val string, allowed ...string) {
	if val == "" {
		return
	}
	for _, a := range allowed {
		if val == a {
			return
		}
	}
	v.err("V005", path, "%q is not one of %s", val, strings.Join(allowed, ", "))
}

func (v *validator) intRange(path string, p *int, min, max int) {
	if p != nil && (*p < min || *p > max) {
		v.err("V005", path, "%d is outside the allowed range %d..%d", *p, min, max)
	}
}

func (v *validator) duration(path, s string) {
	if s == "" {
		return
	}
	if _, err := ParseDuration(s); err != nil {
		v.err("V005", path, "%v", err)
	}
}

func (v *validator) text(path, s string) {
	if !safeText(s) {
		v.err("V005", path, "contains control characters or is longer than %d characters", maxTextLen)
	}
}

func (v *validator) settings() {
	s := v.doc.Settings
	if s == nil {
		return
	}
	v.enum("settings.mode", s.Mode, ModeEnforce, ModeAudit)
	if rt := s.RepositoryTypes; rt != nil {
		for k, val := range map[string]string{"project": rt.Project, "wiki": rt.Wiki, "snippet": rt.Snippet, "design": rt.Design} {
			v.enum("settings.repository_types."+k, val, RepoAll, RepoMandatoryOnly, RepoNone)
		}
	}
	if l := s.Limits; l != nil {
		v.intRange("settings.limits.max_ref_updates", l.MaxRefUpdates, 1, 100000)
		v.intRange("settings.limits.max_new_commits", l.MaxNewCommits, 1, 10000000)
		v.intRange("settings.limits.max_new_blobs", l.MaxNewBlobs, 1, 100000000)
		v.duration("settings.limits.evaluation_timeout", l.EvaluationTimeout)
	}
	if m := s.Membership; m != nil {
		v.duration("settings.membership.soft_max_age", m.SoftMaxAge)
		v.duration("settings.membership.hard_max_age", m.HardMaxAge)
		v.enum("settings.membership.on_unavailable", m.OnUnavailable, OnUnavailableDeny, OnUnavailableIgnore)
		if v.eff.Membership.SoftMaxAgeSeconds > v.eff.Membership.HardMaxAgeSeconds {
			v.err("V041", "settings.membership", "soft_max_age must not be greater than hard_max_age")
		}
	}
	if a := s.Audit; a != nil {
		v.intRange("settings.audit.retention_days", a.RetentionDays, 1, 3650)
	}
	if x := s.Exceptions; x != nil {
		v.intRange("settings.exceptions.max_lifetime_days", x.MaxLifetimeDays, 1, 366)
	}
	if m := s.Messages; m != nil {
		v.text("settings.messages.header", m.Header)
		v.text("settings.messages.support", m.Support)
		for _, code := range sortedKeys(m.Remediation) {
			p := joinPath("settings.messages.remediation", code)
			if !IsRuleCode(code) {
				v.err("V005", p, "%q is not a rule code", code)
			}
			v.text(p, m.Remediation[code])
		}
	}
}

// ---- content layers ---------------------------------------------------------

// blockList validates and normalises a block or unblock list.
func (v *validator) blockList(path string, exts, paths, sigs []string, prefix string) sets {
	s := newSets()
	for i, e := range exts {
		p := index(path+"."+prefix+"extensions", i)
		if strings.HasPrefix(strings.TrimSpace(e), ".") {
			v.warn("W001", p, "leading dot in %q is ignored", e)
		}
		n := NormalizeExtension(e)
		if len(n) > 32 || !extensionRe.MatchString(n) {
			v.err("V005", p, "invalid extension %q", e)
			continue
		}
		if s.ext[n] {
			v.warn("W002", p, "duplicate extension %q", n)
		}
		s.ext[n] = true
	}
	for i, g := range paths {
		p := index(path+"."+prefix+"paths", i)
		if !v.pathGlob(p, g) {
			continue
		}
		n := strings.ToLower(g)
		if s.path[n] {
			v.warn("W002", p, "duplicate path pattern %q", g)
		}
		s.path[n] = true
	}
	for i, g := range sigs {
		p := index(path+"."+prefix+"signatures", i)
		if g != "pe" {
			v.err("V005", p, "unknown signature %q (supported: pe)", g)
			continue
		}
		s.sig[g] = true
	}
	return s
}

func (v *validator) pathGlob(path, g string) bool {
	if len(g) > 512 || !safeText(g) || strings.ContainsAny(g, "\n\t") {
		v.err("V005", path, "invalid path pattern %q", g)
		return false
	}
	if err := CheckGlob(g); err != nil {
		v.err("V023", path, "invalid path pattern %q: %v", g, err)
		return false
	}
	return true
}

func (v *validator) refGlob(path, g string) bool {
	if len(g) > 255 || !refGlobRe.MatchString(g) {
		v.err("V005", path, "invalid ref pattern %q (must be a full ref glob like refs/heads/release/*)", g)
		return false
	}
	if err := CheckGlob(g); err != nil {
		v.err("V023", path, "invalid ref pattern %q: %v", g, err)
		return false
	}
	return true
}

func (v *validator) size(path, s string) int64 {
	if s == "" {
		return 0
	}
	n, err := ParseSize(s)
	if err != nil {
		v.err("V005", path, "%v", err)
		return 0
	}
	if v.cap > 0 && n > v.cap {
		v.err("V021", path, "%s exceeds mandatory.max_file_size %s", s, FormatSize(v.cap))
	}
	return n
}

func (v *validator) mandatory() {
	m := v.doc.Mandatory
	if m == nil {
		return
	}
	v.enum("mandatory.mode", m.Mode, ModeEnforce, ModeAudit)
	v.mand = v.blockList("mandatory", m.BlockedExtensions, m.BlockedPaths, m.BlockedSignatures, "blocked_")
	if m.MaxFileSize != "" {
		n, err := ParseSize(m.MaxFileSize)
		if err != nil {
			v.err("V005", "mandatory.max_file_size", "%v", err)
		}
		v.cap = n
	}
	for i, u := range m.DenyUsers {
		if !validUser(u) {
			v.err("V005", index("mandatory.deny_users", i), "invalid username %q", u)
		}
	}
	for i, g := range m.DenyGroups {
		if code, msg := checkGitLabPath(g, 1); code != "" {
			v.err(code, index("mandatory.deny_groups", i), "%s", msg)
		}
	}
}

// layer is one validated content layer with its normalised lists.
type layer struct {
	path    string
	block   sets
	unblock sets
}

func (v *validator) refScope(path string, r *RefScope) layer {
	l := layer{path: path, block: newSets(), unblock: newSets()}
	if r == nil {
		return l
	}
	v.enum(path+".mode", r.Mode, ModeEnforce, ModeAudit)
	l.block = v.blockList(path, r.BlockedExtensions, r.BlockedPaths, r.BlockedSignatures, "blocked_")
	l.unblock = v.blockList(path, r.UnblockExtensions, r.UnblockPaths, r.UnblockSignatures, "unblock_")
	v.size(path+".max_file_size", r.MaxFileSize)
	return l
}

// scope validates a namespace/project layer and its refs; returns the scope
// layer plus its ref layers.
func (v *validator) scope(path string, s *Scope) []layer {
	if s == nil {
		return nil
	}
	out := []layer{v.refScope(path, &s.RefScope)}
	for _, g := range sortedKeys(s.Refs) {
		p := joinPath(path+".refs", g)
		if v.refGlob(p, g) {
			out = append(out, v.refScope(p, s.Refs[g]))
		}
	}
	return out
}

func (v *validator) content() {
	// available[scopeKey] = everything blocked by that scope incl. its refs,
	// used for W003 (unblock of something never blocked above).
	var defaults []layer
	if d := v.doc.Defaults; d != nil {
		defaults = append(defaults, layer{"defaults", v.blockList("defaults", d.BlockedExtensions, d.BlockedPaths, d.BlockedSignatures, "blocked_"), newSets()})
		v.size("defaults.max_file_size", d.MaxFileSize)
		for _, g := range sortedKeys(d.Refs) {
			p := joinPath("defaults.refs", g)
			if v.refGlob(p, g) {
				defaults = append(defaults, v.refScope(p, d.Refs[g]))
			}
		}
	}
	base := unionOf(defaults)

	nsLayers := map[string][]layer{}
	v.caseCollisions("namespaces", keysOf(v.doc.Namespaces))
	for _, k := range sortedKeys(v.doc.Namespaces) {
		p := joinPath("namespaces", k)
		if code, msg := checkGitLabPath(k, 1); code != "" {
			v.err(code, p, "%s", msg)
			continue
		}
		nsLayers[strings.ToLower(k)] = v.scope(p, v.doc.Namespaces[k])
	}

	projLayers := map[string][]layer{}
	v.caseCollisions("projects", keysOf(v.doc.Projects))
	for _, k := range sortedKeys(v.doc.Projects) {
		p := joinPath("projects", k)
		if code, msg := checkGitLabPath(k, 2); code != "" {
			v.err(code, p, "%s", msg)
			continue
		}
		if ps := v.doc.Projects[k]; ps != nil {
			projLayers[strings.ToLower(k)] = v.scope(p, &ps.Scope)
		}
	}

	// ancestors(path) = defaults + every namespace that is a component prefix.
	ancestors := func(path string, includeSelf bool) sets {
		s := newSets()
		s.add(base)
		segs := strings.Split(path, "/")
		n := len(segs)
		if !includeSelf {
			n--
		}
		for i := 1; i <= n; i++ {
			s.add(unionOf(nsLayers[strings.Join(segs[:i], "/")]))
		}
		return s
	}
	check := func(ls []layer, avail sets) {
		for _, l := range ls {
			v.unblocks(l, avail)
		}
	}
	check(defaults, base)
	for _, k := range sortedKeys(nsLayers) {
		check(nsLayers[k], ancestors(k, true))
	}
	for _, k := range sortedKeys(projLayers) {
		avail := ancestors(k, false)
		avail.add(unionOf(projLayers[k]))
		check(projLayers[k], avail)
	}
}

// unblocks reports V020 (unblocking a mandatory entry) and W003 (unblocking
// something that nothing above blocks).
func (v *validator) unblocks(l layer, avail sets) {
	report := func(kind string, unblock, mand, av map[string]bool) {
		for _, e := range sortedKeys(unblock) {
			p := l.path + ".unblock_" + kind
			switch {
			case mand[e]:
				v.err("V020", p, "%q is blocked by mandatory and cannot be unblocked (use an exception with mandatory: true)", e)
			case !av[e]:
				v.warn("W003", p, "%q is not blocked by this scope or any scope above it", e)
			}
		}
	}
	report("extensions", l.unblock.ext, v.mand.ext, avail.ext)
	report("paths", l.unblock.path, v.mand.path, avail.path)
	report("signatures", l.unblock.sig, v.mand.sig, avail.sig)
}

// ---- identity -----------------------------------------------------------------

func (v *validator) identities() {
	denyUsers, denyGroups := map[string]bool{}, map[string]bool{}
	if m := v.doc.Mandatory; m != nil {
		for _, u := range m.DenyUsers {
			denyUsers[strings.ToLower(u)] = true
		}
		for _, g := range m.DenyGroups {
			denyGroups[strings.ToLower(g)] = true
		}
	}
	v.caseCollisions("users", keysOf(v.doc.Users))
	for _, k := range sortedKeys(v.doc.Users) {
		p := joinPath("users", k)
		if !validUser(k) {
			v.err("V005", p, "invalid username %q", k)
			continue
		}
		if v.identityRule(p, v.doc.Users[k]) && denyUsers[strings.ToLower(k)] {
			v.warn("W011", p, "allow rules never take effect: user is in mandatory.deny_users")
		}
	}
	v.caseCollisions("groups", keysOf(v.doc.Groups))
	for _, k := range sortedKeys(v.doc.Groups) {
		p := joinPath("groups", k)
		if code, msg := checkGitLabPath(k, 1); code != "" {
			v.err(code, p, "%s", msg)
			continue
		}
		if v.identityRule(p, v.doc.Groups[k]) && denyGroups[strings.ToLower(k)] {
			v.warn("W011", p, "allow rules never take effect: group is in mandatory.deny_groups")
		}
	}
}

// identityRule validates a rule tree and reports whether it contains any allow.
func (v *validator) identityRule(path string, r *IdentityRule) bool {
	if r == nil || (r.Push == "" && len(r.Refs) == 0 && len(r.Namespaces) == 0 && len(r.Projects) == 0) {
		v.err("V005", path, "empty rule")
		return false
	}
	allow := v.action(path+".push", r.Push)
	allow = v.refActions(path+".refs", r.Refs) || allow
	v.caseCollisions(path+".namespaces", keysOf(r.Namespaces))
	for _, k := range sortedKeys(r.Namespaces) {
		p := joinPath(path+".namespaces", k)
		if code, msg := checkGitLabPath(k, 1); code != "" {
			v.err(code, p, "%s", msg)
			continue
		}
		allow = v.scopedIdentity(p, r.Namespaces[k]) || allow
	}
	v.caseCollisions(path+".projects", keysOf(r.Projects))
	for _, k := range sortedKeys(r.Projects) {
		p := joinPath(path+".projects", k)
		if code, msg := checkGitLabPath(k, 2); code != "" {
			v.err(code, p, "%s", msg)
			continue
		}
		allow = v.scopedIdentity(p, r.Projects[k]) || allow
	}
	return allow
}

func (v *validator) scopedIdentity(path string, s *ScopedIdentity) bool {
	if s == nil || (s.Push == "" && len(s.Refs) == 0) {
		v.err("V005", path, "empty rule")
		return false
	}
	allow := v.action(path+".push", s.Push)
	return v.refActions(path+".refs", s.Refs) || allow
}

func (v *validator) refActions(path string, m map[string]string) bool {
	allow := false
	for _, g := range sortedKeys(m) {
		p := joinPath(path, g)
		if v.refGlob(p, g) {
			allow = v.action(p, m[g]) || allow
		}
	}
	return allow
}

func (v *validator) action(path, a string) bool {
	v.enum(path, a, "allow", "deny")
	return a == "allow"
}

// ---- exceptions ---------------------------------------------------------------

func (v *validator) exceptions() {
	seen := map[string]int{}
	today := v.now.Truncate(24 * time.Hour)
	for i, x := range v.doc.Exceptions {
		p := index("exceptions", i)
		if x == nil {
			v.err("V005", p, "empty exception")
			continue
		}
		if !exceptionRe.MatchString(x.ID) {
			v.err("V005", p+".id", "%q must match [A-Z][A-Z0-9-]{2,63}", x.ID)
		} else if first, dup := seen[x.ID]; dup {
			v.err("V030", p+".id", "duplicate exception id %q (also exceptions[%d])", x.ID, first)
		} else {
			seen[x.ID] = i
		}

		if len(x.Rules) == 0 {
			v.err("V005", p+".rules", "at least one rule code is required")
		}
		codes := map[string]bool{}
		pathless := false
		for j, c := range x.Rules {
			rp := index(p+".rules", j)
			switch {
			case !IsRuleCode(c):
				v.err("V035", rp, "%q is not a rule code (wildcards are not allowed)", c)
			case !IsWaivable(c):
				v.err("V035", rp, "%s cannot be waived by an exception", c)
			case codes[c]:
				v.warn("W002", rp, "duplicate rule %s", c)
			}
			codes[c] = true
			if IsRuleCode(c) && !HasPath(c) {
				pathless = true
			}
		}

		hasSubjects := false
		if s := x.Subjects; s != nil {
			hasSubjects = len(s.Users)+len(s.Groups) > 0
			for j, u := range s.Users {
				if !validUser(u) {
					v.err("V005", index(p+".subjects.users", j), "invalid username %q", u)
				}
			}
			for j, g := range s.Groups {
				if code, msg := checkGitLabPath(g, 1); code != "" {
					v.err(code, index(p+".subjects.groups", j), "%s", msg)
				}
			}
		}
		hasScope := false
		if sc := x.Scope; sc != nil {
			hasScope = len(sc.Namespaces)+len(sc.Projects) > 0
			for j, n := range sc.Namespaces {
				if code, msg := checkGitLabPath(n, 1); code != "" {
					v.err(code, index(p+".scope.namespaces", j), "%s", msg)
				}
			}
			for j, n := range sc.Projects {
				if code, msg := checkGitLabPath(n, 2); code != "" {
					v.err(code, index(p+".scope.projects", j), "%s", msg)
				}
			}
			for j, g := range sc.Refs {
				v.refGlob(index(p+".scope.refs", j), g)
			}
			for j, g := range sc.Paths {
				v.pathGlob(index(p+".scope.paths", j), g)
			}
			if len(sc.Paths) > 0 && pathless {
				v.err("V033", p+".scope.paths", "paths cannot be combined with identity or limit rules (they have no file path)")
			}
		}
		if !hasSubjects && !hasScope {
			v.err("V031", p, "exception needs subjects or a namespace/project scope; global exceptions for everyone are not allowed")
		}

		if x.MaxFileSize != "" {
			if !codes[FileTooLarge] {
				v.err("V034", p+".max_file_size", "max_file_size is only valid with rule FILE_TOO_LARGE")
			}
			if n, err := ParseSize(x.MaxFileSize); err != nil {
				v.err("V005", p+".max_file_size", "%v", err)
			} else if v.cap > 0 && n > v.cap && !x.Mandatory {
				v.warn("W012", p+".max_file_size", "sizes above mandatory.max_file_size %s are only waived with mandatory: true", FormatSize(v.cap))
			}
		}

		if len(strings.TrimSpace(x.Reason)) < 10 {
			v.err("V005", p+".reason", "a reason of at least 10 characters is required")
		}
		v.text(p+".reason", x.Reason)
		v.text(p+".approved_by", x.ApprovedBy)
		if x.Ticket != "" && !ticketRe.MatchString(x.Ticket) {
			v.err("V005", p+".ticket", "invalid ticket reference %q", x.Ticket)
		}

		if x.Expires == "" {
			v.err("V005", p+".expires", "expires (YYYY-MM-DD) is required")
			continue
		}
		exp, err := ParseDate(x.Expires)
		if err != nil {
			v.err("V005", p+".expires", "%v", err)
			continue
		}
		maxDays := v.eff.ExceptionMaxLifetimeDays
		if exp.Sub(today) > time.Duration(maxDays)*24*time.Hour {
			v.err("V032", p+".expires", "%s is more than %d days away (settings.exceptions.max_lifetime_days)", x.Expires, maxDays)
		}
		if EndOfDay(exp).Before(v.now) {
			v.warn("W004", p+".expires", "exception expired on %s and is inactive", x.Expires)
		}
	}
}

// ---- helpers ------------------------------------------------------------------

// caseCollisions reports V010 for keys that are equal after case-folding:
// GitLab paths and usernames are case-insensitive.
func (v *validator) caseCollisions(path string, keys []string) {
	seen := map[string]string{}
	sort.Strings(keys)
	for _, k := range keys {
		l := strings.ToLower(k)
		if prev, ok := seen[l]; ok {
			v.err("V010", joinPath(path, k), "%q and %q are the same name in GitLab (case-insensitive)", prev, k)
			continue
		}
		seen[l] = k
	}
}

func unionOf(ls []layer) sets {
	s := newSets()
	for _, l := range ls {
		s.add(l.block)
	}
	return s
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := keysOf(m)
	sort.Strings(out)
	return out
}
