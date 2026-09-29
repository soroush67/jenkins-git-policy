# git-policy — Phase 1: Architecture Review & Design

Status: **PROPOSED — awaiting approval**
Software version target: `git-policy v0.1.0`
Policy schema: `apiVersion: git-policy/v1`
Date: 2026-09-29

---

## 1. Review of the requirements — risks and incorrect assumptions

The verified path `GitLab → Gitaly → global pre-receive.d` is kept as-is. The
points below are where the brief is either incomplete or would produce an
insecure or unusable system if implemented literally.

### R1. "Scan new commits" is ambiguous — the exclusion base matters (anti-bypass)

The naive way to find new commits is `git rev-list <new> --not --all`. It has
**two real bypasses**:

1. **Fork / merge-request bypass.** `--all` includes `refs/merge-requests/*`,
   `refs/keep-around/*`, `refs/pipelines/*`, `refs/environments/*`. GitLab
   fetches a fork's MR source branch into `refs/merge-requests/N/head` of the
   target repo **without running pre-receive**. When the MR is merged (merging
   *does* run pre-receive, `GL_PROTOCOL=web`), those commits are already
   "reachable from --all" and get skipped. Result: content pushed to a fork in
   a lax namespace lands in `finance/payment-api` unscanned.
   **Fix:** exclude only refs that went through policy: `--not --branches --tags`
   (i.e. `refs/heads/*` and `refs/tags/*`), never `--all`.

2. **Branch-scoped rule bypass.** If `release/*` blocks `.exe` but `develop`
   does not, then `git push origin develop:release/1.0` introduces *zero* new
   commits to the repository, and the stricter release rule sees nothing.
   **Fix:** the exclusion base for a ref update is the set of existing refs
   whose *effective content policy* is at least as strict as the target ref's
   ("policy class"). In the common case (no branch-scoped content rules) every
   ref is in one class, and this reduces to `--not --branches --tags`.
   Consequence: the first `release/*` branch in a repo scans the history from
   `develop` back to the last point already vetted under the release class.
   This is bounded by limits (R10) and is a **decision point** (see §12 Q4).

### R2. Extension checks cannot be purely object-based

`git rev-list --objects` lists only objects *new to the repo*. Renaming an
existing `readme.txt` blob to `evil.dll` creates a new tree but **not** a new
blob, so an object-only scan misses it. Blob objects also have no path of
their own.
**Fix:** extension rules are **path-based**. Run `git diff-tree` over every new
commit, including the root commit (`--root`) and merges (see §7). Size rules
are **blob-based**, using the new-side blob SHAs from the same `diff-tree --raw`
output, batched through one `git cat-file --batch-check`.

### R3. Extension blocking alone is trivially bypassed by renaming

`Mic.Caching.dll` → `Mic.Caching.dl_` or `.txt` defeats any extension list.
Filename-normalisation tricks matter too, because Windows developers are the
target population:

- trailing dots/spaces (`foo.dll.`, `foo.dll `) resolve to `foo.dll` on NTFS
- NTFS streams (`foo.dll::$DATA`)
- Unicode/case (`FOO.DLL`, full-width characters)

**Fix (v1):** normalise before matching: case-fold, strip trailing `.` and
spaces, and strip `::$DATA`/`:stream` suffixes.
**Optional (v1, off by default):** a `content_signatures: [pe]` rule that
reads the first bytes of each *new* blob and detects the PE/COFF header
(`MZ` + `PE\0\0`). That is the real `.dll/.exe` detector. It costs a content
read per new blob, so it is bounded by size and count limits.

### R4. `branches.main.direct_push: deny` conflicts with GitLab itself

Merging an MR, using the Web IDE, and uploading through the UI all run
pre-receive with `GL_PROTOCOL=web` and the merging user as `GL_USERNAME`. A
hook cannot reliably tell "MR merge" apart from "web commit directly to main".
The native mechanism is correct here and already available in CE: **Protected
Branches** (allowed to push = *No one*, allowed to merge = *Maintainers*,
force-push toggle) and **Protected Tags**.
**Recommendation:** do not implement `direct_push` in git-policy. Branch
patterns in git-policy scope **content and identity rules** only. §12 Q5 asks
you to confirm this.

### R5. Not every write path runs pre-receive

These paths bypass Gitaly custom hooks. That is inherent to GitLab and
**cannot** be fixed by any hook:

- project import (file export, GitHub import, and so on)
- pull mirroring (to be verified in the lab)
- repository restore from backup
- `refs/merge-requests/*` fetches from forks (handled by R1)

**Mitigation:** document this, and restrict import sources and mirroring in
*Admin → Settings*. git-policy is a push-path control, not a repository-content
guarantee.

### R6. Identity context has gaps that must be verified in the lab

Gitaly exposes these to server hooks: `GL_ID` (`user-N` / `key-N`),
`GL_USERNAME`, `GL_PROJECT_PATH`, `GL_REPOSITORY` (`project-N`, `wiki-N`,
`snippet-N`, `design-N`), and `GL_PROTOCOL` (`ssh`/`http`/`web`).

- These variables are trustworthy. Gitaly sets them from GitLab's internal API,
  and a client cannot inject them.
- **Push options** (`git push -o …` → `GIT_PUSH_OPTION_*`) *are* client-controlled.
  They must **never** influence a decision.
- Deploy keys, project/group access-token bot users, and CI job tokens show up
  as unusual identities. `GL_USERNAME` may be empty or a bot name. An empty
  identity is treated as the principal `@unknown`: identity *allow* rules never
  match it, and content rules still apply.
- Under hashed storage the repository path on disk is `@hashed/..`. The project
  is taken **only** from `GL_PROJECT_PATH`, never from `cwd`.
- Wikis and snippets also run hooks. The policy needs a per-repo-type switch.
  The default is that global mandatory rules apply to every repo type.
- GitLab paths and usernames are case-insensitive, so both are case-folded for
  matching.

### R7. Project path is a mutable key

Renaming or transferring `finance/payment-api` → `sandbox/payment-api` moves
the project out from under the finance rules. Only Owners/Maintainers can do
this, and GitLab audits it, so v1 keys rules by **path**. Keying by project ID
(`GL_REPOSITORY=project-N`) is a v2 option. It would be fed by the same
Jenkins sync that maintains the membership cache.

### R8. Existing repositories already contain DLLs

Turning on enforcement will break:

- modifications to existing DLLs (the edited blob is new)
- the first `release/*` branch under R1-2 semantics

Deleting a DLL must always be allowed, since removal is the goal.
**Recommendation:** an explicit **`mode: audit`** (log "would reject", allow
the push) at global, per-rule, and per-scope level. Roll out as:
audit → review the logs → enforce per namespace.

### R9. Git LFS

A `.dll` tracked by LFS arrives in Git as a ~130-byte pointer file named
`foo.dll`. The extension rule catches the name, which is correct for the Nexus
goal. The size rule never sees the real binary. LFS size is controlled
natively in GitLab. Allowing LFS pointers for specific extensions is a policy
decision (§12 Q6).

### R10. Synchronous hook = DoS surface

A push of 200k commits, or of 10k refs, would make evaluation unbounded.
Documented, configurable limits apply:

| Limit | Default | Exceeded → | Operational consequence |
|---|---|---|---|
| `max_ref_updates` | 1000 | reject `LIMIT_REF_UPDATES` | bulk tag imports need an exception |
| `max_new_commits` | 50 000 | reject `LIMIT_COMMITS` | initial push of a huge repo needs a migration exception |
| `max_new_blobs_inspected` | 500 000 | reject `LIMIT_OBJECTS` | same |
| `evaluation_timeout` | 45 s | reject `EVAL_TIMEOUT` | below Workhorse/HTTP timeouts, so the user sees a clean message |

Limit failures are **fail-closed**. A skipped scan would itself be a bypass.
Exceptions of type `limits` exist for migration accounts.

### R11. Output injection

File names are attacker-controlled. Printing them raw to `remote:` lets an
attacker inject ANSI escape sequences into developer terminals and forge
`GL-HOOK-ERR` lines. Every string that reaches stderr or the logs is escaped:
non-printables become `\xNN`, and names are truncated to a maximum length.
Git output is always read with `-z` so paths are never mangled by `core.quotePath`.

### R12. Jenkins "must access the server" means root, unless scoped

`docker exec` requires the docker group, which is root-equivalent on the host.
A Jenkins credential with plain SSH + docker access would make Jenkins a
root-on-GitLab-host backdoor. §9 describes a forced-command channel that
scopes this down.

### R13. The phase order has a dependency problem

Phases 6 (extensions) and 7 (size) depend on Phase 8 (object traversal).
**Suggested order:** 5 identity/scope → **8 traversal** → 6 extensions →
7 size. §12 Q7 asks you to confirm.

### R14. GitLab backup does not include `/var/opt/gitlab/git-policy`

`gitlab-backup` covers repositories and the database, not arbitrary
directories. The policy source of truth is therefore a **policy Git
repository**, plus `backup/` exports. See §10.

### Items to verify in the lab before Phase 3 (not assumed)

1. The exact env vars present in the hook: `GL_*`, `GIT_QUARANTINE_PATH`,
   `GIT_OBJECT_DIRECTORY`, `GIT_ALTERNATE_OBJECT_DIRECTORIES`, `PATH`, uid/gid.
2. Which `git` binary is on `PATH` inside the hook, and its version.
   Gitaly 17 uses bundled `gitaly-git-v2.x`.
3. That only executable files in `pre-receive.d` run, and that `*~` files are
   skipped.
4. `GL_USERNAME`/`GL_ID` for: a deploy key, a project access token, a CI job
   token, a web MR merge.
5. Whether pull mirroring runs pre-receive.
6. Uid/gid of the `git` user, and that `/var/opt/gitlab` is the persistent
   volume.

A one-shot, **non-blocking** environment-capture hook for this is given in §13.

---

## 2. Implementation language: Go

| Criterion | Go (static binary) | Python | Bash |
|---|---|---|---|
| Runtime dependency inside `gitlab/gitlab-ce` | none (CGO off, static) | image has no guaranteed system python3; omnibus's embedded interpreter is GitLab-internal and changes across upgrades; PyYAML not guaranteed | available |
| Survives GitLab image upgrade | yes — binary lives on the persistent volume | risky | yes |
| Strict YAML (unknown keys, duplicate keys) | `yaml.v3` `KnownFields(true)`, duplicate keys rejected natively | needs PyYAML plus a custom loader | no |
| Startup cost per push | ~2–5 ms | ~40–80 ms | ~1 ms, but many forks |
| Complex precedence logic, testability | strong typing, `go test`, table tests, race detector | good | poor |
| Safe subprocess handling | `exec.Command` with argv, no shell | good | quoting-hazard prone |
| Concurrency primitives (timeouts, flock) | `context`, `syscall.Flock` | ok | weak |

**Decision: Go**, as a single static binary with subcommands:

- `hook` (runtime)
- `validate`, `status`, `version`
- `admin enable|disable|apply|rollback|apply-membership` (root only)
- `sync-membership` (runs on the Jenkins agent)

**Git access: shell out to the `git` binary, not go-git.** go-git does not
honour Gitaly's quarantine environment (`GIT_OBJECT_DIRECTORY`,
`GIT_ALTERNATE_OBJECT_DIRECTORIES`), object pools, or commit-graph/bitmap
acceleration. The engine inherits the environment untouched and never sets or
overrides those variables. Only `LC_ALL=C`, `GIT_TERMINAL_PROMPT=0` and
`GIT_CONFIG_NOSYSTEM`-style hardening are added.

**Build:**

- `go mod vendor`, so builds work offline and in air-gapped setups
- built inside a `golang` container, so no Go toolchain is needed on hosts
- reproducible: `-trimpath`, version injected with `-ldflags`

The wrapper hook stays in POSIX `sh`.

---

## 3. Trust boundaries

```
 UNTRUSTED                         TRUSTED-BY-GITLAB             TRUSTED (root-controlled)
 ──────────                        ─────────────────             ─────────────────────────
 developer / client                Gitaly-set env:               /var/opt/gitlab/git-policy/
  • all repo content (blobs,        GL_USERNAME, GL_ID,            bin/  policies/  state/
    trees, paths, commit msgs)      GL_PROJECT_PATH,               membership/ (root:git, 0750/0640)
  • ref names                       GL_REPOSITORY, GL_PROTOCOL   pre-receive.d/50-git-policy
  • push options (-o)              quarantine env vars
  • object counts / sizes          stdin ref list format
                                    (values untrusted)

 SEMI-TRUSTED                      CONTROL PLANE
 ────────────                      ─────────────
 membership cache                  Jenkins (validates, approves, ships)
  (as fresh as last sync;          GitLab API token (Jenkins only)
   can only restrict when stale)   policy Git repo (MR-reviewed)
```

Rules that follow from this:

- Repository content is only ever *read* through git plumbing. It is never
  executed, sourced, checked out, or used as configuration. There is no
  `.git-policy.yml` in repositories.
- Ref names and paths are validated against a strict grammar before use in any
  argv. Git refs are always passed after `--end-of-options`/`--`, so a ref
  like `--output=/x` is never parsed as an option.
- The hook process runs as the `git` user. It can **read** policy and state,
  and can **append** only to its own audit log. It cannot modify policy, state,
  or membership.
- Admin mutations require euid 0 inside the container. The binary checks this,
  and filesystem permissions enforce it independently.
- The GitLab API token never touches the GitLab server's filesystem. It lives
  only in Jenkins credentials and is passed by environment to `sync-membership`
  on the Jenkins agent.

---

## 4. Policy model and precedence

### 4.1 Rule classes

| Class | Who may relax it | Examples |
|---|---|---|
| **mandatory** (global only) | nothing except an explicit exception naming its rule ID | `.dll/.exe` deny, 50 MB hard cap, terminated-user deny |
| **defaults / scoped** (global → namespace → project → ref) | a more specific scope, within the mandatory bounds | namespace adds `.pdb`; project lowers the size to 10 MB |
| **exceptions** | n/a; they *are* the relaxation | `svc-migration` may bypass `LIMIT_COMMITS` on `legacy/*` until a set date |

### 4.2 Identity decision (push allowed at all?)

Evaluated before content, and cheap:

1. **Mandatory identity denies** (global `mandatory.deny_users`, `mandatory.deny_groups`)
   → REJECT, unless an exception names that rule ID.
2. **Scoped identity rules.** Collect every rule matching
   (user or any of the user's cached groups) × (scope matching project/ref).
   Order them by **scope specificity**:
   ref-in-project > project > deepest namespace > … > top namespace > global.
   The **first scope level with any matching rule decides**. Within that level,
   **user beats group**, and on a tie **deny beats allow**.
3. No rule matched → ALLOW. git-policy is additive; GitLab authorisation
   already happened before the hook ran.

This supports the brief's examples:

- "alex denied everywhere": global user rule.
- "alex denied on finance/payment-api only": project-level user rule.
- "alex allowed A, denied B": two project rules.

It also keeps "terminated employee" style denies unbreakable by putting them
in `mandatory`.

### 4.3 Content decision (what may this push contain)

The effective rule set for (project, ref) is computed as follows:

- **Blocked extensions**: `mandatory ∪ global ∪ ns(top…deepest) ∪ project ∪ ref-patterns`.
  A more specific scope may remove a *non-mandatory* entry, but only with an
  explicit `unblock_extensions`. There is no implicit "replace list" semantics.
  Union is the safe default.
- **Max blob size**: the most specific configured value wins, then it is capped
  at `mandatory.max_file_size`. A project can raise its namespace's 5 MB to
  20 MB, but never above the mandatory 50 MB.
- **Multiple matching ref patterns** (e.g. `release/*` and `*`): all of them
  apply. Lists are unioned and the size is the minimum. The result is
  deterministic and does not depend on pattern order.
- **Namespace matching** is per path component, so `finance` matches
  `finance/x` and `finance/a/b`, but never `financeold/x`.
- **Mode**: each rule is `enforce` (default) or `audit`. `audit` records a
  `WOULD_REJECT` and does not block. A mandatory rule's mode can only be set
  globally.

Each **violation** is then checked against exceptions (§4.4). Any violation
not waived → REJECT, and the message lists up to N violations.

### 4.4 Exceptions

Exceptions are first-class objects, not flags scattered through rules:

```yaml
exceptions:
  - id: EXC-2026-014              # required, unique
    rules: [BLOCKED_EXTENSION]    # required; rule IDs, "*" rejected by validator
    subjects: {users: [svc-legacy-build]}   # users and/or groups
    scope:  {projects: [finance/legacy-erp], refs: ["refs/heads/main"], paths: ["vendor/*.dll"]}
    reason: "Legacy ERP vendored SDK until NuGet migration"
    ticket: "INFRA-2231"
    expires: 2026-12-31           # required; validator rejects > max_exception_days
```

- The validator enforces the required fields, rejects `rules: ["*"]` and
  `bypass_all`, and caps the lifetime.
- An expired exception is ignored at runtime. Nothing needs to clean it up.
- **Group-subject exceptions are honoured only when the membership cache is
  fresh** (§5).
- Every push where an exception waived a violation logs an `EXCEPTION_APPLIED`
  audit event, even though the push was accepted.

### 4.5 Full evaluation order

```
0  wrapper: break-glass file present?          → exit 0 (logged by wrapper)
1  engine state disabled (and not expired)?    → exit 0
2  load ACTIVE policy (immutable version dir)  → fail → see §6
3  parse stdin ref updates; enforce ref limit
4  identity: mandatory denies → scoped rules   → REJECT fast (no object scan)
5  classify each ref update: create/update/force/delete, branch/tag/other
6  deletes: identity only (no content)         (+ optional deny_delete on tags)
7  traversal (§7): new commits per policy class → diff-tree paths → cat-file sizes
8  content rules → violations → exceptions
9  audit (REJECT / WOULD_REJECT / EXCEPTION_APPLIED), message, exit code
```

---

## 5. Group membership cache

```
Jenkins (cron H/15) ── git-policy sync-membership ──> GitLab API (read_api token, Jenkins-held)
        │   builds membership.json, sanity-checks
        ▼
  forced-command channel ──> git-policy admin apply-membership (root, in container)
        │   validate → write tmp → fsync → rename previous → rename current → fsync dir
        ▼
  /var/opt/gitlab/git-policy/membership/current.json   (root:git 0640)
        ▲
  hook: single read + JSON decode, local lookup only (no network, ever)
```

- **Content.** `{schema, generated_at, source_url_hash, users: {username: [group_full_path, …]}}`.
  It includes inherited memberships (`/groups/:id/members/all`), and blocked
  users are listed as blocked. JSON is used rather than YAML because the file
  is machine-generated, faster to parse, and unambiguous.
- **Scope of the sync.** Only groups referenced in the active policy are
  synced, so the file stays small and the API call count stays bounded.
- **Sanity guard.** If the member count drops by more than 30% compared with
  the previous cache, or the sync returns zero groups, the sync refuses to
  apply unless `FORCE=true` (with approval in production). This prevents an
  API outage or permission change from wiping memberships.
- **Freshness**: `soft_max_age` (default 2 h) and `hard_max_age` (default 24 h).

| Cache state | Group **deny** rules | Group **allow / exception** grants | Signal |
|---|---|---|---|
| fresh (< soft) | applied | applied | — |
| stale (soft–hard) | applied | applied | `status` warning, `membership_stale` in audit |
| expired (> hard) | **applied** (last known) | **ignored** | warning on push stderr |
| missing / corrupt, previous OK | fall back to `previous.json` under the same rules | | |
| missing and no previous | **reject if the policy has any group-deny rule** (`MEMBERSHIP_UNAVAILABLE`); otherwise continue without groups | ignored | |

The principle is that **a stale cache may only restrict, never grant.** The
cost: someone removed from `contractors` stays denied until the next sync.
That is the correct failure direction, and it is documented.

---

## 6. Failure model (fail-open vs fail-closed)

| Failure | Behaviour | Rationale |
|---|---|---|
| Jenkins down | **no effect** | enforcement is fully local |
| GitLab API down | **no effect** on pushes; the cache ages per §5 | push path has no network dependency |
| Nexus down | **no effect** | Nexus appears only as text in messages |
| `state` file missing | **enabled** (closed) | deleting a file must not turn security off |
| `state` unreadable / garbled | **closed** | same |
| disable with TTL expired | re-enabled automatically | a forgotten disable cannot become permanent |
| `ACTIVE` pointer / active version missing or corrupt | fall back to the **previous** version dir, loudly | versions are immutable and validated at apply time |
| no loadable policy at all | **closed** (`POLICY_UNAVAILABLE`), with the emergency procedure in the message | cannot happen under atomic deploy, so it indicates tampering or disk corruption |
| engine panic / crash / binary missing | **closed** (non-zero exit → Git rejects) | the break-glass file in the wrapper does not depend on the binary |
| permission error on policy | **closed** | |
| evaluation timeout / limits | **closed** | an unscanned push is a bypass |
| `git` plumbing error (corrupt object) | **closed** | |
| audit log unwritable (disk full, perms) | **decision unchanged**; warning on stderr; `status` flags it. Configurable `audit.required: true` → closed | a full disk already breaks Git's quarantine; making availability depend on logging is usually worse. High-security orgs can flip it |
| membership cache problems | see §5 | restrict-only |
| unknown identity (`GL_USERNAME` empty) | principal `@unknown`: content rules apply, identity allows never match | |
| unknown repo type | global mandatory rules apply | |

**Emergency bypass** is only possible with root in the container, and every
path to it leaves a trace:

1. **Normal path:** `git-policy admin disable --reason … --ttl 2h`.
   This writes an audit event, and the TTL makes it auto-expire.
2. **Engine broken:** `touch /var/opt/gitlab/git-policy/state/break-glass`.
   `state/` is root-only writable. The wrapper appends a line to
   `logs/break-glass.log` on every push while the file exists, and `status`
   plus the Jenkins STATUS job flag it in red.

---

## 7. Traversal algorithm (anti-bypass core) and performance

Per push, the engine reads stdin `old new ref` lines, validates them
(40/64-hex SHAs, ref grammar), and classifies each update:

| Case | Detection | Content scan |
|---|---|---|
| create | old = zero-OID | yes, relative to the exclusion base |
| update (fast-forward) | `merge-base --is-ancestor old new` | yes |
| force push | not an ancestor | yes; commits only reachable from old are irrelevant |
| delete | new = zero-OID | no content; identity rules still apply |
| tag → commit | `cat-file -t` peels to a commit | yes |
| tag → tree / blob (legal in Git) | peel type is tree/blob | tree: `ls-tree -r -z` (all paths new); blob: size check only |
| non-branch/tag refs (`refs/notes/*`, custom) | prefix | treated as the "other" class; global rules apply |
| SHA-256 repos | OID length 64 | zero-OID detected by length-agnostic all-zero check |

**Pipeline.** A constant number of processes per push, independent of commit
count:

```
1. git rev-list --stdin  (tips = new SHAs of one policy class;
                          --not = existing refs/heads+refs/tags of that class)
   → list of new commits (and count limit)
2. git diff-tree --stdin -r -z --raw --root --no-renames -c
   → per commit: (new-blob-SHA, mode, status, path) for every added/modified path
      non-merge: diff vs parent (root commit: vs empty tree)
      merge:     combined diff (-c) = paths differing from ALL parents
                 (paths inherited unchanged from any parent were already
                  vetted as part of that parent's commits)
   → extension check on every path (A/M/T/C; D ignored; gitlinks 160000 ignored)
3. git cat-file --batch-check='%(objectname) %(objecttype) %(objectsize)'
   ← deduplicated new-side blob SHAs
   → size check
   (4. optional PE signature: git cat-file --batch on candidate blobs, read first 4 KiB only)
```

- The "commit A adds `malicious.dll`, commit B deletes it" case is caught: A is
  in the new-commit set, and A's diff contains `A malicious.dll`.
- `--no-renames` is deliberate. A rename appears as delete + add, so the new
  name is always checked.

**Performance characteristics:**

- `rev-list` cost is O(new commits + boundary walk). Gitaly housekeeping keeps
  commit-graphs and bitmaps, so a normal push is milliseconds even in repos
  with 100k+ commits.
- Pushing an existing commit to a new branch in the same policy class costs
  **zero** diff work.
- `diff-tree` cost is O(Σ changed paths in new commits), streamed in one
  process. It never walks full trees except for root commits and tag→tree.
- `cat-file --batch-check` does one object-header lookup per unique new blob,
  with no content read.
- Expected: a typical push (< 100 commits) completes in < 100 ms, dominated by
  about 3 process spawns. The pathological cases (initial import of a huge
  repo) are linear and are bounded by the limits in R10.
- There are no global locks on the push path. Readers take no locks at all
  (§8).

Quarantine note: Gitaly runs the hook with `GIT_OBJECT_DIRECTORY` pointing at
the quarantine and `GIT_ALTERNATE_OBJECT_DIRECTORIES` at the real object store.
Child `git` processes inherit these unchanged, so they see both the new
objects and the existing ones. The engine never writes objects or refs.

---

## 8. Persistent filesystem layout

Replacing the brief's `config/` + `rules/<category>` layout: **a set of
separate rule files cannot be swapped atomically as a unit.** A reader could
see new extension rules paired with old project rules. Instead, one policy
document is validated and compiled into an **immutable version directory**,
and a single pointer file is atomically renamed.

```
/var/opt/gitlab/git-policy/                 root:git  0750   (on the persistent /var/opt/gitlab volume)
├── bin/
│   ├── git-policy                          root:root 0755   static binary (replaced via rename)
│   └── git-policy.previous                 root:root 0755   last binary, for rollback
├── hooks/
│   └── pre-receive.sh                      root:root 0644   canonical wrapper source (installer copies it)
├── policies/                               root:git  0750
│   ├── ACTIVE                              root:git  0640   contains e.g. "000012"  (replaced by rename)
│   ├── 000011/                             root:git  0550   immutable
│   │   ├── policy.yaml                     0440   exact source as approved
│   │   ├── compiled.json                   0440   normalised, what the hook loads (no YAML at push time)
│   │   └── meta.json                       0440   version, sha256, deployed_by, jenkins build, timestamp
│   └── 000012/ …
├── membership/                             root:git  0750
│   ├── current.json                        root:git  0640
│   └── previous.json                       root:git  0640
├── state/                                  root:git  0750
│   ├── engine.json                         root:git  0640   {enabled, changed_by, reason, changed_at, expires_at}
│   ├── break-glass                         (absent normally; root-only creatable)
│   └── admin.lock                          root      0600   flock for admin ops only
├── logs/                                   git:git   0750   (hook runs as git and must append)
│   ├── audit-2026-09-29.jsonl              git:git   0640   one file per UTC day
│   └── break-glass.log
├── backup/                                 root:root 0700   tar exports (policies + membership + state)
└── tmp/                                    root:root 0700   staging, same filesystem → rename(2) is atomic
```

Why a pointer file rather than a symlink: there is no symlink to follow at
all. The engine reads `ACTIVE`, validates that it matches `^[0-9]{6}$`, and
opens `policies/<n>/compiled.json` with `O_NOFOLLOW`. Version dirs are never
modified, so a reader holding an old version finishes consistently.

**Installed hook** (the only file placed in Gitaly's directory):

```
/var/opt/gitlab/gitaly/custom_hooks/pre-receive.d/50-git-policy   root:root 0755
```

The existing `01-block-dll` PoC is kept **untouched** until git-policy is
validated in enforce mode. It is then moved to `backup/` (not deleted). Moving
it out of the directory is required, because every executable in
`pre-receive.d` runs.

**Audit log concurrency and rotation.** Each event is one `write(2)` of one
JSON line, with `O_APPEND` plus a sub-millisecond `flock` on that day's file.
Daily file names mean **no rotation race**: nothing ever renames a file that a
writer holds. Retention (default 180 days) is pruned by the Jenkins STATUS
job, or by `git-policy admin prune-logs`. Tamper resistance is limited because
the `git` user can write the logs. The mitigation is that Jenkins ships a copy
of each closed day's file off-host, and SIEM forwarding is documented.

---

## 9. Jenkins control plane

### 9.1 Access channel (scoped, no docker-group for Jenkins)

```
Jenkins agent ──ssh (key: git-policy-ssh-<env>)──> GitLab docker host
    authorized_keys: command="sudo -n /usr/local/sbin/git-policy-ctl",no-pty,no-port-forwarding,no-agent-forwarding,no-X11-forwarding ssh-ed25519 …
        │
        ▼
/usr/local/sbin/git-policy-ctl  (root:root 0755)
    • parses $SSH_ORIGINAL_COMMAND against a fixed verb allowlist:
      status | validate | apply | rollback | enable | disable | apply-membership | backup | export-logs
    • strict argument grammar (no free-form strings except a sanitised --reason)
    • policy/membership payloads arrive on STDIN (size-capped), never as file paths
    • runs: docker exec -i -u root gitlab /var/opt/gitlab/git-policy/bin/git-policy admin <verb> …
    • sudoers: gitpolicy-deploy ALL=(root) NOPASSWD: /usr/local/sbin/git-policy-ctl
```

The Jenkins credential can therefore do exactly these nine things and nothing
else on the host. It cannot touch repositories.

### 9.2 Policy as code

The policy source lives in a dedicated GitLab repository (e.g.
`platform/git-policy-config`, protected `main`, MR approvals). Jenkins
**reads** it; it never writes to any repository. Validation uses **the same
binary** (`git-policy validate`) on the agent and again server-side during
`apply`. The two checks are identical by construction.

### 9.3 Pipeline actions

| ACTION | TEST | PRODUCTION | Server op |
|---|---|---|---|
| DEPLOY (install/upgrade binary + wrapper) | auto | approval | `install.sh` run over the same channel (verb `deploy`, binary on stdin, checksum-verified) |
| VALIDATE_POLICY | auto | auto | local only |
| UPDATE_POLICY | auto | **approval + diff shown** | `apply` |
| ROLLBACK_POLICY | auto | **approval** | `rollback [--to N]` |
| ENABLE | auto | auto | `enable` |
| DISABLE | auto | **approval, reason required, TTL required** | `disable` |
| STATUS | auto | auto | `status --json` |
| BACKUP_POLICY | auto | auto | `backup` → archived as a Jenkins artifact |
| SYNC_GROUP_MEMBERSHIP | cron | cron | `sync-membership` on the agent → `apply-membership` |

**Approvals** use `input` with `submitter: 'git-policy-approvers'`, a timeout,
and a four-eyes check (approver ≠ build trigger user).

**Credentials** (IDs only, bound with `withCredentials`/`sshagent`, never echoed):

- `gitlab-api-readonly` (secret text, admin `read_api`)
- `git-policy-ssh-test`, `git-policy-ssh-prod`

Every write op's post-condition compares `status` output (policy checksum or
state) against the expected value, and the build fails otherwise.

`apply` flow (server side, under `admin.lock`):

1. read stdin
2. validate
3. compile
4. write `tmp/<n>/`
5. fsync
6. `rename(tmp/<n>, policies/<n>)`
7. write `tmp/ACTIVE`, fsync
8. `rename(tmp/ACTIVE, policies/ACTIVE)`
9. fsync the dir
10. audit `POLICY_UPDATED`

An invalid policy stops at step 2, so the active version is never touched.
Rollback is steps 7–10 with an older `<n>`.

---

## 10. Backup and disaster recovery (outline; full runbook in Phase 15)

- **Source of truth:** the policy repo. Membership is re-derivable with one
  sync. State is a single tiny file.
- `backup/` tarballs are produced by the BACKUP_POLICY job and archived in Jenkins.
- **Total loss of `/var/opt/gitlab/git-policy`:** the wrapper's `exec` fails,
  so pushes are **rejected**. Recovery is DEPLOY, then UPDATE_POLICY from the
  repo, then SYNC. The fast path is break-glass on the host.
- **Hook corrupted:** reinstall with `install.sh`, which is idempotent and
  compares the checksum of the installed wrapper.

---

## 11. Final architecture

```
                          ┌───────────────────────── CONTROL PLANE ─────────────────────────┐
                          │  Policy repo (MR-reviewed)      Jenkins                          │
                          │  platform/git-policy-config ──> validate (same binary)           │
                          │                                 diff / approval (prod)           │
                          │  GitLab API ──(read_api)──────> sync-membership (cron)           │
                          └───────────────────────────────────┬──────────────────────────────┘
                                                              │ ssh forced-command
                                                              │ → sudo git-policy-ctl (verb allowlist)
                                                              │ → docker exec … git-policy admin <verb>
                                                              ▼
 Developer ── git push ──> GitLab (authn/authz, protected branches) ──> Gitaly
                                                                         │ pre-receive (quarantine env)
                                                                         ▼
                           custom_hooks/pre-receive.d/50-git-policy   (sh, ~10 lines)
                                   │ break-glass? → exit 0 + log
                                   ▼ exec
                ┌─────────── git-policy hook  (Go, static, runs as git) ───────────┐
                │ state/engine.json ──> enabled?                                   │
                │ policies/ACTIVE ──> 0000NN/compiled.json   (immutable, no locks) │
                │ membership/current.json (restrict-only when stale)               │
                │                                                                   │
                │ ① IDENTITY   user · groups(cache) · protocol · @unknown           │
                │ ② SCOPE      global → namespace chain → project → ref pattern     │
                │ ③ CONTENT    rev-list(policy class) → diff-tree -c → cat-file     │
                │              extension(normalised) · size · [PE signature]        │
                │ ④ EXCEPTIONS explicit, rule-scoped, expiring                      │
                │ ⑤ LIMITS     refs · commits · blobs · timeout  (fail-closed)      │
                └───────────────┬───────────────────────────────┬──────────────────┘
                                ▼                               ▼
                             ALLOW                    REJECT (GL-HOOK-ERR, escaped,
                          (exit 0; audit if            rule ID + remediation → Nexus/NuGet)
                           exception/would-reject)              │
                                                                ▼
                                      logs/audit-YYYY-MM-DD.jsonl  (O_APPEND + flock)

   Nexus: artifact store, referenced only in message text (no runtime dependency)
   GitLab: source of truth for code, auth, protected branches, MR approvals
```

---

## 12. Decisions needed from you before Phase 2

1. **Language:** Go static binary as described in §2. Approve?
2. **Jenkins → server channel:** SSH forced-command + sudo-restricted
   `git-policy-ctl` wrapper, so Jenkins does not get the docker group (§9.1).
   Approve? Also: where do Jenkins and the Jenkins agent run relative to
   192.168.120.128?
3. **Policy source:** a dedicated policy Git repo in GitLab (recommended), or a
   policy file kept inside this project/Jenkins job?
4. **Branch-scoped rule semantics (R1-2):** use "policy class" exclusion
   (secure; the first `release/*` branch may scan older history), or
   "new-to-repository only" (cheaper; allows the develop→release bypass)?
5. **`direct_push`:** drop it from git-policy and use GitLab Protected
   Branches (recommended, R4)?
6. **LFS pointers** named `*.dll`: block (current proposal) or allow per rule?
7. **Phase order:** move traversal (Phase 8) before extensions/size (R13)?
8. **Rollout:** start production in global `mode: audit` for a period before
   enforce (R8)?
9. **Lab access:** 192.168.120.128 is not reachable from this workstation
   (WSL: "No route to host"). Integration tests will need you to run
   commands on the lab, unless you can provide a route or SSH access.

---

## 13. Optional lab verification (safe, non-blocking)

This answers the open items at the end of §1 without affecting pushes: it
always exits 0. Run it in the lab, push once over SSH, once over HTTP, and do
one Web-UI commit, then remove it.

```bash
docker exec -u root gitlab sh -c 'cat > /var/opt/gitlab/gitaly/custom_hooks/pre-receive.d/00-envprobe <<'"'"'EOF'"'"'
#!/bin/sh
{
  echo "=== $(date -Iseconds) uid=$(id -u) gid=$(id -g) pwd=$(pwd)"
  env | grep -E "^(GL_|GIT_|PATH=)" | sort
  echo "git=$(command -v git) $(git --version 2>&1)"
  cat   # the stdin ref lines: old new ref
} >> /tmp/git-policy-envprobe.log 2>&1
exit 0
EOF
chmod 0755 /var/opt/gitlab/gitaly/custom_hooks/pre-receive.d/00-envprobe'

# after the test pushes:
docker exec gitlab cat /tmp/git-policy-envprobe.log
docker exec -u root gitlab rm /var/opt/gitlab/gitaly/custom_hooks/pre-receive.d/00-envprobe
```

One side effect: `01-block-dll` still runs after the probe, so DLL pushes
remain blocked during this test.
