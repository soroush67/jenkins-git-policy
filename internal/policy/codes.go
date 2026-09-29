package policy

// Rule codes are the stable contract shared by rejection messages, audit
// events, exceptions and tests (docs/design/PHASE-2-SCHEMA.md §4).
const (
	MandatoryUserDenied  = "MANDATORY_USER_DENIED"
	MandatoryGroupDenied = "MANDATORY_GROUP_DENIED"
	UserPushDenied       = "USER_PUSH_DENIED"
	GroupPushDenied      = "GROUP_PUSH_DENIED"

	BlockedExtension = "BLOCKED_EXTENSION"
	BlockedPath      = "BLOCKED_PATH"
	BlockedSignature = "BLOCKED_SIGNATURE"
	FileTooLarge     = "FILE_TOO_LARGE"

	LimitRefUpdates = "LIMIT_REF_UPDATES"
	LimitCommits    = "LIMIT_COMMITS"
	LimitObjects    = "LIMIT_OBJECTS"
	EvalTimeout     = "EVAL_TIMEOUT"

	PolicyUnavailable     = "POLICY_UNAVAILABLE"
	MembershipUnavailable = "MEMBERSHIP_UNAVAILABLE"
	InvalidRefUpdate      = "INVALID_REF_UPDATE"
	InternalError         = "INTERNAL_ERROR"
	AuditUnavailable      = "AUDIT_UNAVAILABLE"
)

type ruleInfo struct {
	waivable    bool
	hasPath     bool // violations carry a file path (exception scope.paths applies)
	remediation string
}

var rules = map[string]ruleInfo{
	MandatoryUserDenied:  {true, false, "Your account is not permitted to push. Contact support."},
	MandatoryGroupDenied: {true, false, "Your account is not permitted to push. Contact support."},
	UserPushDenied:       {true, false, "You are not permitted to push to this project or ref."},
	GroupPushDenied:      {true, false, "Your group is not permitted to push to this project or ref."},

	BlockedExtension: {true, true, "Binary artifacts do not belong in Git. Publish them to the artifact repository (Nexus)."},
	BlockedPath:      {true, true, "Build output and vendored package folders must not be committed."},
	BlockedSignature: {true, true, "This file is a Windows executable or library regardless of its name. Publish it to Nexus."},
	FileTooLarge:     {true, true, "Store large files in the artifact repository (Nexus), not in Git."},

	LimitRefUpdates: {true, false, "Push fewer refs at once, or request a migration exception."},
	LimitCommits:    {true, false, "Push in smaller batches, or request a migration exception."},
	LimitObjects:    {true, false, "Push in smaller batches, or request a migration exception."},
	EvalTimeout:     {false, false, "Push in smaller batches."},

	PolicyUnavailable:     {false, false, "The Git policy service is unavailable. Contact the platform team."},
	AuditUnavailable:      {false, false, "The Git policy audit log is unavailable. Contact the platform team."},
	MembershipUnavailable: {false, false, "The Git policy service is unavailable. Contact the platform team."},
	InvalidRefUpdate:      {false, false, "Malformed ref update."},
	InternalError:         {false, false, "The Git policy service failed. Contact the platform team."},
}

// IsRuleCode reports whether c is a known rule code.
func IsRuleCode(c string) bool { _, ok := rules[c]; return ok }

// IsWaivable reports whether an exception may name rule code c.
func IsWaivable(c string) bool { return rules[c].waivable }

// HasPath reports whether violations of c are tied to a file path.
func HasPath(c string) bool { return rules[c].hasPath }

// DefaultRemediation returns the built-in developer guidance for c.
func DefaultRemediation(c string) string { return rules[c].remediation }
