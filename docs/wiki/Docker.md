# Docker

Run BetterDesk in containers. Quickest path uses pre-built GHCR images.

---

## Quick Start (Pre-built Images)

Use the official **all-in-one** image (no build required). One container runs `betterdesk-server`, which serves signal, relay, API and the web console in one process:

```bash
curl -fsSL https://raw.githubusercontent.com/trybetterdesk/betterdesk/dev/docker-compose.quick.single.yml \
  -o docker-compose.yml
docker compose pull && docker compose up -d
```

Default access:
- **Web Console:** http://localhost:5000
- **Client API:** `:21121` — never panel `:5000`
- **Default admin:** `docker compose exec betterdesk betterdesk-show-admin-credentials`

**Image tags:** prefer pinned versions or `:latest` for stable; `:dev` tracks the development channel. The all-in-one (`betterdesk`) and server-only (`betterdesk-server`) **`:dev` images are multi-arch** (amd64 + arm64). CI builds ARM after amd64; if ARM fails, tags may briefly be amd64-only until the next green ARM publish.

### Source build vs GHCR updates

| File | Images | Update |
|------|--------|--------|
| `docker-compose.quick.single*.yml` | `ghcr.io/unitronix/betterdesk` | `docker compose pull && docker compose up -d` |
| `docker-compose.single.yml` | `betterdesk:local` (`pull_policy: never`) | `git pull` then `docker compose -f docker-compose.single.yml up -d --build` |

`docker compose pull` does **not** refresh local source-build tags. See [DOCKER_QUICKSTART](../docker/DOCKER_QUICKSTART.md#source-build-vs-prebuilt-ghcr-do-not-mix-update-commands) and [initiator_token_rejected](../docker/DOCKER_TROUBLESHOOTING.md#problem-logged-in-client-fails-with-initiator_token_rejected).

---

## Deployment Options

### Option 1: Build from source

```bash
git clone https://github.com/UNITRONIX/BetterDesk.git
cd BetterDesk
docker compose -f docker-compose.single.yml up -d --build
```

The image builds the web app, embeds it into `betterdesk-server` and runs that one process. The web console listens on **5000** and the Client API on **21121**.

> ⚠️ **Note:** The signal port is always `SIGNAL_PORT` (default `21116`). A `PORT` variable from an older compose file does not change it.

### Option 2: Interactive Docker Script

```bash
./betterdesk-docker.sh
```

Interactive menu with:
- New installation
- Update
- Migrate from existing RustDesk Docker
- Database migration (SQLite ↔ PostgreSQL)

---

## Ports

Expose the following ports in your Docker configuration:

```yaml
ports:
  - "21115:21115"   # NAT test
  - "21116:21116"   # Signal (TCP)
  - "21116:21116/udp"  # Signal (UDP)
  - "21117:21117"   # Relay
  - "21118:21118"   # WS Signal
  - "21119:21119"   # WS Relay
  - "21121:21121"   # API (RustDesk Client API + REST)
  - "5000:5000"     # Web Console
```

In the container the server's API listens on `21121`; there is no separate `21114`.

---

## Volumes

### Data Persistence

```yaml
volumes:
  - betterdesk-data:/opt/rustdesk
  - console-data:/app/data
```

Persist these paths:
- `/opt/rustdesk/` — Database files, keys, API key
- `/app/data/` — Web console data (uploads, fonts, agent builds, legacy `auth.db`)

### Important Files in Volume

| File | Description |
|------|-------------|
| `id_ed25519` | Server private key |
| `id_ed25519.pub` | Server public key |
| `db_v2.sqlite3` | Main SQLite DB (devices, accounts and console settings) |
| `.api_key` | API authentication key |
| `auth.db` | Legacy console auth DB in `/app/data` from older installs; the server copies it into `db_v2.sqlite3` once at its first start |

> ⚠️ Never delete `id_ed25519` — all connected clients use this key. Regenerating it requires reconfiguring every client.

---

## PostgreSQL with Docker

### Add PostgreSQL Container

`docker-compose.single.yml` has a `postgres` profile. Put the settings in a `.env` file next to it:

```env
DB_TYPE=postgres
DATABASE_URL=postgres://betterdesk:your-secure-password@postgres:5432/betterdesk
PG_PASSWORD=your-secure-password
```

```bash
docker compose -f docker-compose.single.yml --profile postgres up -d --build
```

With another compose file, add a `postgres` service and give the `betterdesk` service the database settings:

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: betterdesk
      POSTGRES_PASSWORD: your-secure-password
      POSTGRES_DB: betterdesk
    volumes:
      - postgres-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U betterdesk"]
      interval: 5s
      timeout: 5s
      retries: 5

  betterdesk:
    # ... existing config ...
    environment:
      - DB_TYPE=postgres
      - DATABASE_URL=postgres://betterdesk:your-secure-password@postgres:5432/betterdesk?sslmode=disable
      - DB_URL=postgres://betterdesk:your-secure-password@postgres:5432/betterdesk?sslmode=disable
    depends_on:
      postgres:
        condition: service_healthy

volumes:
  postgres-data:
```

---

## Environment Variables

### `betterdesk` Container

| Variable | Default | Description |
|----------|---------|-------------|
| `SIGNAL_PORT` | `21116` | Signal server port |
| `RELAY_PORT` | `21117` | Relay server port |
| `DB_URL` | `db_v2.sqlite3` | Database path or PostgreSQL DSN |
| `RELAY_SERVERS` | auto-detected | Relay server addresses |
| `API_KEY` | auto-generated | API authentication key |
| `CDAP_ENABLED` | `N` | Enable CDAP gateway |
| `CONSOLE_PORT` | `5000` | Web console port (`0` turns it off) |
| `BETTERDESK_API_URL` | `http://127.0.0.1:21121/api` | Address of the server's API |
| `DB_TYPE` | `sqlite` | Database type (`sqlite` or `postgres`) |
| `DATABASE_URL` | (empty) | PostgreSQL DSN |
| `SESSION_SECRET` | auto-generated | Session encryption key |

---

## Building Images

### Build With Compose

```bash
docker compose -f docker-compose.single.yml build
```

### Build Individual Images

```bash
# All-in-one: server with the web console
docker build -t betterdesk:local .

# Server only, web console off (CONSOLE_PORT=0)
docker build -f Dockerfile.server -t betterdesk-server .
```

---

## Monitoring

### View Logs

```bash
# All services
docker compose logs -f

# The BetterDesk container
docker compose logs -f betterdesk
```

### Health Check

```bash
# API health
curl http://localhost:21121/api/health

# Console health
curl http://localhost:5000/health
```

### Container Status

```bash
docker compose ps
```

---

## Migration from RustDesk Docker

If you have an existing RustDesk Docker deployment:

```bash
./betterdesk-docker.sh
# Choose option M — Migrate from RustDesk Docker
```

The migration:
1. Stops existing RustDesk containers
2. Copies `db_v2.sqlite3` and `id_ed25519` keys
3. Starts BetterDesk containers
4. Verifies data migration

See [[Migration]] for more details.

---

## Troubleshooting

### GHCR "denied" Error

If pre-built images from `ghcr.io` are not available:

```bash
# Option 1: Build locally
docker compose -f docker-compose.single.yml up -d --build

# Option 2: Authenticate with GitHub
echo $GITHUB_TOKEN | docker login ghcr.io -u USERNAME --password-stdin
```

### DNS Resolution Failures During Build

Docker build on some hosts (AlmaLinux, CentOS) may fail DNS resolution. The Dockerfiles include retry logic for `apk add` commands. If issues persist:

```bash
# Add DNS to Docker daemon
echo '{"dns": ["8.8.8.8", "8.8.4.4"]}' | sudo tee /etc/docker/daemon.json
sudo systemctl restart docker
```

### SELinux Volume Mount Issues

On RHEL/CentOS with SELinux:

```bash
# Option 1: Named volumes (recommended)
# Already used in the compose files

# Option 2: :z flag for bind mounts
volumes:
  - ./data:/opt/rustdesk:z

# Option 3: Set context
sudo chcon -Rt svirt_sandbox_file_t ./data
```

---

## See also

- [[Installation]] — bare-metal alternative
- [[Panel Updates|Panel-Updates]] — GHCR image updates
- [GHCR package readme](https://github.com/UNITRONIX/BetterDesk/blob/main/docs/docker/GHCR_PACKAGE_README.md)
