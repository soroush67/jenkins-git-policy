package policy

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Severity of a validation finding: errors block activation, warnings do not.
type Severity string

const (
	SevError   Severity = "error"
	SevWarning Severity = "warning"
)

// Finding is one validation result, identified by a stable V/W code.
type Finding struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Path     string   `json:"path,omitempty"`
	Line     int      `json:"line,omitempty"`
	Message  string   `json:"message"`
}

func (f Finding) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-7s %s", strings.ToUpper(string(f.Severity)), f.Code)
	if f.Path != "" {
		b.WriteString("  " + f.Path)
	}
	if f.Line > 0 {
		fmt.Fprintf(&b, " (line %d)", f.Line)
	}
	b.WriteString(": " + f.Message)
	return b.String()
}

// Count returns the number of errors and warnings in fs.
func Count(fs []Finding) (errs, warns int) {
	for _, f := range fs {
		if f.Severity == SevError {
			errs++
		} else {
			warns++
		}
	}
	return errs, warns
}

func errorf(code, path string, line int, format string, a ...any) Finding {
	return Finding{Code: code, Severity: SevError, Path: path, Line: line, Message: fmt.Sprintf(format, a...)}
}

var simpleKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// joinPath renders a document path: a.b, a["finance/x"], a[3].
func joinPath(base, key string) string {
	if !simpleKey.MatchString(key) {
		return base + "[" + strconv.Quote(key) + "]"
	}
	if base == "" {
		return key
	}
	return base + "." + key
}

func index(base string, i int) string { return fmt.Sprintf("%s[%d]", base, i) }
