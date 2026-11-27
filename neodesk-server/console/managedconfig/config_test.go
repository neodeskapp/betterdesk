package managedconfig

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
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
	env := map[string]string{"NEODESK_PATH": f.server, "DATA_DIR": f.data}
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
		{changes("DB_PATH", "/etc/passwd"), "Path is outside NeoDesk roots for DB_PATH"},
		{changes("DB_PATH", "relative/db"), "Path must be absolute for DB_PATH"},
		{changes("DB_PATH", filepath.Join(f.server, "..", "x")), "Path is outside NeoDesk roots for DB_PATH"},
		{changes("DB_PATH", f.server+"-other/db"), "Path is outside NeoDesk roots for DB_PATH"},
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
	// STORE_ADMIN_CREDENTIALS is no switch the panel manages (upstream
	// 0429c408, BN2): any value passes the general checks
	compiled := New(Options{ConsoleDir: f.console, Getenv: func(string) string { return "" }})
	for _, value := range []string{"maybe", "true", "0", "never"} {
		if s, msg := validate(compiled, changes("STORE_ADMIN_CREDENTIALS", value)); msg != "" || s[0].Value != value {
			t.Errorf("STORE_ADMIN_CREDENTIALS=%s: %v %q", value, s, msg)
		}
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
	for key, secret := range map[string]bool{"SESSION_SECRET": true, "SMTP_PASS": true, "UPDATE_GITHUB_TOKEN": true, "database_url": true, "PORT": false, "NEODESK_API_KEY": true} {
		if (Mask(key, "v") == "********") != secret {
			t.Errorf("%s masked %v", key, !secret)
		}
	}
}

// Every allowlisted key is sorted into secret or shown, and every secret
// one comes out of Mask as "********" (RV15). A key added to the template
// fails here until someone decides which list it belongs in.
func TestEverySecretKeyOfTheAllowlistIsMasked(t *testing.T) {
	secret := setOf("NEODESK_CUSTOM_CLIENT_SIGNING_SEED", "NEODESK_GITHUB_TOKEN", "DATABASE_URL",
		"DEFAULT_ADMIN_PASSWORD", "ORG_PEER_VAULT_KEY", "PANEL_TRUST_SECRET", "REMOTE_TARGET_VAULT_KEY",
		"SESSION_SECRET", "SMTP_PASS", "UPDATE_GITHUB_TOKEN")
	shown := setOf("AGENT_ARTIFACT_DIR", "AGENT_BUILD_COOLDOWN_MS", "AGENT_BUILD_POLL_MS", "AGENT_BUILD_WORKER",
		"ALLOW_SHARED_NAT_INITIATOR", "API_ALLOW_FROM", "API_ENABLED", "API_HOST", "API_KEY_PATH", "API_PORT", "API_TOKEN_EXPIRY_DAYS",
		"BACKEND_PANEL_GROUPING", "ND_REGISTER_ALLOW_OPEN", "ND_REGISTER_RATE_LIMIT_MAX", "NEODESK_API_URL",
		"NEODESK_CLIENT_ALLOWED_REPOS", "NEODESK_CLIENT_BUILD_COMMAND", "NEODESK_CLIENT_BUILD_MIN_FREE_MB",
		"NEODESK_CLIENT_BUILD_TIMEOUT_MS", "NEODESK_CLIENT_BUILD_WORKFLOW", "NEODESK_CLIENT_GENERATOR_MODE",
		"NEODESK_CLIENT_GITHUB_ACTIONS", "NEODESK_CLIENT_PROVIDER_TARGETS_FILE",
		"NEODESK_CLIENT_PROVIDER_TARGETS_JSON", "NEODESK_CLIENT_REF", "NEODESK_CLIENT_RELEASE_TAG",
		"NEODESK_CLIENT_REPO", "NEODESK_SUPPORT_RELEASE_PUBLIC_KEY", "BILLING_MAX_CLOCK_SKEW_MS",
		"BILLING_REQUIRE_SYNCED_CLOCK", "BILLING_REQUIRE_WORK_REPORT", "BILLING_ROUNDING_MINUTES", "BILLING_TRUST_OS_NTP",
		"CDAP_ENABLED", "CDAP_TLS", "CDAP_TLS_REQUIRED", "CLIENT_SESSION_EXPIRY_DAYS", "CLIENT_SESSION_MAX_DAYS",
		"CLIENT_SESSION_SLIDING", "DATA_DIR", "DB_PATH", "DB_TYPE", "DEFAULT_ADMIN_USERNAME", "GO_API_PORT",
		"GUACD_ADDRESS", "HBBS_API_URL", "HOST", "HTTPS_ENABLED", "HTTPS_PORT", "HTTP_REDIRECT_HTTPS", "KEYS_PATH",
		"LOG_LEVEL", "NODE_ENV", "NTP_SERVERS", "PANEL_PUBLIC_HOST", "PANEL_PUBLIC_URL", "PANEL_SIGNAL_PROXY_CIDRS",
		"PANEL_TRUST_ALLOWED_HOSTS", "PORT", "PUBLIC_API_URL", "PUBLIC_RELAY_SERVER", "PUBLIC_SERVER_ID",
		"PUB_KEY_PATH", "RUSTDESK_API_PROXY", "RUSTDESK_API_TLS", "RUSTDESK_DIR", "SERVER_BACKEND",
		"SESSION_IDLE_TIMEOUT", "SESSION_IDLE_WARN_MS", "SESSION_MAX_AGE", "SESSION_MAX_PER_USER", "SMTP_ALERT_EMAIL",
		"SMTP_FROM", "SMTP_HOST", "SMTP_PORT", "SMTP_SECURE", "SMTP_TLS_VERIFY", "SMTP_USER", "SSL_CA_PATH",
		"SSL_CERT_PATH", "SSL_KEY_PATH", "STORE_ADMIN_CREDENTIALS", "TRUSTED_PROXIES", "TRUST_PROXY",
		"UPDATE_GITHUB_BRANCH", "UPDATE_MANIFEST_PUBLIC_KEY", "UPDATE_REQUIRE_SIGNED_MANIFEST", "WEB_UI_DIR",
		"WS_ALLOWED_ORIGINS")
	for _, key := range New(Options{}).AllowedKeys() {
		switch {
		case secret[key] && shown[key]:
			t.Errorf("%s is in both lists", key)
		case secret[key]:
			if got := Mask(key, "the-value"); got != "********" {
				t.Errorf("%s holds a secret and comes out as %q", key, got)
			}
		case !shown[key]:
			t.Errorf("%s is in neither list: decide whether it holds a secret", key)
		}
	}
}

func TestPaths(t *testing.T) {
	cfg := New(Options{ConsoleDir: "/opt/NeoDeskConsole", GOOS: "linux"})
	p := cfg.Paths()
	if runtime.GOOS != "windows" && (p.ServerPath != "/opt/neodesk" || p.DataPath != "/opt/NeoDeskConsole/data" ||
		p.EnvPath != "/opt/NeoDeskConsole/.env" || p.BackupPath != "/opt/NeoDeskConsole/data/backups") {
		t.Errorf("%+v", p)
	}
	env := map[string]string{"NEODESK_CONSOLE_PATH": "/srv/c", "RUSTDESK_PATH": "/srv/s"}
	cfg = New(Options{ConsoleDir: "/x", EnvFile: "/etc/neodesk.env", Getenv: func(k string) string { return env[k] }})
	if p := cfg.Paths(); runtime.GOOS != "windows" && (p.ConsolePath != "/srv/c" || p.ServerPath != "/srv/s" || p.EnvPath != "/etc/neodesk.env") {
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

// TestApplyKeepsTheFiveNewestBackups: every write of the env file leaves
// a backup with all its secrets, and only the KeptBackups newest stay,
// the one of this write among them. A file of another name next to them
// (a copy made by hand, the backup of another file) stays (BUGS L58).
func TestApplyKeepsTheFiveNewestBackups(t *testing.T) {
	f := newFixture(t)
	env := filepath.Join(f.console, ".env")
	for _, name := range []string{".env.bak.manual", ".env.local.bak.2026-01-01T00-00-00-000Z", ".env.bak.2026-01-01T00-00-00-000Z.old"} {
		if err := os.WriteFile(filepath.Join(f.console, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	clock := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	f.cfg.opts.Now = func() time.Time { return clock }
	var last Applied
	for i := 0; i < 8; i++ {
		clock = clock.Add(time.Minute)
		applied, err := f.cfg.Apply([]Setting{{"PORT", strconv.Itoa(5000 + i)}})
		if err != nil {
			t.Fatal(err)
		}
		last = applied
	}
	entries, err := os.ReadDir(f.console)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if e.Name() != ".env" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	want := []string{
		".env.bak.2026-01-01T00-00-00-000Z.old",
		".env.bak.2026-11-22T09-04-00-000Z", ".env.bak.2026-11-22T09-05-00-000Z", ".env.bak.2026-11-22T09-06-00-000Z",
		".env.bak.2026-11-22T09-07-00-000Z", ".env.bak.2026-11-22T09-08-00-000Z",
		".env.bak.manual", ".env.local.bak.2026-01-01T00-00-00-000Z",
	}
	if strings.Join(names, "\n") != strings.Join(want, "\n") {
		t.Errorf("files next to the env file:\n%s\nwant:\n%s", strings.Join(names, "\n"), strings.Join(want, "\n"))
	}
	if last.BackupPath != env+".bak.2026-11-22T09-08-00-000Z" {
		t.Errorf("the last write's backup is %s", last.BackupPath)
	}
	if err := f.cfg.RestoreBackup(last.BackupPath); err != nil {
		t.Errorf("restore the last write's backup: %v", err)
	}
}

// TestHostOnlyKeysAreNotWrittenFromTheConsole: the keys that decide which
// releases and code the server trusts, and the secrets, are not written
// through the console's management API, though the template lists them: a
// hijacked admin session could otherwise add its own release key (L410).
// The other keys still are, and the command line on the host writes all.
func TestHostOnlyKeysAreNotWrittenFromTheConsole(t *testing.T) {
	c := New(Options{})
	for _, key := range []string{"UPDATE_MANIFEST_PUBLIC_KEY", "UPDATE_REQUIRE_SIGNED_MANIFEST", "NEODESK_SUPPORT_RELEASE_PUBLIC_KEY",
		"NEODESK_CUSTOM_CLIENT_SIGNING_SEED", "NEODESK_CLIENT_BUILD_COMMAND", "SESSION_SECRET", "PANEL_TRUST_SECRET",
		"ORG_PEER_VAULT_KEY", "REMOTE_TARGET_VAULT_KEY", "UPDATE_GITHUB_TOKEN", "NEODESK_GITHUB_TOKEN"} {
		if _, err := c.ValidateFromConsole(changes(key, "x"), true); err == nil {
			t.Errorf("%s is written from the console", key)
		}
		if _, msg := validate(c, changes(key, "x")); msg != "" {
			t.Errorf("%s from the command line: %s", key, msg)
		}
	}
	if _, err := c.ValidateFromConsole(changes("LOG_LEVEL", "debug"), true); err != nil {
		t.Errorf("LOG_LEVEL: %v", err)
	}
}
