#!/usr/bin/env bash
# Every way NeoDesk is installed on Linux tells the server who starts it
# again (NEODESK_SUPERVISOR): the systemd unit neodesk.sh writes and
# the image. Without it a confirmed restart, update or restore in the panel
# cannot end the process (RC1). neodesk.ps1's Windows service is checked in
# scripts/test-windows-installer.ps1 and scripts/test-windows-service.ps1.
#
#   bash scripts/check-supervisor-env.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
failures=0

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    failures=$((failures + 1))
}

# The unit neodesk.sh writes
unit_supervisor=$(
    set +eu
    set --
    # shellcheck disable=SC1091
    source "$ROOT/install/neodesk.sh" >/dev/null 2>&1
    tmp=$(mktemp -d)
    SERVER_UNIT="$tmp/neodesk-server.service"
    RUSTDESK_PATH="$tmp/server"
    CONSOLE_PATH="$tmp/console"
    export LOG_FILE=/dev/null
    mkdir -p "$RUSTDESK_PATH" "$CONSOLE_PATH"
    write_server_unit "-mode all" "" >/dev/null 2>&1
    sed -n 's/^Environment=NEODESK_SUPERVISOR=//p' "$SERVER_UNIT" 2>/dev/null | tr -d '\r'
    rm -rf "$tmp"
) || true
[ "$unit_supervisor" = systemd ] \
    || fail "the systemd unit of neodesk.sh sets NEODESK_SUPERVISOR='${unit_supervisor}', expected systemd"

# The image: ENV of the last stage of the Dockerfile
image_supervisor=$(tr -d '\r' < "$ROOT/docker/Dockerfile" | awk '
    /^FROM / { stage = ""; next }
    { stage = stage "\n" $0 }
    END { print stage }' | sed -n 's/.*[[:space:]]NEODESK_SUPERVISOR=\([a-z]*\).*/\1/p' | tail -n 1)
[ "$image_supervisor" = docker ] \
    || fail "the Dockerfile's runtime stage sets NEODESK_SUPERVISOR='${image_supervisor}', expected docker"

# The compose files keep the image's value
checked=0
for compose in "$ROOT"/install/docker-compose*.yml; do
    [ -f "$compose" ] || continue
    checked=$((checked + 1))
    if tr -d '\r' < "$compose" | grep -E 'NEODESK_SUPERVISOR[=:]' | grep -vqE 'NEODESK_SUPERVISOR[=:][[:space:]]*"?docker"?[[:space:]]*$'; then
        fail "$(basename "$compose") sets NEODESK_SUPERVISOR to something other than docker"
    fi
done
[ "$checked" -gt 0 ] || fail "no compose files in $ROOT/install"

if [ "$failures" -gt 0 ]; then
    exit 1
fi
printf 'NEODESK_SUPERVISOR is set by the systemd unit (systemd) and the image (docker)\n'
