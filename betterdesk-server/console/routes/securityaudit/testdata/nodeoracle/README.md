# Node oracle for package securityaudit

`securityaudit.js` ran the Node routes of
`web-nodejs/routes/security-audit.routes.js` through the console's real API
client against a made-up API server on a local port, and wrote what they
answered to `../securityaudit.json`. The Go tests replay that file.

The route file is deleted with the move to Go, so the script runs only on a
commit that still has it:

    cd /repo/web-nodejs && node ../betterdesk-server/console/routes/securityaudit/testdata/nodeoracle/securityaudit.js

The times in the recorded answers of the API server are relative to the
moment of the recording, which each scenario carries as `now`.

These files are fixtures. They go away with the Node console.
