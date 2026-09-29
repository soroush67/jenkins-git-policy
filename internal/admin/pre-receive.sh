#!/bin/sh
# git-policy global pre-receive entrypoint.
# Installed by `git-policy admin install` (never by hand) as
#   /var/opt/gitlab/gitaly/custom_hooks/pre-receive.d/50-git-policy
#
# Deliberately tiny: all policy logic lives in the engine binary. Do not edit
# the installed copy; `git-policy status` reports it as MODIFIED if you do.
#
# Break-glass: root can create $GP_ROOT/state/break-glass to bypass the engine
# even when the binary itself is broken. Every bypassed push is logged.
# If the binary is missing or crashes, the push is rejected (fail closed).

GP_ROOT=/var/opt/gitlab/git-policy

if [ -e "$GP_ROOT/state/break-glass" ]; then
    printf '%s BREAK_GLASS_BYPASS user=%s project=%s protocol=%s\n' \
        "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${GL_USERNAME:-}" "${GL_PROJECT_PATH:-}" "${GL_PROTOCOL:-}" \
        >> "$GP_ROOT/logs/break-glass.log" 2>/dev/null
    exit 0
fi

exec "$GP_ROOT/bin/git-policy" hook --root "$GP_ROOT"
