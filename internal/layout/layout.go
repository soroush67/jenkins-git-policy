// Package layout defines the on-disk layout of an installed git-policy.
//
// Everything lives under one root on the persistent GitLab data volume
// (/var/opt/gitlab), so it survives container re-creation and image upgrades.
// See docs/design/PHASE-1-ARCHITECTURE.md §8.
package layout

import (
	"fmt"
	"path/filepath"
)

const (
	// DefaultRoot is the installation root inside the GitLab container.
	DefaultRoot = "/var/opt/gitlab/git-policy"
	// HookPath is the only file git-policy places in Gitaly's hook directory.
	HookPath = "/var/opt/gitlab/gitaly/custom_hooks/pre-receive.d/50-git-policy"
)

// Owner of a path: the hook runs as the git user and may only read policy and
// state, and append to logs. Everything else is root-owned.
type Owner string

const (
	OwnerRoot    Owner = "root:root"
	OwnerRootGit Owner = "root:git" // root-owned, readable by the hook
	OwnerGit     Owner = "git:git"  // hook-writable (audit logs only)
)

// DirSpec is the required ownership and mode of one directory.
type DirSpec struct {
	Path  string
	Owner Owner
	Mode  uint32
}

// Layout resolves paths relative to an installation root.
type Layout struct{ Root string }

// New returns a Layout for root, or DefaultRoot when root is empty.
func New(root string) Layout {
	if root == "" {
		root = DefaultRoot
	}
	return Layout{Root: filepath.Clean(root)}
}

func (l Layout) BinDir() string      { return filepath.Join(l.Root, "bin") }
func (l Layout) Binary() string      { return filepath.Join(l.BinDir(), "git-policy") }
func (l Layout) PoliciesDir() string { return filepath.Join(l.Root, "policies") }
func (l Layout) ActivePointer() string {
	return filepath.Join(l.PoliciesDir(), "ACTIVE")
}
func (l Layout) MembershipDir() string { return filepath.Join(l.Root, "membership") }
func (l Layout) StateDir() string      { return filepath.Join(l.Root, "state") }
func (l Layout) StateFile() string     { return filepath.Join(l.StateDir(), "engine.json") }
func (l Layout) BreakGlass() string    { return filepath.Join(l.StateDir(), "break-glass") }
func (l Layout) AdminLock() string     { return filepath.Join(l.StateDir(), "admin.lock") }
func (l Layout) LogsDir() string       { return filepath.Join(l.Root, "logs") }
func (l Layout) BackupDir() string     { return filepath.Join(l.Root, "backup") }
func (l Layout) TmpDir() string        { return filepath.Join(l.Root, "tmp") }

// VersionName formats a policy version directory name (000012).
func VersionName(n int) string { return fmt.Sprintf("%06d", n) }

// VersionDir is the immutable directory holding one deployed policy version.
func (l Layout) VersionDir(n int) string {
	return filepath.Join(l.PoliciesDir(), VersionName(n))
}

// Dirs lists every directory the installer creates, with ownership and mode.
func (l Layout) Dirs() []DirSpec {
	return []DirSpec{
		{l.Root, OwnerRootGit, 0o750},
		{l.BinDir(), OwnerRoot, 0o755},
		{l.PoliciesDir(), OwnerRootGit, 0o750},
		{l.MembershipDir(), OwnerRootGit, 0o750},
		{l.StateDir(), OwnerRootGit, 0o750},
		{l.LogsDir(), OwnerGit, 0o750},
		{l.BackupDir(), OwnerRoot, 0o700},
		{l.TmpDir(), OwnerRoot, 0o700},
	}
}
