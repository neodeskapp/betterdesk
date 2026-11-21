# Embedded Support builder — operational notes

The incoming-only Support generator is the Go package
`betterdesk-server/console/generator` (the template module, the client builder
and the build queue with its worker). It runs inside `betterdesk-server`.

- The local provider is host-only. It builds Linux targets on Linux, Windows
  targets on Windows and macOS targets on macOS.
- The Linux package targets additionally require `dpkg-deb`, `rpmbuild` and
  `zstd` as applicable.
- Run `scripts/install-build-toolchain.sh` as root. It writes
  `/etc/betterdesk/build.env`. The server reads that file itself
  (`BETTERDESK_BUILD_ENV_FILE` names another one) and takes from it the names
  its own environment lacks, such as `FLUTTER_ROOT`, `VCPKG_ROOT`,
  `CARGO_HOME` and `PUB_CACHE`. `PATH` always comes from the service, so put
  the tool folders on `PATH` in `betterdesk-server.service` or a systemd
  drop-in.
- Inspect the Generator status before creating a profile. Unavailable targets
  must be disabled in the UI and must not be queued by the API.
- A full Windows/Linux/macOS and ARM matrix requires a trusted provider that
  returns a verified embedded manifest. The local Linux provider must never be
  presented as that matrix.
- The artifact directory is `${DATA_DIR}/agent-builds`; builder requests and
  outputs live under `${DATA_DIR}/modules/betterdesk-client-builder`.
- `AGENT_BUILD_WORKER=off` stops the build worker.

## Retry a failed build
```bash
sudo -u postgres psql betterdesk -c \
  "UPDATE agent_bundle_builds SET status='queued', error_message='' \
   WHERE branding_hash='<hash>' AND platform='linux' AND format='deb';"
```

## Reset admin password
`betterdesk-server reset-password` works on SQLite and PostgreSQL. It reads
the new password from the first line of standard input and refuses a terminal
there:
```bash
read -rs PW && printf '%s\n' "$PW" | \
  sudo BETTERDESK_ENV_FILE=/opt/BetterDeskConsole/.env \
  /opt/rustdesk/betterdesk-server reset-password admin
```
