# Recordings of the old console for console/routes/trends

The file below is a fixed record of what the Node console answered, made
while it still existed. It can't be recorded again. The Go tests replay it
and need no Node. Don't edit it: when a fix changes an answer on purpose,
the test lists that scenario as an intended difference with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `trends.json` | scenarios: answers of a made-up API client and what the Node route made of them | `TestCollectMatchesNode`, `TestParseDateMatchesNode` (`collect_test.go`) |

## What is recorded

The recorder ran the Node route `web-nodejs/routes/trends.routes.js` with
a made-up API client and wrote down what it answered.
