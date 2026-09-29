#!/usr/bin/env bash
# Remove the git-policy hook wrapper from the GitLab container (run on the
# Docker host). Only pre-receive.d/50-git-policy is removed, after a backup
# into /var/opt/gitlab/git-policy/backup/. Policies, state, logs and the
# binary are kept, so re-running install.sh restores the previous setup.
# Complete removal of the data directory is deliberately manual (see --help).
set -euo pipefail

CONTAINER=gitlab
ACTOR="uninstall.sh:${SUDO_USER:-${USER:-unknown}}"
FORCE=""
ROOT=/var/opt/gitlab/git-policy

usage() {
    cat <<EOF
Usage: $0 [--container NAME] [--actor NAME] [--force]

  --force   also remove a hook wrapper that was modified by hand

Removing ALL git-policy data (irreversible; audit logs are lost) is manual:
  docker exec -u root $CONTAINER tar -czf /tmp/git-policy-final.tgz -C /var/opt/gitlab git-policy
  docker cp $CONTAINER:/tmp/git-policy-final.tgz .
  docker exec -u root $CONTAINER rm -rf $ROOT
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --container) CONTAINER=$2; shift 2 ;;
        --actor) ACTOR=$2; shift 2 ;;
        --force) FORCE=--force; shift ;;
        -h|--help) usage; exit 0 ;;
        *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
    esac
done
[[ "$CONTAINER" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*$ ]] || { echo "invalid container name" >&2; exit 2; }

docker exec -u root "$CONTAINER" "$ROOT/bin/git-policy" admin uninstall --actor "$ACTOR" $FORCE
docker exec -u root "$CONTAINER" ls -l /var/opt/gitlab/gitaly/custom_hooks/pre-receive.d
