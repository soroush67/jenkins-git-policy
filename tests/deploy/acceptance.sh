#!/usr/bin/env bash
# Acceptance test of a REAL git-policy deployment (install.sh +
# deploy/jenkins/setup.sh). Unlike tests/gitlab/*, it never resets, stops or
# corrupts anything: it drives the system the way operators and developers do.
#
#   - control channel: pinned host key, forced command, injection refused
#   - Jenkins: STATUS, GitOps apply, VALIDATE, ENABLE, EXPLAIN, EXPORT_LOGS, BACKUP
#   - pushes of a temporary user into a temporary group: accepted / rejected
#     by extension, PE signature, path, size, history traversal, Web/API commit
#   - PRODUCTION four-eyes approval (with a 30-minute DISABLE that is reverted)
#   - no secret in any build console
#
# Temporary objects (group gp-acceptance-*, user gpacc-*, tokens) are removed
# at the end. Run as root on the GitLab host, from the repository:
#   tests/deploy/acceptance.sh --address 192.168.120.128 [--gitlab-url URL] [--jenkins-port 8081]
set -uo pipefail

ADDRESS="" GITLAB_URL="" PORT=8081 CONTAINER=gitlab
while [ $# -gt 0 ]; do
    case "$1" in
        --address) ADDRESS=$2; shift 2 ;;
        --gitlab-url) GITLAB_URL=$2; shift 2 ;;
        --jenkins-port) PORT=$2; shift 2 ;;
        --container) CONTAINER=$2; shift 2 ;;
        *) echo "usage: $0 --address HOST [--gitlab-url URL] [--jenkins-port N] [--container NAME]" >&2; exit 2 ;;
    esac
done
[ -n "$ADDRESS" ] || { echo "--address is required" >&2; exit 2; }
[ "$(id -u)" = 0 ] || { echo "run as root (reads deploy/jenkins/.secrets)" >&2; exit 2; }
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
SEC=$ROOT/deploy/jenkins/.secrets
GL=${GITLAB_URL:-http://$ADDRESS:8080}; GL=${GL%/}
J=http://127.0.0.1:$PORT
S=$(date +%m%d%H%M%S)
GROUP=gp-acceptance-$S USERNAME=gpacc-$S
WORK=$(mktemp -d)
PASS=0 FAIL=0 BUILDS=()
ok() { PASS=$((PASS + 1)); printf '  \e[32mPASS\e[0m %s\n' "$1"; }
ko() { FAIL=$((FAIL + 1)); printf '  \e[31mFAIL\e[0m %s\n' "$1"; [ -n "${2:-}" ] && printf '%s\n' "$2" | tail -20 | sed 's/^/       /'; return 0; }

# ---- tokens: a short-lived admin token for fixtures, created by GitLab itself ----
rails() { docker exec -u root "$CONTAINER" gitlab-rails runner "$1"; }
TOKEN=$(rails "u = User.find_by_username('root')
  t = u.personal_access_tokens.create!(name: 'git-policy-acceptance', scopes: [:api], expires_at: 1.day.from_now)
  print t.token")
[[ "$TOKEN" =~ ^glpat- ]] || { echo "could not create the acceptance token" >&2; exit 1; }
cleanup() {
    api DELETE "/groups/$GROUP" >/dev/null 2>&1
    [ -n "${UID_:-}" ] && api DELETE "/users/$UID_?hard_delete=true" >/dev/null 2>&1
    rails "User.find_by_username('root').personal_access_tokens.active.where(name: 'git-policy-acceptance').each(&:revoke!)" >/dev/null 2>&1
    rm -rf "$WORK"
}
trap cleanup EXIT
api() { local m=$1 p=$2; shift 2; curl -sS -X "$m" -H "PRIVATE-TOKEN: $TOKEN" -H 'Content-Type: application/json' "$GL/api/v4$p" "$@"; }

# ---- ctl channel exactly as Jenkins uses it ----------------------------------------
ctl() { ssh -q -i "$SEC/jenkins_ctl_key" -o BatchMode=yes -o StrictHostKeyChecking=yes \
        -o UserKnownHostsFile="$SEC/known_hosts" "gitpolicy-deploy@$ADDRESS" "$*"; }
engine_enabled() { ctl status --json | jq -r '.engine.enforcing'; }

# ---- Jenkins REST (each user with own cookie + crumb) --------------------------------
declare -A JPW=([admin]="$(cat "$SEC/jenkins-admin-password")" [approver]="$(cat "$SEC/jenkins-approver-password")")
jcurl() { local u=$1; shift; curl -s -u "$u:${JPW[$u]}" -b "$WORK/ck-$u" -c "$WORK/ck-$u" "$@"; }
crumb() { jcurl "$1" "$J/crumbIssuer/api/json" | jq -r '.crumbRequestField+":"+.crumb'; }
start() { # start <user> <job> [k=v ...] -> build number
    local u=$1 job=$2 loc n i; shift 2
    local data=(); for kv in "$@"; do data+=(--data-urlencode "$kv"); done
    loc=$(jcurl "$u" -H "$(crumb "$u")" -X POST -D - -o /dev/null "$J/job/$job/buildWithParameters" "${data[@]}" | tr -d '\r' | awk 'tolower($1)=="location:"{print $2}')
    [ -n "$loc" ] || loc=$(jcurl "$u" -H "$(crumb "$u")" -X POST -D - -o /dev/null "$J/job/$job/build" | tr -d '\r' | awk 'tolower($1)=="location:"{print $2}')
    for i in $(seq 1 90); do n=$(jcurl "$u" "${loc}api/json" | jq -r '.executable.number // empty'); [ -n "$n" ] && break; sleep 2; done
    BUILDS+=("$job/$n"); echo "$n"
}
wait_build() {
    local r i; for i in $(seq 1 300); do r=$(jcurl admin "$J/job/$1/$2/api/json" | jq -r '.result // empty'); [ -n "$r" ] && break; sleep 2; done
    jcurl admin "$J/job/$1/$2/consoleText" > "$WORK/console"; echo "$r"
}
run() { local n; n=$(start admin "$@"); echo "$(wait_build "$1" "$n") $n"; }
wait_input() { local i; for i in $(seq 1 120); do
    [ "$(jcurl admin "$J/job/$1/$2/wfapi/pendingInputActions" | jq 'length' 2>/dev/null)" = 1 ] && return 0; sleep 2; done; return 1; }
approve() { jcurl "$1" -H "$(crumb "$1")" -X POST -o /dev/null "$J/job/$2/$3/input/Approve/proceedEmpty"; }
abort()   { jcurl "$1" -H "$(crumb "$1")" -X POST -o /dev/null "$J/job/$2/$3/input/Approve/abort"; }
expect() { # expect <RESULT-regex> <name> "<result> <n>" [console-regex]
    local got=${3%% *} n=${3##* }
    if [[ "$got" =~ ^($1)$ ]] && { [ -z "${4:-}" ] || grep -qE -- "$4" "$WORK/console"; }; then ok "$2 (#$n $got)"
    else ko "$2 (#$n: $got)" "$(grep -vE '^\[Pipeline\]' "$WORK/console")"; fi
}
artifact() { jcurl admin "$J/job/$1/$2/artifact/$3"; }

echo "== control channel (Jenkins -> ssh gitpolicy-deploy@$ADDRESS -> git-policy-ctl)"
out=$(ctl status 2>&1); rc=$?
[ $rc -le 1 ] && ok "status over the pinned-host-key channel (exit $rc)" || ko "ctl status (exit $rc)" "$out"
ctl 'docker ps' >/dev/null 2>&1 && ko "arbitrary command 'docker ps' was executed" || ok "arbitrary command 'docker ps' refused (forced command)"
ctl 'status;id' >/dev/null 2>&1 && ko "'status;id' accepted" || ok "injection 'status;id' refused"
ssh -q -i "$SEC/jenkins_ctl_key" -o BatchMode=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/dev/null \
    "gitpolicy-deploy@$ADDRESS" status >/dev/null 2>&1 && ko "unpinned host key accepted" || ok "unknown host key refused (StrictHostKeyChecking)"

echo "== Jenkins: operations on TEST"
expect 'SUCCESS' "STATUS" "$(run git-policy ACTION=STATUS ENVIRONMENT=TEST)" 'HEALTH: (OK|WARNING)'
expect 'SUCCESS|UNSTABLE' "GitOps: repository -> TEST" "$(run git-policy-gitops)" 'TEST (now|already) runs'
want=$(api GET "/projects/platform%2Fgit-policy-config/repository/files/test%2Fpolicy.yaml/raw?ref=main" | sha256sum | cut -d' ' -f1)
[ "$(ctl status --json | jq -r '.policy.source_sha256')" = "$want" ] && ok "server runs exactly test/policy.yaml of main" || ko "active policy differs from the repository"
expect 'SUCCESS' "VALIDATE_POLICY" "$(run git-policy ACTION=VALIDATE_POLICY ENVIRONMENT=TEST)" 'RESULT: VALID'
expect 'SUCCESS' "ENABLE" "$(run git-policy ACTION=ENABLE ENVIRONMENT=TEST "REASON=acceptance test $S")"
[ "$(engine_enabled)" = true ] && ok "engine is enforcing" || ko "engine not enforcing"
[ -z "$(ctl status --json | jq -r '.hook.other_hooks[]? // empty' 2>/dev/null | grep -x 01-block-dll)" ] || \
    echo "  NOTE 01-block-dll is still installed: it may reject .dll pushes before git-policy does"

echo "== fixtures: user $USERNAME, group $GROUP, project $GROUP/app"
UID_=$(api POST /users -d "$(jq -nc --arg u "$USERNAME" '{username:$u, name:$u, email:($u+"@acceptance.invalid"), password:("Acc-"+($u|ascii_upcase)+"-9x!"), skip_confirmation:true}')" | jq -r .id)
UTOK=$(api POST "/users/$UID_/personal_access_tokens" -d "$(jq -nc --arg e "$(date -u -d '+1 day' +%F)" '{name:"acceptance", scopes:["api","write_repository"], expires_at:$e}')" | jq -r .token)
GID=$(api POST /groups -d "$(jq -nc --arg p "$GROUP" '{name:$p, path:$p, visibility:"private"}')" | jq -r .id)
api POST "/groups/$GID/members" -d "{\"user_id\":$UID_,\"access_level\":40}" >/dev/null
PID=$(api POST /projects -d "$(jq -nc --argjson ns "$GID" '{name:"app", path:"app", namespace_id:$ns, initialize_with_readme:true, default_branch:"main"}')" | jq -r .id)
[[ "$UID_$GID$PID" =~ ^[0-9]+$ ]] && [[ "$UTOK" =~ ^glpat- ]] && ok "fixtures created" || { ko "fixtures"; exit 1; }
sleep 3
git clone -q "http://$USERNAME:$UTOK@${GL#*://}/$GROUP/app.git" "$WORK/app" 2>/dev/null || { ko "clone"; exit 1; }
git -C "$WORK/app" config user.email "$USERNAME@acceptance.invalid"; git -C "$WORK/app" config user.name "$USERNAME"

# try <expect: accept|RULE> <name> <setup-shell>: commit in a fresh copy of main and push
try() {
    local want=$1 name=$2 out
    (cd "$WORK/app" && git fetch -q origin && git reset -q --hard origin/main && eval "$3") >/dev/null 2>&1
    out=$(cd "$WORK/app" && git push origin HEAD:main 2>&1); local rc=$?
    if [ "$want" = accept ]; then [ $rc = 0 ] && ok "$name: accepted" || ko "$name: rejected" "$out"
    else [ $rc != 0 ] && grep -q "Rule: $want" <<<"$out" && ok "$name: rejected ($want)" || ko "$name: expected $want (rc $rc)" "$out"; fi
}
pe() { python3 -c "import sys; b=bytearray(512); b[0:2]=b'MZ'; b[0x3c:0x40]=(0x80).to_bytes(4,'little'); b[0x80:0x84]=b'PE\0\0'; sys.stdout.buffer.write(bytes(b))" > "$1"; }
export -f pe

echo "== pushes as $USERNAME"
try accept "ordinary source file" 'echo "class A {}" > A.cs && git add A.cs && git commit -qm src'
try BLOCKED_EXTENSION "Lib/Foo.dll (mandatory)" 'mkdir -p Lib && echo x > Lib/Foo.dll && git add Lib && git commit -qm dll'
try BLOCKED_EXTENSION "SETUP.EXE (case)" 'echo x > SETUP.EXE && git add SETUP.EXE && git commit -qm exe'
try BLOCKED_SIGNATURE "PE file renamed to notes.txt" 'pe notes.txt && git add notes.txt && git commit -qm pe'
try BLOCKED_EXTENSION "App.pdb (defaults)" 'echo x > App.pdb && git add App.pdb && git commit -qm pdb'
try BLOCKED_PATH "src/obj/cache.bin (path)" 'mkdir -p src/obj && echo x > src/obj/cache.bin && git add src && git commit -qm obj'
try FILE_TOO_LARGE "21MiB file (default cap 20MiB)" 'head -c 22020096 /dev/urandom > big.bin && git add big.bin && git commit -qm big'
try BLOCKED_EXTENSION "dll added then deleted in the same push (history)" \
    'echo x > hidden.dll && git add hidden.dll && git commit -qm add && git rm -q hidden.dll && git commit -qm del'
resp=$(curl -sS -X POST -H "PRIVATE-TOKEN: $UTOK" -H 'Content-Type: application/json' "$GL/api/v4/projects/$PID/repository/commits" \
    -d '{"branch":"main","commit_message":"web","actions":[{"action":"create","file_path":"web/Web.dll","content":"x"}]}')
grep -q BLOCKED_EXTENSION <<<"$resp" && ok "Web/API commit of a .dll: rejected (BLOCKED_EXTENSION)" || ko "Web/API commit not rejected" "$resp"

echo "== Jenkins: EXPLAIN, EXPORT_LOGS, BACKUP"
expect 'SUCCESS' "EXPLAIN $USERNAME @ $GROUP/app" "$(run git-policy ACTION=EXPLAIN ENVIRONMENT=TEST "EXPLAIN_USER=$USERNAME" "EXPLAIN_PROJECT=$GROUP/app")" 'verdict: ALLOW'
r=$(run git-policy ACTION=EXPORT_LOGS ENVIRONMENT=TEST); expect 'SUCCESS' "EXPORT_LOGS" "$r"
artifact git-policy "${r##* }" "audit-TEST-$(date -u +%F).jsonl" > "$WORK/audit.jsonl"
for rule in BLOCKED_EXTENSION BLOCKED_SIGNATURE BLOCKED_PATH FILE_TOO_LARGE; do
    jq -e --arg u "$USERNAME" --arg r "$rule" 'select(.user==$u and .action=="REJECT" and ((.rule==$r) or (.rules // [] | index($r))))' "$WORK/audit.jsonl" >/dev/null 2>&1 \
        && ok "audit log: REJECT $rule by $USERNAME" || ko "audit log: no REJECT $rule for $USERNAME"
done
r=$(run git-policy ACTION=BACKUP_POLICY ENVIRONMENT=TEST); expect 'SUCCESS' "BACKUP_POLICY" "$r"
jcurl admin "$J/job/git-policy/${r##* }/api/json" | jq -e '.artifacts[].fileName | select(endswith(".tgz"))' >/dev/null && ok "backup archive stored in Jenkins" || ko "no backup artifact"

echo "== PRODUCTION: four-eyes approval (DISABLE for 30m, reverted right after)"
n=$(start admin git-policy ACTION=DISABLE ENVIRONMENT=PRODUCTION DISABLE_TTL=30m "REASON=acceptance four-eyes $S")
wait_input git-policy "$n" && ok "DISABLE waits for approval (#$n)" || ko "no approval step"
approve admin git-policy "$n"
expect 'FAILURE' "requester approving own request is refused" "$(wait_build git-policy "$n") $n" 'four-eyes rule'
[ "$(engine_enabled)" = true ] && ok "engine still enforcing" || ko "engine was disabled without a second person"
n=$(start admin git-policy ACTION=DISABLE ENVIRONMENT=PRODUCTION DISABLE_TTL=30m "REASON=acceptance four-eyes $S")
wait_input git-policy "$n"; abort approver git-policy "$n"
expect 'ABORTED' "approver rejects -> nothing happens" "$(wait_build git-policy "$n") $n"
n=$(start admin git-policy ACTION=DISABLE ENVIRONMENT=PRODUCTION DISABLE_TTL=30m "REASON=acceptance four-eyes $S")
wait_input git-policy "$n"; approve approver git-policy "$n"
expect 'SUCCESS' "approver approves -> disabled" "$(wait_build git-policy "$n") $n" 'approved by approver'
[ "$(engine_enabled)" = false ] && ok "engine disabled (auto re-enable in 30m)" || ko "engine not disabled"
expect 'SUCCESS' "ENABLE again" "$(run git-policy ACTION=ENABLE ENVIRONMENT=TEST "REASON=acceptance four-eyes done $S")"
[ "$(engine_enabled)" = true ] && ok "engine enforcing again" || ko "engine not re-enabled"

echo "== secrets"
leak=0
for b in "${BUILDS[@]}"; do
    c=$(jcurl admin "$J/job/${b%/*}/${b##*/}/consoleText")
    for f in gitlab-api-token jenkins-admin-password jenkins-approver-password; do grep -qF "$(cat "$SEC/$f")" <<<"$c" && leak=1; done
    grep -q 'PRIVATE KEY' <<<"$c" && leak=1
done
[ "$leak" = 0 ] && ok "no token, password or private key in ${#BUILDS[@]} build consoles" || ko "secret found in a build console"

echo
echo "RESULT: $PASS passed, $FAIL failed   (temporary user/group/tokens are being removed)"
[ "$FAIL" = 0 ]
