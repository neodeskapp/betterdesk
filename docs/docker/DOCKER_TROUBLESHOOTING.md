# 🐳 Docker Troubleshooting for BetterDesk

BetterDesk runs as one container, `betterdesk`, with one process: signal, relay, Client API and web console. The commands below use that service name.

## Problem: DNS Failure During Build (AlmaLinux/CentOS)

### Symptom
The local build (`docker compose -f docker-compose.single.yml up -d --build`) stops while it downloads packages or modules, with a name resolution error.

### Cause
Docker on some RHEL-based systems (AlmaLinux, CentOS, Rocky Linux) and on Oracle Cloud VMs can have DNS resolution issues during build.

### ✅ Solutions

**Option 1: Configure Docker DNS (recommended)**
```bash
# Edit Docker daemon config
sudo mkdir -p /etc/docker
sudo tee /etc/docker/daemon.json > /dev/null <<EOF
{
    "dns": ["8.8.8.8", "1.1.1.1"]
}
EOF

# Restart Docker
sudo systemctl restart docker

# Rebuild
docker compose -f docker-compose.single.yml build --no-cache
```

**Option 2: Use host network during build**
```bash
# Build with host network
docker build --network=host -t betterdesk:local .

# Then start normally
docker compose -f docker-compose.single.yml up -d
```

**Option 3: Disable IPv6 in Docker (if IPv6 issues)**
```bash
# Add to /etc/docker/daemon.json
{
    "dns": ["8.8.8.8", "1.1.1.1"],
    "ipv6": false
}
```

---

## Problem: Volume Mount Permission Denied (SELinux — AlmaLinux/RHEL/CentOS)

### Symptom
```
Error: EACCES: permission denied, open '/opt/rustdesk/db_v2.sqlite3'
Error: cannot open database file
```

Or containers fail to start with permission errors when using bind mounts.

### Cause
SELinux-enabled systems (AlmaLinux, RHEL, CentOS, Rocky Linux) require special volume mount options or SELinux context changes for bind mounts.

On Synology / NAS bind mounts, host folders are often owned by DSM UIDs that are **not** the image default `10001:10001`. Prefer setting `PUID`/`PGID` (see below) rather than Compose `user:`.

### ✅ Solutions

**Option 1: Use Named Volumes (recommended)**

The default docker-compose.yml uses named volumes which work correctly with SELinux:
```yaml
volumes:
  - rustdesk-data:/opt/rustdesk    # Named volume - SELinux compatible
  - console-data:/app/data         # Named volume - SELinux compatible
```

**Option 2: Set PUID/PGID for bind mounts (#376)**

Map the in-container `betterdesk` user to the host directory owner (linuxserver-style). Defaults remain `10001:10001`.

```yaml
environment:
  - PUID=1000013
  - PGID=1000001
```

Restart the stack after changing these values so the entrypoint can remount ownership. Avoid Compose `user:` — it interferes with `su-exec`.

**Option 3: Add `:z` flag for Bind Mounts (SELinux)**

If you must use bind mounts (host paths), add the `:z` suffix:
```yaml
volumes:
  - /opt/betterdesk:/opt/rustdesk:z     # :z makes it SELinux-compatible
  - /opt/console-data:/app/data:z
```

**Option 4: Apply SELinux Context Manually**
```bash
# Apply container-compatible SELinux context to directories
sudo chcon -Rt svirt_sandbox_file_t /path/to/data/directory

# Example for BetterDesk
sudo chcon -Rt svirt_sandbox_file_t /opt/betterdesk
sudo chcon -Rt svirt_sandbox_file_t /opt/console-data
```

**Option 5: Temporarily Disable SELinux (not recommended for production)**
```bash
# Set SELinux to permissive mode temporarily
sudo setenforce 0

# Start containers
docker compose up -d

# Re-enable SELinux
sudo setenforce 1
```

> **Note:** The `betterdesk-docker.sh` script automatically handles SELinux contexts for RHEL-based systems.

---

## 📋 Troubleshooting

### Problem: Permission denied reading `.admin_credentials`

**Symptom:** After install, this fails:

```bash
docker compose exec betterdesk sh -c 'cat /opt/rustdesk/.admin_credentials'
# cat: can't open '/opt/rustdesk/.admin_credentials': Permission denied
```

**Cause:** Hardened Docker images use `cap_drop: [ALL]`. `docker compose exec` runs as root, but without `CAP_DAC_OVERRIDE` root cannot read `.admin_credentials` (mode `0600`, owned by the `betterdesk` user). The web panel still works because the server runs as `betterdesk`.

**Fix (recommended):**

```bash
docker compose exec betterdesk betterdesk-show-admin-credentials
```

**Fix (works on images before the helper was added):**

```bash
docker compose exec -u betterdesk betterdesk sh -c 'cat /opt/rustdesk/.admin_credentials 2>/dev/null || cat /app/data/.admin_credentials'
```

**Do not** run `chmod 777` on the credentials file — that makes the bootstrap password world-readable.

If no file is found yet, wait for first boot to finish and check `docker compose logs betterdesk` for the bootstrap message. On a running installation, the file is only a recovery aid; it is not the database of users.

### Problem: Panel login fails but `/opt/rustdesk/.admin_credentials` looks correct (#385)

**Symptom:** Fresh Docker install (`docker-compose.quick.single.yml`, `docker-compose.single.yml` or `install.sh`). You read the bootstrap password from `/opt/rustdesk/.admin_credentials` (or `betterdesk-show-admin-credentials`), but the web panel at `:5000` returns **Invalid username or password**.

**Cause:** On first boot without `ADMIN_PASSWORD`, the server generates a random password for the first admin and writes it to the credentials file. The file is a recovery aid, not the user database: when the stored admin was made by another start (another volume, an older install), the file does not match the password hash stored for the admin user. Fresh SQLite installations use the centralized `/opt/rustdesk/db_v2.sqlite3` store (and PostgreSQL installations use the primary PostgreSQL database); `auth.db` is only a legacy migration path.

**Also common:** wiping only the `/opt/rustdesk` bind mount or volume while keeping `/app/data` (`console-data`). An old `auth.db` forces legacy panel authentication with a stale password hash, while bootstrap regenerates `.admin_credentials` on the RustDesk volume.

**Also common:** commenting out `INIT_ADMIN_PASS` / `DEFAULT_ADMIN_PASSWORD` / `ADMIN_PASSWORD` in `docker-compose*.yml`. `ADMIN_PASSWORD=… docker compose up -d` on the host only passes the password into the container when those `${ADMIN_PASSWORD}` lines are present in the compose file.

**Also common on ARM64:** after a failed ARM CI phase, `:dev` / tip tags may briefly be **amd64-only** (soft-merge keeps the amd64 channel publishing). Confirm with `docker buildx imagetools inspect ghcr.io/unitronix/betterdesk:dev` — expect both `linux/amd64` and `linux/arm64` on a healthy publish. If only amd64 is listed, wait for the next green Docker publish or use a local build (`docker-compose.single.yml`) meanwhile.

**Workaround (existing broken install):**

```bash
# For a disposable/test installation, remove BOTH Docker stores:
docker compose down
# Named volumes:
docker compose down -v
# Bind mounts — wipe both directories, e.g.:
# rm -rf ./betterdesk-data/* ./betterdesk-console-data/*
ADMIN_PASSWORD='YourSecurePassword123' docker compose up -d
```

Do not delete only the `/opt/rustdesk` bind mount while keeping `console-data`: that is not a clean reset. Do not use a credentials file to overwrite an existing user's password; use the normal password-reset procedure instead.

**Fix:** Setting `ADMIN_PASSWORD` before first start is the deterministic option — keep the `INIT_ADMIN_*` / `DEFAULT_ADMIN_*` / `ADMIN_PASSWORD` env mappings in your compose file. Check `docker logs` for `Bootstrap: INIT_ADMIN_PASS/DEFAULT_ADMIN_PASSWORD set=yes|no` (password itself is never logged).

**Diagnose before wiping data:** First determine whether the failure is authentication, CSRF, rate limiting, or session persistence. Run this against a disposable or maintenance window deployment; replace `<PASSWORD>` locally and do not paste it into an issue:

```bash
# Confirm the effective paths. A credentials file is not itself an
# identity store.
docker compose exec betterdesk sh -lc '
  printf "DB_TYPE=%s DB_URL=%s DB_PATH=%s AUTH_DB_PATH=%s\n" \
    "$DB_TYPE" "$DB_URL" "$DB_PATH" "$AUTH_DB_PATH"
'

# Inspect every possible identity store and the credential file. On a fresh
# consolidated SQLite install, db_v2.sqlite3 contains the admin row and
# /app/data/auth.db should be absent.
docker compose exec -u betterdesk betterdesk sh -lc '
  for db in /opt/rustdesk/db_v2.sqlite3 /app/data/db_v2.sqlite3 /app/data/auth.db; do
    if [ -f "$db" ]; then
      echo "=== $db ==="
      sqlite3 "$db" \
        "SELECT username, substr(password_hash,1,30), role FROM users;"
    else
      echo "=== $db: absent ==="
    fi
  done
  echo "=== credentials ==="
  ls -la /opt/rustdesk/.admin_credentials /app/data/.admin_credentials 2>&1
'

# Capture the CSRF token from the login page, then log in from inside the
# container so Nginx Proxy Manager is not part of this test.
docker compose exec betterdesk sh -lc '
  curl -sS -c /tmp/bd-cookies http://127.0.0.1:5000/login > /tmp/bd-login.html
  grep -o "csrfToken: '[^']*'" /tmp/bd-login.html
  # Copy the token printed above into <CSRF_TOKEN>.
  curl -i -sS -b /tmp/bd-cookies -c /tmp/bd-cookies \
    -H "Content-Type: application/json" \
    -H "X-CSRF-Token: <CSRF_TOKEN>" \
    --data "{\"username\":\"admin\",\"password\":\"<PASSWORD>\"}" \
    http://127.0.0.1:5000/api/auth/login
  curl -i -sS -b /tmp/bd-cookies http://127.0.0.1:5000/api/auth/verify
'

docker compose logs betterdesk 2>&1 | grep -E '\[AUTH\]|CSRF|Too many login|rate'
```

Interpret the result as follows:

- `401` plus `password mismatch` or `user not found`: wrong identity store, stale hash, or bootstrap state.
- `403` or a CSRF error: cookie/token or proxy handling, not the password hash.
- `429`: login rate limiting; set `TRUST_PROXY=1` for one trusted reverse proxy so client IPs are not collapsed to the NPM address.
- `200` from login followed by `401` from `/api/auth/verify`: session/cookie persistence or proxy headers.
- `200` from both requests: the database credentials work; compare NPM access, forwarded headers, and browser cookies.

When the console is behind Nginx Proxy Manager, set `TRUST_PROXY=1` on the `betterdesk` service and ensure NPM forwards `X-Forwarded-Proto`, `X-Forwarded-For`, and WebSocket upgrade headers. This fixes proxy/session diagnostics and rate-limit attribution; it does not by itself repair a mismatched SQLite password hash.

### Problem: `betterdesk-show-admin-credentials: executable file not found`

**Symptom:** After `install.sh` or `docker compose exec … betterdesk-show-admin-credentials`:

```text
OCI runtime exec failed: exec failed: … executable file not found in $PATH
```

**Cause:** The running image is older than the helper (#195 / 3.2.17+). `install.sh` used to pin a stale default tag while compose defaults moved ahead (#299).

**Fix:**

```bash
# Pull the current tag (match VERSION / compose default), then recreate:
cd /opt/betterdesk/docker   # or your compose directory
# Ensure .env has BETTERDESK_IMAGE_TAG=<current VERSION>, e.g. 3.5.37
docker compose pull && docker compose up -d

docker compose exec betterdesk betterdesk-show-admin-credentials

# Until you can pull a newer image:
docker compose exec -u betterdesk betterdesk \
  sh -c 'cat /opt/rustdesk/.admin_credentials 2>/dev/null || cat /app/data/.admin_credentials'
```

### Problem: Password reset says `No such container: betterdesk-console`

**Symptom:** `betterdesk-docker.sh` → reset admin password fails with:

```text
Error response from daemon: No such container: betterdesk-console
```

**Cause:** The install runs one container named `betterdesk`; an older `betterdesk-docker.sh` looked for `betterdesk-console` (#299).

**Fix:** Update `betterdesk-docker.sh`, or reset manually (the password goes on stdin):

```bash
printf '%s\n' 'YourNewPassword' | docker exec -i -u betterdesk betterdesk /usr/local/bin/betterdesk-server reset-password admin
```

### Problem: Hardened bind mounts regenerate `.api_key` or restart after credentials cleanup (#424)

**Symptom:** An all-in-one container uses `cap_drop: [ALL]`, a bind-mounted
`/opt/rustdesk` owned by UID/GID `10001:10001` with mode `0700`, and logs
`Auto-generated API key` on every restart. If `.admin_credentials` was removed
from an installation that already has users, the container may also enter a
supervisord restart loop with an `ENV_DEFAULT_ADMIN_*` or `ENV_INIT_ADMIN_*`
format-string error.

**Cause:** Root cannot traverse a `0700` app-owned directory after
`CAP_DAC_READ_SEARCH` and `CAP_DAC_OVERRIDE` are dropped. Older images tested
for files as root, so existing app-owned files looked absent. Older bootstrap
paths could also leave the variables required by supervisord undefined.

**Fix:** Pull or rebuild the current image and recreate the container:

```bash
docker compose pull
docker compose up -d --force-recreate
```

Current entrypoints perform data-file existence checks as `betterdesk` and
export empty admin defaults when no replacement password is appropriate. Do
not add `DAC_READ_SEARCH` just to work around this issue and do not delete
`.admin_credentials` to change a password; use the normal password-reset flow.
For custom bind mounts, keep the data directory accessible to the container's
configured `PUID`/`PGID` (defaults to `10001:10001`) and keep it private, for
example:

```bash
chown -R 10001:10001 ./betterdesk-data
chmod 700 ./betterdesk-data
```

**Verify after updating:**

```bash
first_key=$(docker compose exec -T -u betterdesk betterdesk \
  sh -c 'cat /opt/rustdesk/.api_key')
docker compose restart betterdesk
docker compose up -d --wait
second_key=$(docker compose exec -T -u betterdesk betterdesk \
  sh -c 'cat /opt/rustdesk/.api_key')
test "$first_key" = "$second_key"
docker compose logs betterdesk 2>&1 \
  | grep -E 'ENV_(DEFAULT_ADMIN|INIT_ADMIN)|Auto-generated API key' || true
```

### Problem: Browser shows `SSL_ERROR_RX_RECORD_TOO_LONG` (or Chrome “ERR_SSL_PROTOCOL_ERROR”)

**Symptom:** After a fresh Docker install or `docker compose pull`, Firefox Advanced details show:

```text
SSL_ERROR_RX_RECORD_TOO_LONG
The page you are trying to view cannot be shown because the authenticity
of the received data could not be verified.
```

Chrome/Edge often report `ERR_SSL_PROTOCOL_ERROR` for the same case.

**Cause:** The browser used **HTTPS** against a port that speaks plain **HTTP**. Official GHCR quick-start / all-in-one images serve the web panel as **HTTP on port 5000** by default (`HTTPS_ENABLED` is off). Common triggers (#299):

- Opening `https://<host>:5000` (bookmark, autocomplete, Portainer “Open”)
- Mapping host **443 → container 5000** and browsing `https://…` without a TLS terminator
- A reverse proxy that forwards TLS to the panel without terminating SSL

**Fix:**

1. Use **`http://<host>:5000`** (not `https://`).
2. Confirm the panel is up over HTTP:
   ```bash
   curl -v http://127.0.0.1:5000/health
   docker compose logs --tail=80
   ```
   Expect a healthy HTTP response and a startup banner showing **HTTP**.
3. Correct Portainer / compose port maps so host 443 is not pointed at the plain-HTTP panel port unless a proxy terminates TLS.

**If you need TLS:** put nginx / Traefik / Caddy in front, or enable panel HTTPS (`HTTPS_ENABLED` + certs) — see [DOCKER_QUICKSTART — SSL/TLS](DOCKER_QUICKSTART.md#ssltls) and [HTTPS_SETUP.md](../setup/HTTPS_SETUP.md).

### Problem: "Database not found"
```bash
# The SQLite database and keys live on the betterdesk-data volume
docker compose exec -u betterdesk betterdesk ls -la /opt/rustdesk/
```

### Problem: Container won't start
```bash
# Check logs of all containers
docker compose logs

# Check status
docker compose ps
```

## 🔧 Useful commands

```bash
# Logs
docker compose logs -f betterdesk

# Shell in the container (Alpine: sh, not bash)
docker compose exec -u betterdesk betterdesk sh

# Restart
docker compose restart betterdesk

# Check database status
docker compose exec -u betterdesk betterdesk sqlite3 /opt/rustdesk/db_v2.sqlite3 ".tables"

# Check users in database
docker compose exec -u betterdesk betterdesk sqlite3 /opt/rustdesk/db_v2.sqlite3 "SELECT username, role FROM users;"
```

## 🔒 Security & Updates

### ⚠️ Watchtower Removed

**Important**: Watchtower has been removed from docker-compose.yml as it's **no longer maintained** and poses a security risk.

### ✅ Safe Update Methods

```bash
# Method 1: Manual updates (recommended)
docker-compose pull && docker-compose down && docker-compose up -d

# Method 2: Update the one service
docker-compose pull betterdesk
docker-compose up -d betterdesk

# Method 3: Check for updates first
docker images --format "table {{.Repository}}\t{{.Tag}}\t{{.CreatedAt}}\t{{.Size}}"
```

### 🤖 Automated Alternatives

Instead of Watchtower, consider modern secure alternatives:

1. **GitHub Dependabot** - Automatic dependency updates via PR
2. **Renovate Bot** - Advanced dependency management 
3. **Custom scripts** with notifications
4. **Kubernetes operators** (for K8s environments)

### 📅 Update Schedule

```bash
# Weekly security check (add to cron)
#!/bin/bash
cd /path/to/BetterDesk-Console
docker-compose pull --quiet
if [ $? -eq 0 ]; then
    echo "Updates available - review and apply manually"
    docker images --format "table {{.Repository}}\t{{.Tag}}\t{{.CreatedAt}}"
fi
```

---

**⚠️ IMPORTANT**: After first login, always change the default administrator password in the console settings!

---

## Problem: Web Remote fails on every device (`initiator_not_registered`)

### Symptom

Browser Web Remote cannot connect to **any** online peer. Security → Audit Events shows:

```text
connection_denied
reason=initiator_not_registered
target_id=<peer id>
```

Desktop↔desktop may still work.

### Cause

The browser's Web Remote goes through the console's bridge, which opens a TCP connection to the signal server on `:21116`. The panel never registers as a RustDesk peer, so the signal server lets the bridge start connections only when both hold:

- The bridge's first message carries the secret the signal server made at start. The bridge runs in the same process and knows it; nothing else does.
- The bridge's source address is in `PANEL_SIGNAL_PROXY_CIDRS` (default `127.0.0.0/8,::1/128`).

A loopback address alone does not count. Every other connection is treated as any client, which needs a registered peer.

The second condition fails when `WS_HBBS_HOST` points the bridge at an address other than loopback, so its connection comes from that interface's address.

### Solution

Leave `WS_HBBS_HOST` unset (the bridge then uses `localhost`), or add the bridge's source address to `PANEL_SIGNAL_PROXY_CIDRS` on the `betterdesk` service:

```env
PANEL_SIGNAL_PROXY_CIDRS=<source-ip>/32,127.0.0.0/8,::1/128
```

Recreate the container after changing env:

```bash
docker compose up -d
```

### Verify

1. Open Web Remote to an online peer — password prompt / stream should appear.
2. New audit rows should not show `initiator_not_registered` for those attempts (initiator is treated as `panel-web-remote`).

---

## Problem: Logged-in client fails with `initiator_token_rejected`

### Symptom

- RustDesk desktop connects when **logged out**, but fails when **logged in**.
- Go / audit logs show:

```text
Rejected outbound … reason=initiator_token_rejected
connection_denied … reason:initiator_token_rejected
```

Approving the **target** device does not fix this — the reject is about the **initiator’s** login session token, not enrollment of the remote peer.

### Cause

The signal server received a 64-character opaque client login token on PunchHole/RequestRelay, but that token hash is missing, expired, or revoked in the **same** database Go uses (`client_sessions`). Common Docker/VPS causes:

1. **Source-build compose not rebuilt** — `docker-compose.single.yml` uses `betterdesk:local` + `pull_policy: never`. `docker compose pull` does not update that image; use `git pull` + `docker compose up -d --build`, or switch to `docker-compose.quick.single.yml` (GHCR) and pull there.
2. **Wrong Client API URL** — API Server pointed at panel `:5000` or only a partial Nginx path (e.g. `/api/ws/` → Go while `/api/login` still hits the panel). Login may “succeed” in the UI without writing a Go `client_sessions` row that PunchHole can validate.
3. **Stale token** — an old local token after image/DB recreate.
4. **Misconfigured TLS behind Nginx** — `TLS_API=Y` (or signal/relay TLS) without certs, or double TLS when the reverse proxy already terminates HTTPS. Upstream to Go should stay plain HTTP.

This is usually **not** a pure WSS/firewall failure: if WebSockets were completely broken, anonymous (logged-out) connections would typically fail too.

### Fix checklist

1. **Refresh the correct way**

```bash
# GHCR quick compose (images from ghcr.io):
docker compose pull && docker compose up -d --force-recreate

# Source build (local tags — pull does nothing):
git pull
docker compose up -d --build --force-recreate
```

Confirm the running version matches what you expect:

```bash
curl -sS http://127.0.0.1:21121/api/health
docker compose ps
```

2. **Point the RustDesk client at the Client API, not the panel**

API Server: `http://YOUR_HOST:21121` (or HTTPS if you terminate TLS for that port).

Do **not** use panel port `5000` as API Server. Behind Nginx, either expose `:21121` directly, or proxy **Client API** paths to it — not only `/api/ws/`. Prefer a separate API hostname; see [REVERSE_PROXY.md](../setup/REVERSE_PROXY.md#client-api-vs-panel-on-the-same-hostname).

3. **Re-login after recreate**

In the Windows RustDesk client: sign out → sign in again with a panel user allowed to connect. Old tokens after image/DB changes produce exactly this log line.

4. **Reverse-proxy / production env hygiene**

- Native WSS: `/ws/id` → host `21118`, `/ws/relay` → host `21119` (Upgrade headers on). Do not send those paths to panel `:5000`.
- When Nginx terminates TLS: leave `TLS_API` / `TLS_SIGNAL` / `TLS_RELAY` unset unless you configure `TLS_CERT`/`TLS_KEY` for Enterprise dual-TLS.
- Prefer `ENCRYPTED_ONLY=1`, `ALLOW_LEGACY_OUTBOUND=N`, and `ENROLLMENT_MODE=managed` after devices are approved.
- Set a reachable relay: `RELAY_SERVERS=YOUR_PUBLIC_IP_OR_DNS:21117`.

### Diagnostics (no passwords / no tokens)

```bash
docker compose ps
docker compose config | grep -E 'image:|pull_policy|DB_URL|DB_PATH|RELAY_SERVERS|TLS_|ALLOW_LEGACY'

curl -sS http://127.0.0.1:21121/api/health
curl -sS http://127.0.0.1:21121/api/login-options

# After a failed logged-in connect (redact secrets; do not paste access tokens):
docker compose logs --tail=300 | grep -E 'initiator_token_rejected|/api/login|issueClientSession|Token generation'
```

A healthy logged-in flow shows a successful `/api/login` (or `issueClientSession`) shortly before PunchHole. If PunchHole rejects with `initiator_token_rejected` and there is **no** matching login/issue line for that attempt, the client is not talking to the Client API you think it is.

### What to paste when asking for help

Redact passwords, keys, and bearer tokens. Include:

- which compose file (and whether the image is `ghcr.io/…` or `betterdesk:local`)
- RustDesk client version; ID / Relay / API host+ports only (no key)
- whether a reverse proxy fronts WSS / API
- a few log lines around the next `initiator_token_rejected` plus any `/api/login` / `issueClientSession` lines from the same attempt

Related: [API port consolidation](../important/betterdesk-api-port-consolidation.md), [REVERSE_PROXY.md](../setup/REVERSE_PROXY.md), [Client Setup — login](../wiki/Client-Setup.md#client-login).
