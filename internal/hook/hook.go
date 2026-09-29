// Package hook is the pre-receive runtime invoked by the Gitaly wrapper.
//
// Order: engine state -> active policy -> ref-update parsing -> identity,
// limits and exceptions -> object traversal (Phase 6; content rules 7-8),
// with the fail-closed behaviour of PHASE-1 §6.
package hook

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/soroush67/git-policy/internal/audit"
	"github.com/soroush67/git-policy/internal/engine"
	"github.com/soroush67/git-policy/internal/layout"
	"github.com/soroush67/git-policy/internal/membership"
	"github.com/soroush67/git-policy/internal/message"
	"github.com/soroush67/git-policy/internal/policy"
	"github.com/soroush67/git-policy/internal/state"
	"github.com/soroush67/git-policy/internal/store"
)

// Env is the identity context Gitaly passes to server hooks. These values are
// set by GitLab and cannot be injected by the client. Push options
// (GIT_PUSH_OPTION_*) are client-controlled and deliberately not read.
type Env struct {
	Username    string // GL_USERNAME
	ID          string // GL_ID (user-N / key-N)
	ProjectPath string // GL_PROJECT_PATH
	Repository  string // GL_REPOSITORY (project-N, wiki-N, snippet-N, design-N)
	Protocol    string // GL_PROTOCOL (ssh, http, web)
}

// EnvFromOS reads the Gitaly hook environment.
func EnvFromOS() Env {
	return Env{
		Username:    os.Getenv("GL_USERNAME"),
		ID:          os.Getenv("GL_ID"),
		ProjectPath: os.Getenv("GL_PROJECT_PATH"),
		Repository:  os.Getenv("GL_REPOSITORY"),
		Protocol:    os.Getenv("GL_PROTOCOL"),
	}
}

func (e Env) fields() map[string]any {
	return map[string]any{
		"user": e.Username, "gl_id": e.ID, "project": e.ProjectPath,
		"repository": e.Repository, "protocol": e.Protocol,
	}
}

// RefUpdate is one "<old> <new> <ref>" line from pre-receive stdin.
type RefUpdate struct {
	Old, New, Ref string
}

var oidRe = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// maxInputLines is a hard parser bound; the policy limit
// (settings.limits.max_ref_updates) is applied during evaluation.
const maxInputLines = 100000

// IsZero reports whether oid is the all-zero object id (ref create/delete).
func IsZero(oid string) bool { return strings.Trim(oid, "0") == "" }

// ParseUpdates validates the ref-update lines. Anything malformed is an error
// (INVALID_REF_UPDATE, fail closed): the input comes from Git, so a bad line
// means something is wrong.
func ParseUpdates(r io.Reader) ([]RefUpdate, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), 8192)
	var out []RefUpdate
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		if len(out) >= maxInputLines {
			return nil, fmt.Errorf("more than %d ref updates", maxInputLines)
		}
		f := strings.Split(line, " ")
		if len(f) != 3 || !oidRe.MatchString(f[0]) || !oidRe.MatchString(f[1]) || len(f[0]) != len(f[1]) {
			return nil, errors.New("malformed ref update line")
		}
		if !validRef(f[2]) {
			return nil, errors.New("malformed ref name")
		}
		out = append(out, RefUpdate{Old: f[0], New: f[1], Ref: f[2]})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func validRef(ref string) bool {
	if !strings.HasPrefix(ref, "refs/") || len(ref) > 1024 {
		return false
	}
	for _, r := range ref {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// Run evaluates one push and returns the process exit code (0 = accept).
func Run(l layout.Layout, env Env, stdin io.Reader, stderr io.Writer, now time.Time) (code int) {
	header := "Push rejected by organizational Git policy."
	reject := func(rule, detail string, extra map[string]any) int {
		message.WriteRejection(stderr, message.Rejection{
			Header: header, Rule: rule, Remediation: policy.DefaultRemediation(rule),
		})
		fields := env.fields()
		fields["rule"] = rule
		fields["detail"] = detail // internal detail goes to the audit log only
		for k, v := range extra {
			fields[k] = v
		}
		_ = audit.Append(l.LogsDir(), nil, audit.PushRejected, fields)
		return 1
	}
	defer func() {
		if r := recover(); r != nil {
			code = reject(policy.InternalError, fmt.Sprint("panic: ", r), nil)
		}
	}()

	st, err := state.Load(l.StateFile())
	if err != nil {
		return reject(policy.PolicyUnavailable, "engine state unreadable: "+err.Error(), nil)
	}
	if !st.EnabledAt(now) {
		_ = audit.Append(l.LogsDir(), nil, audit.PushAllowedDisabled, env.fields())
		return 0
	}

	act, err := store.LoadActive(l)
	if err != nil {
		return reject(policy.PolicyUnavailable, "no loadable policy: "+err.Error(), nil)
	}
	header = act.Policy.Settings.Messages.Header
	if act.FellBack {
		fields := env.fields()
		fields["version"] = layout.VersionName(act.Version)
		fields["detail"] = act.ActiveError.Error()
		_ = audit.Append(l.LogsDir(), nil, audit.PolicyFallback, fields)
	}

	updates, err := ParseUpdates(stdin)
	if err != nil {
		return reject(policy.InvalidRefUpdate, err.Error(), nil)
	}

	pol := act.Policy
	mem := membership.Load(l,
		time.Duration(pol.Settings.Membership.SoftMaxAgeSeconds)*time.Second,
		time.Duration(pol.Settings.Membership.HardMaxAgeSeconds)*time.Second, now)
	req := engine.Request{
		User: env.Username, Project: env.ProjectPath, Repository: env.Repository, Now: now,
		Membership: engine.Membership{
			Available: mem.Available(), GrantsUsable: mem.GrantsUsable(),
			Groups: mem.GroupsOf(env.Username), State: string(mem.Freshness),
		},
	}
	for _, u := range updates {
		req.Updates = append(req.Updates, engine.RefUpdate{Old: u.Old, New: u.New, Ref: u.Ref})
	}
	eng := engine.New(pol)
	d := eng.Evaluate(req)
	// Identity decides first and cheaply; objects are only inspected for
	// pushes that passed it (Phase 6 traversal; content checks in 7-8).
	if !d.Rejected() {
		_ = ScanPush(eng, d, req, "")
	}

	base := env.fields()
	base["policy_version"] = layout.VersionName(act.Version)
	base["membership"] = d.Membership
	for _, w := range d.Waived {
		_ = audit.Append(l.LogsDir(), nil, audit.ExceptionApplied, violationFields(base, w.Violation, map[string]any{"exception": w.ExceptionID}))
	}
	for _, v := range d.WouldReject {
		_ = audit.Append(l.LogsDir(), nil, audit.WouldReject, violationFields(base, v, map[string]any{"mode": "audit"}))
	}
	if d.Truncated {
		_ = audit.Append(l.LogsDir(), nil, audit.FindingsTruncated, base)
	}
	if !d.Rejected() {
		return 0
	}
	rep := message.Report{
		Header: header, User: displayUser(d.User), Project: env.ProjectPath,
		Remediation: pol.Settings.Messages.Remediation, Support: pol.Settings.Messages.Support,
		Truncated: d.Truncated,
	}
	for _, v := range d.Violations {
		it := message.Item{Rule: v.Code, Ref: v.Ref, File: v.Path}
		switch v.Code {
		case policy.BlockedExtension, policy.BlockedPath, policy.BlockedSignature, policy.FileTooLarge:
			it.Detail = v.Detail
		}
		if !IsZero(v.New) {
			it.Commit = v.New
		}
		rep.Items = append(rep.Items, it)
		_ = audit.Append(l.LogsDir(), nil, audit.PushRejected, violationFields(base, v, nil))
	}
	message.WriteReport(stderr, rep)
	return 1
}

func displayUser(u string) string {
	if u == policy.UnknownUser {
		return "(unknown)"
	}
	return u
}

// violationFields builds one audit event: the push context plus the rule,
// its policy source and the object it concerns.
func violationFields(base map[string]any, v engine.Violation, extra map[string]any) map[string]any {
	f := make(map[string]any, len(base)+10)
	for k, x := range base {
		f[k] = x
	}
	f["rule"] = v.Code
	f["source"] = v.Source
	if v.Ref != "" {
		f["ref"] = v.Ref
	}
	if v.New != "" && !IsZero(v.New) {
		f["commit"] = v.New
	}
	if v.Path != "" {
		f["file"] = v.Path
	}
	if v.Size > 0 {
		f["size"] = v.Size
	}
	if v.Detail != "" {
		f["detail"] = v.Detail
	}
	for k, x := range extra {
		f[k] = x
	}
	return f
}
