# Node fixtures for console/authimport

There is no Node code that imports `auth.db`; the import is new (DECISIONS
D1). These scripts use the real Node console to build what the import
works on and to check what the old console reads afterwards.

| Script | What it does |
|---|---|
| `split.js <folder>` | builds an install in the old split layout with the console's own database adapter and session store (`auth.db` and `db_v2.sqlite3`), fills it with accounts (one deleted afterwards), groups, folders, strategies, address books, panel access rows, settings, tickets, alerts, the audit log, tokens, metrics and client audit rows, and in the main file devices, tenants, agent bundles and sessions; writes the SQL text of both files to `../split/` |
| `readback.js <main> <auth>` | reads an install through the console's database facade and prints what it sees, keyed by names: the old console picks split or single-file mode itself, by the marker |

The Go tests build the install from `../split/*.sql` (`authimport_test.go`).
`nodeoracle_test.go` (build tag `nodeoracle`) runs `readback.js` on a copy
of the split install and on the main file after the import: the old
console, started after a rollback, must read the same accounts, groups,
folders, strategies, settings and audit entries, apart from what the
import rules decide differently.

## Running

In the dev image, from the worktree:

    tools/dev.sh "cd betterdesk-server/console/authimport/testdata/nodeoracle && node split.js /tmp/split"
    tools/dev.sh "cd betterdesk-server && go test -tags nodeoracle ./console/authimport/ -count=1"

This folder goes away with `web-nodejs`; `../split/*.sql` stays.
