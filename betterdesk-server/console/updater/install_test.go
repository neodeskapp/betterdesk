package updater

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func installed(t *testing.T, in *install, sha string) *InstallResult {
	t.Helper()
	out, err := in.svc.Install(context.Background(), InstallOptions{SHA: sha, Backup: true})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	result, ok := out.(*InstallResult)
	if !ok {
		t.Fatalf("install answered %T %+v", out, out)
	}
	return result
}

// A release installs: the new binary is in place, the old one is .prev,
// the backup holds the old binary, the pending marker names both, and the
// download is gone.
func TestInstallSwapsTheBinary(t *testing.T) {
	for _, aside := range []bool{false, true} {
		t.Run(map[bool]string{false: "rename over", true: "rename aside"}[aside], func(t *testing.T) {
			in := newInstall(t, func(o *Options) { o.RenameAside = &aside })
			rel := newTestRelease("3.6.0", newCommit, BranchStable)
			in.gh.serve(rel)
			in.env["BETTERDESK_SUPERVISOR"] = "systemd"

			result := installed(t, in, newCommit[:7])
			if in.binaryContent() != string(rel.binaries[asset]) {
				t.Fatalf("the binary is not the release's")
			}
			prev, err := os.ReadFile(in.binary + ".prev")
			if err != nil || string(prev) != string(binaryFor("3.5.0", oldCommit)) {
				t.Fatalf("the previous binary is not kept: %v", err)
			}
			if info, _ := os.Stat(in.binary); runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
				t.Errorf("mode %o", info.Mode().Perm())
			}
			var marker pendingMarker
			in.readJSON(in.binary+".update-pending.json", &marker)
			if marker.Kind != "update" || marker.From != "3.5.0" || marker.To != "3.6.0" || marker.ToCommit != newCommit ||
				marker.Starts != 0 || marker.DataDir != in.dataDir || marker.PrevSHA256 != sum(prev) {
				t.Errorf("marker %+v", marker)
			}
			if got := mustJSON(t, result.Applied); got != `["betterdesk-server 3.6.0"]` {
				t.Errorf("applied %s", got)
			}
			if result.ConsoleRestartBlocked != nil || !result.NeedsConsoleRestart || result.ServerDeploy == nil ||
				!result.ServerDeploy.Success || result.ServerDeploy.Method != "release" || result.ServerDeploy.TargetPath != in.binary {
				t.Errorf("result %+v", result)
			}
			if result.ServerBuild == nil || !result.ServerBuild.Success || result.Signature.Code != TrustUnsignedAllowed {
				t.Errorf("build %+v signature %+v", result.ServerBuild, result.Signature)
			}
			if result.BackupPath == nil {
				t.Fatal("no backup")
			}
			var bm BackupManifest
			in.readJSON(filepath.Join(*result.BackupPath, "manifest.json"), &bm)
			if bm.Version != "3.5.0" || bm.SHA != oldCommit || bm.Kind != backupKind || bm.FilesBackedUp != 1 ||
				mustJSON(t, bm.Files) != `["server/betterdesk-server"]` || bm.BinarySHA256 != sum(prev) {
				t.Errorf("backup manifest %+v", bm)
			}
			if copied, _ := os.ReadFile(filepath.Join(*result.BackupPath, "server", "betterdesk-server")); !bytes.Equal(copied, prev) {
				t.Error("the backup does not hold the old binary")
			}
			if entries, _ := os.ReadDir(filepath.Join(in.dataDir, updatesDir)); len(entries) != 0 {
				t.Errorf("downloads left: %d", len(entries))
			}
			entries, _ := os.ReadDir(in.dir)
			if len(entries) != 3 {
				var names []string
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Errorf("binary folder holds %v", names)
			}
		})
	}
}

// Without a supervisor the install says so, and nothing ends the process.
func TestInstallWithoutSupervisor(t *testing.T) {
	in := newInstall(t)
	in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	result := installed(t, in, newCommit)
	if result.ConsoleRestartBlocked == nil || *result.ConsoleRestartBlocked != RestartBlocked {
		t.Errorf("consoleRestartBlocked %v", result.ConsoleRestartBlocked)
	}
	if err := in.svc.RecordInstall(newCommit, result); err != nil {
		t.Fatal(err)
	}
	var last LastResult
	in.readJSON(filepath.Join(in.dataDir, lastResultFile), &last)
	if last.ConsoleRestartBlocked == nil || last.SHA != newCommit || last.Applied != 1 {
		t.Errorf("last result %+v", last)
	}
}

// A data backup goes into the backup folder beside the binary.
func TestInstallMakesTheDataBackup(t *testing.T) {
	in := newInstall(t, func(o *Options) {
		o.DataBackup = fakeBackup{data: []byte("archive"), name: "betterdesk-backup-2026-11-18-12-00-00.tar.gz"}
	})
	in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	result := installed(t, in, newCommit)
	data, err := os.ReadFile(filepath.Join(*result.BackupPath, "betterdesk-backup-2026-11-18-12-00-00.tar.gz"))
	if err != nil || string(data) != "archive" || result.BackedUp != 2 {
		t.Fatalf("data backup: %v %q, backedUp %d", err, data, result.BackedUp)
	}
	var bm BackupManifest
	in.readJSON(filepath.Join(*result.BackupPath, "manifest.json"), &bm)
	if bm.DataBackup != "betterdesk-backup-2026-11-18-12-00-00.tar.gz" {
		t.Errorf("manifest %+v", bm)
	}
}

// RV17: without a data backup the answer and the manifest name the binary
// alone, and the folder holds nothing else.
func TestInstallWithoutDataBackupClaimsNone(t *testing.T) {
	in := newInstall(t)
	in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	result := installed(t, in, newCommit)
	if result.BackupPath == nil || result.BackedUp != 1 {
		t.Fatalf("backup %v, backedUp %d", result.BackupPath, result.BackedUp)
	}
	var bm BackupManifest
	in.readJSON(filepath.Join(*result.BackupPath, "manifest.json"), &bm)
	if bm.DataBackup != "" || bm.FilesBackedUp != 1 {
		t.Errorf("manifest %+v", bm)
	}
	entries, _ := os.ReadDir(*result.BackupPath)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "manifest.json,server" {
		t.Errorf("the backup folder holds %v", names)
	}
}

type fakeBackup struct {
	data []byte
	name string
	err  error
}

func (f fakeBackup) CreateFullBackup(context.Context) ([]byte, string, error) {
	return f.data, f.name, f.err
}

// The running binary is the release already: nothing happens.
func TestInstallNoChange(t *testing.T) {
	in := newInstall(t, func(o *Options) { o.Commit = newCommit })
	in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	out, err := in.svc.Install(context.Background(), InstallOptions{SHA: newCommit, Backup: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := mustJSON(t, out); got != `{"applied":[],"message":"No files to update"}` {
		t.Errorf("answer %s", got)
	}
	in.assertUntouched()
}

// Every step that fails before the swap leaves the install as it was.
func TestInstallFailuresLeaveTheInstall(t *testing.T) {
	private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, 32))
	keyText := base64.StdEncoding.EncodeToString(private.Public().(ed25519.PublicKey))
	for _, tc := range []struct {
		name  string
		setup func(in *install, rel *testRelease)
		sha   string
		want  string
	}{
		{"release lookup fails", func(in *install, _ *testRelease) {
			in.gh.on("GET https://api.github.com/repos/trybetterdesk/betterdesk/releases/latest", func(*http.Request) *http.Response {
				return respond(500, "boom", nil)
			})
		}, newCommit, "GitHub API 500: boom"},
		{"rate limit", func(in *install, _ *testRelease) {
			in.gh.on("GET https://api.github.com/repos/trybetterdesk/betterdesk/releases/latest", func(*http.Request) *http.Response {
				return respond(403, "API rate limit exceeded", http.Header{"X-Ratelimit-Reset": {"1795000000"}})
			})
		}, newCommit, "rate limit exceeded for unauthenticated requests"},
		{"another commit than the check's", nil, "abcdef1", "check for updates again"},
		{"older release", func(in *install, rel *testRelease) {
			*rel = *newTestRelease("3.4.0", newCommit, BranchStable)
			in.gh.serve(rel)
		}, newCommit, "older than the running version"},
		{"no binary for the platform", func(in *install, rel *testRelease) {
			rel.manifest.Files = rel.manifest.Files[1:]
			rel.render()
			in.gh.serve(rel)
		}, newCommit, "has no binary for linux/amd64"},
		{"manifest of another repository", func(in *install, rel *testRelease) {
			rel.manifest.Owner = "evil"
			rel.render()
			in.gh.serve(rel)
		}, newCommit, `owner "evil" is not trybetterdesk`},
		{"manifest of another branch", func(in *install, rel *testRelease) {
			rel.manifest.Branch, rel.manifest.Channel = "dev", "development"
			rel.render()
			in.gh.serve(rel)
		}, newCommit, `branch "dev" is not main`},
		{"key configured, no signature", func(in *install, _ *testRelease) { in.env[EnvPublicKey] = keyText }, newCommit, "carries no manifest.sig"},
		{"signature by another key", func(in *install, rel *testRelease) {
			in.env[EnvPublicKey] = keyText
			rel.sign(ed25519.NewKeyFromSeed(bytes.Repeat([]byte{2}, 32)))
			in.gh.serve(rel)
		}, newCommit, "did not verify"},
		{"signature over another manifest", func(in *install, rel *testRelease) {
			in.env[EnvPublicKey] = keyText
			rel.sign(private)
			rel.manifest.Files[0].SHA256 = sum([]byte("evil"))
			rel.binaries[asset] = []byte("evil")
			rel.render()
			in.gh.serve(rel)
		}, newCommit, "did not verify"},
		{"signing required, none configured", func(in *install, _ *testRelease) { in.env[EnvRequireSigning] = "true" }, newCommit, "UPDATE_REQUIRE_SIGNED_MANIFEST is enabled"},
		{"download fails", func(in *install, _ *testRelease) {
			in.gh.on("GET https://objects.githubusercontent.com/github-production-release-asset/"+asset, func(*http.Request) *http.Response {
				return respond(503, "", nil)
			})
		}, newCommit, "HTTP 503"},
		{"redirect to another host", func(in *install, rel *testRelease) {
			in.gh.on("GET https://github.com/trybetterdesk/betterdesk/releases/download/"+rel.tag+"/"+asset, func(*http.Request) *http.Response {
				return redirect("https://evil.example/file")
			})
		}, newCommit, "not GitHub's"},
		{"redirect to plain http", func(in *install, rel *testRelease) {
			in.gh.on("GET https://github.com/trybetterdesk/betterdesk/releases/download/"+rel.tag+"/"+asset, func(*http.Request) *http.Response {
				return redirect("http://objects.githubusercontent.com/x")
			})
		}, newCommit, "not GitHub's"},
		{"redirect loop", func(in *install, rel *testRelease) {
			loop := "https://github.com/trybetterdesk/betterdesk/releases/download/" + rel.tag + "/" + asset
			in.gh.on("GET "+loop, func(*http.Request) *http.Response { return redirect(loop) })
		}, newCommit, "too many redirects"},
		{"checksum mismatch", func(in *install, rel *testRelease) {
			rel.binaries[asset] = append([]byte(nil), rel.binaries[asset]...)
			rel.binaries[asset][0] ^= 1
			in.gh.serve(rel)
		}, newCommit, "does not have the SHA-256"},
		{"longer than the manifest says", func(in *install, rel *testRelease) {
			rel.binaries[asset] = append(rel.binaries[asset], ' ')
			in.gh.serve(rel)
		}, newCommit, "larger than allowed"},
		{"binary does not run", func(in *install, rel *testRelease) {
			rel.binaries[asset] = []byte("\x7fELF not this machine")
			rel.manifest.Files[0].SHA256, rel.manifest.Files[0].Size = sum(rel.binaries[asset]), int64(len(rel.binaries[asset]))
			rel.render()
			in.gh.serve(rel)
		}, newCommit, "does not run here"},
		{"binary of another version", func(in *install, rel *testRelease) {
			rel.binaries[asset] = binaryFor("3.7.0", newCommit)
			rel.manifest.Files[0].SHA256, rel.manifest.Files[0].Size = sum(rel.binaries[asset]), int64(len(rel.binaries[asset]))
			rel.render()
			in.gh.serve(rel)
		}, newCommit, "is version 3.7.0"},
		{"binary of another commit", func(in *install, rel *testRelease) {
			rel.binaries[asset] = binaryFor("3.6.0", oldCommit)
			rel.manifest.Files[0].SHA256, rel.manifest.Files[0].Size = sum(rel.binaries[asset]), int64(len(rel.binaries[asset]))
			rel.render()
			in.gh.serve(rel)
		}, newCommit, "was built from"},
		{"binary without a commit", func(in *install, rel *testRelease) {
			rel.binaries[asset] = binaryFor("3.6.0", "")
			rel.manifest.Files[0].SHA256, rel.manifest.Files[0].Size = sum(rel.binaries[asset]), int64(len(rel.binaries[asset]))
			rel.render()
			in.gh.serve(rel)
		}, newCommit, "names no commit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := newInstall(t)
			rel := newTestRelease("3.6.0", newCommit, BranchStable)
			in.gh.serve(rel)
			if tc.setup != nil {
				tc.setup(in, rel)
			}
			in.svc = New(in.opts)
			_, err := in.svc.Install(context.Background(), InstallOptions{SHA: tc.sha, Backup: true})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
			in.assertUntouched()
			if _, err := os.Stat(in.binary + ".prev"); err == nil {
				t.Error("a .prev was made although nothing was swapped")
			}
		})
	}
}

// Failures of the backup and of the swap itself leave the old binary.
func TestInstallSwapFailures(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name   string
		aside  bool
		mutate func(o *fileOps)
		backup error
		want   string
	}{
		{name: "data backup fails", backup: boom, want: "back up the data: boom"},
		{name: "copy into the folder fails", mutate: func(o *fileOps) {
			o.copy = func(string, string, string) error { return boom }
		}, want: "copy the new binary"},
		{name: "keeping .prev fails", mutate: func(o *fileOps) {
			o.link = func(string, string) error { return boom }
			real := o.copy
			o.copy = func(src, dst, s string) error {
				if strings.HasSuffix(dst, ".prev") {
					return boom
				}
				return real(src, dst, s)
			}
		}, want: "keep the running binary"},
		{name: "final rename fails", mutate: func(o *fileOps) {
			o.rename = func(old, new string) error { return boom }
		}, want: "put the new binary in place"},
		{name: "windows: moving the binary aside fails", aside: true, mutate: func(o *fileOps) {
			o.rename = func(old, new string) error { return boom }
		}, want: "move the running binary"},
		{name: "windows: the new name fails, the old binary goes back", aside: true, mutate: func(o *fileOps) {
			o.rename = func(old, new string) error {
				if strings.Contains(old, ".new.") {
					return boom
				}
				return os.Rename(old, new)
			}
		}, want: "put the new binary in place"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			aside := tc.aside
			in := newInstall(t, func(o *Options) {
				o.RenameAside = &aside
				if tc.backup != nil {
					o.DataBackup = fakeBackup{err: tc.backup}
				}
			})
			in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
			ops := osFileOps()
			if tc.mutate != nil {
				tc.mutate(&ops)
			}
			in.svc.ops = ops
			_, err := in.svc.Install(context.Background(), InstallOptions{SHA: newCommit, Backup: true})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
			in.assertUntouched()
			if tc.backup != nil {
				if entries, _ := os.ReadDir(filepath.Join(in.dataDir, backupsDir)); len(entries) != 0 {
					t.Errorf("a half-made backup stays: %d", len(entries))
				}
			}
		})
	}
}

// A hard link that fails (another file system, no links) falls back to a
// copy for .prev.
func TestInstallCopiesPrevWithoutLinks(t *testing.T) {
	in := newInstall(t)
	in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	ops := osFileOps()
	ops.link = func(string, string) error { return errors.New("no links here") }
	in.svc.ops = ops
	installed(t, in, newCommit)
	if prev, err := os.ReadFile(in.binary + ".prev"); err != nil || string(prev) != string(binaryFor("3.5.0", oldCommit)) {
		t.Fatalf("prev: %v", err)
	}
}

// When the pending marker cannot be written, the swap is undone: an
// update without a marker would have no automatic rollback.
func TestInstallUndoesTheSwapWithoutMarker(t *testing.T) {
	in := newInstall(t)
	in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	if err := os.Mkdir(in.binary+".update-pending.json", 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := in.svc.Install(context.Background(), InstallOptions{SHA: newCommit})
	if err == nil || !strings.Contains(err.Error(), "write the pending marker") {
		t.Fatalf("error %v", err)
	}
	if in.binaryContent() != string(binaryFor("3.5.0", oldCommit)) {
		t.Error("the old binary was not put back")
	}
}

// One install at a time.
func TestInstallIsExclusive(t *testing.T) {
	in := newInstall(t)
	in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	in.svc.busy.Store(true)
	if _, err := in.svc.Install(context.Background(), InstallOptions{SHA: newCommit}); !errors.Is(err, ErrBusy) {
		t.Fatalf("error %v", err)
	}
	in.svc.busy.Store(false)
	installed(t, in, newCommit)
}

// A Docker image install refuses.
func TestInstallRefusedInImageMode(t *testing.T) {
	in := newInstall(t, func(o *Options) { o.Docker = true })
	if _, err := in.svc.Install(context.Background(), InstallOptions{SHA: newCommit}); err == nil || !strings.Contains(err.Error(), "docker compose pull && docker compose up -d") {
		t.Fatalf("error %v", err)
	}
	in.assertUntouched()
}

// The real self-test runs the binary with -version, without the
// process's environment.
func TestRunVersionRunsTheBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "bin")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"betterdesk-server 1.2.3 (built x, commit "+newCommit+")$SECRET_FOR_TEST\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SECRET_FOR_TEST", "leak")
	out, err := runVersion(context.Background(), script)
	if err != nil || strings.TrimSpace(out) != "betterdesk-server 1.2.3 (built x, commit "+newCommit+")" {
		t.Fatalf("out %q err %v", out, err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runVersion(context.Background(), script); err == nil {
		t.Error("a failing binary passed")
	}
}

// A repair installs the release of the running version again.
func TestRepair(t *testing.T) {
	in := newInstall(t, func(o *Options) { o.Version, o.Commit = "3.6.0", newCommit })
	if err := os.WriteFile(in.binary, []byte("damaged"), 0o755); err != nil {
		t.Fatal(err)
	}
	rel := newTestRelease("3.6.0", newCommit, BranchStable)
	in.gh.serve(rel)
	if err := (dataFiles{dir: in.dataDir, now: in.opts.Now}).markStale("rollback", "x", ""); err != nil {
		t.Fatal(err)
	}
	out := in.svc.RepairResult(context.Background())
	if !out.Success || out.Steps.Deploy == nil || out.Steps.Restart == nil || out.Steps.Restart.Error != RestartBlocked {
		t.Fatalf("repair %+v", out)
	}
	if in.binaryContent() != string(rel.binaries[asset]) {
		t.Error("the binary was not replaced")
	}
	if _, err := os.Stat(filepath.Join(in.dataDir, staleFile)); err == nil {
		t.Error("the stale marker is still there")
	}
	var marker pendingMarker
	in.readJSON(in.binary+".update-pending.json", &marker)
	if marker.Kind != "repair" {
		t.Errorf("marker %+v", marker)
	}

	// A version without a release fails and changes nothing.
	other := newInstall(t, func(o *Options) { o.Version = "3.5.9" })
	other.gh.serve(rel)
	if out := other.svc.RepairResult(context.Background()); out.Success || !strings.Contains(out.Error, "no release of version 3.5.9") {
		t.Errorf("repair of an unknown version %+v", out)
	}
	other.assertUntouched()
}

// A build that does not know its commit is not updated in the app; the
// refusal comes before any request to GitHub.
func TestInstallNeedsTheRunningCommit(t *testing.T) {
	in := newInstall(t, func(o *Options) { o.Commit = "" })
	in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	if _, err := in.svc.Install(context.Background(), InstallOptions{SHA: newCommit}); err == nil || err.Error() != "No local baseline SHA — run update check first" {
		t.Fatalf("error %v", err)
	}
	if _, err := in.svc.Changes(context.Background(), newCommit); err == nil || err.Error() != "No local baseline SHA — run update check first" {
		t.Fatalf("changes %v", err)
	}
	if n := len(in.gh.requests); n != 0 {
		t.Errorf("%d requests to GitHub", n)
	}
	in.assertUntouched()
}
