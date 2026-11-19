package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/updater"
)

type managementCase struct {
	Name        string            `json:"name"`
	Args        []string          `json:"args"`
	EnvContent  *string           `json:"envContent"`
	WithExample bool              `json:"withExample"`
	Dirs        []string          `json:"dirs"`
	Env         map[string]string `json:"env"`
	Status      int               `json:"status"`
	Stdout      string            `json:"stdout"`
	Stderr      string            `json:"stderr"`
	EnvAfter    *fileState        `json:"envAfter"`
	Backups     []*fileState      `json:"backups"`
}

// managementDifferences are the recorded runs Go answers differently, with
// the reason.
var managementDifferences = map[string]string{
	// The allowlist is compiled in (spec 4.9); the script read it from the
	// console folder's .env.example and refused every key without one
	"set/noexample":    "the allowlist is built in",
	"set/noconsoledir": "the allowlist is built in (the folder BETTERDESK_CONSOLE_PATH names has no .env.example)",
}

// managementSetDifferences are the "set" arguments (sets in the oracle, by
// index) whose answer differs on every env file.
var managementSetDifferences = map[string]string{
	// console/managedconfig checks STORE_ADMIN_CREDENTIALS as a switch;
	// the spec's managementCapabilities.js stopped doing so in upstream's
	// 0429c408 (BUGS BN2)
	"16": "managedconfig refuses STORE_ADMIN_CREDENTIALS=maybe, Node takes it (BUGS BN2)",
}

var backupStamp = regexp.MustCompile(`\.bak\.\d{4}-\d\d-\d\dT\d\d-\d\d-\d\d-\d{3}Z`)

// managementSetup lays a case out as the oracle did and answers the root
// and the environment the installers set.
func managementSetup(t *testing.T, c managementCase) (string, map[string]string) {
	t.Helper()
	root := t.TempDir()
	consoleDir := filepath.Join(root, "console")
	if err := os.MkdirAll(consoleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if c.EnvContent != nil {
		if err := os.WriteFile(filepath.Join(consoleDir, ".env"), []byte(*c.EnvContent), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if c.WithExample {
		if err := os.WriteFile(filepath.Join(consoleDir, ".env.example"), []byte("PORT=5000\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range c.Dirs {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The console's folder comes from BETTERDESK_CONSOLE_PATH alone, as in
	// the script: CONSOLE_DIR names another one
	env := map[string]string{
		"BETTERDESK_CONSOLE_PATH": consoleDir, "BETTERDESK_PATH": filepath.Join(root, "server"),
		"DATA_DIR": filepath.Join(root, "data"), "CONSOLE_DIR": filepath.Join(root, "elsewhere"),
	}
	for k, v := range c.Env {
		env[k] = strings.ReplaceAll(v, "{ROOT}", root)
	}
	return root, env
}

// Every recorded "set" of management-cli.js: the same exit status, the
// same JSON (the backup's time aside), the same message for a refused
// change, the same env file and backup, byte for byte and with their mode.
func TestConfigSetLikeNode(t *testing.T) {
	var cases []managementCase
	readVectors(t, "managementcli", &cases)
	replayed := 0
	for _, c := range cases {
		if strings.HasPrefix(c.Name, "check/") {
			continue
		}
		if reason, ok := managementDifferences[c.Name]; ok {
			t.Logf("%s differs: %s", c.Name, reason)
			continue
		}
		if parts := strings.Split(c.Name, "/"); len(parts) == 3 && managementSetDifferences[parts[2]] != "" {
			continue
		}
		root, env := managementSetup(t, c)
		var args []string
		for _, a := range c.Args {
			args = append(args, strings.ReplaceAll(a, "{ROOT}", root))
		}
		vars, out, errOut := testEnv(env)
		vars.Now = time.Now
		status := Config(args, vars)
		replayed++
		gotOut := backupStamp.ReplaceAllString(strings.ReplaceAll(out.String(), root, "{ROOT}"), ".bak.{STAMP}")
		if status != c.Status {
			t.Errorf("%s: exit %d, Node %d (%q)", c.Name, status, c.Status, errOut.String())
		}
		if strings.HasPrefix(c.Name, "usage/") {
			if !strings.HasPrefix(out.String(), "Usage:") {
				t.Errorf("%s: stdout %q", c.Name, out.String())
			}
			continue
		}
		if gotOut != c.Stdout {
			t.Errorf("%s: stdout\n got %q\nwant %q", c.Name, gotOut, c.Stdout)
		}
		if got := strings.ReplaceAll(errOut.String(), root, "{ROOT}"); got != c.Stderr {
			t.Errorf("%s: stderr\n got %q\nwant %q", c.Name, got, c.Stderr)
		}
		consoleDir := filepath.Join(root, "console")
		compareFiles(t, c.Name, consoleDir, map[string]*fileState{".env": c.EnvAfter}, root)
		entries, _ := os.ReadDir(consoleDir)
		var backups []string
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".env.bak.") {
				backups = append(backups, e.Name())
			}
		}
		if len(backups) != len(c.Backups) {
			t.Errorf("%s: %d backups, Node %d", c.Name, len(backups), len(c.Backups))
			continue
		}
		for i, name := range backups {
			compareFiles(t, c.Name, consoleDir, map[string]*fileState{name: c.Backups[i]}, root)
		}
	}
	if replayed < 300 {
		t.Fatalf("only %d runs replayed", replayed)
	}
}

// keyPaths lists the object keys of a JSON text in order, as paths.
func keyPaths(t *testing.T, text string) []string {
	t.Helper()
	var paths []string
	dec := json.NewDecoder(strings.NewReader(text))
	var read func(prefix string) error
	read = func(prefix string) error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tok {
		case json.Delim('{'):
			for dec.More() {
				key, _ := dec.Token()
				paths = append(paths, prefix+"."+key.(string))
				if err := read(prefix + "." + key.(string)); err != nil {
					return err
				}
			}
			_, err = dec.Token()
		case json.Delim('['):
			for i := 0; dec.More(); i++ {
				if err := read(prefix + "[]"); err != nil {
					return err
				}
			}
			_, err = dec.Token()
		}
		return err
	}
	if err := read(""); err != nil {
		t.Fatalf("%v in %s", err, text)
	}
	return paths
}

// "check" reports the same folders, paths and checks as the script, in
// the same JSON layout. The restart and update checks and the service
// names are those of the server binary (DECISIONS A29): the restart is
// the process's exit under a supervisor, an update is a release download.
func TestConfigCheckLikeNode(t *testing.T) {
	var cases []managementCase
	readVectors(t, "managementcli", &cases)
	replayed := 0
	for _, c := range cases {
		if !strings.HasPrefix(c.Name, "check/") {
			continue
		}
		root, env := managementSetup(t, c)
		vars, out, _ := testEnv(env)
		Config([]string{"check"}, vars)
		replayed++
		var got, want updater.Capabilities
		gotText := strings.ReplaceAll(out.String(), root, "{ROOT}")
		if err := json.Unmarshal([]byte(gotText), &got); err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if err := json.Unmarshal([]byte(c.Stdout), &want); err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if got.Paths != want.Paths || got.Platform != want.Platform || got.Docker != want.Docker || got.Success != want.Success {
			t.Errorf("%s: %+v\nNode %+v", c.Name, got, want)
		}
		byID := func(list []updater.Check) map[string]updater.Check {
			m := map[string]updater.Check{}
			for _, check := range list {
				m[check.ID] = check
			}
			return m
		}
		gotChecks, wantChecks := byID(got.Checks), byID(want.Checks)
		for id, w := range wantChecks {
			// The allowlist is built in: config-schema is ready without a template
			if id == "restart" || id == "update" || (id == "config-schema" && !c.WithExample) {
				continue
			}
			g := gotChecks[id]
			gj, _ := json.Marshal(g)
			wj, _ := json.Marshal(w)
			if string(gj) != string(wj) {
				t.Errorf("%s: check %s %s, Node %s", c.Name, id, gj, wj)
			}
		}
		groups := map[string][2]updater.Group{
			"permissions": {got.Groups.Permissions, want.Groups.Permissions},
			"backup":      {got.Groups.Backup, want.Groups.Backup}, "health": {got.Groups.Health, want.Groups.Health},
		}
		if c.WithExample {
			groups["config"] = [2]updater.Group{got.Groups.Config, want.Groups.Config}
		}
		for name, pair := range groups {
			gj, _ := json.Marshal(pair[0])
			wj, _ := json.Marshal(pair[1])
			if string(gj) != string(wj) {
				t.Errorf("%s: group %s %s, Node %s", c.Name, name, gj, wj)
			}
		}
		// Without a template the script's config-schema check had an error
		// (one key more); the layout is compared where both have one
		if c.WithExample && strings.Join(keyPaths(t, gotText), " ") != strings.Join(keyPaths(t, c.Stdout), " ") {
			t.Errorf("%s: JSON layout\n got %v\nwant %v", c.Name, keyPaths(t, gotText), keyPaths(t, c.Stdout))
		}
		if !strings.HasSuffix(out.String(), "}\n") || !strings.HasPrefix(out.String(), "{\n  \"success\": true,\n  \"platform\": ") {
			t.Errorf("%s: not JSON.stringify(report, null, 2): %q", c.Name, out.String()[:min(60, out.Len())])
		}
	}
	if replayed != 32 {
		t.Fatalf("%d check runs replayed", replayed)
	}
}

// check exits with 0 when every group is ready, else with 1.
func TestConfigCheckExitStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the folders' rights are those of the test user")
	}
	root := t.TempDir()
	for _, dir := range []string{"console", "server", "data/backups"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	base := map[string]string{
		"BETTERDESK_CONSOLE_PATH": filepath.Join(root, "console"), "BETTERDESK_PATH": filepath.Join(root, "server"),
		"DATA_DIR": filepath.Join(root, "data"), "CONSOLE_DIR": filepath.Join(root, "console"),
	}
	run := func(extra map[string]string) (int, updater.Capabilities) {
		vars := map[string]string{}
		for k, v := range base {
			vars[k] = v
		}
		for k, v := range extra {
			vars[k] = v
		}
		env, out, _ := testEnv(vars)
		status := Config([]string{"check"}, env)
		var report updater.Capabilities
		if err := json.Unmarshal(out.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		return status, report
	}
	status, report := run(map[string]string{"BETTERDESK_SUPERVISOR": "systemd"})
	ready := report.Groups.Update.Ready && report.Groups.Config.Ready && report.Groups.Restart.Ready &&
		report.Groups.Permissions.Ready && report.Groups.Backup.Ready && report.Groups.Health.Ready
	if want := map[bool]int{true: 0, false: 1}[ready]; status != want {
		t.Errorf("all ready %v: exit %d", ready, status)
	}
	if !report.Groups.Restart.Ready {
		t.Error("a supervisor gives no restart")
	}
	status, report = run(nil)
	if report.Groups.Restart.Ready || status != 1 {
		t.Errorf("without a supervisor: restart %v, exit %d", report.Groups.Restart.Ready, status)
	}
}

func TestConfigUsage(t *testing.T) {
	for args, want := range map[string]int{"": 0, "--help": 0, "-h": 0, "bogus": 2, "get": 2} {
		env, out, _ := testEnv(nil)
		var list []string
		if args != "" {
			list = []string{args}
		}
		if status := Config(list, env); status != want || !bytes.HasPrefix(out.Bytes(), []byte("Usage:")) {
			t.Errorf("%q: %d %q", args, status, out.String())
		}
	}
}
