#!/usr/bin/env bash
# NeoDesk mesh assets are authored in-repo (AGPL). No upstream fetch required.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../assets" && pwd)"
echo "NeoDesk mesh assets live in: $ROOT"
echo "  - neocore.js (NeoCore, embedded in neodesk-server)"
echo "  - NeoViewer, the panel's mesh desktop viewer: web-ui/src/lib/remote/client/neoviewer.ts"
ls -la "$ROOT/neocore.js" 2>/dev/null || true
