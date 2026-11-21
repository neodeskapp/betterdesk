# API port consolidation (Go :21114 + client API port :21121)

**Status:** active (v3+)  
**Related:** issue #160, Discussion #452, `betterdesk-server/api/*`, `betterdesk-server/console/listen/clientapi.go`

## Summary

All ports below are served by the one `betterdesk-server` process.

| Port | Listener | Role |
|------|----------|------|
| **21114** | Go API | **Default Client API** on native installs: `/api/login`, AB, heartbeat, REST |
| **21121** | Go API (Docker single container) **or** the console's client API port (native installs) | Docker: the API itself. Native: the console's client API port passes requests on to the API in the same process when `RUSTDESK_API_PROXY=true` |
| **5000** | Web console | Admin web panel only. **Never** set RustDesk **API Server** to this port |
| **21118 / 21119** | Signal / relay | Native RustDesk WSS signal / relay (not Client API) |

Handlers and database for Client API live **only in the Go API**. The client API port on **21121** does not implement login/AB logic itself when `RUSTDESK_API_PROXY=true`.

## Architecture

```
RustDesk client (native / recommended)  ──HTTP :21114──►  Go API  /api/login, /api/ab, …

RustDesk client (Docker single)         ──HTTP :21121──►  Go API  (same handlers)

RustDesk client (native, :21121 URL)    ──HTTP :21121──►  console client API port (WAN limits)
                                                             └──►  Go API in the same process

Web panel  ── :5000 ──►  console  ──►  Go API in the same process
```

**Native (systemd / Windows service) new deployments:** `http://<host>:21114`  
**Official single-container Docker:** `http://<host>:21121`  
**Existing native fleets on :21121:** keep that URL; the client API port passes it on

### Reverse proxy pitfalls

Do **not** send only `/api/ws/` to Go while leaving `/api/login` (and AB) on panel `:5000`. Client login tokens are issued by Go into `client_sessions`; PunchHole then validates that hash. A partial proxy often yields “login looks fine” in the UI but `initiator_token_rejected` on connect — see [DOCKER_TROUBLESHOOTING.md](../docker/DOCKER_TROUBLESHOOTING.md#problem-logged-in-client-fails-with-initiator_token_rejected) and [REVERSE_PROXY.md — Client API vs panel](../setup/REVERSE_PROXY.md#client-api-vs-panel-on-the-same-hostname).

## Defaults (.env)

The console's `.env` (for example `/opt/BetterDeskConsole/.env`), which the server reads through `BETTERDESK_ENV_FILE`:

```bash
GO_API_PORT=21114
BETTERDESK_API_URL=http://localhost:21114/api
HBBS_API_URL=http://localhost:21114/api

# Backward compatibility (legacy client configs on :21121)
API_ENABLED=true
API_PORT=21121
RUSTDESK_API_PROXY=true
```

Go systemd: `-api-port 21114`

## Firewall (WAN)

Open **both** for full compatibility:

- **21114/tcp** — default Go API (direct clients)
- **21121/tcp**: client API port (clients configured with `:21121`)

## Issue #160

| Symptom | Fix |
|--------|-----|
| Refused on **21114** | Go running, `-api-port 21114`, `ufw allow 21114/tcp` |
| Refused on **21121** | `API_ENABLED=true`, web console on (`CONSOLE_PORT` not `0`), `ufw allow 21121/tcp` |
| Go API on **21121**, not 21114 | Repair: Go API → 21114, client API port stays on 21121 |

**Linux repair:** `ensure_go_server_signal_ports()` moves `-api-port 21121` back to `21114`; it runs from Repair → Repair permissions and Repair → Repair HTTPS / TLS.

## RustDesk device groups (panel → client)

Panel **device groups**, **folders**, and ACL are in the **consolidated database**:

- **PostgreSQL** (v3+ default): same DSN as Go `-db` / `DATABASE_URL` — tables `device_groups`, `folders`, `device_folder_assignments`, `device_group_members`, ACL join tables.
- **SQLite**: `db_v2.sqlite3`, once an older install's **`auth.db`** has been imported into it ([sqlite-auth-consolidation.md](./sqlite-auth-consolidation.md)). Until then the server reads `auth.db` (`AUTH_DB_PATH`) read-only.

RustDesk desktop calls (Flutter client):

- `GET /api/ab` — legacy address book + **Tagi** sidebar
- `GET /api/device-group/accessible` — group/folder names
- `GET /api/peers?accessible&pageSize=` — accessible devices

Go endpoints (also reachable on **:21121**):

- `GET /api/group`, `/api/group/get` — full group payload with `team.peers`
- `GET /api/peers/list` — PRO peer list with tags and `device_group_name`

On **:21121**, the client API port passes RustDesk routes on to the Go API (JWT from `/api/login`).

## Verification

```bash
curl -sf http://127.0.0.1:21114/api/health
curl -sf http://127.0.0.1:21114/api/login-options
curl -sf http://127.0.0.1:21121/api/login-options
curl -sf -H "Authorization: Bearer <token>" http://127.0.0.1:21114/api/group/get
```
