#!/usr/bin/env bash
# Install or upgrade git-policy inside the GitLab container.
# Run on the Docker host. Non-destructive and idempotent:
#   - never removes or edits other hooks (e.g. the 01-block-dll PoC);
#   - never overwrites a different 50-git-policy hook unless --replace-hook
#     (the old one is backed up);
#   - on first install the engine starts DISABLED: push behaviour does not
#     change until a policy is deployed and the engine is enabled;
#   - upgrades keep policy, state and logs, and keep the previous binary.
set -euo pipefail

CONTAINER=gitlab
BINARY=""
ACTOR="install.sh:${SUDO_USER:-${USER:-unknown}}"
DRY_RUN=0
REPLACE_HOOK=0
ROOT=/var/opt/gitlab/git-policy
HOOK_DIR=/var/opt/gitlab/gitaly/custom_hooks/pre-receive.d
GITALY_CONFIG=/var/opt/gitlab/gitaly/config.toml

usage() {
    cat <<EOF
Usage: $0 [--container NAME] [--binary PATH] [--actor NAME] [--replace-hook] [--dry-run]

  --container NAME   GitLab container (default: gitlab)
  --binary PATH      git-policy linux/amd64 binary (default: newest dist/git-policy-*-linux-amd64)
  --actor NAME       recorded in the audit log (default: install.sh:\$USER)
  --replace-hook     back up and replace an existing, different 50-git-policy hook
  --dry-run          run all checks, change nothing
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --container) CONTAINER=$2; shift 2 ;;
        --binary) BINARY=$2; shift 2 ;;
        --actor) ACTOR=$2; shift 2 ;;
        --replace-hook) REPLACE_HOOK=1; shift ;;
        --dry-run) DRY_RUN=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
    esac
done

step() { printf '\n==> %s\n' "$*"; }
info() { printf '    %s\n' "$*"; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
cexec() { docker exec -u root "$CONTAINER" "$@"; }

[[ "$CONTAINER" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*$ ]] || die "invalid container name"
[[ "$ACTOR" =~ ^[A-Za-z0-9\ _.:#@/+=-]{1,128}$ ]] || die "invalid --actor"

step "1/7 prerequisites on the host"
command -v docker >/dev/null || die "docker not found"
if [ -z "$BINARY" ]; then
    BINARY=$(ls "$(dirname "$0")"/dist/git-policy-*-linux-amd64 2>/dev/null | sort -V | tail -1 || true)
fi
[ -n "$BINARY" ] && [ -f "$BINARY" ] || die "binary not found (build with tools/build.sh build, or pass --binary)"
SHA=$(sha256sum "$BINARY" | cut -d' ' -f1)
if [ -f "$BINARY.sha256" ]; then
    [ "$(cut -d' ' -f1 < "$BINARY.sha256")" = "$SHA" ] || die "checksum mismatch for $BINARY"
    info "binary: $BINARY (sha256 verified)"
else
    info "binary: $BINARY (sha256 $SHA, no .sha256 file to verify against)"
fi
"$BINARY" version | sed 's/^/    /'

step "2/7 GitLab container"
[ "$(docker inspect -f '{{.State.Running}}' "$CONTAINER" 2>/dev/null)" = true ] || die "container $CONTAINER is not running"
info "container $CONTAINER is running"
docker inspect -f '{{range .Mounts}}{{println .Destination}}{{end}}' "$CONTAINER" | grep -qx /var/opt/gitlab \
    && info "/var/opt/gitlab is a persistent mount" \
    || die "/var/opt/gitlab is not a mounted volume in $CONTAINER: installed files would be lost on re-creation"
cexec id git >/dev/null 2>&1 || die "no 'git' account in $CONTAINER"
info "git account: $(cexec id git)"

step "3/7 Gitaly custom hooks"
if cexec grep -q "custom_hooks_dir *= *\"/var/opt/gitlab/gitaly/custom_hooks\"" "$GITALY_CONFIG" 2>/dev/null; then
    info "gitaly custom_hooks_dir = /var/opt/gitlab/gitaly/custom_hooks"
else
    die "gitaly config does not set custom_hooks_dir to /var/opt/gitlab/gitaly/custom_hooks ($GITALY_CONFIG)"
fi
# A fresh GitLab has no pre-receive.d yet: create it (root:root 0755, like
# the directory Gitaly reads) instead of making the operator do it by hand.
if cexec test -d "$HOOK_DIR"; then
    info "existing global pre-receive hooks (left untouched):"
    cexec ls -l "$HOOK_DIR" | sed 's/^/      /'
elif [ "$DRY_RUN" = 1 ]; then
    info "$HOOK_DIR does not exist yet: it will be created (root:root 0755)"
else
    cexec install -d -o root -g root -m 0755 "$HOOK_DIR"
    info "created $HOOK_DIR (root:root 0755)"
fi

step "4/7 existing installation"
if cexec test -x "$ROOT/bin/git-policy"; then
    info "upgrade: currently installed $(cexec "$ROOT/bin/git-policy" version | head -1)"
    cexec "$ROOT/bin/git-policy" status | sed 's/^/      /' || true
else
    info "fresh install into $ROOT"
fi

if [ "$DRY_RUN" = 1 ]; then
    step "dry run: all checks passed, nothing changed"
    exit 0
fi

step "5/7 copy binary into the container"
TMP=$(cexec mktemp -d /tmp/git-policy-install.XXXXXX)
trap 'docker exec -u root "$CONTAINER" rm -rf "$TMP" >/dev/null 2>&1 || true' EXIT
docker cp "$BINARY" "$CONTAINER:$TMP/git-policy"
cexec chown root:root "$TMP/git-policy"
cexec chmod 0700 "$TMP/git-policy"
[ "$(cexec sha256sum "$TMP/git-policy" | cut -d' ' -f1)" = "$SHA" ] || die "checksum mismatch after copy"
info "copied and verified"

step "6/7 install (git-policy admin install)"
args=(admin install --actor "$ACTOR")
[ "$REPLACE_HOOK" = 1 ] && args+=(--replace-hook)
cexec "$TMP/git-policy" "${args[@]}" | sed 's/^/    /'

step "7/7 verify"
cexec ls -l "$HOOK_DIR" | sed 's/^/      /'
set +e
cexec "$ROOT/bin/git-policy" status | sed 's/^/    /'
rc=${PIPESTATUS[0]}
set -e
case "$rc" in
    0|1) info "installed. Next: deploy a policy (admin apply) and enable it (admin enable)." ;;
    *) die "status reports CRITICAL (exit $rc); see above" ;;
esac
