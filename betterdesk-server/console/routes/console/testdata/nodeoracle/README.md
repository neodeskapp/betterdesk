# Node oracle for console/routes/console

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

`bootscript.js` loads the Node console's `routes/console.routes.js` with
stand-ins for the services it reads and records what `GET /console-boot.js`
sent for 102 sets of boot values: languages written left to right and right
to left, every theme mode, and values that must be escaped (`</script>`,
`&`, `>`, line and paragraph separators, quotes, control characters, text
outside the basic plane). `TestBootScriptAsNodeSentIt` builds the script
from the same boot object and compares it byte for byte.

`bootstrap_policy.js` writes `../bootstrap_with_policy.json`: the access,
navigation and permissions parts of what the Node console answers to
`GET /api/console/bootstrap` under an account policy, for four roles. It
saves the policy with `savePolicy` and resolves it with `resolveForUser`,
as the routes do, on an in-memory database; a role's permissions are the
built-in role map. Run on the tree before the upstream 3.5.264 merge, it
gave the file that was first taken from a contract recording, unchanged.
`TestBootstrapUnderAPolicyAsNodeAnswered` compares the Go console's answer
under the same policy with it.

These are fixtures from the time both servers existed. The route file is gone
from `web-nodejs` since the routes moved to Go, so the scripts run only on a
tree that still has it (`WEB_NODEJS`, and `GO_AUTH` for the Go server's
`auth` folder). The recorded files stay.

    tools/dev.sh "cd betterdesk-server/console/routes/console/testdata/nodeoracle && node bootscript.js"
    DEV_DOCKER_ARGS="-v <tree>:/spec:ro -v bdgo-node-nm:/spec/web-nodejs/node_modules \
      -e WEB_NODEJS=/spec/web-nodejs -e GO_AUTH=/spec/betterdesk-server/auth" \
      tools/dev.sh "cd betterdesk-server/console/routes/console/testdata/nodeoracle && node bootstrap_policy.js"
