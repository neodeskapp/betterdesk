# Recordings of the old console for console/routes/internal/jsreq

The files below are fixed records of what Express and JavaScript did in the
Node console, made while it still existed. They can't be recorded again.
The Go tests replay them and need no Node. Don't edit them: when a fix
changes a result on purpose, the test lists that input as an intended
difference with the row id.

| File | What it holds | Replayed by |
|---|---|---|
| `query.json` | what the Express query parser and JavaScript made of query strings, JSON values and path segments | `TestQueryMatchesExpress`, `TestValuesMatchJavaScript`, `TestSafeSegmentMatchesNode` (`jsreq_test.go`) |
| `dates.json` | what `Date.parse`, `new Date` and `JSON.stringify` made of texts and JSON values, in the time zone America/New_York | `TestParseDateMatchesJavaScript`, `TestDateOfMatchesJavaScript`, `TestStringifyMatchesJavaScript` (`date_test.go`) |

## What is recorded

`query.json` holds the answers of the real Express query parser and of
JavaScript's conversions on the values it gave.

`dates.json` holds its time zone with the answers. Its list `loose` holds
forms `Date.parse` reads that the Go port does not; the tests leave them
out.
