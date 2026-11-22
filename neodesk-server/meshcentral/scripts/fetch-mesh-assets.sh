#!/usr/bin/env bash
# BetterDesk mesh assets are authored in-repo (AGPL). No upstream fetch required.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../assets" && pwd)"
echo "BetterDesk mesh assets live in: $ROOT"
echo "  - bettercore.js (BetterCore — embedded in betterdesk-server)"
echo "  - BetterViewer, the panel's mesh desktop viewer: web-ui/src/lib/remote/client/betterviewer.ts"
ls -la "$ROOT/bettercore.js" 2>/dev/null || true
