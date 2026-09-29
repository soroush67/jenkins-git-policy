package admin

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/soroush67/git-policy/internal/layout"
	"github.com/soroush67/git-policy/internal/membership"
	"github.com/soroush67/git-policy/internal/store"
)

func memFile(generated time.Time, users map[string][]string, groups ...string) []byte {
	var parts []string
	for u, gs := range users {
		parts = append(parts, fmt.Sprintf("%q:{\"state\":\"active\",\"groups\":[\"%s\"]}", u, strings.Join(gs, `","`)))
	}
	return []byte(fmt.Sprintf(`{"schema":"git-policy/membership/v1","generated_at":%q,"generator":"t","groups":["%s"],"users":{%s}}`,
		generated.UTC().Format(time.RFC3339), strings.Join(groups, `","`), strings.Join(parts, ",")))
}

func newCtx(t *testing.T) *Context {
	t.Helper()
	root := t.TempDir()
	t.Cleanup(func() { os.Chmod(root, 0o755); exec(root) })
	c, err := NewContext(root, root+"/hook", "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range c.L.Dirs() {
		os.MkdirAll(d.Path, 0o750)
	}
	return c
}

// exec makes read-only version dirs deletable for t.TempDir cleanup.
func exec(root string) {
	entries, _ := os.ReadDir(root + "/policies")
	for _, e := range entries {
		os.Chmod(root+"/policies/"+e.Name(), 0o750)
	}
}

func users(n int, group string) map[string][]string {
	m := map[string][]string{}
	for i := 0; i < n; i++ {
		m[fmt.Sprintf("u%d", i)] = []string{group}
	}
	return m
}

func TestApplyMembershipGuard(t *testing.T) {
	c := newCtx(t)
	now := time.Now()
	store.Apply(c.L, store.ApplyRequest{Now: now, Actor: "t", Source: []byte(
		"apiVersion: git-policy/v1\nkind: GitPolicy\nmetadata: {name: t, revision: 1}\ngroups: {contractors: {push: deny}}\n")})

	if _, err := c.ApplyMembership(memFile(now, users(20, "contractors"), "contractors"), false); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	// 20 -> 10 memberships (50% drop): refused.
	_, err := c.ApplyMembership(memFile(now, users(10, "contractors"), "contractors"), false)
	if err == nil || !strings.Contains(err.Error(), "dropped from 20 to 10") {
		t.Fatalf("drop guard: %v", err)
	}
	// 20 -> 16 (20% drop): accepted, previous kept.
	if _, err := c.ApplyMembership(memFile(now, users(16, "contractors"), "contractors"), false); err != nil {
		t.Fatalf("small drop: %v", err)
	}
	prev, _ := os.ReadFile(membership.PreviousPath(c.L))
	if f, err := membership.Parse(prev); err != nil || f.Pairs() != 20 {
		t.Fatalf("previous.json must hold the prior cache: %v", err)
	}
	// Group used by the policy missing from the file: refused, --force overrides.
	if _, err := c.ApplyMembership(memFile(now, users(16, "other"), "other"), false); err == nil || !strings.Contains(err.Error(), `"contractors"`) {
		t.Fatalf("missing referenced group: %v", err)
	}
	if res, err := c.ApplyMembership(memFile(now, users(3, "other"), "other"), true); err != nil || !res.Forced {
		t.Fatalf("--force: %v", err)
	}
	// Stale data (older than hard_max_age 24h) refused; future data refused.
	if _, err := c.ApplyMembership(memFile(now.Add(-48*time.Hour), users(3, "contractors"), "contractors"), false); err == nil || !strings.Contains(err.Error(), "hard_max_age") {
		t.Fatalf("stale: %v", err)
	}
	if _, err := c.ApplyMembership(memFile(now.Add(time.Hour), users(3, "contractors"), "contractors"), true); err == nil || !strings.Contains(err.Error(), "future") {
		t.Fatalf("future: %v", err)
	}
	if _, err := c.ApplyMembership([]byte(`{"schema":"x"}`), true); err == nil {
		t.Fatal("malformed file accepted")
	}
	_ = layout.DefaultRoot
}
