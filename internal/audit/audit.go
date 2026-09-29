// Package audit appends structured events (JSON Lines) to the daily audit log.
//
// One file per UTC day (audit-YYYY-MM-DD.jsonl) means nothing ever renames a
// file a writer holds, so there is no rotation race. Each event is a single
// write(2) under a short exclusive flock on that file; concurrent pushes and
// admin commands interleave whole lines only.
package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/soroush67/git-policy/internal/fsutil"
	"github.com/soroush67/git-policy/internal/version"
)

// Schema identifies the event format for downstream consumers (SIEM).
const Schema = "git-policy/audit/v1"

// Action names.
const (
	PolicyEnabled       = "POLICY_ENABLED"
	PolicyDisabled      = "POLICY_DISABLED"
	PolicyUpdated       = "POLICY_UPDATED"
	PolicyUpdateRefused = "POLICY_UPDATE_REFUSED"
	PolicyRollback      = "POLICY_ROLLBACK"
	PolicyFallback      = "POLICY_FALLBACK"
	EngineInstalled     = "ENGINE_INSTALLED"
	HookUninstalled     = "HOOK_UNINSTALLED"
	PushAllowedDisabled = "PUSH_ALLOWED_ENGINE_DISABLED"
	PushRejected        = "REJECT"
	PushAccepted        = "ACCEPT"
	LogsPruned          = "LOGS_PRUNED"
	ExceptionApplied    = "EXCEPTION_APPLIED"
	WouldReject         = "WOULD_REJECT"
	FindingsTruncated   = "FINDINGS_TRUNCATED"
	HookRetired         = "HOOK_RETIRED"
)

// FileName returns the audit file for the UTC day of t.
func FileName(dir string, t time.Time) string {
	return filepath.Join(dir, "audit-"+t.UTC().Format("2006-01-02")+".jsonl")
}

// Append writes one event. owner is applied when this call creates the day's
// file (root-run admin commands must leave it appendable by the git user).
func Append(dir string, owner *fsutil.Owner, action string, fields map[string]any) error {
	now := time.Now()
	ev := make(map[string]any, len(fields)+3)
	for k, v := range fields {
		ev[k] = v
	}
	ev["timestamp"] = now.Format(time.RFC3339)
	ev["action"] = action
	ev["engine_version"] = version.Version
	ev["schema"] = Schema
	ev["event_id"] = fsutil.RandomSuffix() + fsutil.RandomSuffix() // de-duplication key for forwarders
	if h, err := os.Hostname(); err == nil {
		ev["host"] = h
	}
	line, err := json.Marshal(ev) // escapes control characters in all strings
	if err != nil {
		return err
	}
	line = append(line, '\n')

	f, err := openLog(FileName(dir, now), owner)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_, err = f.Write(line)
	return err
}

func openLog(path string, owner *fsutil.Owner) (*os.File, error) {
	const flags = os.O_WRONLY | os.O_APPEND | syscall.O_NOFOLLOW | syscall.O_CLOEXEC
	f, err := os.OpenFile(path, flags, 0)
	if !errors.Is(err, os.ErrNotExist) {
		return f, err
	}
	f, err = os.OpenFile(path, flags|os.O_CREATE|os.O_EXCL, 0o640)
	if errors.Is(err, os.ErrExist) { // another writer created it first
		return os.OpenFile(path, flags, 0)
	}
	if err != nil {
		return nil, err
	}
	if owner != nil {
		if err := f.Chown(owner.UID, owner.GID); err != nil {
			f.Close()
			return nil, err
		}
	}
	return f, nil
}

var fileRe = regexp.MustCompile(`^audit-([0-9]{4}-[0-9]{2}-[0-9]{2})\.jsonl$`)

// Prune deletes daily audit files older than retentionDays (by the date in
// the file name). Today's and younger files are never touched; other files in
// the directory (break-glass.log, …) are ignored. Returns the removed names.
func Prune(dir string, retentionDays int, now time.Time) ([]string, error) {
	if retentionDays < 1 {
		return nil, fmt.Errorf("retention must be at least 1 day")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	cutoff := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -retentionDays)
	var removed []string
	for _, e := range entries {
		m := fileRe.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		day, err := time.Parse("2006-01-02", m[1])
		if err != nil || !day.Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			return removed, err
		}
		removed = append(removed, e.Name())
	}
	return removed, nil
}
