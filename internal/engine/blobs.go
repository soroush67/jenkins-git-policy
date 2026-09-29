package engine

import (
	"encoding/binary"
	"fmt"

	"github.com/soroush67/git-policy/internal/policy"
)

// PEHeadSize is how many leading bytes signature detection needs.
const PEHeadSize = 64 << 10

// BlobEntry is an introduced path with object metadata.
type BlobEntry struct {
	PathEntry
	Blob string
	Size int64
	Head []byte // first bytes of content; nil when not inspected
}

// IsPE reports whether head starts a Windows PE image (exe, dll, sys, ...):
// the DOS "MZ" stub whose e_lfanew (offset 0x3C) points at "PE\0\0".
// A bare "MZ" prefix alone is not enough, which keeps false positives
// (text files starting with "MZ") out.
func IsPE(head []byte) bool {
	if len(head) < 0x40 || head[0] != 'M' || head[1] != 'Z' {
		return false
	}
	off := binary.LittleEndian.Uint32(head[0x3C:0x40])
	if off < 0x40 || uint64(off)+4 > uint64(len(head)) {
		return false
	}
	return string(head[off:off+4]) == "PE\x00\x00"
}

// NeedsSizes / NeedsHeads tell the caller which blobs to inspect.
func (c Content) NeedsSizes() bool { return c.MaxFileSize > 0 }
func (c Content) NeedsHeads() bool { _, ok := c.Signatures["pe"]; return ok }

// CheckBlobs applies max_file_size and blocked_signatures (PHASE-2 §3.3).
// A blob above the mandatory cap is a mandatory violation (only an
// exception with mandatory: true and a large enough max_file_size waives
// it); above a scoped limit only, a scoped one.
func (e *Engine) CheckBlobs(d *Decision, r Request, cls *Classifier, entries []BlobEntry) {
	mandCap := e.P.Mandatory.MaxFileSize
	for _, b := range entries {
		if len(d.Violations)+len(d.WouldReject) >= maxRecorded {
			d.Truncated = true
			return
		}
		c := cls.Content(cls.ClassOf(b.Ref))
		if c.MaxFileSize > 0 && b.Size > c.MaxFileSize {
			src := Entry{Source: c.SizeSource}
			if mandCap > 0 && b.Size > mandCap {
				src = Entry{Mandatory: true, Source: "mandatory.max_file_size"}
			}
			e.contentFinding(d, r, c, src, Violation{Code: policy.FileTooLarge, Ref: b.Ref, New: b.Commit,
				Path: b.Path, Size: b.Size,
				Detail: fmt.Sprintf("Size: %s (limit %s)", HumanSize(b.Size), HumanSize(c.MaxFileSize))})
		}
		if src, ok := c.Signatures["pe"]; ok && b.Head != nil && IsPE(b.Head) {
			e.contentFinding(d, r, c, src, Violation{Code: policy.BlockedSignature, Ref: b.Ref, New: b.Commit,
				Path: b.Path, Size: b.Size, Detail: "Content is a Windows PE executable/library (detected from the file header)"})
		}
	}
}

// HumanSize renders a byte count for messages.
func HumanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
