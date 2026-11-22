// Package updater installs new versions of betterdesk-server from the
// project's GitHub releases (DECISIONS A28, spec updater-installers.md
// 4.4): no toolchain, no Node and no source on the host.
//
// # Releases
//
// The stable channel (branch "main") installs the newest release that is
// not a pre-release; the development channel (branch "dev") installs the
// pre-release tagged "dev", which the release job replaces on every dev
// build. UPDATE_GITHUB_OWNER and UPDATE_GITHUB_REPO name the repository,
// UPDATE_GITHUB_BRANCH the channel. A release has one binary per
// platform (betterdesk-server-linux-amd64, -linux-arm64, -linux-armv7,
// -windows-amd64.exe), manifest.json, manifest.sig when it is signed, and
// SHA256SUMS for people who check by hand.
//
// manifest.json:
//
//	{"schema_version": 1, "product": "betterdesk-server", "version": "3.6.0",
//	 "channel": "stable", "created": "<RFC 3339>", "min_rollback_version": "3.5.0",
//	 "owner": "trybetterdesk", "repo": "betterdesk", "branch": "main", "sha": "<commit>",
//	 "files": [{"path": "betterdesk-server-linux-amd64", "sha256": "<hex>", "size": 41234567,
//	            "os": "linux", "arch": "amd64"}, ...]}
//
// manifest.sig is an ed25519 signature in base64 over the canonical text
// of owner, repo, branch, sha and the files' names and SHA-256 sums
// (CanonicalPayload): upstream's signed update manifest
// (web-nodejs/lib/updateManifestSignature.js), byte for byte, so one key
// and one signing step serve the old updater's file manifests and these
// release manifests. The public keys are compiled in (keys.go) and can be
// added to with UPDATE_MANIFEST_PUBLIC_KEY; with a key, every release
// must carry a valid signature. Without one, UPDATE_REQUIRE_SIGNED_MANIFEST
// refuses unsigned releases and otherwise they install unverified, with a
// warning. The SHA-256 of the binary is always checked.
//
// # An install
//
// Check, then the operator's install: the manifest again (no cache),
// the binary into DATA_DIR/updates/<version>/, its size and SHA-256,
// "<binary> -version" naming the manifest's version and commit (which
// ties the unsigned version to the signed binary, and catches a binary
// for another processor), the backup into
// DATA_DIR/backups/pre-update-<time>/ (the running binary, and the full
// data backup of console/backup only when Options.DataBackup is set; the
// answer's backedUp and the manifest's dataBackup say which were made),
// the swap, and the pending marker. The route then answers, and with a
// supervisor (BETTERDESK_SUPERVISOR) the process ends with ExitRestart
// two seconds later.
//
// The swap works in the running binary's own folder (os.Executable). On
// Linux and other Unix systems the new file is copied in as
// .<name>.new.<pid>, the running binary is linked to <name>.prev, and the
// copy is renamed over <name>: the running process keeps its old file. On
// Windows, where a running binary cannot be replaced but can be renamed,
// the running binary is renamed to <name>.prev and the copy takes its
// name. Every failure before the last rename leaves the old binary where
// it was.
//
// # The pending marker and the automatic rollback
//
// <binary>.update-pending.json beside the binary names the update, the
// SHA-256 of .prev and the data folder. StartupCheck, the first thing
// main does, counts the starts of the new binary there; Pending.Ready
// removes the marker once the server listens and its database is
// migrated. On the fourth start without that, the check puts .prev back,
// records the failure in the data folder (.last_update_result.json and
// .server_binary_stale with the reason "rollback", which the panel shows)
// and the process ends with ExitRestart, so that the supervisor starts
// the old binary.
//
// # Files in the data folder
//
// .last_update_result.json, .server_binary_stale, .update_in_progress.json,
// .update_sha and backups/pre-update-* keep the old console's names and
// forms. Backups of the old console (console and source files) are listed,
// deleted and pruned like the new ones, and their restore is refused
// (DECISIONS A29).
package updater
