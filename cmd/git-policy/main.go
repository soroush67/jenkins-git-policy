// Command git-policy is the server-side Git policy engine for GitLab.
//
// Commands: the pre-receive runtime (hook), read-only tooling (version,
// validate, compile, status) and privileged management (admin ...).
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/soroush67/git-policy/internal/policy"
	"github.com/soroush67/git-policy/internal/version"
)

// Exit codes shared by all subcommands.
const (
	exitOK      = 0
	exitInvalid = 1 // policy invalid / request rejected
	exitUsage   = 2 // usage or I/O error
)

const usage = `git-policy — server-side Git policy engine for GitLab

Usage:
  git-policy version  [--json]
  git-policy validate [--json] [--now YYYY-MM-DD] <policy.yaml | ->
  git-policy compile  [--now YYYY-MM-DD] [-o compiled.json] <policy.yaml | ->
  git-policy status   [--json] [--root DIR] [--hook-path FILE]
  git-policy explain  --project PATH [--user NAME] [--groups a,b] [--ref REF] [--policy FILE] [--json]
  git-policy scan     [--policy FILE] [--project PATH] [--repo DIR] [--json] < ref-updates   (diagnostics)
  git-policy hook     [--root DIR]                 (run by the pre-receive wrapper)

  git-policy admin install   [--replace-hook]        install binary, layout, hook wrapper
  git-policy admin uninstall [--force]               remove the hook wrapper (data kept)
  git-policy admin enable    [--reason TEXT]
  git-policy admin disable   --reason TEXT --ttl DURATION   (e.g. 30m, 2h; max 168h)
  git-policy admin apply     [--reason TEXT] <policy.yaml | ->
  git-policy admin rollback  --reason TEXT [--to VERSION]
  git-policy admin versions
    common admin flags: [--root DIR] [--hook-path FILE] [--actor NAME]

Exit codes: 0 ok, 1 invalid/rejected/refused, 2 usage or I/O error.
status:     0 OK, 1 WARNING, 3 CRITICAL.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	switch args[0] {
	case "version", "--version":
		return cmdVersion(args[1:], stdout, stderr)
	case "validate":
		return cmdValidate(args[1:], stdin, stdout, stderr)
	case "compile":
		return cmdCompile(args[1:], stdin, stdout, stderr)
	case "hook":
		return cmdHook(args[1:], stdin, stderr)
	case "status":
		return cmdStatus(args[1:], stdout, stderr)
	case "explain":
		return cmdExplain(args[1:], stdout, stderr)
	case "scan":
		return cmdScan(args[1:], stdin, stdout, stderr)
	case "admin":
		return cmdAdmin(args[1:], stdin, stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return exitOK
	}
	fmt.Fprintf(stderr, "git-policy: unknown command %q\n\n%s", args[0], usage)
	return exitUsage
}

func cmdVersion(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *asJSON {
		return writeJSON(stdout, map[string]string{
			"version": version.Version, "commit": version.Commit, "build_date": version.BuildDate,
			"policy_api_version": version.PolicyAPIVersion, "platform": version.Platform(),
		})
	}
	fmt.Fprintf(stdout, "git-policy v%s\n  policy schema: %s\n  commit:        %s\n  built:         %s\n  platform:      %s\n",
		version.Version, version.PolicyAPIVersion, version.Commit, version.BuildDate, version.Platform())
	return exitOK
}

type validateResult struct {
	File     string           `json:"file"`
	Valid    bool             `json:"valid"`
	Errors   int              `json:"errors"`
	Warnings int              `json:"warnings"`
	Name     string           `json:"name,omitempty"`
	Revision int              `json:"revision,omitempty"`
	SHA256   string           `json:"sha256"`
	Findings []policy.Finding `json:"findings"`
}

func cmdValidate(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "machine-readable output")
	now := fs.String("now", "", "reference date for exception expiry (YYYY-MM-DD, default today)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	opts, err := options(*now)
	if err != nil {
		fmt.Fprintf(stderr, "git-policy: %v\n", err)
		return exitUsage
	}
	file := fs.Arg(0)
	src, err := readPolicy(file, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "git-policy: %v\n", err)
		return exitUsage
	}

	compiled, findings := policy.Build(src, opts)
	errs, warns := policy.Count(findings)
	res := validateResult{File: file, Valid: errs == 0, Errors: errs, Warnings: warns,
		SHA256: policy.SourceSHA256(src), Findings: findings}
	if res.Findings == nil {
		res.Findings = []policy.Finding{}
	}
	if compiled != nil {
		res.Name, res.Revision = compiled.Name, compiled.Revision
	}

	if *asJSON {
		writeJSON(stdout, res)
	} else {
		fmt.Fprintf(stdout, "git-policy validate: %s\n", file)
		if compiled != nil {
			fmt.Fprintf(stdout, "  policy:   %s (revision %d)\n", res.Name, res.Revision)
		}
		fmt.Fprintf(stdout, "  sha256:   %s\n", res.SHA256)
		for _, f := range findings {
			fmt.Fprintf(stdout, "  %s\n", f)
		}
		verdict := "VALID"
		if !res.Valid {
			verdict = "INVALID"
		}
		fmt.Fprintf(stdout, "RESULT: %s (%d errors, %d warnings)\n", verdict, errs, warns)
	}
	if !res.Valid {
		return exitInvalid
	}
	return exitOK
}

func cmdCompile(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("compile", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "", "write compiled.json to this file instead of stdout")
	now := fs.String("now", "", "reference date for exception expiry (YYYY-MM-DD, default today)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	opts, err := options(*now)
	if err != nil {
		fmt.Fprintf(stderr, "git-policy: %v\n", err)
		return exitUsage
	}
	src, err := readPolicy(fs.Arg(0), stdin)
	if err != nil {
		fmt.Fprintf(stderr, "git-policy: %v\n", err)
		return exitUsage
	}
	compiled, findings := policy.Build(src, opts)
	for _, f := range findings {
		fmt.Fprintf(stderr, "%s\n", f)
	}
	if compiled == nil {
		fmt.Fprintln(stderr, "git-policy: policy is invalid; nothing compiled")
		return exitInvalid
	}
	b, err := policy.MarshalCompiled(compiled)
	if err != nil {
		fmt.Fprintf(stderr, "git-policy: %v\n", err)
		return exitUsage
	}
	if *out == "" {
		_, err = stdout.Write(b)
	} else {
		// Phase 3: plain write for inspection. Atomic activation (tmp + fsync +
		// rename) is the job of `admin apply` (Phase 4), never of compile.
		err = os.WriteFile(*out, b, 0o640)
	}
	if err != nil {
		fmt.Fprintf(stderr, "git-policy: %v\n", err)
		return exitUsage
	}
	return exitOK
}

func options(now string) (policy.Options, error) {
	if now == "" {
		return policy.Options{}, nil
	}
	t, err := policy.ParseDate(now)
	if err != nil {
		return policy.Options{}, fmt.Errorf("--now: %w", err)
	}
	return policy.Options{Now: t.Add(12 * time.Hour)}, nil
}

// readPolicy reads a policy file (or stdin for "-") with a size cap, refusing
// anything that is not a regular file.
func readPolicy(name string, stdin io.Reader) ([]byte, error) {
	var r io.Reader = stdin
	if name != "-" {
		st, err := os.Stat(name)
		if err != nil {
			return nil, err
		}
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("%s: not a regular file", name)
		}
		f, err := os.Open(name)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, policy.MaxPolicySize+1))
	if err != nil {
		return nil, err
	}
	if len(b) > policy.MaxPolicySize {
		return nil, errors.New("policy is larger than 4 MiB")
	}
	return b, nil
}

func writeJSON(w io.Writer, v any) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return exitUsage
	}
	return exitOK
}
