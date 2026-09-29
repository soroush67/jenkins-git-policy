#!/usr/bin/env bash
# Verifies every row of the scenario tables in docs/fa/EXAMPLES-FA.md with a
# real `git push` through the installed git-policy hook.
#
# Each case runs in a fresh bare repository (one repository = one project),
# so cases are independent. Case syntax:
#
#   c <expect> <user|-> <groups|-> <project> <ref> <spec>...
#     expect : accept | audit:RULE (accepted, WOULD_REJECT logged) | RULE (rejected with RULE)
#     groups : comma-separated GitLab groups written to the membership cache
#     spec   : path              small text file
#              path@12M          file of that size (random, incompressible)
#              path@pe           Windows PE header (any name)
#              #commits=N        N extra commits
#
# Usage: tests/examples/verify.sh [binary] [scenario-number...]
set -uo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
BIN=$(realpath "${1:-$(ls "$ROOT_DIR"/dist/git-policy-*-linux-amd64 | head -1)}")
shift || true
ONLY=("$@")
WORK=$(mktemp -d)
trap 'chmod -R u+w "$WORK" 2>/dev/null; rm -rf "$WORK"' EXIT
PASS=0 FAIL=0
ok() { PASS=$((PASS + 1)); printf '  \e[32mPASS\e[0m %s\n' "$1"; }
ko() { FAIL=$((FAIL + 1)); printf '  \e[31mFAIL\e[0m %s\n' "$1"; [ -n "${2:-}" ] && printf '%s\n' "$2" | sed 's/^/       /'; return 0; }

GP="" HOOK="" N=0
scenario() { # scenario <file>
    local f="$ROOT_DIR/examples/scenarios/$1"
    GP="$WORK/gp-${1%%-*}"; HOOK="$WORK/hooks-${1%%-*}/50-git-policy"; mkdir -p "$(dirname "$HOOK")"
    # Exception expiry dates in the examples are fixed; keep them valid for the test run.
    sed -E "s/expires: [0-9-]+/expires: $(date -u -d '+60 days' +%F)/" "$f" > "$WORK/policy.yaml"
    "$BIN" admin install --root "$GP" --hook-path "$HOOK" --actor verify >/dev/null
    membership "-" "-"
    "$BIN" admin apply --root "$GP" --hook-path "$HOOK" --actor verify "$WORK/policy.yaml" >/dev/null 2>&1 || ko "$1: apply"
    "$BIN" admin enable --root "$GP" --hook-path "$HOOK" --actor verify >/dev/null 2>&1 || ko "$1: enable"
    echo "== $1"
}
membership() { # membership <user|-> <groups|->
    local users="{}"
    if [ "$1" != - ] && [ "$2" != - ]; then
        users=$(python3 -c "import json,sys; print(json.dumps({sys.argv[1]: {'state':'active','groups':sys.argv[2].split(',')}}))" "$1" "$2")
    fi
    printf '{"schema":"git-policy/membership/v1","generated_at":"%s","generator":"verify","groups":[],"users":%s}\n' \
        "$(date -u +%FT%TZ)" "$users" > "$GP/membership/current.json"
}
make_spec() { # in the work tree
    case "$1" in
        "#commits="*) local n=${1#\#commits=}
            python3 - "$n" <<'PYEOF' | git fast-import --quiet
import sys, time
n = int(sys.argv[1]); w = sys.stdout.write
w("commit refs/heads/main\nmark :1\ncommitter t <t@t> %d +0000\ndata 1\nx\nfrom refs/heads/main^0\n" % time.time())
for i in range(2, n + 1):
    w("commit refs/heads/main\nmark :%d\ncommitter t <t@t> %d +0000\ndata 1\nx\nfrom :%d\nM 100644 inline bulk/%d.txt\ndata 2\n%d\n\n" % (i, time.time(), i - 1, i, i % 10))
PYEOF
            git reset -q --hard main ;;
        *@pe) local p=${1%@pe}; mkdir -p "$(dirname "$p")"
            python3 -c "
import struct,sys,os
b=bytearray(os.urandom(4096)); b[0:2]=b'MZ'; b[0x3C:0x40]=struct.pack('<I',0x80); b[0x80:0x84]=b'PE\0\0'
open(sys.argv[1],'wb').write(b)" "$p"; git add -- "$p"; git commit -qm "add $p" ;;
        *@*M) local p=${1%@*} m=${1##*@}; m=${m%M}; mkdir -p "$(dirname "$p")"
            head -c $((m * 1048576)) /dev/urandom > "$p"; git add -- "$p"; git commit -qm "add $p" ;;
        *) mkdir -p "$(dirname "$1")"; echo "content $RANDOM" > "$1"; git add -- "$1"; git commit -qm "add $1" ;;
    esac
}
c() { # c <expect> <user> <groups> <project> <ref> <spec>...
    local expect=$1 user=$2 groups=$3 project=$4 ref=$5; shift 5
    N=$((N + 1))
    local bare="$WORK/r$N.git" wt="$WORK/w$N"
    git init -q --bare "$bare"
    git init -q -b main "$wt"
    ( cd "$wt" && git config user.email t@t && git config user.name t && echo base > README.md && git add . && git commit -qm base && git push -q "$bare" main 2>/dev/null )
    printf '#!/bin/sh\nexec "%s"\n' "$HOOK" > "$bare/hooks/pre-receive"; chmod +x "$bare/hooks/pre-receive"
    membership "$user" "$groups"
    ( cd "$wt" && for s in "$@"; do make_spec "$s"; done ) >/dev/null 2>&1
    local envs=(GL_PROJECT_PATH="$project" GL_REPOSITORY=project-1 GL_PROTOCOL=ssh)
    [ "$user" != - ] && envs+=(GL_USERNAME="$user")
    local before; before=$(cat "$GP"/logs/audit-*.jsonl 2>/dev/null | wc -l)
    local out rc; out=$(cd "$wt" && env "${envs[@]}" git push "$bare" "HEAD:$ref" 2>&1); rc=$?
    local desc="$user${groups:+[$groups]} -> $project $ref : $* => $expect"
    desc=${desc//\[-\]/}
    case "$expect" in
        accept) [ $rc = 0 ] && ok "$desc" || ko "$desc" "$out" ;;
        audit:*) local rule=${expect#audit:}
            if [ $rc = 0 ] && tail -n +$((before + 1)) "$GP"/logs/audit-*.jsonl | grep -q "\"action\":\"WOULD_REJECT\".*\"rule\":\"$rule\""; then ok "$desc"; else ko "$desc" "$out"; fi ;;
        *) if [ $rc != 0 ] && grep -q "GL-HOOK-ERR: Rule: $expect" <<<"$out"; then ok "$desc"; else ko "$desc" "$out"; fi ;;
    esac
    rm -rf "$bare" "$wt"
}
want() { [ ${#ONLY[@]} = 0 ] && return 0; local x; for x in "${ONLY[@]}"; do [ "$x" = "$1" ] && return 0; done; return 1; }

if want 01; then scenario 01-block-dll-only.yaml
c accept               dev - team/app refs/heads/main src/Program.cs
c BLOCKED_EXTENSION    dev - team/app refs/heads/main Lib/Mic.Caching.dll
c BLOCKED_EXTENSION    dev - team/app refs/heads/main Lib/TEST.DLL
c BLOCKED_EXTENSION    dev - team/app refs/heads/main "Lib/evil.dll."
c accept               dev - team/app refs/heads/main tools/setup.exe
fi

if want 02; then scenario 02-binaries-nexus-message.yaml
c BLOCKED_EXTENSION    dev - team/app refs/heads/main installer/setup.msi
c BLOCKED_EXTENSION    dev - team/app refs/heads/main bin/app.pdb
c BLOCKED_EXTENSION    dev - team/app refs/heads/main deps/tools.7z
c BLOCKED_EXTENSION    dev - team/app refs/heads/main pkgs/lib.1.0.nupkg
c accept               dev - team/app refs/heads/main docs/manual.pdf
fi

if want 03; then scenario 03-file-size.yaml
c accept               dev - team/app refs/heads/main data/a.bin@5M
c FILE_TOO_LARGE       dev - team/app refs/heads/main data/b.bin@12M
c accept               dev - design/assets refs/heads/main video/intro.mp4@30M
c FILE_TOO_LARGE       dev - design/assets refs/heads/main video/raw.mov@45M
fi

if want 04; then scenario 04-audit-rollout.yaml
c audit:BLOCKED_EXTENSION dev - team/app refs/heads/main Lib/Mic.Caching.dll
c audit:FILE_TOO_LARGE    dev - team/app refs/heads/main data/big.bin@12M
c accept                  dev - team/app refs/heads/main src/Program.cs
fi

if want 05; then scenario 05-namespaces-projects.yaml
c BLOCKED_EXTENSION    dev - finance/accounting refs/heads/main bin/app.pdb
c accept               dev - marketing/site refs/heads/main bin/app.pdb
c BLOCKED_PATH         dev - finance/accounting refs/heads/main src/obj/Debug/app.cache
c accept               dev - finance/reporting refs/heads/main templates/q3.zip
c BLOCKED_EXTENSION    dev - finance/accounting refs/heads/main templates/q3.zip
c audit:BLOCKED_EXTENSION dev - finance/legacy/billing refs/heads/main bin/old.pdb
c BLOCKED_EXTENSION    dev - finance/legacy/billing refs/heads/main bin/old.dll
c accept               dev - sandbox/playground refs/heads/main test.zip
c BLOCKED_EXTENSION    dev - sandbox/playground refs/heads/main test.exe
fi

if want 06; then scenario 06-users.yaml
c USER_PUSH_DENIED     alex - finance/payment-api refs/heads/feature/x src/a.cs
c accept               alex - finance/reporting refs/heads/main src/a.cs
c USER_PUSH_DENIED     intern1 - finance/app refs/heads/main src/a.cs
c accept               intern1 - training/lab1 refs/heads/main src/a.cs
c USER_PUSH_DENIED     bob - finance/payment-api refs/heads/main src/a.cs
c accept               bob - finance/payment-api refs/heads/feature/x src/a.cs
c MANDATORY_USER_DENIED ex.employee - sandbox/x refs/heads/main src/a.cs
c MANDATORY_USER_DENIED - - team/app refs/heads/main src/a.cs
fi

if want 07; then scenario 07-groups.yaml
c accept               dave contractors outsourcing/portal refs/heads/main src/a.cs
c GROUP_PUSH_DENIED    dave contractors finance/app refs/heads/main src/a.cs
c accept               carol contractors finance/app refs/heads/main src/a.cs
c GROUP_PUSH_DENIED    carol contractors hr/app refs/heads/main src/a.cs
c GROUP_PUSH_DENIED    tina qa finance/payment-api refs/heads/release/1.0 src/a.cs
c accept               tina qa finance/payment-api refs/heads/main src/a.cs
c MANDATORY_GROUP_DENIED mallory offboarding team/app refs/heads/main src/a.cs
fi

if want 08; then scenario 08-release-branches.yaml
c accept               dev - team/app refs/heads/main data/a.bin@8M
c FILE_TOO_LARGE       dev - team/app refs/heads/release/1.0 data/a.bin@8M
c BLOCKED_SIGNATURE    dev - team/app refs/heads/release/1.0 docs/readme.txt@pe
c accept               dev - team/app refs/heads/feature/x docs/readme.txt@pe
c BLOCKED_SIGNATURE    dev - team/app refs/tags/v1.0 docs/readme.txt@pe
fi

if want 09; then scenario 09-exceptions.yaml
c accept               dev - finance/legacy-erp refs/heads/main vendor/VendorSdk/Sdk.dll
c BLOCKED_EXTENSION    dev - finance/legacy-erp refs/heads/dev vendor/VendorSdk/Sdk.dll
c BLOCKED_EXTENSION    dev - finance/legacy-erp refs/heads/main vendor/Other/x.dll
c accept               ds1 - analytics/forecast refs/heads/main models/m1.bin@60M
c FILE_TOO_LARGE       ds1 - analytics/forecast refs/heads/main models/m2.bin@90M
c FILE_TOO_LARGE       dev - analytics/forecast refs/heads/main models/m3.bin@60M
c accept               svc-migration - legacy/erp refs/heads/main "#commits=1200"
c LIMIT_COMMITS        dev - legacy/erp refs/heads/main "#commits=1200"
fi

if want 10; then scenario 10-production.yaml
c accept               dev - team/app refs/heads/main src/Program.cs
c BLOCKED_EXTENSION    dev - team/app refs/heads/main Lib/Mic.Caching.dll
c BLOCKED_PATH         dev - team/app refs/heads/main src/obj/Release/app.cache
c FILE_TOO_LARGE       dev - finance/accounting refs/heads/main data/export.csv@12M
c accept               dev - design/assets refs/heads/main video/intro.mp4@60M
c BLOCKED_SIGNATURE    dev - team/app refs/heads/release/2.0 docs/readme.txt@pe
c audit:BLOCKED_EXTENSION dev - finance/legacy/billing refs/heads/main build/app.pdb
c accept               dev - finance/legacy-erp refs/heads/main vendor/VendorSdk/Sdk.dll
c USER_PUSH_DENIED     intern1 - team/app refs/heads/main src/a.cs
c GROUP_PUSH_DENIED    dave contractors team/app refs/heads/main src/a.cs
c accept               dave contractors outsourcing/portal refs/heads/main src/a.cs
c MANDATORY_USER_DENIED ex.employee - team/app refs/heads/main src/a.cs
fi

echo
echo "RESULT: $PASS passed, $FAIL failed"
[ "$FAIL" = 0 ]
