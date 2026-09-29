package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadSemantics(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "engine.json")
	now := time.Now()

	v, err := Load(p)
	if err != nil || v.Present || !v.EnabledAt(now) {
		t.Fatalf("missing file must mean enabled: %+v %v", v, err)
	}

	exp := now.Add(time.Hour)
	if err := Save(p, State{Enabled: false, ExpiresAt: &exp, Reason: "maintenance"}, nil); err != nil {
		t.Fatal(err)
	}
	v, err = Load(p)
	if err != nil || !v.Present || v.EnabledAt(now) || !v.EnabledAt(now.Add(2*time.Hour)) {
		t.Fatalf("time-limited disable: %+v %v", v, err)
	}

	for name, content := range map[string]string{
		"garbage":        "{nope",
		"unknown field":  `{"schema":"git-policy/state/v1","enabled":false,"bypass":true}`,
		"unknown schema": `{"schema":"other","enabled":false}`,
	} {
		os.WriteFile(p, []byte(content), 0o640)
		if _, err := Load(p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	os.Remove(p)
	target := filepath.Join(dir, "elsewhere.json")
	Save(target, State{Enabled: false}, nil)
	os.Symlink(target, p)
	if _, err := Load(p); err == nil {
		t.Error("symlinked state file accepted")
	}
}
