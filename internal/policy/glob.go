package policy

import (
	"errors"
	"regexp"
	"strings"
)

// Glob syntax shared by ref globs and path globs:
//
//	*    any run of characters within one path component
//	?    exactly one character within one path component
//	**   as a whole component: zero or more components
//	     (trailing "a/**" means one or more components below a, not a itself)
//
// Patterns are anchored at the start: "*.dll" matches only top-level files,
// "**/*.dll" matches at any depth. No character classes or braces.

// CheckGlob validates glob syntax.
func CheckGlob(p string) error {
	if p == "" {
		return errors.New("empty pattern")
	}
	if strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") {
		return errors.New("pattern must not start or end with '/'")
	}
	if strings.ContainsAny(p, "[]{}\\") {
		return errors.New("character classes, braces and escapes are not supported")
	}
	for _, seg := range strings.Split(p, "/") {
		switch {
		case seg == "":
			return errors.New("empty path component ('//')")
		case seg == "." || seg == "..":
			return errors.New("'.' and '..' components are not allowed")
		case strings.Contains(seg, "**") && seg != "**":
			return errors.New("'**' must be a whole path component")
		}
	}
	return nil
}

// CompileGlob translates a glob to an anchored regexp.
func CompileGlob(p string, foldCase bool) (*regexp.Regexp, error) {
	if err := CheckGlob(p); err != nil {
		return nil, err
	}
	parts := strings.Split(p, "/")
	var b strings.Builder
	if foldCase {
		b.WriteString("(?i)")
	}
	b.WriteString("^")
	for i, s := range parts {
		last := i == len(parts)-1
		if s == "**" {
			switch {
			case len(parts) == 1:
				b.WriteString(".+")
			case i == 0:
				b.WriteString("(?:[^/]+/)*")
			case last:
				b.WriteString("(?:/[^/]+)+")
			default:
				b.WriteString("(?:/[^/]+)*")
			}
			continue
		}
		if i > 0 && !(i == 1 && parts[0] == "**") {
			b.WriteString("/")
		}
		for _, r := range s {
			switch r {
			case '*':
				b.WriteString("[^/]*")
			case '?':
				b.WriteString("[^/]")
			default:
				b.WriteString(regexp.QuoteMeta(string(r)))
			}
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}
