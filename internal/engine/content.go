package engine

import (
	"sort"

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
