package admin

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/soroush67/git-policy/internal/audit"
	"github.com/soroush67/git-policy/internal/fsutil"
	"github.com/soroush67/git-policy/internal/layout"
	"github.com/soroush67/git-policy/internal/state"
)

//go:embed pre-receive.sh
var hookTemplate string

const maxBinarySize = 256 << 20

var safeRootRe = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)

// HookScript renders the wrapper for an installation root.
func HookScript(root string) ([]byte, error) {
	if !safeRootRe.MatchString(root) {
		return nil, fmt.Errorf("unsafe installation root %q", root)
	}
	out := strings.Replace(hookTemplate, "GP_ROOT="+layout.DefaultRoot, "GP_ROOT="+root, 1)
	return []byte(out), nil
}

// InstallReport says what install changed.
type InstallReport struct {
	BinaryUpdated bool
	StateCreated  bool
	HookAction    string // "installed", "unchanged", "replaced"
	OtherHooks    []string
}

// Install creates the layout, installs the running binary and the hook
// wrapper, and creates the initial state. It is idempotent and never deletes:
//   - an existing, different hook at HookPath is only replaced with
//     replaceHook, after a backup;
//   - other hooks in the directory (e.g. the 01-block-dll PoC) are untouched;
//   - on first install the engine starts DISABLED (explicit, no expiry) so
//     installing changes no push behaviour until an operator enables it.
func (c *Context) Install(selfPath string, replaceHook bool) (*InstallReport, error) {
	if c.HookPath == "" {
		return nil, errors.New("--hook-path is required with a custom --root")
	}
	hookDir := filepath.Dir(c.HookPath)
	if st, err := os.Lstat(hookDir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("hook directory %s does not exist; check gitaly [hooks] custom_hooks_dir", hookDir)
	}
	script, err := HookScript(c.L.Root)
	if err != nil {
		return nil, err
	}

	owners := map[layout.Owner]*fsutil.Owner{
		layout.OwnerRoot: c.RootOwn, layout.OwnerRootGit: c.RootGit, layout.OwnerGit: c.GitOwn,
	}
	for _, d := range c.L.Dirs() {
		if err := fsutil.EnsureDir(d.Path, fs.FileMode(d.Mode), owners[d.Owner]); err != nil {
			return nil, err
		}
	}
	unlock, err := c.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()

	rep := &InstallReport{}
	if rep.BinaryUpdated, err = c.installBinary(selfPath); err != nil {
		return nil, err
	}

	if _, err := os.Lstat(c.L.StateFile()); errors.Is(err, fs.ErrNotExist) {
		st := state.State{Enabled: false, ChangedAt: c.Now().UTC(), ChangedBy: c.Actor,
			Reason: "initial install: engine is disabled until an operator deploys a policy and enables it"}
		if err := state.Save(c.L.StateFile(), st, c.RootGit); err != nil {
			return nil, err
		}
		rep.StateCreated = true
	}

	switch cur, err := fsutil.ReadFileNoFollow(c.HookPath, 1<<20); {
	case errors.Is(err, fs.ErrNotExist):
		rep.HookAction = "installed"
	case err != nil:
		return nil, fmt.Errorf("existing hook %s: %w", c.HookPath, err)
	case bytes.Equal(cur, script):
		rep.HookAction = "unchanged"
	case !replaceHook:
		return nil, fmt.Errorf("%s exists with different content; not touching it (use --replace-hook to back it up and replace it)", c.HookPath)
	default:
		backup := filepath.Join(c.L.BackupDir(), filepath.Base(c.HookPath)+"."+c.Now().UTC().Format("20060102T150405Z"))
		if err := fsutil.WriteFileAtomic(backup, cur, 0o600, c.RootOwn); err != nil {
			return nil, err
		}
		rep.HookAction = "replaced"
	}
	if rep.HookAction != "unchanged" {
		if err := fsutil.WriteFileAtomic(c.HookPath, script, 0o755, c.RootOwn); err != nil {
			return nil, err
		}
	}
	rep.OtherHooks = otherHooks(hookDir, filepath.Base(c.HookPath))

	c.audit(audit.EngineInstalled, map[string]any{
		"binary_updated": rep.BinaryUpdated, "state_created": rep.StateCreated,
		"hook": rep.HookAction, "hook_path": c.HookPath, "other_hooks": rep.OtherHooks,
	})
	return rep, nil
}

// installBinary copies the running executable into bin/, keeping the old one
// as git-policy.previous. The live path is replaced by rename, so a
// concurrent push always finds a complete binary.
func (c *Context) installBinary(selfPath string) (bool, error) {
	self, err := os.ReadFile(selfPath)
	if err != nil {
		return false, err
	}
	if len(self) > maxBinarySize {
		return false, errors.New("binary too large")
	}
	dst := c.L.Binary()
	cur, err := fsutil.ReadFileNoFollow(dst, maxBinarySize)
	switch {
	case err == nil && bytes.Equal(cur, self):
		return false, nil
	case err == nil:
		if err := fsutil.WriteFileAtomic(dst+".previous", cur, 0o755, c.RootOwn); err != nil {
			return false, err
		}
	case !errors.Is(err, fs.ErrNotExist):
		return false, err
	}
	return true, fsutil.WriteFileAtomic(dst, self, 0o755, c.RootOwn)
}

// Uninstall removes only the git-policy hook (after a backup). Policy, state
// and logs stay in place. A modified hook is only removed with force.
func (c *Context) Uninstall(force bool) (string, error) {
	if c.HookPath == "" {
		return "", errors.New("--hook-path is required with a custom --root")
	}
	script, err := HookScript(c.L.Root)
	if err != nil {
		return "", err
	}
	cur, err := fsutil.ReadFileNoFollow(c.HookPath, 1<<20)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !bytes.Equal(cur, script) && !force {
		return "", fmt.Errorf("%s is not the git-policy wrapper (modified?); use --force to remove it anyway", c.HookPath)
	}
	unlock, err := c.lock()
	if err != nil {
		return "", err
	}
	defer unlock()
	backup := filepath.Join(c.L.BackupDir(), filepath.Base(c.HookPath)+".uninstalled."+c.Now().UTC().Format("20060102T150405Z"))
	if err := fsutil.WriteFileAtomic(backup, cur, 0o600, c.RootOwn); err != nil {
		return "", err
	}
	if err := os.Remove(c.HookPath); err != nil {
		return "", err
	}
	if err := fsutil.SyncDir(filepath.Dir(c.HookPath)); err != nil {
		return "", err
	}
	c.audit(audit.HookUninstalled, map[string]any{"hook_path": c.HookPath, "backup": backup, "forced": force})
	return backup, nil
}

func otherHooks(dir, self string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.Name() != self && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}
