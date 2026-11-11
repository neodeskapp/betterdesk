# Node oracle for the address parser

`addressparser.js` runs the address parser of the nodemailer version the Node
console has installed on about 23,000 inputs and writes what it returns to
`../addressparser.json`. The Go test `TestNodeVectors` replays that file and
compares every result with `Parse` and `Flatten`.

The script and the vector file are fixtures for the move from Node to Go. They
go away together with `web-nodejs`.

## Running it

In the dev image, from `betterdesk-server/console/mail/addressparser`:

```sh
node testdata/nodeoracle/addressparser.js
go test . -run TestNodeVectors -count=1
```

The inputs are written by hand or built by a seeded generator, so every run
writes the same file as long as nodemailer stays the same.

## The vector file

One vector per line: `in` is the input, `out` is `addressparser(in)`, and
`flat` is `addressparser(in, {flatten: true})`, left out where it equals
`out`. Everything outside printable ASCII is written as `\uXXXX`.

A few inputs hold lone surrogates. Go reads each of them as U+FFFD, in the
input and in the result. The script leaves out the inputs where the module
itself gives another result for the U+FFFD form, which happens when it drops
a character between two halves and so builds a pair. It prints how many.

## Checking other inputs

To compare Go and Node on inputs from somewhere else, such as the corpus the
fuzzer built, put them into a JSON array of strings and run:

```sh
node testdata/nodeoracle/addressparser.js --inputs inputs.json --out vectors.json
ADDRESSPARSER_VECTORS=vectors.json go test . -run TestExtraNodeVectors -count=1 -v
```
