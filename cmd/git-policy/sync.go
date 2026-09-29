package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/soroush67/git-policy/internal/layout"
	"github.com/soroush67/git-policy/internal/membersync"
	"github.com/soroush67/git-policy/internal/policy"
	"github.com/soroush67/git-policy/internal/store"
)

// loadPolicy compiles --policy FILE, or reads the active policy from --root.
func loadPolicy(file, root string) (*policy.Compiled, error) {
	if file != "" {
		src, err := readPolicy(file, nil)
		if err != nil {
			return nil, err
		}
		c, findings := policy.Build(src, policy.Options{})
		if c == nil {
			msgs := make([]string, 0, len(findings))
			for _, f := range findings {
				msgs = append(msgs, f.String())
			}
			return nil, errors.New("policy is invalid:\n  " + strings.Join(msgs, "\n  "))
		}
		return c, nil
	}
	act, err := store.LoadActive(layout.New(root))
	if err != nil {
		return nil, fmt.Errorf("no active policy: %w (use --policy FILE)", err)
	}
	return act.Policy, nil
}

// cmdGroups prints the GitLab groups a policy depends on, one per line.
func cmdGroups(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("groups", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", layout.DefaultRoot, "installation root (active policy)")
	file := fs.String("policy", "", "policy file instead of the active policy")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return exitUsage
	}
	c, err := loadPolicy(*file, *root)
	if err != nil {
		fmt.Fprintf(stderr, "git-policy: %v\n", err)
		return exitInvalid
	}
	for _, g := range policy.ReferencedGroups(c) {
		fmt.Fprintln(stdout, g)
	}
	return exitOK
}

// cmdSyncMembership builds membership.json (and optionally inventory.json)
// from the GitLab API. Runs on the Jenkins agent; the token comes from the
// GITLAB_TOKEN environment variable or --token-file and is never printed.
func cmdSyncMembership(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sync-membership", flag.ContinueOnError)
	fs.SetOutput(stderr)
	gitlabURL := fs.String("gitlab-url", "", "GitLab base URL, e.g. https://gitlab.example.com (required)")
	tokenFile := fs.String("token-file", "", "file containing the API token (default: $GITLAB_TOKEN)")
	file := fs.String("policy", "", "take the group list from this policy file")
	groups := fs.String("groups", "", "comma/space/newline separated group paths (e.g. output of `ctl groups`)")
	out := fs.String("o", "", "write membership JSON here (default: stdout)")
	invOut := fs.String("inventory-out", "", "also write the GitLab inventory (users, groups, projects) here")
	caFile := fs.String("ca-file", "", "extra CA bundle for GitLab TLS")
	insecure := fs.Bool("insecure", false, "skip TLS verification (lab only)")
	timeout := fs.Duration("timeout", 30*time.Second, "per-request timeout")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *gitlabURL == "" {
		fmt.Fprintln(stderr, "usage: git-policy sync-membership --gitlab-url URL (--policy FILE | --groups LIST) [-o FILE] [--inventory-out FILE] [--token-file F] [--ca-file F] [--insecure]")
		return exitUsage
	}
	// Credential files and secret stores often add a trailing newline.
	token := strings.TrimSpace(os.Getenv("GITLAB_TOKEN"))
	if *tokenFile != "" {
		b, err := os.ReadFile(*tokenFile)
		if err != nil {
			fmt.Fprintf(stderr, "git-policy: %v\n", err)
			return exitUsage
		}
		token = strings.TrimSpace(string(b))
	}
	var list []string
	switch {
	case *file != "":
		c, err := loadPolicy(*file, "")
		if err != nil {
			fmt.Fprintf(stderr, "git-policy: %v\n", err)
			return exitInvalid
		}
		list = policy.ReferencedGroups(c)
	case *groups != "":
		list = strings.FieldsFunc(*groups, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
	default:
		fmt.Fprintln(stderr, "git-policy: --policy or --groups is required")
		return exitUsage
	}
	client, err := membersync.NewClient(*gitlabURL, token, *caFile, *insecure, *timeout)
	if err != nil {
		fmt.Fprintf(stderr, "git-policy: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	now := time.Now()
	m, err := client.Membership(ctx, list, now)
	if err != nil {
		fmt.Fprintf(stderr, "git-policy: sync failed, nothing written: %v\n", err)
		return exitInvalid
	}
	if m.Groups == nil {
		m.Groups = []string{}
	}
	if err := writeJSONFile(*out, stdout, m); err != nil {
		fmt.Fprintf(stderr, "git-policy: %v\n", err)
		return exitUsage
	}
	fmt.Fprintf(stderr, "membership: %d group(s), %d user(s), %d membership(s)\n", len(m.Groups), len(m.Users), m.Pairs())
	if *invOut != "" {
		inv, err := client.FetchInventory(ctx, now)
		if err != nil {
			fmt.Fprintf(stderr, "git-policy: inventory failed: %v\n", err)
			return exitInvalid
		}
		if err := writeJSONFile(*invOut, nil, inv); err != nil {
			fmt.Fprintf(stderr, "git-policy: %v\n", err)
			return exitUsage
		}
		fmt.Fprintf(stderr, "inventory: %d user(s), %d group(s), %d project(s)\n", len(inv.Users), len(inv.Groups), len(inv.Projects))
	}
	return exitOK
}

func writeJSONFile(path string, stdout io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if path == "" || path == "-" {
		_, err = stdout.Write(b)
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// loadInventory reads an inventory file for validate --inventory.
func loadInventory(path string) (*policy.Inventory, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var inv membersync.Inventory
	if err := json.Unmarshal(b, &inv); err != nil || inv.Schema != membersync.InventorySchema {
		return nil, fmt.Errorf("%s: not a git-policy inventory file", path)
	}
	return policy.NewInventory(inv.Users, inv.Groups, inv.Projects), nil
}

// cmdShowPolicy prints the stored source (policy.yaml) of the active or a
// given version — exactly the approved text, used by Jenkins for diffs.
func cmdShowPolicy(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("show-policy", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", layout.DefaultRoot, "installation root")
	ver := fs.Int("version", 0, "stored version number (default: active)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *ver < 0 {
		return exitUsage
	}
	l := layout.New(*root)
	n := *ver
	if n == 0 {
		act, err := store.LoadActive(l)
		if err != nil {
			fmt.Fprintf(stderr, "git-policy: no active policy: %v\n", err)
			return exitInvalid
		}
		n = act.Version
	}
	if _, err := store.LoadVersion(l, n); err != nil { // verifies integrity first
		fmt.Fprintf(stderr, "git-policy: %v\n", err)
		return exitInvalid
	}
	b, err := os.ReadFile(l.VersionDir(n) + "/policy.yaml")
	if err != nil {
		fmt.Fprintf(stderr, "git-policy: %v\n", err)
		return exitInvalid
	}
	stdout.Write(b)
	return exitOK
}
