# Node oracle for console/ws

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

This folder is a test fixture for the time both servers exist. The script
needs only Node and `middleware/wsOrigin.js`:

    tools/dev.sh "cd betterdesk-server/console/ws/testdata && node nodeoracle/targets.js"
