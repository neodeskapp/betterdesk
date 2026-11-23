#!/usr/bin/env bash
# NeoDesk unified security suite (local only).
# Run before publishing a release to GitHub.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
echo "== NeoDesk security suite =="
echo "Root: $ROOT"

echo ""
echo "-- Go: relay / sessiongrant / crypto connection security --"
cd "$ROOT/neodesk-server"
go test ./relay/ ./sessiongrant/ ./crypto/ ./auth/ -count=1 \
  -run 'ConnectionSecurity|AuthorizationRegistry|SignIdPk|IssueAndVerify|VerifyRejects|JWT'

echo ""
echo "Security suite PASSED."
