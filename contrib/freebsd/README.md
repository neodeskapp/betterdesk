# FreeBSD (experimental / community)

BetterDesk has **no official FreeBSD installer** (`betterdesk.sh` is Linux + systemd). This directory has an example `rc.d` script and a manual build path for community / Tier 3 deployments.

Related: [GitHub #310](https://github.com/UNITRONIX/BetterDesk/issues/310).

## Status

| Component | Status |
|-----------|--------|
| `betterdesk-server` (signal, relay, API and web console in one binary) | Build from source (`CGO_ENABLED=0`) |
| `rc.d` example | In this tree, adapt paths as needed |
| Release CI FreeBSD binaries | Not provided |
| Panel Settings → Updates | Expects Linux/Windows assets, use git pull and rebuild |
| Agent on FreeBSD | Not supported |

## Dependencies

```sh
pkg install -y git go node npm ca_root_nss
```

Go must satisfy `betterdesk-server/go.mod` (see its toolchain line). Node.js 22.17 or newer is needed only to build the web app.

## Suggested layout

| Path | Role |
|------|------|
| `/usr/local/betterdesk` | Binary, keys, SQLite database, `.env` and the console's `data/` |
| `/usr/local/etc/rc.d/betterdesk_server` | rc script |

## Build

The web app in `web-ui/` is built first and then embedded into the binary.

```sh
git clone https://github.com/UNITRONIX/BetterDesk.git
cd BetterDesk/web-ui
npx -y npm@11.6.2 ci --no-audit --no-fund   # npm 10 can't resolve its dependencies
npm run build

cd ../betterdesk-server
go run ./cmd/embed-webui
CGO_ENABLED=0 go build -trimpath -o betterdesk-server .

install -d /usr/local/betterdesk
install -m 755 betterdesk-server /usr/local/betterdesk/
touch /usr/local/betterdesk/.env
# Generate or copy id_ed25519 / id_ed25519.pub into /usr/local/betterdesk
```

The build dependencies take about 220 MB in `web-ui/node_modules`; only builds need them. Repeat the build after every `git pull`.

Settings go in `/usr/local/betterdesk/.env` (see `docs/wiki/Configuration.md`). The rc script points the server at that file, and at that folder for the console's state.

## Install the rc.d script

```sh
install -m 755 contrib/freebsd/rc.d/betterdesk_server /usr/local/etc/rc.d/betterdesk_server
```

Enable in `/etc/rc.conf` (or `/etc/rc.conf.d/`):

```sh
betterdesk_server_enable="YES"
betterdesk_server_relay="YOUR.PUBLIC.IP"
# Optional overrides, see the script header
```

Start:

```sh
service betterdesk_server start
```

The web console then answers on `127.0.0.1:5000`. To reach it from the network, put a reverse proxy with TLS in front of it, or set `HOST=0.0.0.0` in `.env`.

## Firewall (`pf`)

Open at least: TCP `21114`–`21119` and `5000` (web console); UDP `21116`. Adjust if you changed ports in `.env` or flags. See `docs/CONNECTION_PORTS.md` for what each port does.

## Limits

- No maintainer CI on FreeBSD, so breakages are best-effort.
- Do not rely on the in-panel updater for FreeBSD binaries.
- Pull requests improving these scripts are welcome.
