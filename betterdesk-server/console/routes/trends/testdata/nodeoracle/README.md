# Node oracle for package trends

`trends.js` ran the Node route `web-nodejs/routes/trends.routes.js` with a
made-up API client and wrote what it answered to `../trends.json`. The Go
tests replay that file.

The route file is deleted with the move to Go, so the script runs only on a
commit that still has it:

    cd /repo/web-nodejs && node ../betterdesk-server/console/routes/trends/testdata/nodeoracle/trends.js

These files are fixtures. They go away with the Node console.
