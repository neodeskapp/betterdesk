# Quick support app protocol

What the NeoDesk quick support app sends to the console and what it gets
back. The client (neodeskapp/neodesk-client, `src/onboard/support.rs` and
`src/onboard/caller.rs`) and this package keep to this file.

## Routes

All routes are on the console's address (`https://<console host>`), take
and answer JSON, and need no console session or CSRF token. Every route
but the redeem needs `Authorization: Bearer <session token>`; a token of a
session that ended, or none, gets `401 {"success":false,"error":"invalid_session"}`.

| Route | Body | What it does |
|---|---|---|
| `POST /api/support/redeem` | `{code}` | Spends the support code; answers the session token, the supporter's name and how to reach the server. |
| `POST /api/support/session/device` | `{device_id, uuid, hostname, platform, version}` | The device the app runs on. A refused device is `403 {"error":"device_refused","reason":...}` (docs/failure-reasons.md). |
| `POST /api/support/session/consent` | `{allowed, grants?}` | The person's answer in the quick support window. |
| `POST /api/support/session/password` | `{password}` | The one-time password for the supporter, kept in memory. |
| `GET /api/support/session` | none | The session's state; the app polls it every few seconds. |
| `POST /api/support/session/end` | `{}` | The app closed. |
| `POST /api/support/session/caller` | `{session}` | Whether a connection that came without the password is a signed-in helper (below). |

## The caller check (A271)

The person at the computer may accept a connection by click instead of
the one-time password only when the server vouches that the connection is
the browser viewer of a signed-in console helper. Every other connection
needs the password. The app never trusts what the connection says about
itself (its ID, its name); it asks the server.

### When the app asks

The app asks once per incoming connection, when all of these hold:

- the person allowed quick support (the session's status is `allowed`),
- the connection came through the relay (the device got signal's
  RequestRelay for it), and
- its first login request has no password.

`session` is the relay session's UUID: `RequestRelay.uuid` as signal sent
it to the device, which the device used to reach the relay. The caller
does not choose what the device sends here: it is the relay session the
device itself joined.

### What the server checks

The server vouches when all of these hold, else it refuses:

| Check | Refusal `reason` |
|---|---|
| The quick support session is `allowed` and names its device | `session_state` |
| The relay session runs, to the session's device, from the console's browser viewer (`panel-web-remote`), for a console account and a signed-in browser session of it | `session_not_open` |
| Both sides met on the relay at most 60 seconds ago | `too_late` |
| The server has not vouched for this relay session before | `already_used` |
| The account exists, is not disabled, has Connect to devices (`device.connect`) and the device is in its scope | `no_permission` |

Signal issues a relay ticket for the browser viewer only with the panel's
proof of the signed-in account, the relay pairs exactly two sides on one
ticket, and a used UUID is not issued again. So a relay session that
passes these checks is the helper's viewer and nothing else.

### Answers

The server vouches:

```
200
{"success":true,"vouched":true,"device_id":"523456781","session":"<the relay session's UUID>","helper":"Olga Operator"}
```

`helper` is the helper's first and last name as the console knows them,
else their user name. The audit log gets a `quick_support_caller` entry
about the helper before the answer goes out; when it can't be written the
answer is `500 {"error":"audit_failed"}` and nobody is vouched for.

The server does not vouch:

```
409
{"success":false,"error":"not_vouched","reason":"session_not_open"}
```

A refusal for `no_permission` writes a `quick_support_caller_refused`
entry about the account. A bad body is `400 {"error":"invalid_body"}`.

### What the app does with the answer

The app offers accepting by click only for a `200` whose `vouched` is
`true`, whose `device_id` is its own ID, whose `session` is the relay
session it asked about and whose `helper` is not empty. It shows the
person `helper` as the name and "From the NeoDesk web console" under it.

Anything else means no click: a `409`, a `404` (a server without this
route), a `401`, any other status, a body it can't read, an answer that
names another device or relay session, no answer within 5 seconds, or a
connection that did not come through the relay. The connection then
needs the password as before, and the connection manager window names
the caller unknown and offers only Decline.

A server without this route keeps working with the app: the helper
connects with the password the app handed over. A client without the
check keeps working with this server; the server only answers when asked.

### Trust

The answer is as trustworthy as the channel the app already hands the
one-time password over: the console's address from the link, with
certificate checks on HTTPS. On a console that runs on plain HTTP, someone
who can change that traffic could fake the answer, but could equally read
the one-time password and connect without any click.
