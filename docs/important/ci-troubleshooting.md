# GitHub Actions — troubleshooting

Quick map of CI workflows, common failures, and local commands that mirror branch protection checks.

## Required checks (branch protection)

| Workflow | What it enforces | Local mirror |
|---|---|---|
| **Version Verify** | Tier 1/2 files match `VERSION` | `node scripts/bump-version.js --verify` |
| **Secret Scan** | Gitleaks + operator fingerprint ban | `gitleaks detect --source . --config .gitleaks.toml` and `bash scripts/check-no-sensitive-paths.sh` |
| **Go Server CI** | `go vet`, race tests, `govulncheck`, the release build with the web app embedded (server and web console) | `cd betterdesk-server && go vet ./... && go test -race -count=1 ./...` |
| **Web UI CI** | `npm ci`, high+ audit, lint, type check, Vitest, build (the web app) | `cd web-ui && npx -y npm@11.6.2 ci && npm run lint && npm run check && npm test && npm run build` |

## Why many jobs run after one push

A single push to `dev` can trigger:

1. First wave: CodeQL (5 languages), Secret Scan, path-filtered CI, Version Bump (dev)
2. Second wave: Version Bump commit → Version Verify, Docker Publish (if paths match)

Merges to `main` add tag + release + server binary builds. This is expected; failures look worse than they are when several independent checks fail for different reasons.

## Common failures

### Version Verify — `betterdesk.sh: expected X, got Y`

Version drift in installer scripts. Fix:

```bash
node scripts/bump-version.js --sync
node scripts/bump-version.js --verify
```

### Secret Scan — `leaks found`

Operator fingerprints must not appear in tracked sources:

- Lab LAN host `192.168.0.x` (specific test IP blocked — use `203.0.113.x` in docs/examples)
- Developer home paths under `/home/...` (build machines must not leak into commits)
- SSH `user@internal-ip` patterns from private runbooks

### Web UI CI: test or type-check failures

```bash
cd web-ui
npx -y npm@11.6.2 ci
npm run check
npm test
```

npm 10 (bundled with Node 22) can't resolve the web app's dependency tree, so install with npm 11 as above.

### Web UI CI: `npm audit`

High+ vulnerabilities fail CI. Fix with dependency bumps or documented `overrides` in `web-ui/package.json`.

### CodeQL red on Dependabot PRs

Dependabot PRs skip SARIF upload (GitHub permission limitation). Analysis still runs; push to `dev`/`main` uploads results. This is intentional in [`.github/workflows/codeql.yml`](../../.github/workflows/codeql.yml).

### Version Bump (dev) — `failed to push some refs`

Concurrent pushes to `dev`. Workflow retries with `git pull --rebase`. If it persists, wait for the other push to finish and re-run the failed job.

### Release server — Go version mismatch

[`release-server.yml`](../../.github/workflows/release-server.yml) uses `go-version-file: betterdesk-server/go.mod` (same as Go Server CI).

## Pre-merge checklist (`dev` → `main`)

See [PRE_RELEASE_CHECKLIST.md](../PRE_RELEASE_CHECKLIST.md). Minimum before opening the release PR:

```bash
node scripts/bump-version.js --verify
(cd betterdesk-server && go vet ./... && go test -race -count=1 ./...)
(cd web-ui && npx -y npm@11.6.2 ci && npm run check && npm test)
gitleaks detect --source . --config .gitleaks.toml
bash scripts/check-no-sensitive-paths.sh
```
