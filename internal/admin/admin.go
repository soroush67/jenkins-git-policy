// Package admin implements the privileged management operations. They are
// the only code paths that modify policy, state or the installed hook, and
// they are what Jenkins drives (through the restricted git-policy-ctl channel).
package admin

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"regexp"
	"strconv"
	"time"

	"github.com/soroush67/git-policy/internal/audit"
	"github.com/soroush67/git-policy/internal/engine"
	"github.com/soroush67/git-policy/internal/fsutil"
	"github.com/soroush67/git-policy/internal/layout"
	"github.com/soroush67/git-policy/internal/membership"
	"github.com/soroush67/git-policy/internal/policy"
	"github.com/soroush67/git-policy/internal/state"
	"github.com/soroush67/git-policy/internal/store"
)

const lockTimeout = 10 * time.Second

var actorRe = regexp.MustCompile(`^[A-Za-z0-9 _.:#@/+=-]{1,128}$`)

// Context carries everything an admin operation needs.
type Context struct {
	L        layout.Layout
	HookPath string        // "" when unknown (custom test root without --hook-path)
	RootOwn  *fsutil.Owner // root:root
	RootGit  *fsutil.Owner // root:git — readable by the hook, writable by root only
	GitOwn   *fsutil.Owner // git:git  — audit logs
	Actor    string
	Now      func() time.Time
}

// NewContext prepares an admin context. On the production root it requires
// euid 0 and the git account. Custom roots (tests, dry runs) may run
// unprivileged; ownership changes are then skipped.
func NewContext(root, hookPath, actor string) (*Context, error) {
	l := layout.New(root)
	c := &Context{L: l, HookPath: hookPath, Now: time.Now}
	if hookPath == "" && l.Root == layout.DefaultRoot {
		c.HookPath = layout.HookPath
	}
	if os.Geteuid() == 0 {
		uid, gid, err := lookupGit()
		if err != nil {
			return nil, fmt.Errorf("git account not found (is this the GitLab container?): %w", err)
		}
		c.RootOwn = &fsutil.Owner{UID: 0, GID: 0}
		c.RootGit = &fsutil.Owner{UID: 0, GID: gid}
		c.GitOwn = &fsutil.Owner{UID: uid, GID: gid}
	} else if l.Root == layout.DefaultRoot {
		return nil, errors.New("admin commands on the production root must run as root (docker exec -u root gitlab ...)")
	}
	if actor == "" {
		actor = defaultActor()
	}
	if !actorRe.MatchString(actor) {
		return nil, fmt.Errorf("invalid --actor %q", actor)
	}
	c.Actor = actor
	return c, nil
}

func lookupGit() (uid, gid int, err error) {
	u, err := user.Lookup("git")
	if err != nil {
		return 0, 0, err
	}
	uid, _ = strconv.Atoi(u.Uid)
	gid, _ = strconv.Atoi(u.Gid)
	return uid, gid, nil
}

func defaultActor() string {
	if s := os.Getenv("SUDO_USER"); s != "" && actorRe.MatchString(s) {
		return "sudo:" + s
	}
	if u, err := user.Current(); err == nil && actorRe.MatchString(u.Username) {
		return "local:" + u.Username
	}
	return "uid:" + strconv.Itoa(os.Geteuid())
}

func checkReason(reason string, required bool) error {
	if reason == "" && required {
		return errors.New("--reason is required")
	}
	if len(reason) > 500 {
		return errors.New("--reason is longer than 500 characters")
	}
	for _, r := range reason {
		if r < 0x20 || r == 0x7f {
			return errors.New("--reason contains control characters")
		}
	}
	return nil
}

func (c *Context) lock() (func(), error) {
	return fsutil.Lock(c.L.AdminLock(), lockTimeout)
}

func (c *Context) audit(action string, fields map[string]any) {
	fields["actor"] = c.Actor
	if err := audit.Append(c.L.LogsDir(), c.GitOwn, action, fields); err != nil {
		fmt.Fprintf(os.Stderr, "git-policy: warning: audit log write failed: %v\n", err)
	}
}

// Enable turns enforcement on. It refuses when no policy is loadable, because
// an enabled engine without a policy rejects every push (fail closed).
func (c *Context) Enable(reason string) error {
	if err := checkReason(reason, false); err != nil {
		return err
	}
	unlock, err := c.lock()
	if err != nil {
		return err
	}
	defer unlock()
	act, err := store.LoadActive(c.L)
	if err != nil {
		return fmt.Errorf("refusing to enable: %v — every push would be rejected; deploy a policy first (admin apply)", err)
	}
	if act.FellBack {
		return fmt.Errorf("refusing to enable: the active policy is unusable (%v); fix with admin apply or admin rollback first", act.ActiveError)
	}
	if err := c.membershipGuard(act.Policy); err != nil {
		return fmt.Errorf("refusing to enable: %w", err)
	}
	now := c.Now().UTC()
	if err := state.Save(c.L.StateFile(), state.State{Enabled: true, ChangedAt: now, ChangedBy: c.Actor, Reason: reason}, c.RootGit); err != nil {
		return err
	}
	c.audit(audit.PolicyEnabled, map[string]any{"reason": reason, "policy_version": layout.VersionName(act.Version)})
	return nil
}

// Disable turns enforcement off for ttl. The expiry is mandatory so a
// forgotten disable cannot become permanent.
func (c *Context) Disable(reason string, ttl time.Duration) (time.Time, error) {
	if err := checkReason(reason, true); err != nil {
		return time.Time{}, err
	}
	if ttl <= 0 || ttl > state.MaxDisableTTL {
		return time.Time{}, fmt.Errorf("--ttl must be between 1s and %s", state.MaxDisableTTL)
	}
	unlock, err := c.lock()
	if err != nil {
		return time.Time{}, err
	}
	defer unlock()
	now := c.Now().UTC()
	exp := now.Add(ttl)
	if err := state.Save(c.L.StateFile(), state.State{Enabled: false, ChangedAt: now, ChangedBy: c.Actor, Reason: reason, ExpiresAt: &exp}, c.RootGit); err != nil {
		return time.Time{}, err
	}
	c.audit(audit.PolicyDisabled, map[string]any{"reason": reason, "expires_at": exp.Format(time.RFC3339)})
	return exp, nil
}

// Apply deploys a policy. Findings are returned for display in both the
// success and the refusal case.
func (c *Context) Apply(source []byte, reason string) (*store.ApplyResult, error) {
	if err := checkReason(reason, false); err != nil {
		return nil, err
	}
	unlock, err := c.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	// Guard: a policy with group deny rules on an enforcing engine without a
	// membership cache would reject every push (MEMBERSHIP_UNAVAILABLE).
	if st, err := state.Load(c.L.StateFile()); err == nil && st.EnabledAt(c.Now()) {
		if compiled, _ := policy.Build(source, policy.Options{Now: c.Now()}); compiled != nil {
			if err := c.membershipGuard(compiled); err != nil {
				c.audit(audit.PolicyUpdateRefused, map[string]any{"reason": reason, "error_codes": []string{"V042"}})
				return &store.ApplyResult{Findings: []policy.Finding{{Code: "V042", Severity: policy.SevError,
					Message: err.Error() + "; deploy the membership cache first or disable the engine"}}}, store.ErrInvalidPolicy
			}
		}
	}
	res, err := store.Apply(c.L, store.ApplyRequest{
		Source: source, Actor: c.Actor, Reason: reason, Now: c.Now(), Owner: c.RootGit,
	})
	if errors.Is(err, store.ErrInvalidPolicy) {
		var codes []string
		for _, f := range res.Findings {
			if f.Severity == "error" {
				codes = append(codes, f.Code)
			}
		}
		c.audit(audit.PolicyUpdateRefused, map[string]any{"reason": reason, "error_codes": codes})
		return res, err
	}
	if err != nil {
		return res, err
	}
	fields := map[string]any{
		"reason": reason, "version": layout.VersionName(res.Meta.Version), "policy_name": res.Meta.Name,
		"revision": res.Meta.Revision, "source_sha256": res.Meta.SourceSHA256,
	}
	if res.Previous != 0 {
		fields["previous_version"] = layout.VersionName(res.Previous)
	}
	c.audit(audit.PolicyUpdated, fields)
	return res, nil
}

// Rollback re-activates an earlier version (0 = PREVIOUS).
func (c *Context) Rollback(to int, reason string) (from, target int, err error) {
	if err := checkReason(reason, true); err != nil {
		return 0, 0, err
	}
	unlock, err := c.lock()
	if err != nil {
		return 0, 0, err
	}
	defer unlock()
	from, target, err = store.Rollback(c.L, to, c.RootGit)
	if err != nil {
		return from, target, err
	}
	c.audit(audit.PolicyRollback, map[string]any{
		"reason": reason, "from_version": layout.VersionName(from), "to_version": layout.VersionName(target),
	})
	return from, target, nil
}

// membershipGuard refuses a state in which every push would be rejected
// because the policy needs group membership and no cache is loadable.
func (c *Context) membershipGuard(p *policy.Compiled) error {
	if !engine.New(p).NeedsMembership() {
		return nil
	}
	set := p.Settings.Membership
	mem := membership.Load(c.L, time.Duration(set.SoftMaxAgeSeconds)*time.Second, time.Duration(set.HardMaxAgeSeconds)*time.Second, c.Now())
	if mem.Available() {
		return nil
	}
	return fmt.Errorf("the policy has group deny rules (on_unavailable: deny_if_group_rules) but no membership cache is deployed: every push would be rejected")
}

// PruneLogs deletes audit files older than the active policy's
// settings.audit.retention_days (or retentionDays when > 0). The pruning
// itself is audited.
func (c *Context) PruneLogs(retentionDays int) ([]string, error) {
	if retentionDays <= 0 {
		act, err := store.LoadActive(c.L)
		if err != nil {
			return nil, fmt.Errorf("no active policy to read retention_days from (pass --days): %w", err)
		}
		retentionDays = act.Policy.Settings.Audit.RetentionDays
	}
	unlock, err := c.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	removed, err := audit.Prune(c.L.LogsDir(), retentionDays, c.Now())
	c.audit(audit.LogsPruned, map[string]any{"retention_days": retentionDays, "removed": removed})
	return removed, err
}
