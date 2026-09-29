#!/usr/bin/env bash
# Phase 4 integration test: real `git push` into a local bare repository whose
# pre-receive runs every executable in a pre-receive.d directory, the way
# Gitaly runs global custom hooks. Exercises install, enable/disable/TTL,
# apply/rollback, fail-closed behaviour, break-glass, concurrency, uninstall.
#
# Usage: tests/integration/phase4.sh [path/to/git-policy-binary]
# Needs: git, bash. Runs unprivileged in a temp directory; touches nothing else.
set -uo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
BIN=${1:-$(ls "$ROOT_DIR"/dist/git-policy-*-linux-amd64 | head -1)}
BIN=$(realpath "$BIN")
WORK=$(mktemp -d)
trap 'chmod -R u+w "$WORK" 2>/dev/null; rm -rf "$WORK"' EXIT

GP="$WORK/gp"                                # engine root
HOOKD="$WORK/custom_hooks/pre-receive.d"     # simulated global hook dir
HOOK="$HOOKD/50-git-policy"
REPO="$WORK/repo.git"
CLONE="$WORK/clone"
PASS=0 FAIL=0

gp() { "$GP/bin/git-policy" "$@"; }
adm() { local sub=$1; shift; "$BIN" admin "$sub" --root "$GP" --hook-path "$HOOK" --actor it-test "$@"; }
st() { "$BIN" status --json --root "$GP" --hook-path "$HOOK"; }
ok() { PASS=$((PASS + 1)); printf '  \e[32mPASS\e[0m %s\n' "$1"; }
ko() { FAIL=$((FAIL + 1)); printf '  \e[31mFAIL\e[0m %s\n' "$1"; [ -n "${2:-}" ] && printf '%s\n' "$2" | sed 's/^/       /'; }

policy() { # policy <revision>
    printf 'apiVersion: git-policy/v1\nkind: GitPolicy\nmetadata: {name: it, revision: %s}\nmandatory:\n  blocked_extensions: [dll]\n' "$1"
}

# push <branch> -> exit code; output in $OUT. Commits a new file each time.
N=0
push() {
    N=$((N + 1))
    (cd "$CLONE" && git checkout -q -B "$1" 2>/dev/null && echo "$N" > "f$N.txt" && git add . && git commit -qm "c$N")
    OUT=$(cd "$CLONE" && GL_USERNAME=alex GL_PROJECT_PATH=finance/payment-api GL_PROTOCOL=ssh \
        git push -q origin "$1" 2>&1)
}
expect_push() { # expect_push accept|reject <name> [grep-pattern]
    if push main; then got=accept; else got=reject; fi
    if [ "$got" = "$1" ] && { [ -z "${3:-}" ] || grep -q -- "$3" <<<"$OUT"; }; then ok "$2"; else ko "$2 (got $got)" "$OUT"; fi
}
json() { python3 -c "import json,sys; d=json.load(sys.stdin)
for k in sys.argv[1].split('.'): d=d[k]
print(d)" "$1"; }

echo "== setup ($WORK)"
mkdir -p "$HOOKD"
git init -q --bare "$REPO"
cat > "$REPO/hooks/pre-receive" <<EOF
#!/bin/sh
# Gitaly-like runner: every executable in pre-receive.d, same stdin, any failure rejects.
input=\$(cat)
for h in "$HOOKD"/*; do
    [ -f "\$h" ] && [ -x "\$h" ] || continue
    printf '%s\n' "\$input" | "\$h" || exit 1
done
EOF
chmod +x "$REPO/hooks/pre-receive"
printf '#!/bin/sh\nexit 0\n' > "$HOOKD/01-block-dll" && chmod +x "$HOOKD/01-block-dll"   # stand-in for the PoC
git clone -q "$REPO" "$CLONE" 2>/dev/null
git -C "$CLONE" config user.email it@test && git -C "$CLONE" config user.name it

echo "== T01 install is non-destructive and starts DISABLED"
out=$(adm install 2>&1)
if [ -x "$GP/bin/git-policy" ] && [ -x "$HOOK" ] && [ -x "$HOOKD/01-block-dll" ] && grep -q "01-block-dll (left untouched)" <<<"$out"; then ok "binary, hook installed; PoC untouched"; else ko "install" "$out"; fi
[ "$(st | json engine.state)" = disabled ] && ok "engine starts disabled" || ko "engine starts disabled"
out=$(adm install 2>&1); grep -q "hook: *unchanged" <<<"$out" && ok "install is idempotent" || ko "idempotent install" "$out"
echo "# local edit" >> "$HOOK"
adm install >/dev/null 2>&1 && ko "modified hook must not be overwritten silently" || ok "refuses to overwrite a modified hook"
out=$(adm install --replace-hook 2>&1); grep -q "hook: *replaced" <<<"$out" && ls "$GP"/backup/50-git-policy.* >/dev/null 2>&1 && ok "--replace-hook backs up and replaces" || ko "--replace-hook" "$out"

echo "== T02 disabled engine: pushes pass, audited"
expect_push accept "push accepted while disabled"
grep -q PUSH_ALLOWED_ENGINE_DISABLED "$GP"/logs/audit-*.jsonl && ok "disabled push audited" || ko "disabled push audited"

echo "== T03 enable is refused without a policy"
adm enable >/dev/null 2>&1 && ko "enable without policy must be refused" || ok "enable refused without policy"

echo "== T04 invalid policy never becomes active"
printf 'apiVersion: git-policy/v1\nkind: GitPolicy\nmetadata: {name: it, revision: 1}\ndefaults: {max_file_size: 20MB}\n' > "$WORK/bad.yaml"
out=$(adm apply "$WORK/bad.yaml" 2>&1) && ko "invalid apply accepted" "$out" || { grep -q V005 <<<"$out" && ok "invalid policy refused (V005)" || ko "invalid refusal message" "$out"; }
[ "$(st | json policy.stored_versions)" = 0 ] && ok "nothing stored after refusal" || ko "nothing stored after refusal"

echo "== T05 apply + enable"
policy 1 > "$WORK/p1.yaml"; policy 2 > "$WORK/p2.yaml"
adm apply --reason "first" "$WORK/p1.yaml" >/dev/null 2>&1 && ok "apply revision 1 -> 000001" || ko "apply rev 1"
out=$(adm apply "$WORK/p1.yaml" 2>&1); grep -q V040 <<<"$out" && ok "same revision refused (V040)" || ko "same revision refused"
adm enable --reason "go live" >/dev/null 2>&1 && ok "enable" || ko "enable"
expect_push accept "push accepted with engine enabled (no content rules yet)"
[ "$(st | json health)" = OK ] && ok "status health OK" || ko "status health OK" "$(st)"

echo "== T06 rollback"
adm apply "$WORK/p2.yaml" >/dev/null 2>&1
[ "$(st | json policy.version)" = 000002 ] && ok "revision 2 active as 000002" || ko "rev 2 active"
adm rollback >/dev/null 2>&1 && ko "rollback without reason must fail" || ok "rollback requires --reason"
adm rollback --reason "bad rules" >/dev/null 2>&1
[ "$(st | json policy.version)" = 000001 ] && [ "$(st | json policy.previous)" = 000002 ] && ok "rolled back to 000001 (previous 000002)" || ko "rollback" "$(st)"
grep -q POLICY_ROLLBACK "$GP"/logs/audit-*.jsonl && ok "rollback audited" || ko "rollback audited"

echo "== T07 corrupted active policy -> fallback, then fail closed"
chmod u+w "$GP/policies/000001" "$GP/policies/000001/compiled.json"
echo '{broken' > "$GP/policies/000001/compiled.json"
expect_push accept "corrupt ACTIVE: served PREVIOUS (fallback)"
[ "$(st | json policy.fell_back)" = True ] && ok "status reports fallback" || ko "status reports fallback"
chmod u+w "$GP/policies/000002" "$GP/policies/000002/compiled.json"
echo '{broken' > "$GP/policies/000002/compiled.json"
expect_push reject "no loadable policy: push rejected (fail closed)" "Rule: POLICY_UNAVAILABLE"
policy 3 > "$WORK/p3.yaml"
adm apply --reason recovery "$WORK/p3.yaml" >/dev/null 2>&1
expect_push accept "recovery by applying a fresh policy"

echo "== T08 malformed state -> fail closed; break-glass bypass"
cp "$GP/state/engine.json" "$WORK/state.bak"
echo '{garbage' > "$GP/state/engine.json"
expect_push reject "malformed state: push rejected" "POLICY_UNAVAILABLE"
touch "$GP/state/break-glass"
expect_push accept "break-glass bypasses a broken engine"
grep -q BREAK_GLASS_BYPASS "$GP/logs/break-glass.log" && ok "break-glass push logged" || ko "break-glass logged"
[ "$(st | json health)" = CRITICAL ] && ok "status CRITICAL while break-glass present" || ko "status CRITICAL"
rm "$GP/state/break-glass"; cp "$WORK/state.bak" "$GP/state/engine.json"

echo "== T09 missing binary -> fail closed"
mv "$GP/bin/git-policy" "$WORK/bin.bak"
expect_push reject "binary missing: push rejected"
mv "$WORK/bin.bak" "$GP/bin/git-policy"

echo "== T10 missing state file means ENABLED"
rm "$GP/state/engine.json"
[ "$(st | json engine.enforcing)" = True ] && ok "deleting the state file keeps enforcement on" || ko "missing state => enabled"
adm enable --reason restore >/dev/null 2>&1

echo "== T11 time-limited disable"
adm disable --reason test >/dev/null 2>&1 && ko "disable without --ttl must fail" || ok "disable requires --ttl"
adm disable --reason test --ttl 200h >/dev/null 2>&1 && ko "ttl over 168h must fail" || ok "disable ttl capped at 168h"
adm disable --reason "maintenance" --ttl 2s >/dev/null 2>&1
[ "$(st | json engine.state)" = disabled ] && ok "disabled for 2s" || ko "disabled"
sleep 3
[ "$(st | json engine.state)" = "enabled (disable expired)" ] && ok "disable expired -> enforcing again" || ko "disable expiry" "$(st)"
adm enable --reason "after maintenance" >/dev/null 2>&1

echo "== T12 concurrent pushes during policy updates"
for i in $(seq 1 8); do git -C "$CLONE" checkout -q -B "b$i" main 2>/dev/null; done
(for r in $(seq 10 25); do policy "$r" > "$WORK/pc.yaml"; adm apply "$WORK/pc.yaml" >/dev/null 2>&1; done) &
pids=()
for i in $(seq 1 8); do
    ( clone="$WORK/c$i"; git clone -q "$REPO" "$clone" 2>/dev/null
      cd "$clone" && git config user.email it@test && git config user.name it
      for j in 1 2 3; do echo "$i-$j" > "x$i-$j" && git add . && git commit -qm "$i-$j" && git push -q origin "HEAD:refs/heads/b$i" 2>/dev/null || exit 1; done ) &
    pids+=($!)
done
cfail=0; for p in "${pids[@]}"; do wait "$p" || cfail=$((cfail + 1)); done; wait
[ "$cfail" = 0 ] && ok "24 concurrent pushes accepted while 16 policy versions were deployed" || ko "concurrent pushes ($cfail failed)"
bad=$(python3 -c "import json,glob;[json.loads(l) for f in glob.glob('$GP/logs/audit-*.jsonl') for l in open(f)]" 2>&1) && ok "audit log intact (every line valid JSON)" || ko "audit log intact" "$bad"

echo "== T13 uninstall removes only the hook"
adm uninstall >/dev/null 2>&1
[ ! -e "$HOOK" ] && [ -x "$HOOKD/01-block-dll" ] && [ -d "$GP/policies/000001" ] && ok "hook removed; PoC, policies kept" || ko "uninstall"
expect_push accept "push works without git-policy hook"

echo
echo "RESULT: $PASS passed, $FAIL failed"
[ "$FAIL" = 0 ]
