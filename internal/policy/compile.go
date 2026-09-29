package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// CompiledSchema identifies the compiled.json format read at push time.
const CompiledSchema = "git-policy/compiled/v1"

// Compiled is the normalised policy the hook loads: defaults applied, keys
// case-folded, sizes in bytes, lists sorted and de-duplicated. It is derived
// deterministically from the source, so equal sources give identical bytes.
type Compiled struct {
	Schema       string                  `json:"schema"`
	APIVersion   string                  `json:"api_version"`
	Name         string                  `json:"name"`
	Revision     int                     `json:"revision"`
	SourceSHA256 string                  `json:"source_sha256"`
	Settings     EffectiveSettings       `json:"settings"`
	Mandatory    CompiledMandatory       `json:"mandatory"`
	Defaults     Layer                   `json:"defaults"`
	Namespaces   map[string]Layer        `json:"namespaces,omitempty"`
	Projects     map[string]ProjectLayer `json:"projects,omitempty"`
	Users        map[string]Identity     `json:"users,omitempty"`
	Groups       map[string]Identity     `json:"groups,omitempty"`
	Exceptions   []CompiledException     `json:"exceptions,omitempty"`
}

// ContentSet is a normalised block or unblock list.
type ContentSet struct {
	Extensions []string `json:"extensions,omitempty"`
	Paths      []string `json:"paths,omitempty"` // lower-cased: path globs match case-insensitively
	Signatures []string `json:"signatures,omitempty"`
}

// Layer is one content layer; Refs is only set on scope layers.
type Layer struct {
	Mode        string           `json:"mode,omitempty"`
	Block       ContentSet       `json:"block"`
	Unblock     ContentSet       `json:"unblock"`
	MaxFileSize int64            `json:"max_file_size,omitempty"`
	Refs        map[string]Layer `json:"refs,omitempty"`
}

type ProjectLayer struct {
	Layer
	Enabled bool `json:"enabled"`
}

type CompiledMandatory struct {
	Mode        string     `json:"mode"`
	Block       ContentSet `json:"block"`
	MaxFileSize int64      `json:"max_file_size,omitempty"`
	DenyUsers   []string   `json:"deny_users,omitempty"`
	DenyGroups  []string   `json:"deny_groups,omitempty"`
}

type Identity struct {
	Push       string                     `json:"push,omitempty"`
	Refs       map[string]string          `json:"refs,omitempty"`
	Namespaces map[string]ScopedIdentityC `json:"namespaces,omitempty"`
	Projects   map[string]ScopedIdentityC `json:"projects,omitempty"`
}

type ScopedIdentityC struct {
	Push string            `json:"push,omitempty"`
	Refs map[string]string `json:"refs,omitempty"`
}

type CompiledException struct {
	ID          string   `json:"id"`
	Rules       []string `json:"rules"`
	Mandatory   bool     `json:"mandatory,omitempty"`
	Users       []string `json:"users,omitempty"`
	Groups      []string `json:"groups,omitempty"`
	Namespaces  []string `json:"namespaces,omitempty"`
	Projects    []string `json:"projects,omitempty"`
	Refs        []string `json:"refs,omitempty"`
	Paths       []string `json:"paths,omitempty"`
	MaxFileSize int64    `json:"max_file_size,omitempty"`
	ExpiresAt   string   `json:"expires_at"`
	Reason      string   `json:"reason"`
	Ticket      string   `json:"ticket,omitempty"`
	ApprovedBy  string   `json:"approved_by,omitempty"`
}

// Build parses, validates and compiles source in one step. It returns the
// findings (errors and warnings) and, only when there are no errors, the
// compiled policy.
func Build(source []byte, opts Options) (*Compiled, []Finding) {
	doc, fs := Parse(source)
	if doc == nil {
		return nil, fs
	}
	fs = Validate(doc, opts)
	if errs, _ := Count(fs); errs > 0 {
		return nil, fs
	}
	return compile(doc, source), fs
}

// SourceSHA256 is the checksum reported by validate/status for a source file.
func SourceSHA256(source []byte) string {
	sum := sha256.Sum256(source)
	return hex.EncodeToString(sum[:])
}

// MarshalCompiled renders compiled.json deterministically.
func MarshalCompiled(c *Compiled) ([]byte, error) {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func compile(d *Document, source []byte) *Compiled {
	c := &Compiled{
		Schema:       CompiledSchema,
		APIVersion:   d.APIVersion,
		Name:         d.Metadata.Name,
		Revision:     d.Metadata.Revision,
		SourceSHA256: SourceSHA256(source),
		Settings:     d.Effective(),
		Mandatory:    CompiledMandatory{Mode: ModeEnforce},
	}
	if m := d.Mandatory; m != nil {
		if m.Mode != "" {
			c.Mandatory.Mode = m.Mode
		}
		c.Mandatory.Block = contentSet(m.BlockedExtensions, m.BlockedPaths, m.BlockedSignatures)
		c.Mandatory.MaxFileSize = mustSize(m.MaxFileSize)
		c.Mandatory.DenyUsers = lowerSorted(m.DenyUsers)
		c.Mandatory.DenyGroups = lowerSorted(m.DenyGroups)
	}
	if df := d.Defaults; df != nil {
		c.Defaults = Layer{
			Block:       contentSet(df.BlockedExtensions, df.BlockedPaths, df.BlockedSignatures),
			MaxFileSize: mustSize(df.MaxFileSize),
			Refs:        refLayers(df.Refs),
		}
	}
	if len(d.Namespaces) > 0 {
		c.Namespaces = map[string]Layer{}
		for k, s := range d.Namespaces {
			c.Namespaces[strings.ToLower(k)] = scopeLayer(s)
		}
	}
	if len(d.Projects) > 0 {
		c.Projects = map[string]ProjectLayer{}
		for k, p := range d.Projects {
			pl := ProjectLayer{Enabled: true}
			if p != nil {
				pl.Layer = scopeLayer(&p.Scope)
				if p.Enabled != nil {
					pl.Enabled = *p.Enabled
				}
			}
			c.Projects[strings.ToLower(k)] = pl
		}
	}
	c.Users = identities(d.Users)
	c.Groups = identities(d.Groups)
	for _, x := range d.Exceptions {
		exp, _ := ParseDate(x.Expires)
		cx := CompiledException{
			ID:          x.ID,
			Rules:       sortedUnique(x.Rules),
			Mandatory:   x.Mandatory,
			MaxFileSize: mustSize(x.MaxFileSize),
			ExpiresAt:   EndOfDay(exp).Format(time.RFC3339),
			Reason:      x.Reason,
			Ticket:      x.Ticket,
			ApprovedBy:  x.ApprovedBy,
		}
		if s := x.Subjects; s != nil {
			cx.Users, cx.Groups = lowerSorted(s.Users), lowerSorted(s.Groups)
		}
		if s := x.Scope; s != nil {
			cx.Namespaces, cx.Projects = lowerSorted(s.Namespaces), lowerSorted(s.Projects)
			cx.Refs, cx.Paths = sortedUnique(s.Refs), lowerSorted(s.Paths)
		}
		c.Exceptions = append(c.Exceptions, cx) // document order is significant (first match wins)
	}
	return c
}

func scopeLayer(s *Scope) Layer {
	if s == nil {
		return Layer{}
	}
	l := refLayer(&s.RefScope)
	l.Refs = refLayers(s.Refs)
	return l
}

func refLayers(m map[string]*RefScope) map[string]Layer {
	if len(m) == 0 {
		return nil
	}
	out := map[string]Layer{}
	for g, r := range m {
		out[g] = refLayer(r) // ref globs are case-sensitive: keys kept verbatim
	}
	return out
}

func refLayer(r *RefScope) Layer {
	if r == nil {
		return Layer{}
	}
	return Layer{
		Mode:        r.Mode,
		Block:       contentSet(r.BlockedExtensions, r.BlockedPaths, r.BlockedSignatures),
		Unblock:     contentSet(r.UnblockExtensions, r.UnblockPaths, r.UnblockSignatures),
		MaxFileSize: mustSize(r.MaxFileSize),
	}
}

func identities(m map[string]*IdentityRule) map[string]Identity {
	if len(m) == 0 {
		return nil
	}
	out := map[string]Identity{}
	for k, r := range m {
		id := Identity{Push: r.Push, Refs: copyRefs(r.Refs)}
		if len(r.Namespaces) > 0 {
			id.Namespaces = map[string]ScopedIdentityC{}
			for n, s := range r.Namespaces {
				id.Namespaces[strings.ToLower(n)] = ScopedIdentityC{Push: s.Push, Refs: copyRefs(s.Refs)}
			}
		}
		if len(r.Projects) > 0 {
			id.Projects = map[string]ScopedIdentityC{}
			for p, s := range r.Projects {
				id.Projects[strings.ToLower(p)] = ScopedIdentityC{Push: s.Push, Refs: copyRefs(s.Refs)}
			}
		}
		out[strings.ToLower(k)] = id
	}
	return out
}

func copyRefs(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func contentSet(exts, paths, sigs []string) ContentSet {
	norm := make([]string, 0, len(exts))
	for _, e := range exts {
		norm = append(norm, NormalizeExtension(e))
	}
	return ContentSet{Extensions: sortedUnique(norm), Paths: lowerSorted(paths), Signatures: sortedUnique(sigs)}
}

func mustSize(s string) int64 {
	if s == "" {
		return 0
	}
	n, _ := ParseSize(s)
	return n
}

func lowerSorted(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, strings.ToLower(s))
	}
	return sortedUnique(out)
}

func sortedUnique(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
