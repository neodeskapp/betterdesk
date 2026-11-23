package updater

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
)

func testFiles(t *testing.T) dataFiles {
	t.Helper()
	return dataFiles{dir: t.TempDir(), now: func() time.Time { return time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC) }}
}

func writeRecord(t *testing.T, d dataFiles, text string) {
	t.Helper()
	if err := os.WriteFile(d.path(lastResultFile), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The Jest tests of updateResultStore.js and updateFailurePolicy.js.
func TestResultForDisplay(t *testing.T) {
	full := "6d4d42b1234567890abcdef1234567890abcdef1"
	if !shaMatches("6d4d42b", full) || !shaMatches(full, "6d4d42b") || shaMatches("deadbeef", full) || shaMatches("", full) {
		t.Error("shaMatches")
	}

	d := testFiles(t)
	if err := d.persistResult(LastResult{SHA: "abc1234567890", Applied: 3, Failed: []Failure{{File: "x", Error: "e"}}, ServicesFailed: []ServiceFailure{{Service: "s"}}}); err != nil {
		t.Fatal(err)
	}
	record := d.readResult()
	if goapi.Dig(record, "sha") != "abc1234567890" || goapi.Dig(record, "applied") != 3.0 || goapi.Dig(record, "savedAt") != "2026-11-18T00:00:00.000Z" {
		t.Errorf("record %v", mustJSON(t, record))
	}

	// The target commit was reached some other way: the record goes.
	writeRecord(t, d, `{"sha":"6d4d42b","failed":[{"file":"web-nodejs/services/dbAdapter.js"}]}`)
	if d.resultForDisplay(full, "", "") != nil || d.readResult() != nil {
		t.Error("a reached target is still shown")
	}
	// At the release's commit while the record targets an older one.
	writeRecord(t, d, `{"sha":"aaaaaaa","failed":[{"file":"web-nodejs/x.js"}]}`)
	if d.resultForDisplay(full, full, "") != nil || d.readResult() != nil {
		t.Error("an old target is still shown")
	}
	// Non-critical failures only.
	writeRecord(t, d, `{"sha":"bbbbbbb","failed":[{"file":"npm install"},{"file":"x","nonCritical":true}]}`)
	if d.resultForDisplay(full, "", "") != nil || d.readResult() != nil {
		t.Error("non-critical failures are shown")
	}
	// Critical ones stay, split from the others, keys in their order.
	writeRecord(t, d, `{"savedAt":"t","sha":"bbbbbbb","failed":[{"file":"web-nodejs/services/dbAdapter.js","error":"e"},{"file":"neodesk-server-deploy"}],"servicesFailed":[]}`)
	got := d.resultForDisplay(full, "", "")
	if s := mustJSON(t, got); s != `{"savedAt":"t","sha":"bbbbbbb","failed":[{"file":"web-nodejs/services/dbAdapter.js","error":"e"}],"servicesFailed":[],"nonCriticalFailures":[{"file":"neodesk-server-deploy"}]}` {
		t.Errorf("display %s", s)
	}
	// A restart that could not happen is shown.
	writeRecord(t, d, `{"sha":"bbbbbbb","failed":[],"consoleRestartBlocked":"Start the server again to finish the update"}`)
	if d.resultForDisplay(full, "", "") == nil {
		t.Error("a blocked restart is not shown")
	}
	if !d.clearResult() || d.readResult() != nil || d.clearResult() {
		t.Error("clear")
	}
	writeRecord(t, d, `{broken`)
	if d.resultForDisplay(full, "", "") != nil {
		t.Error("a broken file is shown")
	}
}

func TestSplitFailures(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "routes"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "routes", "index.js"), nil, 0o644)
	_ = os.MkdirAll(filepath.Join(root, "lib", "x"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "lib", "x", "index.js"), nil, 0o644)
	for file, want := range map[string]bool{
		"npm install": true, "neodesk-server.service": true, "support-agent-source-sync": true, "support-agent-rebuild-defer": true,
		"web-nodejs/routes.js": true, "web-nodejs/scripts/scripts/foo.js": true, "web-nodejs/lib/x.js": true,
		"web-nodejs/services/dbAdapter.js": false, "neodesk-server-update": false,
	} {
		if got := failureNonCritical(goapi.NewObject("file", file), root); got != want {
			t.Errorf("%s: non-critical %v", file, got)
		}
	}
	critical, nonCritical := splitFailures([]any{
		goapi.NewObject("file", "web-nodejs/services/dbAdapter.js"), goapi.NewObject("file", "npm install", "nodeModulesOk", true),
		goapi.NewObject("file", "web-ui build"), nil,
	}, root)
	if len(critical) != 2 || len(nonCritical) != 2 {
		t.Errorf("critical %d, non-critical %d", len(critical), len(nonCritical))
	}
}

func TestBinaryStatus(t *testing.T) {
	d := testFiles(t)
	if s := mustJSON(t, d.binaryStatus()); s != `{"stale":false}` {
		t.Errorf("none %s", s)
	}
	if err := d.markStale("rollback", "detail", ""); err != nil {
		t.Fatal(err)
	}
	if s := mustJSON(t, d.binaryStatus()); s != `{"stale":true,"reason":"rollback","detail":"detail","sha":null,"since":"2026-11-18T00:00:00.000Z"}` {
		t.Errorf("stale %s", s)
	}
	for text, want := range map[string]string{
		`null`:                   `{"stale":false}`,
		`{x`:                     `{"stale":false}`,
		`"text"`:                 `{"stale":true,"reason":null,"detail":null,"sha":null,"since":null}`,
		`{"reason":0,"sha":"a"}`: `{"stale":true,"reason":null,"detail":null,"sha":"a","since":null}`,
	} {
		_ = os.WriteFile(d.path(staleFile), []byte(text), 0o644)
		if s := mustJSON(t, d.binaryStatus()); s != want {
			t.Errorf("%s: %s", text, s)
		}
	}
}

func TestInstallStatus(t *testing.T) {
	in := newInstall(t)
	if err := in.svc.files.markInProgress(newCommit, "3.6.0", 1); err != nil {
		t.Fatal(err)
	}
	got := in.svc.InstallStatus()
	if got.InProgress || got.Marker != nil || *got.LocalSHA != oldCommit || got.Pending != nil {
		t.Errorf("idle %+v", got)
	}
	if _, err := os.Stat(filepath.Join(in.dataDir, inProgressFile)); err == nil {
		t.Error("an orphaned marker stays")
	}
	in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	installed(t, in, newCommit)
	got = in.svc.InstallStatus()
	if got.Pending == nil || got.Pending.To != "3.6.0" || got.Pending.Kind != "update" {
		t.Errorf("pending %+v", got.Pending)
	}
	in.svc.busy.Store(true)
	_ = in.svc.files.markInProgress(newCommit, "3.6.0", 1)
	if got := in.svc.InstallStatus(); !got.InProgress || goapi.Dig(got.Marker, "sha") != newCommit {
		t.Errorf("busy %+v", got)
	}
}
