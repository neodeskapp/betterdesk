# Node oracle for console/routes/registration

`registration.js` runs the Node console's registration routes
(`routes/registration.routes.js`, mounted at the root and at `/api/bd`)
with the real `services/dbAdapter.js`, the real permission loading, panel
access policy and guard, translations, JSON content-type rule, API client,
device layer (`services/serverBackend.js`), device group service and key
service, against a made-up API server, and records every answer.
`TestNodeAnswers` in the Go package replays the requests on a database of
the same kind and must get the same status and body, byte for byte after
the times the database fills in and the random access tokens, the same
rows, audit entries and calls to the API server, and the same events on
`/ws/panel-events`.

This folder is a test fixture for the time both servers exist. Once
`web-nodejs` loses the route file the script cannot run any more; the
recorded files `../node_sqlite.json` and `../node_postgres.json` stay and
keep pinning the Go code to what Node answered.

## What is recorded

658 steps per database:

- every route at both mounts as every role and signed out;
- the device request with bodies of every kind (ids of every length in
  UTF-16 units, white space around them, numbers, lists, objects, fields
  cut in the middle of a pair, raw JSON that is no object, forms, text),
  the address from `X-Forwarded-For`, `X-Real-IP` and the connection, and
  a device asking again while pending, approved and rejected;
- the status a device polls, with ids given once, twice, as a list, as an
  object, in other letter case, cut in the middle of an escape;
- the list with every status and search filter, ids in every spelling;
- the pending count with the API server's count of every JSON type;
- the enrollment UI flags with every setting value and every mode answer;
- approvals with folders, device groups (folder groups, tag groups, the
  groups an operator may see, more than 100 guids), display names and tags
  of every type, Host headers with an IPv6 address and without a name;
- rejections with reasons of every type and length, deletions;
- the enrollment queue, its history and the approve, reject and
  clear-rejection proxies with the API server answering in every way;
- help requests: the list with entries of every shape, names from the
  console's audit log and its memory of accepts at both mounts, accept and
  resolve with every kind of answer;
- `listRegistrationNotifications` on the same database at three points.

The made-up API server answers by method and path from the table in the
script (saved in the recording as `api`); a step can override answers
(`api` of the step). Role permission lookups are answered from the
built-in role map and left out of the comparison. Rows the run creates get
fixed times before lists are read, so the order by time is one order. The
panel events come from a stand-in for `services/goConsoleBridge.js`.

## Running

From a worktree that still has the route file, in the dev image (the
recordings were made with `TZ=UTC`, which the Go test checks):

    tools/dev.sh "cd betterdesk-server/console/routes/registration/testdata && TZ=UTC node nodeoracle/registration.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/routes/registration/testdata && \
      TZ=UTC ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node nodeoracle/registration.js"

On PostgreSQL the script creates a database of its own and drops it at the
end.
