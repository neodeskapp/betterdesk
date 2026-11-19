#!/usr/bin/env bash
# The Go the installers download or accept for a source build, and the Go
# of the Docker build stages, must be the toolchain go.mod asks for: an
# older one builds, but with a standard library go.mod moved away from for
# a security fix.
#
#   bash scripts/check-go-toolchain-pins.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
failures=0

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    failures=$((failures + 1))
}

# lines FILE: the file without carriage returns (a Windows checkout)
lines() {
    tr -d '\r' < "$1"
}

toolchain=$(lines "$ROOT/betterdesk-server/go.mod" | sed -n 's/^toolchain go\([0-9][0-9.]*\)$/\1/p')
[ -n "$toolchain" ] || { printf 'FAIL: go.mod has no toolchain line\n' >&2; exit 1; }

# check_pin FILE NAME VALUE
check_pin() {
    if [ -z "$3" ]; then
        fail "$1: no $2 found"
    elif [ "$3" != "$toolchain" ]; then
        fail "$1: $2 is $3, go.mod asks for $toolchain"
    fi
}

for name in GO_MIN_VERSION GO_DOWNLOAD_VERSION; do
    check_pin betterdesk.sh "$name" \
        "$(lines "$ROOT/betterdesk.sh" | sed -n "s/^${name}=\"\\([0-9.]*\\)\".*/\\1/p" | head -n 1)"
    check_pin betterdesk.ps1 "$name" \
        "$(lines "$ROOT/betterdesk.ps1" | sed -n "s/^\\\$script:${name} = \"\\([0-9.]*\\)\".*/\\1/p" | head -n 1)"
done

for dockerfile in "$ROOT"/Dockerfile*; do
    [ -f "$dockerfile" ] || continue
    while IFS= read -r version; do
        check_pin "$(basename "$dockerfile")" "golang image" "$version"
    done < <(lines "$dockerfile" | sed -n 's/^FROM golang:\([0-9][0-9.]*\)[-@ ].*/\1/p')
done

if [ "$failures" -gt 0 ]; then
    exit 1
fi
printf 'Go toolchain pins match go.mod (%s)\n' "$toolchain"
