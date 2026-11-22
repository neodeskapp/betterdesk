# Recordings of the old console for console/mail/addressparser

The file below is a fixed record of what the address parser of the
nodemailer version the Node console had installed returned, made while
the console still existed. It can't be recorded again. The Go tests replay
it and need no Node. Don't edit it: when a fix changes a result on
purpose, the test lists that input as an intended difference with the row
id.

| File | What it holds | Replayed by |
|---|---|---|
| `addressparser.json` | about 23,000 inputs and what `addressparser` returned for each | `TestNodeVectors` (`vectors_test.go`), and `TestVectorsKeepTheRules` and the seeds of `FuzzParse` (`fuzz_test.go`) |

## What is recorded

One vector per line: `in` is the input, `out` is `addressparser(in)`, and
`flat` is `addressparser(in, {flatten: true})`, left out where it equals
`out`. Everything outside printable ASCII is written as `\uXXXX`. The
inputs were written by hand or built by a seeded generator.

A few inputs hold lone surrogates. Go reads each of them as U+FFFD, in the
input and in the result. The inputs where the module itself gave another
result for the U+FFFD form were left out; that happened when it dropped a
character between two halves and so built a pair.
