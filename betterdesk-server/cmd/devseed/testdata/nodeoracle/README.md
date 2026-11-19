# Node oracle for devseed

`web-nodejs` is gone from this tree. These scripts stay as the record of
how the recordings in this package were made; the Go tests replay the
recordings and need no Node. To run a script again, use a tree that still
has `web-nodejs`: the spec worktree
`C:\Users\info\worktrees\betterdesk-go-spec264`. Mount its `web-nodejs`,
with its modules, over `/repo/web-nodejs` of this tree:

    DEV_DOCKER_ARGS="-v C:/Users/info/worktrees/betterdesk-go-spec264/web-nodejs:/repo/web-nodejs -v bdgo-node-nm:/repo/web-nodejs/node_modules" \
      tools/dev.sh "cd betterdesk-server/<this folder> && node <script>"

Its Node code is upstream's release there, so a script that loads a file
the port deleted or changed on the way can fail or record other answers.

`rows.js` runs `web-nodejs/scripts/dev/seed-ui-data.js` unchanged on a
recording stand-in for better-sqlite3 and writes what the script wrote
straight into the database into `../rows.json.gz`:

- six cases (fixed times, two of them with the demo accounts already
  there): every INSERT as `{table, row}` and the UPDATE of the contract
  counters, which `oracle_test.go` compares with fresh SQLite and
  PostgreSQL databases seeded by devseed at the same times
- 200 more times: a SHA-256 per table of the rows the builders make,
  compared without a database

Run it in the dev image after a change to the demo data:

    cd betterdesk-server/cmd/devseed/testdata/nodeoracle && node rows.js > ../rows.json.gz

`rows.js` is a fixture of the Node seed and stays now that web-nodejs is gone;
`rows.json.gz` stays as the recorded specification.
