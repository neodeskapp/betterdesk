package config

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/internal/productversion"
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

// folders are the folders and files of a configuration by field name.
func folders(cfg *Config) map[string]string {
	return map[string]string{
		"ConsoleDir": cfg.ConsoleDir, "DataDir": cfg.DataDir, "WebUIDir": cfg.WebUIDir, "WallpapersDir": cfg.WallpapersDir,
		"UploadsDir": cfg.UploadsDir, "TransfersDir": cfg.TransfersDir, "FontsDir": cfg.FontsDir,
		"FontCacheFile": cfg.FontCacheFile, "TempDir": cfg.TempDir, "StaticDir": cfg.StaticDir, "LangDir": cfg.LangDir,
		"PublicDir": cfg.PublicDir, "ProtosDir": cfg.ProtosDir, "ThemesDir": cfg.ThemesDir, "PalettesFile": cfg.PalettesFile,
		"FaviconFile": cfg.FaviconFile, "SupportersFile": cfg.SupportersFile,
	}
}

// machine is a probe with the given files and folders.
func machine(files ...string) probe {
	have := map[string]bool{}
	for _, f := range files {
		have[filepath.FromSlash(f)] = true
	}
	return probe{exists: func(p string) bool { return have[p] }}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := load(env(nil), machine())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 5000 || cfg.Host != "127.0.0.1" || cfg.NodeUpstream != nil || cfg.TrustProxy.Enabled() || cfg.DebugRouting {
		t.Errorf("defaults = %+v", cfg)
	}
	if cfg.NodeEnv != "development" || cfg.Production || cfg.Docker || cfg.HTTPS || cfg.HTTPSExact || cfg.AllowSelfSignedCerts {
		t.Errorf("mode defaults = %+v", cfg)
	}
	if cfg.DefaultLanguage != "en" || cfg.DeviceScopeDefault != "open" || cfg.LogLevel != "" || cfg.APIHost != "127.0.0.1:21114" {
		t.Errorf("defaults = %+v", cfg)
	}
	console := "/opt/BetterDeskConsole"
	want := map[string]string{
		"ConsoleDir":    console,
		"DataDir":       filepath.Join(console, "data"),
		"WebUIDir":      "",
		"WallpapersDir": filepath.Join(console, "wallpapers"),
		"UploadsDir":    filepath.Join(console, "data", "uploads"),
		"FontsDir":      filepath.Join(console, "public", "fonts"),
		// The console's own files come from the binary
		"StaticDir": "", "LangDir": "", "PublicDir": "", "ProtosDir": "",
		"ThemesDir": "", "PalettesFile": "", "FaviconFile": "", "SupportersFile": "",
	}
	got := folders(cfg)
	for name, dir := range want {
		if got[name] != dir {
			t.Errorf("%s = %q, want %q", name, got[name], dir)
		}
	}
	if cfg.Static == nil {
		t.Fatal("no files in the binary")
	}
	for _, name := range []string{
		"lang/en.json", "lang/pl.json", "lang/zh-TW.json", "public/favicon.svg", "public/img/betterdesk_icon.png",
		"public/img/login-slides/free.jpg", "protos/rendezvous.proto", "protos/message.proto", "themes/default.json",
		"config/theme-palettes.json", "config/supporters.json",
	} {
		if info, err := fs.Stat(cfg.Static, name); err != nil || info.Size() == 0 {
			t.Errorf("%s in the binary: %v", name, err)
		}
	}
	if cfg.Version == "" || cfg.Version != productversion.Product() {
		t.Errorf("Version = %q, want the binary's %q", cfg.Version, productversion.Product())
	}
	if cfg.Getenv == nil {
		t.Error("Getenv is not kept for the packages that read their own settings")
	}
}

// TestPanelGroupingSettings reads BACKEND_PANEL_GROUPING and
// PANEL_TRUST_SECRET the way services/panelGrouping.js and panelTrust.js
// did: String(value).trim() with JavaScript's white space (U+FEFF is
// trimmed, U+0085 is not), the owner in lower case. The bridge is on only
// where the API server's TrimSpace leaves something too (it trims U+0085,
// so a secret of that alone was ignored there).
func TestPanelGroupingSettings(t *testing.T) {
	for _, c := range []struct {
		grouping, secret string
		node, trust      bool
	}{
		{"", "", false, false},
		{"node", "s", true, true},
		{" NODE\t", "  ", true, false},
		{"\xef\xbb\xbfNode\xe3\x80\x80", "\xef\xbb\xbf", true, false},
		{"\u0085node", "\u0085", false, false},
		{"node", "\u0085x\xef\xbb\xbf", true, true},
		{"go", "a b", false, true},
		{"nodes", "\n", false, false},
		{"nod", "0", false, true},
	} {
		cfg, err := load(env(map[string]string{"BACKEND_PANEL_GROUPING": c.grouping, "PANEL_TRUST_SECRET": c.secret}), machine())
		if err != nil {
			t.Fatal(err)
		}
		if cfg.PanelGroupingNode != c.node || cfg.PanelTrust != c.trust {
			t.Errorf("BACKEND_PANEL_GROUPING %q, PANEL_TRUST_SECRET %q: node %v trust %v, want %v %v",
				c.grouping, c.secret, cfg.PanelGroupingNode, cfg.PanelTrust, c.node, c.trust)
		}
	}
}

func TestLoadValues(t *testing.T) {
	cfg, err := load(env(map[string]string{
		"HOST": "127.0.0.1", "CONSOLE_PORT": "5100", "TRUST_PROXY": "loopback",
		"CONSOLE_NODE_UPSTREAM": "http://127.0.0.1:5001",
		"NODE_ENV":              "production", "HTTPS_ENABLED": "TRUE", "HSTS_ENABLED": "On", "ALLOW_SELF_SIGNED_CERTS": "True",
		"DEFAULT_LANGUAGE": "de", "DEVICE_SCOPE_DEFAULT": "Restricted", "LOG_LEVEL": " Debug ",
		"DATA_DIR": "/data", "CONSOLE_DIR": "/console", "CONSOLE_LANG_DIR": "/langs", "WEB_UI_DIR": "/ui",
		"HBBS_API_URL": "http://127.0.0.1:21121/api",
	}), machine())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr() != "127.0.0.1:5100" {
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
	want := map[string]string{
		"ConsoleDir": "/console", "DataDir": "/data", "WebUIDir": "/ui", "UploadsDir": filepath.Join("/data", "uploads"),
		"WallpapersDir": filepath.Join("/console", "wallpapers"),
		// The transfers and the branding's files stay in the console's
		// folder
		"FontsDir":      filepath.Join("/console", "public", "fonts"),
		"TransfersDir":  filepath.Join("/console", "data", "transfers"),
		"FontCacheFile": filepath.Join("/console", "data", "google-fonts-cache.json"),
		"TempDir":       filepath.Join("/console", "data", "tmp"),
		// The language files come from CONSOLE_LANG_DIR, the other
		// files of the console from the binary
		"LangDir": "/langs", "StaticDir": "", "PublicDir": "", "ProtosDir": "",
		"ThemesDir": "", "PalettesFile": "", "FaviconFile": "", "SupportersFile": "",
	}
	got := folders(cfg)
	for name, dir := range want {
		if got[name] != dir {
			t.Errorf("%s = %q, want %q", name, got[name], dir)
		}
	}
	if cfg.Static == nil {
		t.Error("no files in the binary")
	}
}

// CONSOLE_STATIC_DIR reads every one of the console's own files from a
// folder on disk; CONSOLE_LANG_DIR still wins for the language files.
func TestStaticDir(t *testing.T) {
	cfg, err := load(env(map[string]string{"CONSOLE_STATIC_DIR": "/src/static", "CONSOLE_DIR": "/console"}), machine())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"StaticDir":      "/src/static",
		"LangDir":        filepath.Join("/src/static", "lang"),
		"PublicDir":      filepath.Join("/src/static", "public"),
		"ProtosDir":      filepath.Join("/src/static", "protos"),
		"ThemesDir":      filepath.Join("/src/static", "themes"),
		"PalettesFile":   filepath.Join("/src/static", "config", "theme-palettes.json"),
		"FaviconFile":    filepath.Join("/src/static", "public", "favicon.svg"),
		"SupportersFile": filepath.Join("/src/static", "config", "supporters.json"),
		// State stays where it was
		"ConsoleDir": "/console", "DataDir": filepath.Join("/console", "data"),
		"FontsDir": filepath.Join("/console", "public", "fonts"), "WallpapersDir": filepath.Join("/console", "wallpapers"),
	}
	got := folders(cfg)
	for name, dir := range want {
		if got[name] != dir {
			t.Errorf("%s = %q, want %q", name, got[name], dir)
		}
	}
	if cfg.Static != nil {
		t.Error("the files in the binary are read beside CONSOLE_STATIC_DIR")
	}
	cfg, err = load(env(map[string]string{"CONSOLE_STATIC_DIR": "/src/static", "CONSOLE_LANG_DIR": "/langs"}), machine())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LangDir != "/langs" || cfg.PublicDir != filepath.Join("/src/static", "public") {
		t.Errorf("CONSOLE_LANG_DIR with CONSOLE_STATIC_DIR: LangDir %q, PublicDir %q", cfg.LangDir, cfg.PublicDir)
	}
}

func TestSwitchesReadAsTheOldConsoleDid(t *testing.T) {
	for value, want := range map[string][2]bool{
		"true": {true, true}, "True": {true, false}, "TRUE": {true, false},
		"1": {false, false}, "yes": {false, false}, "": {false, false}, " true": {false, false},
	} {
		cfg, err := load(env(map[string]string{"HTTPS_ENABLED": value}), machine())
		if err != nil {
			t.Fatal(err)
		}
		if cfg.HTTPS != want[0] || cfg.HTTPSExact != want[1] {
			t.Errorf("HTTPS_ENABLED=%q gives HTTPS %v, exact %v; want %v", value, cfg.HTTPS, cfg.HTTPSExact, want)
		}
	}
	for value, want := range map[string]bool{"production": true, "Production": false, "development": false, "": false} {
		cfg, _ := load(env(map[string]string{"NODE_ENV": value}), machine())
		if cfg.Production != want {
			t.Errorf("NODE_ENV=%q gives Production %v", value, cfg.Production)
		}
	}
}

func TestDockerRule(t *testing.T) {
	byEnv, _ := load(env(map[string]string{"DOCKER": "true"}), machine())
	byFile, _ := load(env(nil), machine("/.dockerenv"))
	for name, cfg := range map[string]*Config{"DOCKER=true": byEnv, "/.dockerenv": byFile} {
		if !cfg.Docker || cfg.DataDir != "/app/data" || cfg.ConsoleDir != "/app" || cfg.LangDir != "" || cfg.Static == nil ||
			cfg.WebUIDir != "" {
			t.Errorf("%s: %+v", name, cfg)
		}
	}
	// DOCKER has to be "true" exactly, and DATA_DIR wins over the rule
	other, _ := load(env(map[string]string{"DOCKER": "TRUE", "DATA_DIR": "/d"}), machine())
	if other.Docker || other.DataDir != "/d" {
		t.Errorf("DOCKER=TRUE: %+v", other)
	}
	set, _ := load(env(map[string]string{"DOCKER": "true", "DATA_DIR": "/d"}), machine())
	if set.DataDir != "/d" || set.UploadsDir != filepath.Join("/d", "uploads") {
		t.Errorf("DATA_DIR in a container: %+v", set)
	}
}

// The console's folder is the one that holds its state. An install of
// the Node console is found by the language files it left there, a newer
// one by its .env. A web-nodejs folder in a source tree is not looked for.
func TestConsoleDirSearch(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		files   []string
		windows bool
		want    string
	}{
		{"nothing found: the folder the installer uses", nil, []string{"/srv/web-nodejs/lang/en.json", "/srv/web-nodejs/.env"}, false, "/opt/BetterDeskConsole"},
		{"a Node install in the folder the installer uses", nil, []string{"/opt/BetterDeskConsole/lang/en.json", "/opt/betterdesk/lang/en.json"}, false, "/opt/BetterDeskConsole"},
		{"a Node install in the older folder", nil, []string{"/opt/betterdesk/lang/en.json"}, false, "/opt/betterdesk"},
		{"an install with only its .env", nil, []string{"/var/lib/betterdesk/.env"}, false, "/var/lib/betterdesk"},
		{"the first folder with either", nil, []string{"/opt/betterdesk/lang/en.json", "/var/lib/betterdesk/.env"}, false, "/opt/betterdesk"},
		{"neither in the folder the installer uses", nil, []string{"/opt/BetterDeskConsole/data/auth.db", "/opt/betterdesk/.env"}, false, "/opt/betterdesk"},
		{"a container without either in /app", map[string]string{"DOCKER": "true"}, []string{"/opt/betterdesk/lang/en.json"}, false, "/opt/betterdesk"},
		{"a container of the Node image", map[string]string{"DOCKER": "true"}, []string{"/app/lang/en.json", "/opt/betterdesk/lang/en.json"}, false, "/app"},
		{"a container with nothing found", map[string]string{"DOCKER": "true"}, nil, false, "/app"},
		{"CONSOLE_DIR wins", map[string]string{"CONSOLE_DIR": "/x"}, []string{"/app/lang/en.json", "/opt/BetterDeskConsole/.env"}, false, "/x"},
		{"Windows", nil, nil, true, `C:\BetterDeskConsole`},
	}
	for _, c := range cases {
		p := machine(c.files...)
		p.windows = c.windows
		cfg, err := load(env(c.env), p)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ConsoleDir != c.want {
			t.Errorf("%s: ConsoleDir = %q, want %q", c.name, cfg.ConsoleDir, c.want)
		}
	}
}

// The build on disk is only the one WEB_UI_DIR names: the builds the old
// console's folder has beside or inside it are not looked for, so an
// upgraded install serves the build in the binary.
func TestWebUIDirOnlyFromTheVariable(t *testing.T) {
	inside := filepath.Join("/opt/BetterDeskConsole", "web-ui", "build")
	beside := filepath.Join("/opt/BetterDeskConsole", "..", "web-ui", "build")
	builds := machine(inside+"/index.html", beside+"/index.html")
	cfg, _ := load(env(map[string]string{"CONSOLE_DIR": "/opt/BetterDeskConsole"}), builds)
	if cfg.WebUIDir != "" {
		t.Errorf("without WEB_UI_DIR: %q, want the build in the binary", cfg.WebUIDir)
	}
	cfg, _ = load(env(map[string]string{"CONSOLE_DIR": "/opt/BetterDeskConsole", "WEB_UI_DIR": inside}), builds)
	if cfg.WebUIDir != inside {
		t.Errorf("WEB_UI_DIR=%s: %q", inside, cfg.WebUIDir)
	}
}

// CONSOLE_PORT, else 5000; 0 turns the console off (DECISIONS A27). PORT
// is the old console's and the signal server's, never the console's.
func TestConsolePort(t *testing.T) {
	for _, c := range []struct {
		env  map[string]string
		want int
	}{
		{nil, 5000},
		{map[string]string{"CONSOLE_PORT": ""}, 5000},
		{map[string]string{"CONSOLE_PORT": "  "}, 5000},
		{map[string]string{"CONSOLE_PORT": "0"}, 0},
		{map[string]string{"CONSOLE_PORT": " 5001 "}, 5001},
		{map[string]string{"CONSOLE_PORT": "65535"}, 65535},
		{map[string]string{"PORT": "6000"}, 5000},
		{map[string]string{"PORT": "6000", "CONSOLE_PORT": "0"}, 0},
	} {
		cfg, err := load(env(c.env), machine())
		if err != nil {
			t.Fatalf("%v: %v", c.env, err)
		}
		if cfg.Port != c.want {
			t.Errorf("%v: port %d, want %d", c.env, cfg.Port, c.want)
		}
		// A62: only a port nobody set may give way to an older console
		if unset := strings.TrimSpace(c.env["CONSOLE_PORT"]) == ""; cfg.PortUnset != unset {
			t.Errorf("%v: PortUnset %v, want %v", c.env, cfg.PortUnset, unset)
		}
	}
	for _, bad := range []string{"-1", "65536", "5000x", "0x10", "5 000"} {
		if _, err := load(env(map[string]string{"CONSOLE_PORT": bad}), machine()); err == nil {
			t.Errorf("CONSOLE_PORT=%q was taken", bad)
		}
	}
}

func TestAPIHost(t *testing.T) {
	for _, c := range []struct{ better, hbbs, want string }{
		{"", "", "127.0.0.1:21114"},
		{"http://10.0.0.5:21114/api", "http://127.0.0.1:21121/api", "10.0.0.5:21114"},
		{"", "https://api.example/api", "api.example"},
		{"not a url", "", "127.0.0.1:21114"},
	} {
		cfg, _ := load(env(map[string]string{"BETTERDESK_API_URL": c.better, "HBBS_API_URL": c.hbbs}), machine())
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
		cfg, err := load(env(c.Env), machine(files...))
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
		{"nothing found: the newer folder", nil, machine(), "/opt/betterdesk"},
		{"the newer folder has the key", nil, machine("/opt/betterdesk/id_ed25519", "/opt/rustdesk/id_ed25519"), "/opt/betterdesk"},
		{"only the older folder has the key", nil, machine("/opt/rustdesk/id_ed25519"), "/opt/rustdesk"},
		{"the public key alone does not count", nil, machine("/opt/rustdesk/id_ed25519.pub"), "/opt/betterdesk"},
		{"a container", nil, machine("/.dockerenv", "/opt/betterdesk/id_ed25519"), "/opt/rustdesk"},
		{"KEYS_PATH first", map[string]string{"KEYS_PATH": "/k", "RUSTDESK_DIR": "/d", "RUSTDESK_PATH": "/p"}, machine("/.dockerenv"), "/k"},
		{"then RUSTDESK_DIR", map[string]string{"RUSTDESK_DIR": "/d", "RUSTDESK_PATH": "/p"}, machine(), "/d"},
		{"then RUSTDESK_PATH", map[string]string{"RUSTDESK_PATH": "/p"}, machine("/opt/rustdesk/id_ed25519"), "/p"},
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
	}), machine())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KeysPath != "/k" || cfg.DBPath != "/x/db.sqlite3" || cfg.PubKeyPath != "/y/key.pub" || cfg.APIKeyPath != "/z/api" {
		t.Errorf("paths = %q %q %q %q", cfg.KeysPath, cfg.DBPath, cfg.PubKeyPath, cfg.APIKeyPath)
	}
}

func TestEnvAndComposePaths(t *testing.T) {
	cfg, err := load(env(map[string]string{"CONSOLE_DIR": "/opt/BetterDeskConsole"}), machine())
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
	}), machine())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SignalHost != "signal.example" || cfg.SignalPort != 31116 || cfg.RelayHost != "relay.example" || cfg.RelayPort != 41117 {
		t.Errorf("signal %s:%d, relay %s:%d", cfg.SignalHost, cfg.SignalPort, cfg.RelayHost, cfg.RelayPort)
	}
}

func TestAPIAddressDefaults(t *testing.T) {
	cfg, err := load(env(nil), machine())
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

// TestHostDefaultsToLoopback: without HOST the console listens on
// 127.0.0.1 only, as the old console did (config.js: HOST || '127.0.0.1').
func TestHostDefaultsToLoopback(t *testing.T) {
	for _, c := range []struct{ host, want string }{
		{"", "127.0.0.1"},
		{"  ", "127.0.0.1"},
		{"0.0.0.0", "0.0.0.0"},
		{"::", "::"},
		{" 10.0.0.5 ", "10.0.0.5"},
	} {
		cfg, err := load(env(map[string]string{"HOST": c.host}), machine())
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Host != c.want {
			t.Errorf("HOST %q: Host %q, want %q", c.host, cfg.Host, c.want)
		}
	}
}
