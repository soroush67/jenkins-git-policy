#!/usr/bin/env bash
# Set up the git-policy control plane on the GitLab Docker host (single-host
# deployment): the git-policy-ctl SSH channel, a GitLab read-only token, the
# GitOps policy repository and Jenkins (a container on this host).
#
# Run as root from a copy of the repository, AFTER ./install.sh:
#   sudo deploy/jenkins/setup.sh --address 192.168.120.128 [--dry-run]
#
# Idempotent: secrets are generated once into deploy/jenkins/.secrets (root,
# 0700) and reused; nothing secret is printed. Passwords are read with:
#   sudo cat deploy/jenkins/.secrets/jenkins-admin-password
set -euo pipefail

ADDRESS="" GITLAB_URL="" CONTAINER=gitlab PORT=8081 DRY=0
JENKINS_IMAGE=jenkins/jenkins:lts-jdk17
usage() {
    cat <<EOF
Usage: $0 --address HOST [options]

  --address HOST        address of THIS host as Jenkins (in its container) reaches it:
                        used for the ctl SSH channel and the default GitLab URL
  --gitlab-url URL      GitLab base URL (default: http://HOST:8080)
  --container NAME      GitLab container (default: gitlab)
  --jenkins-port N      published Jenkins port (default: 8081)
  --jenkins-image IMG   Jenkins base image (default: $JENKINS_IMAGE)
  --dry-run             run the checks and show the plan, change nothing
EOF
}
while [ $# -gt 0 ]; do
    case "$1" in
        --address) ADDRESS=$2; shift 2 ;;
        --gitlab-url) GITLAB_URL=$2; shift 2 ;;
        --container) CONTAINER=$2; shift 2 ;;
        --jenkins-port) PORT=$2; shift 2 ;;
        --jenkins-image) JENKINS_IMAGE=$2; shift 2 ;;
        --dry-run) DRY=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
    esac
done

step() { printf '\n==> %s\n' "$*"; }
info() { printf '    %s\n' "$*"; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/../.." && pwd)
SEC=$HERE/.secrets
[ "$(id -u)" = 0 ] || die "run as root"
[[ "$ADDRESS" =~ ^[A-Za-z0-9.-]+$ ]] || die "--address HOST is required (IP or DNS name of this host)"
[[ "$PORT" =~ ^[0-9]{2,5}$ ]] || die "invalid --jenkins-port"
[[ "$CONTAINER" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*$ ]] || die "invalid container name"
GITLAB_URL=${GITLAB_URL:-http://$ADDRESS:8080}
GITLAB_URL=${GITLAB_URL%/}
[[ "$GITLAB_URL" =~ ^https?://[A-Za-z0-9.:-]+$ ]] || die "invalid --gitlab-url (scheme://host[:port], no path)"
if docker compose version >/dev/null 2>&1; then DC=(docker compose); else DC=(docker-compose); fi
DC+=(-f "$HERE/docker-compose.yml" --env-file "$HERE/.env")
cexec() { docker exec -u root "$CONTAINER" "$@"; }
gen() { head -c 64 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c "$1"; }

step "1/9 checks"
for c in docker ssh-keygen ssh-keyscan ssh curl jq git python3; do command -v "$c" >/dev/null || die "$c not found"; done
[ "$(docker inspect -f '{{.State.Running}}' "$CONTAINER" 2>/dev/null)" = true ] || die "container $CONTAINER is not running"
cexec test -x /var/opt/gitlab/git-policy/bin/git-policy || die "git-policy is not installed in $CONTAINER: run ./install.sh first"
BIN=$(ls "$ROOT"/dist/git-policy-*-linux-amd64 2>/dev/null | sort -V | tail -1 || true)
[ -n "$BIN" ] || die "no binary in dist/ (tools/build.sh build)"
GP_BIN=/opt/git-policy/$(basename "$BIN")
code=$(curl -s -o /dev/null -w '%{http_code}' "$GITLAB_URL/users/sign_in" || true)
[ "$code" = 200 ] || die "GitLab not reachable at $GITLAB_URL (HTTP $code)"
info "GitLab $GITLAB_URL, container $CONTAINER, binary $(basename "$BIN")"
info "Jenkins will listen on http://$ADDRESS:$PORT/ (image $JENKINS_IMAGE)"
info "ctl channel: gitpolicy-deploy@$ADDRESS (TEST and PRODUCTION: this host)"
if [ "$DRY" = 1 ]; then
    info "would: generate secrets in $SEC, run deploy/install-ctl.sh, pin the host key,"
    info "       create GitLab token 'git-policy-jenkins', create platform/git-policy-config,"
    info "       build and start Jenkins (container gp-jenkins)"
    step "dry run: nothing changed"; exit 0
fi

step "2/9 secrets ($SEC, root only)"
install -d -m 0700 "$SEC"
for f in jenkins-admin-password jenkins-approver-password; do
    [ -s "$SEC/$f" ] || { (umask 077; gen 24 > "$SEC/$f"); info "generated $f"; }
done
if [ ! -f "$SEC/jenkins_ctl_key" ]; then
    ssh-keygen -q -t ed25519 -N '' -C "jenkins@git-policy" -f "$SEC/jenkins_ctl_key"
    info "generated the Jenkins -> ctl SSH key"
fi

step "3/9 ctl channel (deploy/install-ctl.sh)"
"$ROOT/deploy/install-ctl.sh" --pubkey "$SEC/jenkins_ctl_key.pub" --container "$CONTAINER" | sed 's/^/    /'

step "4/9 pin the host key"
ssh-keyscan -t ed25519 "$ADDRESS" 2>/dev/null > "$SEC/known_hosts.new"
[ -s "$SEC/known_hosts.new" ] || die "ssh-keyscan $ADDRESS returned nothing"
# The scanned key must be THIS host's key (no one in between at setup time).
[ "$(awk '{print $3}' "$SEC/known_hosts.new")" = "$(awk '{print $2}' /etc/ssh/ssh_host_ed25519_key.pub)" ] \
    || die "the key scanned from $ADDRESS is not this host's ssh_host_ed25519_key"
mv "$SEC/known_hosts.new" "$SEC/known_hosts"; chmod 0600 "$SEC/known_hosts"
set +e
ssh -q -i "$SEC/jenkins_ctl_key" -o BatchMode=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile="$SEC/known_hosts" \
    "gitpolicy-deploy@$ADDRESS" status > /dev/null
rc=$?
set -e
case $rc in 0|1) info "channel works: ssh gitpolicy-deploy@$ADDRESS status (exit $rc)" ;;
            *) die "ctl channel test failed (exit $rc)" ;; esac

step "5/9 GitLab read-only token for Jenkins"
# GitLab generates the tokens and prints them to our stdout only: no secret on a command line.
if [ ! -s "$SEC/gitlab-api-token" ]; then
    (umask 077; cexec gitlab-rails runner "
        u = User.find_by_username('root')
        u.personal_access_tokens.active.where(name: 'git-policy-jenkins').each(&:revoke!)
        t = u.personal_access_tokens.create!(name: 'git-policy-jenkins', scopes: [:read_api, :read_repository], expires_at: 365.days.from_now)
        print t.token" > "$SEC/gitlab-api-token")
    grep -qE '^glpat-[A-Za-z0-9_.-]+$' "$SEC/gitlab-api-token" || { rm -f "$SEC/gitlab-api-token"; die "token creation failed"; }
    info "created token 'git-policy-jenkins' (root, read_api + read_repository, 365 days)"
else
    info "reusing the existing token"
fi

step "6/9 GitOps policy repository platform/git-policy-config"
setup_tok=$(cexec gitlab-rails runner "
    u = User.find_by_username('root')
    u.personal_access_tokens.active.where(name: 'git-policy-setup').each(&:revoke!)
    t = u.personal_access_tokens.create!(name: 'git-policy-setup', scopes: [:api], expires_at: 1.day.from_now)
    print t.token")
[[ "$setup_tok" =~ ^glpat-[A-Za-z0-9_.-]+$ ]] || die "setup token creation failed"
GITLAB_URL=$GITLAB_URL GITLAB_TOKEN=$setup_tok SEED_DIR="$ROOT/deploy/policy-repo-seed" "$ROOT/lab/seed-policy-repo.sh" | sed 's/^/    /'
unset setup_tok
cexec gitlab-rails runner "User.find_by_username('root').personal_access_tokens.active.where(name: 'git-policy-setup').each(&:revoke!)" >/dev/null
info "setup token revoked"

step "7/9 Jenkins configuration (.env) and image"
umask 077
cat > "$HERE/.env" <<EOF
# written by setup.sh (no secrets here)
JENKINS_IMAGE=$JENKINS_IMAGE
JENKINS_PORT=$PORT
JENKINS_URL=http://$ADDRESS:$PORT/
GP_POLICY_REPO=$GITLAB_URL/platform/git-policy-config.git
GP_CTL_TEST=gitpolicy-deploy@$ADDRESS
GP_CTL_PRODUCTION=gitpolicy-deploy@$ADDRESS
GP_GITLAB_URL_TEST=$GITLAB_URL
GP_GITLAB_URL_PRODUCTION=$GITLAB_URL
GP_APPROVERS=approver
GP_BIN=$GP_BIN
EOF
umask 022
"${DC[@]}" build -q
"${DC[@]}" create >/dev/null 2>&1
info "image git-policy-jenkins:local built"

step "8/9 secrets volume (readable only by the jenkins user in the container)"
vol=$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/run/git-policy-secrets"}}{{.Name}}{{end}}{{end}}' gp-jenkins)
[ -n "$vol" ] || die "secrets volume not found"
tar -C "$SEC" -cf - jenkins-admin-password jenkins-approver-password jenkins_ctl_key gitlab-api-token known_hosts \
    | docker run --rm -i -u 0 -v "$vol:/s" --entrypoint sh git-policy-jenkins:local -c \
      'rm -f /s/* && tar -xf - -C /s && chown -R 1000:1000 /s && chmod 0400 /s/* && chmod 0500 /s'
info "volume $vol filled"

step "9/9 start Jenkins"
"${DC[@]}" up -d >/dev/null
J=http://127.0.0.1:$PORT
JPW=$(cat "$SEC/jenkins-admin-password")
until curl -fsS -o /dev/null "$J/login" 2>/dev/null; do sleep 5; printf '.'; done; echo
sleep 5
# Jobs created by JCasC while Jenkins still loads jobs from disk are only
# registered after the next start: restart once if needed.
if ! curl -fsS -u "admin:$JPW" "$J/job/git-policy/api/json" >/dev/null 2>&1; then
    docker restart gp-jenkins >/dev/null
    until curl -fsS -o /dev/null "$J/login" 2>/dev/null; do sleep 5; printf '.'; done; echo
    sleep 5
fi
curl -fsS -u "admin:$JPW" "$J/job/git-policy/api/json" >/dev/null || die "Jenkins job git-policy not found"
# First run of each job registers its parameters and triggers (poll/cron):
#   git-policy (STATUS), git-policy-gitops (applies test/policy.yaml; the engine stays as it is),
#   git-policy-sync-membership.
ck=$(mktemp); trap 'rm -f "$ck"' EXIT
jc() { curl -s -u "admin:$JPW" -b "$ck" -c "$ck" "$@"; }
crumb=$(jc "$J/crumbIssuer/api/json" | jq -r '.crumbRequestField+":"+.crumb')
for job in git-policy git-policy-gitops git-policy-sync-membership; do
    before=$(jc "$J/job/$job/api/json" | jq -r '.nextBuildNumber')
    # a job with parameters (after its first run) only accepts buildWithParameters
    code=$(jc -H "$crumb" -X POST -o /dev/null -w '%{http_code}' "$J/job/$job/build")
    [ "$code" = 201 ] || jc -H "$crumb" -X POST -o /dev/null "$J/job/$job/buildWithParameters"
    r=""; for i in $(seq 1 150); do r=$(jc "$J/job/$job/$before/api/json" | jq -r '.result // empty' 2>/dev/null); [ -n "$r" ] && break; sleep 3; done
    info "$job #$before: ${r:-still running}"
done

cat <<MSG

git-policy control plane is up.
  Jenkins   http://$ADDRESS:$PORT/   users: admin, approver
            passwords: sudo cat $SEC/jenkins-admin-password
                       sudo cat $SEC/jenkins-approver-password
  Policy    $GITLAB_URL/platform/git-policy-config  (test/ -> TEST automatically, production/ -> with approval)
Next (in Jenkins, job git-policy):
  1. ACTION=STATUS                          check health
  2. ACTION=ENABLE, ENVIRONMENT=TEST        turn enforcement on
  3. ACTION=RETIRE_HOOK, HOOK_NAME=01-block-dll   when git-policy replaces an older hook
MSG
