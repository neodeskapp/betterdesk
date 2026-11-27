## [Unreleased]

### Changed
- _(none yet)_

---

## [3.6.53] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.52] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.51] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.50] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.49] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.48] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.47] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.46] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.45] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.44] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.43] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.42] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.41] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.40] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.39] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.38] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.37] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.36] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.35] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.34] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.33] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.32] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.31] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.30] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.29] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.28] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.27] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.26] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.25] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.24] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.23] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.22] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.21] — 2026-11-27

### Changed
- _(none yet)_

---

## [3.6.20] — 2026-11-26

### Changed
- _(none yet)_

---

## [3.6.19] — 2026-11-26

### Changed
- _(none yet)_

---

## [3.6.18] — 2026-11-26

### Changed
- _(none yet)_

---

## [3.6.17] — 2026-11-26

### Changed
- _(none yet)_

---

## [3.6.16] — 2026-11-26

### Changed
- _(none yet)_

---

## [3.6.15] — 2026-11-26

### Changed
- _(none yet)_

---

## [3.6.14] — 2026-11-26

### Changed
- _(none yet)_

---

## [3.6.13] — 2026-11-26

### Changed
- _(none yet)_

---

## [3.6.12] — 2026-11-26

### Changed
- _(none yet)_

---

## [3.6.11] — 2026-11-26

### Changed
- _(none yet)_

---

## [3.6.10] — 2026-11-26

### Changed
- _(none yet)_

---

## [3.6.9] — 2026-11-26

### Changed
- _(none yet)_

---

## [3.6.8] — 2026-11-26

### Changed
- _(none yet)_

---

## [3.6.7] — 2026-11-26

### Changed
- _(none yet)_

---

## [3.6.6] — 2026-11-26

### Changed
- _(none yet)_

---

## [3.6.5] — 2026-11-26

### Changed
- _(none yet)_

---

## [3.6.4] — 2026-11-26

### Changed
- _(none yet)_

---

## [3.6.3] — 2026-11-26

### Changed
- _(none yet)_

---

## [3.6.2] — 2026-11-25

### Changed
- _(none yet)_

---

## [3.6.1] — 2026-11-25

### Changed
- _(none yet)_

---

## [3.6.0-rc.6] — 2026-11-25

Sixth release candidate of 3.6.0.

### Added
- An account can be signed in on up to 10 browsers at once. Signed-in
  devices, in the account menu and on Your account, lists them with Sign
  out and Sign out everywhere else; admins with Edit users see a
  person's list on Users. The setting One session per account keeps the
  old rule, and `SESSION_MAX_PER_USER` decides over it when set.
- Your account: profile, password, two-factor sign-in, signed-in
  devices, API tokens and preferences on one page.
- The chat is rebuilt as Support, a shared inbox with one conversation
  per computer, assignment and closing, and Team, with direct
  conversations and groups. A chat panel opens over any page; unread
  counts, notifications, replies, edits, reactions, files, canned
  replies and message search. A mail goes out when a person waits 15
  minutes, in the server's branding and each recipient's language.
  Chat with computers can be turned off. Computers reach the chat on the
  server's API port.
- Invites go out as a welcome mail in the server's branding, and the
  invite sheet walks through a new computer's name, folder, access and a
  test session as it joins. `neodesk invites show` lists the computers
  that joined with an invite.
- The device page keeps a usage history and draws CPU, memory, disk and
  network graphs; CPU and network rates come with a NeoDesk client
  after 1.5.0-rc.7.
- Remote sessions open inside the console as tabs that stay connected
  across pages. Copy and paste of text and images in the browser
  viewer, Cmd as Ctrl on a Mac, phones held sideways, and Firefox's
  limits named.
- The top bar search finds every setting and actions such as Quick
  support, Add a computer and New API token.
- The command line's `sessions` group, the `chat` commands and
  `settings console-sessions`: 564 commands in 60 groups.

### Changed
- The console's sidebar has the sections Devices, Support, People,
  Activity, Rules, Server and Settings. Backups, updates and
  configuration files are under Server; Directory (LDAP), Single
  sign-on, Joining, MeshCentral and Branding are tabs of their own;
  invites, device tokens, pairing codes, support agents and manual setup
  are tabs of Devices, Add computers; Security checks left the audit
  log. The old addresses lead to the new pages.
- Theme, language and accessibility moved into the account menu, Help
  is an icon, and Refresh is gone: six pages load again every 30
  seconds while they are on screen.
- Enrollment requests are called Join requests.
- The permission Chat access is split into Support chat and Team chat;
  Assign conversations and Manage canned replies are new.
- On a phone every dialog is a full-height sheet.
- The client API counts heartbeats per device (40 a minute), with 6000 a
  minute per address as a flood brake, answers 429 when over and counts
  the drops in /metrics.
- The console's German and the server's German mails say "du".
- The earlier chat's messages move once into the new conversations;
  what fits nobody stays under Old messages for admins.

### Fixed
- A join under an enrolled computer's ID needs the proof of that
  computer's key, so an invite can't take over a computer; refused
  joins show in the audit log.
- Direct chat messages reach only their two people, and read marks are
  per account.
- The one-line Docker install opens the console's ports only when they
  are served and no reverse proxy is set.
- A browser's live updates, chat and remote sessions close as soon as
  its session ends, turning two-factor sign-in on or off ends the
  account's other sessions, and a password reset on the server's
  command line signs the account out of the console.
- A signed-out browser is told why, whichever request notices first.
- The device page shows real values and asks an online NeoDesk client
  for new live data instead of calling it offline.
- A computer's message without a conversation shows in its
  conversation.

---

## [3.6.0-rc.5] — 2026-11-25

Fifth release candidate of 3.6.0.

### Added
- Saved sign-in: a device keeps the account name and password of a
  Windows account, encrypted on the server, and the browser viewer signs
  in with them at the computer's lock or sign-in screen, once per
  session, by itself when the device's switch is on. Permissions Use
  saved sign-in and Manage saved sign-in, super admins only by default;
  API tokens can manage the details but never use or read them. With a
  client that opens sealed sign-ins the password never reaches the
  browser.
- Connect in the browser viewer signs in with the device password an
  organisation's address book stores, without the browser getting it.
- Revoking, expiring or deleting an API token ends the guest links,
  device tokens, quick support sessions, invites, pairing codes, guest
  desktop links and organization invitations it made, and the revoke
  dialog lists the computers it let in, which stay.
- Every release carries its installers and compose files, listed in
  SHA256SUMS and signed in SHA256SUMS.sig; the install commands fetch
  them from neode.sk.
- `neodesk` hands the desktop client's options and a bare join or
  support link to the client on Linux, and says when it is older than
  the server.

### Changed
- `neodesk` is the command line on every install: on Linux a copy of
  the server binary in /usr/local/bin that every user can run, on
  Windows `C:\NeoDesk\bin\neodesk.exe` on the machine's Path. The Linux
  installer's manager is `neodesk-setup`; its old options on `neodesk`
  still reach it.
- The installers and compose files are in `install/`, the Dockerfiles
  in `docker/`, and the scripts update themselves from the release they
  install instead of from the main branch.
- The console's default port is 3375 instead of 5000; installs whose
  settings name a port keep it. HTTPS stays on 5443.
- Linux backups go to /opt/neodesk-backups and the Docker data volume is
  mounted at /opt/neodesk/data; an update moves both.
- The address book's device passwords are sealed with the key file
  `.device_vault_key` next to the server's key pair, which no backup
  holds. The console, the command line and MCP no longer return them.
- Every command of the command line names the permission its route
  needs, every option has a help text, and times without a zone say UTC.
- `neodesk mcp` asks the person before a tool that deletes, ends, runs
  or changes access, when the MCP client can ask.
- Links that open a device in an app use `neodesk://`; Open in RustDesk
  stays as a second choice.
- Help points to the NeoDesk Discord.

### Removed
- The upgrade path from the old Node.js console and the migration tools.

### Fixed
- A computer whose key the server refuses shows offline, also after a
  restart, and its system information isn't stored.
- A permanently deleted device leaves no key, passwords, grants or
  settings behind.
- Accounts that see only some devices can't widen their access through
  the API port, and access rules reach device groups made from tags.
- A read-only API token sees agent templates' enrollment tokens only by
  their start.
- The browser viewer no longer logs a 409 for the quick support password
  at every session start.
- The Intune script falls back to the client API port 21121.
- The Docker update backs up the settings from before the update, and a
  failed Linux update starts the previous binary also when systemd gave
  up on the new one.
- The invite page says what Windows' permission prompt shows for the
  exe.

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
[3.6.0-rc.5]: https://github.com/neodeskapp/neodesk/compare/v3.6.0-rc.4...v3.6.0-rc.5
[3.6.0-rc.6]: https://github.com/neodeskapp/neodesk/compare/v3.6.0-rc.5...v3.6.0-rc.6
[3.6.1]: https://github.com/neodeskapp/neodesk/compare/v3.6.0-rc.6...v3.6.1
[3.6.2]: https://github.com/neodeskapp/neodesk/compare/v3.6.1...v3.6.2
[3.6.3]: https://github.com/neodeskapp/neodesk/compare/v3.6.2...v3.6.3
[3.6.4]: https://github.com/neodeskapp/neodesk/compare/v3.6.3...v3.6.4
[3.6.5]: https://github.com/neodeskapp/neodesk/compare/v3.6.4...v3.6.5
[3.6.6]: https://github.com/neodeskapp/neodesk/compare/v3.6.5...v3.6.6
[3.6.7]: https://github.com/neodeskapp/neodesk/compare/v3.6.6...v3.6.7
[3.6.8]: https://github.com/neodeskapp/neodesk/compare/v3.6.7...v3.6.8
[3.6.9]: https://github.com/neodeskapp/neodesk/compare/v3.6.8...v3.6.9
[3.6.10]: https://github.com/neodeskapp/neodesk/compare/v3.6.9...v3.6.10
[3.6.11]: https://github.com/neodeskapp/neodesk/compare/v3.6.10...v3.6.11
[3.6.12]: https://github.com/neodeskapp/neodesk/compare/v3.6.11...v3.6.12
[3.6.13]: https://github.com/neodeskapp/neodesk/compare/v3.6.12...v3.6.13
[3.6.14]: https://github.com/neodeskapp/neodesk/compare/v3.6.13...v3.6.14
[3.6.15]: https://github.com/neodeskapp/neodesk/compare/v3.6.14...v3.6.15
[3.6.16]: https://github.com/neodeskapp/neodesk/compare/v3.6.15...v3.6.16
[3.6.17]: https://github.com/neodeskapp/neodesk/compare/v3.6.16...v3.6.17
[3.6.18]: https://github.com/neodeskapp/neodesk/compare/v3.6.17...v3.6.18
[3.6.19]: https://github.com/neodeskapp/neodesk/compare/v3.6.18...v3.6.19
[3.6.20]: https://github.com/neodeskapp/neodesk/compare/v3.6.19...v3.6.20
[3.6.21]: https://github.com/neodeskapp/neodesk/compare/v3.6.20...v3.6.21
[3.6.22]: https://github.com/neodeskapp/neodesk/compare/v3.6.21...v3.6.22
[3.6.23]: https://github.com/neodeskapp/neodesk/compare/v3.6.22...v3.6.23
[3.6.24]: https://github.com/neodeskapp/neodesk/compare/v3.6.23...v3.6.24
[3.6.25]: https://github.com/neodeskapp/neodesk/compare/v3.6.24...v3.6.25
[3.6.26]: https://github.com/neodeskapp/neodesk/compare/v3.6.25...v3.6.26
[3.6.27]: https://github.com/neodeskapp/neodesk/compare/v3.6.26...v3.6.27
[3.6.28]: https://github.com/neodeskapp/neodesk/compare/v3.6.27...v3.6.28
[3.6.29]: https://github.com/neodeskapp/neodesk/compare/v3.6.28...v3.6.29
[3.6.30]: https://github.com/neodeskapp/neodesk/compare/v3.6.29...v3.6.30
[3.6.31]: https://github.com/neodeskapp/neodesk/compare/v3.6.30...v3.6.31
[3.6.32]: https://github.com/neodeskapp/neodesk/compare/v3.6.31...v3.6.32
[3.6.33]: https://github.com/neodeskapp/neodesk/compare/v3.6.32...v3.6.33
[3.6.34]: https://github.com/neodeskapp/neodesk/compare/v3.6.33...v3.6.34
[3.6.35]: https://github.com/neodeskapp/neodesk/compare/v3.6.34...v3.6.35
[3.6.36]: https://github.com/neodeskapp/neodesk/compare/v3.6.35...v3.6.36
[3.6.37]: https://github.com/neodeskapp/neodesk/compare/v3.6.36...v3.6.37
[3.6.38]: https://github.com/neodeskapp/neodesk/compare/v3.6.37...v3.6.38
[3.6.39]: https://github.com/neodeskapp/neodesk/compare/v3.6.38...v3.6.39
[3.6.40]: https://github.com/neodeskapp/neodesk/compare/v3.6.39...v3.6.40
[3.6.41]: https://github.com/neodeskapp/neodesk/compare/v3.6.40...v3.6.41
[3.6.42]: https://github.com/neodeskapp/neodesk/compare/v3.6.41...v3.6.42
[3.6.43]: https://github.com/neodeskapp/neodesk/compare/v3.6.42...v3.6.43
[3.6.44]: https://github.com/neodeskapp/neodesk/compare/v3.6.43...v3.6.44
[3.6.45]: https://github.com/neodeskapp/neodesk/compare/v3.6.44...v3.6.45
[3.6.46]: https://github.com/neodeskapp/neodesk/compare/v3.6.45...v3.6.46
[3.6.47]: https://github.com/neodeskapp/neodesk/compare/v3.6.46...v3.6.47
[3.6.48]: https://github.com/neodeskapp/neodesk/compare/v3.6.47...v3.6.48
[3.6.49]: https://github.com/neodeskapp/neodesk/compare/v3.6.48...v3.6.49
[3.6.50]: https://github.com/neodeskapp/neodesk/compare/v3.6.49...v3.6.50
[3.6.51]: https://github.com/neodeskapp/neodesk/compare/v3.6.50...v3.6.51
[3.6.52]: https://github.com/neodeskapp/neodesk/compare/v3.6.51...v3.6.52
[3.6.53]: https://github.com/neodeskapp/neodesk/compare/v3.6.52...v3.6.53
