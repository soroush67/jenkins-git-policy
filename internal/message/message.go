// Package message renders developer-facing hook output.
//
// Every line starts with GL-HOOK-ERR: so GitLab shows it in the CLI and the
// web UI. Everything printed may contain attacker-controlled bytes (file
// names, ref names), so each line is sanitised: control characters and invalid
// UTF-8 are escaped, preventing terminal-escape injection and forged lines.
package message

import (
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	Prefix     = "GL-HOOK-ERR: "
	maxLineLen = 300
)

// Sanitize escapes non-printable characters as \xNN / \u{NNNN} and truncates.
func Sanitize(s string) string {
	var b strings.Builder
	n := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size <= 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r < 0x80 && !unicode.IsPrint(r):
			fmt.Fprintf(&b, `\x%02x`, r)
		case !unicode.IsPrint(r) && r != ' ':
			fmt.Fprintf(&b, `\u{%04x}`, r)
		default:
			b.WriteRune(r)
		}
		i += size
		if n++; n >= maxLineLen {
			b.WriteString("...")
			break
		}
	}
	return b.String()
}

// Write prints lines with the GitLab prefix. Embedded newlines start new
// prefixed lines; each resulting line is sanitised.
func Write(w io.Writer, lines ...string) {
	for _, l := range lines {
		for _, part := range strings.Split(l, "\n") {
			fmt.Fprintf(w, "%s%s\n", Prefix, Sanitize(part))
		}
	}
}

// Item is one rejected rule occurrence.
type Item struct {
	Rule   string
	Ref    string
	Commit string
	File   string
	Detail string
}

// Report is a complete rejection for one push.
type Report struct {
	Header      string
	User        string
	Project     string
	Items       []Item
	Remediation map[string]string // rule code -> required action
	Support     string
}

// maxItems caps the listed violations; the audit log has all of them.
const maxItems = 20

// WriteReport prints a push rejection: who/where once, then each violation,
// then the required action per distinct rule.
func WriteReport(w io.Writer, r Report) {
	lines := []string{r.Header}
	if r.User != "" {
		lines = append(lines, "User: "+r.User)
	}
	if r.Project != "" {
		lines = append(lines, "Project: "+r.Project)
	}
	var codes []string
	seen := map[string]bool{}
	for i, it := range r.Items {
		if !seen[it.Rule] {
			seen[it.Rule] = true
			codes = append(codes, it.Rule)
		}
		if i >= maxItems {
			continue
		}
		lines = append(lines, "")
		lines = append(lines, "Rule: "+it.Rule)
		if it.Ref != "" {
			lines = append(lines, "Ref: "+it.Ref)
		}
		if it.Commit != "" {
			lines = append(lines, "Commit: "+it.Commit)
		}
		if it.File != "" {
			lines = append(lines, "File: "+it.File)
		}
		if it.Detail != "" {
			lines = append(lines, it.Detail)
		}
	}
	if n := len(r.Items) - maxItems; n > 0 {
		lines = append(lines, "", fmt.Sprintf("... and %d more violation(s)", n))
	}
	for _, c := range codes {
		if a := r.Remediation[c]; a != "" {
			lines = append(lines, "", "Required action ("+c+"): "+a)
		}
	}
	if r.Support != "" {
		lines = append(lines, r.Support)
	}
	Write(w, lines...)
}

// Rejection is a rendered rejection for one rule.
type Rejection struct {
	Header      string // settings.messages.header
	Rule        string // rule code
	Details     []string
	Remediation string
	Support     string
}

// WriteRejection prints a rejection block.
func WriteRejection(w io.Writer, r Rejection) {
	lines := []string{r.Header, "Rule: " + r.Rule}
	lines = append(lines, r.Details...)
	if r.Remediation != "" {
		lines = append(lines, "Required action: "+r.Remediation)
	}
	if r.Support != "" {
		lines = append(lines, r.Support)
	}
	Write(w, lines...)
}
