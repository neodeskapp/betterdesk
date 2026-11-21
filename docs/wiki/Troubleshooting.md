# Troubleshooting

Common issues and their solutions.

---

## Device Status Issues

### Devices Show as Offline

**Symptoms:** All devices show "Offline" in the web console despite clients being connected.

**Causes & Solutions:**

| Cause | Solution |
|-------|----------|
| Go server not running | `sudo systemctl restart betterdesk-server` |
| Client pointing to wrong server | Verify ID Server address in client settings |
| Public key mismatch | Check `id_ed25519.pub` matches client config |
| Firewall blocking ports | Open 21116 TCP+UDP, 21117 TCP |

### Devices Go Offline Intermittently

**Possible causes:**
- **Strict NAT** — Enable `-always-relay` (`ALWAYS_USE_RELAY=Y`)
- **Firewall timeout** — Check firewall keepalive settings
- **Server resource exhaustion** — Check CPU/memory on server

### "Zombie" Devices Reappearing After Delete

This has been fixed. Signal handlers now check `IsPeerSoftDeleted()` — deleted devices cannot re-register. If you're seeing this, update to the latest version.

---

## Connection Issues

### "Failed to secure TCP: deadline has elapsed"

**Cause:** The TCP signal handler is not sending immediate responses for punch hole/relay requests.

**Solutions:**
1. Update to the latest Go server binary
2. Check TLS configuration — if using self-signed certs, ensure `--tls-api` is NOT set
3. Verify the relay server address on **Settings → Client setup** in the web console
4. If server is behind NAT, set `RELAY_SERVERS=YOUR.PUBLIC.IP`

### Relay Connections Fail

**Common causes:**

| Cause | Solution |
|-------|----------|
| Empty UUID in relay | Update Go server to current 3.5.x |
| Private IP detected | Set `RELAY_SERVERS=YOUR.PUBLIC.IP` |
| Relay port blocked | Open 21117 TCP |
| Public IP detection failed | Check `curl -4 ifconfig.me` from server |

### "client sent an HTTP request to an HTTPS server"

**Cause:** API port 21114 has TLS enabled (`-tls-api`) but a client or reverse proxy connects to it over plain HTTP.

**Solution:** Point that connection at `https://`, or remove the `-tls-api` flag. For self-signed certs:
```bash
# Edit service file
sudo systemctl edit --full betterdesk-server
# Remove -tls-api from ExecStart
sudo systemctl restart betterdesk-server
```

---

## Web Console Issues

### Login Page Redirects in Loop

**Possible causes:**
- Session cookie not being set (check `TRUST_PROXY` setting behind reverse proxy)
- Browser blocking cookies (SameSite policy)
- Session secret changed (all sessions invalidated)

---

## Installation Issues

### `get_public_ip: command not found`

**Cause:** Diagnostics function called undefined function in older script versions.

**Solution:** Update `betterdesk.sh` to latest version. The `get_public_ip()` function is now defined in all scripts.

### PostgreSQL Config Lost After Update

**Cause:** Older scripts overwrote `.env` with SQLite defaults during UPDATE.

**Solution:** Update install scripts to current 3.5.x. They preserve database configuration via `preserve_database_config()`.

### PostgreSQL authentication fails during installation

**Cause:** The PostgreSQL role and database were created successfully, but the server does not have a matching localhost password rule in `pg_hba.conf`. This is common with fresh Rocky Linux or RHEL-compatible PostgreSQL installations.

**Solution:** Update to the current installer and retry. Native local PostgreSQL setup now adds database- and role-specific rules for `127.0.0.1/32` and `::1/128`, reloads PostgreSQL, and tests the TCP connection. For a remote PostgreSQL host, configure its `pg_hba.conf` on the database server instead; BetterDesk intentionally does not edit remote authentication policy.

To inspect the active configuration file:

```bash
sudo -u postgres psql -Atqc 'SHOW hba_file;'
```

Only use loopback-scoped `scram-sha-256` rules for this local setup. Do not open PostgreSQL with `trust` or a network-wide `host all all` rule.

### Auth Database Destroyed After Update

**Cause:** Older install scripts unconditionally deleted `auth.db` and regenerated admin password on every update.

**Solution:** Update to latest scripts. The fix detects existing `.env` as an update indicator and preserves auth.db.

### Password Contains `$` — Service Fails

**Cause:** systemd interprets `$` as variable substitution in service files.

**Solution:** `$` is now escaped to `$$` in service files. Re-run the installer to regenerate service files:
```bash
sudo ./betterdesk.sh
# Choose option 3 — Repair
```

### Windows: `RandomNumberGenerator::Fill` Error

**Cause:** `.NET 6+ static method unavailable in Windows PowerShell 5.1.

**Solution:** Update `betterdesk.ps1` to latest version. Fixed to use `RNGCryptoServiceProvider.GetBytes()`.

---

## Docker Issues

### Port 5000 Conflict (Single Container)

**Cause:** When `SIGNAL_PORT` is unset, the server takes its signal port from a generic `PORT` variable, and an older compose file sets `PORT=5000`.

**Solution:** The image sets `SIGNAL_PORT=21116`. If you replace the container environment, keep it:
```yaml
environment:
  SIGNAL_PORT: "21116"
```

### SELinux Volume Mount Denied

**Solutions:**
```bash
# Option 1: Named volumes (recommended)
volumes:
  betterdesk-data:

# Option 2: :z flag
volumes:
  - ./data:/opt/rustdesk:z

# Option 3: Set SELinux context
sudo chcon -Rt svirt_sandbox_file_t ./data
```

### DNS Resolution Failures During Build

```bash
# Add DNS to Docker daemon
echo '{"dns": ["8.8.8.8", "8.8.4.4"]}' | sudo tee /etc/docker/daemon.json
sudo systemctl restart docker
docker compose -f docker-compose.single.yml build --no-cache
```

---

## Diagnostics

### Run Full Diagnostics

```bash
sudo ./betterdesk.sh
# Choose option 8 — Diagnostics
```

This checks:
- Service status (systemd)
- Port availability
- Database integrity
- API key consistency
- Public IP detection
- TLS certificate validity
- Disk space
- Log errors

### Manual Diagnostics

```bash
# Service status
sudo systemctl status betterdesk-server

# Logs
journalctl -u betterdesk-server --since "1 hour ago" --no-pager

# Port check
ss -tlnp | grep -E '21114|21116|21117|21121|5000'

# API health
curl -s http://localhost:21114/api/health

# Peer count (needs the server's API key)
curl -s -H "X-API-Key: $(sudo cat /opt/betterdesk/.api_key)" http://localhost:21114/api/server/stats

# Public IP
curl -4 ifconfig.me
```

---

## RustDesk Client Session Timeout (#242)

**Symptoms:** Desktop/mobile RustDesk clients lose server login after ~24 hours.

**Solution:** Update to v3.3.129+. Sessions are now DB-backed (7-day sliding, 30-day max). After updating, sign in once in each RustDesk client. Adjust TTL under **Settings → Sign-in → RustDesk client sessions**.

---

## HTTP/HTTPS Port Confusion (#219)

**Symptoms:** After toggling HTTP/HTTPS, Go API calls fail or hit port 21121 instead of 21114.

**Cause:** The console's `.env` uses `API_PORT=21121` for the Client API listener; the server's own API needs `GO_API_PORT=21114`.

**Solution:** Update to latest build. Verify `betterdesk-server.service` includes `Environment=GO_API_PORT=21114`. Restart `betterdesk-server` after toggle.

---

## Getting Help

1. Check the [GitHub Issues](https://github.com/UNITRONIX/BetterDesk/issues) for known problems
2. Run diagnostics and include the output in your bug report
3. Include the BetterDesk server version
4. Include relevant log output (redact sensitive data)

---

## See also

- [[FAQ]] — quick answers
- [[Configuration]] — ports and environment variables
- [[Panel Updates|Panel-Updates]] — update failures and stale banners
- [[Migration Guide|Migration]] — database migration issues
