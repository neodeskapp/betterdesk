# Node oracle for devseed

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

`rows.js` is a fixture of the Node seed and goes away with web-nodejs;
`rows.json.gz` stays as the recorded specification.
