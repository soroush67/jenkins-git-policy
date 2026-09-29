package policy

// Document is the policy file as written by humans (apiVersion git-policy/v1).
// It is decoded strictly (unknown keys are errors) and never used directly at
// push time: Compile turns it into the normalised Compiled form.
type Document struct {
	APIVersion string                   `yaml:"apiVersion"`
	Kind       string                   `yaml:"kind"`
	Metadata   *Metadata                `yaml:"metadata"`
	Settings   *Settings                `yaml:"settings"`
	Mandatory  *Mandatory               `yaml:"mandatory"`
	Defaults   *Defaults                `yaml:"defaults"`
	Namespaces map[string]*Scope        `yaml:"namespaces"`
	Projects   map[string]*ProjectScope `yaml:"projects"`
	Users      map[string]*IdentityRule `yaml:"users"`
	Groups     map[string]*IdentityRule `yaml:"groups"`
	Exceptions []*Exception             `yaml:"exceptions"`
}

const (
	APIVersion = "git-policy/v1"
	Kind       = "GitPolicy"
)

type Metadata struct {
	Name        string `yaml:"name"`
	Revision    int    `yaml:"revision"`
	Description string `yaml:"description"`
}

type Settings struct {
	Mode            string              `yaml:"mode"`
	RepositoryTypes *RepositoryTypes    `yaml:"repository_types"`
	Limits          *Limits             `yaml:"limits"`
	Membership      *MembershipSettings `yaml:"membership"`
	Audit           *AuditSettings      `yaml:"audit"`
	Exceptions      *ExceptionSettings  `yaml:"exceptions"`
	Messages        *Messages           `yaml:"messages"`
}

type RepositoryTypes struct {
	Project string `yaml:"project"`
	Wiki    string `yaml:"wiki"`
	Snippet string `yaml:"snippet"`
	Design  string `yaml:"design"`
}

type Limits struct {
	MaxRefUpdates     *int   `yaml:"max_ref_updates"`
	MaxNewCommits     *int   `yaml:"max_new_commits"`
	MaxNewBlobs       *int   `yaml:"max_new_blobs"`
	EvaluationTimeout string `yaml:"evaluation_timeout"`
}

type MembershipSettings struct {
	SoftMaxAge    string `yaml:"soft_max_age"`
	HardMaxAge    string `yaml:"hard_max_age"`
	OnUnavailable string `yaml:"on_unavailable"`
}

type AuditSettings struct {
	Required      *bool `yaml:"required"`
	RetentionDays *int  `yaml:"retention_days"`
	LogAccepted   *bool `yaml:"log_accepted"`
}

type ExceptionSettings struct {
	MaxLifetimeDays *int `yaml:"max_lifetime_days"`
}

type Messages struct {
	Header      string            `yaml:"header"`
	Support     string            `yaml:"support"`
	Remediation map[string]string `yaml:"remediation"`
}

// BlockList is shared by every content layer.
type BlockList struct {
	BlockedExtensions []string `yaml:"blocked_extensions"`
	BlockedPaths      []string `yaml:"blocked_paths"`
	BlockedSignatures []string `yaml:"blocked_signatures"`
}

// UnblockList is allowed below defaults only.
type UnblockList struct {
	UnblockExtensions []string `yaml:"unblock_extensions"`
	UnblockPaths      []string `yaml:"unblock_paths"`
	UnblockSignatures []string `yaml:"unblock_signatures"`
}

type Mandatory struct {
	Mode        string `yaml:"mode"`
	BlockList   `yaml:",inline"`
	MaxFileSize string   `yaml:"max_file_size"`
	DenyUsers   []string `yaml:"deny_users"`
	DenyGroups  []string `yaml:"deny_groups"`
}

type Defaults struct {
	BlockList   `yaml:",inline"`
	MaxFileSize string               `yaml:"max_file_size"`
	Refs        map[string]*RefScope `yaml:"refs"`
}

// RefScope is a content layer bound to a ref glob (no further nesting).
type RefScope struct {
	Mode        string `yaml:"mode"`
	BlockList   `yaml:",inline"`
	UnblockList `yaml:",inline"`
	MaxFileSize string `yaml:"max_file_size"`
}

// Scope is a namespace content layer.
type Scope struct {
	RefScope `yaml:",inline"`
	Refs     map[string]*RefScope `yaml:"refs"`
}

// ProjectScope is a project content layer.
type ProjectScope struct {
	Scope   `yaml:",inline"`
	Enabled *bool `yaml:"enabled"`
}

// IdentityRule is the rule tree of one user or group.
type IdentityRule struct {
	Push       string                     `yaml:"push"`
	Refs       map[string]string          `yaml:"refs"`
	Namespaces map[string]*ScopedIdentity `yaml:"namespaces"`
	Projects   map[string]*ScopedIdentity `yaml:"projects"`
}

type ScopedIdentity struct {
	Push string            `yaml:"push"`
	Refs map[string]string `yaml:"refs"`
}

type Exception struct {
	ID          string          `yaml:"id"`
	Rules       []string        `yaml:"rules"`
	Mandatory   bool            `yaml:"mandatory"`
	Subjects    *Subjects       `yaml:"subjects"`
	Scope       *ExceptionScope `yaml:"scope"`
	MaxFileSize string          `yaml:"max_file_size"`
	Reason      string          `yaml:"reason"`
	Ticket      string          `yaml:"ticket"`
	ApprovedBy  string          `yaml:"approved_by"`
	Expires     string          `yaml:"expires"`
}

type Subjects struct {
	Users  []string `yaml:"users"`
	Groups []string `yaml:"groups"`
}

type ExceptionScope struct {
	Namespaces []string `yaml:"namespaces"`
	Projects   []string `yaml:"projects"`
	Refs       []string `yaml:"refs"`
	Paths      []string `yaml:"paths"`
}
