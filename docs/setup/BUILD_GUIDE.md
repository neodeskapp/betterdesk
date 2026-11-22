# BetterDesk - Building from Source

BetterDesk is one Go program, `betterdesk-server`, that runs the ID and
relay servers, the client API and the web console. The console's pages are
the web app in `web-ui` (SvelteKit), built once and embedded in the binary.

## What you need

- Go, the version `betterdesk-server/go.mod` names (its `toolchain` line)
- Node.js 22.17 or newer, for the web app
- Git

## Build the server with the web console

```bash
# 1. Build the web app
cd web-ui
npm ci
npm run build

# 2. Copy the build to where the binary embeds it from
cd ../betterdesk-server
go run ./cmd/embed-webui

# 3. Build the server
CGO_ENABLED=0 go build -trimpath -o betterdesk-server .
```

On Windows, run the same commands in PowerShell, set `$env:CGO_ENABLED = "0"`
and name the output `betterdesk-server.exe`.

Without step 2 the binary has no web console built in; it then serves the
build named by `WEB_UI_DIR`.

## Build for another platform

Go builds for other platforms without extra tools. Run step 2 first, then
set the target:

```bash
GOOS=linux   GOARCH=arm64          CGO_ENABLED=0 go build -trimpath -o betterdesk-server-linux-arm64 .
GOOS=linux   GOARCH=arm   GOARM=7  CGO_ENABLED=0 go build -trimpath -o betterdesk-server-linux-armv7 .
GOOS=windows GOARCH=amd64          CGO_ENABLED=0 go build -trimpath -o betterdesk-server-windows-amd64.exe .
```

## Check a build

```bash
./betterdesk-server -version   # the version line the updater reads
./betterdesk-server -h         # the flags
```

To install a build, use the installers (`betterdesk.sh`, `betterdesk.ps1`,
`betterdesk-docker.sh`); see [installer-contract.md](../important/installer-contract.md).

---

## GitHub Actions CI/CD

GitHub Actions builds the server on every push to `main` or `dev` and on
version tags (`v*`). Other workflows run on changes to their own paths.

### Releases and artifacts
`release-go.yml` ("Release Go server") is the only workflow that publishes
server binaries to GitHub Releases:

- A tag `vX.Y.Z` publishes the stable release `vX.Y.Z`. The tag must match
  `betterdesk-server/internal/productversion/VERSION`. When a merge to `main`
  makes the tag, `version-bump-main.yml` starts this workflow for the tag with
  `channel=stable`, because a tag pushed by the workflow's own token starts no
  other workflow.
- A push to `dev`, or a manual run with `channel=development`, replaces the
  pre-release tagged `dev`.

Each release holds the server binary for linux amd64, arm64 and armv7 and
windows amd64, each with the web console built in, plus `manifest.json`,
`SHA256SUMS` (bare file names, for `sha256sum -c`) and `manifest.sig`, the
manifest's signature with the release key (secret `RELEASE_SIGNING_KEY`;
without it the workflow publishes nothing). The updater in the console installs from these
releases: the stable channel takes the newest release that is not a
pre-release, the development channel the `dev` pre-release. It checks the
manifest, its signature and the binary's SHA-256 before it installs. For a
private repository, or to get past GitHub's limit of 60 API requests an hour,
set `UPDATE_GITHUB_TOKEN` to a token with read access to the repository's
contents.

`release-server.yml` ("Build & Release Go Server") builds the server for
linux amd64 and arm64 and windows amd64, and the migration tool, on pushes to
`main` and `dev` and on tags. It keeps them as artifacts of its run only,
without the web console built in, and attaches nothing to a release.
