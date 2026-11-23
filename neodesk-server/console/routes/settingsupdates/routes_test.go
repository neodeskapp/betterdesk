package settingsupdates_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/restart"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/settingsupdates"
	"github.com/neodeskapp/neodesk/neodesk-server/console/updater"
)

const (
	oldCommit = "1111111111111111111111111111111111111111"
	newCommit = "2222222222222222222222222222222222222222"
	asset     = "neodesk-server-linux-amd64"
)

// github answers what the updater asks GitHub: a release of version 3.6.0
// with its manifest and binary when release is set, 404 otherwise, or a
// rate limit.
type github struct {
	mu        sync.Mutex
	release   bool
	rateLimit bool
}

func binaryFor(version, commit string) []byte {
	return []byte(fmt.Sprintf("#!/bin/sh\necho 'neodesk-server %s (built x, commit %s)'\n", version, commit))
}

func hexSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func (g *github) RoundTrip(req *http.Request) (*http.Response, error) {
	g.mu.Lock()
	release, limited := g.release, g.rateLimit
	g.mu.Unlock()
	reply := func(status int, body string) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	if limited {
		return reply(403, "API rate limit exceeded")
	}
	if !release {
		return reply(404, `{"message":"Not Found"}`)
	}
	bin := binaryFor("3.6.0", newCommit)
	files := map[string]string{asset: string(bin)}
	manifest, _ := json.Marshal(map[string]any{
		"schema_version": 1, "product": "neodesk-server", "version": "3.6.0", "channel": "stable", "owner": "neodeskapp",
		"repo": "neodesk", "branch": "main", "sha": newCommit,
		"files": []any{map[string]any{"path": asset, "sha256": hexSum(bin), "size": len(bin)}},
	})
	files["manifest.json"] = string(manifest)
	switch u := req.URL.String(); {
	case u == "https://api.github.com/repos/neodeskapp/neodesk/releases/latest":
		var assets []map[string]any
		for name := range files {
			assets = append(assets, map[string]any{"name": name, "browser_download_url": "https://github.com/neodeskapp/neodesk/releases/download/v3.6.0/" + name})
		}
		body, _ := json.Marshal(map[string]any{"tag_name": "v3.6.0", "name": "NeoDesk 3.6.0", "assets": assets})
		return reply(200, string(body))
	case strings.HasPrefix(u, "https://github.com/neodeskapp/neodesk/releases/download/v3.6.0/"):
		return reply(200, files[strings.TrimPrefix(u, "https://github.com/neodeskapp/neodesk/releases/download/v3.6.0/")])
	}
	return reply(404, `{"message":"Not Found"}`)
}

var reEcho = regexp.MustCompile(`echo '([^']*)'`)

func fakeVersion(_ context.Context, binary string) (string, error) {
	data, err := os.ReadFile(binary)
	if err != nil {
		return "", err
	}
	if m := reEcho.FindSubmatch(data); m != nil {
		return string(m[1]), nil
	}
	return "", fmt.Errorf("exec format error")
}

type world struct {
	*routetest.World
	gh      *github
	env     map[string]string
	binary  string
	console string
	exits   *int
}

// newWorld is a console whose updater talks to the fake GitHub and swaps
// a fake binary in a folder of the test.
func newWorld(t *testing.T, docker bool) *world {
	t.Helper()
	consoleDir := t.TempDir()
	w := &world{gh: &github{}, env: map[string]string{}, console: consoleDir, exits: new(int)}
	w.World = routetest.New(t, routetest.Options{Config: func(cfg *config.Config) { cfg.ConsoleDir = consoleDir }})
	bin := t.TempDir()
	w.binary = filepath.Join(bin, "neodesk-server")
	if err := os.WriteFile(w.binary, binaryFor("3.5.0", oldCommit), 0o755); err != nil {
		t.Fatal(err)
	}
	aside := false
	d := w.Server.Deps()
	d.Updater = updater.New(updater.Options{
		DataDir: w.DataDir, ConsoleDir: consoleDir, Version: "3.5.0", Commit: oldCommit, Docker: docker,
		Getenv: func(k string) string { return w.env[k] }, Keys: updater.Keys{}, Transport: w.gh,
		Executable: func() (string, error) { return w.binary, nil }, GOOS: "linux", GOARCH: "amd64", RenameAside: &aside,
		RunVersion: fakeVersion, FreeSpace: func(string) (int64, error) { return 1 << 40, nil },
	})
	return w
}

// supervise gives the console a restart coordinator that ends the process
// (counted) at once, as with NEODESK_SUPERVISOR.
func (w *world) supervise() {
	w.env["NEODESK_SUPERVISOR"] = "systemd"
	d := w.Server.Deps()
	d.Restart = restart.New(restart.Options{
		DataDir: w.DataDir, Exit: func() { *w.exits++ }, After: func(_ time.Duration, f func()) { f() },
	})
}

func data(t *testing.T, body string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("not JSON: %s", body)
	}
	return out
}

var routes = [][2]string{
	{"GET", "/api/settings/updates/server-info"}, {"POST", "/api/settings/updates/install-go"},
	{"GET", "/api/settings/updates/preflight"}, {"GET", "/api/settings/management/capabilities"},
	{"GET", "/api/settings/management/config"}, {"PUT", "/api/settings/management/config"},
	{"GET", "/api/settings/updates/channel"}, {"POST", "/api/settings/updates/channel"},
	{"GET", "/api/settings/updates/check"}, {"GET", "/api/settings/updates/last-result"},
	{"GET", "/api/settings/updates/install-status"}, {"GET", "/api/settings/updates/changes"},
	{"POST", "/api/settings/updates/install"}, {"GET", "/api/settings/updates/server-binary/status"},
	{"POST", "/api/settings/updates/server-binary/rebuild"}, {"GET", "/api/settings/updates/backups"},
	{"POST", "/api/settings/updates/restore"}, {"DELETE", "/api/settings/updates/backups/pre-update-2026"},
	{"POST", "/api/settings/updates/backups/prune"},
}

// Every route needs a session and server.config.
func TestGuards(t *testing.T) {
	w := newWorld(t, false)
	viewer, admin := w.As("viewer"), w.As("admin")
	for _, r := range routes {
		if res := w.SignedOut().Do(r[0], r[1], "{}"); res.Status != http.StatusUnauthorized {
			t.Errorf("%s %s signed out: %d", r[0], r[1], res.Status)
		}
		if res := viewer.Do(r[0], r[1], "{}"); res.Status != http.StatusForbidden {
			t.Errorf("%s %s as viewer: %d", r[0], r[1], res.Status)
		}
		if res := admin.Do(r[0], r[1], "{}"); res.Status == http.StatusUnauthorized || res.Status == http.StatusForbidden || res.Status == http.StatusNotFound {
			t.Errorf("%s %s as admin: %d %s", r[0], r[1], res.Status, res.Body)
		}
	}
	if *w.exits != 0 {
		t.Error("a refusal ended the process")
	}
}

func TestReadRoutes(t *testing.T) {
	w := newWorld(t, false)
	admin := w.As("admin")
	info := data(t, admin.Do("GET", "/api/settings/updates/server-info", "").Body)["data"].(map[string]any)
	if info["goAvailable"] != true || info["buildMethod"] != "release" || info["binaryPath"] != w.binary || info["canInstallGo"] != false || info["sourcePresent"] != false {
		t.Errorf("server-info %v", info)
	}
	if res := admin.Do("POST", "/api/settings/updates/install-go", "{}"); res.Status != http.StatusGone ||
		res.Body != `{"success":false,"error":"Updates install prebuilt releases; no Go toolchain is needed"}` {
		t.Errorf("install-go %d %s", res.Status, res.Body)
	}
	if res := admin.Do("GET", "/api/settings/updates/channel", ""); res.Body != `{"success":true,"data":{"channel":"stable","branch":"main","label":"Stable","owner":"neodeskapp","repo":"neodesk"}}` {
		t.Errorf("channel %s", res.Body)
	}
	if res := admin.Do("GET", "/api/settings/updates/install-status", ""); res.Body != `{"success":true,"data":{"inProgress":false,"marker":null,"localSHA":"`+oldCommit+`","lastResult":null}}` {
		t.Errorf("install-status %s", res.Body)
	}
	if res := admin.Do("GET", "/api/settings/updates/server-binary/status", ""); res.Body != `{"success":true,"data":{"stale":false}}` {
		t.Errorf("binary status %s", res.Body)
	}
	if res := admin.Do("GET", "/api/settings/updates/backups", ""); res.Body != `{"success":true,"data":[]}` {
		t.Errorf("backups %s", res.Body)
	}
	if res := admin.Do("GET", "/api/settings/updates/last-result", ""); res.Body != `{"success":true,"data":null}` {
		t.Errorf("last-result %s", res.Body)
	}
	caps := data(t, admin.Do("GET", "/api/settings/management/capabilities", "").Body)["data"].(map[string]any)
	if caps["success"] != true || caps["services"].([]any)[0] != "neodesk-server" || len(caps["checks"].([]any)) != 10 {
		t.Errorf("capabilities %v", caps)
	}
	pf := data(t, admin.Do("GET", "/api/settings/updates/preflight?sha=abc1234", "").Body)["data"].(map[string]any)
	if pf["ready"] != false || pf["remoteSHA"] != "abc1234" || pf["capabilities"] == nil ||
		!strings.Contains(fmt.Sprint(pf["issues"]), "Cannot reach GitHub API: GitHub API 404") {
		t.Errorf("preflight %v", pf)
	}
}

func TestChannelRoute(t *testing.T) {
	w := newWorld(t, false)
	admin := w.As("admin")
	for _, body := range []string{`{}`, `{"channel":1}`, `{"channel":"nightly"}`, `{"channel":{}}`, `{"channel":["stable","x"]}`, `{"channel":""}`} {
		if res := admin.Do("POST", "/api/settings/updates/channel", body); res.Status != 400 || res.Body != `{"success":false,"error":"Invalid channel. Use stable or development."}` {
			t.Errorf("%s: %d %s", body, res.Status, res.Body)
		}
	}
	env := filepath.Join(w.console, ".env")
	res := admin.Do("POST", "/api/settings/updates/channel", `{"channel":"development"}`)
	if res.Body != `{"success":true,"data":{"channel":"development","branch":"dev","label":"Development","previousBranch":"main","previousChannel":"stable"}}` {
		t.Errorf("switch %s", res.Body)
	}
	if text, _ := os.ReadFile(env); string(text) != "UPDATE_GITHUB_BRANCH=dev\n" {
		t.Errorf("env %q", text)
	}
	if res := admin.Do("POST", "/api/settings/updates/channel", `{"channel":["stable"]}`); !strings.Contains(res.Body, `"branch":"main"`) {
		t.Errorf("a list of one: %s", res.Body)
	}
	// The names every JavaScript object has are no channels (UPD2): they
	// are refused and nothing is written
	for _, name := range []string{"toString", "constructor", "__proto__", "valueOf", "hasOwnProperty", "toLocaleString"} {
		if res := admin.Do("POST", "/api/settings/updates/channel", `{"channel":"`+name+`"}`); res.Status != 400 || res.Body != `{"success":false,"error":"Invalid channel. Use stable or development."}` {
			t.Errorf("%s: %d %s", name, res.Status, res.Body)
		}
	}
	if text, _ := os.ReadFile(env); string(text) != "UPDATE_GITHUB_BRANCH=main\n" {
		t.Errorf("env %q", text)
	}
	if res := admin.Do("GET", "/api/settings/updates/channel", ""); !strings.Contains(res.Body, `"branch":"main"`) {
		t.Errorf("channel after the refused names: %s", res.Body)
	}
	docker := newWorld(t, true)
	if res := docker.As("admin").Do("POST", "/api/settings/updates/channel", `{"channel":"stable"}`); res.Status != 400 || !strings.Contains(res.Body, `"code":"DOCKER_IMAGE_CHANNEL"`) ||
		!strings.Contains(res.Body, `"error":"settings.updates_tab.channel_docker_note"`) {
		t.Errorf("docker %d %s", res.Status, res.Body)
	}
}

func TestGitHubFailures(t *testing.T) {
	w := newWorld(t, false)
	admin := w.As("admin")
	for _, q := range []string{"", "?sha=xyz", "?sha=abc", "?sha=" + strings.Repeat("a", 41)} {
		if res := admin.Do("GET", "/api/settings/updates/changes"+q, ""); res.Status != 400 || res.Body != `{"success":false,"error":"Valid SHA parameter required"}` {
			t.Errorf("changes%s: %d %s", q, res.Status, res.Body)
		}
	}
	if res := admin.Do("GET", "/api/settings/updates/check", ""); res.Status != 500 || res.Body != `{"success":false,"error":"Failed to check for updates: GitHub API 404: {\"message\":\"Not Found\"}"}` {
		t.Errorf("check %d %s", res.Status, res.Body)
	}
	w.gh.rateLimit = true
	if res := admin.Do("GET", "/api/settings/updates/changes?sha=abcdef1", ""); res.Status != 503 || !strings.Contains(res.Body, `"code":"GITHUB_RATE_LIMIT"`) ||
		!strings.Contains(res.Body, "Failed to get changed files: GitHub API rate limit exceeded") {
		t.Errorf("changes rate limit %d %s", res.Status, res.Body)
	}
	if res := admin.Do("POST", "/api/settings/updates/server-binary/rebuild", "{}"); res.Status != 500 || !strings.HasPrefix(res.Body, `{"success":false,"error":"no release of version 3.5.0 found`) ||
		!strings.Contains(res.Body, `"data":{"success":false`) {
		t.Errorf("rebuild %d %s", res.Status, res.Body)
	}
}

func TestInstallRoute(t *testing.T) {
	docker := newWorld(t, true)
	res := docker.As("admin").Do("POST", "/api/settings/updates/install", `{"remoteSHA":"`+newCommit+`"}`)
	if res.Status != 400 || !strings.HasPrefix(res.Body, `{"success":false,"error":"In-app updates are disabled in Docker image deployments. Run: docker compose pull && docker compose up -d","dockerImageMode":true,"dockerUpdate":{`) {
		t.Errorf("docker %d %s", res.Status, res.Body)
	}

	w := newWorld(t, false)
	admin := w.As("admin")
	for _, body := range []string{`{}`, `{"remoteSHA":"xyz"}`, `{"remoteSHA":123}`} {
		if res := admin.Do("POST", "/api/settings/updates/install", body); res.Status != 400 || res.Body != `{"success":false,"error":"Valid remoteSHA is required"}` {
			t.Errorf("%s: %d %s", body, res.Status, res.Body)
		}
	}
	if res := admin.Do("POST", "/api/settings/updates/install", `{"remoteSHA":"`+newCommit+`"}`); res.Status != 500 || !strings.HasPrefix(res.Body, `{"success":false,"error":"Update failed: GitHub API 404`) {
		t.Errorf("no release %d %s", res.Status, res.Body)
	}
	w.gh.release = true
	if res := admin.Do("POST", "/api/settings/updates/install", `{"remoteSHA":"abcdef1"}`); res.Status != 409 {
		t.Errorf("another commit %d %s", res.Status, res.Body)
	}

	// Without a supervisor: installed, the reason kept, no exit.
	res = admin.Do("POST", "/api/settings/updates/install", `{"remoteSHA":"`+newCommit[:7]+`","createBackup":false}`)
	got := data(t, res.Body)
	result := got["data"].(map[string]any)
	if res.Status != 200 || result["consoleRestartBlocked"] != updater.RestartBlocked || result["backupPath"] != nil || fmt.Sprint(result["applied"]) != "[neodesk-server 3.6.0]" {
		t.Fatalf("install %d %s", res.Status, res.Body)
	}
	if text, _ := os.ReadFile(w.binary); string(text) != string(binaryFor("3.6.0", newCommit)) {
		t.Error("not installed")
	}
	if *w.exits != 0 {
		t.Error("ended without a supervisor")
	}
	last := data(t, admin.Do("GET", "/api/settings/updates/last-result", "").Body)["data"].(map[string]any)
	if last["consoleRestartBlocked"] != updater.RestartBlocked || last["sha"] != newCommit[:7] {
		t.Errorf("last result %v", last)
	}
	if rows := strings.Join(w.AuditRows(), "\n"); !strings.Contains(rows, "system_update|Updated to 2222222 (1 applied, 0 failed)") {
		t.Errorf("audit %s", rows)
	}
}

func TestInstallEndsTheProcessUnderASupervisor(t *testing.T) {
	w := newWorld(t, false)
	w.supervise()
	w.gh.release = true
	admin := w.As("admin")
	res := admin.Do("POST", "/api/settings/updates/install", `{"remoteSHA":"`+newCommit+`"}`)
	if res.Status != 200 || strings.Contains(res.Body, "consoleRestartBlocked") {
		t.Fatalf("%d %s", res.Status, res.Body)
	}
	if *w.exits != 1 {
		t.Errorf("exits %d", *w.exits)
	}
	backups := data(t, admin.Do("GET", "/api/settings/updates/backups", "").Body)["data"].([]any)
	if len(backups) != 1 {
		t.Fatalf("backups %v", backups)
	}
	name := backups[0].(map[string]any)["name"].(string)

	// The restore of that backup puts 3.5.0 back and ends the process.
	res = admin.Do("POST", "/api/settings/updates/restore", `{"backupName":"`+name+`"}`)
	if res.Status != 200 || !strings.Contains(res.Body, `"version":"3.5.0"`) || *w.exits != 2 {
		t.Errorf("restore %d %s exits %d", res.Status, res.Body, *w.exits)
	}
	if text, _ := os.ReadFile(w.binary); string(text) != string(binaryFor("3.5.0", oldCommit)) {
		t.Error("not restored")
	}
	if rows := strings.Join(w.AuditRows(), "\n"); !strings.Contains(rows, "system_restore|Restored from backup: "+name+" (v3.5.0, 1 files)") {
		t.Errorf("audit %s", rows)
	}
}

func TestBackupRoutes(t *testing.T) {
	w := newWorld(t, false)
	admin := w.As("admin")
	node := filepath.Join(w.DataDir, "backups", "pre-update-2026-01-01T10-00-00")
	_ = os.MkdirAll(filepath.Join(node, "console"), 0o755)
	_ = os.WriteFile(filepath.Join(node, "manifest.json"), []byte(`{"version":"3.5.1","sha":"abcdef12","timestamp":"2026-01-01T10:00:00.000Z","filesBackedUp":1,"files":["console/server.js"]}`), 0o644)
	_ = os.WriteFile(filepath.Join(node, "console", "server.js"), []byte("x"), 0o644)

	for body, want := range map[string]string{
		`{}`:               `{"success":false,"error":"backupName is required"}`,
		`{"backupName":5}`: `{"success":false,"error":"backupName is required"}`,
		`{"backupName":"pre-update-2026-01-01T10-00-00"}`: `{"success":false,"error":"This backup holds files of the old Node console and cannot be restored by this version"}`,
		`{"backupName":"pre-update-2030-01-01T10-00-00"}`: `{"success":false,"error":"Backup not found"}`,
		`{"backupName":"../../etc"}`:                      `{"success":false,"error":"Invalid backup name"}`,
	} {
		// A name that is no backup's is the client's error, as for a
		// delete (UPD4)
		if res := admin.Do("POST", "/api/settings/updates/restore", body); res.Status != 400 || res.Body != want {
			t.Errorf("restore %s: %d %s", body, res.Status, res.Body)
		}
	}
	// A backup whose files are broken too
	broken := filepath.Join(w.DataDir, "backups", "pre-update-2026-02-01T10-00-00")
	_ = os.MkdirAll(broken, 0o755)
	if res := admin.Do("POST", "/api/settings/updates/restore", `{"backupName":"pre-update-2026-02-01T10-00-00"}`); res.Status != 400 ||
		res.Body != `{"success":false,"error":"Invalid backup — missing manifest"}` {
		t.Errorf("restore without a manifest: %d %s", res.Status, res.Body)
	}
	_ = os.RemoveAll(broken)
	list := admin.Do("GET", "/api/settings/updates/backups", "").Body
	if !strings.Contains(list, `"name":"pre-update-2026-01-01T10-00-00"`) || !strings.Contains(list, `"version":"3.5.1","sha":"abcdef1"`) {
		t.Errorf("list %s", list)
	}
	for name, want := range map[string]string{
		"pre-update-x": "Invalid backup name", "pre-update-2030-01-01T10-00-00": "Backup not found", "..": "",
	} {
		res := admin.Do("DELETE", "/api/settings/updates/backups/"+name, "")
		if want != "" && (res.Status != 400 || res.Body != `{"success":false,"error":"`+want+`"}`) {
			t.Errorf("delete %s: %d %s", name, res.Status, res.Body)
		}
	}
	for _, body := range []string{`{}`, `{"keep":0}`, `{"keep":"abc"}`, `{"keep":-1}`, `{"keep":true}`} {
		if res := admin.Do("POST", "/api/settings/updates/backups/prune", body); res.Status != 400 || res.Body != `{"success":false,"error":"keep must be a positive integer"}` {
			t.Errorf("prune %s: %d %s", body, res.Status, res.Body)
		}
	}
	if res := admin.Do("POST", "/api/settings/updates/backups/prune", `{"keep":"3x"}`); res.Body != `{"success":true,"data":{"kept":3,"deleted":[]}}` {
		t.Errorf("prune 3x %s", res.Body)
	}
	if res := admin.Do("DELETE", "/api/settings/updates/backups/pre-update-2026-01-01T10-00-00", ""); res.Body != `{"success":true,"data":{"deleted":"pre-update-2026-01-01T10-00-00"}}` {
		t.Errorf("delete %s", res.Body)
	}
	if rows := strings.Join(w.AuditRows(), "\n"); !strings.Contains(rows, "backup_deleted|Deleted update backup: pre-update-2026-01-01T10-00-00") {
		t.Errorf("audit %s", rows)
	}
}

func TestManagementConfig(t *testing.T) {
	w := newWorld(t, false)
	admin := w.As("admin")
	env := filepath.Join(w.console, ".env")
	_ = os.WriteFile(env, []byte("PORT=5000\nSESSION_SECRET=s3cret\nNOT_ALLOWED=1\n"), 0o600)

	got := data(t, admin.Do("GET", "/api/settings/management/config", "").Body)["data"].(map[string]any)
	if got["envPath"] != env || fmt.Sprint(got["values"]) != "map[PORT:5000 SESSION_SECRET:********]" || len(got["keys"].([]any)) < 50 {
		t.Errorf("config %v", got)
	}
	for body, want := range map[string]struct {
		status int
		text   string
	}{
		`{}`:                                {400, `{"success":false,"error":"Configuration changes must be an object"}`},
		`{"changes":[]}`:                    {400, `{"success":false,"error":"Configuration changes must be an object"}`},
		`{"changes":"PORT=1"}`:              {400, `{"success":false,"error":"Configuration changes must be an object"}`},
		`{"changes":{}}`:                    {400, `{"success":false,"error":"No configuration changes supplied"}`},
		`{"changes":{"NOPE":"1"}}`:          {400, `{"success":false,"error":"Environment key is not allowlisted: NOPE"}`},
		`{"changes":{"PORT":"1","10":"x"}}`: {400, `{"success":false,"error":"Environment key is not allowlisted: 10"}`},
		`{"changes":{"20":"y","10":"x"}}`:   {400, `{"success":false,"error":"Environment key is not allowlisted: 10"}`},
		`{"changes":{"PORT":"70000"}}`:      {400, `{"success":false,"error":"Invalid port for PORT"}`},
		`{"changes":{"PORT":null}}`:         {400, `{"success":false,"error":"Invalid value for PORT"}`},
	} {
		if res := admin.Do("PUT", "/api/settings/management/config", body); res.Status != want.status || res.Body != want.text {
			t.Errorf("%s: %d %s", body, res.Status, res.Body)
		}
	}
	// The secrets and the release keys are set on the host only (L410)
	for _, key := range []string{"SESSION_SECRET", "UPDATE_MANIFEST_PUBLIC_KEY", "UPDATE_REQUIRE_SIGNED_MANIFEST"} {
		refused := admin.Do("PUT", "/api/settings/management/config", `{"changes":{"`+key+`":"x","PORT":5001},"dryRun":"true"}`)
		if want := `{"success":false,"error":"` + key + ` is set on the server's host only, not in the console"}`; refused.Status != 400 || refused.Body != want {
			t.Errorf("%s: %d %s, want 400 %s", key, refused.Status, refused.Body, want)
		}
	}
	dry := admin.Do("PUT", "/api/settings/management/config", `{"changes":{"SMTP_PASS":"x","PORT":5001},"dryRun":"true"}`)
	if !strings.HasPrefix(dry.Body, `{"success":true,"data":{"dryRun":true,"changes":{"SMTP_PASS":"********","PORT":"5001"},"capabilities":{`) {
		t.Errorf("dry run %s", dry.Body)
	}
	if text, _ := os.ReadFile(env); !strings.Contains(string(text), "PORT=5000") {
		t.Error("a dry run wrote")
	}

	saved := admin.Do("PUT", "/api/settings/management/config", `{"changes":{"PORT":"5443"}}`)
	out := data(t, saved.Body)
	pending := out["data"].(map[string]any)["pendingRestart"].(map[string]any)
	if saved.Status != 200 || out["message"] != "Configuration saved; service restart confirmation is required" || pending["phase"] != "pending" ||
		fmt.Sprint(out["data"].(map[string]any)["values"]) != "map[PORT:5443]" {
		t.Fatalf("save %d %s", saved.Status, saved.Body)
	}
	if text, _ := os.ReadFile(env); !strings.Contains(string(text), "PORT=5443") {
		t.Fatal("not written")
	}
	// Cancel undoes it through the env-config rollback.
	cancel := admin.Do("POST", "/api/settings/restart/cancel", `{"id":"`+pending["id"].(string)+`"}`)
	if cancel.Status != 200 {
		t.Fatalf("cancel %d %s", cancel.Status, cancel.Body)
	}
	if text, _ := os.ReadFile(env); !strings.Contains(string(text), "PORT=5000") {
		t.Errorf("not undone: %q", text)
	}
	if rows := strings.Join(w.AuditRows(), "\n"); !strings.Contains(rows, "managed_config_saved|Saved allowlisted environment keys: PORT") {
		t.Errorf("audit %s", rows)
	}
}

// Behind the panel guard each route has its own: a session, then
// server.config. The handlers do not run for a person without them.
func TestPermissionGuards(t *testing.T) {
	d := &kit.Deps{Router: kit.NewRouter(), Log: kit.NewLogger("error", true)}
	settingsupdates.Register(d)
	viewer := &access.Context{UserID: 6, HasUser: true, Role: "viewer", Loaded: true,
		Permissions: access.PermissionSet{"settings.view": {}, "audit.view": {}}}
	for _, route := range routes {
		for _, who := range []*access.Context{nil, viewer} {
			r := httptest.NewRequest(route[0], route[1], strings.NewReader("{}"))
			r = kit.Begin(r, httpx.TrustProxy{}, nil)
			if who != nil {
				kit.SetAccess(r, who)
			}
			rec := httptest.NewRecorder()
			func() {
				defer func() {
					if recover() != nil {
						t.Errorf("%s %s: the handler ran", route[0], route[1])
					}
				}()
				if !d.Router.Serve(rec, r) {
					t.Errorf("%s %s: no route", route[0], route[1])
				}
			}()
			want, text := http.StatusUnauthorized, `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`
			if who != nil {
				want, text = http.StatusForbidden, "Permission denied: server.config"
			}
			if rec.Code != want || !strings.Contains(rec.Body.String(), text) {
				t.Errorf("%s %s as %v: %d %s", route[0], route[1], who != nil, rec.Code, rec.Body)
			}
		}
	}
}

// A save whose restart transaction is refused (another operator's is
// pending) puts the env file back and answers 500 with the refusal.
func TestManagementConfigUndoneWhenTheTransactionIsRefused(t *testing.T) {
	w := newWorld(t, false)
	env := filepath.Join(w.console, ".env")
	_ = os.WriteFile(env, []byte("PORT=5000\n"), 0o600)
	if res := w.As("super_admin").Do("PUT", "/api/settings/management/config", `{"changes":{"LOG_LEVEL":"debug"}}`); res.Status != 200 {
		t.Fatalf("first save %d %s", res.Status, res.Body)
	}
	before, _ := os.ReadFile(env)
	// The refusal keeps the status the restart transaction gave it (UPD6)
	res := w.As("admin").Do("PUT", "/api/settings/management/config", `{"changes":{"PORT":"5443"}}`)
	if res.Status != 409 || res.Body != `{"success":false,"error":"restart_transaction_owned"}` {
		t.Errorf("second save %d %s", res.Status, res.Body)
	}
	if after, _ := os.ReadFile(env); string(after) != string(before) {
		t.Errorf("the env file was not put back: %q", after)
	}
}
