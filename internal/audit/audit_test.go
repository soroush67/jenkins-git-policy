package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendAddsIdentityFields(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, nil, PushAccepted, map[string]any{"user": "bob"}); err != nil {
		t.Fatal(err)
	}
	if err := Append(dir, nil, PushAccepted, map[string]any{"user": "bob"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(FileName(dir, time.Now()))
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	ids := map[string]bool{}
	for _, l := range lines {
		var ev map[string]any
		if err := json.Unmarshal([]byte(l), &ev); err != nil {
			t.Fatal(err)
		}
		if ev["schema"] != Schema || ev["action"] != PushAccepted || ev["host"] == "" || ev["timestamp"] == "" {
			t.Fatalf("missing fields: %v", ev)
		}
		ids[ev["event_id"].(string)] = true
	}
	if len(ids) != 2 {
		t.Fatalf("event_id must be unique per event: %v", ids)
	}
}

func TestPrune(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	for _, name := range []string{
		"audit-2026-06-30.jsonl", // 91 days old -> pruned (retention 90)
		"audit-2026-07-01.jsonl", // exactly 90 days -> kept
		"audit-2026-09-29.jsonl", // today
		"break-glass.log",        // never touched
		"audit-garbage.jsonl",    // not a dated file -> ignored
	} {
		os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o640)
	}
	removed, err := Prune(dir, 90, now)
	if err != nil || len(removed) != 1 || removed[0] != "audit-2026-06-30.jsonl" {
		t.Fatalf("removed %v, %v", removed, err)
	}
	left, _ := os.ReadDir(dir)
	if len(left) != 4 {
		t.Fatalf("left %d files", len(left))
	}
	if _, err := Prune(dir, 0, now); err == nil {
		t.Fatal("retention 0 must be refused")
	}
}
