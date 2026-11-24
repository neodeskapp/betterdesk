## [Unreleased]

### Changed
- _(none yet)_

---

## [3.6.0-rc.4] — 2026-11-24

Fourth release candidate of 3.6.0.

### Added
- API tokens for scripts, made under API tokens in the account menu. A
  token acts as its account with only the permissions picked for it, read
  only or read and write, for at most a year and optionally from some
  addresses only, and can be revoked. It never gets the server's secrets.
- The `neodesk` command line for everything the console's API does
  (`neodesk-server cli` on Linux hosts), made from one table of commands.
  `neodesk describe` prints them as JSON or Markdown, and `neodesk mcp`
  serves them as MCP tools, read only unless writes are allowed.
- A setup guide for the first admin of a new install, on the overview.
  Each of its eight steps leads to its place and back, the server ticks
  the steps it sees done, and Help and Settings bring the guide back.

### Changed
- Leaving the settings while a save runs waits for the save instead of
  asking whether to discard what it saves.
- The core script for MeshCentral agents is served at `/neocore.js`;
  `/meshcore.js` still answers.
- The class names of the accessibility helpers and the attestation badge
  start with `nd-`.

### Removed
- The supporters list in Help, `GET /api/console/supporters` and the
  theme preset of a former sponsor.

### Fixed
- A computer that registers again is asked for its key, so a changed key
  is refused and the computer shows offline instead of online while every
  session to it fails. Running its invite again offers Approve new key.
- Leaving a page while part of it still loads no longer raises a page
  error, and a part that failed to load loads again.

---

## [3.6.0-rc.3] — 2026-11-23

Third release candidate of 3.6.0.

### Added
- The browser viewer shows several displays of a device, as arranged, in a
  grid or each in its own window, sends clicks to the display under the
  pointer, and shows each display's frame rate and bitrate.
- Docker: HTTPS for the web console with Let's Encrypt. `--domain` and
  `--email` (`--letsencrypt-email` in install.sh) put a small Traefik
  container in front of the console that gets the certificate and renews it
  by itself; the console's own port then answers on the host only.
  `--letsencrypt-staging` uses Let's Encrypt's staging server, and
  `--no-traefik` turns it off again. The compose file has it as the profile
  `traefik`.
- Approve new key: a reinstalled or reset computer keeps its ID but comes
  back with a new key. The server still refuses it, keeps the new key as a
  request, and the device page offers Approve new key (Approve enrollment
  permission). After that the computer rejoins with its invite. Until then
  the device list shows it offline.
- Quick support choices: the person decides in the support app whether the
  supporter may also copy text and send files. Needs the client 1.5.0-rc.5.
- The permissions Terminal on NeoDesk agents and Files on NeoDesk agents.

### Changed
- Defaults that changed:
  - Remote operator no longer opens the terminal and the files of NeoDesk
    agents. They need the two new permissions, which Global admin and the
    super admins hold; Edit devices covered them before.
  - Server admin no longer sees the whole console API key and can't
    download, restore or schedule full backups. The Backup tab says so.
  - The IoT gateway and its devices need View CDAP devices. Before, the
    permission only hid the menu entry.
  - Fields the server's environment sets can't be changed in the console:
    the public addresses with `PUBLIC_ENDPOINTS_ENV_OVERRIDE=Y`, the
    enrollment mode with `ENROLLMENT_MODE_ENV_OVERRIDE=Y`, and the release
    keys, the client build command and the secrets in `.env`. The console
    says so instead of Saved.
- Docker: Connection strategy and the time settings save. The server keeps
  them in its database and applies them at once, and the page shows what
  the server runs instead of defaults. Pairing codes redeem, RDP and VNC
  targets reconnect after the container restarts, and Updates shows
  commands that fetch the new version.
- The browser viewer over plain HTTP ends the session with a message that
  it needs HTTPS instead of showing a black screen.
- Directory sign-in: the console waits up to 30 seconds for a slow
  directory and says when it took too long. A directory account signs in
  to the account the directory names instead of a second one under the
  typed name. LDAP groups are searched as the service account, role map
  lines can name a group by its Group name attribute, the connection
  timeout covers connecting, LDAPS and StartTLS, and saved LDAP settings
  apply at once. Settings that can't work are refused with the reason at
  the field, and directory and single sign-on accounts are told to change
  their password there.
- Quick support: a session gets the screen, the mouse and the keyboard,
  and the clipboard and files only when the person allowed them. File
  transfer, terminal, tunnel and camera sessions are refused otherwise.
- Restore brings back only the parts you choose: Server device database
  leaves the console accounts alone and Console data leaves the devices,
  on SQLite and PostgreSQL. It says when a part brought back nothing.
- Small screens and keyboard: the top bar, long buttons, status lines and
  device names stay inside the screen on phones, search has Cancel on
  touch screens, the remote screen says that F8 leaves it, and the font
  list and popups behave for keyboard and screen readers.
- Languages: audit actions and details, hardening checks, built-in group
  notes and refusals of passwords, two-factor codes, Email, Appearance and
  fonts are in the console's language. The German console shows the search
  shortcut as Strg K.
- Appearance: the console background, the web remote client background
  and the text font reach the pages, a saved look shows at the next page
  load, built-in themes show their colours, Google fonts are downloaded to
  the server, and values the server would drop are refused at the field.
- The device ticket routes are `/api/tickets/nd`.

### Removed
- Appearance: Enable glass surfaces with tint, Surface blur, Glass opacity
  and Application description. They changed nothing in the console.

### Security
- CORS: the console answers cross-origin requests only for the desktop
  client's webview and never with cookies; the development address
  http://localhost:1420 is gone. A Bearer header no longer switches the
  CSRF check off for a request that the console's cookies authenticate.
- No account widens its own access: an API key, the console's own
  included, no longer creates, changes or deletes accounts, roles, API
  keys or second factors; no account gives a role or a key more than it
  has; an account can't change its own device access or raise its own
  panel access; deleting a user group keeps its panel limits on its
  members.
- The client feature switches (Terminal, File transfer, Remote control,
  Clipboard) cover the NeoDesk agent's tools.
- Client API tokens keep the saved session duration after a restart.
- Backup archives keep the dumps with password hashes private (0600).
- The Advanced tab refuses a session secret the console can't start with,
  and a broken one no longer stops the server. The blocklist it saves is
  loaded at the server's start.
- The Advanced editor shows the audit log read-only, and keeps release
  keys, the client build command and secrets out of the files it writes.
- The console takes the client-facing host from X-Forwarded-Host only when
  a trusted proxy sent it, and links can no longer add lines or another
  server to the downloaded `.msh` file.
- Access rules for users, user groups and device groups apply to their
  devices, and devices get only their own data loss prevention policies.
- A read-only account that opens a device's live tabs sends it no command.
- The installers leave a switched-off ufw or firewalld alone instead of
  opening ports.

### Fixed
- An invite's Linux install script moves a computer from client 1.5.0-rc.4
  to a later build, which apt and dnf call a downgrade.
- The first-run password file is deleted when an admin sets a password.
- Single sign-on sign-ins record the browser's address, not 127.0.0.1.
- Only failed sign-ins count against an address, and too many are named
  as such instead of a wrong password.
- A refused two-factor code or password keeps you signed in and says what
  is wrong.
- Organizations, accounts, user groups, network monitor targets, client
  branding, device tokens and the General, Backup and Email tabs refuse
  values that can't work, with a message at the field.
- A Public ID server or Public relay server with a port is refused instead
  of saved without the port.
- Enrollment requests approve without name, folder, tags and groups when
  Full approval form is off.
- The default mesh group keeps its MeshID.
- RDP and VNC targets test and save after a port is typed.
- Chat shows devices online only while the device list does.
- A stock RustDesk device approved from the signal server is typed
  RustDesk, and the audit log names no account for what devices and the
  quick support app did.
- The invite page, the viewer's password step and the Users page say what
  the product does.

---

## [3.6.0-rc.2] — 2026-11-23

Second release candidate, and the first as NeoDesk.

### Changed
- The project is called NeoDesk. The repository is
  github.com/neodeskapp/neodesk, the client github.com/neodeskapp/neodesk-client,
  and the documentation is on neode.sk.
- Names for admins:
  - Installers: `neodesk.sh`, `neodesk.ps1` and `neodesk-docker.sh`.
  - Linux: the service `neodesk-server` (`neodesk-server.service`), the
    system user `neodesk`, the folder `/opt/neodesk` and the binary
    `/usr/local/bin/neodesk-server`.
  - Windows: the service `NeoDeskServer` and the folders `C:\NeoDesk` and
    `C:\NeoDeskConsole`.
  - Docker: the image `ghcr.io/neodeskapp/neodesk`, the container
    `neodesk`, the volumes `neodesk-data`, `neodesk-console-data` and
    `neodesk-postgres-data`, and `neodesk-show-admin-credentials`.
  - Environment variables start with `NEODESK_` and `ND_`.
- The device API is `/api/nd/*`, with the sockets `/ws/nd-signal`,
  `/ws/nd-relay` and `/ws/nd-mgmt` and the `X-ND-*` headers
  (`X-ND-Enrollment-Timestamp`, `-Nonce` and `-Signature`,
  `X-ND-Background`). Devices sign `nd-enrollment-v1` and `nd-mgmt-v1`.
- LAN discovery answers `neodesk-discover` probes, mDNS announces
  `_neodesk._tcp`, and update manifests are signed for NeoDesk.
- There is no upgrade from 3.6.0-rc.1 or older: agents, clients and
  installs from before the rename can't register with, connect to or update
  to NeoDesk, and the installers don't take over their services, folders,
  volumes or settings. Install NeoDesk fresh.

### Fixed
- The console's socket origin check still looked for the old session
  cookie name, so a socket upgrade with a session cookie and no Origin
  header was accepted.
- Support clients are built from the client repository's main branch and
  checked against its signing key.

---

[3.6.0-rc.2]: https://github.com/neodeskapp/neodesk/releases/tag/v3.6.0-rc.2
[3.6.0-rc.3]: https://github.com/neodeskapp/neodesk/compare/v3.6.0-rc.2...v3.6.0-rc.3
[3.6.0-rc.4]: https://github.com/neodeskapp/neodesk/compare/v3.6.0-rc.3...v3.6.0-rc.4
