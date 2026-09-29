// Package store keeps deployed policy versions and the ACTIVE pointer.
//
//	policies/
//	  ACTIVE      "000012"  – replaced by atomic rename
//	  PREVIOUS    "000011"  – the version ACTIVE pointed to before the last switch
//	  000011/     policy.yaml compiled.json meta.json   (immutable, 0550/0440)
//	  000012/     ...
//
// A reader resolves ACTIVE once and then reads an immutable directory, so it
// always sees one complete, validated policy. There are no locks on the read
// path; writers (admin commands) serialize on state/admin.lock.
package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/soroush67/git-policy/internal/fsutil"
	"github.com/soroush67/git-policy/internal/layout"
	"github.com/soroush67/git-policy/internal/policy"
	"github.com/soroush67/git-policy/internal/version"
)

const (
	MetaSchema      = "git-policy/version-meta/v1"
	previousPointer = "PREVIOUS"
	maxCompiledSize = 16 << 20
	maxMetaSize     = 64 << 10
)

var versionRe = regexp.MustCompile(`^[0-9]{6}$`)

// Meta describes one deployed version.
type Meta struct {
	Schema         string    `json:"schema"`
	Version        int       `json:"version"`
	Name           string    `json:"name"`
	Revision       int       `json:"revision"`
	SourceSHA256   string    `json:"source_sha256"`
	CompiledSHA256 string    `json:"compiled_sha256"`
	DeployedAt     time.Time `json:"deployed_at"`
	DeployedBy     string    `json:"deployed_by"`
	Reason         string    `json:"reason,omitempty"`
	EngineVersion  string    `json:"engine_version"`
	Warnings       int       `json:"warnings"`
}

// Loaded is a verified policy version.
type Loaded struct {
	Version int
	Meta    Meta
	Policy  *policy.Compiled
}

// Active is what the push path uses. FellBack means ACTIVE was unusable and
// PREVIOUS is being served instead; ActiveError says why.
type Active struct {
	Loaded
	FellBack    bool
	ActiveError error
}

var (
	// ErrNoPolicy means no policy has ever been deployed.
	ErrNoPolicy = errors.New("no policy has been deployed")
	// ErrInvalidPolicy means apply refused the policy; see the findings.
	ErrInvalidPolicy = errors.New("policy is invalid; the active policy was not changed")
)

func previousPath(l layout.Layout) string { return filepath.Join(l.PoliciesDir(), previousPointer) }

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// readPointer reads ACTIVE or PREVIOUS.
func readPointer(path string) (int, error) {
	b, err := fsutil.ReadFileNoFollow(path, 64)
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(b))
	if !versionRe.MatchString(s) || s == "000000" {
		return 0, fmt.Errorf("%s: malformed version pointer %q", path, s)
	}
	n, _ := strconv.Atoi(s)
	return n, nil
}

func writePointer(path string, n int, owner *fsutil.Owner) error {
	return fsutil.WriteFileAtomic(path, []byte(layout.VersionName(n)+"\n"), 0o640, owner)
}

// ActiveVersion returns the version ACTIVE points to (0 if none).
func ActiveVersion(l layout.Layout) (int, error) {
	n, err := readPointer(l.ActivePointer())
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	return n, err
}

// PreviousVersion returns the version PREVIOUS points to (0 if none).
func PreviousVersion(l layout.Layout) (int, error) {
	n, err := readPointer(previousPath(l))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	return n, err
}

func decodeStrict(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// LoadVersion reads and verifies one version: the directory must be real (no
// symlink), meta must match, and compiled.json must match its recorded checksum.
func LoadVersion(l layout.Layout, n int) (*Loaded, error) {
	dir := l.VersionDir(n)
	st, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s: not a directory", dir)
	}
	mb, err := fsutil.ReadFileNoFollow(filepath.Join(dir, "meta.json"), maxMetaSize)
	if err != nil {
		return nil, err
	}
	var m Meta
	if err := decodeStrict(mb, &m); err != nil {
		return nil, fmt.Errorf("%s/meta.json: %w", dir, err)
	}
	if m.Schema != MetaSchema || m.Version != n {
		return nil, fmt.Errorf("%s/meta.json: unexpected schema or version", dir)
	}
	cb, err := fsutil.ReadFileNoFollow(filepath.Join(dir, "compiled.json"), maxCompiledSize)
	if err != nil {
		return nil, err
	}
	if sha(cb) != m.CompiledSHA256 {
		return nil, fmt.Errorf("%s/compiled.json: checksum mismatch (file modified or corrupted)", dir)
	}
	var c policy.Compiled
	if err := decodeStrict(cb, &c); err != nil {
		return nil, fmt.Errorf("%s/compiled.json: %w", dir, err)
	}
	if c.Schema != policy.CompiledSchema {
		return nil, fmt.Errorf("%s/compiled.json: unsupported schema %q", dir, c.Schema)
	}
	return &Loaded{Version: n, Meta: m, Policy: &c}, nil
}

// LoadActive returns the policy the push path must enforce: ACTIVE, or
// PREVIOUS when ACTIVE is unusable. ErrNoPolicy when nothing was deployed.
func LoadActive(l layout.Layout) (*Active, error) {
	var activeErr error
	n, err := readPointer(l.ActivePointer())
	switch {
	case err == nil:
		ld, err := LoadVersion(l, n)
		if err == nil {
			return &Active{Loaded: *ld}, nil
		}
		activeErr = fmt.Errorf("active version %s: %w", layout.VersionName(n), err)
	case errors.Is(err, fs.ErrNotExist):
		activeErr = ErrNoPolicy
	default:
		activeErr = err
	}
	if p, err := readPointer(previousPath(l)); err == nil {
		if ld, err := LoadVersion(l, p); err == nil {
			return &Active{Loaded: *ld, FellBack: true, ActiveError: activeErr}, nil
		}
	}
	return nil, activeErr
}

// Versions lists stored version numbers in ascending order.
func Versions(l layout.Layout) ([]int, error) {
	entries, err := os.ReadDir(l.PoliciesDir())
	if err != nil {
		return nil, err
	}
	var out []int
	for _, e := range entries {
		if e.IsDir() && versionRe.MatchString(e.Name()) {
			n, _ := strconv.Atoi(e.Name())
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out, nil
}

// ApplyRequest is one policy deployment.
type ApplyRequest struct {
	Source []byte
	Actor  string
	Reason string
	Now    time.Time
	Owner  *fsutil.Owner // root:git for files and directories; nil in test installs
}

// ApplyResult reports what apply did.
type ApplyResult struct {
	Meta     Meta
	Previous int
	Findings []policy.Finding
}

// Apply validates, compiles, stores and activates a policy. An invalid policy
// (including a revision not newer than the active one, V040) never touches the
// active version. The caller must hold the admin lock.
func Apply(l layout.Layout, req ApplyRequest) (*ApplyResult, error) {
	compiled, findings := policy.Build(req.Source, policy.Options{Now: req.Now})
	res := &ApplyResult{Findings: findings}
	if compiled == nil {
		return res, ErrInvalidPolicy
	}
	// cur is nil when nothing is loadable; a fresh apply is then the
	// documented recovery path, so that is not an error here.
	cur, _ := LoadActive(l)
	if cur != nil && compiled.Revision <= cur.Meta.Revision {
		res.Findings = append(res.Findings, policy.Finding{
			Code: "V040", Severity: policy.SevError, Path: "metadata.revision",
			Message: fmt.Sprintf("revision %d is not newer than the active revision %d (version %s); bump metadata.revision, or use rollback to re-activate an old version",
				compiled.Revision, cur.Meta.Revision, layout.VersionName(cur.Version)),
		})
		return res, ErrInvalidPolicy
	}

	versions, err := Versions(l)
	if err != nil {
		return res, err
	}
	next := 1
	if len(versions) > 0 {
		next = versions[len(versions)-1] + 1
	}
	cb, err := policy.MarshalCompiled(compiled)
	if err != nil {
		return res, err
	}
	_, warns := policy.Count(findings)
	meta := Meta{
		Schema: MetaSchema, Version: next, Name: compiled.Name, Revision: compiled.Revision,
		SourceSHA256: compiled.SourceSHA256, CompiledSHA256: sha(cb),
		DeployedAt: req.Now.UTC(), DeployedBy: req.Actor, Reason: req.Reason,
		EngineVersion: version.Version, Warnings: warns,
	}
	mb, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return res, err
	}

	// Stage the complete version in tmp/ (same filesystem), then move it into
	// place with one rename and only afterwards switch ACTIVE.
	tmp := filepath.Join(l.TmpDir(), "version-"+fsutil.RandomSuffix())
	if err := os.Mkdir(tmp, 0o700); err != nil {
		return res, err
	}
	defer os.RemoveAll(tmp) // no-op after a successful rename
	for name, data := range map[string][]byte{
		"policy.yaml": req.Source, "compiled.json": cb, "meta.json": append(mb, '\n'),
	} {
		if err := writeNew(filepath.Join(tmp, name), data, req.Owner); err != nil {
			return res, err
		}
	}
	final := l.VersionDir(next)
	if err := os.Rename(tmp, final); err != nil {
		return res, err
	}
	if err := os.Chmod(final, 0o550); err != nil {
		return res, err
	}
	if req.Owner != nil {
		if err := os.Lchown(final, req.Owner.UID, req.Owner.GID); err != nil {
			return res, err
		}
	}
	if err := fsutil.SyncDir(l.PoliciesDir()); err != nil {
		return res, err
	}
	if cur != nil {
		if err := writePointer(previousPath(l), cur.Version, req.Owner); err != nil {
			return res, err
		}
		res.Previous = cur.Version
	}
	if err := writePointer(l.ActivePointer(), next, req.Owner); err != nil {
		return res, err
	}
	res.Meta = meta
	return res, nil
}

// writeNew creates a file that must not exist yet, fsyncs it and makes it read-only.
func writeNew(path string, data []byte, owner *fsutil.Owner) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if owner != nil {
		if err := f.Chown(owner.UID, owner.GID); err != nil {
			return err
		}
	}
	if err := f.Chmod(0o440); err != nil {
		return err
	}
	return f.Sync()
}

// Rollback re-activates version `to` (0 = PREVIOUS). The target is verified
// before the switch. Returns the versions switched from and to. The caller
// must hold the admin lock.
func Rollback(l layout.Layout, to int, owner *fsutil.Owner) (from, target int, err error) {
	from, _ = ActiveVersion(l) // may be 0 or unreadable: rollback is also a recovery path
	if to == 0 {
		if to, err = PreviousVersion(l); err != nil {
			return from, 0, err
		}
		if to == 0 {
			return from, 0, errors.New("no previous version recorded; pass --to <version>")
		}
	}
	if to == from {
		return from, to, fmt.Errorf("version %s is already active", layout.VersionName(to))
	}
	if _, err := LoadVersion(l, to); err != nil {
		return from, to, fmt.Errorf("cannot roll back to %s: %w", layout.VersionName(to), err)
	}
	if from != 0 {
		if err := writePointer(previousPath(l), from, owner); err != nil {
			return from, to, err
		}
	}
	return from, to, writePointer(l.ActivePointer(), to, owner)
}
