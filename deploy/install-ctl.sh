#!/usr/bin/env bash
# Install the Jenkins -> GitLab control channel on the Docker host that runs
# the GitLab container (run as root on that host). Idempotent.
#
# Creates:
#   user gitpolicy-deploy          no password, no docker group, no shell access
#   /usr/local/sbin/git-policy-ctl root:root 0755 (the only thing Jenkins can run)
#   /etc/sudoers.d/git-policy      0440, validated with visudo -c
#   ~gitpolicy-deploy/.ssh/authorized_keys   command="sudo -n …git-policy-ctl",restrict <jenkins public key>
#
# Usage: sudo deploy/install-ctl.sh --pubkey jenkins_ctl_key.pub [--container gitlab] [--dry-run]
set -euo pipefail

PUBKEY="" CONTAINER=gitlab DRY=0
USER_NAME=gitpolicy-deploy
CTL=/usr/local/sbin/git-policy-ctl
SUDOERS=/etc/sudoers.d/git-policy
while [ $# -gt 0 ]; do
    case "$1" in
        --pubkey) PUBKEY=$2; shift 2 ;;
        --container) CONTAINER=$2; shift 2 ;;
        --dry-run) DRY=1; shift ;;
        -h|--help) sed -n '2,14p' "$0"; exit 0 ;;
        *) echo "unknown option $1" >&2; exit 2 ;;
    esac
done
die() { echo "ERROR: $*" >&2; exit 1; }
[ "$(id -u)" = 0 ] || die "run as root"
[ -f "$PUBKEY" ] || die "--pubkey FILE (the Jenkins public key) is required"
grep -qE '^(ssh-ed25519|ecdsa-sha2-nistp[0-9]+|ssh-rsa) [A-Za-z0-9+/=]+' "$PUBKEY" || die "$PUBKEY is not an SSH public key"
[[ "$CONTAINER" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*$ ]] || die "invalid container name"
command -v docker >/dev/null || die "docker not found"
command -v sudo >/dev/null || die "sudo not found"
[ "$(docker inspect -f '{{.State.Running}}' "$CONTAINER" 2>/dev/null)" = true ] || die "container $CONTAINER is not running"
SRC=$(dirname "$0")/git-policy-ctl
[ -f "$SRC" ] || die "$SRC not found"

echo "plan:"
echo "  user        $USER_NAME (locked password, not in the docker group)"
echo "  command     $CTL  (container: $CONTAINER)"
echo "  sudoers     $SUDOERS"
echo "  ssh key     $(cut -d' ' -f1,3- "$PUBKEY")"
[ "$DRY" = 1 ] && { echo "dry run: nothing changed"; exit 0; }

id "$USER_NAME" >/dev/null 2>&1 || useradd --system --create-home --shell /bin/sh "$USER_NAME"
passwd -l "$USER_NAME" >/dev/null 2>&1 || true
if id -nG "$USER_NAME" | tr ' ' '\n' | grep -qx docker; then die "$USER_NAME must NOT be in the docker group"; fi

install -o root -g root -m 0755 "$SRC" "$CTL"
if [ "$CONTAINER" != gitlab ]; then
    sed -i "s/^CONTAINER=\${GITLAB_CONTAINER:-gitlab}/CONTAINER=\${GITLAB_CONTAINER:-$CONTAINER}/" "$CTL"
fi

tmp=$(mktemp)
cat > "$tmp" <<EOF
# git-policy: Jenkins may run exactly one command as root (see $CTL).
Defaults!$CTL env_keep += "SSH_ORIGINAL_COMMAND SSH_CLIENT"
$USER_NAME ALL=(root) NOPASSWD: $CTL
EOF
visudo -c -f "$tmp" >/dev/null || { rm -f "$tmp"; die "generated sudoers file is invalid"; }
install -o root -g root -m 0440 "$tmp" "$SUDOERS"; rm -f "$tmp"

home=$(getent passwd "$USER_NAME" | cut -d: -f6)
install -d -o "$USER_NAME" -g "$USER_NAME" -m 0700 "$home/.ssh"
printf 'command="sudo -n %s",restrict %s\n' "$CTL" "$(cut -d' ' -f1-2 "$PUBKEY") jenkins-git-policy" > "$home/.ssh/authorized_keys"
chown "$USER_NAME:$USER_NAME" "$home/.ssh/authorized_keys"; chmod 0600 "$home/.ssh/authorized_keys"
touch /var/log/git-policy-ctl.log; chmod 0600 /var/log/git-policy-ctl.log

echo "installed. Test from the Jenkins agent:"
echo "  ssh -i <jenkins private key> $USER_NAME@$(hostname -f 2>/dev/null || hostname) status"
echo "Pin the host key on Jenkins (GP_SSH_KNOWN_HOSTS):"
echo "  ssh-keyscan -t ed25519 $(hostname -f 2>/dev/null || hostname)"
