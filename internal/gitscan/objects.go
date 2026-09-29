package gitscan

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Sizes returns the size in bytes of each blob, from object headers only
// (`git cat-file --batch-check`): no content is read or decompressed beyond
// the header, and nothing is checked out.
func Sizes(ctx context.Context, git, dir string, oids []string) (map[string]int64, error) {
	s := &scanner{ctx: ctx, o: Options{Git: git, Dir: dir}}
	var in bytes.Buffer
	for _, o := range oids {
		if !hexRe.MatchString(o) {
			return nil, fmt.Errorf("invalid object id %q", o)
		}
		in.WriteString(o + "\n")
	}
	var out, errb bytes.Buffer
	cmd := s.command("cat-file", "--batch-check=%(objectname) %(objecttype) %(objectsize)")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = &in, &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, s.wrap("cat-file --batch-check", err, &errb)
	}
	sizes := make(map[string]int64, len(oids))
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		f := strings.Fields(l)
		if len(f) != 3 || !hexRe.MatchString(f[0]) {
			return nil, fmt.Errorf("object missing or unreadable: %q", l)
		}
		if f[1] != "blob" {
			return nil, fmt.Errorf("object %s is a %s, not a blob", f[0], f[1])
		}
		n, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil {
			return nil, err
		}
		sizes[f[0]] = n
	}
	if len(sizes) != len(uniq(oids)) {
		return nil, fmt.Errorf("cat-file returned %d of %d objects", len(sizes), len(uniq(oids)))
	}
	return sizes, nil
}

// Heads returns the first n bytes of each blob through one
// `git cat-file --batch` stream. Git still inflates each object fully, so
// the cost is proportional to the total size of the requested blobs; the
// caller only asks for blobs whose class has signature rules.
func Heads(ctx context.Context, git, dir string, oids []string, n int) (map[string][]byte, error) {
	s := &scanner{ctx: ctx, o: Options{Git: git, Dir: dir}}
	oids = uniq(oids)
	var in bytes.Buffer
	for _, o := range oids {
		if !hexRe.MatchString(o) {
			return nil, fmt.Errorf("invalid object id %q", o)
		}
		in.WriteString(o + "\n")
	}
	var errb bytes.Buffer
	cmd := s.command("cat-file", "--batch")
	cmd.Stdin, cmd.Stderr = &in, &errb
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	heads := make(map[string][]byte, len(oids))
	br := bufio.NewReaderSize(stdout, 64<<10)
	perr := func() error {
		for range oids {
			hdr, err := br.ReadString('\n')
			if err != nil {
				return fmt.Errorf("cat-file --batch: %w", err)
			}
			f := strings.Fields(hdr)
			if len(f) != 3 || !hexRe.MatchString(f[0]) {
				return fmt.Errorf("cat-file --batch: object missing or unreadable: %q", strings.TrimSpace(hdr))
			}
			size, err := strconv.ParseInt(f[2], 10, 64)
			if err != nil {
				return err
			}
			take := int64(n)
			if size < take {
				take = size
			}
			head := make([]byte, take)
			if _, err := io.ReadFull(br, head); err != nil {
				return err
			}
			if _, err := io.CopyN(io.Discard, br, size-take+1); err != nil { // rest + trailing LF
				return err
			}
			heads[f[0]] = head
		}
		return nil
	}()
	if perr != nil {
		cmd.Process.Kill()
		cmd.Wait()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, perr
	}
	if err := cmd.Wait(); err != nil {
		return nil, s.wrap("cat-file --batch", err, &errb)
	}
	return heads, nil
}

func uniq(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
