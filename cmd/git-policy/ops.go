package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/soroush67/git-policy/internal/admin"
	"github.com/soroush67/git-policy/internal/hook"
	"github.com/soroush67/git-policy/internal/layout"
	"github.com/soroush67/git-policy/internal/membership"
	"github.com/soroush67/git-policy/internal/policy"
	"github.com/soroush67/git-policy/internal/store"
)

func cmdHook(args []string, stdin io.Reader, stderr io.Writer) int {
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", layout.DefaultRoot, "installation root")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		// A broken invocation must never let a push through.
		fmt.Fprintln(stderr, "GL-HOOK-ERR: Push rejected: git-policy hook invoked incorrectly. Contact the platform team.")
		return exitInvalid
	}
	return hook.Run(layout.New(*root), hook.EnvFromOS(), stdin, stderr, time.Now())
}

func cmdStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "machine-readable output")
	root := fs.String("root", layout.DefaultRoot, "installation root")
	hookPath := fs.String("hook-path", "", "installed hook wrapper (default for the production root)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return exitUsage
	}
	l := layout.New(*root)
	if *hookPath == "" && l.Root == layout.DefaultRoot {
		*hookPath = layout.HookPath
	}
	r := admin.Status(l, *hookPath, time.Now())
	if *asJSON {
		writeJSON(stdout, r)
	} else {
		printStatus(stdout, r)
	}
	switch r.Health {
	case admin.HealthOK:
		return 0
	case admin.HealthWarning:
		return 1
	}
	return 3
}

func printStatus(w io.Writer, r admin.Report) {
	row := func(k, v string) { fmt.Fprintf(w, "  %-18s %s\n", k+":", v) }
	fmt.Fprintf(w, "git-policy v%s  (root %s)\n", r.EngineVersion, r.Root)
	e := r.Engine
	engine := e.State
	if e.ExpiresAt != "" && e.State == "disabled" {
		engine += " until " + e.ExpiresAt
	}
	row("Engine", engine)
	if e.ChangedBy != "" {
		row("  changed", e.ChangedAt+" by "+e.ChangedBy)
	}
	if e.Reason != "" {
		row("  reason", e.Reason)
	}
	if e.Error != "" {
		row("  error", e.Error)
	}
	bg := "absent"
	if r.BreakGlass {
		bg = "PRESENT — all pushes bypass git-policy"
	}
	row("Break-glass", bg)
	hookLine := r.Hook.State
	if r.Hook.Path != "" {
		hookLine += " (" + r.Hook.Path + ")"
	}
	row("Hook wrapper", hookLine)
	if len(r.Hook.OtherHooks) > 0 {
		row("  other hooks", strings.Join(r.Hook.OtherHooks, ", "))
	}
	p := r.Policy
	if p.Loaded {
		active := fmt.Sprintf("%s  %s revision %d", p.Version, p.Name, p.Revision)
		if p.FellBack {
			active += "  (FALLBACK: ACTIVE unusable)"
		}
		row("Active policy", active)
		row("Policy checksum", "sha256:"+p.SourceSHA256)
		row("Last deployment", p.DeployedAt+" by "+p.DeployedBy)
	} else {
		row("Active policy", "none ("+p.Error+")")
	}
	prev := p.Previous
	if prev == "" {
		prev = "-"
	}
	row("Previous version", prev)
	row("Stored versions", strconv.Itoa(p.Stored))
	row("Membership cache", r.Membership)
	row("Audit log", r.AuditLog)
	fmt.Fprintf(w, "HEALTH: %s\n", r.Health)
	for _, pr := range r.Problems {
		fmt.Fprintf(w, "  - %s\n", pr)
	}
}

func cmdAdmin(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	sub := args[0]
	fs := flag.NewFlagSet("admin "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", layout.DefaultRoot, "installation root")
	hookPath := fs.String("hook-path", "", "hook wrapper path (default for the production root)")
	actor := fs.String("actor", "", "who performs the change (recorded in the audit log)")
	reason := fs.String("reason", "", "why (recorded in the audit log)")
	ttl := fs.String("ttl", "", "disable duration, e.g. 30m, 2h")
	to := fs.String("to", "", "rollback target version, e.g. 000003 or 3")
	replaceHook := fs.Bool("replace-hook", false, "install: back up and replace a different existing hook")
	force := fs.Bool("force", false, "uninstall: remove a modified hook wrapper; apply-membership: override the safety guard")
	name := fs.String("name", "", "retire-hook: file name in pre-receive.d, e.g. 01-block-dll")
	days := fs.Int("days", 0, "prune-logs: retention in days (default: settings.audit.retention_days)")
	if err := fs.Parse(args[1:]); err != nil {
		return exitUsage
	}
	ctx, err := admin.NewContext(*root, *hookPath, *actor)
	if err != nil {
		fmt.Fprintf(stderr, "git-policy: %v\n", err)
		return exitUsage
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "git-policy admin %s: %v\n", sub, err)
		return exitInvalid
	}

	switch sub {
	case "install":
		self, err := os.Executable()
		if err != nil {
			return fail(err)
		}
		rep, err := ctx.Install(self, *replaceHook)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "installed into %s\n  binary:      %s\n  state:       %s\n  hook:        %s (%s)\n",
			ctx.L.Root, yesNo(rep.BinaryUpdated, "updated", "unchanged"),
			yesNo(rep.StateCreated, "created (engine DISABLED until enabled)", "kept"), rep.HookAction, ctx.HookPath)
		if len(rep.OtherHooks) > 0 {
			fmt.Fprintf(stdout, "  other hooks: %s (left untouched)\n", strings.Join(rep.OtherHooks, ", "))
		}
	case "uninstall":
		backup, err := ctx.Uninstall(*force)
		if err != nil {
			return fail(err)
		}
		if backup == "" {
			fmt.Fprintln(stdout, "hook wrapper not installed; nothing to do")
		} else {
			fmt.Fprintf(stdout, "hook wrapper removed (backup: %s); policy, state and logs kept\n", backup)
		}
	case "retire-hook":
		backup, err := ctx.RetireHook(*name)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "hook %s retired (backup: %s)\n", *name, backup)
	case "enable":
		if err := ctx.Enable(*reason); err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, "engine ENABLED")
	case "disable":
		d, err := policy.ParseDuration(*ttl)
		if err != nil {
			return fail(fmt.Errorf("--ttl: %w", err))
		}
		exp, err := ctx.Disable(*reason, d)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "engine DISABLED until %s (re-enables automatically)\n", exp.Format(time.RFC3339))
	case "apply":
		if fs.NArg() != 1 {
			return fail(errors.New("exactly one policy file (or -) is required"))
		}
		src, err := readPolicy(fs.Arg(0), stdin)
		if err != nil {
			return fail(err)
		}
		res, err := ctx.Apply(src, *reason)
		if res != nil {
			for _, f := range res.Findings {
				fmt.Fprintf(stderr, "  %s\n", f)
			}
		}
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "policy %s revision %d deployed as version %s (previous: %s)\n  sha256: %s\n",
			res.Meta.Name, res.Meta.Revision, layout.VersionName(res.Meta.Version), versionOrDash(res.Previous), res.Meta.SourceSHA256)
	case "rollback":
		n := 0
		if *to != "" {
			if n, err = strconv.Atoi(strings.TrimLeft(*to, "0")); err != nil || n < 1 {
				return fail(fmt.Errorf("invalid --to %q", *to))
			}
		}
		from, target, err := ctx.Rollback(n, *reason)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "rolled back: %s -> %s\n", versionOrDash(from), layout.VersionName(target))
	case "versions":
		return listVersions(ctx.L, stdout, stderr)
	case "apply-membership":
		if fs.NArg() != 1 {
			return fail(errors.New("exactly one membership file (or -) is required"))
		}
		var data []byte
		if fs.Arg(0) == "-" {
			data, err = io.ReadAll(io.LimitReader(stdin, membership.MaxSize+1))
		} else {
			data, err = os.ReadFile(fs.Arg(0))
		}
		if err != nil {
			return fail(err)
		}
		res, err := ctx.ApplyMembership(data, *force)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "membership cache updated: %d group(s), %d user(s), %d membership(s) (previously %d)%s\n",
			res.Groups, res.Users, res.Pairs, res.PreviousPairs, yesNo(res.Forced, " [forced]", ""))
	case "prune-logs":
		removed, err := ctx.PruneLogs(*days)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "pruned %d audit file(s)\n", len(removed))
		for _, r := range removed {
			fmt.Fprintf(stdout, "  %s\n", r)
		}
	default:
		fmt.Fprintf(stderr, "git-policy: unknown admin command %q\n\n%s", sub, usage)
		return exitUsage
	}
	return exitOK
}

func listVersions(l layout.Layout, stdout, stderr io.Writer) int {
	vs, err := store.Versions(l)
	if err != nil {
		fmt.Fprintf(stderr, "git-policy: %v\n", err)
		return exitInvalid
	}
	active, _ := store.ActiveVersion(l)
	prev, _ := store.PreviousVersion(l)
	fmt.Fprintf(stdout, "%-3s %-7s %-22s %-5s %-21s %s\n", "", "VERSION", "NAME", "REV", "DEPLOYED", "BY")
	for _, v := range vs {
		mark := ""
		switch v {
		case active:
			mark = "*"
		case prev:
			mark = "p"
		}
		ld, err := store.LoadVersion(l, v)
		if err != nil {
			fmt.Fprintf(stdout, "%-3s %-7s CORRUPT: %v\n", mark, layout.VersionName(v), err)
			continue
		}
		m := ld.Meta
		fmt.Fprintf(stdout, "%-3s %-7s %-22s %-5d %-21s %s\n", mark, layout.VersionName(v), m.Name, m.Revision, m.DeployedAt.Format(time.RFC3339), m.DeployedBy)
	}
	fmt.Fprintln(stdout, "(* active, p previous)")
	return exitOK
}

func yesNo(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}

func versionOrDash(n int) string {
	if n == 0 {
		return "-"
	}
	return layout.VersionName(n)
}
