package policy

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	segmentRe   = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,254}$`)
	userRe      = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,254}$`)
	extensionRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_+-]*(\.[a-z0-9_+-]+)*$`)
	refGlobRe   = regexp.MustCompile(`^refs/[A-Za-z0-9._/*?@+-]+$`)
	nameRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	exceptionRe = regexp.MustCompile(`^[A-Z][A-Z0-9-]{2,63}$`)
	ticketRe    = regexp.MustCompile(`^[A-Za-z0-9._#/-]{1,64}$`)
	sizeRe      = regexp.MustCompile(`^([1-9][0-9]{0,5})(B|KiB|MiB|GiB)$`)
	durationRe  = regexp.MustCompile(`^([1-9][0-9]{0,4})(s|m|h)$`)
)

// UnknownUser is the reserved subject for pushes without GL_USERNAME.
const UnknownUser = "@unknown"

const (
	maxNamespaceDepth = 20
	maxTextLen        = 1000
)

// checkGitLabPath validates a namespace or project path. minSegments is 1 for
// namespaces/groups and 2 for projects. It returns a V-code and message, or "".
func checkGitLabPath(p string, minSegments int) (string, string) {
	if len(p) > 1024 {
		return "V005", "path is too long"
	}
	segs := strings.Split(p, "/")
	if len(segs) > maxNamespaceDepth+1 {
		return "V005", fmt.Sprintf("path has more than %d components", maxNamespaceDepth+1)
	}
	for _, s := range segs {
		if !segmentRe.MatchString(s) {
			return "V005", fmt.Sprintf("invalid path component %q (letters, digits, '_', '-', '.'; must not start with '-' or '.')", s)
		}
		ls := strings.ToLower(s)
		if strings.HasSuffix(ls, ".") || strings.HasSuffix(ls, ".git") || strings.HasSuffix(ls, ".atom") {
			return "V011", fmt.Sprintf("path component %q cannot end with '.', '.git' or '.atom' in GitLab", s)
		}
	}
	if len(segs) < minSegments {
		return "V012", "project path must be <namespace>/<project>"
	}
	return "", ""
}

func validUser(u string) bool { return u == UnknownUser || userRe.MatchString(u) }

// NormalizeExtension lower-cases and strips one leading dot.
func NormalizeExtension(e string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e), "."))
}

// ParseSize parses a binary-unit size such as 20MiB into bytes.
func ParseSize(s string) (int64, error) {
	m := sizeRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("invalid size %q (use B, KiB, MiB or GiB, e.g. 20MiB)", s)
	}
	n, _ := strconv.ParseInt(m[1], 10, 64)
	switch m[2] {
	case "KiB":
		n <<= 10
	case "MiB":
		n <<= 20
	case "GiB":
		n <<= 30
	}
	return n, nil
}

// FormatSize renders bytes with the largest exact binary unit.
func FormatSize(n int64) string {
	switch {
	case n >= 1<<30 && n%(1<<30) == 0:
		return fmt.Sprintf("%dGiB", n>>30)
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%dMiB", n>>20)
	case n >= 1<<10 && n%(1<<10) == 0:
		return fmt.Sprintf("%dKiB", n>>10)
	}
	return fmt.Sprintf("%dB", n)
}

// ParseDuration parses 45s / 30m / 2h.
func ParseDuration(s string) (time.Duration, error) {
	m := durationRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("invalid duration %q (use s, m or h, e.g. 45s, 2h)", s)
	}
	n, _ := strconv.Atoi(m[1])
	unit := map[string]time.Duration{"s": time.Second, "m": time.Minute, "h": time.Hour}[m[2]]
	return time.Duration(n) * unit, nil
}

// ParseDate parses YYYY-MM-DD as a UTC date.
func ParseDate(s string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q (use YYYY-MM-DD)", s)
	}
	return t.UTC(), nil
}

// EndOfDay is the last second of the UTC day d: exceptions are valid through it.
func EndOfDay(d time.Time) time.Time { return d.Add(24*time.Hour - time.Second) }

// safeText rejects control characters (except tab/newline) and overlong text:
// messages are printed to every pusher's terminal.
func safeText(s string) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxTextLen {
		return false
	}
	for _, r := range s {
		if (r < 0x20 && r != '\t' && r != '\n') || r == 0x7f {
			return false
		}
	}
	return true
}
