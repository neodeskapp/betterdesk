# Recordings of the old console for console/authimport

The files in `split/` are fixed records of an install in the old split
layout, made with the Node console's own code while it still existed. They
can't be made again. The Go tests build the install from them and need no
Node. Don't edit them.

| File | What it holds | Read by |
|---|---|---|
| `split/auth.sql` | the SQL text of `auth.db` | `splitInstall` (`authimport_test.go`) |
| `split/main.sql` | the SQL text of `db_v2.sqlite3` | `splitInstall` (`authimport_test.go`) |

## What is recorded

There is no Node code that imports `auth.db`; the import is new. The
recorder built the split install with the console's own database
adapter and session store and filled it through them: accounts (one deleted
afterwards), groups, folders, strategies, address books, panel access rows,
settings, tickets, alerts, the audit log, tokens, metrics and client audit
rows in `auth.db`, and devices, tenants, agent bundles and sessions in the
main file. Then it wrote the SQL text of both files.

`splitInstall` loads the two files, lets the signal server create its
tables in the main file, adds the server's own accounts and rows, and runs
the console's migration. The tests then check every table after the import
(`TestImportMergesASplitInstall`) and the rules for accounts found in both
files.
