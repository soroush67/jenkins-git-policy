package admin

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/soroush67/git-policy/internal/audit"
	"github.com/soroush67/git-policy/internal/fsutil"
	"github.com/soroush67/git-policy/internal/layout"
	"github.com/soroush67/git-policy/internal/membership"
	"github.com/soroush67/git-policy/internal/state"
	"github.com/soroush67/git-policy/internal/store"
	"github.com/soroush67/git-policy/internal/version"
)

// Health levels, also used as status exit codes.
const (
	HealthOK       = "OK"
	HealthWarning  = "WARNING"
	HealthCritical = "CRITICAL"
)

// Report is the output of `git-policy status`. It contains no secrets.
type Report struct {
	EngineVersion string       `json:"engine_version"`
	Root          string       `json:"root"`
	Health        string       `json:"health"`
	Problems      []string     `json:"problems"`
	Engine        EngineStatus `json:"engine"`
	BreakGlass    bool         `json:"break_glass"`
	Hook          HookStatus   `json:"hook"`
	Policy        PolicyStatus `json:"policy"`
	Membership    string       `json:"membership"`
	AuditLog      string       `json:"audit_log"`
}

type EngineStatus struct {
	State        string `json:"state"` // enabled | disabled | enabled (disable expired) | error
	Enforcing    bool   `json:"enforcing"`
	StatePresent bool   `json:"state_present"`
	ChangedAt    string `json:"changed_at,omitempty"`
	ChangedBy    string `json:"changed_by,omitempty"`
	Reason       string `json:"reason,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
	Error        string `json:"error,omitempty"`
}

type HookStatus struct {
	Path       string   `json:"path,omitempty"`
	State      string   `json:"state"` // installed | missing | MODIFIED | unknown
	OtherHooks []string `json:"other_hooks,omitempty"`
}

type PolicyStatus struct {
	Loaded       bool   `json:"loaded"`
	Version      string `json:"version,omitempty"`
	Name         string `json:"name,omitempty"`
	Revision     int    `json:"revision,omitempty"`
	SourceSHA256 string `json:"source_sha256,omitempty"`
	DeployedAt   string `json:"deployed_at,omitempty"`
	DeployedBy   string `json:"deployed_by,omitempty"`
	Previous     string `json:"previous,omitempty"`
	Stored       int    `json:"stored_versions"`
	FellBack     bool   `json:"fell_back"`
	Error        string `json:"error,omitempty"`
}

// Status collects a read-only health report. It needs read access only.
func Status(l layout.Layout, hookPath string, now time.Time) Report {
	r := Report{EngineVersion: version.Version, Root: l.Root, Health: HealthOK, Problems: []string{}}
	critical := func(f string, a ...any) {
		r.Health = HealthCritical
		r.Problems = append(r.Problems, "CRITICAL: "+fmt.Sprintf(f, a...))
	}
	warning := func(f string, a ...any) {
		if r.Health == HealthOK {
			r.Health = HealthWarning
		}
		r.Problems = append(r.Problems, "WARNING: "+fmt.Sprintf(f, a...))
	}

	// Engine switch.
	st, err := state.Load(l.StateFile())
	switch {
	case err != nil:
		r.Engine = EngineStatus{State: "error", Error: err.Error()}
		critical("engine state unreadable: every push is rejected (fail closed); fix with admin enable/disable")
	default:
		r.Engine = EngineStatus{Enforcing: st.EnabledAt(now), StatePresent: st.Present, ChangedBy: st.ChangedBy, Reason: st.Reason}
		if !st.ChangedAt.IsZero() {
			r.Engine.ChangedAt = st.ChangedAt.Format(time.RFC3339)
		}
		if st.ExpiresAt != nil {
			r.Engine.ExpiresAt = st.ExpiresAt.Format(time.RFC3339)
		}
		switch {
		case st.Enabled:
			r.Engine.State = "enabled"
		case st.DisableExpired(now):
			r.Engine.State = "enabled (disable expired)"
		default:
			r.Engine.State = "disabled"
			if st.ExpiresAt == nil {
				warning("engine is disabled without expiry (initial install state)")
			} else {
				warning("engine is disabled until %s", st.ExpiresAt.Format(time.RFC3339))
			}
		}
		if !st.Present {
			warning("state file missing: engine defaults to enabled")
		}
	}

	if _, err := os.Lstat(l.BreakGlass()); err == nil {
		r.BreakGlass = true
		critical("break-glass file present: ALL pushes bypass git-policy (remove %s after recovery)", l.BreakGlass())
	}

	// Hook wrapper.
	r.Hook = HookStatus{Path: hookPath, State: "unknown"}
	if hookPath != "" {
		want, _ := HookScript(l.Root)
		cur, err := fsutil.ReadFileNoFollow(hookPath, 1<<20)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			r.Hook.State = "missing"
			critical("hook wrapper not installed: git-policy is not enforcing anything")
		case err != nil:
			r.Hook.State = "error"
			critical("hook wrapper unreadable: %v", err)
		case !bytes.Equal(cur, want):
			r.Hook.State = "MODIFIED"
			critical("hook wrapper differs from the version this engine installs; reinstall with admin install --replace-hook")
		default:
			r.Hook.State = "installed"
		}
		r.Hook.OtherHooks = otherHooks(filepath.Dir(hookPath), filepath.Base(hookPath))
	}

	// Policy.
	if vs, err := store.Versions(l); err == nil {
		r.Policy.Stored = len(vs)
	}
	if p, _ := store.PreviousVersion(l); p != 0 {
		r.Policy.Previous = layout.VersionName(p)
	}
	act, err := store.LoadActive(l)
	if err != nil {
		r.Policy.Error = err.Error()
		if r.Engine.Enforcing {
			critical("engine is enabled but no policy is loadable: every push is rejected (fail closed)")
		} else {
			warning("no loadable policy: %v", err)
		}
	} else {
		m := act.Meta
		r.Policy = PolicyStatus{Loaded: true, Version: layout.VersionName(act.Version), Name: m.Name, Revision: m.Revision,
			SourceSHA256: m.SourceSHA256, DeployedAt: m.DeployedAt.Format(time.RFC3339), DeployedBy: m.DeployedBy,
			Previous: r.Policy.Previous, Stored: r.Policy.Stored, FellBack: act.FellBack}
		if act.FellBack {
			r.Policy.Error = act.ActiveError.Error()
			warning("ACTIVE policy unusable, serving PREVIOUS %s: %v", r.Policy.Version, act.ActiveError)
		}
	}

	r.Membership = "not deployed"
	if act != nil {
		set := act.Policy.Settings.Membership
		mem := membership.Load(l, time.Duration(set.SoftMaxAgeSeconds)*time.Second, time.Duration(set.HardMaxAgeSeconds)*time.Second, now)
		usesGroups := len(act.Policy.Groups) > 0 || len(act.Policy.Mandatory.DenyGroups) > 0
		switch {
		case mem.Available():
			r.Membership = fmt.Sprintf("%s (generated %s, age %s, %d users)", mem.Freshness,
				mem.File.GeneratedAt.Format(time.RFC3339), mem.Age.Round(time.Second), len(mem.File.Users))
			if mem.FromPrevious {
				r.Membership += " [from previous.json]"
			}
		case mem.Err != nil:
			r.Membership = "unavailable: " + mem.Err.Error()
		}
		if usesGroups {
			switch mem.Freshness {
			case membership.Stale:
				warning("membership cache is stale (older than soft_max_age)")
			case membership.Expired:
				warning("membership cache expired: group allows and group exceptions are ignored")
			case membership.Unavailable:
				if set.OnUnavailable == "deny_if_group_rules" && r.Engine.Enforcing {
					critical("policy has group rules but no membership cache: pushes are rejected (MEMBERSHIP_UNAVAILABLE)")
				} else {
					warning("policy has group rules but no membership cache")
				}
			}
		}
	}
	r.AuditLog = auditLogStatus(l, now)
	return r
}

func auditLogStatus(l layout.Layout, now time.Time) string {
	name := audit.FileName(l.LogsDir(), now)
	f, err := os.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return "no events today (" + filepath.Base(name) + ")"
	}
	if err != nil {
		return "unreadable: " + err.Error()
	}
	defer f.Close()
	n, _ := io.Copy(io.Discard, f)
	return fmt.Sprintf("%s (%d bytes)", filepath.Base(name), n)
}
