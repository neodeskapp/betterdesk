#!/usr/bin/env bash
# Builds the Docker image of this checkout for the installer tests, as
# ghcr.io/neodeskapp/neodesk:<tag> (default: local), so the compose files
# of this checkout run the image they belong to:
#
#   bash scripts/build-test-image.sh [tag]
#
# NEODESK_TEST_BINARY: a neodesk-server built from this checkout; the
# image is then the Dockerfile's last stage around it, which takes seconds.
# Without it the whole Dockerfile is built.
set -euo pipefail

SCRIPT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="ghcr.io/neodeskapp/neodesk:${1:-local}"

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

if [ -n "${NEODESK_TEST_BINARY:-}" ]; then
    context=$(mktemp -d)
    trap 'rm -rf "$context"' EXIT
    cp -a "$SCRIPT_ROOT/docker" "$context/"
    cp "$NEODESK_TEST_BINARY" "$context/neodesk-server.bin"
    sed -n '/^# ============= Runtime/,$p' "$SCRIPT_ROOT/docker/Dockerfile" \
        | sed 's|^COPY --from=go-builder /neodesk-server /usr/local/bin/neodesk-server$|COPY neodesk-server.bin /usr/local/bin/neodesk-server|' \
        > "$context/Dockerfile"
    grep -q '^COPY neodesk-server.bin ' "$context/Dockerfile" || fail "the Dockerfile's last stage has no binary to replace"
    docker build -q -t "$IMAGE" "$context" >/dev/null || fail "the image did not build"
else
    docker build -q -f "$SCRIPT_ROOT/docker/Dockerfile" -t "$IMAGE" "$SCRIPT_ROOT" >/dev/null || fail "the image did not build"
fi
printf '%s\n' "$IMAGE"
