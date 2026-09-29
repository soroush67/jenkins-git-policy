// Package fsutil provides the few filesystem primitives git-policy relies on:
// atomic replacement, symlink-refusing reads, and advisory locks.
package fsutil

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Owner is a uid/gid pair applied to files git-policy creates. A nil *Owner
// means "leave ownership as created" (non-root test installs).
type Owner struct{ UID, GID int }

func (o *Owner) apply(f *os.File) error {
	if o == nil {
		return nil
	}
	return f.Chown(o.UID, o.GID)
}

// ReadFileNoFollow reads a regular file without following a symlink at the
// final path component, refusing files larger than max bytes.
func ReadFileNoFollow(path string, max int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", path)
	}
	if st.Size() > max {
		return nil, fmt.Errorf("%s: larger than %d bytes", path, max)
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s: larger than %d bytes", path, max)
	}
	return b, nil
}

// RandomSuffix returns a short random hex string for temporary names.
func RandomSuffix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// WriteFileAtomic replaces path with data: write a temp file in the same
// directory (O_EXCL, never following symlinks), fsync, set mode and owner,
// rename over the target, fsync the directory. Readers see the old or the new
// content, never a partial file.
func WriteFileAtomic(path string, data []byte, mode os.FileMode, owner *Owner) error {
	dir, base := filepath.Split(path)
	tmp := filepath.Join(dir, "."+base+".tmp-"+RandomSuffix())
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if err := owner.apply(f); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return SyncDir(dir)
}

// SyncDir fsyncs a directory so a rename inside it is durable.
func SyncDir(dir string) error {
	if dir == "" {
		dir = "."
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// EnsureDir creates dir if missing and enforces mode and owner. It refuses a
// symlink in place of the directory.
func EnsureDir(dir string, mode os.FileMode, owner *Owner) error {
	st, err := os.Lstat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.Mkdir(dir, mode); err != nil {
			return err
		}
	case err != nil:
		return err
	case st.Mode()&os.ModeSymlink != 0:
		return fmt.Errorf("%s: is a symlink; refusing to use it", dir)
	case !st.IsDir():
		return fmt.Errorf("%s: exists and is not a directory", dir)
	}
	if err := os.Chmod(dir, mode); err != nil {
		return err
	}
	if owner != nil {
		return os.Lchown(dir, owner.UID, owner.GID)
	}
	return nil
}

// ErrLocked is returned when another admin operation holds the lock.
var ErrLocked = errors.New("another git-policy admin operation is in progress")

// Lock takes an exclusive advisory lock on path, waiting up to timeout. Only
// admin operations lock; the push path never does.
func Lock(path string, timeout time.Duration) (release func(), err error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			f.Close()
			if errors.Is(err, syscall.EWOULDBLOCK) {
				return nil, ErrLocked
			}
			return nil, err
		}
		time.Sleep(100 * time.Millisecond)
	}
}
