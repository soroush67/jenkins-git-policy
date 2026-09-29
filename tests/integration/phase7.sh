#!/usr/bin/env bash
# Phase 7 integration test: extension and path rules enforced by the real
# installed hook on real `git push`, including the anti-bypass cases, audit
# mode rollout, path-scoped exceptions, PoC retirement and concurrency.
#
# Usage: tests/integration/phase7.sh [path/to/git-policy-binary]
set -uo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
BIN=$(realpath "${1:-$(ls "$ROOT_DIR"/dist/git-policy-*-linux-amd64 | head -1)}")
WORK=$(mktemp -d)
trap 'chmod -R u+w "$WORK" 2>/dev/null; rm -rf "$WORK"' EXIT
GP="$WORK/gp"
HOOKD="$WORK/custom_hooks/pre-receive.d"
HOOK="$HOOKD/50-git-policy"
REPO="$WORK/repo.git"
CL="$WORK/clone"
PASS=0 FAIL=0
ok() { PASS=$((PASS + 1)); printf '  \e[32mPASS\e[0m %s\n' "$1"; }
ko() { FAIL=$((FAIL + 1)); printf '  \e[31mFAIL\e[0m %s\n' "$1"; [ -n "${2:-}" ] && printf '%s\n' "$2" | sed 's/^/       /'; return 0; }
adm() { local sub=$1; shift; "$BIN" admin "$sub" --root "$GP" --hook-path "$HOOK" --actor it-test "$@"; }

REV=0
policy() { # policy <mandatory-mode> <settings-mode>
    REV=$((REV + 1))
    cat > "$WORK/p.yaml" <<EOF
apiVersion: git-policy/v1
kind: GitPolicy
metadata: {name: it-phase7, revision: $REV}
settings:
  mode: $2
  messages:
    support: "Help: #devops-help"
    remediation:
      BLOCKED_EXTENSION: "Publish binary dependencies as NuGet packages to Nexus instead of committing them."
mandatory:
  mode: $1
  blocked_extensions: [dll, exe]
defaults:
  blocked_extensions: [pdb, zip]
namespaces:
  finance:
    blocked_paths: ["**/obj/**", "**/bin/Debug/**"]
exceptions:
  - id: EXC-SDK
    rules: [BLOCKED_EXTENSION]
    mandatory: true
    scope: {projects: [finance/legacy-erp], refs: ["refs/heads/main"], paths: ["vendor/VendorSdk/*.dll"]}
    reason: vendor SDK has no NuGet package yet (INFRA-2231)
    expires: $(date -u -d '+30 days' +%F)
EOF
    adm apply --reason "rev $REV" "$WORK/p.yaml" >/dev/null 2>&1 || ko "apply revision $REV"
}

# push <project> <refspec...> ; OUT has the output
push() { local p=$1; shift; OUT=$(cd "$CL" && GL_USERNAME=dev GL_PROJECT_PATH=$p GL_REPOSITORY=project-1 git push origin "$@" 2>&1); }
add() { (cd "$CL" && mkdir -p "$(dirname "$1")" && printf '%s' "${2:-x$RANDOM}" > "$1" && git add -- "$1" && git commit -qm "add $1"); }
reset() { (cd "$CL" && git fetch -q origin 2>/dev/null; git reset -q --hard "origin/${1:-main}"); }
expect() { # expect accept|reject <name> <project> <refspec[ refspec...]> [grep]
    # shellcheck disable=SC2086 # refspecs are word-split on purpose
    if push "$3" $4; then got=accept; else got=reject; fi
    if [ "$got" = "$1" ] && { [ -z "${5:-}" ] || grep -q -- "$5" <<<"$OUT"; }; then ok "$2"; else ko "$2 (got $got)" "$OUT"; fi
}

echo "== setup: repo, PoC stand-in, git-policy installed and enabled"
mkdir -p "$HOOKD"
git init -q --bare "$REPO"
cat > "$REPO/hooks/pre-receive" <<EOF
#!/bin/sh
input=\$(cat)
for h in "$HOOKD"/*; do
    [ -f "\$h" ] && [ -x "\$h" ] || continue
    printf '%s\n' "\$input" | "\$h" || exit 1
done
EOF
chmod +x "$REPO/hooks/pre-receive"
printf '#!/bin/sh\n# PoC stand-in (accepts everything; real PoC blocks DLLs)\nexit 0\n' > "$HOOKD/01-block-dll"; chmod +x "$HOOKD/01-block-dll"
git clone -q "$REPO" "$CL" 2>/dev/null
(cd "$CL" && git config user.email it@test && git config user.name it && git checkout -q -b main && echo hi > README.md && git add . && git commit -qm init && git push -q origin main 2>/dev/null)
adm install >/dev/null
policy enforce enforce
adm enable --reason "phase7" >/dev/null 2>&1 && ok "installed, policy applied, engine enabled" || ko "enable"

echo "== TEST 01-03: normal file, .dll, .DLL"
add src/Program.cs "class P {}"; expect accept "TEST 01 normal source file accepted" finance/app main
add Lib/Mic.Caching.dll; expect reject "TEST 02 .dll rejected" finance/app main "Rule: BLOCKED_EXTENSION"
for want in "File: Lib/Mic.Caching.dll" "Blocked extension: .dll" "Commit: " "Required action (BLOCKED_EXTENSION): Publish binary dependencies as NuGet packages to Nexus" "Help: #devops-help"; do
    grep -q "GL-HOOK-ERR: $want" <<<"$OUT" && ok "message: '$want'" || ko "message: '$want'" "$OUT"
done
reset; add Lib/TEST.DLL; expect reject "TEST 03 .DLL rejected" finance/app main "File: Lib/TEST.DLL"
reset; add Lib/Test.Dll; expect reject "Test.Dll rejected" finance/app main "BLOCKED_EXTENSION"

echo "== anti-bypass (TEST 09-11, 13, 14)"
reset; add evil.dll; (cd "$CL" && git rm -q evil.dll && git commit -qm rm)
expect reject "TEST 09 DLL added in A, deleted in B -> rejected" finance/app main "File: evil.dll"
A=$(cd "$CL" && git rev-parse HEAD~1); grep -q "Commit: $A" <<<"$OUT" && ok "message names the introducing commit A" || ko "commit A in message" "$OUT"
reset; (cd "$CL" && git mv README.md README.dll && git commit -qm mv); expect reject "rename of an existing file to .dll rejected" finance/app main "File: README.dll"
reset; for f in "a.dll." "b.dll " "c.exe::\$DATA"; do add "tricks/$f"; done
expect reject "NTFS tricks (trailing dot/space, ::\$DATA) rejected" finance/app main "BLOCKED_EXTENSION"
n=$(grep -c "GL-HOOK-ERR: File: tricks/" <<<"$OUT"); [ "$n" = 3 ] && ok "all 3 NTFS tricks reported" || ko "NTFS tricks reported ($n/3)" "$OUT"
reset; (cd "$CL" && git checkout -q -b feat && echo x > tool.exe && git add . && git commit -qm exe)
expect reject "TEST 10 new branch with prohibited object rejected" finance/app feat "File: tool.exe"
(cd "$CL" && git checkout -q main && git branch -q -D feat)
reset; (cd "$CL" && git commit -q --amend -m "amended" --allow-empty && echo y > x.pdb && git add . && git commit -qm pdb)
expect reject "TEST 11 force push with prohibited object rejected" finance/app +main "File: x.pdb"
reset; add lib/v.dll; (cd "$CL" && git tag -a -m v v9 && git reset -q --hard HEAD~1)
expect reject "TEST 13 annotated tag introducing a DLL rejected" finance/app v9 "Ref: refs/tags/v9"
(cd "$CL" && git tag -d v9 >/dev/null)
reset; (cd "$CL" && git checkout -q -b ok1 && echo ok > ok.txt && git add . && git commit -qm ok && git checkout -q -b bad1 && echo z > z.dll && git add . && git commit -qm bad && git checkout -q main)
expect reject "TEST 14 multi-ref push with one bad ref rejected as a whole" finance/app "ok1 bad1" "Ref: refs/heads/bad1"
git --git-dir="$REPO" rev-parse -q --verify refs/heads/ok1 >/dev/null && ko "ok1 must not be created (atomic)" || ok "clean ref of a rejected push not created"
(cd "$CL" && git branch -q -D ok1 bad1)

echo "== legacy content: deleting is allowed, modifying is not"
cat > "$REPO/hooks/pre-receive.bak" < "$REPO/hooks/pre-receive"; printf '#!/bin/sh\nexit 0\n' > "$REPO/hooks/pre-receive"
reset; add legacy/Old.dll "v1"; push finance/app main >/dev/null   # seeded while hooks are off
cp "$REPO/hooks/pre-receive.bak" "$REPO/hooks/pre-receive"
add legacy/Old.dll "v2"; expect reject "modifying a legacy DLL rejected (new blob)" finance/app main "File: legacy/Old.dll"
reset; (cd "$CL" && git rm -q legacy/Old.dll && git commit -qm "remove legacy dll")
expect accept "deleting a legacy DLL accepted" finance/app main

echo "== path rules and scopes"
reset; add src/obj/Debug/app.cache; expect reject "blocked path **/obj/** in finance" finance/app main "Blocked path pattern: \*\*/obj/\*\*"
expect accept "same path outside finance accepted" other/app main
reset; add vendor/VendorSdk/Sdk.dll; expect accept "path-scoped exception EXC-SDK accepted on main" finance/legacy-erp main
grep -q '"action":"EXCEPTION_APPLIED".*"exception":"EXC-SDK".*' "$GP"/logs/audit-*.jsonl && ok "exception use audited with file" || ko "exception audited"
reset; add vendor/Other/x.dll; expect reject "exception does not cover other paths" finance/legacy-erp main

echo "== audit (shadow) mode rollout"
policy audit audit
reset; add shadow/y.dll; expect accept "mandatory audit mode: DLL accepted" finance/app main
grep -q '"action":"WOULD_REJECT".*"file":"shadow/y.dll"' "$GP"/logs/audit-*.jsonl && ok "WOULD_REJECT logged with file" || ko "WOULD_REJECT logged"
policy enforce enforce
reset; add shadow/z.dll; expect reject "back to enforce: rejected" finance/app main

echo "== audit REJECT event"
line=$(grep '"action":"REJECT"' "$GP"/logs/audit-*.jsonl | grep '"file":"Lib/Mic.Caching.dll"' | head -1)
for k in '"rule":"BLOCKED_EXTENSION"' '"source":"mandatory"' '"project":"finance/app"' '"ref":"refs/heads/main"' '"commit":"' '"user":"dev"'; do
    grep -qF -- "$k" <<<"$line" && ok "REJECT event has $k" || ko "REJECT event has $k" "$line"
done

echo "== retire the PoC (non-destructive)"
out=$(adm retire-hook --name 01-block-dll 2>&1) && [ ! -e "$HOOKD/01-block-dll" ] && ls "$GP"/backup/01-block-dll.retired.* >/dev/null 2>&1 \
    && ok "PoC moved to backup/" || ko "retire PoC" "$out"
adm retire-hook --name 50-git-policy >/dev/null 2>&1 && ko "must refuse retiring git-policy itself" || ok "refuses to retire its own hook"
adm retire-hook --name "../../etc/passwd" >/dev/null 2>&1 && ko "path traversal accepted" || ok "hook name path traversal refused"
grep -q '"action":"HOOK_RETIRED"' "$GP"/logs/audit-*.jsonl && ok "retirement audited" || ko "retirement audited"
reset; add after/poc.dll; expect reject "DLL still rejected by git-policy alone" finance/app main "BLOCKED_EXTENSION"

echo "== TEST 19: concurrent pushes"
pids=(); results="$WORK/conc"; mkdir -p "$results"
for i in $(seq 1 10); do
    ( c="$WORK/cc$i"; git clone -q "$REPO" "$c" 2>/dev/null; cd "$c"; git config user.email it@test; git config user.name it
      git checkout -q -b "c$i" origin/main 2>/dev/null
      if [ $((i % 2)) = 0 ]; then f="bin$i.dll"; else f="src$i.cs"; fi
      echo "$i" > "$f"; git add .; git commit -qm "$i"
      if GL_USERNAME=dev GL_PROJECT_PATH=finance/app git push -q origin "c$i" 2>/dev/null; then echo accept > "$results/$i"; else echo reject > "$results/$i"; fi ) &
    pids+=($!)
done
wait "${pids[@]}"
bad=0; for i in $(seq 1 10); do want=accept; [ $((i % 2)) = 0 ] && want=reject; [ "$(cat "$results/$i")" = "$want" ] || bad=$((bad + 1)); done
[ "$bad" = 0 ] && ok "10 concurrent pushes: 5 clean accepted, 5 DLL rejected" || ko "concurrent outcomes ($bad wrong)"

echo
echo "RESULT: $PASS passed, $FAIL failed"
[ "$FAIL" = 0 ]
