# git-policy — Phase 2: Policy Schema & Precedence

Status: **PROPOSED — awaiting approval**
Schema: `apiVersion: git-policy/v1`, `kind: GitPolicy`
Machine-readable schema: [`schema/policy.v1.schema.json`](../../schema/policy.v1.schema.json)
Reference example: [`examples/policy.example.yaml`](../../examples/policy.example.yaml)

## 0. Decisions carried over from Phase 1

These are the recommended answers to the Phase 1 §12 questions, accepted as the
defaults:

| # | Decision |
|---|---|
| Q1 | Engine is a static Go binary. |
| Q2 | Jenkins → server over SSH forced-command + a sudo-restricted `git-policy-ctl` wrapper. |
| Q3 | Policy source lives in a dedicated GitLab repo (e.g. `platform/git-policy-config`). |
| Q4 | Branch-scoped content rules use **policy-class** exclusion (secure). |
| Q5 | No `direct_push` rule. Use GitLab Protected Branches / Protected Tags for that. |
| Q6 | LFS pointer files are checked by name like any other path, so `foo.dll` is blocked even as a pointer. |
| Q7 | Revised phase order: 5 identity/scope → 6 **traversal** → 7 extensions/paths → 8 size/signature. The rest is unchanged. |
| Q8 | Production rollout starts with `mandatory.mode: audit` and `settings.mode: audit`. |
| Q9 | Integration tests against the lab are run by you; I will provide the commands. |

---

## 1. Document structure

```yaml
apiVersion: git-policy/v1        # schema version (≠ software version v0.1.0)
kind: GitPolicy
metadata:   {name, revision, description}
settings:   {mode, repository_types, limits, membership, audit, exceptions, messages}
mandatory:  {mode, blocked_*, max_file_size, deny_users, deny_groups}
defaults:   {blocked_*, max_file_size, refs}
namespaces: {<group/subgroup path>: Scope}
projects:   {<full project path>: ProjectScope}
users:      {<username>: IdentityRule}
groups:     {<group full path>: IdentityRule}
exceptions: [Exception, ...]
```

Only `apiVersion`, `kind`, and `metadata` are required. The smallest valid
policy is [`examples/policy.minimal.yaml`](../../examples/policy.minimal.yaml).

### 1.1 General parsing rules

These apply everywhere in the document:

- **Unknown keys → error**, at every level. This catches typos: a misspelled
  `blocked_extentions` must fail validation, not be silently ignored.
- **Duplicate YAML keys → error.** The Go decoder rejects them. PyYAML
  accepts them silently, which is one reason the JSON Schema is only an
  editor aid.
- **Keys are case-insensitive** for usernames, namespaces, and projects.
  After case-folding, two keys that collide (e.g. `Finance` and `finance`)
  are an error.
- **Extensions are case-insensitive.** A leading dot is tolerated (`.dll` is
  the same as `dll`), and the validator emits a warning.
- **Sizes are strings with binary units:** `B`, `KiB`, `MiB`, `GiB`
  (e.g. `20MiB`). The brief's `max_file_size_mb` is not used. `MB` is
  rejected, so there is no ambiguity between 1000 and 1024.
- **Durations** look like `45s`, `30m`, `2h`.
- **Dates** use the form `YYYY-MM-DD` and are interpreted as end of day in
  UTC.

### 1.2 Pattern languages

| Where | Syntax | Case | Notes |
|---|---|---|---|
| namespace keys | exact path, component-wise prefix match | insensitive | `finance` matches `finance/a` and `finance/a/b`, but **not** `financeold/a` |
| project keys | exact full path, at least 2 components | insensitive | |
| `refs` keys, exception `refs` | full ref glob, must start with `refs/` | sensitive (Git refs are) | `*` stays within one path component, `**` spans components, `?` is one character. `refs/heads/release/*` ≠ `refs/heads/release/1.0/hotfix`, so use `release/**` for that |
| `blocked_paths`, exception `paths` | repo-relative path glob, no leading `/` | **insensitive** (Windows clients) | same `*`, `**`, `?` rules; `**/obj/**` matches any `obj` directory |
| extensions | matched against the **last** extension *and* compound extensions | insensitive | `tar.gz` matches `a.tar.gz`; `dll` matches `x.DLL` |

### 1.3 Path normalisation before content matching

Git paths are stored as bytes, and the engine inspects them as such. Before
any extension or path rule is matched, each path is normalised:

1. Take the basename.
2. Strip trailing `.` and space characters. On NTFS, `foo.dll.` and `foo.dll `
   both resolve to `foo.dll`.
3. Strip an NTFS stream suffix (`foo.dll::$DATA` → `foo.dll`,
   `foo.dll:x` → `foo.dll`).
4. Case-fold.

Invalid UTF-8 is matched byte-wise and reported as escaped `\xNN`.

---

## 2. Sections

### 2.1 `metadata`

| Key | Type | Required | Notes |
|---|---|---|---|
| `name` | `[a-z0-9-]` | yes | |
| `revision` | int ≥ 1 | yes | `apply` refuses a revision ≤ the active revision, unless the operation is `rollback`, which re-activates an existing stored version. This stops an old file being deployed by mistake. |
| `description` | string | no | |

### 2.2 `settings`

All keys are optional. The defaults are shown below.

```yaml
settings:
  mode: enforce                 # default mode for NON-mandatory rules: enforce | audit
  repository_types:             # all | mandatory_only | none
    project: all
    wiki: mandatory_only
    snippet: mandatory_only
    design: none                # design repos are LFS image uploads
  limits:
    max_ref_updates: 1000
    max_new_commits: 50000
    max_new_blobs: 500000
    evaluation_timeout: 45s
  membership:
    soft_max_age: 2h
    hard_max_age: 24h
    on_unavailable: deny_if_group_rules   # | ignore_groups
  audit:
    required: false             # true → audit write failure rejects the push
    retention_days: 180
  exceptions:
    max_lifetime_days: 180      # validator rejects exceptions expiring further out
  messages:
    header: "Push rejected by organizational Git policy."
    support: ""                 # e.g. "Help: #devops-help"
    remediation: {<RULE_CODE>: "<text>"}   # built-in defaults exist for every code
```

Messages are policy text. They must not contain control characters, and each
is limited to 1000 characters. **Do not put secrets in messages.** They are
shown to every pusher.

### 2.3 `mandatory` (global only — the security floor)

```yaml
mandatory:
  mode: enforce                 # enforce | audit — ONLY place mandatory can be softened
  blocked_extensions: [dll, exe]
  blocked_paths: []
  blocked_signatures: []        # v1 supports: pe
  max_file_size: 50MiB          # hard cap; no scope may configure a larger value
  deny_users: []                # also accepts "@unknown" (empty GL_USERNAME)
  deny_groups: []
```

- Nothing in `defaults`, `namespaces`, `projects`, or `refs` can remove or
  raise these values. Any attempt is a **validation error**, not a warning.
- The only way through is an `exception` with `mandatory: true`.
- `mandatory.mode` applies to mandatory content rules only. Mandatory identity
  denies are always enforced (§3.1).

### 2.4 Content scopes: `defaults`, `namespaces`, `projects`, `refs`

| Key | defaults | namespace | project | ref (inside any of these) |
|---|---|---|---|---|
| `blocked_extensions` / `blocked_paths` / `blocked_signatures` | ✓ | ✓ | ✓ | ✓ |
| `unblock_extensions` / `unblock_paths` / `unblock_signatures` | — | ✓ | ✓ | ✓ |
| `max_file_size` | ✓ | ✓ | ✓ | ✓ |
| `mode` | — (use `settings.mode`) | ✓ | ✓ | ✓ |
| `refs: {<ref-glob>: …}` | ✓ | ✓ | ✓ | — (no nesting) |
| `enabled` | — | — | ✓ | — |

`enabled: false` on a project means **only mandatory content rules apply** to
it. Identity rules still apply. This is the escape hatch for sandbox projects.
It cannot switch off the security floor.

### 2.5 Identity: `users` and `groups`

Both sections use the same shape:

```yaml
users:
  alex:
    push: deny                        # global scope for this subject
    refs: {"refs/heads/main": deny}   # global scope, ref-bound
    namespaces:
      finance: {push: deny, refs: {"refs/heads/release/**": deny}}
    projects:
      finance/reporting: {push: allow}
groups:
  contractors:                        # full group path, as in the membership cache
    push: deny
    namespaces:
      outsourcing: {push: allow}
```

Rules:

- `push` means **any ref update**: create, update, force-push, or delete.
- Deliberately absent keys: there is no `bypass_content_policy`, `bypass_all`,
  or `direct_push`. Using any of them is an error, and the error message
  points to exceptions or Protected Branches.
- Reserved subject `@unknown` matches pushes with an empty `GL_USERNAME`.
  Further reserved subjects, such as `@deploy-key`, will be added only after
  the Phase-1 lab verification tells us what GitLab sends for deploy keys and
  tokens.

### 2.6 `exceptions`

```yaml
exceptions:
  - id: EXC-2026-001              # required, unique, ^[A-Z][A-Z0-9-]{2,63}$
    rules: [BLOCKED_EXTENSION]    # required; from the waivable list (§4); no "*"
    mandatory: false              # must be true to waive a MANDATORY rule
    subjects:                     # users and/or groups; omitted = any pusher
      users: [svc-legacy-build]
      groups: [devops]
    scope:                        # all given dimensions must match (AND); lists are OR
      namespaces: [finance]
      projects: [finance/legacy-erp]
      refs: ["refs/heads/main"]
      paths: ["vendor/VendorSdk/*.dll"]
    max_file_size: 200MiB         # only with FILE_TOO_LARGE: waive up to this size, not unlimited
    reason: "…"                   # required, ≥ 10 chars
    ticket: "INFRA-2231"          # optional
    approved_by: "security-team"  # optional
    expires: 2026-12-31           # required
```

Validator constraints (semantic, beyond the JSON Schema):

| Constraint | Result |
|---|---|
| no `subjects` **and** no `scope.namespaces` / `scope.projects` | error: a global "anyone, anywhere" exception is not allowed |
| `expires` further out than `max_lifetime_days` | error |
| `expires` in the past | warning; the exception is inactive at runtime |
| `scope.paths` combined with a rule that has no path (identity / limit rules) | error |
| `max_file_size` without `FILE_TOO_LARGE` | error |
| waiving a mandatory rule without `mandatory: true` | error |

**Group subjects** are honoured only while the membership cache is at most
`hard_max_age` old. **Every use** of an exception emits an
`EXCEPTION_APPLIED` audit event.

---

## 3. Precedence (normative)

### 3.1 Evaluation order

```
0. break-glass file (wrapper)            → ALLOW, logged
1. engine state disabled & not expired   → ALLOW, logged
2. repository type → all | mandatory_only | none(→ ALLOW)
3. limits: ref updates                   → LIMIT_REF_UPDATES (waivable)
4. identity — mandatory deny_users/deny_groups → REJECT (waivable only with mandatory:true)
5. identity — scoped users/groups rules (§3.2)  → REJECT or continue
6. per ref update: deletes stop here (identity only)
7. traversal: new commits per policy class; limits: commits / blobs
8. content rules — effective set per (project, ref) (§3.3) → violations
9. each violation: exceptions (§3.4) → waived | WOULD_REJECT(audit) | REJECT(enforce)
10. any REJECT → exit 1 with all REJECT violations (capped at 20 lines) ; else exit 0
```

Identity rules (steps 4–5) are **always enforced**. `mode` affects content
rules only. Identity denies are explicit decisions about a person and have no
"dry-run" meaning.

### 3.2 Identity resolution

Every identity rule has a **specificity level**, from least to most specific:

```
global (users.X.push)                         level 0
global + ref (users.X.refs)                   level 0.5
namespace depth d                             level d
namespace depth d + ref                       level d + 0.5
project                                       level 1000
project + ref                                 level 1000.5
```

Resolution works as follows:

1. Collect the rules that match the pushing user and the user's cached groups,
   for this project and this ref.
2. Take the **highest level** that has any match. Only that level decides.
3. Within that level, **user rules beat group rules**. If any user rule
   matches, only user rules are considered.
4. Among the remaining rules, **deny beats allow**.
5. If nothing matched at any level, the result is ALLOW. git-policy is
   additive; GitLab has already authorised the push.

Scope dominates ref: a project-level `allow` outranks a global
`refs/heads/main: deny`. If you need "alex may never push to main anywhere",
put it at the project/namespace level with a ref, or use Protected Branches.

Stale membership (§5 of Phase 1): once the cache is older than `hard_max_age`,
group `allow` rules are treated as **not matching**. Group `deny` rules keep
using the last-known membership.

### 3.3 Content resolution — effective rule set for (project P, ref R)

The layer chain, from least to most specific:

```
defaults → defaults.refs[matching R]
→ ns(depth1) → ns(depth1).refs[R] → … → ns(deepest) → ns(deepest).refs[R]
→ projects[P] → projects[P].refs[R]
```

**Block lists** (extensions, paths, signatures):

```
effective = {}
for layer in chain:                 # least → most specific
    effective += layer.blocked_*
    effective -= layer.unblock_*    # same layer: block wins over unblock
effective += mandatory.blocked_*    # always, last; cannot be unblocked
```

When several ref patterns match within the same layer, their blocks and
unblocks are merged first, and **block wins**.

**Max file size**:

- The most specific layer that sets `max_file_size` wins. When several ref
  patterns match at the same layer, the **smallest** value wins.
- The result is then capped at `mandatory.max_file_size`.
- A scoped value above the cap is a validation error.

**Mode** for non-mandatory violations: the most specific `mode` in the chain,
otherwise `settings.mode`. Mandatory violations use `mandatory.mode`.

**`projects[P].enabled: false`**: the chain is empty, so only mandatory rules
apply.

**Attribution**: every violation records the layer and key that produced it
(e.g. `namespaces.finance.blocked_extensions`). This feeds the audit log and
the `git-policy explain` command (Phase 5). The internal layer path is **not**
shown to the developer; they see the rule code and the file.

**Policy class** (Phase 1 R1-2): two refs are in the same class when the
content rule set resolved for them is identical. That is a fingerprint of the
resolved block lists, size, and mode for the same P. The traversal excludes
commits reachable from existing branches and tags in the same class, or in a
strictly-stricter one.

### 3.4 Exception matching

An exception waives violation V (rule code C, user U, groups G, project P,
ref R, path F, blob size S) when **all** of these hold:

- `C ∈ rules`
- if V comes from `mandatory`, then `mandatory: true`
- today ≤ `expires`
- `subjects` is empty, **or** U ∈ `subjects.users`, **or** G ∩ `subjects.groups` ≠ ∅
  (the groups case requires a fresh-enough cache)
- each `scope` dimension that is given matches: P under a listed namespace,
  P listed, R matches a ref glob, F matches a path glob
- for `FILE_TOO_LARGE` with `max_file_size`: S ≤ that value

Exceptions never stack or combine. Each violation is checked independently,
and the first matching exception (in document order) is the one recorded.

---

## 4. Rule codes

This is the stable contract used by messages, audit, exceptions, and tests.

| Code | Category | Waivable | Default remediation text (overridable) |
|---|---|---|---|
| `MANDATORY_USER_DENIED` | identity | only with `mandatory: true` | Your account is not permitted to push. Contact support. |
| `MANDATORY_GROUP_DENIED` | identity | only with `mandatory: true` | same |
| `USER_PUSH_DENIED` | identity | yes | You are not permitted to push to this project/ref. |
| `GROUP_PUSH_DENIED` | identity | yes | Your group is not permitted to push to this project/ref. |
| `BLOCKED_EXTENSION` | content | yes | Binary artifacts do not belong in Git. Publish them to Nexus. |
| `BLOCKED_PATH` | content | yes | Build output / vendored package folders must not be committed. |
| `BLOCKED_SIGNATURE` | content | yes | This file is a Windows executable/library regardless of its name. |
| `FILE_TOO_LARGE` | content | yes | Store large files in Nexus. |
| `LIMIT_REF_UPDATES` | limit | yes (no paths) | Push fewer refs at once, or request a migration exception. |
| `LIMIT_COMMITS` | limit | yes (no paths) | same |
| `LIMIT_OBJECTS` | limit | yes (no paths) | same |
| `EVAL_TIMEOUT` | limit | **no** | Push in smaller batches. |
| `POLICY_UNAVAILABLE` | system | **no** | Contact the platform team (fail-closed). |
| `MEMBERSHIP_UNAVAILABLE` | system | **no** | Contact the platform team (fail-closed). |
| `INVALID_REF_UPDATE` | system | **no** | Malformed ref update. |
| `INTERNAL_ERROR` | system | **no** | Contact the platform team (fail-closed). |

---

## 5. Validation catalogue

Codes `V…` are errors that block `apply`. `W…` are warnings, which are shown
but do not block.

| Code | Check | Detected by |
|---|---|---|
| V001 | YAML syntax | Go validator |
| V002 | duplicate YAML key | Go validator |
| V003 | unknown key | Go + JSON Schema |
| V004 | wrong `apiVersion` / `kind` | Go + JSON Schema |
| V005 | type / enum / format errors (sizes, durations, dates, globs, names) | Go + JSON Schema |
| V010 | case-folded key collision (users / namespaces / projects / groups) | Go |
| V011 | invalid GitLab path (e.g. ends with `.`, `.git`, `.atom`; empty segment) | Go |
| V012 | project key with fewer than 2 components | Go + JSON Schema |
| V020 | `unblock_*` of a mandatory entry | Go |
| V021 | scoped `max_file_size` > `mandatory.max_file_size` | Go |
| V022 | `mandatory` containing `refs` or `unblock_*` (mandatory is global and ref-independent) | Go + JSON Schema |
| V023 | invalid glob (unbalanced `[`, empty segment, `***`) | Go |
| V030 | exception id duplicated | Go |
| V031 | exception has no subjects and no project/namespace scope | Go |
| V032 | exception lifetime > `max_lifetime_days` | Go |
| V033 | exception `paths` with a path-less rule | Go |
| V034 | exception `max_file_size` without `FILE_TOO_LARGE` | Go |
| V035 | non-waivable rule in exception | Go + JSON Schema |
| V040 | `revision` ≤ active revision (apply only) | Go (server side) |
| V041 | `soft_max_age` > `hard_max_age` | Go |
| V042 | the policy has group deny rules (`on_unavailable: deny_if_group_rules`), the engine is enforcing, and no membership cache is deployed, so every push would be rejected. Checked by `apply`; `enable` refuses the same state | Go (server side) |
| W001 | leading dot in extension (normalised) | Go |
| W002 | duplicate list entry | Go |
| W003 | `unblock_*` of an entry never blocked above it | Go |
| W004 | exception already expired | Go |
| W010 | user / group / project / namespace not in the inventory snapshot (§6) | Go (if inventory present) |
| W011 | rule can never take effect (e.g. `users.X.push: allow` while X ∈ `mandatory.deny_users`) | Go |

---

## 6. Companion file formats (runtime, generated)

These files are not written by humans. They are defined here so that the
schema set is complete.

**`membership/current.json`**: produced by `sync-membership` and read by the
hook.

```json
{
  "schema": "git-policy/membership/v1",
  "generated_at": "2026-09-29T08:00:00Z",
  "generator": "git-policy 0.1.0",
  "source": {"gitlab_url_sha256": "…"},
  "groups": ["contractors", "devops", "finance/auditors"],
  "users": {
    "alex":  {"state": "active",  "groups": ["contractors"]},
    "bob":   {"state": "blocked", "groups": []}
  }
}
```

- The sync includes only the groups referenced by the active policy, with
  inherited memberships resolved.
- Usernames are stored case-folded.

**`membership/inventory.json`**: every username, group path, and project path.
It is used **only** by `validate` for the W010 warnings and is never read on
the push path.

**`state/engine.json`**

```json
{"schema": "git-policy/state/v1", "enabled": false,
 "changed_at": "2026-09-29T09:10:00Z", "changed_by": "jenkins#57 approved-by:ali",
 "reason": "Nexus migration window", "expires_at": "2026-09-29T13:10:00Z"}
```

`disable` requires `expires_at`. The maximum TTL is 7 days.

---

## 7. Worked examples

These use [`examples/policy.example.yaml`](../../examples/policy.example.yaml).
They are normative and will become engine test cases in Phases 5–8.

| # | Push | Resolution | Result |
|---|---|---|---|
| E01 | alex → `finance/payment-api`, any ref | project-level user rule `deny` | **REJECT** `USER_PUSH_DENIED` |
| E02 | alex → `finance/reporting` | project-level user rule `allow` (outranks nothing lower) | identity OK → content |
| E03 | carol (group `contractors`) → `outsourcing/portal` | namespace-level group `allow` (level 1) beats global group `deny` (level 0) | identity OK |
| E04 | carol → `finance/accounting` | only the global group `deny` matches | **REJECT** `GROUP_PUSH_DENIED` |
| E05 | carol → `outsourcing/portal`, cache older than `hard_max_age` | group allow ignored, global group deny still applied | **REJECT** `GROUP_PUSH_DENIED` |
| E06 | anyone → `finance/payment-api`, adds `Lib/Mic.Caching.DLL` | mandatory `dll` | **REJECT** `BLOCKED_EXTENSION` |
| E07 | anyone → `finance/payment-api`, adds `Lib/evil.dll.` (trailing dot) | normalised to `evil.dll` | **REJECT** `BLOCKED_EXTENSION` |
| E08 | anyone → `finance/payment-api`, 8 MiB `data.bin` | project 5MiB wins over finance 10MiB | **REJECT** `FILE_TOO_LARGE` |
| E09 | same 8 MiB blob → `finance/accounting` | finance 10MiB | **ACCEPT** |
| E10 | alex → `finance/reporting`, adds `templates/q3.zip` | defaults block `zip`, the project unblocks it | **ACCEPT** |
| E11 | `finance/legacy/billing`, adds `x.pdb` | defaults block `pdb`; effective mode = `finance/legacy.mode: audit` | **ACCEPT** + `WOULD_REJECT` audit |
| E12 | `finance/legacy/billing`, adds `x.dll` | mandatory, `mandatory.mode: enforce` | **REJECT** |
| E13 | `finance/legacy-erp` `refs/heads/main`, adds `vendor/VendorSdk/Sdk.dll` | mandatory `dll`, waived by EXC-2026-001 (`mandatory: true`, project+ref+path match). `legacy-erp` is a *project under* `finance`, not under `finance/legacy` (component matching) | **ACCEPT** + `EXCEPTION_APPLIED` |
| E14 | same file on `refs/heads/dev` | ref does not match the exception | **REJECT** |
| E15 | `sandbox/playground`, adds `a.zip` | `enabled: false` → mandatory only | **ACCEPT** |
| E16 | `sandbox/playground`, adds `a.exe` | mandatory | **REJECT** |
| E17 | `finance/accounting` `refs/heads/release/2.0`, adds `readme.txt` that is a PE file | `finance.refs["refs/heads/release/**"].blocked_signatures: [pe]` | **REJECT** `BLOCKED_SIGNATURE` |
| E18 | same PE `readme.txt` on `refs/heads/feature/x` | signature rule is release-only | **ACCEPT** |
| E19 | svc-migration pushes 80 000 commits → `legacy/erp-import` | `LIMIT_COMMITS` waived by EXC-2026-002 | **ACCEPT** + `EXCEPTION_APPLIED` |
| E20 | member of `data-science` pushes a 120 MiB model to `analytics/forecast` | mandatory cap 50MiB; EXC-2026-003 waives up to 200MiB | **ACCEPT** + `EXCEPTION_APPLIED` |
| E21 | same member, 300 MiB | above the exception's `max_file_size` | **REJECT** `FILE_TOO_LARGE` |
| E22 | `terminated.user` → anywhere | `mandatory.deny_users` | **REJECT** `MANDATORY_USER_DENIED` |
| E23 | policy with `projects.x/y.unblock_extensions: [dll]` | V020 | **apply refused**, the old policy stays active |
