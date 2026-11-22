# The console's rate limiters

Every limiter counts in fixed windows per key, in memory. A request is
refused with 429 when its count in the window is above the maximum. All
answers a limiter counted carry `RateLimit-Policy`, `RateLimit-Limit`,
`RateLimit-Remaining` and `RateLimit-Reset`; a refused one also
`Retry-After`. When several limiters count one request, the client sees
the headers of the last one.

`W` is `RATE_LIMIT_WINDOW_MS` (default 60000). "Address" is the client's
address under `TRUST_PROXY`, else the `X-Forwarded-For` header as sent,
else `unknown`. "User" is the id of the signed-in user. A "signed-in
read" is a GET or HEAD with a session.

## Limiters of the console port

| `Set` field | Node name | Window | Maximum (variable, default) | Key | Skips | 429 text |
|---|---|---|---|---|---|---|
| `API` | `apiLimiter` | `W` | `RATE_LIMIT_MAX`, 100 | address | signed-in reads, GET and HEAD of poll paths and of paths below `/api/dashboard/` and `/api/panel/`, GET and HEAD of settings read paths and profile pictures, `POST /api/desktop/layout` | Too many requests. Please try again later. |
| `SessionRead` | `sessionReadLimiter` | `W` | `PANEL_READ_RATE_LIMIT_MAX`, 600 | `session-read:<user>` | everything that is not a signed-in read | Too many requests. Please slow down. |
| `Widget` | `widgetLimiter` | 60 s | `WIDGET_RATE_LIMIT_MAX`, 600 | address | signed-in reads | Too many widget requests. Please slow down. |
| `PanelRead` | `panelReadLimiter` | `W` | `PANEL_READ_RATE_LIMIT_MAX`, 600 | `panel-read:<user>`, signed out the address | everything that is not a GET or HEAD | Too many panel read requests. Please slow down. |
| `PanelPreference` | `panelPreferenceLimiter` | 60 s | `PANEL_PREFERENCE_RATE_LIMIT_MAX`, 30 | `pref:<user>`, signed out the address | nothing | Too many layout save requests. Please slow down. |
| `RDClientPage` | `rdClientPageLimiter` | 60 s | `RDCLIENT_PAGE_RATE_LIMIT_MAX`, 120 | address | nothing | Too many remote page requests. Please slow down. |
| `Login` | `loginLimiter` | `W` | `LOGIN_RATE_LIMIT_MAX`, 5 | address | nothing | Too many login attempts. Please try again in a minute. |
| `TOTPVerify` | `totpVerifyLimiter` | `W` | `LOGIN_RATE_LIMIT_MAX`, 5 | address | nothing | Too many verification attempts. Please try again in a minute. |
| `PasswordChange` | `passwordChangeLimiter` | 5 min | 3, fixed | `password:<user>`, signed out the address | nothing | Too many password change attempts. Please try again later. |
| `Upload` | `uploadLimiter` | 60 s | `UPLOAD_RATE_LIMIT_MAX`, 30 | address | nothing | Too many upload requests. Please try again later. |
| `FileAccess` | `fileAccessLimiter` | 60 s | `FILE_ACCESS_RATE_LIMIT_MAX`, 120 | address | nothing | Too many file requests. Please try again later. |
| `Register` | `registerLimiter` | 60 s | `BD_REGISTER_RATE_LIMIT_MAX`, 20 | address | nothing | Too many registration requests. Please slow down. |
| `Invitation` | `invitationLimiter` (Go only) | 15 min | 20, fixed | address | nothing | Too many invitation requests. Please try again later. |

Every 429 body is `{"success":false,"error":"<text>"}`.

A variable that is not a number, or zero, gives the default. Text after
the digits is ignored (`7.9` and `7abc` are 7). A negative maximum refuses
every request.

## In front of every route: `Set.AppLevel()`

Node mounted these in `server.js` (step 11 of the pipeline) with
`app.use(path, limiter)`: the path itself and everything below it, in any
letter case. A request passes them in this order. Each limiter counts a
request once, also when the request is under two of its mounts or meets
the limiter again on its route. The first limiter that refuses ends the
request.

| Order | Mounted at | Limiter |
|---|---|---|
| 1 | `/api/` | `SessionRead` |
| 2 | each poll path (below) | `Widget` |
| 3 | each settings read path (below) | `PanelRead` |
| 4 | `/api/panel`, `/api/dashboard` | `Widget` |
| 5 | `/api/desktop/layout` | `PanelPreference` |
| 6 | `/api/` | `API` |

Poll paths: `/api/stats`, `/api/server/status`, `/api/server/bandwidth`,
`/api/devices`, `/api/audit/conn`, `/api/dashboard/client-config`,
`/api/dashboard/activity`, `/api/registrations/count`,
`/api/network/targets`, `/api/tickets/stats`, `/api/cdap/devices`,
`/api/users`, `/api/audit-log`, `/api/system/info`,
`/api/docker/containers`, `/api/settings/restart-status`,
`/api/folders`, `/api/tags`,
`/api/device-groups`, `/api/bd/notifications`.

Settings read paths: `/api/settings/` followed by `audit`, `info`,
`restart/pending`, `device-scope`, `connection-mode`, `public-endpoints`,
`branding`, `appearance`, `branding/backgrounds`, `branding/export`,
`branding/profiles`, `themes`, `fonts`, `fonts/local`, `backup/stats`,
`updates/channel`, `updates/last-result`, `updates/server-binary/status`,
`backup/retention`, `enrollment`, `client-sessions`, `ldap`, `oidc`,
`email/smtp`.

What that adds up to:

- A signed-in person's GET or HEAD below `/api`: `SessionRead` only, plus
  `PanelRead` on the settings read paths and `PanelPreference` on
  `/api/desktop/layout`.
- Anything else below `/api` (writes, and everything without a session):
  `API` per address, unless `API` skips it. Requests at or below a poll
  path, `/api/panel` or `/api/dashboard` also count against `Widget`.
- The mounts match below the path too, the skips of `API` only the exact
  path: `GET /api/devices/7` without a session counts against `Widget`
  and `API`, `GET /api/devices` against `Widget` alone.
- Paths below two read paths count once against `PanelRead`:
  `/api/settings/branding/backgrounds`, `/branding/export`,
  `/branding/profiles` and `/api/settings/fonts/local`. Writes below a
  read path do not count against it.
- `GET /api/dashboard/<anything>` without a session counts against
  `Widget`.
- The mounts match in any letter case, the path lists of the skips do
  not: `GET /API/devices` counts against `Widget` and `API`. The skips
  read the path as sent: `GET /api//devices` counts against `API`.

## On routes: `Limiter.Middleware`

Paths are as written in the Node route file; the tickets and file
transfer routers are mounted below a prefix. "First" means the limiter
runs before the sign-in check, so requests without a session count too.

| Limiter | Routes (Node file) | Position |
|---|---|---|
| `Login` | `POST /api/auth/login` (`auth.routes.js`) | first |
| `TOTPVerify` | `POST /api/auth/totp/verify` (`auth.routes.js`) | first |
| `PasswordChange` | `POST /api/auth/password` (`auth.routes.js`) | after the sign-in check |
| `PanelRead` | `GET /api/users/avatars`, `GET /api/users/:id/avatar` (`avatars.routes.js`) | after the sign-in check |
| `RDClientPage` | `GET /api/remote/login-info`, `/api/remote/guest-info`, `/api/remote/desk`, `/api/remote/viewer/:deviceId`, `/remote-cdap/:deviceId`, `/remote-desktop/:deviceId` (`remote.routes.js`) | first |
| `RDClientPage` | `GET /api/guest/access-links/validate`, `GET /api/guest/access-links/peers`, `POST /api/guest/mesh/devices/:id/desktop` (`guest.routes.js`) | first |
| `RDClientPage` | `POST /api/mesh/devices/:id/desktop`, `GET /api/mesh/share/validate` (`meshcentral.routes.js`) | first |
| `Upload` | `POST /transfer` (`fileTransfer.routes.js`), `POST /api/panel/languages/:code/fix` (`languages.routes.js`) | first |
| `Upload` | `POST /`, `DELETE /:id`, `POST /:id/comments`, `POST /:id/attachments` (`tickets.routes.js`) | first |
| `FileAccess` | `GET /transfers/:id/download` (`fileTransfer.routes.js`), `GET /api/panel/languages`, `GET /api/panel/languages/:code/missing` (`languages.routes.js`) | first |
| `FileAccess` | `GET /:id/attachments`, `GET /attachments/:aid` (`tickets.routes.js`), `GET /downloads/generic-agent/:tag/:name` (`phase4_5.routes.js`) | first |
| `API` | `GET /api/logs/recent`, `GET /api/database/stats` (`system.routes.js`). `Set.AppLevel()` has counted them against `API` already, so the route does not count them again | first |

These routes pass `Set.AppLevel()` before, so `POST /api/auth/login`
counts against `API` and `Login`, and the client sees the headers of
`Login`.

## The client API port: `WANStack`

All three count per address (`unknown` without one) in windows of 60 s.

| Node name | Maximum | Applies to | 429 body |
|---|---|---|---|
| `wanGlobalLimiter` | 60 | every request to a listed path | `{"error":"Too many requests"}` |
| `wanAuditLimiter` | 20 | POST below `/api/audit/` | `{"error":"Audit rate limit exceeded"}` |
| `wanLoginLimiter` | 5 | `/api/login`, `/api/logout` | `{"error":"Too many requests, please try again later"}` |

They run in this order, after the path list (a 404 or 405 is not counted)
and before the JSON rule and the body limit.
