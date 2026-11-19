# Node oracle for console/ws

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

`targets.js` records what the old console made of an upgrade:

- 6,000 request targets (hand-picked and generated from parts: dot
  segments in every spelling, backslashes, two slashes, absolute URLs,
  escapes good and bad, query pairs with `+`, empty names, repeated names,
  bytes that are not UTF-8): `new URL(target, 'http://console.test:5000')`,
  whether it threw, its pathname, and `searchParams.get()` for eight names;
- 1,320 origin checks: `validateOrigin` of `middleware/wsOrigin.js` for
  eleven origins, eight cookie headers and three hosts under five settings
  of `WS_ALLOWED_ORIGINS` and `BD_ALLOW_MISSING_ORIGIN`.

`TestNodeTargets` and `TestNodeOrigins` replay `../node_targets.json`.

This folder is a test fixture from the time both servers existed. The script
needs only Node and `middleware/wsOrigin.js`:

    tools/dev.sh "cd betterdesk-server/console/ws/testdata && node nodeoracle/targets.js"
