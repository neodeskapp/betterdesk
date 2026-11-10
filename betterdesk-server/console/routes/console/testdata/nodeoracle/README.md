# Node oracle for console/routes/console

`bootscript.js` loads the Node console's `routes/console.routes.js` with
stand-ins for the services it reads and records what `GET /console-boot.js`
sent for 102 sets of boot values: languages written left to right and right
to left, every theme mode, and values that must be escaped (`</script>`,
`&`, `>`, line and paragraph separators, quotes, control characters, text
outside the basic plane). `TestBootScriptAsNodeSentIt` builds the script
from the same boot object and compares it byte for byte.

`../bootstrap_with_policy.json` is not made by a script: it is the access
and navigation part of what the Node console answered to
`GET /api/console/bootstrap` under an account policy, taken from a contract
recording (see the file's note). `TestBootstrapUnderAPolicyAsNodeAnswered`
compares the Go console's answer under the same policy with it.

These are fixtures for the time both servers exist. The route file is gone
from `web-nodejs` since the routes moved to Go, so the script runs only on a
tree that still has it. The recorded files stay.

    tools/dev.sh "cd betterdesk-server/console/routes/console/testdata/nodeoracle && node bootscript.js"
