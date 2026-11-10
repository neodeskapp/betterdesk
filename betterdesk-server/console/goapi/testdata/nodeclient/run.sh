#!/usr/bin/env bash
# Compares console/goapi with the Node console's client: builds the Go
# server, lets vectors.js run the calls through the Node code, and runs the
# Go test against what that wrote. Run in the dev image.
#   run.sh [extra arguments for go test]
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
server_dir="$(cd "$here/../../../.." && pwd)"
out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT

cd "$server_dir"
go build -o "$out/betterdesk-server" .
NODE_PATH="$server_dir/../web-nodejs/node_modules" node "$here/vectors.js" "$out/betterdesk-server" "$out/vectors.json"
if [ -n "${GOAPI_KEEP_VECTORS:-}" ]; then cp "$out/vectors.json" "$GOAPI_KEEP_VECTORS"; fi
GOAPI_NODE_VECTORS="$out/vectors.json" go test -tags nodeclient ./console/goapi/ -run 'TestNodeClient' -count=1 "$@"
