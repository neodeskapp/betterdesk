#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

printf '%s\n' "Checking local BetterDesk gate prerequisites..."
bash scripts/local-gate.sh --preflight-only
git config --local core.hooksPath .githooks
printf '%s\n' "Local hooks enabled at .githooks."
