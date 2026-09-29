package engine

import (
	"sort"
	"strconv"
	"strings"

	"github.com/soroush67/git-policy/internal/policy"
)

// Entry records where a blocked item comes from.
type Entry struct {
	Mandatory bool   `json:"mandatory"`
	Source    string `json:"source"`
}

// Content is the effective content rule set for one (project, ref)
// (PHASE-2 §3.3). Phases 7-8 enforce it; Phase 5 resolves and explains it.
type Content struct {
	Enabled       bool             `json:"enabled"` // false: projects[P].enabled=false or repo mandatory_only
	Extensions    map[string]Entry `json:"extensions"`
	Paths         map[string]Entry `json:"paths"`
	Signatures    map[string]Entry `json:"signatures"`
	MaxFileSize   int64            `json:"max_file_size"`
	SizeSource    string           `json:"size_source"`
	Mode          string           `json:"mode"` // for non-mandatory rules
	ModeSource    string           `json:"mode_source"`
	MandatoryMode string           `json:"mandatory_mode"`
}

type namedLayer struct {
	name  string
	layer policy.Layer
}

// levelLayers is one scope layer followed by the ref layers of that scope
// that match the ref (merged: block wins over unblock, smallest size wins).
type level struct {
	scope namedLayer
	refs  []namedLayer
}

// ResolveContent computes the effective content rules for project/ref.
func (e *Engine) ResolveContent(project, ref, repoMode string) Content {
	c := Content{
		Enabled:    true,
		Extensions: map[string]Entry{}, Paths: map[string]Entry{}, Signatures: map[string]Entry{},
		Mode: e.P.Settings.Mode, ModeSource: "settings.mode", MandatoryMode: e.P.Mandatory.Mode,
	}
	var chain []level
	if repoMode == policy.RepoMandatoryOnly {
		c.Enabled = false
	} else if pl, ok := e.P.Projects[project]; ok && !pl.Enabled {
		c.Enabled = false
	} else {
		chain = append(chain, e.level("defaults", e.P.Defaults, ref))
		for _, ns := range namespaces(project) {
			if l, ok := e.P.Namespaces[ns]; ok {
				chain = append(chain, e.level("namespaces["+quote(ns)+"]", l, ref))
			}
		}
		if pl, ok := e.P.Projects[project]; ok {
			chain = append(chain, e.level("projects["+quote(project)+"]", pl.Layer, ref))
		}
	}

	apply := func(dst map[string]Entry, block, unblock []string, src string) {
		for _, x := range block {
			dst[x] = Entry{Source: src}
		}
		for _, x := range unblock {
			if !contains(block, x) { // same layer: block wins
				delete(dst, x)
			}
		}
	}
	applyLayer := func(block, unblock policy.ContentSet, src string) {
		apply(c.Extensions, block.Extensions, unblock.Extensions, src)
		apply(c.Paths, block.Paths, unblock.Paths, src)
		apply(c.Signatures, block.Signatures, unblock.Signatures, src)
	}
	for _, lv := range chain {
		s := lv.scope
		applyLayer(s.layer.Block, s.layer.Unblock, s.name)
		if s.layer.MaxFileSize > 0 {
			c.MaxFileSize, c.SizeSource = s.layer.MaxFileSize, s.name+".max_file_size"
		}
		if s.layer.Mode != "" {
			c.Mode, c.ModeSource = s.layer.Mode, s.name+".mode"
		}
		if len(lv.refs) == 0 {
			continue
		}
		// Several matching ref patterns of one scope act as one merged layer.
		var block, unblock policy.ContentSet
		src := lv.refs[0].name
		if len(lv.refs) > 1 {
			src = s.name + ".refs[*]"
		}
		var size int64
		sizeSrc, mode, modeSrc := "", "", ""
		for _, r := range lv.refs {
			block = merge(block, r.layer.Block)
			unblock = merge(unblock, r.layer.Unblock)
			if sz := r.layer.MaxFileSize; sz > 0 && (size == 0 || sz < size) {
				size, sizeSrc = sz, r.name+".max_file_size"
			}
			if r.layer.Mode == policy.ModeEnforce || (r.layer.Mode == policy.ModeAudit && mode == "") {
				mode, modeSrc = r.layer.Mode, r.name+".mode"
			}
		}
		applyLayer(block, unblock, src)
		if size > 0 {
			c.MaxFileSize, c.SizeSource = size, sizeSrc
		}
		if mode != "" {
			c.Mode, c.ModeSource = mode, modeSrc
		}
	}

	m := e.P.Mandatory
	for _, x := range m.Block.Extensions {
		c.Extensions[x] = Entry{Mandatory: true, Source: "mandatory"}
	}
	for _, x := range m.Block.Paths {
		c.Paths[x] = Entry{Mandatory: true, Source: "mandatory"}
	}
	for _, x := range m.Block.Signatures {
		c.Signatures[x] = Entry{Mandatory: true, Source: "mandatory"}
	}
	if m.MaxFileSize > 0 && (c.MaxFileSize == 0 || c.MaxFileSize > m.MaxFileSize) {
		c.MaxFileSize, c.SizeSource = m.MaxFileSize, "mandatory.max_file_size"
	}
	return c
}

func (e *Engine) level(name string, l policy.Layer, ref string) level {
	lv := level{scope: namedLayer{name, l}}
	globs := make([]string, 0, len(l.Refs))
	for g := range l.Refs {
		globs = append(globs, g)
	}
	sort.Strings(globs)
	for _, g := range globs {
		if e.match(g, ref, false) {
			lv.refs = append(lv.refs, namedLayer{name + ".refs[" + quote(g) + "]", l.Refs[g]})
		}
	}
	return lv
}

func merge(a, b policy.ContentSet) policy.ContentSet {
	return policy.ContentSet{
		Extensions: append(append([]string{}, a.Extensions...), b.Extensions...),
		Paths:      append(append([]string{}, a.Paths...), b.Paths...),
		Signatures: append(append([]string{}, a.Signatures...), b.Signatures...),
	}
}

// HasRules reports whether any content rule applies (otherwise the push
// needs no object inspection at all).
func (c Content) HasRules() bool {
	return len(c.Extensions)+len(c.Paths)+len(c.Signatures) > 0 || c.MaxFileSize > 0
}

// Classifier assigns refs of one project to policy classes: refs whose
// effective content rules are identical share a class (PHASE-1 R1-2).
type Classifier struct {
	e                 *Engine
	project, repoMode string
	byKey             map[string]int
	byRef             map[string]int
	contents          []Content
}

// Classifier returns a classifier for one project.
func (e *Engine) Classifier(project, repoMode string) *Classifier {
	return &Classifier{e: e, project: project, repoMode: repoMode, byKey: map[string]int{}, byRef: map[string]int{}}
}

// ClassOf returns the class id of ref.
func (c *Classifier) ClassOf(ref string) int {
	if id, ok := c.byRef[ref]; ok {
		return id
	}
	content := c.e.ResolveContent(c.project, ref, c.repoMode)
	key := fingerprint(content)
	id, ok := c.byKey[key]
	if !ok {
		id = len(c.contents)
		c.contents = append(c.contents, content)
		c.byKey[key] = id
	}
	c.byRef[ref] = id
	return id
}

// Content returns the effective content rules of a class.
func (c *Classifier) Content(id int) Content { return c.contents[id] }

// Covers reports whether content already vetted under class a counts as
// vetted for class b: a is at least as strict as b in every dimension.
func (c *Classifier) Covers(a, b int) bool {
	if a == b {
		return true
	}
	x, y := c.contents[a], c.contents[b]
	superset := func(p, q map[string]Entry) bool {
		for k := range q {
			if _, ok := p[k]; !ok {
				return false
			}
		}
		return true
	}
	if !superset(x.Extensions, y.Extensions) || !superset(x.Paths, y.Paths) || !superset(x.Signatures, y.Signatures) {
		return false
	}
	if y.MaxFileSize > 0 && (x.MaxFileSize == 0 || x.MaxFileSize > y.MaxFileSize) {
		return false
	}
	// Content accepted in audit mode was never actually enforced.
	return !(y.Mode == "enforce" && x.Mode != "enforce")
}

func fingerprint(c Content) string {
	keys := func(m map[string]Entry) string {
		ks := make([]string, 0, len(m))
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return strings.Join(ks, "\x1f")
	}
	return strings.Join([]string{keys(c.Extensions), keys(c.Paths), keys(c.Signatures),
		strconv.FormatInt(c.MaxFileSize, 10), c.Mode, c.MandatoryMode, strconv.FormatBool(c.Enabled)}, "\x1e")
}
