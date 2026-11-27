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

The helper's viewer has one route of its own here, with the console
session of a signed-in account: `POST /api/support/devices/{id}/caller`
(below, "What the helper's viewer learns").

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
  RequestRelay for it),
- its first login request has no password, and
- the console's address is HTTPS (A273). On a console reached over plain
  HTTP the app does not ask, and quick support takes only the one-time
  password.

`session` is the relay session's UUID: `RequestRelay.uuid` as signal sent
it to the device, which the device used to reach the relay. The caller
does not choose what the device sends here: it is the relay session the
device itself joined.

### What the server checks

The server vouches when all of these hold, else it refuses:

| Check | Refusal `reason` |
|---|---|
| The app's request came over HTTPS, as the console tells it from the connection or a trusted proxy (`TRUST_PROXY`, `X-Forwarded-Proto`) | `not_https` |
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
{"success":true,"vouched":true,"device_id":"523456781","session":"<the relay session's UUID>","helper":"Olga Operator","helper_avatar":"data:image/jpeg;base64,/9j/4AAQ..."}
```

`helper` is the helper's first and last name as the console knows them,
else their user name. `helper_avatar` is the helper account's console picture as
`data:image/png;base64,...` (a picture with see-through parts) or
`data:image/jpeg;base64,...`, scaled down to at most 96 by 96 pixels and
at most 48 KB of text, the `data:` prefix included. It is left out when
the account has no picture or it can't be made that small (a WebP picture
among them, which the server does not decode). The audit log gets a `quick_support_caller` entry
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

The answer comes over the channel the app already hands the one-time
password over: the console's address from the link, with certificate
checks. Over plain HTTP someone on the way could fake it, so neither side
uses it there (A273): the app does not ask, and the server answers
`not_https`.

## What the helper's viewer learns (A273)

The computer does not tell the viewer why it offers no Allow; the console
knows what it told the app. The viewer of a helper whose quick support
session runs on the device asks, after it sent its login without a
password, at 2, 5 and 10 seconds:

```
POST /api/support/devices/{id}/caller
{"session": "<the viewer's relay session UUID>"}

200
{"success": true, "state": "not_allowed_yet", "app_https": true}
```

The account needs Connect to devices and an open quick support session on
the device (`404 {"error": "no_session"}` otherwise). `app_https` is
whether the app's last request came over HTTPS; it is left out when the
console has not heard from the app since it started (the app polls every
few seconds). `state` is what the console told the app about this relay
session:

| `state` | The viewer |
|---|---|
| `vouched` | keeps waiting: the person sees Allow |
| `not_allowed_yet` | fails with `consent_not_allowed`: the person has not clicked Allow in the quick support window yet |
| `not_https` | fails with `consent_needs_https` |
| `refused` | keeps waiting (any other refusal; the person sees Decline) |
| `not_asked` | with `app_https` false: `consent_needs_https`; otherwise asks again, and at the last check fails with `consent_needs_update`: an app that never asks is too old to accept by click |

The texts are in `docs/failure-reasons.md`. The console keeps the states
in memory for 10 minutes.
