package policy

import (
	"testing"
	"time"
)

func TestParseSize(t *testing.T) {
	good := map[string]int64{"1B": 1, "512KiB": 512 << 10, "20MiB": 20 << 20, "2GiB": 2 << 30}
	for in, want := range good {
		if got, err := ParseSize(in); err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
		if back := FormatSize(want); back != in {
			t.Errorf("FormatSize(%d) = %q; want %q", want, back, in)
		}
	}
	for _, in := range []string{"", "20", "20MB", "20mib", "0MiB", "-1MiB", "1.5MiB", "1234567MiB"} {
		if _, err := ParseSize(in); err == nil {
			t.Errorf("ParseSize(%q) accepted", in)
		}
	}
}

func TestParseDuration(t *testing.T) {
	good := map[string]time.Duration{"45s": 45 * time.Second, "30m": 30 * time.Minute, "2h": 2 * time.Hour}
	for in, want := range good {
		if got, err := ParseDuration(in); err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "1d", "0s", "1h30m", "5"} {
		if _, err := ParseDuration(in); err == nil {
			t.Errorf("ParseDuration(%q) accepted", in)
		}
	}
}

func TestCheckGitLabPath(t *testing.T) {
	cases := []struct {
		path string
		min  int
		want string
	}{
		{"finance", 1, ""},
		{"finance/payment-api", 2, ""},
		{"finance/team_a/api.v2", 2, ""},
		{"payment-api", 2, "V012"},
		{"finance/api.git", 2, "V011"},
		{"finance/api.atom", 2, "V011"},
		{"finance/api.", 2, "V011"},
		{"-finance", 1, "V005"},
		{"finance//api", 2, "V005"},
		{"finance/../etc", 2, "V005"},
		{"fin ance", 1, "V005"},
	}
	for _, c := range cases {
		if got, _ := checkGitLabPath(c.path, c.min); got != c.want {
			t.Errorf("checkGitLabPath(%q, %d) = %q; want %q", c.path, c.min, got, c.want)
		}
	}
}

func TestSafeText(t *testing.T) {
	if !safeText("Publish to Nexus.\nSee wiki.\tThanks") {
		t.Error("plain text rejected")
	}
	for _, s := range []string{"\x1b[31mred", "a\rb", "bell\a", "\x7f"} {
		if safeText(s) {
			t.Errorf("safeText(%q) accepted a control character", s)
		}
	}
}
