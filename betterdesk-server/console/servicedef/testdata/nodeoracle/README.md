# The Node console as the reference for `console/servicedef`

This folder is a test fixture. It goes away together with `web-nodejs`.

`sanitize.js` takes `sanitizeGoServerServiceConfig` and the helpers it
calls out of `web-nodejs/services/updateService.js` as source and runs
them with the system around them stubbed: the shell, nssm, the files and
the privileged daemon reload answer what each vector scripts, and every
command and write is recorded. Loading the whole updater would start too
much. It writes `../sanitize.json` (the text helpers with many inputs,
and whole checks on Linux and Windows), which `vectors_test.go` replays.

    tools/dev.sh "cd betterdesk-server/console/servicedef/testdata/nodeoracle && node sanitize.js"
