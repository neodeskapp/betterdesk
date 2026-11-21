# Web Console

The BetterDesk Web Console is part of `betterdesk-server` and is accessible at **http://your-server:5000**. Its pages are the Svelte app in `web-ui/`, built into the server binary.

The sidebar groups the pages into Devices / Support / People / Activity / Settings, with a list-first Devices page and per-topic Settings.

---

## Visual preview

The following screenshots are from an earlier version of the console, in the dark theme and English locale. All device names, IDs, notes, and network values are fictional test data.

<table>
  <tr>
    <td><img src="https://raw.githubusercontent.com/UNITRONIX/BetterDesk/dev/screenshots/generated/login-dark.png" alt="BetterDesk web console login" width="100%"></td>
    <td><img src="https://raw.githubusercontent.com/UNITRONIX/BetterDesk/dev/screenshots/generated/dashboard-dark.png" alt="BetterDesk web console dashboard" width="100%"></td>
  </tr>
  <tr>
    <td align="center">Login</td>
    <td align="center">Dashboard</td>
  </tr>
  <tr>
    <td><img src="https://raw.githubusercontent.com/UNITRONIX/BetterDesk/dev/screenshots/generated/devices-list-dark.png" alt="BetterDesk devices list" width="100%"></td>
    <td><img src="https://raw.githubusercontent.com/UNITRONIX/BetterDesk/dev/screenshots/generated/device-details-dark.png" alt="BetterDesk device details" width="100%"></td>
  </tr>
  <tr>
    <td align="center">Devices</td>
    <td align="center">Device details</td>
  </tr>
  <tr>
    <td colspan="2"><img src="https://raw.githubusercontent.com/UNITRONIX/BetterDesk/dev/screenshots/generated/settings-updates-dark.png" alt="BetterDesk settings updates" width="100%"></td>
  </tr>
  <tr>
    <td colspan="2" align="center">Settings → Updates</td>
  </tr>
</table>

The source files are kept in [`screenshots/generated/`](../../screenshots/generated/) in the repository.

---

## Dashboard

Overview cards: device counts, online status, server uptime, versions.

---

## Devices Page

### Device List

The devices page shows all registered RustDesk clients in a responsive table:

| Column | Description |
|--------|-------------|
| **ID** | RustDesk client ID (numeric) |
| **Hostname** | Device hostname (from sysinfo) |
| **Type** | Device type (desktop, server, agent, etc.) |
| **Platform** | OS (Windows, Linux, macOS, Android, iOS) |
| **Last Online** | Last heartbeat timestamp |
| **Status** | Online/Degraded/Critical/Offline with colored dot |
| **Actions** | Kebab menu (⋮) with device operations |

### Filters

- **Search** — Filter by ID, hostname, platform, or tags
- **Status filter** — Segmented pills: All / Online / Offline
- **Folder filter** — Horizontal scrollable folder chips

### Device Actions (Kebab Menu)

| Action | Description |
|--------|-------------|
| **Edit** | Change device notes, tags, user assignment |
| **Connect** | Launch RustDesk connection (URI handler) |
| **Rename** | Change the device ID |
| **Delete** | Soft-delete the device |
| **Revoke** | Delete + block ID + disconnect active sessions |
| **Ban** | Block the device from reconnecting |
| **Wake on LAN** | Send WOL magic packet (offline devices) |

### Folders

Organize devices into folders:
- Create/rename/delete folders
- Drag-and-drop devices between folders
- Folder counts update automatically

### Real-time Status Updates

Device status updates in real-time via WebSocket push from the Go server event bus. No page reload needed — green/red dots update in-place.

---

## Device Detail

Click a device row to open the detail panel:

### Info Tab
- Device ID, hostname, platform, version
- IP address, last online timestamp
- Tags, notes, user assignment
- Connect button (RustDesk URI handler)

### Hardware Tab
- CPU model, cores, architecture
- Total RAM, total disk
- OS version, kernel

### Metrics Tab
- Live CPU, memory, disk gauges (animated bars)
- Historical charts (last 100 data points)
- Data from `POST /api/heartbeat` with metrics

---

## Users Page

Manage console users and roles. See [[User Management|User-Management]] and [[Organizations and RBAC|Organizations-and-RBAC]].

---

## Additional Panel Pages

| Page | Description |
|------|-------------|
| **Organizations** | Multi-tenant orgs, members, scoped devices |
| **Fleet** | Device groups, batch operations, scaling |
| **Policies** | Access policies, unattended schedules |
| **BetterDesk Agents** | Incoming-only Support agents (import from BetterDesk-Client) — [[BetterDesk Agents\|Client-Generator]] |
| **Help Requests** | End-user support inbox |
| **Tickets** | Internal ticket tracking |
| **CDAP Devices / Studio** | IoT device management and widget editor |
| **Activity / Reports** | Audit and reporting views |
| **Security Audit** | Login and API security events |
| **Server Management** | Go server health, config, API keys |
| **Settings → Updates** | In-panel updates — [[Panel Updates\|Panel-Updates]] |
| **Settings → Sign-in** | RustDesk client session TTL, OIDC — [[OIDC SSO\|OIDC-SSO]] |

---

## Settings Page

### Client setup (Settings → Client setup)
- View server address and public key
- Generate QR codes for client setup
- Copy configuration strings

### Console Settings
- Language selection (**26 locales** from `betterdesk-server/console/static/files/lang/`)
- Console version info
- Session timeout configuration

### Password Change
- Current password verification
- New password with confirmation
- TOTP 2FA enrollment/removal

---

## Login screen

The console uses the standard panel login (username / password / optional TOTP).

---

## UI Features

### Theme Support
- **Dark** (default) — Dark backgrounds, light text
- **Light** — White backgrounds, dark text
- **Auto** — Follows system `prefers-color-scheme`

### Toast Notifications
- Success/error/warning/info pop-ups
- Auto-dismiss with progress bar
- Hover to pause
- Max 5 simultaneous toasts

### Skeleton Loading
- Animated shimmer placeholders during data load
- Applied to tables, cards, avatars

### Page Transitions
- Fade + translateY animation on page content
- Staggered list item animations (30ms per row)

### Responsive Design
- 4 breakpoints: 1024px, 768px, 600px, 400px
- Card-style layout on mobile (< 600px)
- Bottom sheet kebab menu on phones
- Collapsible sidebar navigation

---

## Internationalization (i18n)

The console supports **26 languages** (including EN, PL, DE, FR, ES, JA, ZH, ZH-TW, and more). The language files are `betterdesk-server/console/static/files/lang/*.json`, built into the server binary.

| Language | Code | Status |
|----------|------|--------|
| English | `en` | Complete (source) |
| All others | `ar`, `cs`, `da`, `de`, `es`, … | Maintained — see repo for coverage |

### Adding a Language

1. Copy `betterdesk-server/console/static/files/lang/en.json` to `{code}.json` in the same folder
2. Translate all values (keep keys as-is)
3. Update the `meta` section with language info
4. Add the code to `knownLanguages` in `betterdesk-server/console/i18n/bundle.go`; the console loads only the languages listed there
5. Run `npm run i18n:check` from the repository root, then rebuild `betterdesk-server`

See the [Contributing Translations](https://github.com/UNITRONIX/BetterDesk/blob/main/docs/development/CONTRIBUTING_TRANSLATIONS.md) guide for details.

---

## See also

- [[Web Remote Desktop|Web-Remote]] — browser remote sessions
- [[User Management|User-Management]] — roles and 2FA
- [[Panel Updates|Panel-Updates]] — upgrading the panel
