# Security policy

How to report a vulnerability in NeoDesk and what to expect after you
do.

## Supported versions

Security fixes go into the `main` branch and the next release. Older
releases may get a backport for a critical issue at the maintainers'
discretion.

| Version | Supported |
| ------- | --------- |
| `main` | Yes |
| Newest release | Yes |
| Older releases | Best effort |

## Reporting a vulnerability

Please don't open a public GitHub issue for a security problem. Report it
privately through GitHub:
<https://github.com/neodeskapp/neodesk/security/advisories/new>, or by
email to security@neode.sk.

Please include, where you can:

- What the problem is and what an attacker gains.
- How to reproduce it: a proof of concept, a request and its answer, or a
  test case.
- The affected version or commit, and how NeoDesk runs (Docker, the
  Linux or Windows installer, a build from source).
- Logs, screenshots or traffic captures, with sensitive data removed.

## Response targets

- **Acknowledgement:** within 7 days of the report.
- **Triage decision:** within 14 days (severity, scope, fix plan).
- **Coordinated disclosure:** at most 90 days from the acknowledgement,
  shorter if the issue is being exploited.
- **Credit:** if you want it, in the release notes or the advisory.

If you get no acknowledgement within 7 days, open a public issue that
says only that you are waiting for an answer to a security report, with
no details.

## Scope

In scope, everything in this repository:

- The Go server in `neodesk-server/`: the ID server (signal) and the
  relay, the HTTP and WebSocket API, the client API for RustDesk clients,
  the CDAP gateway, the MeshCentral compatibility layer, the database
  code, the release signature checks and the command-line tools.
- The web console the server serves: its server code in
  `neodesk-server/console/` and the web app in `web-ui/`, including the
  browser remote desktop viewer.
- The installers `install.sh`, `neodesk.sh`, `neodesk.ps1` and
  `neodesk-docker.sh`, the Docker images (`Dockerfile`,
  `Dockerfile.server`, `docker/`) and the compose files.
- The CDAP SDKs in `sdks/` and the reference bridges in `bridges/`.
- The RDP and VNC gateway deployment files in `remote-gateway/`.
- The release and publishing workflows in `.github/workflows/`.

Out of scope:

- The NeoDesk desktop client. It is built in
  [neodeskapp/neodesk-client](https://github.com/neodeskapp/neodesk-client);
  report its issues there.
- Issues that need an attacker who already controls the host's operating
  system, the database file, or an administrator's browser session.
- Denial of service from plain network flooding without an amplification
  vector.
- Self-XSS, missing security headers on the documentation website, or
  theoretical issues without a concrete way to exploit them.
- Reports from automated scanners that nobody checked by hand.

## Running a server safely

The settings and steps that make a server harder to attack are on the
website: <https://neode.sk/docs/operations/security>.
