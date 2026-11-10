# Node oracle for package jsreq

`query.js` asks the real Express query parser and JavaScript what they make
of query strings, JSON values and path segments, and writes the answers to
`../query.json`.

`dates.js` asks JavaScript, in the time zone America/New_York, what
`Date.parse`, `new Date` and `JSON.stringify` make of texts and JSON
values, and writes the answers to `../dates.json`. Its list `loose` holds
forms `Date.parse` reads that the Go port does not; the tests leave them
out.

The Go tests replay both files.

    cd /repo/web-nodejs && node ../betterdesk-server/console/routes/internal/jsreq/testdata/nodeoracle/query.js
    cd /repo/web-nodejs && node ../betterdesk-server/console/routes/internal/jsreq/testdata/nodeoracle/dates.js

These files are fixtures. They go away with the Node console.
