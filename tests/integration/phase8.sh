#!/usr/bin/env bash
# Phase 8 integration test: blob size limits and PE signature detection with
# the real installed hook and real `git push` (TEST 04 and friends).
#
# Usage: tests/integration/phase8.sh [path/to/git-policy-binary]
set -uo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/../.." && pwd)
BIN=$(realpath "${1:-$(ls "$ROOT_DIR"/dist/git-policy-*-linux-amd64 | head -1)}")
WORK=$(mktemp -d)
trap 'chmod -R u+w "$WORK" 2>/dev/null; rm -rf "$WORK"' EXIT
GP="$WORK/gp"
HOOKD="$WORK/custom_hooks/pre-receive.d"
HOOK="$HOOKD/50-git-policy"
REPO="$WORK/repo.git"
CL="$WORK/clone"
PASS=0 FAIL=0
ok() { PASS=$((PASS + 1)); printf '  \e[32mPASS\e[0m %s\n' "$1"; }
ko() { FAIL=$((FAIL + 1)); printf '  \e[31mFAIL\e[0m %s\n' "$1"; [ -n "${2:-}" ] && printf '%s\n' "$2" | sed 's/^/       /'; return 0; }
adm() { local sub=$1; shift; "$BIN" admin "$sub" --root "$GP" --hook-path "$HOOK" --actor it-test "$@"; }

push() { local u=$1 p=$2; shift 2; OUT=$(cd "$CL" && GL_USERNAME=$u GL_PROJECT_PATH=$p GL_REPOSITORY=project-1 git push origin "$@" 2>&1); }
reset() { (cd "$CL" && git fetch -q origin 2>/dev/null; git reset -q --hard origin/main); }
# mkfile <path> <size-bytes>   (random content: incompressible, distinct blobs)
mkfile() { (cd "$CL" && mkdir -p "$(dirname "$1")" && head -c "$2" /dev/urandom > "$1" && git add -- "$1" && git commit -qm "add $1"); }
# mkpe <path> : minimal Windows PE header ("MZ" .. e_lfanew=0x80 .. "PE\0\0")
mkpe() { (cd "$CL" && mkdir -p "$(dirname "$1")" && python3 -c "
import struct,sys,os
b=bytearray(os.urandom(4096)); b[0:2]=b'MZ'; b[0x3C:0x40]=struct.pack('<I',0x80); b[0x80:0x84]=b'PE\0\0'
open(sys.argv[1],'wb').write(b)" "$1" && git add -- "$1" && git commit -qm "add $1"); }
expect() { # expect accept|reject <name> <user> <project> <refspec> [grep]
    if push "$3" "$4" "$5"; then got=accept; else got=reject; fi
    if [ "$got" = "$1" ] && { [ -z "${6:-}" ] || grep -q -- "$6" <<<"$OUT"; }; then ok "$2"; else ko "$2 (got $got)" "$OUT"; fi
}
MIB=1048576

echo "== setup"
mkdir -p "$HOOKD"
git init -q --bare "$REPO"
cat > "$REPO/hooks/pre-receive" <<EOF
#!/bin/sh
exec "$HOOK"
EOF
chmod +x "$REPO/hooks/pre-receive"
git clone -q "$REPO" "$CL" 2>/dev/null
(cd "$CL" && git config user.email it@test && git config user.name it && git checkout -q -b main && echo hi > README.md && git add . && git commit -qm init && git push -q origin main 2>/dev/null)
cat > "$WORK/p.yaml" <<EOF
apiVersion: git-policy/v1
kind: GitPolicy
metadata: {name: it-phase8, revision: 1}
settings:
  messages:
    remediation:
      FILE_TOO_LARGE: "Store large artifacts in Nexus (raw-hosted) and reference them by version."
mandatory:
  max_file_size: 20MiB
defaults:
  max_file_size: 2MiB
projects:
  media/site: {max_file_size: 8MiB}
  finance/app:
    refs:
      "refs/heads/release/**": {blocked_signatures: [pe]}
exceptions:
  - id: EXC-MODEL
    rules: [FILE_TOO_LARGE]
    mandatory: true
    subjects: {users: [ds1]}
    scope: {projects: [analytics/forecast]}
    max_file_size: 30MiB
    reason: model snapshots until the artifact store exists
    expires: $(date -u -d '+30 days' +%F)
EOF
adm install >/dev/null && adm apply --reason p8 "$WORK/p.yaml" >/dev/null 2>&1 && adm enable --reason p8 >/dev/null 2>&1 && ok "installed and enabled" || ko "setup"

echo "== TEST 04: large blob"
mkfile data/small.bin $((1 * MIB)); expect accept "1 MiB accepted (default limit 2 MiB)" dev other/app main
mkfile data/large.bin $((3 * MIB)); expect reject "TEST 04: 3 MiB rejected" dev other/app main "Rule: FILE_TOO_LARGE"
for want in "File: data/large.bin" "Size: 3.0 MiB (limit 2.0 MiB)" "Required action (FILE_TOO_LARGE): Store large artifacts in Nexus"; do
    grep -q "GL-HOOK-ERR: $want" <<<"$OUT" && ok "message: '$want'" || ko "message: '$want'" "$OUT"
done
grep -q '"rule":"FILE_TOO_LARGE".*"size":3145728' "$GP"/logs/audit-*.jsonl && ok "audit REJECT carries the size" || ko "audit size"
reset; mkfile media/hero.mp4 $((6 * MIB)); expect accept "6 MiB accepted in media/site (project limit 8 MiB)" dev media/site main
reset; mkfile media/raw.mov $((9 * MIB)); expect reject "9 MiB rejected in media/site" dev media/site main "limit 8.0 MiB"

echo "== anti-bypass for sizes"
reset; mkfile tmp/huge.bin $((3 * MIB)); (cd "$CL" && git rm -q tmp/huge.bin && git commit -qm rm)
expect reject "large blob added then deleted in the same push: rejected" dev other/app main "File: tmp/huge.bin"
reset; BLOB=$(cd "$CL" && head -c $((3 * MIB)) /dev/urandom | git hash-object -w --stdin)
(cd "$CL" && git tag blob-tag "$BLOB")
expect reject "tag pointing directly at a large blob: rejected" dev other/app refs/tags/blob-tag "Rule: FILE_TOO_LARGE"
(cd "$CL" && git tag -d blob-tag >/dev/null)

echo "== mandatory cap and exceptions (max_file_size)"
reset; mkfile model/m1.bin $((25 * MIB)); expect accept "25 MiB waived for ds1 by EXC-MODEL (above the 20 MiB cap)" ds1 analytics/forecast main
grep -q '"action":"EXCEPTION_APPLIED".*"exception":"EXC-MODEL"' "$GP"/logs/audit-*.jsonl && ok "waiver audited" || ko "waiver audited"
reset; mkfile model/m2.bin $((31 * MIB)); expect reject "31 MiB above the exception's 30 MiB: rejected" ds1 analytics/forecast main "FILE_TOO_LARGE"
reset; mkfile model/m3.bin $((25 * MIB)); expect reject "25 MiB by another user: rejected" dev analytics/forecast main "FILE_TOO_LARGE"

echo "== PE signature (content, not name)"
# Orphan histories: this single test repo stands in for several projects, and
# a first branch of a stricter class rescans all history not yet vetted under
# that class (decision Q4) -- which would include the other "projects" files.
reset; (cd "$CL" && git checkout -q --orphan release/1.0 && git rm -rq --cached . && git clean -qfdx && echo r > R && git add R && git commit -qm root); mkpe docs/readme.txt
expect reject "PE disguised as readme.txt on release/1.0: rejected" dev finance/app release/1.0 "Rule: BLOCKED_SIGNATURE"
grep -q "detected from the file header" <<<"$OUT" && ok "message explains header detection" || ko "PE message" "$OUT"
expect accept "same file on a feature branch (no signature rule): accepted" dev finance/app release/1.0:refs/heads/feature/pe
(cd "$CL" && git checkout -q -f main && git branch -q -D release/1.0)
reset; (cd "$CL" && git checkout -q --orphan release/1.1 && git rm -rq --cached . && git clean -qfdx && printf 'MZ is just text here, not an executable header\n' > MZ-notes.txt && git add . && git commit -qm t)
expect accept "text starting with 'MZ' is not a PE: accepted" dev finance/app release/1.1
(cd "$CL" && git checkout -q -f main)

echo "== stricter class rescans history (decision Q4)"
expect reject "release branch created from main rescans main's history under release rules" dev finance/app main:refs/heads/release/2.0 "FILE_TOO_LARGE"

echo "== performance"
reset; (cd "$CL" && mkdir -p many && for i in $(seq 1 300); do head -c 20000 /dev/urandom > "many/f$i.bin"; done && git add many && git commit -qm many)
s=$(date +%s%N); push dev other/app main; rc=$?; e=$(( ($(date +%s%N) - s) / 1000000 ))
[ $rc = 0 ] && ok "300 new files size-checked, whole push ${e} ms" || ko "300 files" "$OUT"
reset; mkfile big/ok.bin $((19 * MIB))
s=$(date +%s%N); push dev media/site main; rc=$?; e=$(( ($(date +%s%N) - s) / 1000000 ))
[ $rc != 0 ] && grep -q FILE_TOO_LARGE <<<"$OUT" && ok "19 MiB blob rejected from header size only, whole push ${e} ms" || ko "19 MiB check" "$OUT"

echo
echo "RESULT: $PASS passed, $FAIL failed"
[ "$FAIL" = 0 ]
