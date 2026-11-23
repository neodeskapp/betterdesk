#!/usr/bin/env bash
# Checks of scripts/check-security-regressions.sh: in a copy of the
# repository's layout it fails on an installer that writes a credential
# into a systemd unit and on a committed unit with a secret, and passes on
# clean files. It must read the repository it sits in, not the folder
# around it.
#
#   bash scripts/test-security-regressions.sh
set -euo pipefail

SCRIPT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_DIR="$(mktemp -d)"
trap 'rm -rf "$TEST_DIR"' EXIT

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

# repo: a repository in $TEST_DIR/repo with the check and a clean installer
repo() {
    rm -rf "$TEST_DIR/repo"
    mkdir -p "$TEST_DIR/repo/scripts"
    cp "$SCRIPT_ROOT/scripts/check-security-regressions.sh" "$TEST_DIR/repo/scripts/"
    printf '%s\n' '#!/usr/bin/env bash' 'echo "Environment=DB_TYPE=sqlite"' > "$TEST_DIR/repo/neodesk.sh"
}

# check: runs the check of $TEST_DIR/repo from another folder; its exit
# status in $status
check() {
    status=0
    (cd "$TEST_DIR" && bash repo/scripts/check-security-regressions.sh) > "$TEST_DIR/out" 2>&1 || status=$?
}

repo
check
[ "$status" = 0 ] || fail "clean files: exit $status: $(cat "$TEST_DIR/out")"

repo
printf '%s\n' 'Environment=SESSION_SECRET=from-the-installer' >> "$TEST_DIR/repo/neodesk.sh"
check
[ "$status" = 1 ] || fail "an installer writing a secret into a unit passed: $(cat "$TEST_DIR/out")"
grep -q 'neodesk.sh' "$TEST_DIR/out" || fail "the failure does not name the installer: $(cat "$TEST_DIR/out")"

repo
printf '%s\n' '[Service]' 'Environment=DATABASE_URL=postgres://u:p@db/x' > "$TEST_DIR/repo/neodesk.service"
check
[ "$status" = 1 ] || fail "a unit with a secret passed: $(cat "$TEST_DIR/out")"

# The files around the repository are not the repository's
repo
printf '%s\n' 'Environment=SESSION_SECRET=outside' > "$TEST_DIR/neodesk.sh"
check
[ "$status" = 0 ] || fail "a file outside the repository failed the check: $(cat "$TEST_DIR/out")"
rm -f "$TEST_DIR/neodesk.sh"

echo "security regression check: all checks passed"
