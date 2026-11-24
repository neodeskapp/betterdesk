#!/usr/bin/env bash
# The options the NeoDesk desktop client takes on its command line, from a
# checkout of neodeskapp/neodesk-client: the ones its core_main.rs,
# onboard/mod.rs and flutter/lib/main.dart compare the arguments with.
# neodesk hands these to the client (neodesk-server/cli_client.go), and a
# test compares its list with neodesk-server/testdata/client-options.txt,
# which this script writes:
#
#   bash scripts/client-options.sh ../neodesk-client [REF] > neodesk-server/testdata/client-options.txt
#
# REF is the client's commit, default HEAD. An option that qualifies
# another one is in the list when the client compares it the same way
# (--relay of --connect), not when it looks its value up (--token of
# --assign, --user_name).
set -euo pipefail

checkout=${1:?usage: client-options.sh CLIENT_CHECKOUT [REF]}
ref=${2:-HEAD}
sha=$(git -C "$checkout" rev-parse "$ref^{commit}")

printf '# The desktop client'"'"'s command-line options, from neodeskapp/neodesk-client %s\n' "$sha"
printf '# (src/core_main.rs, src/onboard/mod.rs, flutter/lib/main.dart). Written by scripts/client-options.sh.\n'
{
    for f in src/core_main.rs src/onboard/mod.rs; do
        git -C "$checkout" show "$sha:$f"
    done | grep -oE '(args\[0\] == |arg == |flag == |contains\(&|^[[:space:]]+\| |^[[:space:]]+)"--[a-z][a-z0-9_-]*"' \
        | grep -oE '"--[^"]+"' | tr -d '"'
    git -C "$checkout" show "$sha:flutter/lib/main.dart" \
        | grep -oE "(args\.first == |args\.contains\()'--[a-z][a-z0-9_-]*'" | grep -oE "'--[^']+'" | tr -d "'"
} | LC_ALL=C sort -u
