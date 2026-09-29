package hook

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/soroush67/git-policy/internal/engine"
	"github.com/soroush67/git-policy/internal/gitscan"
	"github.com/soroush67/git-policy/internal/policy"
)

// ScanOutcome is the result of inspecting the objects a push introduces.
type ScanOutcome struct {
	Skipped    bool   // no content rule applies to any updated ref
	SkipReason string //
	Result     *gitscan.Result
	Duration   time.Duration
}

// ScanPush runs the anti-bypass traversal for a push that passed identity
// checks. Limit and timeout violations are recorded on d; any other failure
// is recorded as INTERNAL_ERROR (fail closed). dir is the repository
// ("" = current directory, which is where Gitaly runs hooks).
func ScanPush(eng *engine.Engine, d *engine.Decision, req engine.Request, dir string) *ScanOutcome {
	out := &ScanOutcome{}
	if d.RepoMode == policy.RepoNone {
		out.Skipped, out.SkipReason = true, "repository type not evaluated"
		return out
	}
	cls := eng.Classifier(d.Project, d.RepoMode)
	needed := false
	var updates []gitscan.Update
	for _, u := range req.Updates {
		updates = append(updates, gitscan.Update{Old: u.Old, New: u.New, Ref: u.Ref})
		if !IsZero(u.New) && cls.Content(cls.ClassOf(u.Ref)).HasRules() {
			needed = true
		}
	}
	if !needed {
		out.Skipped, out.SkipReason = true, "no content rules apply to the updated refs"
		return out
	}

	git, err := gitscan.FindGit()
	if err != nil {
		d.Violations = append(d.Violations, engine.Violation{Code: policy.InternalError, Source: "gitscan", Detail: err.Error()})
		return out
	}
	lim := eng.P.Settings.Limits
	timeout := time.Duration(lim.EvaluationTimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()
	res, err := gitscan.Scan(ctx, gitscan.Options{
		Git: git, Dir: dir,
		MaxCommits: lim.MaxNewCommits, MaxBlobs: lim.MaxNewBlobs,
		ClassOf: cls.ClassOf, Covers: cls.Covers,
		OnLimit: func(code string, n, limit int) bool {
			return eng.Record(d, req, engine.Violation{Code: code,
				Source: "settings.limits", Detail: fmt.Sprintf("more than %d, limit %d", n, limit)})
		},
	}, updates)
	out.Duration = time.Since(start)
	var le *gitscan.LimitError
	switch {
	case err == nil:
		out.Result = res
	case errors.As(err, &le):
		// already recorded (unwaived) by OnLimit
	case errors.Is(err, context.DeadlineExceeded):
		d.Violations = append(d.Violations, engine.Violation{Code: policy.EvalTimeout, Source: "settings.limits.evaluation_timeout",
			Detail: fmt.Sprintf("object inspection exceeded %s", timeout)})
	default:
		d.Violations = append(d.Violations, engine.Violation{Code: policy.InternalError, Source: "gitscan", Detail: err.Error()})
	}
	return out
}
