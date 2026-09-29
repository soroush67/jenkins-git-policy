package membership

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/soroush67/git-policy/internal/layout"
)

func write(t *testing.T, path string, generated time.Time) {
	t.Helper()
	body := fmt.Sprintf(`{"schema":"git-policy/membership/v1","generated_at":%q,"generator":"test",
"groups":["contractors"],"users":{"Carol":{"state":"active","groups":["Contractors","Outsourcing/Team"]}}}`,
		generated.Format(time.RFC3339))
	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
}

func TestFreshnessAndFallback(t *testing.T) {
	l := layout.New(t.TempDir())
	os.MkdirAll(l.MembershipDir(), 0o750)
	now := time.Now()
	soft, hard := 2*time.Hour, 24*time.Hour

	if c := Load(l, soft, hard, now); c.Available() || c.Freshness != Unavailable || c.GrantsUsable() {
		t.Fatalf("no file: %+v", c)
	}
	for _, tc := range []struct {
		age  time.Duration
		want Freshness
	}{{time.Minute, Fresh}, {5 * time.Hour, Stale}, {48 * time.Hour, Expired}} {
		write(t, CurrentPath(l), now.Add(-tc.age))
		c := Load(l, soft, hard, now)
		if c.Freshness != tc.want {
			t.Errorf("age %s: %s, want %s", tc.age, c.Freshness, tc.want)
		}
		if got := c.GrantsUsable(); got != (tc.want != Expired) {
			t.Errorf("age %s: GrantsUsable=%v", tc.age, got)
		}
	}
	c := Load(l, soft, hard, now)
	if g := c.GroupsOf("CAROL"); len(g) != 2 || g[0] != "contractors" || g[1] != "outsourcing/team" {
		t.Errorf("groups not case-folded: %v", g)
	}

	// Corrupt current.json -> previous.json is served.
	write(t, PreviousPath(l), now.Add(-time.Minute))
	os.WriteFile(CurrentPath(l), []byte("{broken"), 0o640)
	c = Load(l, soft, hard, now)
	if !c.Available() || !c.FromPrevious || c.Err == nil {
		t.Fatalf("fallback: %+v", c)
	}
	os.WriteFile(CurrentPath(l), []byte(`{"schema":"git-policy/membership/v1","generated_at":"2026-01-01T00:00:00Z","users":{},"extra":1}`), 0o640)
	if c := Load(l, soft, hard, now); !c.FromPrevious {
		t.Error("unknown fields must make current.json unusable")
	}
}
