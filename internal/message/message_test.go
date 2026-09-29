package message

import (
	"bytes"
	"strings"
	"testing"
)

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"Lib/Mic.Caching.dll":  "Lib/Mic.Caching.dll",
		"evil\x1b[31m.dll":     `evil\x1b[31m.dll`,
		"a\rGL-HOOK-ERR: fake": `a\x0dGL-HOOK-ERR: fake`,
		"bad\xffutf8":          `bad\xffutf8`,
		"فایل.dll":             "فایل.dll",
		"zero​width":           `zero\u{200b}width`,
	}
	for in, want := range cases {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q; want %q", in, got, want)
		}
	}
	if got := Sanitize(strings.Repeat("a", 1000)); len(got) > maxLineLen+3 {
		t.Errorf("not truncated: %d", len(got))
	}
}

func TestWriteSplitsAndPrefixesEveryLine(t *testing.T) {
	var b bytes.Buffer
	Write(&b, "first\nsecond")
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if !strings.HasPrefix(line, Prefix) {
			t.Errorf("unprefixed line %q", line)
		}
	}
}
