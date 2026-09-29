#!/usr/bin/env bash
# Phase 6 integration test: anti-bypass traversal with real `git push`.
#
# The test repository's pre-receive runs `git-policy scan` exactly where the
# production hook runs: inside receive-pack, with the pushed objects still in
# Git's quarantine directory (GIT_QUARANTINE_PATH / GIT_OBJECT_DIRECTORY /
# GIT_ALTERNATE_OBJECT_DIRECTORIES). It records what the push introduces and
# accepts the push, so each scenario can be asserted on the scan report.
# The last block runs the real installed engine for limit enforcement.
#
# Usage: tests/integration/phase6.sh [path/to/git-policy-binary]
set -uo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
BIN=$(realpath "${1:-$(ls "$ROOT_DIR"/dist/git-policy-*-linux-amd64 | head -1)}")
WORK=$(mktemp -d)
trap 'chmod -R u+w "$WORK" 2>/dev/null; rm -rf "$WORK"' EXIT
REPO="$WORK/repo.git"
CL="$WORK/clone"
REPORT="$WORK/report.json"
PASS=0 FAIL=0
ok() { PASS=$((PASS + 1)); printf '  \e[32mPASS\e[0m %s\n' "$1"; }
ko() { FAIL=$((FAIL + 1)); printf '  \e[31mFAIL\e[0m %s\n' "$1"; [ -n "${2:-}" ] && printf '%s\n' "$2" | sed 's/^/       /'; }

cat > "$WORK/policy.yaml" <<'EOF'
apiVersion: git-policy/v1
kind: GitPolicy
metadata: {name: it-phase6, revision: 1}
mandatory:
  blocked_extensions: [dll, exe]
namespaces:
  finance:
    refs:
      "refs/heads/release/**": {blocked_signatures: [pe]}
EOF

git init -q --bare "$REPO"
cat > "$REPO/hooks/pre-receive" <<EOF
#!/bin/sh
input=\$(cat)
{ [ -n "\$GIT_QUARANTINE_PATH" ] && echo quarantine || echo no-quarantine; } > "$WORK/env"
printf '%s\n' "\$input" | "$BIN" scan --json --policy "$WORK/policy.yaml" --project finance/app > "$REPORT" 2>"$WORK/scan.err"
exit 0
EOF
chmod +x "$REPO/hooks/pre-receive"
git clone -q "$REPO" "$CL" 2>/dev/null
cd "$CL" && git config user.email it@test && git config user.name it && git checkout -q -b main

# q <python expression over the report `r`> : prints the value
q() { python3 -c "import json,sys; r=json.load(open('$REPORT')); print($1)"; }
paths() { q "','.join(sorted(e['Path'] for e in r['entries']))"; }
commits() { q "(r.get('stats') or {}).get('Commits', 0)"; }
check() { # check <name> <expected> <actual>
    if [ "$2" = "$3" ]; then ok "$1"; else ko "$1" "expected: $2"$'\n'"actual:   $3"$'\n'"$(cat "$WORK/scan.err" 2>/dev/null)"; fi
}
c() { mkdir -p "$(dirname "$1")"; echo "$2" > "$1"; git add -- "$1"; git commit -qm "add $1"; }

echo "== setup"
c README.md "hello"
git push -q origin main 2>/dev/null
check "scan runs inside Git quarantine" quarantine "$(cat "$WORK/env")"

echo "== T01 normal push (TEST 01)"
c src/app.cs "class A {}"
git push -q origin main 2>/dev/null
check "one commit, one new path" "1 src/app.cs" "$(commits) $(paths)"

echo "== T02 add in A, delete in B, push A+B together (TEST 09)"
c Lib/Mic.Caching.dll "MZ"
git rm -q Lib/Mic.Caching.dll && git commit -qm "remove dll"
git push -q origin main 2>/dev/null
check "dll found although the tip does not contain it" "2 Lib/Mic.Caching.dll" "$(commits) $(paths)"

echo "== T03 case variants and rename of an existing blob (TEST 02/03)"
c TEST.DLL "x1"; c sub/Test.Dll "x2"
git mv README.md README.exe && git commit -qm rename
git push -q origin main 2>/dev/null
check "all paths reported raw (matching is Phase 7)" "README.exe,TEST.DLL,sub/Test.Dll" "$(paths)"

echo "== T04 new branch (TEST 10)"
git push -q origin main:refs/heads/copy 2>/dev/null
check "branch at existing commit: nothing new" "0 " "$(commits) $(paths)"
git checkout -q -b feature && c tools/x.exe "MZ"
git push -q origin feature 2>/dev/null
check "branch with a new commit: its content" "1 tools/x.exe" "$(commits) $(paths)"

echo "== T05 force push (TEST 11)"
git reset -q --hard HEAD~1 && c tools/y.txt "y"
git push -q -f origin feature 2>/dev/null
check "force push: only the rewritten commit" "1 tools/y.txt" "$(commits) $(paths)"

echo "== T06 deletion (TEST 12)"
git push -q origin :copy 2>/dev/null
check "branch deletion introduces nothing" "0 " "$(commits) $(paths)"

echo "== T07 tags (TEST 13)"
git checkout -q main
git tag light HEAD && git push -q origin light 2>/dev/null
check "lightweight tag on existing commit: nothing" "0 " "$(commits) $(paths)"
c rel.dll "r"
git tag -a -m v1 v1 && git reset -q --hard HEAD~1
git push -q origin v1 2>/dev/null
check "annotated tag on a new commit: its content" "1 rel.dll" "$(commits) $(paths)"
BLOB=$(printf 'MZ' | git hash-object -w --stdin)
SUB=$(printf '100644 blob %s\tevil.dll\n' "$BLOB" | git mktree)
TREE=$(printf '040000 tree %s\tinside\n' "$SUB" | git mktree)
git tag tree-tag "$TREE" && git push -q origin tree-tag 2>/dev/null
check "tag pointing at a tree: all its paths" "inside/evil.dll" "$(paths)"

echo "== T08 multiple refs in one push (TEST 14)"
git checkout -q -b m1 && c m1.txt "1"
git checkout -q -b m2 && c m2.txt "2"
git push -q origin m1 m2 2>/dev/null
check "two refs, shared history scanned once" "2 m1.txt,m2.txt" "$(commits) $(paths)"
check "commits attributed per ref" "1 1" "$(q "r['ref_commits'].get('refs/heads/m1',0), r['ref_commits'].get('refs/heads/m2',0)" | tr -d '(),')"

echo "== T09 stricter release class"
git checkout -q main && git checkout -q -b develop && c dev-tool.exe "MZ" && git push -q origin develop 2>/dev/null
git push -q origin develop:refs/heads/release/1.0 2>/dev/null
n=$(commits); [ "$n" -gt 1 ] && ok "develop -> release/1.0 rescans history not vetted under release rules ($n commits)" || ko "release class rescan" "commits=$n"
git push -q origin develop:refs/heads/feature/other 2>/dev/null
check "develop -> feature/other (same class): nothing" "0" "$(commits)"
git push -q origin develop:refs/heads/release/1.1 2>/dev/null
check "release/1.0 exists -> release/1.1 at same commit: nothing" "0" "$(commits)"

echo "== T10 content from a GitLab-internal ref is not trusted (fork MR bypass)"
git checkout -q main && git checkout -q -b fork && c vendor/fork.dll "x" && FORK=$(git rev-parse HEAD)
git push -q origin "$FORK:refs/merge-requests/7/head" 2>/dev/null   # (GitLab writes these without hooks)
git checkout -q main && git merge -q --ff-only fork && git push -q origin main 2>/dev/null
check "merging an MR head scans its content" "vendor/fork.dll" "$(paths)"

echo "== T11 real engine: limits enforced in the hook"
GP="$WORK/gp"; HOOKD="$WORK/hooks.d"; mkdir -p "$HOOKD"
"$BIN" admin install --root "$GP" --hook-path "$HOOKD/50-git-policy" --actor it >/dev/null
cat > "$WORK/limits.yaml" <<'EOF'
apiVersion: git-policy/v1
kind: GitPolicy
metadata: {name: it-limits, revision: 1}
settings:
  limits: {max_new_commits: 5}
mandatory:
  blocked_extensions: [dll]
exceptions:
  - id: EXC-IMPORT
    rules: [LIMIT_COMMITS]
    subjects: {users: [svc-import]}
    reason: history import for the legacy project
    expires: 2099-01-01
EOF
sed -i "s/2099-01-01/$(date -u -d '+30 days' +%F)/" "$WORK/limits.yaml"
"$BIN" admin apply --root "$GP" --hook-path "$HOOKD/50-git-policy" --actor it "$WORK/limits.yaml" >/dev/null 2>&1
"$BIN" admin enable --root "$GP" --hook-path "$HOOKD/50-git-policy" --actor it >/dev/null 2>&1
cat > "$REPO/hooks/pre-receive" <<EOF
#!/bin/sh
exec "$HOOKD/50-git-policy"
EOF
for i in $(seq 1 8); do c "bulk$i.txt" "$i"; done
OUT=$(GL_USERNAME=dev GL_PROJECT_PATH=legacy/app git push origin main 2>&1) && ko "8 new commits > 5 must be rejected" "$OUT" \
    || { grep -q "Rule: LIMIT_COMMITS" <<<"$OUT" && ok "LIMIT_COMMITS rejects an oversized push" || ko "limit message" "$OUT"; }
GL_USERNAME=svc-import GL_PROJECT_PATH=legacy/app git push -q origin main 2>/dev/null && ok "svc-import passes via exception EXC-IMPORT" || ko "exception for limit"
grep -q '"action":"EXCEPTION_APPLIED".*"rule":"LIMIT_COMMITS"' "$GP"/logs/audit-*.jsonl && ok "limit waiver audited" || ko "limit waiver audited"

echo "== T12 performance: large history, small push"
cd "$WORK" && git init -q --bare big.git && git clone -q big.git bigc 2>/dev/null && cd bigc
git config user.email it@test && git config user.name it
python3 - <<'PYEOF' | git fast-import --quiet
import sys
w = sys.stdout.write
for i in range(1, 20001):
    data = f"line {i}\n"
    w(f"commit refs/heads/main\nmark :{i}\ncommitter it <it@test> {1700000000+i} +0000\ndata 3\nc{i%10}\n")
    if i > 1: w(f"from :{i-1}\n")
    w(f"M 100644 inline f{i%500}.txt\ndata {len(data)}\n{data}\n")
PYEOF
git checkout -q main && git push -q origin main 2>/dev/null
git -C "$WORK/big.git" commit-graph write --reachable 2>/dev/null
echo x > new.txt && git add new.txt && git commit -qm new
cat > "$WORK/big.git/hooks/pre-receive" <<EOF
#!/bin/sh
"$BIN" scan --json --policy "$WORK/policy.yaml" --project finance/app > "$REPORT"
EOF
chmod +x "$WORK/big.git/hooks/pre-receive"
git push -q origin main 2>/dev/null
check "20k-commit repo, 1-commit push: 1 commit inspected" "1" "$(commits)"
ms=$(q "r['duration_ms']"); [ "$ms" -lt 500 ] && ok "scan took ${ms} ms on a 20,000-commit history" || ko "scan too slow: ${ms} ms"
git push -q origin main:refs/heads/copy2 2>/dev/null
ms=$(q "r['duration_ms']"); check "new branch on 20k history: 0 commits" "0" "$(commits)"
echo "       (new branch scan: ${ms} ms)"

echo
echo "RESULT: $PASS passed, $FAIL failed"
[ "$FAIL" = 0 ]
