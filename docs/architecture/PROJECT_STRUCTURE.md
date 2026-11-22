# BetterDesk — Project Structure

Current layout of the BetterDesk monorepo (one Go server with the web console built in). Legacy Flask / `hbbs-patch` trees were removed in the 2.x → 3.x era.

## Top-level layout

```
BetterDesk/
├── betterdesk-server/          # Go signal + relay + HTTP API + web console (single binary)
├── web-ui/                     # The console's pages (Svelte 5, SvelteKit single-page app)
├── sdks/
│   ├── nodejs/                 # CDAP Node.js SDK (betterdesk-cdap)
│   └── python/                 # CDAP Python SDK
├── bridges/                    # Reference CDAP bridges (modbus, snmp, rest-webhook)
├── remote-gateway/             # Optional guacd (RDP/VNC gateway) service files
├── config/                     # RustDesk upstream watch state
├── docker/                     # Entrypoints and helpers
├── docs/                       # Architecture, setup, security, wiki
├── scripts/                    # Version bump, toolchain, i18n and local checks, wiki sync
├── contrib/                    # Community contrib (e.g. FreeBSD rc.d)
├── .github/                    # CI, Dependabot, CodeQL
├── Dockerfile                  # The betterdesk image (one process: betterdesk-server)
├── Dockerfile.server           # The betterdesk-server image
├── install.sh                  # One-line Docker / native bootstrap
├── betterdesk.sh / .ps1        # Native install / update
├── betterdesk-docker.sh        # Docker-oriented installer
├── CHANGELOG.md
└── VERSION
```

**Client Generator:** The console builds BetterDesk-Client template bundles and stores them
under the console data module. The withdrawn `betterdesk-support-agent/`,
`betterdesk-agent/`, and `betterdesk-agent-client/` source trees are not part of the
Docker images or the panel update path.

## Core components

### `betterdesk-server/`
Clean-room Go implementation replacing RustDesk `hbbs`+`hbbr`: UDP/TCP/WS signal, relay, REST API, JWT/RBAC, SQLite/PostgreSQL, CDAP gateway, MeshCentral compat, and the web console (`console/`: its routes, listeners on 5000 and 21121, i18n with 26 locales, updater). Pure Go (no CGO). Module: `go.mod` (toolchain pinned).

### `web-ui/`
The console's pages: Svelte 5 and SvelteKit as a single-page app, built with Vite into `web-ui/build`. `go run ./cmd/embed-webui` in `betterdesk-server/` copies the build into the binary before `go build`. Node.js (22.17 or newer) is needed only to build it. See `web-ui/README.md`.

### `sdks/` + `bridges/`
CDAP client libraries and sample industrial/IoT bridges. SNMP bridge uses official **`pysnmp` 7.x** (not the legacy `pysnmplib` fork).

### Docker / install
- Images: `betterdesk` (`Dockerfile`) and `betterdesk-server` (`Dockerfile.server`), both with the web app built in. Build stages: `node:22.23.2-alpine3.24` for the web app, `golang:1.26-alpine` for the server; runtime: `alpine:3.22`
- Compose files at repo root (`docker-compose*.yml`)
- Updates: panel Settings → Updates (installs release binaries) or `betterdesk.sh` / `betterdesk.ps1`

## Documentation hub (`docs/`)

| Area | Path |
|------|------|
| Branching / versioning | `docs/important/branching-and-versioning.md` |
| Update flow | `docs/important/betterdesk-update-flow.md` |
| Docker | `docs/docker/` |
| Security audits | `docs/security/` |
| Wiki mirror | `docs/wiki/` |

## Historical note

Older docs and installers may still mention Flask consoles, `hbbs-patch/`, or a `deprecated/ban_enforcer.py` tree. Those components are **not** in this repository anymore. Use `betterdesk-server` + `web-ui` only.

## For contributors

1. Work on **`dev`** by default (see branching docs).
2. Run `betterdesk-server` → `go test ./...` (+ `govulncheck`); `web-ui` → `npm run check` and `npm test`.
3. Keep locale keys in sync across all `betterdesk-server/console/static/files/lang/*.json` files (`go run ./cmd/repotool i18n-check --system console` in `betterdesk-server`).

---

Last updated: 2026-11-21
