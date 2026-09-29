#!/usr/bin/env bash
# Phase 5 integration test: identity and scope enforcement with real
# `git push`. GL_USERNAME / GL_PROJECT_PATH / GL_REPOSITORY are set the way
# Gitaly sets them for server hooks.
#
# Usage: tests/integration/phase5.sh [path/to/git-policy-binary]
set -uo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
BIN=$(realpath "${1:-$(ls "$ROOT_DIR"/dist/git-policy-*-linux-amd64 | head -1)}")
WORK=$(mktemp -d)
trap 'chmod -R u+w "$WORK" 2>/dev/null; rm -rf "$WORK"' EXIT

GP="$WORK/gp"
HOOKD="$WORK/custom_hooks/pre-receive.d"
HOOK="$HOOKD/50-git-policy"
REPO="$WORK/repo.git"
CLONE="$WORK/clone"
PASS=0 FAIL=0

adm() { local sub=$1; shift; "$BIN" admin "$sub" --root "$GP" --hook-path "$HOOK" --actor it-test "$@"; }
st() { "$BIN" status --json --root "$GP" --hook-path "$HOOK"; }
ok() { PASS=$((PASS + 1)); printf '  \e[32mPASS\e[0m %s\n' "$1"; }
ko() { FAIL=$((FAIL + 1)); printf '  \e[31mFAIL\e[0m %s\n' "$1"; [ -n "${2:-}" ] && printf '%s\n' "$2" | sed 's/^/       /'; }
json() { python3 -c "import json,sys; d=json.load(sys.stdin)
for k in sys.argv[1].split('.'): d=d[k]
print(d)" "$1"; }

# membership <generated-at> : writes the cache the Jenkins sync will produce in Phase 10
membership() {
    cat > "$GP/membership/current.json" <<EOF
{"schema":"git-policy/membership/v1","generated_at":"$1","generator":"it-test",
 "groups":["contractors","auditors"],
 "users":{"carol":{"state":"active","groups":["contractors"]},
          "ann":{"state":"active","groups":["auditors"]}}}
EOF
}

# gpush <user|-> <project> <git push args...>  -> exit code, output in $OUT
N=0
commit() { N=$((N + 1)); (cd "$CLONE" && echo "$N" > "f$N.txt" && git add . && git commit -qm "c$N"); }
gpush() {
    local user=$1 project=$2; shift 2
    local envs=(GL_PROJECT_PATH="$project" GL_REPOSITORY=project-1 GL_PROTOCOL=ssh GL_ID=user-1)
    [ "$user" != - ] && envs+=(GL_USERNAME="$user")
    OUT=$(cd "$CLONE" && env "${envs[@]}" git push -q origin "$@" 2>&1)
}
expect() { # expect accept|reject <name> <user> <project> <push args...>   (+ optional EXPECT_GREP)
    local want=$1 name=$2; shift 2
    commit
    if gpush "$@"; then got=accept; else got=reject; fi
    if [ "$got" = "$want" ] && { [ -z "${EXPECT_GREP:-}" ] || grep -q -- "$EXPECT_GREP" <<<"$OUT"; }; then ok "$name"; else ko "$name (got $got)" "$OUT"; fi
    EXPECT_GREP=""
}

cat > "$WORK/policy.yaml" <<'EOF'
apiVersion: git-policy/v1
kind: GitPolicy
metadata: {name: it-phase5, revision: 1}
settings:
  limits: {max_ref_updates: 3}
  messages:
    support: "Help: #devops-help"
mandatory:
  deny_users: ["@unknown", terminated.user]
users:
  alex:
    projects:
      finance/payment-api: {push: deny}
      finance/reporting:   {push: allow}
groups:
  contractors:
    push: deny
    namespaces:
      outsourcing: {push: allow}
exceptions:
  - id: EXC-HOTFIX-1
    rules: [USER_PUSH_DENIED]
    subjects: {users: [alex]}
    scope: {projects: [finance/payment-api], refs: ["refs/heads/hotfix/*"]}
    reason: incident INC-77 hotfix access for alex
    expires: 2099-01-01
EOF
# the exception above expires too far in the future for the default 180-day limit:
sed -i "s/2099-01-01/$(date -u -d '+30 days' +%F)/" "$WORK/policy.yaml"

echo "== setup ($WORK)"
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
git clone -q "$REPO" "$CLONE" 2>/dev/null
git -C "$CLONE" config user.email it@test && git -C "$CLONE" config user.name it
git -C "$CLONE" checkout -q -b main
adm install >/dev/null

echo "== T01 group-deny policy needs the membership cache"
adm apply --reason p5 "$WORK/policy.yaml" >/dev/null 2>&1 && ok "policy applied while engine disabled" || ko "apply"
out=$(adm enable --reason go 2>&1) && ko "enable must be refused without membership cache" "$out" || { grep -q "membership" <<<"$out" && ok "enable refused: no membership cache (would reject every push)" || ko "enable refusal reason" "$out"; }
membership "$(date -u +%FT%TZ)"
adm enable --reason go >/dev/null 2>&1 && ok "enable after deploying the cache" || ko "enable"
[ "$(st | json health)" = OK ] && ok "status OK, membership fresh" || ko "status" "$(st)"

echo "== T02 user rules (TEST 05/06)"
EXPECT_GREP="Rule: USER_PUSH_DENIED" expect reject "alex -> finance/payment-api rejected" alex finance/payment-api HEAD:refs/heads/main
for want in "User: alex" "Project: finance/payment-api" "Ref: refs/heads/main" "Required action (USER_PUSH_DENIED)" "Help: #devops-help"; do
    grep -q "GL-HOOK-ERR: $want" <<<"$OUT" && ok "message shows '$want'" || ko "message shows '$want'" "$OUT"
done
grep -q 'users\[' <<<"$OUT" && ko "message must not expose policy internals" "$OUT" || ok "message hides policy internals"
expect accept "alex -> finance/reporting accepted" alex finance/reporting HEAD:refs/heads/main
expect accept "alex -> unrelated project accepted" alex other/app HEAD:refs/heads/main
expect accept "bob -> finance/payment-api accepted" bob finance/payment-api HEAD:refs/heads/main
EXPECT_GREP="USER_PUSH_DENIED" expect reject "case-insensitive: ALEX -> Finance/Payment-API" ALEX Finance/Payment-API HEAD:refs/heads/main

echo "== T03 exceptions"
expect accept "alex hotfix/* waived by EXC-HOTFIX-1" alex finance/payment-api HEAD:refs/heads/hotfix/1
grep -q '"action":"EXCEPTION_APPLIED".*"exception":"EXC-HOTFIX-1"' "$GP"/logs/audit-*.jsonl && ok "exception use audited" || ko "exception audited"
EXPECT_GREP="USER_PUSH_DENIED" expect reject "exception does not cover other refs" alex finance/payment-api HEAD:refs/heads/feature/x

echo "== T04 group rules via membership cache (TEST 07, 18)"
expect accept "contractor carol -> outsourcing/portal accepted (namespace allow)" carol outsourcing/portal HEAD:refs/heads/main
EXPECT_GREP="Rule: GROUP_PUSH_DENIED" expect reject "contractor carol -> finance/accounting rejected" carol finance/accounting HEAD:refs/heads/main
membership "$(date -u -d '-48 hours' +%FT%TZ)"
EXPECT_GREP="GROUP_PUSH_DENIED" expect reject "expired cache: group allow ignored (restrict-only)" carol outsourcing/portal HEAD:refs/heads/main
[ "$(st | json health)" = WARNING ] && ok "status WARNING: cache expired" || ko "status warning" "$(st)"
membership "$(date -u +%FT%TZ)"

echo "== T05 mandatory denies"
EXPECT_GREP="Rule: MANDATORY_USER_DENIED" expect reject "push without GL_USERNAME rejected (@unknown)" - finance/reporting HEAD:refs/heads/main
EXPECT_GREP="MANDATORY_USER_DENIED" expect reject "terminated.user rejected everywhere" terminated.user sandbox/x HEAD:refs/heads/main

echo "== T06 deletions, multiple refs, limits (TEST 12, 14)"
gpush bob finance/payment-api HEAD:refs/heads/tmp-branch >/dev/null
if gpush alex finance/payment-api :refs/heads/tmp-branch; then ko "alex deleting a branch on payment-api must be rejected" "$OUT"; else grep -q USER_PUSH_DENIED <<<"$OUT" && ok "branch deletion is a push: rejected for alex" || ko "deletion message" "$OUT"; fi
commit
if gpush alex finance/payment-api HEAD:refs/heads/hotfix/2 HEAD:refs/heads/main; then ko "mixed push must be rejected as a whole" "$OUT"
else
    grep -q "Ref: refs/heads/main" <<<"$OUT" && ! grep -q "Ref: refs/heads/hotfix/2" <<<"$OUT" \
        && ok "multi-ref push rejected; only the denied ref is reported" || ko "multi-ref report" "$OUT"
fi
git --git-dir="$REPO" rev-parse -q --verify refs/heads/hotfix/2 >/dev/null && ko "hotfix/2 must not exist after a rejected push" || ok "rejected push updated no ref (atomic)"
commit
if gpush bob other/app HEAD:refs/tags/a HEAD:refs/tags/b HEAD:refs/tags/c HEAD:refs/tags/d; then ko "4 refs > max_ref_updates 3 must be rejected"; else grep -q LIMIT_REF_UPDATES <<<"$OUT" && ok "LIMIT_REF_UPDATES enforced" || ko "limit message" "$OUT"; fi

echo "== T07 audit trail"
line=$(grep '"action":"REJECT"' "$GP"/logs/audit-*.jsonl | grep '"rule":"USER_PUSH_DENIED"' | head -1)
for k in '"user":"alex"' '"project":"finance/payment-api"' '"ref":"refs/heads/main"' '"source":"users[\"alex\"].projects[\"finance/payment-api\"].push"' '"policy_version":"000001"' '"membership":"fresh"' '"commit":"'; do
    grep -qF -- "$k" <<<"$line" && ok "REJECT event has $k" || ko "REJECT event has $k" "$line"
done

echo "== T08 membership cache lost -> fail closed; apply guard"
mv "$GP/membership/current.json" "$WORK/mem.bak"
EXPECT_GREP="MEMBERSHIP_UNAVAILABLE" expect reject "cache missing + group deny rules: rejected" bob other/app HEAD:refs/heads/main
[ "$(st | json health)" = CRITICAL ] && ok "status CRITICAL" || ko "status CRITICAL" "$(st)"
sed 's/revision: 1/revision: 2/' "$WORK/policy.yaml" > "$WORK/p2.yaml"
out=$(adm apply --reason p "$WORK/p2.yaml" 2>&1) && ko "apply must be refused (V042)" "$out" || { grep -q V042 <<<"$out" && ok "apply refused while enforcing without cache (V042)" || ko "V042" "$out"; }
mv "$WORK/mem.bak" "$GP/membership/current.json"

echo "== T09 explain matches the hook"
out=$("$BIN" explain --root "$GP" --user carol --project finance/accounting)
grep -q "Verdict:     REJECT" <<<"$out" && grep -q GROUP_PUSH_DENIED <<<"$out" && ok "explain: carol -> finance/accounting REJECT" || ko "explain" "$out"
out=$("$BIN" explain --root "$GP" --user alex --project finance/payment-api --ref refs/heads/hotfix/9)
grep -q "waived by exception EXC-HOTFIX-1" <<<"$out" && grep -q "Verdict:     ALLOW" <<<"$out" && ok "explain shows the exception" || ko "explain exception" "$out"

echo
echo "RESULT: $PASS passed, $FAIL failed"
[ "$FAIL" = 0 ]
