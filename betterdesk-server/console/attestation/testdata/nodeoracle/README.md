# Node oracle for console/attestation

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

These scripts run the Node console's `services/serverAttestation.js` and
record what it did. The Go tests of `console/attestation` replay every
record and must do the same.

This folder is a test fixture from the time both servers existed. It needs
`web-nodejs`, which this tree no longer has; `services/serverAttestation.js` is
already gone from the tree, so the scripts run on a checkout from before
the move (point `WEB_NODEJS` at its `web-nodejs` folder). The recorded files
in the parent folder stay: they are plain data and keep pinning the Go code
to what Node did.

## badge.js

Computes, as `GET /api/auth/login-page` did, the `attestation` of the
sign-in page for 14,522 combinations of the stored test result
(`server_attestation_result`), the stored badge texts
(`server_attestation_badge_config`), the brand name and the texts of the
request language, and for a settings table that cannot be read. The values
are hostile on purpose: every JSON type, tiers named like what every
JavaScript object has, texts that need trimming and cutting at their
limits, JSON that cannot be read. Writes `../badge.json.gz`;
`TestSignInBadgeAsNodeMadeIt` replays it.

## run.js

Runs 44 capacity tests in 42 scenarios in virtual time. The service is
compiled with a fake clock (`@sinonjs/fake-timers`) in place of
`setTimeout`, `setInterval` and `Date`, so its waits of seconds to minutes
take no time; a fake `ws` plays the connections a scenario scripts (opens,
refusals, connections that never answer, answers to the health check and
when they come), the host readings and the API server's statistics are
scripted, and the bytes of the relay session ids come from a counter. The
last result is written to a real database. Aborts are timers of the same
clock.

Each record holds the scenario and what happened: every connection,
message (its bytes), answer, close and timeout, every host reading and
statistics call, with its time; the status after each step of the
progress; the result or the error the run ended with; the stored row; the
status after the run. Scenarios cover the estimate without reachable
listeners, each threshold (processor, memory, disk, a disk that was full
before), 500 connections, the error rate, failing connections after the
probe, failures in the step that crosses the threshold, a relay alone,
a relay that does not answer, half-open relay pairs, the second
host, slow connections and the 10-minute limit, failing statistics, the
environment variables and the API address, aborts in each phase, two runs
in a row, a failing save, and a result stored before.

`TestRunsAsNodeRanThem` replays each record against the Go service with a
fake clock, dialer and host, on the database it was recorded on.

## Running

From the worktree, in the dev image:

    tools/dev.sh "cd betterdesk-server/console/attestation/testdata/nodeoracle && node badge.js && node run.js"
    DEV_DOCKER_ARGS="--network bdgo" tools/dev.sh "cd betterdesk-server/console/attestation/testdata/nodeoracle && \
      ORACLE_PG_DSN='postgres://betterdesk:bdgo-local@bdgo-testpg:5432/betterdesk_test?sslmode=disable' node run.js"

`run.js` writes `../runs_sqlite.json.gz` and, with `ORACLE_PG_DSN`,
`../runs_postgres.json.gz` (in a schema of its own that it drops). Both
scripts write the same files on every run.
