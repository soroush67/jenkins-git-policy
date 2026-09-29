package policy

// EffectiveSettings are settings with defaults applied (PHASE-2-SCHEMA.md §2.2).
type EffectiveSettings struct {
	Mode                     string              `json:"mode"`
	RepositoryTypes          map[string]string   `json:"repository_types"`
	Limits                   EffectiveLimits     `json:"limits"`
	Membership               EffectiveMembership `json:"membership"`
	Audit                    EffectiveAudit      `json:"audit"`
	ExceptionMaxLifetimeDays int                 `json:"exception_max_lifetime_days"`
	Messages                 EffectiveMessages   `json:"messages"`
}

type EffectiveLimits struct {
	MaxRefUpdates            int `json:"max_ref_updates"`
	MaxNewCommits            int `json:"max_new_commits"`
	MaxNewBlobs              int `json:"max_new_blobs"`
	EvaluationTimeoutSeconds int `json:"evaluation_timeout_seconds"`
}

type EffectiveMembership struct {
	SoftMaxAgeSeconds int    `json:"soft_max_age_seconds"`
	HardMaxAgeSeconds int    `json:"hard_max_age_seconds"`
	OnUnavailable     string `json:"on_unavailable"`
}

type EffectiveAudit struct {
	Required      bool `json:"required"`
	RetentionDays int  `json:"retention_days"`
	LogAccepted   bool `json:"log_accepted"`
}

type EffectiveMessages struct {
	Header      string            `json:"header"`
	Support     string            `json:"support,omitempty"`
	Remediation map[string]string `json:"remediation"`
}

const (
	ModeEnforce = "enforce"
	ModeAudit   = "audit"

	RepoAll           = "all"
	RepoMandatoryOnly = "mandatory_only"
	RepoNone          = "none"

	OnUnavailableDeny   = "deny_if_group_rules"
	OnUnavailableIgnore = "ignore_groups"

	defaultHeader = "Push rejected by organizational Git policy."
)

// Effective returns the document's settings with defaults applied. Values that
// fail validation fall back to defaults; callers validate first.
func (d *Document) Effective() EffectiveSettings {
	e := EffectiveSettings{
		Mode: ModeEnforce,
		RepositoryTypes: map[string]string{
			"project": RepoAll, "wiki": RepoMandatoryOnly, "snippet": RepoMandatoryOnly, "design": RepoNone,
		},
		Limits:                   EffectiveLimits{1000, 50000, 500000, 45},
		Membership:               EffectiveMembership{2 * 3600, 24 * 3600, OnUnavailableDeny},
		Audit:                    EffectiveAudit{Required: false, RetentionDays: 180},
		ExceptionMaxLifetimeDays: 180,
		Messages:                 EffectiveMessages{Header: defaultHeader, Remediation: map[string]string{}},
	}
	for code, info := range rules {
		e.Messages.Remediation[code] = info.remediation
	}
	s := d.Settings
	if s == nil {
		return e
	}
	setStr(&e.Mode, s.Mode)
	if rt := s.RepositoryTypes; rt != nil {
		for k, v := range map[string]string{"project": rt.Project, "wiki": rt.Wiki, "snippet": rt.Snippet, "design": rt.Design} {
			if v != "" {
				e.RepositoryTypes[k] = v
			}
		}
	}
	if l := s.Limits; l != nil {
		setInt(&e.Limits.MaxRefUpdates, l.MaxRefUpdates)
		setInt(&e.Limits.MaxNewCommits, l.MaxNewCommits)
		setInt(&e.Limits.MaxNewBlobs, l.MaxNewBlobs)
		if dur, err := ParseDuration(l.EvaluationTimeout); err == nil {
			e.Limits.EvaluationTimeoutSeconds = int(dur.Seconds())
		}
	}
	if m := s.Membership; m != nil {
		if dur, err := ParseDuration(m.SoftMaxAge); err == nil {
			e.Membership.SoftMaxAgeSeconds = int(dur.Seconds())
		}
		if dur, err := ParseDuration(m.HardMaxAge); err == nil {
			e.Membership.HardMaxAgeSeconds = int(dur.Seconds())
		}
		setStr(&e.Membership.OnUnavailable, m.OnUnavailable)
	}
	if a := s.Audit; a != nil {
		if a.Required != nil {
			e.Audit.Required = *a.Required
		}
		setInt(&e.Audit.RetentionDays, a.RetentionDays)
		if a.LogAccepted != nil {
			e.Audit.LogAccepted = *a.LogAccepted
		}
	}
	if x := s.Exceptions; x != nil {
		setInt(&e.ExceptionMaxLifetimeDays, x.MaxLifetimeDays)
	}
	if m := s.Messages; m != nil {
		setStr(&e.Messages.Header, m.Header)
		e.Messages.Support = m.Support
		for code, text := range m.Remediation {
			e.Messages.Remediation[code] = text
		}
	}
	return e
}

func setStr(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}

func setInt(dst *int, v *int) {
	if v != nil {
		*dst = *v
	}
}
