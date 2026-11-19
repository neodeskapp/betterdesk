package managedconfig

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

type fixture struct {
	root, console, server, data string
	cfg                         *Config
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	f := fixture{root: root, console: filepath.Join(root, "console"), server: filepath.Join(root, "server"), data: filepath.Join(root, "data")}
	for _, dir := range []string{f.console, f.server, f.data} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(f.console, ".env"), []byte("PORT=5000\nSESSION_SECRET=old-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"BETTERDESK_PATH": f.server, "DATA_DIR": f.data}
	f.cfg = New(Options{
		ConsoleDir: f.console, Getenv: func(k string) string { return env[k] }, Allowed: []string{"PORT", "SESSION_SECRET", "DB_PATH", "LOG_LEVEL", "TRUST_PROXY"},
		Now: func() time.Time { return time.Date(2026, 11, 18, 10, 20, 30, 456e6, time.UTC) }, PID: 9,
	})
	return f
}

func changes(pairs ...any) []Change {
	var out []Change
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, Change{Key: pairs[i].(string), Value: pairs[i+1]})
	}
	return out
}

func validate(c *Config, ch []Change) ([]Setting, string) {
	s, err := c.Validate(ch, true)
	if err != nil {
		return nil, err.Error()
	}
	return s, ""
}

// The Jest tests of managementCapabilities.js and the old messages.
func TestValidate(t *testing.T) {
	f := newFixture(t)
	if s, msg := validate(f.cfg, changes("PORT", "5443")); msg != "" || s[0] != (Setting{"PORT", "5443"}) {
		t.Errorf("port: %v %s", s, msg)
	}
	for _, tc := range []struct {
		changes []Change
		want    string
	}{
		{changes("UNKNOWN_SETTING", "x"), "Environment key is not allowlisted: UNKNOWN_SETTING"},
		{changes("", "x"), "Environment key is not allowlisted: (empty)"},
		{changes("PORT", "65536"), "Invalid port for PORT"},
		{changes("PORT", "0"), "Invalid port for PORT"},
		{changes("PORT", "5000\nExecStart=evil"), "Invalid control character in PORT"},
		{changes("PORT", "50\x00"), "Invalid control character in PORT"},
		{changes("DB_PATH", "/etc/passwd"), "Path is outside BetterDesk roots for DB_PATH"},
		{changes("DB_PATH", "relative/db"), "Path must be absolute for DB_PATH"},
		{changes("DB_PATH", filepath.Join(f.server, "..", "x")), "Path is outside BetterDesk roots for DB_PATH"},
		{changes("DB_PATH", f.server+"-other/db"), "Path is outside BetterDesk roots for DB_PATH"},
		{changes("LOG_LEVEL", "verbose"), "Invalid value for LOG_LEVEL"},
		{changes("TRUST_PROXY", "maybe"), "Invalid boolean for TRUST_PROXY"},
		{changes("PORT", nil), "Invalid value for PORT"},
		{changes("PORT", []any{"1"}), "Invalid value for PORT"},
		{nil, "No configuration changes supplied"},
		{changes("PORT", "1", "NOPE", "x"), "Environment key is not allowlisted: NOPE"},
	} {
		if _, msg := validate(f.cfg, tc.changes); msg != tc.want {
			t.Errorf("%v: %q, want %q", tc.changes, msg, tc.want)
		}
	}
	if _, err := f.cfg.Validate(nil, false); err == nil || err.Error() != "Configuration changes must be an object" {
		t.Errorf("not an object: %v", err)
	}
	ok, msg := validate(f.cfg, changes("DB_PATH", filepath.Join(f.server, "db_v2.sqlite3"), "PORT", 5000.0, "TRUST_PROXY", true, "LOG_LEVEL", "debug"))
	if msg != "" || ok[1].Value != "5000" || ok[2].Value != "true" {
		t.Errorf("%v %s", ok, msg)
	}
	if _, msg := validate(f.cfg, changes("DB_PATH", "/etc/letsencrypt/live/x/privkey.pem")); msg != "" && runtime.GOOS != "windows" {
		t.Errorf("letsencrypt: %s", msg)
	}
}

func TestApplyAndRestore(t *testing.T) {
	f := newFixture(t)
	env := filepath.Join(f.console, ".env")
	if err := os.WriteFile(env, []byte("# comment\r\nPORT=5000\r\n  PORT=5001\nSESSION_SECRET=old-secret\n\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	applied, err := f.cfg.Apply([]Setting{{"PORT", "5443"}, {"SESSION_SECRET", "new"}, {"LOG_LEVEL", "info"}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(env)
	// Blank lines before the appended key stay, as the old write kept them.
	if string(data) != "# comment\nPORT=5443\nPORT=5443\nSESSION_SECRET=new\n\n\n\nLOG_LEVEL=info\n" {
		t.Errorf("env file %q", data)
	}
	if applied.BackupPath != env+".bak.2026-11-18T10-20-30-456Z" || applied.EnvPath != env {
		t.Errorf("backup %s", applied.BackupPath)
	}
	if strings.Join(applied.Changed, ",") != "PORT,SESSION_SECRET,LOG_LEVEL" || applied.Values[1].Value != "********" || applied.Values[0].Value != "5443" {
		t.Errorf("%+v", applied)
	}
	if info, _ := os.Stat(env); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mode %o", info.Mode().Perm())
	}
	if _, err := os.Stat(env + ".tmp-9"); err == nil {
		t.Error("the temporary file stays")
	}
	if err := f.cfg.RestoreBackup(applied.BackupPath); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(env); !strings.Contains(string(data), "PORT=5000") || !strings.Contains(string(data), "\r\n") {
		t.Errorf("restored %q", data)
	}
	for _, bad := range []string{"", "/etc/passwd", env + ".bak.", env + ".bak./../x", env + ".bak.missing", filepath.Join(f.console, "x", "..", ".env.bak.2026")} {
		if err := f.cfg.RestoreBackup(bad); err == nil || err.Error() != "Invalid environment backup path" {
			t.Errorf("restore %q: %v", bad, err)
		}
	}
}

func TestReadAndMask(t *testing.T) {
	f := newFixture(t)
	env := filepath.Join(f.console, ".env")
	_ = os.WriteFile(env, []byte("PORT=1\n  LOG_LEVEL=debug\n#PORT=2\nlower=x\nPORT=3\nEMPTY=\nA-B=1\n"), 0o600)
	values, err := ReadEnvFile(env)
	if err != nil || len(values) != 3 || values["PORT"] != "3" || values["LOG_LEVEL"] != "debug" || values["EMPTY"] != "" {
		t.Errorf("%v %v", values, err)
	}
	if values, err := ReadEnvFile(filepath.Join(f.root, "missing")); err != nil || len(values) != 0 {
		t.Errorf("missing: %v %v", values, err)
	}
	for key, secret := range map[string]bool{"SESSION_SECRET": true, "SMTP_PASS": true, "UPDATE_GITHUB_TOKEN": true, "database_url": true, "PORT": false, "BETTERDESK_API_KEY": true} {
		if (Mask(key, "v") == "********") != secret {
			t.Errorf("%s masked %v", key, !secret)
		}
	}
}

func TestPaths(t *testing.T) {
	cfg := New(Options{ConsoleDir: "/opt/BetterDeskConsole", GOOS: "linux"})
	p := cfg.Paths()
	if runtime.GOOS != "windows" && (p.ServerPath != "/opt/betterdesk" || p.DataPath != "/opt/BetterDeskConsole/data" ||
		p.EnvPath != "/opt/BetterDeskConsole/.env" || p.BackupPath != "/opt/BetterDeskConsole/data/backups") {
		t.Errorf("%+v", p)
	}
	env := map[string]string{"BETTERDESK_CONSOLE_PATH": "/srv/c", "RUSTDESK_PATH": "/srv/s"}
	cfg = New(Options{ConsoleDir: "/x", EnvFile: "/etc/betterdesk.env", Getenv: func(k string) string { return env[k] }})
	if p := cfg.Paths(); runtime.GOOS != "windows" && (p.ConsolePath != "/srv/c" || p.ServerPath != "/srv/s" || p.EnvPath != "/etc/betterdesk.env") {
		t.Errorf("%+v", p)
	}
}

// The compiled allowlist is every key the template names, as the old
// console read it (parseEnvKeys). Run "go generate" after the template
// changed.
func TestAllowlistMatchesTheTemplate(t *testing.T) {
	data, err := os.ReadFile("env.example")
	if err != nil {
		t.Fatal(err)
	}
	line := regexp.MustCompile(`^\s*#?\s*([A-Z][A-Z0-9_]*)\s*=`)
	seen := map[string]bool{}
	var keys []string
	for _, l := range regexp.MustCompile(`\r?\n`).Split(string(data), -1) {
		if m := line.FindStringSubmatch(l); m != nil && !seen[m[1]] {
			seen[m[1]] = true
			keys = append(keys, m[1])
		}
	}
	sort.Strings(keys)
	if got := New(Options{}).AllowedKeys(); strings.Join(got, ",") != strings.Join(keys, ",") {
		t.Errorf("allowlist differs from the template; run go generate\ncompiled %v\ntemplate %v", got, keys)
	}
	if len(keys) < 50 {
		t.Errorf("only %d keys", len(keys))
	}
}
