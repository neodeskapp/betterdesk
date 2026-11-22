# Installation

BetterDesk supports **Linux (bare-metal)**, **Windows (PowerShell)**, and **Docker** as primary install paths. **FreeBSD** is Tier 3 / community (manual build + example `rc.d`; no `betterdesk.sh`). Default Linux paths are `/opt/betterdesk` (Go server) and `/opt/BetterDeskConsole` (the web console's `.env` and `data/`); the installer still detects legacy `/opt/rustdesk` installs.

---

## Requirements

### Linux
- Ubuntu 20.04+ / Debian 11+ / CentOS 8+ / AlmaLinux 8+
- 1 CPU core, 512 MB RAM minimum (2 cores, 2 GB recommended)
- Root access (sudo)
- Firewall: follow a profile from [CONNECTION_PORTS.md](../CONNECTION_PORTS.md) (profile B is the recommended one). Do **not** expose the panel (`5000`) to the internet. Quick minimum for clients: 21116 TCP+UDP and 21117 TCP WAN, Client API on `443` or `21114` over TLS.
- Node.js is not needed: the web console is part of the `betterdesk-server` binary. Only a build from source (`--from-source`) needs Node.js 22 and npm, to build the web app.

### Windows
- Windows 10/11 or Windows Server 2019+
- PowerShell 5.1+ (run as Administrator)
- [NSSM](https://nssm.cc/) (auto-installed by script)
- Firewall: same profiles as Linux — see [CONNECTION_PORTS.md](../CONNECTION_PORTS.md). The panel port stays local.

### Docker
- Docker Engine 20.10+ with Docker Compose v2
- 512 MB RAM minimum

### FreeBSD (experimental / community)
- FreeBSD 13+ (amd64); root or doas/sudo
- `pkg` packages: `git`, `go`, `node`, `npm` (to build the web app), `ca_root_nss`
- Open ports (e.g. via `pf`): follow a profile from [CONNECTION_PORTS.md](../CONNECTION_PORTS.md); at minimum 21116 TCP+UDP and 21117 TCP.
- No official installer or release binaries — see [FreeBSD (experimental)](#freebsd-experimental) below and `contrib/freebsd/`

---

## Linux Installation

### Interactive Mode

```bash
git clone https://github.com/UNITRONIX/BetterDesk.git
cd BetterDesk
sudo ./betterdesk.sh
```

The interactive menu offers:

| Option | Description |
|--------|-------------|
| **1** | New installation (full setup from scratch) |
| **2** | Update existing installation |
| **3** | Repair (auto-fix common issues) |
| **4** | Validate installation correctness |
| **5** | Create backup |
| **6** | Reset admin password |
| **7** | Build the server from source and deploy it |
| **8** | Run diagnostics |
| **9** | Uninstall |
| **L** | Minimal installation (server with its web console off) |
| **C** | Configure SSL/TLS certificates |
| **M** | Migrate databases (SQLite ↔ PostgreSQL) |
| **R** | Roll back the upgrade from the Node.js console |

### Automatic Mode

```bash
sudo ./betterdesk.sh --auto
```

Non-interactive install with default settings. Useful for CI/CD or scripted deployments.

### Options

```bash
# Skip SHA256 binary verification
sudo ./betterdesk.sh --skip-verify

# Custom RustDesk Client API port (default 21121; the server's own API stays on GO_API_PORT=21114)
CLIENT_API_PORT=21130 sudo ./betterdesk.sh --auto

# Custom relay servers (overrides auto-detected IP)
RELAY_SERVERS=YOUR.PUBLIC.IP sudo ./betterdesk.sh --auto
```

### Installation Path

After installation, files are located at:

| Path | Description |
|------|-------------|
| `/opt/betterdesk/` | Go server binary, keys, database |
| `/opt/BetterDeskConsole/` | Web console settings (`.env`) and data (`data/`) |
| `/etc/systemd/system/betterdesk-server.service` | The one systemd service: signal, relay, API and web console |

### Verify Installation

```bash
sudo systemctl status betterdesk-server

# Check logs
journalctl -u betterdesk-server -f
```

---

## Windows Installation

### Interactive Mode

Open PowerShell **as Administrator**:

```powershell
git clone https://github.com/UNITRONIX/BetterDesk.git
cd BetterDesk
.\betterdesk.ps1
```

### Automatic Mode

```powershell
.\betterdesk.ps1 -Auto
```

### Options

```powershell
# Skip SHA256 verification
.\betterdesk.ps1 -SkipVerify

# Custom RustDesk Client API port (default 21121; Go admin API is GO_API_PORT=21114)
$env:CLIENT_API_PORT = "21130"
.\betterdesk.ps1 -Auto
```

### Installation Path

| Path | Description |
|------|-------------|
| `C:\BetterDesk\` | Go server binary, keys, database |
| `C:\BetterDeskConsole\` | Web console settings (`.env`) and data (`data\`) |

The one service, `BetterDeskServer`, is registered via NSSM and can be managed from `services.msc`.

---

## Docker Installation

### Quick Start (Pre-built Images)

All-in-one image (server + relay + web console in one container) — recommended:

```bash
curl -fsSL https://raw.githubusercontent.com/trybetterdesk/betterdesk/dev/docker-compose.quick.single.yml -o docker-compose.yml
docker compose pull && docker compose up -d
docker compose exec betterdesk betterdesk-show-admin-credentials
```

Open **http://your-server:5000** (plain HTTP, not HTTPS) and log in with the password printed above.

### Build Locally

```bash
git clone https://github.com/UNITRONIX/BetterDesk.git
cd BetterDesk
docker compose -f docker-compose.single.yml up -d --build
```

### Interactive Docker Script

```bash
./betterdesk-docker.sh
```

See [[Docker]] for detailed Docker documentation.

---

## FreeBSD (experimental)

FreeBSD is **Tier 3 (community)**. There is no `betterdesk.sh` path (that installer assumes Linux + systemd). Build `betterdesk-server` from source with the web app embedded, then optionally install the example `rc.d` script from the repository.

### Quick outline

```sh
pkg install -y git go node npm ca_root_nss

git clone https://github.com/UNITRONIX/BetterDesk.git
cd BetterDesk/web-ui
npx -y npm@11.6.2 ci --no-audit --no-fund
npm run build

cd ../betterdesk-server
go run ./cmd/embed-webui
CGO_ENABLED=0 go build -trimpath -o betterdesk-server .
install -d /usr/local/betterdesk
install -m 755 betterdesk-server /usr/local/betterdesk/
touch /usr/local/betterdesk/.env
# Place id_ed25519 / id_ed25519.pub under /usr/local/betterdesk

install -m 755 ../contrib/freebsd/rc.d/betterdesk_server \
  /usr/local/etc/rc.d/betterdesk_server

sysrc betterdesk_server_enable=YES
sysrc betterdesk_server_relay=YOUR.PUBLIC.IP
service betterdesk_server start
```

The web console then answers on `127.0.0.1:5000`.

Full notes, path defaults, and limits (no panel updater FreeBSD binaries): see [`contrib/freebsd/README.md`](../../contrib/freebsd/README.md) in the repo. Pull requests improving FreeBSD packaging are welcome ([#310](https://github.com/UNITRONIX/BetterDesk/issues/310)).

---

## PostgreSQL Setup

By default, BetterDesk uses SQLite. To use PostgreSQL:

### During Installation

When prompted for database type, choose **PostgreSQL** and provide the connection DSN:

```
postgres://user:password@host:5432/betterdesk?sslmode=disable
```

For a native Linux installation with a local PostgreSQL server, you can select PostgreSQL non-interactively:

```bash
sudo ./betterdesk.sh --auto --postgresql
```

The installer creates the BetterDesk role and database, then configures narrowly scoped `pg_hba.conf` rules for `127.0.0.1/32` and `::1/128` using `scram-sha-256`. It reloads PostgreSQL before testing the same TCP/password connection used by BetterDesk. Remote PostgreSQL hosts are not modified; their authentication policy remains operator-managed.

### Migrate Existing Data

```bash
# Interactive
sudo ./betterdesk.sh
# Choose option M — Migrate databases

# Or use the migration tool directly
./tools/migrate/migrate-linux-amd64 -mode sqlite2pg \
  -src /opt/betterdesk/db_v2.sqlite3 \
  -dst "postgres://user:pass@localhost:5432/betterdesk"
```

See [[Migration]] for more details.

---

## After Installation

1. Open **http://your-server:5000** in a browser
2. Log in with the admin credentials displayed during installation
3. Configure your [[RustDesk clients|Client-Setup]] to connect to your server
4. Optionally configure [[TLS/SSL certificates|TLS-SSL]] for encrypted connections

---

## Upgrading

### From Previous BetterDesk Version

```bash
# Linux
sudo ./betterdesk.sh
# Choose option 2 — Update

# Windows
.\betterdesk.ps1
# Choose option 2 — Update
```

The update process preserves:
- Database files (`db_v2.sqlite3`; legacy `auth.db` from older installs)
- PostgreSQL configuration
- SSL certificates
- API keys
- Admin credentials

An install that still runs the Node.js web console (a separate `betterdesk-console` / `BetterDeskConsole` service) is upgraded in place by the same update, or without questions:

```bash
sudo ./betterdesk.sh --update     # Linux
.\betterdesk.ps1 -Update          # Windows
```

Afterwards one service, `betterdesk-server` (`BetterDeskServer` on Windows), serves the panel on the same ports. Data, keys and accounts stay where they are. Roll back with `sudo ./betterdesk.sh --rollback-go` or `.\betterdesk.ps1 -RollbackGo`.

### From RustDesk OSS Server

If you are migrating from the original RustDesk OSS server (`hbbs`+`hbbr`), the installer detects the legacy Rust server and recommends a fresh install. See [[Migration]] for data migration steps.

---

## Uninstalling

```bash
# Linux
sudo ./betterdesk.sh
# Choose option 9 — Uninstall

# Windows
.\betterdesk.ps1
# Choose option 9 — Uninstall
```

This removes all services, binaries, and optionally data files.

---

## Check it worked

```bash
# Linux service
sudo systemctl status betterdesk-server
curl -s http://localhost:21114/api/health
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:5000/
```

Expect health JSON and HTTP 200 (or a login redirect) from the panel. Then [[Client Setup|Client-Setup]].

---

## See also

- [[Configuration]] — environment variables and service units
- [[Panel Updates|Panel-Updates]] — in-app updates via Settings → Updates
- [[Docker Deployment|Docker]] — container deployment
- [[Migration Guide|Migration]] — SQLite ↔ PostgreSQL, RustDesk OSS migration
- [[Privacy]] — what stays on your server
