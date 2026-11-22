#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT/betterdesk-server"

if command -v go >/dev/null 2>&1; then
    exec go run ./cmd/repotool local-gate "$@"
elif command -v go.exe >/dev/null 2>&1; then
    exec go.exe run ./cmd/repotool local-gate "$@"
else
    printf '%s\n' "BLOCKED: Go is not available to run the local gate." >&2
    exit 1
fi
