# git-policy

Server-side Git policy enforcement for GitLab Self-Managed (Gitaly global
pre-receive hook). Jenkins is the control plane; the engine enforces locally
and keeps working when Jenkins, Nexus or the GitLab API are down.

Software version: see `VERSION` (v0.1.0). Policy schema: `apiVersion: git-policy/v1`.

> Status: under phased development. Phases 1-9 are done and verified end-to-end on a real
> GitLab CE 17.10.5 lab (install, enable/disable/status, atomic policy versions, identity
> rules, anti-bypass traversal, extension/path/size/PE enforcement, audit log).
> Jenkins is the operator UI (Phase 11, GitOps). Full documentation (INSTALL, CONFIGURATION,
> SECURITY, OPERATIONS, TROUBLESHOOTING) arrives in Phase 15.
> Design: [Phase 1 architecture](docs/design/PHASE-1-ARCHITECTURE.md),
> [Phase 2 schema & precedence](docs/design/PHASE-2-SCHEMA.md).

## Layout

```
cmd/git-policy/        CLI entrypoint (static Go binary)
internal/policy/       strict loader, validator (V/W codes), compiler -> compiled.json
internal/layout/       on-server paths, ownership and modes
internal/version/      software version (set via -ldflags)
internal/admin/        install, enable/disable, apply/rollback, status; embeds the hook wrapper (pre-receive.sh)
internal/engine/       push evaluation: identity, scope, exceptions, effective content rules
internal/membership/   local group-membership cache (no GitLab API on the push path)
internal/gitscan/      anti-bypass traversal: every new commit, policy classes, tags, limits (git plumbing only)
internal/{hook,store,state,audit,message,fsutil}/  push runtime, policy versions, engine switch, audit log, output, atomic fs
tests/integration/     real `git push` tests; tests/examples/verify.sh checks every scenario row (66 pushes)
install.sh, uninstall.sh  run on the Docker host against the gitlab container
docs/fa/               Persian docs: POLICY-FA (writing policies, step by step), GUIDE-FA (guide),
                       CLI-FA (every command/option), EXAMPLES-FA (10 scenarios)
deploy/git-policy-ctl  the only command Jenkins may run on the Docker host (SSH forced command)
deploy/install-ctl.sh  installs that channel on the real Docker host (user, sudoers, forced-command key)
jenkins/Jenkinsfile    control-plane pipeline (13 actions, TEST/PRODUCTION, four-eyes approval)
jenkins/Jenkinsfile.gitops  GitOps loop: policy repo -> TEST automatically, PRODUCTION drift report
lab/                   docker-compose lab: GitLab CE 17.10.5 + Jenkins + gp-ctl (lab/up.sh, lab/down.sh)
tests/gitlab/          end-to-end verification of all phases on the real lab GitLab
schema/                JSON Schema for editors/CI (Go validator is authoritative)
examples/              reference policies; scenarios/ = 10 graded scenarios (docs/fa/EXAMPLES-FA.md),
                       tutorial/ = the step files of docs/fa/POLICY-FA.md
testdata/policies/     invalid-policy fixtures with expected codes
tools/build.sh         docker-based vendor/test/build (no Go needed on the host)
```

## Build & test

```bash
tools/build.sh test     # gofmt + go vet + go test (in golang:1.24-alpine)
tests/integration/phase{4,5,6,7,8}.sh   # real git push tests (phase6 runs inside Git's quarantine)
tools/build.sh build    # dist/git-policy-<version>-linux-amd64 + .sha256
dist/git-policy-0.1.0-linux-amd64 validate examples/policy.example.yaml
```
