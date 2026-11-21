# Configuration

BetterDesk is configured through **CLI flags**, **environment variables**, and **`.env` files**.

---

## Go Server Configuration

### CLI Flags

```bash
betterdesk-server [flags]

  -mode string     Server mode: all (signal, relay, API and web console in one process)
  -port int        Signal server port (default 21116)
  -relay-port int  Relay server port (default 21117)
  -api-port int    HTTP API port (default 21114)
  -key-file string Ed25519 key file path, without extension (default "id_ed25519")
  -db string       Database path or DSN (default "db_v2.sqlite3")

  -relay-servers string     Comma-separated relay servers (e.g., "1.2.3.4:21117")
  -always-relay             Force all connections through relay

  -tls-cert string   TLS certificate file path
  -tls-key string    TLS private key file path
  -tls-signal        Enable TLS on signal port (21116)
  -tls-relay         Enable TLS on relay port (21117)
  -tls-api           Enable TLS on API port (21114)
  -force-https       Force HTTPS redirects (implies --tls-api)

  -cdap              Enable CDAP gateway (:21122)
  -admin-port int    TCP admin console port (disabled by default)
  -log-format string Log format: text or json (default "text")
  -log-level string  Log level: debug, info, warn, error (default "info")
```

Prometheus text is served at `GET /metrics` on the admin API port (gated by auth/allowlist). See [[Monitoring]].

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` / `SIGNAL_PORT` | `21116` | Signal server port |
| `RELAY_PORT` | `21117` | Relay server port |
| `API_PORT` | signal-2 (21114) | HTTP API port |
| `DB_URL` | `db_v2.sqlite3` | Database path or PostgreSQL DSN |
| `RELAY_SERVERS` | auto-detected | Comma-separated relay addresses |
| `ALWAYS_USE_RELAY` | `N` | Force relay for all connections |
| `TLS_SIGNAL` | `N` | Enable TLS on signal |
| `TLS_RELAY` | `N` | Enable TLS on relay |
| `TLS_API` | `N` | Enable TLS on API |
| `TLS_CERT` | (empty) | TLS certificate file path |
| `TLS_KEY` | (empty) | TLS private key file path |
| `MESH_ENABLED` | `Y` | MeshCentral compatibility layer |
| `MESH_AGENT_CERT_FILE` | `mesh_agent_server.pem` | Agent-server RSA cert (`.msh` `ServerID`) |
| `MESH_WEB_CERT_FILE` | (empty) | Public TLS cert for MeshAgent web-hash (e.g. proxy LE fullchain) |
| `WS_ALLOWED_ORIGINS` | `*` | WebSocket signal/relay origin allowlist |
| `API_WS_ALLOWED_ORIGINS` | `*` | API WebSocket origin allowlist |
| `CDAP_ENABLED` | `N` | Enable CDAP gateway |
| `API_KEY` | (auto-generated) | API authentication key |
| `MASK` | (empty → /24 fallback) | LAN CIDR for same-network detection |
| `ALLOW_SHARED_NAT_INITIATOR` | `N` | Opt-in for multi-device CGNAT punch (see [[Monitoring]]) |
| `ENROLLMENT_MODE` | `open` | `open` / `managed` / `locked` |
| `PANEL_MDNS` | on | Set `off` to disable panel mDNS |

---

## Web Console Configuration

The web console runs inside `betterdesk-server`. On a native install it is configured through `/opt/BetterDeskConsole/.env` (`C:\BetterDeskConsole\.env` on Windows); the service points the server at that file with `BETTERDESK_ENV_FILE`. Docker passes the same settings as container environment variables.

### Managed configuration and capability checks

The installers and the web panel use the same allowlist, built into
`betterdesk-server`. Before changing runtime settings, check whether
the console can write configuration, deploy updates and restart the
BetterDesk service:

```bash
sudo ./betterdesk.sh --check-permissions
sudo ./betterdesk.sh --set-config LOG_LEVEL=info --set-config HTTPS_PORT=5443
```

The Windows manager provides equivalent `-CheckPermissions` and `-SetConfig`
parameters. Docker uses `betterdesk-docker.sh --check-permissions` and
`--set-config`. Changes are validated, backed up, written atomically and
followed by a service/container restart and health check. Unknown keys,
arbitrary service names and shell commands are not accepted. Secret values
are never printed in capability or configuration reports.

```env
# Server Connection
BETTERDESK_API_URL=http://localhost:21114/api
API_KEY=<your-api-key>

# Web Console
CONSOLE_PORT=5000        # 0 turns the web console off
HOST=127.0.0.1           # Panel bind address (localhost-only by default)
SESSION_SECRET=<random>

# RustDesk Client API
API_PORT=21121
API_HOST=0.0.0.0          # Client API bind address (WAN-facing)

# Database
DB_TYPE=sqlite             # sqlite or postgres
DATABASE_URL=              # PostgreSQL DSN (required when DB_TYPE=postgres)
DB_URL=/opt/betterdesk/db_v2.sqlite3  # The server's database: SQLite path or the PostgreSQL DSN

# Security
TRUST_PROXY=false          # Set Y behind reverse proxy (the API also needs TRUSTED_PROXIES)
# TRUSTED_PROXIES=127.0.0.1/32,::1/128
STORE_ADMIN_CREDENTIALS=false  # Persist admin credentials to file

# Optional
NODE_ENV=production        # production hides error details in answers
LOG_LEVEL=info
```

### Important Settings

#### `HOST` vs `API_HOST`

- `HOST` (default `127.0.0.1`) — Binds the web panel. Default is localhost-only for security. Set to `0.0.0.0` to expose the panel to the network (use with reverse proxy + TLS).
- `API_HOST` (default `0.0.0.0`) — Binds the RustDesk Client API (port 21121). Must be WAN-accessible for client login, AB sync, and heartbeat.

#### `TRUST_PROXY`

Set to **`Y`** or **`1`** when running behind a reverse proxy (nginx, Caddy, Cloudflare). This enables:
- Reading `X-Forwarded-For` for real client IPs in rate limiting
- Proper HTTPS detection for secure cookies

The server's API needs **`TRUST_PROXY=Y`** (or `-trust-proxy`); the web console also accepts `1`, `yes`, `true`, a number of proxy hops, or a comma-separated list of proxy addresses.

#### `TRUSTED_PROXIES`

Go server only. Comma-separated CIDR or bare IP of reverse proxies allowed to set `X-Forwarded-For` / `X-Real-IP`. Required with `TRUST_PROXY=Y` — if empty, the Go server **ignores** forwarded headers (security-first, [#276](https://github.com/UNITRONIX/BetterDesk/issues/276)).

```env
TRUST_PROXY=Y
TRUSTED_PROXIES=127.0.0.1/32,::1/128
```

> [!NOTE]
> UDP/TCP signal on port **21116** cannot use HTTP headers like `X-Forwarded-For`. `TRUST_PROXY` / `TRUSTED_PROXIES` apply to HTTP/API and signal **WebSocket** (`/ws/id`).

#### Panel read rate limiting

The general web console API limit is controlled by `RATE_LIMIT_MAX` (default
`100` requests per minute). Lightweight authenticated Settings reads use a
separate quota so loading the console does not consume the mutation
budget:

```env
PANEL_READ_RATE_LIMIT_MAX=600
```

This quota applies only to the allowlisted read endpoints. Login, mutations,
uploads, backups, update actions, and other public/API paths keep their own
limits.

> [!TIP]
> External reverse proxy (TLS on Caddy/Nginx :443): see [External Reverse Proxy Guide](https://github.com/UNITRONIX/BetterDesk/blob/dev/docs/setup/REVERSE_PROXY.md). Use `HOST=127.0.0.1`, `HTTPS_ENABLED=false`, and run `sudo betterdesk.sh` → **External reverse proxy** to generate Caddy/Nginx snippets.

#### `GO_API_PORT` vs `API_PORT`

In the console's `.env` on a native install:
- **`GO_API_PORT=21114`** — the server's own HTTP API (admin REST and the RustDesk client handlers)
- **`API_PORT=21121`** — RustDesk Client API port. The web console listens on it inside `betterdesk-server` and passes the client API on to the server's API (`RUSTDESK_API_PROXY=true`). The installers take this port from `CLIENT_API_PORT`.

In Docker the server's API itself listens on `21121` and the console's own client API listener is off (`API_ENABLED=false`).

The installer sets `GO_API_PORT=21114` on `betterdesk-server.service` to avoid HTTP/HTTPS toggle conflicts (#219).

#### Update channel

```env
UPDATE_GITHUB_BRANCH=main   # stable (default) or dev
```

Switch in **Settings → Updates → Update channel**. See [[Panel Updates|Panel-Updates]].

## Ports Reference

| Port | Protocol | Service | Description |
|------|----------|---------|-------------|
| 21114 | TCP (HTTP) | Go API | REST API + WebSocket events |
| 21115 | TCP | NAT Test | `TestNatRequest`, `OnlineRequest` |
| 21116 | TCP + UDP | Signal | Client registration, punch hole |
| 21117 | TCP | Relay | Bidirectional stream relay |
| 21118 | WS | WS Signal | WebSocket signal (21116 + 2) |
| 21119 | WS | WS Relay | WebSocket relay (21117 + 2) |
| 21121 | TCP (HTTP) | Client API | RustDesk Client API |
| 21122 | WS | CDAP | CDAP WebSocket gateway |
| 5000 | TCP (HTTP) | Web Console | Admin/operator panel |

### Reverse proxy vs direct ports

When TLS terminates at **Caddy/Nginx on :443**:

- **HTTP-proxied:** panel (`:5000`), console WebSockets, optional RustDesk WSS paths `/ws/id` → `:21118` and `/ws/relay` → `:21119`
- **Direct to host (not HTTP reverse proxy):** signal **21116** (TCP+UDP), relay **21117** (TCP), Client API **21121** unless you add a separate API vhost

See [External Reverse Proxy Guide](https://github.com/UNITRONIX/BetterDesk/blob/dev/docs/setup/REVERSE_PROXY.md).

### Firewall Configuration

```bash
# Linux (ufw)
sudo ufw allow 21114:21119/tcp
sudo ufw allow 21116/udp
sudo ufw allow 21121/tcp
sudo ufw allow 5000/tcp

# Linux (firewalld)
sudo firewall-cmd --permanent --add-port=21114-21119/tcp
sudo firewall-cmd --permanent --add-port=21116/udp
sudo firewall-cmd --permanent --add-port=21121/tcp
sudo firewall-cmd --permanent --add-port=5000/tcp
sudo firewall-cmd --reload
```

```powershell
# Windows
New-NetFirewallRule -DisplayName "BetterDesk" -Direction Inbound `
  -Protocol TCP -LocalPort 21114-21119,21121,5000 -Action Allow
New-NetFirewallRule -DisplayName "BetterDesk UDP" -Direction Inbound `
  -Protocol UDP -LocalPort 21116 -Action Allow
```

---

## Systemd Service Configuration

### `/etc/systemd/system/betterdesk-server.service`

The one service runs signal, relay, API and the web console. The installer writes it along these lines:

```ini
[Unit]
Description=BetterDesk server and web console
After=network-online.target postgresql.service
Wants=network-online.target

[Service]
Type=simple
User=betterdesk
Group=betterdesk
WorkingDirectory=/opt/betterdesk
Environment=BETTERDESK_ENV_FILE=/opt/BetterDeskConsole/.env
Environment=BETTERDESK_SUPERVISOR=systemd
Environment=SIGNAL_PORT=21116
Environment=RELAY_PORT=21117
Environment=GO_API_PORT=21114
Environment=MESH_ENABLED=Y
ExecStart=/opt/betterdesk/betterdesk-server -mode all -relay-servers YOUR.PUBLIC.IP -key-file /opt/betterdesk/id_ed25519 -api-port 21114
Restart=always
RestartSec=2
SuccessExitStatus=3
LimitNOFILE=1000000
NoNewPrivileges=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
```

The server reads the console's `.env` itself and fills only what the unit leaves unset. Exit status 3 is the server asking to be started again (a restart, an update or a restore from the panel).

---

## Device Status Configuration

Fine-tune device status detection with these environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `PEER_TIMEOUT_SECS` | `15` | Seconds until device is marked offline |
| `HEARTBEAT_INTERVAL_SECS` | `3` | Status check interval |
| `HEARTBEAT_WARNING_THRESHOLD` | `2` | Missed heartbeats → DEGRADED |
| `HEARTBEAT_CRITICAL_THRESHOLD` | `4` | Missed heartbeats → CRITICAL |

### Status Levels

| Status | Description |
|--------|-------------|
| **Online** | All heartbeats received |
| **Degraded** | 2-3 missed heartbeats |
| **Critical** | 4+ missed heartbeats |
| **Offline** | Timeout exceeded |

---

## Reverse Proxy Configuration

### Nginx

```nginx
server {
    listen 443 ssl;
    server_name betterdesk.example.com;

    ssl_certificate /etc/letsencrypt/live/betterdesk.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/betterdesk.example.com/privkey.pem;

    # Web Console
    location / {
        proxy_pass http://127.0.0.1:5000;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    # WebSocket (device status push)
    location /ws/ {
        proxy_pass http://127.0.0.1:5000;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

> **Note:** Signal, relay, and client API ports (21114-21121) should NOT go through the reverse proxy. They use custom TCP/UDP protocols, not HTTP.

---

## See also

- [[TLS / SSL Certificates|TLS-SSL]] — certificates and dual-mode TLS
- [[Installation]] — default paths and services
- [[Docker Deployment|Docker]] — container env vars
- [[Panel Updates|Panel-Updates]] — `.env` merge on update
