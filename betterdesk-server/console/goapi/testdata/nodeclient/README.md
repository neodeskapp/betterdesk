# Comparison with the Node client

This folder is a test fixture. It goes away together with `web-nodejs`:
once the Node console is deleted there is nothing left to compare with.

The test checks that the methods of `console/goapi` return what the
functions of the Node console's client returned
(`web-nodejs/services/betterdeskApi.js`, `serverBackend.js` and the calls to
Go in `authService.js`).

- `calls.json`: the calls, in order. A step is a function call (`fn`,
  `args`), a plain API request (`http`: method, path under `/api`, body) or
  the SQL file with the first peers (`sql`). `save` keeps a value of a
  result under a name, and `{{name}}` in a later step is replaced by it.
  `hang` marks the steps that also run against a server that never
  answers; `only` limits a step to the named phases.
- `seed.sql`: three peers with a fixed last contact. Peers cannot be
  created through the API without their device, and a device that
  registers counts as online for half a minute.
- `vectors.js`: starts the Go server on a new database, runs the steps
  through the Node code and writes every result to a file. It runs them
  three times: with nothing listening (`down`), against the server (`up`)
  and against a listener that never answers (`hang`). It also compares a
  list of texts with `localeCompare`, the order of the device list.
- `run.sh`: builds the server, runs `vectors.js`, then runs the Go test
  `TestNodeClient` (build tag `nodeclient`), which performs the same steps
  in process and compares the results as parsed JSON.

Run it in the dev image, from the repository root:

    bash betterdesk-server/console/goapi/testdata/nodeclient/run.sh

`GOAPI_NODE_WEB` names another tree's `web-nodejs` to take the Node code
from, such as the specification tree of an upstream merge mounted into the
container (`-v <spec tree>/web-nodejs:/spec/web-nodejs -v
bdgo-node-nm:/spec/web-nodejs/node_modules -e GOAPI_NODE_WEB=/spec/web-nodejs`).

Values that cannot be equal in two runs are masked before the comparison.
The list is in `nodeclient_test.go` (`maskRules`).
