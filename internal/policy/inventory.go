package policy

import (
	"sort"
	"strings"
)

// Inventory is what exists in GitLab (from `git-policy sync-membership
// --inventory-out`). When given to Validate, names the policy uses but
// GitLab does not know produce warning W010 — typically typos.
type Inventory struct {
	Users    map[string]bool
	Groups   map[string]bool
	Projects map[string]bool
}

// NewInventory builds lookup sets (case-folded).
func NewInventory(users, groups, projects []string) *Inventory {
	set := func(xs []string) map[string]bool {
		m := make(map[string]bool, len(xs))
		for _, x := range xs {
			m[strings.ToLower(x)] = true
		}
		return m
	}
	return &Inventory{Users: set(users), Groups: set(groups), Projects: set(projects)}
}

// ReferencedGroups returns every GitLab group a compiled policy depends on:
// group rules, mandatory.deny_groups and exception subjects. These are the
// groups the membership sync must fetch.
func ReferencedGroups(c *Compiled) []string {
	set := map[string]bool{}
	for g := range c.Groups {
		set[g] = true
	}
	for _, g := range c.Mandatory.DenyGroups {
		set[g] = true
	}
	for _, x := range c.Exceptions {
		for _, g := range x.Groups {
			set[g] = true
		}
	}
	out := make([]string, 0, len(set))
	for g := range set {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

// inventoryWarnings reports W010 for names unknown to GitLab.
func (v *validator) inventoryWarnings(inv *Inventory) {
	d := v.doc
	user := func(path, u string) {
		if u != UnknownUser && !inv.Users[strings.ToLower(u)] {
			v.warn("W010", path, "user %q does not exist in GitLab (inventory)", u)
		}
	}
	group := func(path, g string) {
		if !inv.Groups[strings.ToLower(g)] {
			v.warn("W010", path, "group %q does not exist in GitLab (inventory)", g)
		}
	}
	namespace := func(path, n string) { // a namespace can be a group or a personal namespace
		if l := strings.ToLower(n); !inv.Groups[l] && !inv.Users[l] {
			v.warn("W010", path, "namespace %q does not exist in GitLab (inventory)", n)
		}
	}
	project := func(path, p string) {
		if !inv.Projects[strings.ToLower(p)] {
			v.warn("W010", path, "project %q does not exist in GitLab (inventory)", p)
		}
	}
	identity := func(base string, r *IdentityRule) {
		if r == nil {
			return
		}
		for _, n := range sortedKeys(r.Namespaces) {
			namespace(joinPath(base+".namespaces", n), n)
		}
		for _, p := range sortedKeys(r.Projects) {
			project(joinPath(base+".projects", p), p)
		}
	}
	for _, u := range sortedKeys(d.Users) {
		user(joinPath("users", u), u)
		identity(joinPath("users", u), d.Users[u])
	}
	for _, g := range sortedKeys(d.Groups) {
		group(joinPath("groups", g), g)
		identity(joinPath("groups", g), d.Groups[g])
	}
	if m := d.Mandatory; m != nil {
		for i, u := range m.DenyUsers {
			user(index("mandatory.deny_users", i), u)
		}
		for i, g := range m.DenyGroups {
			group(index("mandatory.deny_groups", i), g)
		}
	}
	for _, n := range sortedKeys(d.Namespaces) {
		namespace(joinPath("namespaces", n), n)
	}
	for _, p := range sortedKeys(d.Projects) {
		project(joinPath("projects", p), p)
	}
	for i, x := range d.Exceptions {
		if x == nil {
			continue
		}
		base := index("exceptions", i)
		if s := x.Subjects; s != nil {
			for j, u := range s.Users {
				user(index(base+".subjects.users", j), u)
			}
			for j, g := range s.Groups {
				group(index(base+".subjects.groups", j), g)
			}
		}
		if s := x.Scope; s != nil {
			for j, n := range s.Namespaces {
				namespace(index(base+".scope.namespaces", j), n)
			}
			for j, p := range s.Projects {
				project(index(base+".scope.projects", j), p)
			}
		}
	}
}
