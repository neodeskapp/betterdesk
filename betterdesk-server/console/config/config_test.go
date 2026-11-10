package config

import (
	"path/filepath"
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
