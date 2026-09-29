// Package membership reads the local GitLab group-membership cache.
//
// The cache is produced off the push path (Jenkins sync, Phase 10) and read
// here with local file I/O only: a push never waits on the GitLab API.
// Freshness rules (PHASE-1 §5): a stale cache may only restrict, never grant.
//
//	age <= soft_max_age        fresh     denies + allows applied
//	age <= hard_max_age        stale     denies + allows applied, reported
//	age >  hard_max_age        expired   denies applied, group allows/exceptions ignored
//	no loadable file           unavailable (policy decides: deny or ignore groups)
package membership

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/soroush67/git-policy/internal/fsutil"
	"github.com/soroush67/git-policy/internal/layout"
)

const (
	Schema  = "git-policy/membership/v1"
	maxSize = 64 << 20
)

// Freshness classifies the cache age.
type Freshness string

const (
	Fresh       Freshness = "fresh"
	Stale       Freshness = "stale"
	Expired     Freshness = "expired"
	Unavailable Freshness = "unavailable"
)

// File is membership/current.json.
type File struct {
	Schema      string               `json:"schema"`
	GeneratedAt time.Time            `json:"generated_at"`
	Generator   string               `json:"generator"`
	Source      map[string]string    `json:"source,omitempty"`
	Groups      []string             `json:"groups"`
	Users       map[string]UserEntry `json:"users"`
}

// UserEntry is one user's state and full group paths (inherited included).
type UserEntry struct {
	State  string   `json:"state"`
	Groups []string `json:"groups"`
}

// Cache is a loaded view of the membership file.
type Cache struct {
	File         *File
	Freshness    Freshness
	Age          time.Duration
	FromPrevious bool  // current.json was unusable, previous.json served
	Err          error // why current.json (or both) could not be used
}

// CurrentPath and PreviousPath locate the cache files.
func CurrentPath(l layout.Layout) string  { return filepath.Join(l.MembershipDir(), "current.json") }
func PreviousPath(l layout.Layout) string { return filepath.Join(l.MembershipDir(), "previous.json") }

// Load reads current.json, falling back to previous.json, and classifies it.
func Load(l layout.Layout, soft, hard time.Duration, now time.Time) *Cache {
	f, err := readFile(CurrentPath(l))
	c := &Cache{}
	if err != nil {
		c.Err = err
		if pf, perr := readFile(PreviousPath(l)); perr == nil {
			f, c.FromPrevious = pf, true
		}
	}
	if f == nil {
		c.Freshness = Unavailable
		return c
	}
	c.File = f
	c.Age = now.Sub(f.GeneratedAt)
	switch {
	case c.Age <= soft:
		c.Freshness = Fresh
	case c.Age <= hard:
		c.Freshness = Stale
	default:
		c.Freshness = Expired
	}
	return c
}

// Available reports whether any membership data is loaded.
func (c *Cache) Available() bool { return c.File != nil }

// GrantsUsable reports whether group allows / group exceptions may be honoured.
func (c *Cache) GrantsUsable() bool { return c.Freshness == Fresh || c.Freshness == Stale }

// GroupsOf returns the lower-cased group paths of a user (nil if unknown).
func (c *Cache) GroupsOf(user string) []string {
	if c.File == nil {
		return nil
	}
	return c.File.Users[strings.ToLower(user)].Groups
}

func readFile(path string) (*File, error) {
	b, err := fsutil.ReadFileNoFollow(path, maxSize)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s: not deployed", filepath.Base(path))
		}
		return nil, err
	}
	var f File
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: malformed: %w", filepath.Base(path), err)
	}
	if f.Schema != Schema {
		return nil, fmt.Errorf("%s: unknown schema %q", filepath.Base(path), f.Schema)
	}
	if f.GeneratedAt.IsZero() {
		return nil, fmt.Errorf("%s: generated_at missing", filepath.Base(path))
	}
	// Normalise: usernames and group paths are case-insensitive in GitLab.
	users := make(map[string]UserEntry, len(f.Users))
	for u, e := range f.Users {
		gs := make([]string, len(e.Groups))
		for i, g := range e.Groups {
			gs[i] = strings.ToLower(g)
		}
		users[strings.ToLower(u)] = UserEntry{State: e.State, Groups: gs}
	}
	f.Users = users
	return &f, nil
}
