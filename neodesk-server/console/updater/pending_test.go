package updater

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// startAs runs StartupCheck as the binary of the install with a version
// and commit.
func (in *install) startAs(version, commit string, args ...string) *Pending {
	in.t.Helper()
	aside := *in.opts.RenameAside
	return StartupCheck(StartupOptions{
		Args: args, Version: version, Commit: commit,
		Executable:  in.opts.Executable,
		Now:         in.opts.Now,
		RenameAside: aside,
	})
}

func (in *install) marker() (pendingMarker, bool) {
	m, err := readMarker(in.binary + ".update-pending.json")
	return m, err == nil
}

// The new binary gets three starts to become ready; the fourth puts the
// old one back, records why, and asks the supervisor for the old one.
func TestRollbackAfterThreeFailedStarts(t *testing.T) {
	for _, aside := range []bool{false, true} {
		t.Run(map[bool]string{false: "unix", true: "windows"}[aside], func(t *testing.T) {
			in := newInstall(t, func(o *Options) { o.RenameAside = &aside })
			in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
			installed(t, in, newCommit)

			for start := 1; start <= MaxStarts; start++ {
				p := in.startAs("3.6.0", newCommit)
				if p.RolledBack() {
					t.Fatalf("start %d rolled back", start)
				}
				if m, ok := in.marker(); !ok || m.Starts != start {
					t.Fatalf("start %d: marker %+v %v", start, m, ok)
				}
			}
			p := in.startAs("3.6.0", newCommit)
			if !p.RolledBack() {
				t.Fatal("the fourth start did not roll back")
			}
			if in.binaryContent() != string(binaryFor("3.5.0", oldCommit)) {
				t.Fatal("the old binary is not back")
			}
			if _, ok := in.marker(); ok {
				t.Error("the marker is still there")
			}
			status := (dataFiles{dir: in.dataDir}).binaryStatus()
			stale, ok := status.(StaleStatus)
			if !ok || !stale.Stale || stale.Reason != "rollback" || !strings.Contains(stale.Detail.(string), "did not become ready") {
				t.Errorf("stale status %+v", status)
			}
			var last LastResult
			in.readJSON(filepath.Join(in.dataDir, lastResultFile), &last)
			if !last.RolledBack || last.SHA != newCommit || len(last.Failed) != 1 || last.Failed[0].File != "betterdesk-server-update" {
				t.Errorf("last result %+v", last)
			}
			// The panel shows it: a failure that is not one of the old
			// non-critical ones.
			display := in.svc.files.resultForDisplay(oldCommit, newCommit, "")
			if display == nil {
				t.Error("the rollback is not shown")
			}
			// The old binary starts without counting anything.
			if p := in.startAs("3.5.0", oldCommit); p.RolledBack() {
				t.Error("the old binary rolled back")
			}
		})
	}
}

// A binary that becomes ready ends the wait: marker gone, commit saved,
// success recorded, stale marker cleared.
func TestReadyAfterAnUpdate(t *testing.T) {
	in := newInstall(t)
	in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	installed(t, in, newCommit)
	files := dataFiles{dir: in.dataDir, now: in.opts.Now}
	if err := files.markStale("rollback", "earlier", ""); err != nil {
		t.Fatal(err)
	}
	p := in.startAs("3.6.0", newCommit)
	p.Ready()
	if _, ok := in.marker(); ok {
		t.Error("the marker is still there")
	}
	if sha, _ := os.ReadFile(filepath.Join(in.dataDir, shaFile)); string(sha) != newCommit+"\n" {
		t.Errorf(".update_sha %q", sha)
	}
	if _, err := os.Stat(filepath.Join(in.dataDir, staleFile)); err == nil {
		t.Error("the stale marker is still there")
	}
	var last LastResult
	in.readJSON(filepath.Join(in.dataDir, lastResultFile), &last)
	if last.SHA != newCommit || last.Applied != 1 || len(last.Failed) != 0 || !last.ShaSaved {
		t.Errorf("last result %+v", last)
	}
	// Shown no more: the running commit is the result's.
	if in.svc.files.resultForDisplay(newCommit, "", "") != nil {
		t.Error("a success is shown as a problem")
	}
	// Ready twice does nothing more.
	p.Ready()
}

// A run that only prints something does not count as a start.
func TestStartupCheckIgnoresVersionRuns(t *testing.T) {
	in := newInstall(t)
	in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	installed(t, in, newCommit)
	for i := 0; i < 5; i++ {
		for _, args := range [][]string{{"-version"}, {"--version"}, {"-h"}, {"-migrate-sqlite-auth=true"}} {
			if p := in.startAs("3.6.0", newCommit, args...); p.RolledBack() {
				t.Fatalf("%v rolled back", args)
			}
		}
	}
	if m, ok := in.marker(); !ok || m.Starts != 0 {
		t.Errorf("marker %+v", m)
	}
}

// A marker written for another binary is removed: an operator put
// another version in place.
func TestStartupCheckDropsAForeignMarker(t *testing.T) {
	in := newInstall(t)
	in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	installed(t, in, newCommit)
	for i := 0; i < 5; i++ {
		if p := in.startAs("3.7.0", "3333333333333333333333333333333333333333"); p.RolledBack() {
			t.Fatal("another version rolled back")
		}
	}
	if _, ok := in.marker(); ok {
		t.Error("the marker of another version stays")
	}
	if in.binaryContent() != string(binaryFor("3.6.0", newCommit)) {
		t.Error("the binary changed")
	}

	// A build that names no commit is told apart by its version alone.
	other := newInstall(t)
	other.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	installed(t, other, newCommit)
	other.startAs("3.7.0", "")
	if _, ok := other.marker(); ok {
		t.Error("the marker of another version without a commit stays")
	}
}

// The previous binary is put back only when it is the one the update
// kept.
func TestRollbackChecksThePreviousBinary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(in *install)
		detail string
	}{
		{"changed", func(in *install) { _ = os.WriteFile(in.binary+".prev", []byte("other"), 0o755) }, "was changed since the update"},
		{"missing", func(in *install) { _ = os.Remove(in.binary + ".prev") }, "previous binary is missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := newInstall(t)
			in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
			installed(t, in, newCommit)
			tc.change(in)
			var p *Pending
			for i := 0; i <= MaxStarts; i++ {
				p = in.startAs("3.6.0", newCommit)
			}
			if p.RolledBack() {
				t.Fatal("rolled back to a binary that is not the update's")
			}
			if in.binaryContent() != string(binaryFor("3.6.0", newCommit)) {
				t.Error("the binary was replaced")
			}
			if _, ok := in.marker(); ok {
				t.Error("the marker stays, so every start would try again")
			}
			stale := (dataFiles{dir: in.dataDir}).binaryStatus().(StaleStatus)
			if !strings.Contains(stale.Detail.(string), tc.detail) {
				t.Errorf("detail %v", stale.Detail)
			}
		})
	}
}

// A failed swap back leaves the new binary and says so.
func TestRollbackSwapFails(t *testing.T) {
	in := newInstall(t)
	in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	installed(t, in, newCommit)
	ops := osFileOps()
	ops.rename = func(string, string) error { return errors.New("locked") }
	var p *Pending
	for i := 0; i <= MaxStarts; i++ {
		p = StartupCheck(StartupOptions{Version: "3.6.0", Commit: newCommit, Executable: in.opts.Executable, Now: in.opts.Now, ops: ops})
	}
	if p.RolledBack() || in.binaryContent() != string(binaryFor("3.6.0", newCommit)) {
		t.Fatal("a failed swap reported a rollback")
	}
	stale := (dataFiles{dir: in.dataDir}).binaryStatus().(StaleStatus)
	if !strings.Contains(stale.Detail.(string), "could not be put back: put the previous binary back: locked") {
		t.Errorf("detail %v", stale.Detail)
	}
}

// A corrupt marker is removed; leftovers of earlier swaps go.
func TestStartupCheckCleansUp(t *testing.T) {
	in := newInstall(t)
	for _, name := range []string{".betterdesk-server.new.77", "betterdesk-server.old.77.123", "betterdesk-server.prev", "keep.txt", ".other.new.1"} {
		if err := os.WriteFile(filepath.Join(in.dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(in.binary+".update-pending.json", []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	in.startAs("3.5.0", oldCommit)
	for name, want := range map[string]bool{
		".betterdesk-server.new.77": false, "betterdesk-server.old.77.123": false, "betterdesk-server.prev": true,
		"keep.txt": true, ".other.new.1": true, "betterdesk-server.update-pending.json": false,
	} {
		_, err := os.Stat(filepath.Join(in.dir, name))
		if (err == nil) != want {
			t.Errorf("%s: present %v, want %v", name, err == nil, want)
		}
	}
}

// The operator's rollback swaps the two binaries, so a second one undoes
// the first.
func TestRollbackCommand(t *testing.T) {
	for _, aside := range []bool{false, true} {
		in := newInstall(t, func(o *Options) { o.RenameAside = &aside })
		in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
		installed(t, in, newCommit)
		if _, err := in.svc.Rollback(); err != nil {
			t.Fatal(err)
		}
		if in.binaryContent() != string(binaryFor("3.5.0", oldCommit)) {
			t.Fatal("the old binary is not back")
		}
		if prev, _ := os.ReadFile(in.binary + ".prev"); string(prev) != string(binaryFor("3.6.0", newCommit)) {
			t.Fatal("the replaced binary is not kept")
		}
		if _, ok := in.marker(); ok {
			t.Error("the marker stays")
		}
		if _, err := in.svc.Rollback(); err != nil {
			t.Fatal(err)
		}
		if in.binaryContent() != string(binaryFor("3.6.0", newCommit)) {
			t.Error("the second rollback did not undo the first")
		}
	}
	empty := newInstall(t)
	if _, err := empty.svc.Rollback(); err == nil || !strings.Contains(err.Error(), "no previous binary") {
		t.Errorf("rollback without .prev: %v", err)
	}
	empty.assertUntouched()
}

// The data folder a marker names is used only when it is an absolute,
// clean path of an existing folder.
func TestCheckedDataDir(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	_ = os.WriteFile(file, nil, 0o644)
	for path, ok := range map[string]bool{
		dir: true, "": false, "relative/data": false, dir + "/../" + filepath.Base(dir): false,
		file: false, filepath.Join(dir, "missing"): false,
	} {
		if _, got := checkedDataDir(path); got != ok {
			t.Errorf("checkedDataDir(%q) = %v", path, got)
		}
	}
}
