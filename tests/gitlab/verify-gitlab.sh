#!/usr/bin/env bash
# End-to-end verification of every phase against a REAL GitLab (the lab in
# lab/, GitLab CE 17.10.5 with Gitaly global hooks), including things only a
# real GitLab can show: GL_* variables from Gitaly, the Gitaly quarantine,
# HTTP/SSH/Web-UI pushes, fork + merge request, wikis, deploy keys, and the
# Jenkins -> gp-ctl -> git-policy-ctl control channel.
#
# Creates fresh groups/projects per run (suffix), reuses test users.
# Usage: tests/gitlab/verify-gitlab.sh [binary]
set -uo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
BIN=$(realpath "${1:-$(ls "$ROOT_DIR"/dist/git-policy-*-linux-amd64 | head -1)}")
GL=http://localhost:8080
TOKEN=$(cat "$ROOT_DIR/lab/.gitlab-token")
S=$(date +%H%M%S)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
PASS=0 FAIL=0 REV=100
ok() { PASS=$((PASS + 1)); printf '  \e[32mPASS\e[0m %s\n' "$1"; }
ko() { FAIL=$((FAIL + 1)); printf '  \e[31mFAIL\e[0m %s\n' "$1"; [ -n "${2:-}" ] && printf '%s\n' "$2" | head -30 | sed 's/^/       /'; return 0; }
info() { printf '  \e[36mINFO\e[0m %s\n' "$1"; }

# ---- helpers ---------------------------------------------------------------------
api() { local m=$1 p=$2; shift 2; curl -sS -X "$m" -H "PRIVATE-TOKEN: $TOKEN" -H 'Content-Type: application/json' "$GL/api/v4$p" "$@"; }
uapi() { local tok=$1 m=$2 p=$3; shift 3; curl -sS -X "$m" -H "PRIVATE-TOKEN: $tok" -H 'Content-Type: application/json' "$GL/api/v4$p" "$@"; }
enc() { python3 -c 'import sys,urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "$1"; }
b64() { printf '%s' "$1" | base64 -w0; }
gpx() { docker exec -u root gitlab /var/opt/gitlab/git-policy/bin/git-policy "$@"; }
# ctl <command...> : run git-policy-ctl exactly as Jenkins does (SSH forced command via gp-ctl)
ctl() { docker exec -i gp-jenkins ssh -q -i /run/secrets/git-policy-ssh-key -o StrictHostKeyChecking=no \
        -o UserKnownHostsFile=/dev/null -o BatchMode=yes gitpolicy-deploy@gp-ctl "$*"; }
audit_tail() { docker exec -u root gitlab sh -c 'cat /var/opt/gitlab/git-policy/logs/audit-*.jsonl' | tail -n "${1:-20}"; }

declare -A UID_OF TOK
user() { # user <name>  -> ensures user exists, creates a fresh PAT
    local u=$1 id
    id=$(api GET "/users?username=$u" | jq -r '.[0].id // empty')
    if [ -z "$id" ]; then
        id=$(api POST /users -d "$(jq -nc --arg u "$u" '{username:$u, name:$u, email:($u+"@lab.local"), password:"Lab-Passw0rd-7g2!", skip_confirmation:true}')" | jq -r .id)
    fi
    UID_OF[$u]=$id
    TOK[$u]=$(api POST "/users/$id/personal_access_tokens" -d "$(jq -nc --arg e "$(date -u -d '+7 days' +%F)" \
        '{name:"gp-verify", scopes:["api","read_repository","write_repository"], expires_at:$e}')" | jq -r .token)
}
group() { # group <path> -> id
    api POST /groups -d "$(jq -nc --arg p "$1" '{name:$p, path:$p, visibility:"private"}')" | jq -r .id
}
member() { api POST "/groups/$1/members" -d "{\"user_id\":${UID_OF[$2]},\"access_level\":${3:-40}}" >/dev/null; }
project() { # project <namespace-id> <path> -> id (initialised with a README on main)
    api POST /projects -d "$(jq -nc --argjson ns "$1" --arg p "$2" '{name:$p, path:$p, namespace_id:$ns, initialize_with_readme:true, default_branch:"main"}')" | jq -r .id
}
url() { echo "http://$1:${TOK[$1]}@localhost:8080/$2.git"; }
# clone <user> <project-path> <dir>
clone() { git clone -q "$(url "$1" "$2")" "$3" 2>/dev/null && git -C "$3" config user.email "$1@lab.local" && git -C "$3" config user.name "$1"; }
# sync <dir> : fast-forward the clone to its upstream (other clones push too)
sync() { (cd "$1" && git fetch -q origin 2>/dev/null && git reset -q --hard "@{upstream}" 2>/dev/null || true); }
add() { (cd "$1" && mkdir -p "$(dirname "$2")" && { if [ -n "${3:-}" ]; then head -c "$3" /dev/urandom; else echo "c $RANDOM"; fi; } > "$2" && git add -- "$2" && git commit -qm "add $2"); }
push() { OUT=$(cd "$1" && shift && git push "$@" 2>&1); }
expect_push() { # expect_push accept|RULE <name> <dir> <push args...>
    local want=$1 name=$2 dir=$3; shift 3
    if push "$dir" "$@"; then got=accept; else got=reject; fi
    if [ "$want" = accept ]; then [ "$got" = accept ] && ok "$name" || ko "$name (rejected)" "$OUT"
    else [ "$got" = reject ] && grep -q "Rule: $want" <<<"$OUT" && ok "$name" || ko "$name (got $got)" "$OUT"; fi
}
# policy [required] : render and apply the lab policy through the ctl channel.
# Called as out=$(policy): a subshell, so the revision comes from a counter file.
policy() {
    local REV; REV=$(( $(cat "$WORK/rev" 2>/dev/null || echo 100) + 1 )); echo "$REV" > "$WORK/rev"
    sed -e "s/@S@/$S/g" -e "s/@REV@/$REV/" -e "s/@EXP@/$(date -u -d '+30 days' +%F)/" -e "s/@REQUIRED@/${1:-false}/" \
        "$ROOT_DIR/tests/gitlab/policy.tmpl.yaml" > "$WORK/policy.yaml"
    ctl "apply --actor-b64=$(b64 jenkins#verify) --reason-b64=$(b64 "lab verify rev $REV")" < "$WORK/policy.yaml"
}
JPW=$(grep '^JENKINS_ADMIN_PASSWORD=' "$ROOT_DIR/lab/.env" | cut -d= -f2-)
# jenkins_build <job> [name=value ...] -> prints "RESULT build#" ; console in $WORK/console
jenkins_build() {
    local job=$1; shift
    local j=(-s -u "admin:$JPW") cookie="$WORK/jcookie" crumb loc q n r i
    crumb=$(curl "${j[@]}" -c "$cookie" http://localhost:8081/crumbIssuer/api/json | jq -r '.crumbRequestField+":"+.crumb')
    local data=(); for kv in "$@"; do data+=(--data-urlencode "$kv"); done
    loc=$(curl "${j[@]}" -b "$cookie" -H "$crumb" -X POST -D - -o /dev/null "http://localhost:8081/job/$job/buildWithParameters" "${data[@]}" | tr -d '\r' | awk 'tolower($1)=="location:"{print $2}')
    if [ -z "$loc" ]; then  # first run: parameters not registered yet
        loc=$(curl "${j[@]}" -b "$cookie" -H "$crumb" -X POST -D - -o /dev/null "http://localhost:8081/job/$job/build" | tr -d '\r' | awk 'tolower($1)=="location:"{print $2}')
    fi
    for i in $(seq 1 60); do n=$(curl "${j[@]}" "${loc}api/json" | jq -r '.executable.number // empty'); [ -n "$n" ] && break; sleep 2; done
    for i in $(seq 1 120); do r=$(curl "${j[@]}" "http://localhost:8081/job/$job/$n/api/json" | jq -r '.result // empty'); [ -n "$r" ] && break; sleep 2; done
    curl "${j[@]}" "http://localhost:8081/job/$job/$n/consoleText" > "$WORK/console"
    echo "$r $n"
}

membership() { # membership <json-users-object>
    printf '{"schema":"git-policy/membership/v1","generated_at":"%s","generator":"verify-gitlab","groups":["contractors-%s"],"users":%s}\n' \
        "$(date -u +%FT%TZ)" "$S" "$1" | docker exec -i -u root gitlab sh -c \
        'cat > /var/opt/gitlab/git-policy/membership/current.json && chown root:git /var/opt/gitlab/git-policy/membership/current.json && chmod 0640 /var/opt/gitlab/git-policy/membership/current.json'
}

echo "== 0. lab reachable"
[ "$(docker inspect -f '{{.State.Health.Status}}' gitlab 2>/dev/null)" = healthy ] && ok "GitLab container healthy" || { ko "GitLab not healthy"; exit 1; }
v=$(api GET /version | jq -r .version); [ "$v" = 17.10.5 ] && ok "GitLab version $v" || ko "unexpected GitLab version $v"
docker exec -u root gitlab grep -q 'custom_hooks_dir = "/var/opt/gitlab/gitaly/custom_hooks"' /var/opt/gitlab/gitaly/config.toml \
    && ok "Gitaly custom_hooks_dir configured like the target lab" || ko "Gitaly custom_hooks_dir"

echo "== reset: start from zero (lab container only)"
if [ "$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project"}}' gitlab)" != git-policy-lab ]; then
    echo "refusing to reset: container 'gitlab' is not the git-policy-lab GitLab"; exit 1
fi
docker exec -u root gitlab sh -c 'chmod -R u+w /var/opt/gitlab/git-policy 2>/dev/null; rm -rf /var/opt/gitlab/git-policy /var/opt/gitlab/gitaly/custom_hooks/pre-receive.d/*'
ok "lab git-policy state wiped (hooks dir emptied)"

echo "== Phase 4: install with install.sh (non-destructive, starts disabled)"
docker exec -u root gitlab sh -c 'mkdir -p /var/opt/gitlab/gitaly/custom_hooks/pre-receive.d && printf "#!/bin/sh\nexit 0\n" > /var/opt/gitlab/gitaly/custom_hooks/pre-receive.d/01-block-dll && chmod 0755 /var/opt/gitlab/gitaly/custom_hooks/pre-receive.d/01-block-dll'
out=$("$ROOT_DIR/install.sh" --container gitlab --binary "$BIN" --dry-run 2>&1) && ok "install.sh --dry-run: all checks pass" || ko "install.sh --dry-run" "$out"
out=$("$ROOT_DIR/install.sh" --container gitlab --binary "$BIN" --actor verify 2>&1) && ok "install.sh installed git-policy" || ko "install.sh" "$out"
docker exec -u root gitlab test -x /var/opt/gitlab/gitaly/custom_hooks/pre-receive.d/01-block-dll && ok "existing hook (PoC stand-in) untouched" || ko "PoC stand-in touched"
st=$(gpx status --json); [ "$(jq -r .hook.state <<<"$st")" = installed ] && ok "status: hook installed" || ko "status hook" "$st"

echo "== setup: users, groups, projects (suffix $S)"
for u in alex bob carol intern1 dev; do user "$u"; done
ok "users alex bob carol intern1 dev with fresh tokens"
FIN=$(group "finance-$S"); OUT_G=$(group "outsourcing-$S"); TRN=$(group "training-$S"); CON=$(group "contractors-$S")
for g in "$FIN" "$OUT_G" "$TRN"; do for u in alex bob carol intern1 dev; do member "$g" "$u" 40; done; done
member "$CON" carol 30
PAY=$(project "$FIN" payment-api); REP=$(project "$FIN" reporting); APP=$(project "$FIN" app)
POR=$(project "$OUT_G" portal); LAB=$(project "$TRN" lab1)
[ -n "$PAY" ] && [ "$PAY" != null ] && ok "groups and projects created (payment-api=$PAY)" || ko "project creation"
F="finance-$S"

echo "== Phase 4: disabled engine lets pushes through and records real Gitaly context"
clone bob "$F/app" "$WORK/app-bob"; add "$WORK/app-bob" src/a.cs
expect_push accept "push accepted while engine disabled" "$WORK/app-bob" origin main
ev=$(audit_tail 5 | grep PUSH_ALLOWED_ENGINE_DISABLED | tail -1)
info "Gitaly context: $(jq -c '{user,gl_id,project,repository,protocol}' <<<"$ev")"
[ "$(jq -r .user <<<"$ev")" = bob ] && ok "GL_USERNAME = bob" || ko "GL_USERNAME" "$ev"
[ "$(jq -r .project <<<"$ev")" = "$F/app" ] && ok "GL_PROJECT_PATH = $F/app" || ko "GL_PROJECT_PATH" "$ev"
[ "$(jq -r .repository <<<"$ev")" = "project-$APP" ] && ok "GL_REPOSITORY = project-$APP" || ko "GL_REPOSITORY" "$ev"
[ "$(jq -r .protocol <<<"$ev")" = http ] && ok "GL_PROTOCOL = http" || ko "GL_PROTOCOL" "$ev"

echo "== Phase 11 preview: Jenkins -> gp-ctl -> git-policy-ctl channel"
out=$(ctl status --json); [ "$(jq -r .engine.state <<<"$out")" = disabled ] && ok "ctl status from the Jenkins container (exit reflects health)" || ko "ctl status" "$out"
refused() { # refused <name> <expected-stderr-fragment> <ctl command>
    local out; if out=$(ctl "$3" 2>&1); then ko "$1 (accepted!)" "$out"
    elif grep -q -- "$2" <<<"$out"; then ok "$1"; else ko "$1 (wrong reason)" "$out"; fi
}
refused "unknown verb refused" "verb not allowed: sh" "sh"
refused "'status;id' refused (no shell)" "verb not allowed: status;id" 'status;id'
refused "\$(id) is not expanded" "verb not allowed" 'status$(id)'
refused "unexpected option refused" "unexpected option --evil" 'status --json --evil=1'
refused "control characters in --reason refused" "contains control characters" "enable --actor-b64=$(b64 x) --reason-b64=$(b64 $'a\nb')"
refused "bad --ttl refused" "invalid --ttl" "disable --actor-b64=$(b64 x) --reason-b64=$(b64 y) --ttl=1d"
refused "path traversal in --name refused" "invalid --name" "retire-hook --actor-b64=$(b64 x) --name=../../etc/passwd"
docker exec gp-jenkins ssh -q -i /run/secrets/git-policy-ssh-key -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
    -o BatchMode=yes -t gitpolicy-deploy@gp-ctl 'docker ps' >/dev/null 2>&1 && ko "docker must not be reachable via ssh" || ok "'docker ps' over ssh refused (forced command)"
out=$(policy 2>&1) && ok "policy applied via ctl (stdin)" || ko "ctl apply" "$out"
read -r res num <<<"$(jenkins_build git-policy-sync-membership)"
[ "$res" = SUCCESS ] && grep -q "membership cache updated: 1 group(s)" "$WORK/console" && ok "Phase 10: Jenkins job git-policy-sync-membership #$num synced from the GitLab API" \
    || ko "Jenkins sync job ($res)" "$(tail -20 "$WORK/console")"
grep -q "$(cat "$ROOT_DIR/lab/.gitlab-token")" "$WORK/console" && ko "API token leaked into the Jenkins console!" || ok "API token not in the Jenkins console"
out=$(ctl "enable --actor-b64=$(b64 jenkins#verify) --reason-b64=$(b64 'lab verify')" 2>&1) && ok "enabled via ctl" || ko "ctl enable" "$out"

echo "== Phase 5: identity rules with real GitLab users (HTTP, SSH, Web)"
clone alex "$F/payment-api" "$WORK/pay-alex"; add "$WORK/pay-alex" src/x.cs
expect_push USER_PUSH_DENIED "HTTP: alex -> payment-api rejected" "$WORK/pay-alex" origin main
grep -q "GL-HOOK-ERR: User: alex" <<<"$OUT" && grep -q "GL-HOOK-ERR: Project: $F/payment-api" <<<"$OUT" && ok "message shows user and project" || ko "message" "$OUT"
clone alex "$F/reporting" "$WORK/rep-alex"; add "$WORK/rep-alex" src/x.cs
expect_push accept "HTTP: alex -> reporting accepted" "$WORK/rep-alex" origin main
ssh-keygen -q -t ed25519 -N '' -f "$WORK/alex_key"
api POST "/users/${UID_OF[alex]}/keys" -d "$(jq -nc --arg k "$(cat "$WORK/alex_key.pub")" '{title:"verify", key:$k}')" >/dev/null
export GIT_SSH_COMMAND="ssh -i $WORK/alex_key -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o IdentitiesOnly=yes"
(cd "$WORK/pay-alex" && git remote add ssh "ssh://git@localhost:2222/$F/payment-api.git")
expect_push USER_PUSH_DENIED "SSH: alex -> payment-api rejected" "$WORK/pay-alex" ssh main
unset GIT_SSH_COMMAND
resp=$(uapi "${TOK[alex]}" POST "/projects/$PAY/repository/commits" -d '{"branch":"main","commit_message":"web edit","actions":[{"action":"create","file_path":"web.txt","content":"x"}]}')
grep -q "USER_PUSH_DENIED" <<<"$resp" && ok "Web/API commit by alex rejected; GitLab returns our message" || ko "web commit" "$resp"
info "Web response: $(jq -r '.message // .' <<<"$resp" | head -c 300)"
ev=$(audit_tail 30 | grep '"protocol":"web"' | tail -1); [ -n "$ev" ] && ok "Web push recorded with GL_PROTOCOL=web" || ko "web protocol not recorded"
clone intern1 "$F/app" "$WORK/app-intern"; add "$WORK/app-intern" src/i.cs
expect_push USER_PUSH_DENIED "intern1 -> finance rejected" "$WORK/app-intern" origin main
clone intern1 "training-$S/lab1" "$WORK/lab-intern"; add "$WORK/lab-intern" src/i.cs
expect_push accept "intern1 -> training accepted" "$WORK/lab-intern" origin main
clone carol "outsourcing-$S/portal" "$WORK/por-carol"; add "$WORK/por-carol" src/c.cs
expect_push accept "contractor carol -> outsourcing accepted (group rule + cache)" "$WORK/por-carol" origin main
clone carol "$F/app" "$WORK/app-carol"; add "$WORK/app-carol" src/c.cs
expect_push GROUP_PUSH_DENIED "contractor carol -> finance rejected" "$WORK/app-carol" origin main
out=$(ctl "explain --user=carol --project=$F/app --groups=contractors-$S"); [ "$(jq -r .verdict <<<"$out")" = REJECT ] && ok "ctl explain agrees (REJECT)" || ko "ctl explain" "$out"

echo "== Phase 6/7: content rules on real pushes (anti-bypass)"
clone dev "$F/app" "$WORK/app-dev"
sync "$WORK/app-dev"; add "$WORK/app-dev" Lib/Mic.Caching.dll; (cd "$WORK/app-dev" && git rm -q Lib/Mic.Caching.dll && git commit -qm rm)
expect_push BLOCKED_EXTENSION "DLL added then deleted in one push rejected (TEST 09)" "$WORK/app-dev" origin main
grep -q "Required action (BLOCKED_EXTENSION): Publish binaries as NuGet packages to Nexus" <<<"$OUT" && ok "Nexus remediation shown" || ko "remediation" "$OUT"
sync "$WORK/app-dev"
add "$WORK/app-dev" "tricks/evil.dll."; expect_push BLOCKED_EXTENSION "NTFS trailing-dot trick rejected" "$WORK/app-dev" origin main
sync "$WORK/app-dev"
add "$WORK/app-dev" src/obj/Debug/x.cache; expect_push BLOCKED_PATH "blocked path **/obj/** rejected" "$WORK/app-dev" origin main
sync "$WORK/app-dev"; (cd "$WORK/app-dev" && git checkout -q -b feature/ok)
add "$WORK/app-dev" src/ok.cs; expect_push accept "clean feature branch accepted" "$WORK/app-dev" origin feature/ok
clone dev "$F/reporting" "$WORK/rep-dev"; add "$WORK/rep-dev" vendor/sdk/Vendor.dll
expect_push accept "narrow exception: vendor/sdk/*.dll on reporting main accepted" "$WORK/rep-dev" origin main
(cd "$WORK/rep-dev" && git checkout -q -b dev)
add "$WORK/rep-dev" vendor/sdk/Other.dll; expect_push BLOCKED_EXTENSION "same path on another branch rejected" "$WORK/rep-dev" origin dev

echo "== Phase 6: fork + merge request cannot smuggle content (real GitLab refs/merge-requests)"
fork=$(uapi "${TOK[dev]}" POST "/projects/$APP/fork" -d "{\"path\":\"app-fork-$S\",\"name\":\"app-fork-$S\"}")
FORK=$(jq -r .id <<<"$fork"); FORK_PATH=$(jq -r .path_with_namespace <<<"$fork")
for i in $(seq 1 30); do [ "$(uapi "${TOK[dev]}" GET "/projects/$FORK" | jq -r .import_status)" = finished ] && break; sleep 2; done
clone dev "$FORK_PATH" "$WORK/fork"; (cd "$WORK/fork" && git checkout -q -b smuggle)
add "$WORK/fork" build/app.pdb
expect_push accept "fork (outside finance): .pdb accepted there (scoped rule)" "$WORK/fork" origin smuggle
mr=$(uapi "${TOK[dev]}" POST "/projects/$FORK/merge_requests" -d "$(jq -nc --argjson t "$APP" '{source_branch:"smuggle", target_branch:"main", target_project_id:$t, title:"smuggle pdb"}')")
IID=$(jq -r .iid <<<"$mr")
for i in $(seq 1 30); do [ "$(uapi "${TOK[dev]}" GET "/projects/$APP/merge_requests/$IID" | jq -r .merge_status)" != checking ] && break; sleep 2; done
resp=$(uapi "${TOK[dev]}" PUT "/projects/$APP/merge_requests/$IID/merge" -d '{}')
state=$(uapi "${TOK[dev]}" GET "/projects/$APP/merge_requests/$IID" | jq -r .state)
[ "$state" != merged ] && ok "MR from fork NOT merged: merge ran our hook and it rejected the .pdb" || ko "fork MR was merged (bypass!)" "$resp"
info "merge response: $(jq -c '.message // .' <<<"$resp" 2>/dev/null | head -c 300)"
audit_tail 40 | grep '"rule":"BLOCKED_EXTENSION"' | grep -q '"file":"build/app.pdb"' && ok "rejection audited with file build/app.pdb" || ko "fork MR rejection not audited"

echo "== Phase 8: size and PE signature on real pushes"
(cd "$WORK/app-dev" && git checkout -q main); sync "$WORK/app-dev"
add "$WORK/app-dev" data/big.bin $((6 * 1048576)); expect_push FILE_TOO_LARGE "6 MiB rejected (limit 5 MiB)" "$WORK/app-dev" origin main
grep -q "Size: 6.0 MiB (limit 5.0 MiB)" <<<"$OUT" && ok "size shown in message" || ko "size message" "$OUT"
sync "$WORK/app-dev"; (cd "$WORK/app-dev" && git checkout -q -b release/1.0)
(cd "$WORK/app-dev" && python3 -c "
import struct,os
b=bytearray(os.urandom(4096)); b[0:2]=b'MZ'; b[0x3C:0x40]=struct.pack('<I',0x80); b[0x80:0x84]=b'PE\0\0'
open('readme.txt','wb').write(b)" && git add readme.txt && git commit -qm pe)
expect_push BLOCKED_SIGNATURE "PE named readme.txt on release/1.0 rejected" "$WORK/app-dev" origin release/1.0

echo "== wiki and deploy key"
api POST "/projects/$APP/wikis" -d '{"title":"home","content":"wiki"}' >/dev/null
clone dev "$F/app.wiki" "$WORK/wiki"
add "$WORK/wiki" notes.pdb; expect_push accept "wiki: scoped .pdb rule not applied (mandatory_only)" "$WORK/wiki" origin HEAD
add "$WORK/wiki" tool.dll; expect_push BLOCKED_EXTENSION "wiki: mandatory .dll rule applied" "$WORK/wiki" origin HEAD
ev=$(audit_tail 10 | grep '"rule":"BLOCKED_EXTENSION"' | tail -1); info "wiki context: $(jq -c '{user,project,repository,protocol}' <<<"$ev")"
ssh-keygen -q -t ed25519 -N '' -f "$WORK/deploy_key"
api POST "/projects/$APP/deploy_keys" -d "$(jq -nc --arg k "$(cat "$WORK/deploy_key.pub")" '{title:"verify-deploy", key:$k, can_push:true}')" >/dev/null
export GIT_SSH_COMMAND="ssh -i $WORK/deploy_key -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o IdentitiesOnly=yes"
(cd "$WORK/app-dev" && git checkout -q main); sync "$WORK/app-dev"; (cd "$WORK/app-dev" && git checkout -q -b deploy-test)
add "$WORK/app-dev" deploy.txt
if push "$WORK/app-dev" "ssh://git@localhost:2222/$F/app.git" deploy-test; then ok "deploy key push accepted"; else info "deploy key push rejected: $(grep GL-HOOK-ERR <<<"$OUT" | head -3)"; fi
unset GIT_SSH_COMMAND
ev=$(audit_tail 10 | grep 'deploy-test' | tail -1)
info "deploy key context: $(jq -c '{user,gl_id,protocol}' <<<"$ev" 2>/dev/null)"

echo "== Phase 10: membership follows GitLab; guards; TEST 17/18"
st=$(ctl status --json); jq -e '.membership | startswith("fresh")' <<<"$st" >/dev/null && ok "status: membership $(jq -r .membership <<<"$st" | cut -c1-40)" || ko "membership status" "$st"
api DELETE "/groups/$CON/members/${UID_OF[carol]}" >/dev/null
read -r res num <<<"$(jenkins_build git-policy-sync-membership)"; [ "$res" = SUCCESS ] && ok "carol removed from contractors in GitLab; sync #$num" || ko "sync after removal" "$(tail -10 "$WORK/console")"
sync "$WORK/app-carol"; add "$WORK/app-carol" src/c2.cs
expect_push accept "carol (no longer a contractor) -> finance accepted" "$WORK/app-carol" origin main
for i in $(seq 1 12); do user "gpbulk$i"; member "$CON" "gpbulk$i" 30; done
read -r res num <<<"$(jenkins_build git-policy-sync-membership)"; [ "$res" = SUCCESS ] && ok "12 bulk members synced (#$num)" || ko "bulk sync" "$(tail -10 "$WORK/console")"
for i in $(seq 1 6); do api DELETE "/groups/$CON/members/${UID_OF[gpbulk$i]}" >/dev/null; done
read -r res num <<<"$(jenkins_build git-policy-sync-membership)"
[ "$res" = FAILURE ] && grep -qE "memberships dropped from [0-9]+ to [0-9]+" "$WORK/console" && ok "guard: ~50% drop refused by the server (#$num FAILURE): $(grep -oE "dropped from [0-9]+ to [0-9]+" "$WORK/console" | head -1)" || ko "drop guard ($res)" "$(tail -10 "$WORK/console")"
read -r res num <<<"$(jenkins_build git-policy-sync-membership FORCE=true)"
[ "$res" = SUCCESS ] && grep -q "\[forced\]" "$WORK/console" && ok "FORCE=true applies it (#$num), audited as forced" || ko "forced sync ($res)" "$(tail -10 "$WORK/console")"
before=$(ctl status --json | jq -r .membership)
out=$("$BIN" sync-membership --gitlab-url http://127.0.0.1:9 --groups "contractors-$S" --timeout 2s -o "$WORK/m.json" 2>&1 <<<"" ) && ko "sync against an unreachable GitLab must fail" || ok "TEST 18: GitLab API down -> sync fails, nothing written"
[ ! -s "$WORK/m.json" ] && ok "no partial membership file produced" || ko "partial file written"
after=$(ctl status --json | jq -r .membership); [ "${before%%,*}" = "${after%%,*}" ] && ok "TEST 18: cache on the server unchanged; group rules keep working from it" || ko "cache changed" "$before / $after"
sync "$WORK/app-carol"; add "$WORK/app-carol" src/c3.cs; clone dave "$F/app" "$WORK/app-dave" 2>/dev/null || true
docker stop gp-jenkins >/dev/null
sync "$WORK/pay-alex"; add "$WORK/pay-alex" src/j.cs
expect_push USER_PUSH_DENIED "TEST 17: Jenkins stopped -> enforcement continues (alex still rejected)" "$WORK/pay-alex" origin main
docker start gp-jenkins >/dev/null; until curl -fsS -o /dev/null http://localhost:8081/login 2>/dev/null; do sleep 3; done; sleep 5
GITLAB_TOKEN=$TOKEN "$BIN" sync-membership --gitlab-url "$GL" --groups "contractors-$S" -o /dev/null --inventory-out "$WORK/inventory.json" 2>/dev/null
sed -e 's/  alex:/  alexx:/' "$WORK/policy.yaml" > "$WORK/typo.yaml"
out=$("$BIN" validate --inventory "$WORK/inventory.json" "$WORK/typo.yaml")
grep -q 'W010  users.alexx: user "alexx" does not exist in GitLab' <<<"$out" && ok "W010: typo 'alexx' flagged using the real GitLab inventory" || ko "W010" "$out"
grep -q 'W010' <<<"$("$BIN" validate --inventory "$WORK/inventory.json" "$WORK/policy.yaml")" && ko "correct policy must have no W010" || ok "correct policy: no W010"

echo "== Phase 4: fail-closed, break-glass, rollback (real GitLab)"
docker exec -u root gitlab sh -c 'cp /var/opt/gitlab/git-policy/state/engine.json /tmp/s.bak && echo "{bad" > /var/opt/gitlab/git-policy/state/engine.json'
sync "$WORK/app-bob"; add "$WORK/app-bob" src/b2.cs; expect_push POLICY_UNAVAILABLE "corrupt state: push rejected (fail closed)" "$WORK/app-bob" origin main
docker exec -u root gitlab touch /var/opt/gitlab/git-policy/state/break-glass
expect_push accept "break-glass: push accepted" "$WORK/app-bob" origin main
docker exec -u root gitlab grep -q "BREAK_GLASS_BYPASS user=bob" /var/opt/gitlab/git-policy/logs/break-glass.log && ok "break-glass push logged" || ko "break-glass log"
docker exec -u root gitlab sh -c 'rm /var/opt/gitlab/git-policy/state/break-glass && cp /tmp/s.bak /var/opt/gitlab/git-policy/state/engine.json'
out=$(policy 2>&1) && ok "new revision applied" || ko "apply" "$out"
out=$(ctl "rollback --actor-b64=$(b64 jenkins#verify) --reason-b64=$(b64 'verify rollback')" 2>&1) && ok "rollback via ctl: $out" || ko "rollback" "$out"
out=$(ctl "disable --actor-b64=$(b64 jenkins#verify) --reason-b64=$(b64 maintenance) --ttl=5s" 2>&1) && ok "disable via ctl with TTL" || ko "disable" "$out"
sleep 6; [ "$(ctl status --json || true; )" ] && [ "$(ctl status --json | jq -r .engine.state)" = "enabled (disable expired)" ] && ok "TTL expired: enforcing again" || ko "TTL expiry"

echo "== Phase 9: accepted-push events, audit.required"
ev=$(audit_tail 60 | grep '"action":"ACCEPT"' | tail -1)
[ -n "$ev" ] && jq -e '.event_id and .host and .schema=="git-policy/audit/v1" and (.refs|length)>0' <<<"$ev" >/dev/null && ok "ACCEPT event with event_id/host/schema/refs" || ko "ACCEPT event" "$ev"
out=$(policy true 2>&1) && ok "policy with audit.required: true" || ko "apply required" "$out"
docker exec -u root gitlab sh -c 'chmod 0440 /var/opt/gitlab/git-policy/logs/audit-$(date -u +%F).jsonl'
sync "$WORK/app-bob"; add "$WORK/app-bob" src/b3.cs; expect_push AUDIT_UNAVAILABLE "audit log not writable + required: push rejected" "$WORK/app-bob" origin main
docker exec -u root gitlab sh -c 'chmod 0640 /var/opt/gitlab/git-policy/logs/audit-$(date -u +%F).jsonl'
expect_push accept "audit writable again: accepted" "$WORK/app-bob" origin main
out=$(ctl "prune-logs --actor-b64=$(b64 jenkins#verify)" 2>&1) && ok "prune-logs via ctl: $(head -1 <<<"$out")" || ko "prune-logs" "$out"
n=$(ctl "logs --date=$(date -u +%F)" | wc -l); [ "$n" -gt 10 ] && ok "logs via ctl ($n events today)" || ko "ctl logs ($n)"
ctl backup > "$WORK/backup.tgz"; tar -tzf "$WORK/backup.tgz" 2>&1 | grep -q policies/ACTIVE && ok "backup via ctl contains policies/ACTIVE" || ko "backup" "$(tar -tzf "$WORK/backup.tgz" 2>&1 | head -5)"

echo "== Phase 7: retire the PoC stand-in via ctl"
out=$(ctl "retire-hook --actor-b64=$(b64 jenkins#verify) --name=01-block-dll" 2>&1) && ok "PoC retired: $out" || ko "retire-hook" "$out"
docker exec -u root gitlab ls /var/opt/gitlab/gitaly/custom_hooks/pre-receive.d | grep -qx 01-block-dll && ko "PoC still present" || ok "only 50-git-policy left in pre-receive.d"

echo "== redeploy through ctl (Jenkins DEPLOY path)"
out=$(ctl "deploy --actor-b64=$(b64 jenkins#verify) --sha256=$(sha256sum "$BIN" | cut -d' ' -f1)" < "$BIN" 2>&1) && ok "deploy via ctl (binary on stdin, sha256 verified)" || ko "ctl deploy" "$out"
out=$(ctl "deploy --actor-b64=$(b64 jenkins#verify) --sha256=$(printf '0%.0s' {1..64})" < "$BIN" 2>&1) && ko "wrong sha256 must be refused" || ok "deploy with wrong sha256 refused"

echo "== latency on the real GitLab"
sync "$WORK/app-bob"; t0=$(date +%s%N); add "$WORK/app-bob" src/perf.cs; push "$WORK/app-bob" origin main; t1=$(date +%s%N)
info "end-to-end push with git-policy enforcing: $(( (t1 - t0) / 1000000 )) ms"
st=$(gpx status); grep -q "HEALTH: OK" <<<"$st" && ok "final status HEALTH: OK" || ko "final status" "$st"

echo
echo "RESULT: $PASS passed, $FAIL failed"
[ "$FAIL" = 0 ]
