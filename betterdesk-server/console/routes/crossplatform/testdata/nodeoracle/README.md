# Node oracle for package crossplatform

`crossplatform.js` ran the Node routes of
`web-nodejs/routes/cross-platform.routes.js` with a made-up API client and
wrote what they answered to `../crossplatform.json`. The Go tests replay
that file.

The route file is deleted with the move to Go, so the script runs only on a
commit that still has it:

    cd /repo/web-nodejs && node ../betterdesk-server/console/routes/crossplatform/testdata/nodeoracle/crossplatform.js

These files are fixtures. They go away with the Node console.
