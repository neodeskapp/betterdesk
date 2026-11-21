# Important AI Notes

Notes migrated into the repository (2026-06-03) so agents and contributors have versioned, in-repo context. Original scope: external Copilot memory (`/memories/repo/`).

## Index

| File | Topic |
|------|--------|
| [betterdesk-api-port-consolidation.md](./betterdesk-api-port-consolidation.md) | **Go API :21114 (default) + client API port :21121** — issue #160, installers, client config. Ports table and firewall profiles now live in [../CONNECTION_PORTS.md](../CONNECTION_PORTS.md) |
| [console-backend-strangler.md](./console-backend-strangler.md) | Moving the Node.js console backend into Go — staged plan, domain/table ownership, trust bridge, rollback flags |
| [betterdesk-server-deploy-paths.example.md](./betterdesk-server-deploy-paths.example.md) | Production deploy paths template — copy to `docs/private/` (gitignored) for operator notes |
| [betterdesk-update-flow.md](./betterdesk-update-flow.md) | Panel self-update, Go rebuild after GitHub update (#158) |
| [betterdesk-enrollment.md](./betterdesk-enrollment.md) | Device enrollment |
| [betterdesk-i18n-audit.md](./betterdesk-i18n-audit.md) | i18n checks |
| [agent-build-worker-fix.md](./agent-build-worker-fix.md) | Agent build worker |
| [installer-tui-and-protocol-tests.md](./installer-tui-and-protocol-tests.md) | Installer TUI / protocol tests |
| [installer-tui-modernization.md](./installer-tui-modernization.md) | Installer TUI UI |
| [rdclient-protobuf-enum-encoding.md](./rdclient-protobuf-enum-encoding.md) | rdclient protobuf |
| [rdclient-webcodecs-keyframe.md](./rdclient-webcodecs-keyframe.md) | WebCodecs keyframes |

When changing API ports, RustDesk client login, or console `.env`, read **betterdesk-api-port-consolidation.md** first.

Operator-only runbooks (real hostnames, SSH users, deploy commands) live in **`docs/private/`** — that directory is gitignored; start from the `*.example.md` templates in this folder.
