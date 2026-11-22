# The routes that use `console/authn`

For the route step. Each route lists what it calls, in order. `s` is the
`*authn.Service`, `sess` the request's `*session.Session`, `m` the
`*session.Manager`, `api` the `*goapi.Client`. Lines are those of the Node
file at the time of the port. Texts in angle brackets are translation keys
(`req.t`).

Things that hold for all of them:

- The service takes text. Where Node accepted any JSON value, the route
  decides what happens to a value that is not text; the cases are listed
  per route. Lengths in the routes are JavaScript lengths (UTF-16 units).
- A `Refusal` (an `error` that `errors.As` finds as `authn.Refusal`) is an
  answer for the client: its text goes out as `error`. Any other error is
  the route's 500.
- `s.RecordAttempt` was called without `await` everywhere. The answer did
  not wait for it and an error in it reached nobody
  (`spec/auth-sessions.md`, section 11, item 17). To keep that: call it
  after the answer is decided and ignore its error. The rows it writes are
  the same either way.
- The audit rows (`db.logAction`) are written by the route through
  `console/auditlog`, not by this package.
- `req.ip` is the raw client address (may start with `::ffff:`);
  `meta.IP` from `m.ClientMeta` is the cleaned one. Both are used below as
  Node used them.

## `routes/auth.routes.js` (17 routes)

### `GET /api/auth/login-page` (91-144)

No service call. `signedIn` is `sess.UserID() != 0` and the session user's
role is not `pro`. Everything else comes from branding, i18n and
attestation.

### `POST /api/auth/login` (149-297), `loginLimiter`

1. `meta := m.ClientMeta(r, body.clientFingerprint || body.fingerprint)`.
2. `username` or `password` falsy: `400 <auth.invalid_credentials>`.
   `username.length > 128 || password.length > 128`: the same 400. (A
   number or an object has no length and passes this check.)
3. `lockout, err := s.CheckBruteForceFor(ctx, name, req.ip)`, where `name` is
   the statement parameter the driver made of `username` (console/jsbind):
   SQLite refused a boolean, an empty list, a list of several values or of
   a list or an object, and an object, which answers 500
   `<errors.server_error>` (UM20). Blocked: audit `login_blocked` /
   `Account/IP locked; ip=<meta.IP>`, `Retry-After` and
   `429 {success:false, error:<auth.too_many_attempts>, code:"account_locked", retry_after}`
   with `lockout.RetryAfter`, or 60 when the end of the lockout could not
   be read.
4. `outcome, err := s.Authenticate(ctx, username, password)`.
   Values that are not text: a `username` that is not text is refused by
   `Authenticate` in Node (answer as for step 5, and the attempt is
   recorded with that value as the name). A `password` that is not text
   made the hash compare throw: 500 `<errors.server_error>`, for a local
   account and for an unknown user alike. Two exceptions: an account with
   provider `oidc` is refused before the password is looked at (step 5),
   and for an `ldap` account the value went to the Go server as it was.
5. `outcome.IsFailure()`: `s.RecordAttemptFor(ctx, name, req.ip, false)`,
   audit `login_failed` /
   `Username collision: <username>; ip=<meta.IP>; ua=<meta.UserAgent, 160>`,
   then `409 {success:false, error:<auth.username_collision>, code:"username_collision"}`
   for `authn.FailureUsernameCollision`, `401 <auth.invalid_credentials>`
   for any other code.
6. `outcome.User == nil`: `s.RecordAttemptFor(..., false)`, audit
   `login_failed` / `Username: <username>; ip=...; ua=...`,
   `401 <auth.invalid_credentials>`.
7. `s.RecordAttemptFor(ctx, name, req.ip, true)`.
8. `outcome.User.Role` is `pro`: `403 <auth.pro_only_account>`. Not
   reachable: `Authenticate` refuses `pro` (kept defect S11).
9. `outcome.User.TOTPRequired`: `sess.Regenerate()` (500 `Server error` when it
   fails), then `sess.Set("pendingTotpUserId", user.ID)`,
   `sess.Set("pendingTotpUser", user)` (the `authn.User` marshals to what
   Node stored), `sess.Set("pendingClientMeta", meta)`, answer
   `200 {success:true, totpRequired:true}`. Node also stored
   `goPartialToken` when the user object had one; it never has (kept
   defect S10).
10. Else `sess.Regenerate()`, `sess.Establish(session.User{...}, meta)`,
   clear the guest cookie, audit `login` (and `session_replaced`), answer
   with `user`, `redirect` (`httpx.SafeReturnPath(body.return)` or `/`) and
   `session`. `last_login` was already written by `Authenticate`.


### `POST /api/auth/logout`, `POST /logout`, `GET /logout`

No service call: audit `logout`, `sess.End(session.ReasonLogout)`,
`sess.Destroy()`, `sess.ClearCookies(w)`, and for `POST /logout` a
redirect to `/login`.
`GET /logout` signs nobody out: 405 with the console's error page for a
signed-in session, a redirect to `/login` otherwise.

### `GET /api/auth/verify` (324-344), `GET /api/auth/sessions` (349-367)

No service call: `m.Policy()`, the session's fields, `m.List(ctx, userID, sess.ID())`.

### `POST /api/auth/password` (372-422), `requireAuth`, `passwordChangeLimiter`

1. `currentPassword` or `newPassword` falsy: `400 <auth.password_required>`.
2. `newPassword !== confirmPassword`: `400 <auth.passwords_mismatch>`.
3. `err := s.ChangePassword(ctx, sess.UserID(), currentPassword, newPassword)`.
   A `Refusal` (`ErrUserNotFound`, `ErrWrongPassword`,
   `ErrPasswordTooShort`): `400 {success:false, error:<its text>}`, in
   English.
4. Mirror the password to the Go server (`userSync.mirrorUpdate`, best
   effort; with one user table there is nothing to mirror, GROUPS.md).
5. Audit `password_changed` / `Password changed`, `200 {success:true}`.

### `GET /api/auth/oidc/status` (451-464)

`api.GetOIDCStatus(ctx)`. Not `success`: `{enabled:false}`. Else its `data`
plus `local_bypass: authn.IsPrivateClientIP(req.ip)`.

### `GET /api/auth/oidc/authorize` (473-485)

`api.StartOIDCAuthorize(ctx, returnPath)` with
`httpx.SafeReturnPath(query.return_url)` or `/`. No service call.

### `GET /api/auth/oidc/callback`, `GET /api/oidc/callback` (494-506)

`api.ProxyOIDCCallback(ctx, query)`. No service call.

### `GET /api/auth/oidc/session` (522-646)

1. No `code` (a string): `302 /login?error=oidc_invalid`.
2. `api.ExchangeOIDCCode(ctx, code)`; no `success`, `token` or `username`:
   the same redirect.
3. `user, err := s.OIDCUser(ctx, username, role)`. It does what the route
   did with the local row: create it with provider `oidc` and an unusable
   password, or set role and provider on the existing one. `user == nil`:
   `302 /login?error=oidc_error`. Node passed the role as the Go server
   sent it; `OIDCUser` takes text, so pass `""` for a role that is not
   text.
4. The profile picture: `avatarService.syncOidcPicture(user.id, exchange.data)`
   in the background (`console/avatars`; `tests/oidcSessionAvatar.test.js`
   is about that call, not about this package).
5. `sess.Regenerate()`, `sess.Establish` with the role `role || user.Role || "viewer"`,
   the extra fields `username`, `role`, `goToken`, `authMethod: "oidc"`,
   clear the guest cookie.
6. `s.TouchLastLogin(ctx, user.ID.V)`, audit `login` / `User logged in via OIDC; ...`
   (and `session_replaced`), redirect to the safe return path. A failure
   in this step is logged and the redirect still happens.

No second factor and no `pro` check on this path (kept defect S11).

### `POST /api/auth/totp/verify` (657-725), `loginLimiter`

1. No `pendingTotpUserId` or `pendingTotpUser` in the session:
   `400 <auth.totp_session_expired>`.
2. If the session has `goPartialToken`:
   `api.VerifyTOTPViaGo(ctx, token, code || recoveryCode || "")`; nil
   (no answer) is audit `totp_unavailable` / `Delegated 2FA unavailable (Go
   unreachable)` and `503 {success:false, error:<auth.totp_temporarily_unavailable>, code:"totp_backend_unavailable"}`
   with no local check, `rejected` with `rateLimited` is
   `429 <auth.totp_rate_limited>`, `rejected` is audit `totp_failed` /
   `Go server rejected TOTP` and `401 <auth.totp_invalid_code>`, a `token`
   finishes the sign-in with the method `totp (Go delegated)`. Never
   reached (kept defect S10).
3. `recoveryCode` truthy: `ok, err := s.VerifyRecoveryCode(ctx, id, recoveryCode)`,
   method `recovery`. A `recoveryCode` that is not text made Node throw:
   500. Else `code` truthy: `ok, err := s.VerifyTOTPCode(ctx, id, code)`,
   method `totp`. A `code` that is not text is never right, but the stored
   secret is still read, so a secret that is no base32 is a 500 either way.
4. Not ok: audit `totp_failed` / `Method: <method>`,
   `401 <auth.totp_invalid_code>`. The pending state stays.
5. Ok: `sess.Regenerate()`, `sess.Establish(pendingUser, pendingClientMeta)`,
   clear the guest cookie, `s.TouchLastLogin(ctx, id)`, audit `login` /
   `User logged in (2FA: <method> (local)); ...`, the same answer as the
   password sign-in.

### `POST /api/auth/totp/setup` (794-826), `requireAuth`

1. `s.IsTOTPEnabled(ctx, userID)` true: `400 <auth.totp_already_enabled>`.
2. `setup, err := s.GenerateTOTPSetup(ctx, userID)`. `Refusal`
   (`ErrUserNotFound`): `400 {success:false, error}`.
3. `200 {success:true, qrCode, secret, otpauthUrl}`: `setup` marshals to
   the three fields in this order.

### `POST /api/auth/totp/enable` (831-873), `requireAuth`

1. `code` falsy or `code.length !== 6`: `400 <auth.totp_invalid_code>`.
   (An array of six entries passes this check and is then never right.)
2. `codes, err := s.VerifyAndEnableTOTP(ctx, userID, code)`. `Refusal`
   (`ErrTOTPNotSetUp`, `ErrInvalidTOTPCode`): `400 {success:false, error}`.
3. Mirror to the Go server (`userSync.mirrorTotpEnable`, best effort).
4. Audit `totp_enabled` / `2FA enabled`,
   `200 {success:true, recoveryCodes: codes}`.

### `POST /api/auth/totp/disable` (878-923), `requireAuth`

1. `password` falsy: `400 <auth.password_required>`.
2. `user, err := s.UserByID(ctx, userID)`; nil: `404 <users.not_found>`.
3. `ok, err := s.VerifyPassword(ctx, password, user.PasswordHash.String)`;
   not ok: `401 <auth.invalid_credentials>`.
4. `s.DisableTOTP(ctx, userID)`. On a `users` table the signal server
   created on PostgreSQL this fails (the secret column is NOT NULL there,
   BUGS D5): 500.
5. Mirror to the Go server, audit `totp_disabled` / `2FA disabled`,
   `200 {success:true}`.

### `GET /api/auth/totp/status` (928-936), `requireAuth`

`enabled, err := s.IsTOTPEnabled(ctx, userID)`; `200 {success:true, enabled}`.

## Other console routes

### `POST /api/bd/operator/login` (`routes/bd-api.routes.js:709-761`)

1. `username` and `password` must be non-empty text: else
   `400 {error:"Username and password are required"}`.
2. `s.CheckBruteForce(ctx, username, ip)`. Node tested the returned object
   for truth, which it always is: every request that gets here answers
   `429 {error:"Too many failed attempts. Try again later."}` (BUGS S4).
   Keeping that, nothing below runs. The rest for the day it is fixed:
3. `s.Authenticate`; collision: `s.RecordAttempt(false)`, 409; nil:
   `s.RecordAttempt(false)`, 401; a role other than `admin` or `operator`:
   `s.RecordAttempt(false)`, 403.
4. `s.RecordAttempt(true)`,
   `s.GenerateSignInToken(ctx, user.ID.V, String(device_id || "").substring(0, 32), "", ip)`,
   `s.TouchLastLogin`, audit `operator_login`, `{access_token, user:{name, role}}`.

### The RustDesk client API (`routes/rustdesk-api.routes.js`, console port and client port)

- Bearer check (`extractBearerToken`, 100-106): the header must start with
  `Bearer `, the rest is trimmed. This is not `authn.ExtractBearerToken`
  (which is the stricter form of `middleware/deviceAuth.js`): the route
  keeps its own five lines.
- `requireAuth` and the handlers that check the token themselves (949,
  972, 999, 1021, 1064, 1094, 1117, 1149, 1573, 1703 and the 48 routes
  with the chain `bearerToken`): `user, err := s.ValidateAccessToken(ctx, token)`;
  nil is `401 {error:"Authorization required"}`. A token that is not 64
  characters long is refused without a look at the database.
- `POST /api/login` (1324-1477): `username` is trimmed. Then
  `s.CheckBruteForce(ctx, username, ip)`; blocked: audit
  `api_login_blocked`, `429 {error: reason, retry_after: retryAfter}`
  (`BruteForce.RetryUnknown` is `retry_after: null`). Then the banned
  device check, then `s.Authenticate`; collision and nil record a failed
  attempt. With `TOTPRequired` and without the bypass setting the pending
  state goes to process memory; with the bypass, and without a second
  factor: `s.RecordAttempt(true)`,
  `s.GenerateSignInToken(ctx, user.ID.V, clientId, clientUuid, ip)`,
  `s.TouchLastLogin`.
  `clientId` and `clientUuid` come from the body as they are: see
  "Missing client id and UUID" below.
- The second step (`handleTfaVerification`, 1481-1537):
  `s.VerifyTOTPCode(ctx, session.userId, code)` whose result Node did not
  await, so every code passed (BUGS S5, kept: the route treats the check
  as passed and does not look at the answer; an error of the call reached
  nobody). Then `s.RecordAttempt(true)`, `s.GenerateSignInToken`,
  `s.TouchLastLogin`.
- `POST /api/logout` (1543-1566): `s.ValidateAccessToken`, then
  `s.RevokeClientTokens(ctx, user.ID.V, clientId, clientUuid)`.

### Device routes (`middleware/deviceAuth.js`)

Every sign-in above gives an account's token, which proves no device
whatever client id it stores (BUGS RV1). A device proves itself with a
device token in device_tokens issued for the device (`created_by`
"system", on behalf of no account): the API server's enrollment and
pairing, or `s.IssueDeviceToken(ctx, deviceID, note)`, which the console
issues for a device an approver admitted (register-status), at
`/api/bd/register` and at a join (`s.ServerDeviceToken` names its device).
A device token an older console issued in access_tokens (token_kind
"device") passes too. Such a token
signs no account in (`s.ValidateAccessToken` gives nil for it) and does not
expire by time; a delete, a ban, a change of id and a new token for the
device end it (`s.RevokeDeviceTokens` ends both kinds).

`requireDeviceToken`: `device, err := s.RequireDeviceToken(ctx, httpx.Latin1(authorization))`.
A `*authn.DeviceAuthError` is the answer `Status` with `{error: Text}`.
Else the request's device id is `device.DeviceID` and its token row
`device.Token`.

`requireTokenDeviceMatch`:
`authn.RequireTokenDeviceMatch(device.DeviceID, authn.ClaimFromJSON(body.device_id, present), <query device_id>, authn.ClaimFromText(header X-Device-Id, present))`;
`authn.ErrDeviceMismatch` is `403 {error:"Device ID mismatch"}`. The query
value is text, or a list or an object when the address has `device_id[]=`
or `device_id[x]=` (Express parsed those): build that claim with
`ClaimFromJSON` from the parsed query.

The 41 routes with `requireDeviceToken`, 19 of them also with
`requireTokenDeviceMatch` (marked `+match`), mounts as in the inventory:

| File | Routes |
|---|---|
| `bd-api.routes.js` | `POST /api/bd/heartbeat` +match, `POST /api/bd/remote-agent-token` +match, `GET` and `DELETE /api/bd/session/:id`, `GET /api/bd/peers`, `GET /api/bd/peer/:id`, `POST /api/bd/help-request` +match, `POST /api/bd/chat/send` +match, `GET /api/bd/chat/history` +match |
| `activity.routes.js` | `POST /api/bd/activity` +match, `POST /api/activity/activity` +match |
| `automation.routes.js` | `GET /api/bd/commands`, `POST /api/bd/commands/:id/result`, `POST /api/automation/commands/:id/result` (`GET /api/automation/commands` is shadowed) |
| `dataguard.routes.js` | `GET /api/bd/dlp-policies`, `POST /api/bd/dlp-events`, and both under `/api/dataguard` |
| `fileTransfer.routes.js` | `POST /api/bd/file-transfer` +match, `GET /api/bd/file-transfer/:id`, and both under `/api/files` |
| `fleet.routes.js` | `POST /api/bd/fleet/task-result` +match, `POST /api/bd/fleet/software` +match, and both under `/api/bd/api/bd/fleet` |
| `policies.routes.js` | `GET /api/bd/device-policy` +match, `POST /api/bd/attestation` +match, and both under `/api/bd/api/bd` |
| `resource-control.routes.js` | `GET /api/bd/resource-policy` +match, and under `/api/bd/api/bd` |
| `tickets.routes.js` | `POST /api/tickets/bd`, `GET /api/tickets/bd` |
| `inventory.routes.js` | `POST /api/bd/inventory`, `POST /api/bd/telemetry`, `GET /api/bd/inventory/:id`, and the three under `/api/inventory` |

`inventory.routes.js` (35-90) has its own copies, which differ: the token
only needs a `client_id` that is not empty (no check of its form), and the
match compares the request's device id with one value the handler picked
(`403 {success:false, error:"Device ID mismatch"}`). Port them in that
group with `authn.ExtractBearerToken`, `s.LookupAccessToken` and
`s.TouchAccessToken`.

`bd-api.routes.js` has two more checks of its own (180-238), built from the
same three calls and `s.UserByID`: `requireDeviceAuth` (any live token;
`401 {error:"Missing or invalid Bearer access token"}`; the operator
routes behind it refuse a device token) and `identifyDevice` for `POST
/api/bd/register` (a device token bound to a device id, else the header
`X-Device-Id` when `authn.IsDeviceID`). Both use the
route's loose Bearer parsing (starts with `Bearer `, rest trimmed).

### Device sockets

`services/bdRelay.js:422`, `services/chatRelay.js:714`,
`services/remoteRelay.js:150`:
`ok, err := s.VerifyDeviceWSAuth(ctx, deviceID, token)`. Node let a failed
enrollment lookup reject the promise; the three callers differ in what
that did (`bdRelay` awaits inside its connection handler, `chatRelay` has
a `.then` without a `.catch`, `remoteRelay` returns the promise). Treat
`err != nil` as "no" and log it.

### Users (`routes/users.routes.js`)

`POST /api/users` (473, 494), `PATCH /api/users/:id` (612, 621),
`POST /api/users/:id/reset-password` (793, 802):
`check := authn.ValidatePasswordStrength(password)`; `check.Strength == "weak"`
is `400 {success:false, error:<users.weak_password>, feedback: check.Feedback}`.
Then `hash, err := s.HashPassword(ctx, password)` and the group's own
insert or update.

### Settings (`routes/settings.routes.js:2101-2119`)

`PUT /api/settings/oidc` with `enabled`:
`authn.IsValidOIDCRedirectURL(strings-trimmed redirect_url)`; false is
`400 <settings.oidc_redirect_url_invalid>`.
`authn.SuggestOIDCRedirectURL` has no caller in the console.

### Start and jobs (`server.js`)

- At start (428-442), after the database is ready:
  `created, err := s.EnsureDefaultAdmin(ctx)`. An error stops the start
  (in Docker: `authn.ErrNoBootstrapAdmin`). Give `ctx` no short deadline:
  the call waits up to 25 s for a credentials file and up to
  `DOCKER_ADMIN_BOOTSTRAP_WAIT_MS` for the signal server's admin.
- Hourly (594-610): `s.CleanupHousekeeping(ctx)`, then the other
  housekeeping. It returns nothing; a failure is one log line.

## Missing client id and UUID

Checked against Node with `generateAccessToken(1, 'c', undefined, 'ip')`
and its neighbours on both databases:

- A missing `uuid` (JavaScript `undefined`) and a `null` one: the call
  works, nothing is revoked (no row has a NULL UUID), and the token is
  stored with an empty UUID. The same on SQLite and PostgreSQL.
- A missing `id`: stored as an empty client id.
- A `uuid` that is a list or an object: SQLite throws
  ("Too few parameter values were provided", the route's 500), PostgreSQL
  stores its text form.

`s.GenerateSignInToken` takes text. An empty UUID revokes the user's
tokens that have an empty UUID, which is what Node did for `""` (the
operator sign-in). For a request without `uuid`, or with `null`, call
`s.IssueAccessToken(ctx, userID, clientID, "", ip)`: it stores the token
and revokes none.

`s.RevokeClientTokens` with a missing id or UUID revokes all of the
user's tokens, as an empty text does.
