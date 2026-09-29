// Package state manages the global engine switch (state/engine.json).
//
// Failure semantics (PHASE-1 §6): a missing state file means ENABLED, so
// deleting it cannot switch enforcement off; an unreadable or malformed file
// is an error and the hook fails closed. A disable always carries an expiry
// (except the initial install state) and silently ends when it passes.
package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/soroush67/git-policy/internal/fsutil"
)

const (
	Schema = "git-policy/state/v1"
	// MaxDisableTTL bounds how long a disable can last.
	MaxDisableTTL = 7 * 24 * time.Hour
	maxStateSize  = 64 << 10
)

// State is the persisted engine switch.
type State struct {
	Schema    string     `json:"schema"`
	Enabled   bool       `json:"enabled"`
	ChangedAt time.Time  `json:"changed_at"`
	ChangedBy string     `json:"changed_by"`
	Reason    string     `json:"reason"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// View is a loaded state plus how it was obtained.
type View struct {
	State
	Present bool // false: no state file, engine defaults to enabled
}

// Load reads the state file. A missing file yields an enabled default view;
// any other problem is returned as an error.
func Load(path string) (View, error) {
	b, err := fsutil.ReadFileNoFollow(path, maxStateSize)
	if errors.Is(err, fs.ErrNotExist) {
		return View{State: State{Schema: Schema, Enabled: true}}, nil
	}
	if err != nil {
		return View{}, err
	}
	var s State
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return View{}, fmt.Errorf("state file %s is malformed: %w", path, err)
	}
	if s.Schema != Schema {
		return View{}, fmt.Errorf("state file %s has unknown schema %q", path, s.Schema)
	}
	return View{State: s, Present: true}, nil
}

// EnabledAt reports whether enforcement is on at time now: explicitly
// enabled, or disabled with an expiry that has passed.
func (v View) EnabledAt(now time.Time) bool {
	return v.Enabled || v.DisableExpired(now)
}

// DisableExpired reports whether a time-limited disable has ended.
func (v View) DisableExpired(now time.Time) bool {
	return !v.Enabled && v.ExpiresAt != nil && !now.Before(*v.ExpiresAt)
}

// Save writes the state atomically.
func Save(path string, s State, owner *fsutil.Owner) error {
	s.Schema = Schema
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, append(b, '\n'), 0o640, owner)
}
