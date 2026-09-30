#!/usr/bin/env bash
# Create the GitOps policy repository platform/git-policy-config in the lab
# GitLab and push the seed content (lab/policy-repo-seed/). Idempotent: an
# existing repository is left untouched.
# deploy/jenkins/setup.sh reuses it with GITLAB_URL, GITLAB_TOKEN (api scope)
# and SEED_DIR (absolute path) set.
set -euo pipefail
cd "$(dirname "$0")"
GL=${GITLAB_URL:-http://localhost:8080}
TOKEN=${GITLAB_TOKEN:-$(cat .gitlab-token)}
SEED=${SEED_DIR:-policy-repo-seed}
api() { local m=$1 p=$2; shift 2; curl -sS -X "$m" -H "PRIVATE-TOKEN: $TOKEN" -H 'Content-Type: application/json' "$GL/api/v4$p" "$@"; }

pid=$(api GET /projects/platform%2Fgit-policy-config | jq -r '.id // empty')
if [ -n "$pid" ] && [ "$(api GET "/projects/$pid/repository/commits" | jq -r 'if type=="array" then length else 0 end')" != 0 ]; then
    echo "platform/git-policy-config already has content (left untouched)"; exit 0
fi
if [ -z "$pid" ]; then
    gid=$(api GET /groups/platform | jq -r '.id // empty')
    [ -n "$gid" ] || gid=$(api POST /groups -d '{"name":"platform","path":"platform","visibility":"private"}' | jq -r .id)
    pid=$(api POST /projects -d "{\"name\":\"git-policy-config\",\"path\":\"git-policy-config\",\"namespace_id\":$gid,\"default_branch\":\"main\",\"description\":\"GitOps source of truth for git-policy\"}" | jq -r .id)
fi
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
cp -r "$SEED/." "$tmp/"
git -C "$tmp" init -q -b main
git -C "$tmp" -c user.name=platform -c user.email=platform@lab.local add .
git -C "$tmp" -c user.name=platform -c user.email=platform@lab.local commit -qm "Initial git-policy configuration"
# The push itself goes through git-policy like any other push.
git -C "$tmp" push -q "${GL%%://*}://root:$TOKEN@${GL#*://}/platform/git-policy-config.git" main
api PUT "/projects/$pid" -d '{"only_allow_merge_if_all_discussions_are_resolved":true}' >/dev/null
echo "created platform/git-policy-config (project $pid): $GL/platform/git-policy-config"
