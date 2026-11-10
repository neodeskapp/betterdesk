package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

// machine is a probe with the given files and folders.
func machine(exeDir, cwd string, files ...string) probe {
	have := map[string]bool{}
	for _, f := range files {
		have[filepath.FromSlash(f)] = true
	}
	return probe{exists: func(p string) bool { return have[p] }, exeDir: exeDir, cwd: cwd}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := load(env(nil), machine("/srv/bin", "/srv/run"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 0 || cfg.Host != "0.0.0.0" || cfg.NodeUpstream != nil || cfg.TrustProxy.Enabled() || cfg.DebugRouting {
		t.Errorf("defaults = %+v", cfg)
	}
	if cfg.NodeEnv != "development" || cfg.Production || cfg.Docker || cfg.HTTPS || cfg.HTTPSExact || cfg.AllowSelfSignedCerts {
		t.Errorf("mode defaults = %+v", cfg)
	}
	if cfg.DefaultLanguage != "en" || cfg.DeviceScopeDefault != "open" || cfg.LogLevel != "" || cfg.APIHost != "127.0.0.1:21114" {
		t.Errorf("defaults = %+v", cfg)
	}
	console := filepath.Join("/srv/bin", "..", "web-nodejs")
	want := map[string]string{
		"ConsoleDir":    console,
		"DataDir":       filepath.Join(console, "data"),
		"LangDir":       filepath.Join(console, "lang"),
		"WebUIDir":      filepath.Join(console, "..", "web-ui", "build"),
		"PublicDir":     filepath.Join(console, "public"),
		"ProtosDir":     filepath.Join(console, "protos"),
		"WallpapersDir": filepath.Join(console, "wallpapers"),
		"UploadsDir":    filepath.Join(console, "data", "uploads"),
	}
	got := map[string]string{
		"ConsoleDir": cfg.ConsoleDir, "DataDir": cfg.DataDir, "LangDir": cfg.LangDir, "WebUIDir": cfg.WebUIDir,
		"PublicDir": cfg.PublicDir, "ProtosDir": cfg.ProtosDir, "WallpapersDir": cfg.WallpapersDir, "UploadsDir": cfg.UploadsDir,
	}
	for name, dir := range want {
		if got[name] != dir {
			t.Errorf("%s = %q, want %q", name, got[name], dir)
		}
	}
	if cfg.Getenv == nil {
		t.Error("Getenv is not kept for the packages that read their own settings")
	}
}

func TestLoadValues(t *testing.T) {
	cfg, err := load(env(map[string]string{
		"HOST": "127.0.0.1", "CONSOLE_PORT": "5000", "TRUST_PROXY": "loopback",
		"CONSOLE_NODE_UPSTREAM": "http://127.0.0.1:5001",
		"NODE_ENV":              "production", "HTTPS_ENABLED": "TRUE", "HSTS_ENABLED": "On", "ALLOW_SELF_SIGNED_CERTS": "True",
		"DEFAULT_LANGUAGE": "de", "DEVICE_SCOPE_DEFAULT": "Restricted", "LOG_LEVEL": " Debug ",
		"DATA_DIR": "/data", "CONSOLE_DIR": "/console", "CONSOLE_LANG_DIR": "/langs", "WEB_UI_DIR": "/ui",
		"HBBS_API_URL": "http://127.0.0.1:21121/api",
	}), machine("/srv/bin", "/srv/run"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr() != "127.0.0.1:5000" {
		t.Errorf("Addr = %q", cfg.Addr())
	}
	if cfg.NodeUpstream == nil || cfg.NodeUpstream.Host != "127.0.0.1:5001" {
		t.Errorf("NodeUpstream = %v", cfg.NodeUpstream)
	}
	if !cfg.TrustProxy.Enabled() {
		t.Error("TrustProxy is off")
	}
	if !cfg.Production || !cfg.HTTPS || cfg.HTTPSExact || cfg.HSTSEnabled != "On" || !cfg.AllowSelfSignedCerts {
		t.Errorf("mode = %+v", cfg)
	}
	if cfg.DefaultLanguage != "de" || cfg.DeviceScopeDefault != "restricted" || cfg.LogLevel != " Debug " || cfg.APIHost != "127.0.0.1:21121" {
		t.Errorf("values = %+v", cfg)
	}
	if cfg.DataDir != "/data" || cfg.LangDir != "/langs" || cfg.WebUIDir != "/ui" ||
		cfg.PublicDir != filepath.Join("/console", "public") || cfg.UploadsDir != filepath.Join("/data", "uploads") {
		t.Errorf("folders = %+v", cfg)
	}
	// The transfers folder stays with the console's files, not with DATA_DIR
	if cfg.TransfersDir != filepath.Join("/console", "data", "transfers") {
		t.Errorf("TransfersDir = %q", cfg.TransfersDir)
	}
}

func TestSwitchesReadAsTheOldConsoleDid(t *testing.T) {
	for value, want := range map[string][2]bool{
		"true": {true, true}, "True": {true, false}, "TRUE": {true, false},
		"1": {false, false}, "yes": {false, false}, "": {false, false}, " true": {false, false},
	} {
		cfg, err := load(env(map[string]string{"HTTPS_ENABLED": value}), machine("", ""))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.HTTPS != want[0] || cfg.HTTPSExact != want[1] {
			t.Errorf("HTTPS_ENABLED=%q gives HTTPS %v, exact %v; want %v", value, cfg.HTTPS, cfg.HTTPSExact, want)
		}
	}
	for value, want := range map[string]bool{"production": true, "Production": false, "development": false, "": false} {
		cfg, _ := load(env(map[string]string{"NODE_ENV": value}), machine("", ""))
		if cfg.Production != want {
			t.Errorf("NODE_ENV=%q gives Production %v", value, cfg.Production)
		}
	}
}

func TestDockerRule(t *testing.T) {
	byEnv, _ := load(env(map[string]string{"DOCKER": "true"}), machine("/usr/local/bin", "/opt/rustdesk"))
	byFile, _ := load(env(nil), machine("/usr/local/bin", "/opt/rustdesk", "/.dockerenv"))
	for name, cfg := range map[string]*Config{"DOCKER=true": byEnv, "/.dockerenv": byFile} {
		if !cfg.Docker || cfg.DataDir != "/app/data" || cfg.ConsoleDir != "/app" || cfg.LangDir != filepath.Join("/app", "lang") ||
			cfg.WebUIDir != filepath.Join("/app", "..", "web-ui", "build") {
			t.Errorf("%s: %+v", name, cfg)
		}
	}
	// DOCKER has to be "true" exactly, and DATA_DIR wins over the rule
	other, _ := load(env(map[string]string{"DOCKER": "TRUE", "DATA_DIR": "/d"}), machine("/b", "/c"))
	if other.Docker || other.DataDir != "/d" {
		t.Errorf("DOCKER=TRUE: %+v", other)
	}
	set, _ := load(env(map[string]string{"DOCKER": "true", "DATA_DIR": "/d"}), machine("/b", "/c"))
	if set.DataDir != "/d" || set.UploadsDir != filepath.Join("/d", "uploads") {
		t.Errorf("DATA_DIR in a container: %+v", set)
	}
}

func TestConsoleDirSearch(t *testing.T) {
	beside := filepath.Join("/srv/bin", "..", "web-nodejs")
	besideCwd := filepath.Join("/srv/run", "..", "web-nodejs")
	cases := []struct {
		name  string
		env   map[string]string
		files []string
		want  string
	}{
		{"nothing found: next to the binary", nil, nil, beside},
		{"next to the binary", nil, []string{beside + "/lang/en.json", "/opt/BetterDeskConsole/lang/en.json"}, beside},
		{"next to the working directory", nil, []string{besideCwd + "/lang/en.json", "/opt/BetterDeskConsole/lang/en.json"}, besideCwd},
		{"the folder the installer uses", nil, []string{"/opt/BetterDeskConsole/lang/en.json", "/opt/betterdesk/lang/en.json"}, "/opt/BetterDeskConsole"},
		{"the older folder", nil, []string{"/opt/betterdesk/lang/en.json"}, "/opt/betterdesk"},
		{"a container without /app/lang", map[string]string{"DOCKER": "true"}, []string{"/opt/betterdesk/lang/en.json"}, "/opt/betterdesk"},
		{"a container", map[string]string{"DOCKER": "true"}, []string{"/app/lang/en.json", "/opt/betterdesk/lang/en.json"}, "/app"},
		{"CONSOLE_DIR wins", map[string]string{"CONSOLE_DIR": "/x"}, []string{"/app/lang/en.json"}, "/x"},
	}
	for _, c := range cases {
		cfg, err := load(env(c.env), machine("/srv/bin", "/srv/run", c.files...))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ConsoleDir != filepath.FromSlash(c.want) {
			t.Errorf("%s: ConsoleDir = %q, want %q", c.name, cfg.ConsoleDir, c.want)
		}
	}
}

func TestWebUIDirSearch(t *testing.T) {
	inside := filepath.Join("/opt/BetterDeskConsole", "web-ui", "build")
	beside := filepath.Join("/opt/BetterDeskConsole", "..", "web-ui", "build")
	only := func(files ...string) *Config {
		cfg, _ := load(env(map[string]string{"CONSOLE_DIR": "/opt/BetterDeskConsole"}), machine("", "", files...))
		return cfg
	}
	if got := only().WebUIDir; got != beside {
		t.Errorf("no build anywhere: %q, want %q", got, beside)
	}
	if got := only(inside + "/index.html").WebUIDir; got != inside {
		t.Errorf("a build inside the console folder: %q, want %q", got, inside)
	}
	if got := only(inside+"/index.html", beside+"/index.html").WebUIDir; got != beside {
		t.Errorf("builds in both places: %q, want %q", got, beside)
	}
}

func TestAPIHost(t *testing.T) {
	for _, c := range []struct{ better, hbbs, want string }{
		{"", "", "127.0.0.1:21114"},
		{"http://10.0.0.5:21114/api", "http://127.0.0.1:21121/api", "10.0.0.5:21114"},
		{"", "https://api.example/api", "api.example"},
		{"not a url", "", "127.0.0.1:21114"},
	} {
		cfg, _ := load(env(map[string]string{"BETTERDESK_API_URL": c.better, "HBBS_API_URL": c.hbbs}), machine("", ""))
		if cfg.APIHost != c.want {
			t.Errorf("BETTERDESK_API_URL=%q HBBS_API_URL=%q: APIHost = %q, want %q", c.better, c.hbbs, cfg.APIHost, c.want)
		}
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	for _, values := range []map[string]string{
		{"CONSOLE_PORT": "abc"},
		{"CONSOLE_PORT": "70000"},
		{"TRUST_PROXY": "not an address"},
		{"CONSOLE_NODE_UPSTREAM": "ftp://x"},
		{"CONSOLE_NODE_UPSTREAM": "127.0.0.1:5001"},
	} {
		if _, err := Load(env(values)); err == nil {
			t.Errorf("Load(%v) accepted a bad value", values)
		}
	}
}

func TestLoadDebugRouting(t *testing.T) {
	for value, want := range map[string]bool{"1": true, " 1 ": true, "": false, "0": false, "true": false} {
		cfg, err := Load(env(map[string]string{"CONSOLE_DEBUG_ROUTING": value}))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.DebugRouting != want {
			t.Errorf("CONSOLE_DEBUG_ROUTING=%q gives DebugRouting %v, want %v", value, cfg.DebugRouting, want)
		}
	}
}

// TestSettingsAsTheOldConsoleReadThem replays testdata/settings.json: what
// config/config.js of the Node console made of each environment
// (testdata/nodeoracle/settings.js).
func TestSettingsAsTheOldConsoleReadThem(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Env    map[string]string `json:"env"`
		Docker bool              `json:"docker"`
		Config struct {
			GoAPIPort  int    `json:"goApiPort"`
			APIPort    int    `json:"apiPort"`
			APIEnabled bool   `json:"apiEnabled"`
			HbbsAPIURL string `json:"hbbsApiUrl"`
			APIURL     string `json:"betterdeskApiUrl"`
			KeysPath   string `json:"keysPath"`
			DBPath     string `json:"dbPath"`
			PubKeyPath string `json:"pubKeyPath"`
			APIKeyPath string `json:"apiKeyPath"`
			WSProxy    struct {
				HbbsHost string `json:"hbbsHost"`
				HbbsPort int    `json:"hbbsPort"`
				HbbrHost string `json:"hbbrHost"`
				HbbrPort int    `json:"hbbrPort"`
			} `json:"wsProxy"`
		} `json:"config"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 100 {
		t.Fatalf("only %d cases in testdata/settings.json", len(cases))
	}
	for _, c := range cases {
		var files []string
		if c.Docker {
			files = append(files, "/.dockerenv")
		}
		cfg, err := load(env(c.Env), machine("/srv/bin", "/srv/run", files...))
		if err != nil {
			t.Errorf("%v: %v", c.Env, err)
			continue
		}
		want := c.Config
		if cfg.GoAPIPort != want.GoAPIPort || cfg.APIPort != want.APIPort || cfg.APIEnabled != want.APIEnabled {
			t.Errorf("%q: GoAPIPort %d, APIPort %d, APIEnabled %v; the old console read %d, %d, %v",
				c.Env, cfg.GoAPIPort, cfg.APIPort, cfg.APIEnabled, want.GoAPIPort, want.APIPort, want.APIEnabled)
		}
		if cfg.APIURL != want.APIURL || cfg.APIURL != want.HbbsAPIURL {
			t.Errorf("%q: APIURL %q; the old console read %q", c.Env, cfg.APIURL, want.APIURL)
		}
		if cfg.KeysPath != want.KeysPath || cfg.DBPath != want.DBPath || cfg.PubKeyPath != want.PubKeyPath || cfg.APIKeyPath != want.APIKeyPath {
			t.Errorf("%q: KeysPath %q, DBPath %q, PubKeyPath %q, APIKeyPath %q; the old console read %q, %q, %q, %q",
				c.Env, cfg.KeysPath, cfg.DBPath, cfg.PubKeyPath, cfg.APIKeyPath, want.KeysPath, want.DBPath, want.PubKeyPath, want.APIKeyPath)
		}
		if cfg.SignalHost != want.WSProxy.HbbsHost || cfg.SignalPort != want.WSProxy.HbbsPort ||
			cfg.RelayHost != want.WSProxy.HbbrHost || cfg.RelayPort != want.WSProxy.HbbrPort {
			t.Errorf("%q: signal %s:%d, relay %s:%d; the old console read %+v",
				c.Env, cfg.SignalHost, cfg.SignalPort, cfg.RelayHost, cfg.RelayPort, want.WSProxy)
		}
	}
}

func TestKeysPathSearch(t *testing.T) {
	windows := func(files ...string) probe {
		have := map[string]bool{}
		for _, f := range files {
			have[f] = true
		}
		return probe{exists: func(p string) bool { return have[p] }, windows: true}
	}
	cases := []struct {
		name string
		env  map[string]string
		on   probe
		want string
	}{
		{"nothing found: the newer folder", nil, machine("", ""), "/opt/betterdesk"},
		{"the newer folder has the key", nil, machine("", "", "/opt/betterdesk/id_ed25519", "/opt/rustdesk/id_ed25519"), "/opt/betterdesk"},
		{"only the older folder has the key", nil, machine("", "", "/opt/rustdesk/id_ed25519"), "/opt/rustdesk"},
		{"the public key alone does not count", nil, machine("", "", "/opt/rustdesk/id_ed25519.pub"), "/opt/betterdesk"},
		{"a container", nil, machine("", "", "/.dockerenv", "/opt/betterdesk/id_ed25519"), "/opt/rustdesk"},
		{"KEYS_PATH first", map[string]string{"KEYS_PATH": "/k", "RUSTDESK_DIR": "/d", "RUSTDESK_PATH": "/p"}, machine("", "", "/.dockerenv"), "/k"},
		{"then RUSTDESK_DIR", map[string]string{"RUSTDESK_DIR": "/d", "RUSTDESK_PATH": "/p"}, machine("", ""), "/d"},
		{"then RUSTDESK_PATH", map[string]string{"RUSTDESK_PATH": "/p"}, machine("", "", "/opt/rustdesk/id_ed25519"), "/p"},
		{"Windows, nothing found", nil, windows(), `C:\BetterDesk`},
		{"Windows, the newer folder", nil, windows(`C:\BetterDesk\id_ed25519`, `C:\RustDesk\id_ed25519`), `C:\BetterDesk`},
		{"Windows, the older folder", nil, windows(`C:\RustDesk\id_ed25519`), `C:\RustDesk`},
	}
	for _, c := range cases {
		cfg, err := load(env(c.env), c.on)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.KeysPath != c.want {
			t.Errorf("%s: KeysPath = %q, want %q", c.name, cfg.KeysPath, c.want)
		}
		if want := filepath.Join(c.want, "id_ed25519.pub"); cfg.PubKeyPath != want {
			t.Errorf("%s: PubKeyPath = %q, want %q", c.name, cfg.PubKeyPath, want)
		}
		if want := filepath.Join(c.want, ".api_key"); cfg.APIKeyPath != want {
			t.Errorf("%s: APIKeyPath = %q, want %q", c.name, cfg.APIKeyPath, want)
		}
		if want := filepath.Join(c.want, "db_v2.sqlite3"); cfg.DBPath != want {
			t.Errorf("%s: DBPath = %q, want %q", c.name, cfg.DBPath, want)
		}
	}
}

func TestKeyFilesHaveTheirOwnSettings(t *testing.T) {
	cfg, err := load(env(map[string]string{
		"KEYS_PATH": "/k", "DB_PATH": "/x/db.sqlite3", "PUB_KEY_PATH": "/y/key.pub", "API_KEY_PATH": "/z/api",
	}), machine("", ""))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KeysPath != "/k" || cfg.DBPath != "/x/db.sqlite3" || cfg.PubKeyPath != "/y/key.pub" || cfg.APIKeyPath != "/z/api" {
		t.Errorf("paths = %q %q %q %q", cfg.KeysPath, cfg.DBPath, cfg.PubKeyPath, cfg.APIKeyPath)
	}
}

func TestEnvAndComposePaths(t *testing.T) {
	cfg, err := load(env(map[string]string{"CONSOLE_DIR": "/opt/BetterDeskConsole"}), machine("", ""))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/opt/BetterDeskConsole", ".env"); cfg.EnvPath != want {
		t.Errorf("EnvPath = %q, want %q", cfg.EnvPath, want)
	}
	if want := filepath.Join("/opt", "docker-compose.yml"); cfg.ComposePath != want {
		t.Errorf("ComposePath = %q, want %q", cfg.ComposePath, want)
	}
}

func TestSignalAndRelaySettings(t *testing.T) {
	cfg, err := load(env(map[string]string{
		"WS_HBBS_HOST": "signal.example", "WS_HBBS_PORT": "31116", "WS_HBBR_HOST": "relay.example", "WS_HBBR_PORT": "41117",
	}), machine("", ""))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SignalHost != "signal.example" || cfg.SignalPort != 31116 || cfg.RelayHost != "relay.example" || cfg.RelayPort != 41117 {
		t.Errorf("signal %s:%d, relay %s:%d", cfg.SignalHost, cfg.SignalPort, cfg.RelayHost, cfg.RelayPort)
	}
}

func TestAPIAddressDefaults(t *testing.T) {
	cfg, err := load(env(nil), machine("", ""))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIURL != "http://127.0.0.1:21114/api" || cfg.GoAPIPort != 21114 || cfg.APIPort != 21121 || !cfg.APIEnabled {
		t.Errorf("API defaults = %q %d %d %v", cfg.APIURL, cfg.GoAPIPort, cfg.APIPort, cfg.APIEnabled)
	}
	if cfg.SignalHost != "localhost" || cfg.SignalPort != 21116 || cfg.RelayHost != "localhost" || cfg.RelayPort != 21117 {
		t.Errorf("signal and relay defaults = %s:%d %s:%d", cfg.SignalHost, cfg.SignalPort, cfg.RelayHost, cfg.RelayPort)
	}
}

// FuzzAPIURLPort reads any text as an API address: the result is the
// fallback or a port, and a plain http address with a port gives that
// port.
func FuzzAPIURLPort(f *testing.F) {
	for _, seed := range []string{"http://127.0.0.1:21114/api", "https://host", "http://host:99999", " http://host:5 ", "localhost:1/x", "//host:5", "http://[::1]:7/", "not a url", "", "http://:5", "ftp://h:21"} {
		f.Add(seed, 21114)
	}
	f.Fuzz(func(t *testing.T, raw string, port int) {
		got := apiURLPort(raw, -1)
		if got != -1 && (got < 0 || got > 65535) {
			t.Fatalf("apiURLPort(%q) = %d", raw, got)
		}
		if port < 1 || port > 65535 || port == 80 {
			return
		}
		address := "http://server.example:" + strconv.Itoa(port) + "/api"
		if got := apiURLPort(address, -1); got != port {
			t.Fatalf("apiURLPort(%q) = %d", address, got)
		}
		if got := parseIntOr(" +"+strconv.Itoa(port)+raw, -1); got < port && got != -1 {
			t.Fatalf("parseIntOr(%q) = %d", " +"+strconv.Itoa(port)+raw, got)
		}
	})
}
