# Panel Updates

BetterDesk can update itself through **Settings → Updates** in the web panel, or via **`betterdesk.sh` / `betterdesk.ps1`**.

---

## Native install (recommended flow)

1. Log in as **super_admin** or **server_admin**
2. Open **Settings → Updates**
3. Click **Check for updates** and review the available version and changelog preview; the panel runs its preflight checks and shows anything that blocks the install
4. Click **Install** — the panel downloads the `betterdesk-server` release binary for this platform from GitHub, checks its SHA-256 (and the manifest signature when the release is signed), backs up the running binary, swaps in the new one and restarts the server. The web console is part of that binary; no toolchain, Node.js or source is needed on the host.

If the new binary does not start properly, the server puts the previous binary back on its fourth failed start and the panel shows the failed update.

### What is preserved

- Database files (`db_v2.sqlite3`, legacy `auth.db`, PostgreSQL data)
- User passwords and TOTP secrets
- SSL certificates and API keys
- Operator settings and secrets in `.env` (a script update appends only **missing keys**)

### Update channels

| Channel | Branch | Use |
|---------|--------|-----|
| **Stable** | `main` | Production (default) |
| **Development** | `dev` | Latest work-in-progress |

Switch under **Settings → Updates → Update channel**, or via installer script.

---

## Script-based update

```bash
# Linux
sudo ./betterdesk.sh   # option 2 — Update

# Windows
.\betterdesk.ps1       # option 2 — Update
```

Script updates also clear stale panel warning banners from failed in-panel attempts.

---

## Docker (GHCR images)

When running pre-built images from GHCR (`docker-compose.quick.single.yml`):

- In-panel **Install** is disabled — use image pull instead
- Panel shows pull instructions:

```bash
docker compose pull && docker compose up -d
```

Each image embeds its build commit; startup syncs `data/.update_sha`.

---

## Troubleshooting updates

| Symptom | Fix |
|---------|-----|
| Red banner after successful script update | Update to latest build — banner cleared on script/Docker success (#192) |
| Server update failed | Check the preflight notes; use **Rebuild server binary** in Updates to install the running version's release again |
| Permission errors on `/opt/` | Fix ownership: `sudo ./betterdesk.sh --repair-permissions` |
| Stale server binary | Use **Rebuild server binary**, or update with the installer script |

---

## See also

- [[Installation]] — fresh install paths
- [[Troubleshooting]] — post-update issues
- [Update flow doc](https://github.com/UNITRONIX/BetterDesk/blob/main/docs/important/betterdesk-update-flow.md)
