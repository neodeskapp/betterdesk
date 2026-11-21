# BetterDesk — Official All-in-One Container Image

**Image:** `ghcr.io/unitronix/betterdesk`

This is the **recommended** BetterDesk Docker deployment. One container runs one process, `betterdesk-server`, which serves signal, relay, the HTTP API and the web console.

## Quick start

```bash
curl -fsSL https://raw.githubusercontent.com/UNITRONIX/BetterDesk/main/docker-compose.quick.single.yml -o docker-compose.yml
docker compose pull && docker compose up -d
```

Or use the one-line installer (defaults to this image):

```bash
curl -fsSL https://raw.githubusercontent.com/UNITRONIX/BetterDesk/main/install.sh | sudo bash
```

## Update

```bash
docker compose pull && docker compose up -d
```

Pin a release tag with `BETTERDESK_IMAGE_TAG` (see [DOCKER_QUICKSTART.md](DOCKER_QUICKSTART.md)).

## Ports

| Port | Service |
|------|---------|
| 5000 | Web console |
| 21115 | NAT test |
| 21116 | Signal (TCP/UDP) |
| 21117 | Relay |
| 21118–21119 | WebSocket signal/relay |
| 21121 | HTTP API (RustDesk client + REST) |

## Server-only image

`ghcr.io/unitronix/betterdesk-server` is the same server with its web console off (`CONSOLE_PORT=0`). An older two-container install (server and console containers) moves to the all-in-one image with `install.sh --migrate-split`; the named volumes are kept.
