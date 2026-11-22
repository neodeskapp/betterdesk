# Security

Practical hardening for a self-hosted BetterDesk install. License: [[Licensing]]. Privacy / no vendor analytics: [[Privacy]]. SSO: [[OIDC SSO|OIDC-SSO]].

---

## Production minimum checklist

1. Keep the panel on localhost or behind TLS reverse proxy (`HOST=127.0.0.1`, see [REVERSE_PROXY](https://github.com/UNITRONIX/BetterDesk/blob/dev/docs/setup/REVERSE_PROXY.md)).
2. Strong admin password + TOTP for operators who need it.
3. Prefer enrollment **`managed`** or **`locked`** over default **`open`** once you are past lab testing (Settings / Go env `ENROLLMENT_MODE`).
4. Do not expose `:21114` admin API to the WAN; clients use **`:21121`**.
5. Turn on TLS for signal/relay/API when clients are off your LAN — [[TLS / SSL Certificates|TLS-SSL]].
6. Gate `/metrics` (auth or IP allowlist) — [[Monitoring]].
7. Disable LAN mDNS if you do not need it (`PANEL_MDNS=off`) — [[Privacy]].

### Enrollment modes

| Mode | Behaviour |
|------|-----------|
| `open` | **Default.** Accept new device registrations (lab-friendly). |
| `managed` | New devices need approval or a valid token. |
| `locked` | Only devices with valid tokens register. |

---

## Encryption

### Signal (NaCl)

Client–server signal (:21116) uses NaCl:

1. Server Ed25519 key pair (`id_ed25519` / `.pub`)
2. DH session key
3. Encrypted messages after handshake

### Relay

Relay (:21117) forwards opaque peer traffic. Peers set up E2E through signal; the relay does not hold your session keys.

Stock RustDesk clients show a **green lock** when E2E is up (P2P or relay). That is client–client crypto on your infrastructure — not a cloud “trust us” feature.

### TLS transport

Optional TLS on signal / relay / API — see [[TLS / SSL Certificates|TLS-SSL]]. Dual-mode listeners accept TLS or plain on the same port when configured.

---

## Authentication

### Web Console

| Mechanism | Description |
|-----------|-------------|
| **Session cookies** | `HttpOnly`, `Secure`, `SameSite=Lax` |
| **Session regeneration** | New session ID after login (prevents fixation) |
| **Bcrypt passwords** | Automatic salt, timing-safe comparison |
| **TOTP 2FA** | 30-second TOTP with one-window tolerance |
| **Partial 2FA token** | 5-minute TTL JWT for 2FA step |

### API Authentication

| Method | Usage |
|--------|-------|
| **API Key** (`X-API-Key` header) | Scripts and integrations |
| **JWT Bearer** (`Authorization: Bearer` header) | User API access (Pro/Admin/Operator) |
| **Session cookie** | Web panel requests |

### RustDesk Client Auth

| Method | Description |
|--------|-------------|
| **Public key** | Ed25519 key exchange on signal connection |
| **Registration token** | Optional token required for client registration |
| **User login** | Username/password via `/api/login` on Client API (port 21121) |

---

## Authorization (RBAC)

Four-tier role-based access control:

| Role | Panel | API | Devices | Users | Settings |
|------|-------|-----|---------|-------|----------|
| **Admin** | ✅ | ✅ | Full | Full | Full |
| **Operator** | ✅ | ✅ | View + Connect | ❌ | ❌ |
| **Viewer** | ✅ | Read | View only | ❌ | ❌ |
| **Pro** | ❌ | ✅ | Full API | ❌ | ❌ |

See [[User Management|User-Management]] for details.

---

## Rate Limiting

### IP-Based Limits

| Endpoint | Limit | Description |
|----------|-------|-------------|
| `POST /api/auth/login` | 5/min per IP | Login attempts |
| `POST /api/auth/login/2fa` | 5/min per IP | TOTP verification |
| TCP signal connections | Configurable | Per-IP connection rate |
| WebSocket upgrades | Per-IP | Signal and relay |

### Connection Limits

| Resource | Limit | Description |
|----------|-------|-------------|
| TCP punch connections | 10,000 max, 2-min TTL | DDoS protection |
| Relay sessions | Idle timeout (configurable) | Stale session cleanup |
| API WebSocket | Per-IP | Event stream connections |

---

## Input Validation

### API Endpoints

| Validation | Rule |
|-----------|------|
| **Peer ID** | Alphanumeric, 1-32 characters |
| **New peer ID** (rename) | `[A-Za-z0-9_-]{6,16}` |
| **Config keys** | `[a-zA-Z0-9._-]{1,64}` |
| **SQL LIKE patterns** | `%` and `_` escaped with `\` |
| **Tags** | String or JSON array accepted |
| **Device IDs** | Coerced to string (numeric accepted) |

### WebSocket

| Check | Description |
|-------|-------------|
| **Origin validation** | `WS_ALLOWED_ORIGINS` env var (signal + relay) |
| **API origin validation** | `API_WS_ALLOWED_ORIGINS` env var |
| **Session required** | WebSocket upgrade requires valid session cookie |

---

## CSRF Protection

The web console uses double-submit cookie pattern:
- CSRF token and its hash kept in an `HttpOnly` cookie
- Token sent back in the `X-CSRF-Token` header (or a `_csrf` body field)
- Server validates token against cookie
- Implemented in `betterdesk-server/console/csrf`

---

## Audit Logging

All security-relevant events are logged:

| Event | Details Logged |
|-------|---------------|
| Login success/failure | IP, user agent, username |
| 2FA attempts | IP, success/failure |
| Password changes | User, IP |
| Device ban/unban | Device ID, admin |
| Device deletion | Device ID, admin, revoke flag |
| API key usage | Key ID, endpoint |
| Config changes | Key, old/new values |
| Sysinfo updates | Device ID, hostname, OS |
| Connection audit | Host ID, peer ID, action, IP |

Audit entries are stored in the Go server's ring buffer and queryable via API.

---

## Device Security

### Soft Delete

Deleted devices are soft-deleted (marked `soft_deleted=1`). They:
- Cannot re-register on the signal server
- Are filtered from device list queries
- Prevent "zombie device" reappearance

### Device Revocation

The `?revoke=true` flag on device deletion:
1. Soft-deletes the device
2. Blocks the device ID (`IsPeerBanned`)
3. Disconnects all active TCP and WebSocket connections
4. Logs `ActionPeerRevoked` audit entry
5. Optionally cascades to linked devices (`?cascade=true`)

### Peer Banning

Banned devices:
- Cannot register on the signal server
- Cannot establish relay connections
- Ban is per-device-ID (not per-IP)

---

## Dependency Security

### Web app (`web-ui`)

- Built at release time and embedded into the server binary; a running install has no Node.js and no npm packages
- `npm audit --audit-level=high` runs in CI (`web-ui-ci.yml`)
- Dependencies pinned via committed `web-ui/package-lock.json`; builds use `npm ci`

### Go Server

- Go toolchain version pinned in `go.mod`
- `govulncheck ./...` in CI
- No CGO dependencies (static binary)
- Minimal external dependencies

### Rust (Tauri clients)

- `cargo audit` in CI (known glib RUSTSEC-2024-0429 ignored until GTK stack migration)
- Dependabot weekly for npm (`web-ui` and the repository root), Go modules and GitHub Actions

---

## Logging (3.4+)

| Component | Control | Default (production) |
|-----------|---------|-------------------|
| Web console | `LOG_LEVEL=error\|warn\|info\|debug` | `warn` |
| Go server | `LOG_LEVEL` or `-log-level` | `info` |

Auth and audit details redact usernames and secrets before stdout/DB insert. Set `LOG_LEVEL=info` temporarily when debugging auth issues.

Operational logs: use systemd journal or Docker log driver; optional Go audit JSONL via `AUDIT_LOG_FILE` (configure logrotate externally).

---

## Production deployment (3.4+)

Recommended before Internet-facing deployment:

| Control | Variable | Recommended |
|---------|----------|-------------|
| Signal TLS | `TLS_SIGNAL=Y` | Required on WAN |
| Relay TLS | `TLS_RELAY=Y` | Required on WAN |
| Enrollment | `ENROLLMENT_MODE` | `managed` or `locked` |
| WS origins | `WS_ALLOWED_ORIGINS` | Explicit panel URL(s) |
| Panel bind | `HOST` | `127.0.0.1` behind reverse proxy |
| Panel TLS | `SSL_CERT_PATH` / `SSL_KEY_PATH` | Valid public certificate |
| Relay limits | `RELAY_MAX_CONNS_PER_IP` | `20` (adjust for NAT scale) |
| Signal rate limit | `SIGNAL_RATE_LIMIT_PER_IP` | Default `20`; raise for large NAT |

Startup banners log **ERROR** when open enrollment is combined with missing signal/relay TLS.

---

## Security Headers

The web console sets standard security headers:
- `X-Content-Type-Options: nosniff`
- `X-Frame-Options: DENY`
- `X-XSS-Protection: 1; mode=block`
- `Referrer-Policy: strict-origin-when-cross-origin`
- `Content-Security-Policy` (configured per environment)

---

## See also

- [[TLS / SSL Certificates|TLS-SSL]] — transport encryption
- [[User Management|User-Management]] — 2FA and sessions
- [[Licensing]] — AGPL and Commercial Grant
