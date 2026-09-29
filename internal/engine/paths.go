package engine

import (
	"sort"
	"strings"

	"github.com/soroush67/git-policy/internal/policy"
)

// PathEntry is one path a push introduces (from the Phase 6 traversal).
type PathEntry struct {
	Ref    string
	Commit string
	Path   string // raw repository path; "" for a tag pointing directly at a blob
}

// maxRecorded bounds per-push violations kept in memory, reported and
// audited; a single push can add 100k binaries.
const maxRecorded = 1000

// Normalize returns the Windows-effective form of a path (PHASE-2 §1.3):
// per component, an NTFS stream suffix (":stream", "::$DATA") is removed and
// trailing dots and spaces are stripped (NTFS ignores them), then the whole
// path is case-folded.
func Normalize(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if j := strings.IndexByte(p, ':'); j >= 0 {
			p = p[:j]
		}
		parts[i] = strings.TrimRight(p, ". ")
	}
	return strings.ToLower(strings.Join(parts, "/"))
}

// Extensions returns every extension candidate of a path's basename, for
// both the raw (case-folded) and the normalized form: "a.tar.gz" yields
// "gz" and "tar.gz"; "evil.dll." and "evil.dll::$DATA" yield "dll".
func Extensions(path string) []string {
	set := map[string]bool{}
	for _, p := range []string{strings.ToLower(path), Normalize(path)} {
		base := p[strings.LastIndexByte(p, '/')+1:]
		parts := strings.Split(base, ".")
		for i := 1; i < len(parts); i++ {
			if ext := strings.Join(parts[i:], "."); ext != "" {
				set[ext] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for e := range set {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

// CheckPaths applies extension and path rules to the introduced paths.
// Each finding goes through exceptions; enforce-mode findings become
// violations (reject), audit-mode findings become WouldReject.
func (e *Engine) CheckPaths(d *Decision, r Request, cls *Classifier, entries []PathEntry) {
	for _, en := range entries {
		if en.Path == "" {
			continue // bare blob (tag -> blob): size/signature rules only (Phase 8)
		}
		if len(d.Violations)+len(d.WouldReject) >= maxRecorded {
			d.Truncated = true
			return
		}
		c := cls.Content(cls.ClassOf(en.Ref))
		if len(c.Extensions)+len(c.Paths) == 0 {
			continue
		}
		for _, ext := range Extensions(en.Path) {
			if src, ok := c.Extensions[ext]; ok {
				e.contentFinding(d, r, c, src, Violation{Code: policy.BlockedExtension, Ref: en.Ref, New: en.Commit,
					Path: en.Path, Detail: "Blocked extension: ." + ext})
				break // one extension finding per path is enough
			}
		}
		raw, norm := en.Path, Normalize(en.Path)
		globs := make([]string, 0, len(c.Paths))
		for g := range c.Paths {
			globs = append(globs, g)
		}
		sort.Strings(globs)
		for _, g := range globs {
			if e.match(g, raw, true) || e.match(g, norm, true) {
				e.contentFinding(d, r, c, c.Paths[g], Violation{Code: policy.BlockedPath, Ref: en.Ref, New: en.Commit,
					Path: en.Path, Detail: "Blocked path pattern: " + g})
				break
			}
		}
	}
}

// contentFinding records one content rule hit with its mode.
func (e *Engine) contentFinding(d *Decision, r Request, c Content, src Entry, v Violation) {
	v.Mandatory = src.Mandatory
	v.Source = src.Source
	mode := c.Mode
	if src.Mandatory {
		mode = c.MandatoryMode
	}
	if x := e.findException(d, r, lowerAll(r.Membership.Groups), v); x != nil {
		d.Waived = append(d.Waived, Waiver{Violation: v, ExceptionID: x.ID})
		return
	}
	if mode == policy.ModeAudit {
		d.WouldReject = append(d.WouldReject, v)
		return
	}
	d.Violations = append(d.Violations, v)
}
