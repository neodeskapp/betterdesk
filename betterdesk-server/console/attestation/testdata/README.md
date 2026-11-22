# Recordings of the old console for console/attestation

The gzipped files here are fixed records of what the Node console's
`services/serverAttestation.js` did, made while it still existed. They
can't be recorded again. The Go tests replay every record and need no
Node. Don't edit them: when a fix changes a run on purpose, the scenario
goes into `runFixes` in `run_test.go` with the row id, and
`ATTESTATION_RECORD_FIXED=1` writes its differences from the Go run into
`runs_fixed.json`.

| File | What it holds | Replayed by |
|---|---|---|
| `badge.json.gz` | the `attestation` of the sign-in page for 14,522 combinations of stored result, badge texts, brand and language | `TestSignInBadgeAsNodeMadeIt` |
| `runs_sqlite.json.gz`, `runs_postgres.json.gz` | 44 capacity tests in 42 scenarios, per database | `TestRunsAsNodeRanThem` |
| `runs_fixed.json` | written by the Go test: how the scenarios a fix changed differ from the recording | `TestRunsAsNodeRanThem` |

## What is recorded

The badge records were computed as `GET /api/auth/login-page` did, from the
stored test result (`server_attestation_result`), the stored badge texts
(`server_attestation_badge_config`), the brand name and the texts of the
request language, and for a settings table that could not be read. The
values are hostile on purpose: every JSON type, tiers named like what every
JavaScript object has, texts that need trimming and cutting at their
limits, JSON that cannot be read.

The capacity tests ran in virtual time. The service was compiled with a
fake clock in place of `setTimeout`, `setInterval` and `Date`, so its waits
took no time; a fake `ws` played the connections a scenario scripted
(opens, refusals, connections that never answer, answers to the health
check and when they came), the host readings and the API server's
statistics were scripted, and the bytes of the relay session ids came from
a counter. The last result was written to a real database.

Each record holds the scenario and what happened: every connection,
message (its bytes), answer, close and timeout, every host reading and
statistics call, with its time; the status after each step of the
progress; the result or the error the run ended with; the stored row; the
status after the run. Scenarios cover the estimate without reachable
listeners, each threshold (processor, memory, disk, a disk that was full
before), 500 connections, the error rate, failing connections after the
probe, failures in the step that crosses the threshold, a relay alone, a
relay that does not answer, half-open relay pairs, the second host, slow
connections and the 10-minute limit, failing statistics, the environment
variables and the API address, aborts in each phase, two runs in a row, a
failing save, and a result stored before. The Go test replays each record
with a fake clock, dialer and host, on the database it was recorded on.
