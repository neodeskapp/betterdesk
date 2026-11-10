#!/usr/bin/env bash
# Build /tmp/oracle/web-nodejs: the Node console of the worktree with route
# files as they were before their routes moved to Go, for the oracle scripts
# of the route groups (run them with BD_REPO=/tmp/oracle). The old files are
# expected in /orig/routes; README.md says how to get them.
set -euo pipefail
rm -rf /tmp/oracle && mkdir -p /tmp/oracle/web-nodejs/routes
for entry in /repo/web-nodejs/* /repo/web-nodejs/.[a-z]*; do
    name="$(basename "$entry")"
    [ "$name" = routes ] || ln -s "$entry" "/tmp/oracle/web-nodejs/$name"
done
for file in /repo/web-nodejs/routes/*; do ln -s "$file" "/tmp/oracle/web-nodejs/routes/$(basename "$file")"; done
for file in /orig/routes/*.js; do rm -f "/tmp/oracle/web-nodejs/routes/$(basename "$file")"; cp "$file" "/tmp/oracle/web-nodejs/routes/"; done
