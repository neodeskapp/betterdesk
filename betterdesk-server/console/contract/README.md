# Contract suite for the console

This suite checks that the Go console answers over HTTP what the old Node
console (`web-nodejs`) answered. The goldens in `testdata/golden` are fixed
records of the Node console's answers, made while it still existed. They
can't be recorded again and are the specification for the Go console. A
golden changes only when a fix changes an answer on purpose (see "Rewriting
goldens for a bug fix"); a new case gets its golden from the Go console (see
"Recording a new case").

The suite is a black box. It talks to a base URL and imports nothing from
the console.

## The two layers

**Matrix** (`TestMatrix`). One placeholder request per console route in
`testdata/routes.json`, for every role: signed out, then `pro`,
`guest_issuer`, `viewer`, `operator`, `global_admin`, `server_admin`,
`super_admin`, `admin`. Path parameters get `999999999`, an ID nothing has.
Writes send `{}` as JSON with the CSRF token. Per role all GET routes go
first (every twelfth also as HEAD), then the writes. The matrix shows who
may call what, what a refusal looks like, what the checks in front of a
route answer, and which headers go out. The sign-in answer of each role is
part of it (`matrix/_session/sign-in.json`). Role `pro` may not use the
console: its sign-in is refused and its requests go out without a session.

**Cases** (`TestCases`). Hand-written requests per route group, in
`cases_<group>_test.go`. They cover what the matrix cannot know: real
parameters, bodies, the JSON of a success, headers, error cases.

`TestEveryRouteIsCovered` fails when a console route gets no request from
either layer. Routes on the skip list (`matrix_skip.go`) need a case.

## Running it

Everything runs on test instances through one script (Git Bash):

    .ui-rework/go/tools/contract.sh check all
    .ui-rework/go/tools/contract.sh check cases/devices
    .ui-rework/go/tools/contract.sh check 'cases/(i18n|languages)'
    .ui-rework/go/tools/contract.sh check matrix STACK_DB=postgres
    .ui-rework/go/tools/contract.sh check cases --keep           # leave the instance up
    .ui-rework/go/tools/contract.sh check cases --on c1          # use the running bdgo-c1
    CONTRACT_REQUIRE_GO=1 .ui-rework/go/tools/contract.sh check all

    CONTRACT_REWRITE='cases/devices/(list|list-all)' .ui-rework/go/tools/contract.sh record cases/devices

`check` starts a fresh instance of the Go console, compares, prints the
summary and exits non-zero on a mismatch. `record` starts two fresh
instances and writes only the goldens `CONTRACT_REWRITE` names (see
"Frozen goldens"). `all` runs the matrix and the cases on separate
instances, because the matrix writes to everything it reaches.

To run the tests by hand against a console you started yourself:

    cd betterdesk-server/console/contract
    BD_URL=http://bdgo-c1:5000 go test -tags contract -count=1 -run '^TestCases$/^devices$'

Run `go test` from this folder without a package argument, otherwise it
hides the summary of a passing run.

| Variable | Meaning |
|---|---|
| `BD_URL` | The console to test. Required. |
| `BD_PASSWORD_FILE` | File with the password of the `e2e_<role>` accounts. Default `/stack/secrets/e2e_password`. |
| `BD_SECRETS_DIR` | Folder of the secrets cases send as `{secret:<file name>}` (see "Secrets"). Default `/stack/secrets`. |
| `CONTRACT_RECORD` | `1` writes goldens from this run. `merge` adds a second run to them (see below). Unset compares. |
| `CONTRACT_RECORD_NEW` | `1` with `CONTRACT_RECORD` also writes new goldens of requests the Go console answers (see "Recording a new case"). |
| `CONTRACT_REWRITE` | With `CONTRACT_RECORD`, a regular expression of golden names (`cases/devices/list`, `matrix/devices/GET_api_devices`) that a recording writes although the Go console answers them (see "Rewriting goldens for a bug fix"). |
| `CONTRACT_GOLDEN` | Golden folder. Default `testdata/golden`. |
| `CONTRACT_ROUTES` | Route list. Default `testdata/routes.json`. |
| `CONTRACT_REQUIRE_GO` | `1` fails every route and case the Go console did not answer itself. |
| `CONTRACT_COVERAGE` | `report` lists uncovered routes without failing. `contract.sh` sets it until the cases exist. |
| `CONTRACT_TIMEOUT` | Limit for one request. Default `60s`: a 50 MB upload takes 20 seconds on a loaded machine. |

Subtests are named `TestMatrix/<role>/<group>.<METHOD>_<path>` and
`TestCases/<group>/<role>/<case>`, so `-run` can pick a role, a group or one
request: `-run '^TestMatrix$/^viewer$/^devices\.'`.

The suite sends one request at a time, in a fixed order. It never prints the
password or a cookie. A second run on a used instance sees what the first
one wrote (settings changed by the matrix, an older session replaced at
sign-in), so some answers differ. Use a fresh instance for a real check.

## Writing a case

A group is one call in an `init` function:

```go
//go:build contract

package contract_test

import (
	"net/url"

	"github.com/unitronix/betterdesk-server/console/contract"
)

func init() {
	contract.Group("tickets",
		contract.Case{
			Name: "list", Path: "/api/tickets", Query: url.Values{"limit": {"10"}},
		},
		contract.Case{
			Name: "create", Method: "POST", Path: "/api/tickets",
			Body:    map[string]any{"title": "Printer", "priority": "low"},
			As:      "admin",
			Capture: map[string]string{"ticket": "data.id"},
		},
		contract.Case{
			Name: "read", Path: "/api/tickets/{ticket}",
		},
		contract.Case{
			Name: "rename", Method: "PATCH", Path: "/api/tickets/{ticket}",
			Body:  map[string]any{"id": "{ticket}", "title": "Printer on floor 2"},
			Roles: contract.Roles("admin", "operator", "viewer"),
		},
		contract.Case{
			Name: "delete", Method: "DELETE", Path: "/api/tickets/{ticket}", As: "admin",
		},
	)
}
```

How a group runs:

- The cases run in the order listed, once per role, least privilege first:
  every case for `signed-out`, then every case for `pro`, and so on.
  `Roles` limits a case to some roles. The default is all nine.
- `As` sends a case as one fixed role in every role's turn. Use it to set up
  and clean up: above, `admin` creates a ticket in each turn, the role whose
  turn it is reads it, `admin` deletes it.
- `Capture` takes values out of the JSON answer into variables. `{name}` in
  a later path, query, header or body of the same turn is replaced by the
  value. A body string that is only `"{name}"` becomes the value with its
  type, so a numeric ID is sent as a number. A variable nothing captured is
  `999999999`: a role that could not create the ticket asks for one that
  does not exist.
- `{console_host}` is there in every turn: the host name of the console
  under test (`bdgo-c1`). A case that points the console's network tools
  at the console itself uses it; loopback and 0.0.0.0 are refused by the
  console's guard.
- Groups run in the order of their names and share nothing but the instance.
  Leave it as you found it: delete what a group creates. Rate limit counters
  cannot be undone. Creating users and resetting passwords share a limit of
  three requests per five minutes per address in Node.
- Each role keeps its session for the whole run. When a group ends a session
  on purpose (a logout case), the cases after it in that group see it gone,
  and the next group signs in again.

Other fields of `Case`:

| Field | Use |
|---|---|
| `Headers` | Extra request headers. They replace the suite's own by name (`Accept`, `Accept-Language`, `Accept-Encoding`, `User-Agent`). |
| `RawBody`, `ContentType` | A body sent as it is: malformed JSON, a form. |
| `Multipart` | Parts of a `multipart/form-data` upload. |
| `NoCSRF` | Leave out the CSRF token of a write. |
| `NoSession`, `NoCookies` | Send without the session cookie, or without any cookie. The cookies of that answer are not kept. |
| `FollowRedirects` | Follow redirects on the console and record the last answer with the list of redirects. Without it the redirect is the answer. |
| `Volatile`, `VolatileHeaders` | Marks set by hand, see below. |
| `Covers` | Route IDs this case stands for in the coverage check, when method and path do not say it: `Covers: []string{"POST /api/auth/logout"}`. |
| `CaptureSecret` | `Capture` for a secret: see "Secrets" below. |
| `Compute` | Variables computed from earlier values: see "Computed values" below. |

`Capture` takes a value from a path of the JSON answer (`"data.id"`), from
a header (`"header:<Name>"`) or from a cookie the answer sets
(`"cookie:<name>"`). Headers and cookies are taken as the console sent
them; JSON values after the instance's address was replaced. The session
and CSRF cookies need no capture: each role's browser keeps them.

Defaults of every request: `Accept: application/json` for paths under
`/api/` and a browser's `Accept` for pages, `Accept-Encoding: gzip`,
`Accept-Language: en-US,en;q=0.9`, the CSRF token on writes, no redirects
followed.

`Group` panics on a case it cannot run, so a typo shows before the first
request. Do not name a group after an operating system or CPU (`windows`,
`linux`, `arm64`, `js`): Go would read `cases_windows_test.go` as a file for
that system only.

Record the goldens of a new case as "Recording a new case" says, look at
them, then run `contract.sh check cases` once to see that the group also
passes after the others.

## Secrets

A case can send a value that must never be written down: a device token,
a client API token, a two-factor secret. It names the value as
`{secret:<name>}`, in the path, a query value, a header or the body:

```go
contract.Case{
	Name: "device-init", Method: "POST", Path: "/api/bd/file-transfer",
	Headers: map[string]string{"Authorization": "Bearer {secret:device_token}"},
	Body:    map[string]any{"action": "init", "filename": "report.pdf", "size": 12},
	Capture: map[string]string{"transfer": "transfer_id"},
	NoCSRF:  true, NoCookies: true, Roles: contract.Roles("signed-out"),
},
```

The value comes from one of two places:

- **A file of the secrets folder** (`BD_SECRETS_DIR`, default
  `/stack/secrets`, which `stack.sh run` mounts from the instance). Every
  file there is a secret named after the file, its content without white
  space around it. A test instance has `device_token` (bound to device
  `900100001`) and `device_token_other` (bound to `900100002`), made by
  `devseed accounts` (`betterdesk-server/cmd/devseed`).
- **An earlier answer of the same turn**, taken with `CaptureSecret`
  (same sources as `Capture`). The value must be text, or a number, of at
  least eight characters.

The suite reads every file of the folder when it starts, and learns each
captured secret when it is captured. From then on the value is masked as
`{secret:<name>}` wherever it shows:

- The golden's `request` keeps the placeholder as the case wrote it.
- Every answer is masked before it is compared or recorded: the headers,
  the JSON (also a value the console wrote with an escape in it) and a text
  body before its length and hash are taken. The answer a secret was
  captured from is masked too. So a route that echoes a token gives
  `"secret": "{secret:totp}"` on every instance, and two recordings agree
  there without a volatile mark.
- Failure messages and the summary are masked.

A recording that would write a known value anyway, one the masking cannot
reach (a case that wrote a token into its own request, a token inside a
binary body), does not write that golden, fails, and names the secret in
the summary. A secret shorter than eight characters is refused: it could
stand in an answer by chance. Short one-time codes are plain variables.

## Computed values

`Compute` sets variables from functions of earlier values, right before
the request is sent: variable name to `function(variable)` or
`function(secret:name)`. The list of functions is closed (`compute.go`):

| Function | Value |
|---|---|
| `totp(x)` | The two-factor code of the base32 secret `x` for the current step of 30 seconds, as `console/authn` computes it (RFC 6238, HMAC-SHA1, 6 digits). With less than three seconds of the step left it waits for the next step, so the console still takes the code. |
| `totp_next(x)` | The code for the first step after the step of the last code this turn computed for `x` (without one, the step after now), waiting for that step when it has not begun. For a case that must send a code the console has not seen. A case has at most one. |

```go
contract.Case{
	Name: "totp-setup", Method: "POST", Path: "/api/auth/totp/setup", Body: map[string]any{},
	CaptureSecret: map[string]string{"totp": "secret"},
},
contract.Case{
	Name: "totp-enable", Method: "POST", Path: "/api/auth/totp/enable",
	Compute: map[string]string{"code": "totp(secret:totp)"},
	Body:    map[string]any{"code": "{code}"},
},
```

A code is a plain variable: it is good for 30 seconds and tells nothing of
the secret. The golden's request notes it as `computed: code = totp(secret:totp)`.

## Websockets

A case with `Socket` is a step on a websocket at its `Path` (a GET, no
body). The first step of a role's turn that names a socket opens it with
the role's cookies (`NoSession` and `NoCookies` work as for requests), the
case's `Headers` and `Query`, and an `Origin` of the console's own address
unless the socket says otherwise. Later steps with the same `Name` work on
the open socket. Each step sends `Send`, then collects what the console
sends until `Expect` messages came, the console closed the socket
(`WaitClose` waits for that), or `Wait` ran out (5 s), and listens `Quiet`
longer (300 ms) so a message too many shows. `Close` closes from the
suite's side. The sockets of a turn are closed when the turn ends.

```go
contract.Case{Name: "open", Path: "/ws/device-status", Socket: &contract.Socket{Name: "status"}},
contract.Case{Name: "change-id", Method: "POST", Path: "/api/devices/900100018/change-id", As: "admin",
	Body: map[string]any{"newId": "900100098"}},
contract.Case{Name: "id-changed", Path: "/ws/device-status",
	Socket: &contract.Socket{Name: "status", Expect: 1, Filter: map[string]string{"type": "device_id_changed"}}},
```

The answer of a step that opens a socket the console refuses is that
refusal: status, headers, body. Otherwise the status is 101 and the body is
JSON the usual rules compare (volatile values, captures, secrets):

```json
{"protocol": "chat",
 "messages": [{"json": {"type": "welcome", "device_id": "900100001"}, "order": "type device_id"},
              {"text": "not JSON"}, {"binary": {"length": 4, "sha256": "..."}}],
 "closed": {"code": 4403, "reason": "Invalid token"}}
```

`order` lists the keys of every object of a JSON message in the order the
console wrote them, so a change of order fails although JSON is compared by
value. `Filter` keeps only messages with the given top-level values, for a
socket where the console also pushes what other things cause (device
status changes the signal server reports). A step on a socket that was
refused or has closed answers `{"socket": "not open"}` with the status of
its opening step. `Capture` and `CaptureSecret` read the body like any JSON
answer (`"messages[0].json.token"`).

A binary message is length and hash, so one byte that belongs to the
machine makes the whole message differ. `Frames: true` writes the binary
messages of a step as the protobuf messages in them, in the frames of
RustDesk's BytesCodec (the signal server's messages on `/ws/rendezvous`),
field by field without the `.proto` files: `{"frames": [{"11": {"3": 2,
"4": "203.0.113.7:21117"}}]}`. A field is a number, a string when it is
printable text, a message when it reads as one, else `"hex:..."`; a field
that comes more than once is an array. Every byte is still compared, and
a case can free the one field that differs:
`Volatile: []string{`~messages[0].frames[0]["11"]["4"]`}` (the relay
address in the answer to a punch hole, which the signal server detects
for its machine). A binary message that is not such frames stays length
and hash.

The client is part of the suite (`socket.go`, standard library only). The
upgrade of a socket case goes outside the suite's HTTP client, so its
answer has none of the default request headers but `User-Agent`.

## Recording a new case

The Go console answers every route, so its goldens are frozen and a new
case cannot be recorded the usual way. With
`CONTRACT_RECORD_NEW=1` a recording also writes the golden of a request the
Go console answers when that golden is new: it does not exist, or it holds
only the first of the two recordings (`"recordings": 1`), which is what the
first half of the same recording left. A golden with both recordings is
never written; the summary counts the new ones (`recorded new: N`).

Use it only for a case whose answers were checked against what Node
answered (the recording of the old console that the group's package
replays), and say so in the case file. `contract.sh` does not pass the
variable on; run the two halves by hand on two fresh instances:

    stack.sh up ft1; stack.sh up ft2
    DEV_DOCKER_ARGS="-e CONTRACT_RECORD=1 -e CONTRACT_RECORD_NEW=1" stack.sh run ft1 \
      "cd betterdesk-server/console/contract && go test -tags contract -count=1 -run '^TestCases\$/^tickets\$'"
    DEV_DOCKER_ARGS="-e CONTRACT_RECORD=merge -e CONTRACT_RECORD_NEW=1" stack.sh run ft2 \
      "cd betterdesk-server/console/contract && go test -tags contract -count=1 -run '^TestCases\$/^tickets\$'"

## Rewriting goldens for a bug fix

A bug fix changes what Go answers on purpose, so the goldens of the routes
it touches have to change with it, and only those. `CONTRACT_REWRITE` names
them: a regular expression that must match a golden's whole name, its path
under the golden folder without `.json` (`cases/devices/list`,
`matrix/devices/GET_api_devices`, `cases/devices/(list|list-all)`). A
recording then writes those goldens from Go's answers, merging the two
recordings as for any golden; every other golden of a Go route stays frozen.
The summary counts them (`rewritten: N`). It needs `CONTRACT_RECORD`:

    CONTRACT_REWRITE='cases/devices/(list|list-all)' contract.sh record cases/devices

Look at the diff of the rewritten files before committing: only what the
fix changes may differ. The commit of the fix carries them.

## Frozen goldens

A golden is what the Node console answered. The goldens are the
specification and nothing may change them, least of all Go's own answer,
but for `CONTRACT_REWRITE` and `CONTRACT_RECORD_NEW` above. A recording
asks the routing report (see "Who answered") about every matrix route and
every case:

- Go answers: the request is sent, because a later step of the group may
  need what it creates or captures, but the golden file is neither read nor
  written. A route that has no golden yet gets none.
- Nobody answers, or the report says nothing: the test fails and the golden
  stays as it is.

A recording against a console without the routing report stops before the
first request. The summary of a recording ends with the count:

```
  wrote 31 golden files with 12 volatile paths
  frozen: 87 (goldens of requests the Go console answers, left as they are)
```

A first recording also removes the golden files that no route and no case
stands for any more (a case that was renamed or deleted). A leftover file
stays when the Go console answers the request it names.

The hand marks (`Volatile`, the tables in `matrix_params.go`) and the skip
list work as before for the goldens that are recorded. A mark added to a
case whose golden is frozen changes nothing, because the file is not
written again.

## Goldens

One file per matrix route (`testdata/golden/matrix/<group>/<METHOD>_<path>.json`)
and per case (`testdata/golden/cases/<group>/<name>.json`). `request`
describes what was sent and is not compared. `answers` holds each distinct
answer once, with the roles that got it:

```json
{
 "id": "GET /api/devices/:id",
 "request": {"method": "GET", "path": "/api/devices/999999999"},
 "recordings": 2,
 "answers": [
  {
   "roles": ["signed-out", "pro"],
   "status": 401,
   "headers": {"content-type": "application/json; charset=utf-8", "set-cookie": "__csrf_h=<value>; Path=/; HttpOnly; SameSite=Lax"},
   "body": {"kind": "json", "json": {"success": false, "error": "Unauthorized"}}
  }
 ]
}
```

An answer is:

- **The status.** `"failed": "timeout"` or `"connection"` stands in when no
  answer came within the limit.
- **The headers on the allowlist** (`keptHeaders` in `answer.go`), names in
  lower case: content type, encoding, disposition and language, the caching
  headers, `Location`, `Vary`, `Allow`, `WWW-Authenticate`, `Retry-After`,
  the `RateLimit` headers, `Set-Cookie`, the security headers and every
  `Access-Control-*`. All other headers (`Date`, `Content-Length`, ...) are
  ignored. Some values are reduced to what means something:
  - `Set-Cookie` keeps the cookie's name and attributes: the value becomes
    `<value>` (an empty value stays empty: the cookie is cleared), `Expires`
    becomes `<date>` or `<past>`, a positive `Max-Age` becomes `<n>`.
    Cookies are sorted by name.
  - `ETag` and `Last-Modified` become `<present>`.
  - `RateLimit-Remaining`, `RateLimit-Reset` and `Retry-After` keep `<n>`
    for their numbers. `RateLimit-Limit` and `RateLimit-Policy` are compared
    as sent.
  - A nonce in a `Content-Security-Policy` becomes `'nonce-<nonce>'`.
  - In a `Content-Security-Policy` of an HTML page, a `'sha256-...'` that
    is the hash of an inline script of the same page becomes
    `'sha256-<inline-script>'`. A hash that matches no script of the page
    stays as sent, so a wrong one still shows.
- **The body.** JSON (by content type) is kept parsed. Anything else is kept
  as media type, length and SHA-256, and for text also the first 300 bytes
  to make a diff readable. An event stream is not read.
- **The web app's page** (`text/html`) is kept in a form that every build
  of the web app gives: the hash of its own inline script in its `<meta>`
  policy becomes `'sha256-<inline-script>'` as in the header, the text of
  SvelteKit's start script (the inline script that names `__sveltekit_`)
  becomes `<start-script>`, and the build's hashed files become
  `/_ui/immutable/<build-file>`. The rest of the page is compared: its
  template, the tags the server puts in (`bd-error`), how many files it
  loads and in which order. Not pinned: which build it is, the names of
  its files and what the start script says.

In headers and bodies the instance's own address becomes `<base>`
(`http://bdgo-c1:5000`), `<host>` or `<hostname>`, the IP address the
suite connects to becomes `<console-ip>`, and the address the requests
come from becomes `<client-ip>`, so two instances give the same text.
Length and hash of a text body are taken after that.

## Volatile values

Some values differ on every instance: IDs, times, tokens. `record` finds
them by running the suite against two fresh instances. The first run writes
what it got (`"recordings": 1`). The second run is merged in
(`CONTRACT_RECORD=merge`, `"recordings": 2`):

- Where the two answers differ in a JSON value of the same type, the path is
  marked in `"volatile"`. When every element of an array differs at the same
  key the mark uses `[*]` (`data.items[*].created_at`), otherwise the index.
- Where the arrays have different lengths, the mark is the length:
  `data.items[#]`, written `{"path": "data.items[#]", "elements": true}`.
  The golden keeps the first element of the first recording as the sample
  of all elements, and every element of both recordings is compared with
  it: what differs in value is marked below the array
  (`data.items[*].id`), a difference in keys or type fails the recording.
  A later answer may have any number of elements, each like the sample: the
  same keys, the same values except at the marks, the formats of the marks.
  A length mark without `"elements"` comes from a golden recorded before:
  its sample was emptied, and each element only has to be of its JSON type.
- A body that is not JSON and differs is volatile as a whole: `$`.
- A value both recordings agree on is still marked when it holds a date
  within 45 days of the recording, or is a Unix time in that range. Both
  instances say `2026-11-10`; tomorrow's console does not.
- Where status, an allowlisted header, a JSON type or the keys of an object
  differ, the recording fails and names the request. That is the console
  answering differently for a reason somebody has to look at. If the
  difference is fine, mark it by hand: `Volatile: []string{"data.sessions"}`
  on a case (a whole object whose keys change) or `VolatileHeaders`; for a
  matrix route the tables `matrixVolatile` and `matrixVolatileHeaders` in
  `matrix_params.go`. Mark by hand as well what two recordings agree on by
  chance: the memory a process uses, the offset of the clock.

Volatile values are masked in the golden (`"<volatile>"`, `0`, `false`), so
no token of a test instance is committed and recording again changes little.

A volatile value is free, but its type and its format are not:

- A volatile **string** keeps its format. The format of a string is its
  class: every digit becomes `9`, every ASCII lower-case letter `a`, every
  ASCII upper-case letter `A`, everything else stays. `2026-11-10 01:02:03`
  has the class `9999-99-99 99:99:99`. When all values at the path have the
  same class, the mark stores it as `"format"`. When they differ only in
  the length of a run of digits or letters, the mark stores a `"pattern"`:
  a regular expression for the class with `9+`, `a+` or `A+` for those runs
  (`^9+\.9+\.9+\.9+$` for an IPv4 address). So a timestamp that turns from
  `2026-11-10 01:02:03` into `2026-11-10T01:02:03.000Z` fails although the
  path is volatile.
- Random tokens get a looser pattern. A string where letters and digits
  touch (`mwa1slae`, a hash, a UUID, a key in base64) counts as a token
  unless its letters are the same in every sample, like the `T` and `Z` of
  a timestamp. In a token the runs fall where chance puts them, and so do
  `+`, `/`, `-`, `_` and `=`, so each stretch of those characters, letters
  and digits is one word: `[9aA+/_=-]{n}` when every sample has `n`
  characters there, `[9aA+/_=-]+` otherwise. A UUID gives
  `^[9aA+/_=-]{36}$`: the length and the alphabet are checked. Strings whose
  classes differ in more than run lengths get the word pattern too when
  their words line up, and nothing when they do not; then only the type is
  checked.
- A fraction of exactly nine digits may be shorter or missing
  (`(\.9{1,9})?`). Those are nanoseconds as Go prints them, and Go leaves
  out the zeros at the end.
- The samples for a path are the values of every role that got the same
  status, in both recordings. More samples make a format less a matter of
  chance, but a format can still be too tight: two instances that both say
  `"17"` give `99`, and `"105"` fails later. Record again, or mark the path
  by hand with a `~` in front (`"~data.uptime"`), which frees it of the
  format: only the type is checked there.
- A volatile **number** that was a whole number in both recordings is marked
  `"integer": true` and must stay whole.
- A mark set by hand on an object or array checks the type only. Mark the
  field that changes (`users[*].last_login`), not the list, or the length
  (`users[#]`) for a list that grows.
- A list whose elements come in no fixed order (a role's permissions) is
  marked by hand with `@` in front (`contract.AnyOrder + "data.permissions"`,
  `{"path": "data.permissions", "anyOrder": true}` in the golden). It is not
  volatile: it must hold the same elements, each as often, in any order.
  The golden keeps them sorted, and two recordings with other elements fail
  the recording.
- A key that only some objects have (the details of some audit events in a
  list compared with one sample, the last sign-in of an account that has
  signed in) is marked by hand with `?` in front
  (`contract.Optional + "events[*].details"`, `"optional": true`). It may be
  missing on either side; where it is there, its value is volatile.

```json
"volatile": [
 "data.id",
 {"path": "data.created_at", "format": "9999-99-99 99:99:99"},
 {"path": "data.count", "integer": true},
 {"path": "data.ip", "pattern": "^9+\\.9+\\.9+\\.9+$"}
]
```

## Comparing

An answer matches its golden when the status is equal, the allowlisted
headers are equal, and the bodies are equal: the same keys (a missing key is
not `null`), arrays in the same order and length, numbers by value. At a
volatile path the rules above apply.

## Reading a failure

```
--- FAIL: TestMatrix/viewer/devices.GET_api_devices__id
    GET /api/devices/:id as viewer differs from testdata/golden/matrix/devices/GET_api_devices__id.json
        status: expected 404, got 500
        header cache-control: missing: expected "no-store", got no header
        body error: expected "Device not found", got "Internal Server Error"
        body data: missing key "total"
        body data.items[0].last_online: format of a volatile value: expected format 9999-99-99 99:99:99, got "2026-11-10T01:02:03.000Z" (9999-99-99A99:99:99.999A)
```

The first line names the request, the role and the golden. Each line after
it is one difference: where, what the golden expects, what the console
sent. For a key that names a secret (`token`, `secret`, `password`, `key`,
`cookie`, `sid`) a message shows the class of the value, never the value.
`answered by node, not by the Go console` is the failure of
`CONTRACT_REQUIRE_GO=1`.

The run ends with a summary: answers passed and failed per group, how many
routes the Go console answers itself, and findings: requests that got no
answer, requests that ended the session, requests slower than three
seconds.

```
== contract summary: http://bdgo-ct-1a2b3c-matrix:5000, compared with testdata/golden ==
  matrix/devices                          531 passed      0 failed
  ...
  total                                  6800 passed      0 failed   (55 groups, 2m10s)
  answered by Go: 757 of 757 matrix routes (node 0, nobody 0)
```

## Who answered

A Go console started with `CONSOLE_DEBUG_ROUTING=1` (the test stack sets
it) answers `GET /__bd/routing?method=GET&path=/api/x` with
`{"handler":"go"}`: it answers every request itself. The suite asks once
per route and case. Without `CONTRACT_REQUIRE_GO` the summary
only counts. For a socket case it adds `upgrade=websocket` and the answer
says whether a Go socket takes the upgrade.

## The route list and the skip list

`testdata/routes.json` is a trimmed copy of the route inventory. Make it
again after the inventory changed:

    cd betterdesk-server/console/contract && go generate

(in the dev container, where the inventory is at `/notes/inventory.json`).

Routes of other apps than `console` (the client API on port 21121) and
routes the inventory marks as not reachable are not sent, and the coverage
check does not ask for them. Where the console registers the same method
and path twice (`GET /api/devices` and `GET /api/devices (2)`), one request
stands for both and is filed under the one registered last: the first steps
aside for requests without a bearer token, which the matrix never sends. A
route registered for every method (`ALL`) is sent as GET and as POST.

`matrix_skip.go` lists the routes that get no placeholder request, each with
its reason: it stops, restarts, updates or restores the server, runs
commands, ends or changes the caller's session, password or second factor,
sends mail, calls out to the internet, writes into the source folder the
test instances share, or loads the server for minutes. `GET /*`, the web
app's pages, is on the list as well: pages get cases. The rule for
borderline routes is in the comment above the list.
`TestEveryRouteIsCovered` names the skipped routes that have no case yet.

`matrix_params.go` holds the placeholders. A GET route can be pointed at
something that exists (`matrixRouteParams`, seeded devices are `900100001`
to `900100020`). Writes always get IDs that do not exist. `matrixQueries`
gives a GET route a query where its default order changes by the minute
(`GET /api/devices` is sorted by last contact; the matrix asks for
`sortBy=id`). The marks set by hand for matrix routes are there too, each
with its reason.
