#!/usr/bin/env bash
# Phase 11 end-to-end: the Jenkins control plane (jenkins/Jenkinsfile and
# Jenkinsfile.gitops) driving git-policy on the real lab GitLab, exactly as an
# operator would: every action through Jenkins, GitOps from the policy repo,
# PRODUCTION approval with the four-eyes rule, and no secrets in consoles.
#
# Usage: tests/gitlab/verify-jenkins.sh      (lab must be up: lab/up.sh)
set -uo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
BIN=$(realpath "$(ls "$ROOT_DIR"/dist/git-policy-*-linux-amd64 | head -1)")
GL=http://localhost:8080
J=http://localhost:8081
TOKEN=$(cat "$ROOT_DIR/lab/.gitlab-token")
JPW=$(grep '^JENKINS_ADMIN_PASSWORD=' "$ROOT_DIR/lab/.env" | cut -d= -f2-)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
PASS=0 FAIL=0
ok() { PASS=$((PASS + 1)); printf '  \e[32mPASS\e[0m %s\n' "$1"; }
ko() { FAIL=$((FAIL + 1)); printf '  \e[31mFAIL\e[0m %s\n' "$1"; [ -n "${2:-}" ] && printf '%s\n' "$2" | tail -25 | sed 's/^/       /'; return 0; }
gpx() { docker exec -u root gitlab /var/opt/gitlab/git-policy/bin/git-policy "$@"; }

# ---- Jenkins REST helpers (each user has its own cookie + crumb) -------------
jcurl() { local u=$1; shift; local pw=$JPW; curl -s -u "$u:$pw" -b "$WORK/ck-$u" -c "$WORK/ck-$u" "$@"; }
crumb() { jcurl "$1" "$J/crumbIssuer/api/json" | jq -r '.crumbRequestField+":"+.crumb'; }
# start <user> <job> [k=v ...] -> build number (does not wait)
start() {
    local u=$1 job=$2; shift 2
    local data=() loc n i
    for kv in "$@"; do data+=(--data-urlencode "$kv"); done
    loc=$(jcurl "$u" -H "$(crumb "$u")" -X POST -D - -o /dev/null "$J/job/$job/buildWithParameters" "${data[@]}" | tr -d '\r' | awk 'tolower($1)=="location:"{print $2}')
    [ -n "$loc" ] || loc=$(jcurl "$u" -H "$(crumb "$u")" -X POST -D - -o /dev/null "$J/job/$job/build" | tr -d '\r' | awk 'tolower($1)=="location:"{print $2}')
    for i in $(seq 1 90); do n=$(jcurl "$u" "${loc}api/json" | jq -r '.executable.number // empty'); [ -n "$n" ] && break; sleep 2; done
    echo "$n"
}
# wait <job> <n> -> result; console saved to $WORK/console
wait_build() {
    local job=$1 n=$2 r i
    for i in $(seq 1 300); do r=$(jcurl admin "$J/job/$job/$n/api/json" | jq -r '.result // empty'); [ -n "$r" ] && break; sleep 2; done
    jcurl admin "$J/job/$job/$n/consoleText" > "$WORK/console"
    echo "$r"
}
# run <job> [k=v ...] -> "RESULT N"
run() { local n; n=$(start admin "$@"); echo "$(wait_build "$1" "$n") $n"; }
# wait_input <job> <n> : until the build waits for approval
wait_input() {
    local i; for i in $(seq 1 120); do
        [ "$(jcurl admin "$J/job/$1/$2/wfapi/pendingInputActions" | jq 'length' 2>/dev/null)" = 1 ] && return 0; sleep 2
    done; return 1
}
approve() { jcurl "$1" -H "$(crumb "$1")" -X POST -o /dev/null -w '%{http_code}' "$J/job/$2/$3/input/Approve/proceedEmpty"; }
abort()   { jcurl "$1" -H "$(crumb "$1")" -X POST -o /dev/null -w '%{http_code}' "$J/job/$2/$3/input/Approve/abort"; }
artifacts() { jcurl admin "$J/job/$1/$2/api/json" | jq -r '.artifacts[].fileName'; }
expect() { # expect <RESULT> <name> <result-line> [console-grep]
    local want=$1 name=$2 got=${3%% *} n=${3##* }
    if [ "$got" = "$want" ] && { [ -z "${4:-}" ] || grep -qE -- "$4" "$WORK/console"; }; then ok "$name (#$n)"
    else ko "$name (#$n: $got, wanted $want)" "$(grep -vE '^\[Pipeline\]' "$WORK/console")"; fi
}
active_sha() { gpx status --json | jq -r '.policy.source_sha256 // ""'; }

# Note: git-policy-gitops also polls the repository every 2 minutes, so a
# commit may already be applied by the poller when a test build runs; the
# assertions accept both and always check the resulting server state.

# ---- policy repository helpers --------------------------------------------------
REPO_URL="http://root:$TOKEN@localhost:8080/platform/git-policy-config.git"
repo_clone() { rm -rf "$WORK/repo"; git clone -q "$REPO_URL" "$WORK/repo" 2>/dev/null && git -C "$WORK/repo" config user.email platform@lab.local && git -C "$WORK/repo" config user.name platform; }
# bump <file> : increase metadata.revision
bump() { python3 - "$1" <<'PY'
import re,sys
p=sys.argv[1]; s=open(p).read()
s=re.sub(r"revision: (\d+)", lambda m: "revision: %d" % (int(m.group(1))+1), s, count=1)
open(p,"w").write(s)
PY
}
repo_push() { (cd "$WORK/repo" && git add -A && git commit -qm "$1" && git push -q origin main 2>&1); }

echo "== 0. start from a clean engine (lab only) and the seeded policy repository"
[ "$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project"}}' gitlab)" = git-policy-lab ] || { echo "not the lab GitLab"; exit 1; }
docker exec -u root gitlab sh -c 'chmod -R u+w /var/opt/gitlab/git-policy 2>/dev/null; rm -rf /var/opt/gitlab/git-policy /var/opt/gitlab/gitaly/custom_hooks/pre-receive.d/*'
"$ROOT_DIR/install.sh" --container gitlab --binary "$BIN" --actor verify-jenkins >/dev/null 2>&1 && ok "git-policy installed fresh (engine disabled)" || ko "install"
"$ROOT_DIR/lab/seed-policy-repo.sh" >/dev/null && ok "policy repository platform/git-policy-config present" || ko "seed repo"
# Deterministic start: reset the policy files to the seed content (earlier
# runs leave their own commits behind; the engine was wiped, so revision 1 is fine).
repo_clone
cp "$ROOT_DIR/lab/policy-repo-seed/test/policy.yaml" "$WORK/repo/test/policy.yaml"
cp "$ROOT_DIR/lab/policy-repo-seed/production/policy.yaml" "$WORK/repo/production/policy.yaml"
if [ -n "$(git -C "$WORK/repo" status --porcelain)" ]; then repo_push "reset to seed content (verify-jenkins)" >/dev/null && ok "policy files reset to the seed content"; else ok "policy files already at seed content"; fi
for job in git-policy git-policy-gitops; do
    [ "$(jcurl admin -o /dev/null -w '%{http_code}' "$J/job/$job/api/json")" = 200 ] && ok "Jenkins job $job exists" || ko "job $job missing"
done

echo "== 1. read-only actions"
expect SUCCESS "STATUS (TEST)" "$(run git-policy ACTION=STATUS ENVIRONMENT=TEST)" "Engine: +disabled"
expect SUCCESS "VALIDATE_POLICY (TEST) from the repository" "$(run git-policy ACTION=VALIDATE_POLICY ENVIRONMENT=TEST)" "RESULT: VALID"
n=${PASS}; artifacts git-policy "$(jcurl admin "$J/job/git-policy/lastBuild/api/json" | jq -r .number)" | grep -q validate.json && ok "validation report archived" || ko "validate.json artifact"

echo "== 2. guards in the pipeline"
expect FAILURE "ENABLE without REASON refused" "$(run git-policy ACTION=ENABLE ENVIRONMENT=TEST)" "REASON is required"
expect FAILURE "invalid EXPLAIN_PROJECT refused before anything runs" "$(run git-policy ACTION=EXPLAIN ENVIRONMENT=TEST 'EXPLAIN_PROJECT=a;rm -rf /')" "invalid EXPLAIN_PROJECT"

echo "== 3. UPDATE_POLICY + ENABLE on TEST"
expect SUCCESS "UPDATE_POLICY (TEST): diff, apply, verify sha256" "$(run git-policy ACTION=UPDATE_POLICY ENVIRONMENT=TEST 'REASON=initial TEST policy from the repository')" "verified: active policy sha256|already equals this file"
repo_clone
want=$(sha256sum "$WORK/repo/test/policy.yaml" | cut -d' ' -f1)
[ "$(active_sha)" = "$want" ] && ok "server runs exactly test/policy.yaml ($want)" || ko "active sha mismatch"
expect SUCCESS "ENABLE (TEST)" "$(run git-policy ACTION=ENABLE ENVIRONMENT=TEST 'REASON=go live on TEST after review')"
gpx status --json | jq -e '.engine.enforcing' >/dev/null && ok "engine enforcing" || ko "engine not enforcing"

echo "== 4. GitOps: push to the policy repository -> TEST updated by git-policy-gitops"
repo_clone
sed -i 's/  blocked_extensions: \[pdb, nupkg\]/  blocked_extensions: [pdb, nupkg, iso]/' "$WORK/repo/test/policy.yaml"; bump "$WORK/repo/test/policy.yaml"
out=$(repo_push "block .iso on TEST") && ok "change pushed to the policy repository (git push through git-policy itself)" || ko "repo push" "$out"
expect UNSTABLE "gitops: validated, applied to TEST (UNSTABLE = PRODUCTION drift reported)" "$(run git-policy-gitops)" "TEST (now|already) runs"
grep -q "PRODUCTION drift" "$WORK/console" && ok "PRODUCTION drift reported, not applied" || ko "drift message"
[ "$(active_sha)" = "$(sha256sum "$WORK/repo/test/policy.yaml" | cut -d' ' -f1)" ] && ok "TEST runs the new commit" || ko "gitops did not apply"
gpx explain --user dev --project team/app | grep -q 'iso (defaults)' && ok "new rule effective (.iso blocked)" || ko "iso not blocked"
expect UNSTABLE "gitops again: nothing to do (idempotent)" "$(run git-policy-gitops)" "TEST already runs this policy"

echo "== 5. GitOps safety: invalid policy / missing revision bump never reach the server"
before=$(active_sha)
sed -i 's/max_file_size: 20MiB/max_file_size: 20MB/' "$WORK/repo/test/policy.yaml"; bump "$WORK/repo/test/policy.yaml"
repo_push "broken size unit" >/dev/null
expect FAILURE "gitops: invalid policy (20MB) stops the build" "$(run git-policy-gitops)" "V005"
[ "$(active_sha)" = "$before" ] && ok "server still runs the previous valid policy" || ko "invalid policy applied!"
sed -i 's/max_file_size: 20MB/max_file_size: 20MiB/' "$WORK/repo/test/policy.yaml"   # fixed, but revision NOT bumped again
repo_push "fix size unit" >/dev/null
out=$(run git-policy-gitops); # this revision is new (the broken one was never applied) -> applies
expect UNSTABLE "gitops: fixed commit applied" "$out" "TEST (now|already) runs"
sed -i 's/  max_file_size: 20MiB/  max_file_size: 15MiB/' "$WORK/repo/test/policy.yaml"   # change WITHOUT revision bump
repo_push "change without revision bump" >/dev/null
expect FAILURE "gitops: change without revision bump refused (V040)" "$(run git-policy-gitops)" "V040"
bump "$WORK/repo/test/policy.yaml"; repo_push "bump revision" >/dev/null
expect UNSTABLE "gitops: after bumping the revision it applies" "$(run git-policy-gitops)" "TEST (now|already) runs"

echo "== 6. PRODUCTION: approval by a second person (four-eyes)"
# Lab only: TEST and PRODUCTION share one GitLab/engine here, so the production
# file must carry a revision above whatever TEST applied (separate servers in
# real life have separate revision histories).
arev=$(gpx status --json | jq -r '.policy.revision')
python3 - "$WORK/repo/production/policy.yaml" "$arev" <<'PY'
import re,sys
p,rev=sys.argv[1],int(sys.argv[2]); s=open(p).read()
open(p,"w").write(re.sub(r"revision: \d+", "revision: %d" % (rev+1), s, count=1))
PY
repo_push "production revision $((arev + 1)) (lab: shared engine)" >/dev/null && ok "production/policy.yaml at revision $((arev + 1))" || ko "production revision push"
n=$(start admin git-policy ACTION=UPDATE_POLICY ENVIRONMENT=PRODUCTION 'REASON=promote production policy (audit mode)')
wait_input git-policy "$n" && ok "UPDATE_POLICY (PRODUCTION) #$n waits for approval" || ko "no approval prompt"
code=$(approve admin git-policy "$n")
r=$(wait_build git-policy "$n")
[ "$r" = FAILURE ] && grep -q "four-eyes rule: admin cannot approve their own request" "$WORK/console" && ok "requester approving own request -> FAILURE (four-eyes)" || ko "four-eyes ($r)" "$(tail -20 "$WORK/console")"
[ "$(active_sha)" != "$(sha256sum "$WORK/repo/production/policy.yaml" | cut -d' ' -f1)" ] && ok "nothing applied" || ko "applied without valid approval!"
n=$(start admin git-policy ACTION=UPDATE_POLICY ENVIRONMENT=PRODUCTION 'REASON=promote production policy (audit mode)')
wait_input git-policy "$n"
code=$(approve approver git-policy "$n")
expect SUCCESS "approved by 'approver' -> applied and verified" "$(wait_build git-policy "$n") $n" "approved by approver"
docker exec -u root gitlab sh -c 'grep POLICY_UPDATED /var/opt/gitlab/git-policy/logs/audit-*.jsonl | tail -1' | grep -q 'approved-by:approver' \
    && ok "audit log: actor includes requester, commit and approved-by:approver" || ko "audit actor"
n=$(start admin git-policy ACTION=DISABLE ENVIRONMENT=PRODUCTION 'REASON=maintenance window test' DISABLE_TTL=30m)
wait_input git-policy "$n"; code=$(abort approver git-policy "$n")
r=$(wait_build git-policy "$n"); [ "$r" = ABORTED ] && ok "DISABLE (PRODUCTION) rejected by the approver -> ABORTED" || ko "abort ($r)"
gpx status --json | jq -e '.engine.enforcing' >/dev/null && ok "engine still enforcing" || ko "engine was disabled!"
n=$(start admin git-policy ACTION=DISABLE ENVIRONMENT=PRODUCTION 'REASON=maintenance window test' DISABLE_TTL=30m)
wait_input git-policy "$n"; code=$(approve approver git-policy "$n")
expect SUCCESS "DISABLE (PRODUCTION) approved -> disabled for 30m" "$(wait_build git-policy "$n") $n" "DISABLED until"
expect SUCCESS "ENABLE (no approval needed: it restores protection)" "$(run git-policy ACTION=ENABLE ENVIRONMENT=PRODUCTION 'REASON=maintenance finished')"

echo "== 7. remaining actions"
expect SUCCESS "ROLLBACK_POLICY (TEST, to previous)" "$(run git-policy ACTION=ROLLBACK_POLICY ENVIRONMENT=TEST 'REASON=rollback drill on TEST')" "rolled back"
res=$(run git-policy ACTION=BACKUP_POLICY ENVIRONMENT=TEST); expect SUCCESS "BACKUP_POLICY" "$res"
artifacts git-policy "${res##* }" | grep -q '^git-policy-backup-TEST-.*\.tgz$' && ok "backup .tgz archived in Jenkins" || ko "backup artifact"
expect SUCCESS "EXPLAIN" "$(run git-policy ACTION=EXPLAIN ENVIRONMENT=TEST EXPLAIN_USER=dev EXPLAIN_PROJECT=team/app)" "verdict: ALLOW"
expect SUCCESS "SYNC_GROUP_MEMBERSHIP (policy uses no groups)" "$(run git-policy ACTION=SYNC_GROUP_MEMBERSHIP ENVIRONMENT=TEST 'REASON=scheduled membership sync')" "nothing to sync"
res=$(run git-policy ACTION=EXPORT_LOGS ENVIRONMENT=TEST); expect SUCCESS "EXPORT_LOGS" "$res" "events"
artifacts git-policy "${res##* }" | grep -q '^audit-TEST-' && ok "audit log archived" || ko "audit artifact"
expect SUCCESS "PRUNE_LOGS" "$(run git-policy ACTION=PRUNE_LOGS ENVIRONMENT=TEST 'REASON=retention housekeeping')" "pruned"
expect SUCCESS "DEPLOY (TEST): binary with sha256 through the ctl channel" "$(run git-policy ACTION=DEPLOY ENVIRONMENT=TEST 'REASON=redeploy same version')" "installed into"
expect FAILURE "RETIRE_HOOK for a hook that does not exist fails cleanly" "$(run git-policy ACTION=RETIRE_HOOK ENVIRONMENT=TEST HOOK_NAME=no-such-hook 'REASON=cleanup test hook')" "no such file"

echo "== 8. no secrets in any console"
leak=0
for job in git-policy git-policy-gitops; do
    last=$(jcurl admin "$J/job/$job/lastBuild/api/json" | jq -r .number)
    for n in $(seq 1 "$last"); do
        c=$(jcurl admin "$J/job/$job/$n/consoleText")
        grep -q "$TOKEN" <<<"$c" && { leak=1; echo "token in $job #$n"; }
        grep -q "PRIVATE KEY" <<<"$c" && { leak=1; echo "key in $job #$n"; }
    done
done
[ "$leak" = 0 ] && ok "no API token or private key in any build log" || ko "secret leaked"

echo
echo "RESULT: $PASS passed, $FAIL failed"
[ "$FAIL" = 0 ]
