package policy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// MaxPolicySize bounds the policy document; far above any realistic policy.
const MaxPolicySize = 4 << 20

// Parse decodes a policy document strictly: exactly one YAML document, no
// anchors/aliases/merge keys, no duplicate keys, no unknown keys, correct
// types. The document is returned only when there are no findings.
func Parse(data []byte) (*Document, []Finding) {
	if len(data) > MaxPolicySize {
		return nil, []Finding{errorf("V001", "", 0, "policy is larger than %d bytes", MaxPolicySize)}
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	var root yaml.Node
	if err := dec.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, []Finding{errorf("V001", "", 0, "policy document is empty")}
		}
		return nil, []Finding{syntaxFinding(err)}
	}
	var extra yaml.Node
	switch err := dec.Decode(&extra); {
	case err == nil:
		return nil, []Finding{errorf("V001", "", extra.Line, "policy must contain exactly one YAML document")}
	case !errors.Is(err, io.EOF):
		return nil, []Finding{syntaxFinding(err)}
	}
	if len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return nil, []Finding{errorf("V001", "", root.Line, "top level of the policy must be a mapping")}
	}
	if fs := checkNodes(&root, ""); len(fs) > 0 {
		return nil, fs
	}

	strict := yaml.NewDecoder(bytes.NewReader(data))
	strict.KnownFields(true)
	var doc Document
	if err := strict.Decode(&doc); err != nil {
		return nil, decodeFindings(err)
	}
	return &doc, nil
}

// checkNodes rejects YAML features a policy does not need and that make
// review harder (anchors, aliases, merge keys) and reports duplicate keys with
// line numbers. yaml.v3 alone would reject duplicates but without the path.
func checkNodes(n *yaml.Node, path string) []Finding {
	var out []Finding
	if n.Anchor != "" {
		out = append(out, errorf("V001", path, n.Line, "YAML anchors are not supported in policy documents"))
	}
	switch n.Kind {
	case yaml.AliasNode:
		out = append(out, errorf("V001", path, n.Line, "YAML aliases are not supported in policy documents"))
	case yaml.MappingNode:
		seen := map[string]int{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			p := joinPath(path, k.Value)
			if k.Value == "<<" {
				out = append(out, errorf("V001", path, k.Line, "YAML merge keys (<<) are not supported"))
				continue
			}
			if first, dup := seen[k.Value]; dup {
				out = append(out, errorf("V002", p, k.Line, "duplicate key %q (first defined on line %d)", k.Value, first))
			} else {
				seen[k.Value] = k.Line
			}
			out = append(out, checkNodes(v, p)...)
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			out = append(out, checkNodes(c, index(path, i))...)
		}
	case yaml.DocumentNode:
		for _, c := range n.Content {
			out = append(out, checkNodes(c, path)...)
		}
	}
	return out
}

var (
	lineMsgRe    = regexp.MustCompile(`^line (\d+): (.*)$`)
	unknownKeyRe = regexp.MustCompile(`^field (\S+) not found in type policy\.(\w+)$`)
)

// sectionNames maps Go types to the policy sections users know.
var sectionNames = map[string]string{
	"Document": "top level", "Metadata": "metadata", "Settings": "settings",
	"RepositoryTypes": "settings.repository_types", "Limits": "settings.limits",
	"MembershipSettings": "settings.membership", "AuditSettings": "settings.audit",
	"ExceptionSettings": "settings.exceptions", "Messages": "settings.messages",
	"Mandatory": "mandatory", "Defaults": "defaults", "RefScope": "a refs entry",
	"Scope": "a namespace", "ProjectScope": "a project", "IdentityRule": "a user/group rule",
	"ScopedIdentity": "a scoped user/group rule", "Exception": "an exception",
	"Subjects": "exception subjects", "ExceptionScope": "exception scope",
}

var unknownKeyHints = map[string]string{
	"bypass_content_policy": "bypass flags do not exist; add an entry under exceptions",
	"bypass_all":            "bypass flags do not exist; add an entry under exceptions",
	"direct_push":           "direct-push control belongs to GitLab Protected Branches",
	"branches":              "ref rules live under refs: with full ref globs (refs/heads/...)",
	"max_file_size_mb":      "use max_file_size with a unit, e.g. 20MiB",
}

func decodeFindings(err error) []Finding {
	var te *yaml.TypeError
	if !errors.As(err, &te) {
		return []Finding{syntaxFinding(err)}
	}
	out := make([]Finding, 0, len(te.Errors))
	for _, msg := range te.Errors {
		line := 0
		if m := lineMsgRe.FindStringSubmatch(msg); m != nil {
			line, _ = strconv.Atoi(m[1])
			msg = m[2]
		}
		if m := unknownKeyRe.FindStringSubmatch(msg); m != nil {
			key, typ := m[1], m[2]
			code := "V003"
			if typ == "Mandatory" {
				code = "V022" // mandatory is global and ref-independent: no refs, no unblock_*
			}
			section := sectionNames[typ]
			if section == "" {
				section = typ
			}
			text := fmt.Sprintf("unknown key %q in %s", key, section)
			if hint := unknownKeyHints[key]; hint != "" {
				text += " (" + hint + ")"
			}
			out = append(out, errorf(code, "", line, "%s", text))
			continue
		}
		code := "V005"
		if strings.Contains(msg, "already defined") {
			code = "V002"
		}
		out = append(out, errorf(code, "", line, "%s", strings.ReplaceAll(msg, "policy.", "")))
	}
	return out
}

func syntaxFinding(err error) Finding {
	msg := strings.TrimPrefix(err.Error(), "yaml: ")
	line := 0
	if m := lineMsgRe.FindStringSubmatch(msg); m != nil {
		line, _ = strconv.Atoi(m[1])
		msg = m[2]
	}
	return errorf("V001", "", line, "YAML syntax error: %s", msg)
}
