package updater

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeNodeBackup writes a backup as the old console made it.
func writeNodeBackup(t *testing.T, dataDir, name, manifest string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(dataDir, backupsDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The list shows the old console's backups and this updater's alike,
// newest first, in the old shape.
func TestListBackups(t *testing.T) {
	in := newInstall(t)
	writeNodeBackup(t, in.dataDir, "pre-update-2026-01-01T10-00-00",
		`{"version":"3.5.100","sha":"abcdef0123456789","timestamp":"2026-01-01T10:00:00.000Z","filesBackedUp":2,"files":["console/server.js","server/main.go"]}`,
		map[string]string{"console/server.js": "12345", "server/main.go": "123"})
	writeNodeBackup(t, in.dataDir, "pre-update-2026-02-01T10-00-00", `{broken`, nil)
	writeNodeBackup(t, in.dataDir, "other-folder", `{}`, nil)
	in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	installed(t, in, newCommit)

	list := in.svc.ListBackups()
	if len(list) != 3 {
		t.Fatalf("%d backups: %+v", len(list), list)
	}
	if list[0].Version != "3.5.0" || list[0].SHA != oldCommit[:7] || list[0].Timestamp != "2026-11-18T12:00:00.000Z" || list[0].FilesBackedUp != 1.0 {
		t.Errorf("newest %+v", list[0])
	}
	if list[1].Name != "pre-update-2026-01-01T10-00-00" || list[1].Version != "3.5.100" || list[1].SHA != "abcdef0" ||
		list[1].FileCount != 2.0 || list[1].SizeBytes != int64(8+len(`{"version":"3.5.100","sha":"abcdef0123456789","timestamp":"2026-01-01T10:00:00.000Z","filesBackedUp":2,"files":["console/server.js","server/main.go"]}`)) {
		t.Errorf("node backup %+v", list[1])
	}
	if list[2].Version != "unknown" || list[2].Timestamp != "" || list[2].FilesBackedUp != 0.0 {
		t.Errorf("broken manifest %+v", list[2])
	}
	got := mustJSON(t, list[1])
	if !strings.HasPrefix(got, `{"name":"pre-update-2026-01-01T10-00-00","path":`) || !strings.Contains(got, `"version":"3.5.100","sha":"abcdef0","timestamp":"2026-01-01T10:00:00.000Z","filesBackedUp":2,"fileCount":2,"sizeBytes":`) {
		t.Errorf("JSON %s", got)
	}
	if empty := newInstall(t); mustJSON(t, empty.svc.ListBackups()) != "[]" {
		t.Error("no backups is not []")
	}
}

func TestDeleteAndPruneBackups(t *testing.T) {
	in := newInstall(t)
	for _, name := range []string{"pre-update-2026-01-01T10-00-00", "pre-update-2026-01-02T10-00-00", "pre-update-2026-01-03T10-00-00"} {
		writeNodeBackup(t, in.dataDir, name, `{"timestamp":"`+name[11:]+`"}`, nil)
	}
	for _, tc := range []struct{ name, want string }{
		{"../outside", "Invalid backup name"}, {"pre-update-x", "Invalid backup name"}, {"pre-update-2026/..", "Invalid backup name"},
		{"pre-update-2027-01-01T10-00-00", "Backup not found"}, {"", "Invalid backup name"},
	} {
		if _, err := in.svc.DeleteBackup(tc.name); err == nil || err.Error() != tc.want {
			t.Errorf("delete %q: %v", tc.name, err)
		}
	}
	// A link is not followed.
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(in.dataDir, backupsDir, "pre-update-2026-05-05T10-00-00")); err == nil {
		if _, err := in.svc.DeleteBackup("pre-update-2026-05-05T10-00-00"); err == nil || err.Error() != "Backup not found" {
			t.Errorf("delete of a link: %v", err)
		}
		_ = os.Remove(filepath.Join(in.dataDir, backupsDir, "pre-update-2026-05-05T10-00-00"))
	}
	if got, err := in.svc.DeleteBackup("pre-update-2026-01-02T10-00-00"); err != nil || got["deleted"] != "pre-update-2026-01-02T10-00-00" {
		t.Fatalf("delete: %v %v", got, err)
	}
	if got := in.svc.PruneBackups(0); got.Kept != -1 || len(got.Deleted) != 0 {
		t.Errorf("prune 0: %+v", got)
	}
	if got := in.svc.PruneBackups(5); got.Kept != 5 || len(got.Deleted) != 0 {
		t.Errorf("prune 5: %+v", got)
	}
	if got := in.svc.PruneBackups(1); got.Kept != 1 || mustJSON(t, got.Deleted) != `["pre-update-2026-01-01T10-00-00"]` {
		t.Errorf("prune 1: %+v", got)
	}
}

// After a backup the old ones are pruned to the retention: the setting
// when it is a positive number, else BACKUP_RETENTION_COUNT.
func TestBackupRetention(t *testing.T) {
	for _, tc := range []struct {
		setting, env string
		found        bool
		want         int
	}{
		{"2", "", true, 2}, {"0", "3", true, 3}, {"abc", "", true, 0}, {"", "1", false, 1}, {"2x", "", true, 2}, {"", "", false, 0},
	} {
		in := newInstall(t, func(o *Options) {
			o.Setting = func(context.Context, string) (string, bool, error) { return tc.setting, tc.found, nil }
		})
		in.env["BACKUP_RETENTION_COUNT"] = tc.env
		if got := in.svc.retention(context.Background()); got != tc.want {
			t.Errorf("setting %q env %q: %d, want %d", tc.setting, tc.env, got, tc.want)
		}
	}
	in := newInstall(t, func(o *Options) {
		o.Setting = func(context.Context, string) (string, bool, error) { return "1", true, nil }
	})
	writeNodeBackup(t, in.dataDir, "pre-update-2026-01-01T10-00-00", `{"timestamp":"2026-01-01"}`, nil)
	in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	installed(t, in, newCommit)
	if list := in.svc.ListBackups(); len(list) != 1 || list[0].Version != "3.5.0" {
		t.Errorf("after the install: %+v", list)
	}
}

// A restore puts the backup's binary back with the pending marker; the
// old console's backups are refused (A29), and so are changed binaries
// and versions the installed release does not allow.
func TestRestore(t *testing.T) {
	in := newInstall(t)
	in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	result := installed(t, in, newCommit)
	name := filepath.Base(*result.BackupPath)
	in.svc = New(func() Options { o := in.opts; o.Version, o.Commit = "3.6.0", newCommit; return o }())

	restored, err := in.svc.Restore(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	if mustJSON(t, restored) != `{"restored":1,"removed":0,"version":"3.5.0","sha":"`+oldCommit+`","totalFiles":1,"consoleRestartBlocked":"Start the server again to finish the update"}` {
		t.Errorf("answer %s", mustJSON(t, restored))
	}
	if in.binaryContent() != string(binaryFor("3.5.0", oldCommit)) {
		t.Fatal("the backup's binary is not in place")
	}
	m, ok := in.marker()
	if !ok || m.Kind != "restore" || m.To != "3.5.0" || m.From != "3.6.0" {
		t.Errorf("marker %+v", m)
	}

	writeNodeBackup(t, in.dataDir, "pre-update-2026-01-01T10-00-00", `{"version":"3.5.100","sha":"abc","files":["console/server.js"]}`, map[string]string{"console/server.js": "x"})
	if _, err := in.svc.Restore(context.Background(), "pre-update-2026-01-01T10-00-00"); !IsNodeBackup(err) {
		t.Errorf("node backup: %v", err)
	}
	if _, err := in.svc.Restore(context.Background(), "pre-update-2027-01-01T10-00-00"); err == nil || err.Error() != "Backup not found" {
		t.Errorf("missing: %v", err)
	}
	if _, err := in.svc.Restore(context.Background(), "../x"); err == nil || err.Error() != "Invalid backup name" {
		t.Errorf("invalid: %v", err)
	}

	changed := filepath.Join(*result.BackupPath, "server", "betterdesk-server")
	if err := os.WriteFile(changed, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := in.svc.Restore(context.Background(), name); err == nil || !strings.Contains(err.Error(), "was changed since the backup") {
		t.Errorf("changed binary: %v", err)
	}
}

func TestRestoreRefusesBelowMinRollback(t *testing.T) {
	in := newInstall(t)
	rel := newTestRelease("3.6.0", newCommit, BranchStable)
	rel.manifest.MinRollbackVersion = "3.5.5"
	rel.render()
	in.gh.serve(rel)
	result := installed(t, in, newCommit)
	if _, err := in.svc.Restore(context.Background(), filepath.Base(*result.BackupPath)); err == nil || !strings.Contains(err.Error(), "older than the installed release allows (3.5.5)") {
		t.Errorf("restore below min_rollback_version: %v", err)
	}
}

// A backup can only put back a binary of the running binary's name, from
// its own server folder.
func TestRestoreRefusesAnotherBinary(t *testing.T) {
	in := newInstall(t)
	in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	result := installed(t, in, newCommit)
	dir := *result.BackupPath
	var m BackupManifest
	in.readJSON(filepath.Join(dir, "manifest.json"), &m)
	for _, name := range []string{"server/other-binary", "server/sub/other-binary"} {
		other := filepath.Join(dir, "server", "other-binary")
		_ = os.WriteFile(other, binaryFor("3.5.0", oldCommit), 0o755)
		m.Files = []string{name}
		text, _ := json.Marshal(m)
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), text, 0o640); err != nil {
			t.Fatal(err)
		}
		if _, err := in.svc.Restore(context.Background(), filepath.Base(dir)); err == nil || !strings.Contains(err.Error(), "another binary") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if in.binaryContent() != string(binaryFor("3.6.0", newCommit)) {
		t.Error("the binary changed")
	}
}
