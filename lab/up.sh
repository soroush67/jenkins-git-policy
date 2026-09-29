#!/usr/bin/env bash
# Bring up the local git-policy lab: GitLab CE 17.10.5, Jenkins, gp-ctl.
# Idempotent. Secrets are generated once into lab/.env and lab/ctl/ (git-ignored).
set -euo pipefail
cd "$(dirname "$0")"
# docker compose plugin, or the standalone docker-compose binary
if docker compose version >/dev/null 2>&1; then DC=(docker compose); else DC=(docker-compose); fi

gen() { head -c 32 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c "$1"; }
if [ ! -f .env ]; then
    umask 077
    printf 'GITLAB_ROOT_PASSWORD=%s\nJENKINS_ADMIN_PASSWORD=%s\n' "Gp$(gen 20)!" "$(gen 20)" > .env
    echo "created lab/.env (passwords)"
fi
if [ ! -f ctl/jenkins_ctl_key ]; then
    ssh-keygen -q -t ed25519 -N '' -C jenkins@git-policy-lab -f ctl/jenkins_ctl_key
    printf 'command="sudo -n /usr/local/sbin/git-policy-ctl",restrict %s\n' "$(cat ctl/jenkins_ctl_key.pub)" > ctl/authorized_keys
    chmod 0600 ctl/jenkins_ctl_key   # jenkins runs as uid 1000 in its container (lab only)
    echo "created lab/ctl/jenkins_ctl_key (Jenkins -> gp-ctl SSH key)"
fi
[ -f .gitlab-token ] || printf 'pending\n' > .gitlab-token

echo "==> starting GitLab (first start takes several minutes)"
"${DC[@]}" up -d gitlab
until [ "$(docker inspect -f '{{.State.Health.Status}}' gitlab)" = healthy ]; do sleep 10; printf '.'; done; echo " GitLab healthy"

if [ "$(cat .gitlab-token)" = pending ]; then
    echo "==> creating an admin API token for the lab (tests + membership sync)"
    tok="glpat-lab$(gen 20)"
    docker exec gitlab gitlab-rails runner "
      u = User.find_by_username('root')
      t = u.personal_access_tokens.create!(name: 'git-policy-lab', scopes: [:api, :read_api, :sudo, :admin_mode], expires_at: 300.days.from_now)
      t.set_token('$tok'); t.save!" >/dev/null
    printf '%s\n' "$tok" > .gitlab-token && chmod 0600 .gitlab-token
fi

echo "==> starting gp-ctl and Jenkins"
install -m 0755 ../deploy/git-policy-ctl ctl/git-policy-ctl   # build context copy (source: deploy/)
"${DC[@]}" up -d --build gp-ctl jenkins
until curl -fsS -o /dev/null http://localhost:8081/login; do sleep 5; printf '.'; done; echo " Jenkins up"

cat <<MSG

Lab is up:
  GitLab   http://localhost:8080   user root   (password: lab/.env GITLAB_ROOT_PASSWORD)   ssh port 2222
  Jenkins  http://localhost:8081   user admin  (password: lab/.env JENKINS_ADMIN_PASSWORD)
  gp-ctl   forced-command gateway (Jenkins -> git-policy-ctl -> docker exec gitlab)
Next: ../install.sh --actor lab   (installs git-policy into the lab GitLab)
MSG
