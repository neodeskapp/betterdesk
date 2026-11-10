package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// Config is the console's configuration.
type Config struct {
	// Host and Port are where the console listens (HOST, CONSOLE_PORT).
	// Port 0 means the Go console is off.
	Host string
	Port int

	// TrustProxy says which reverse proxies may speak for the client
	// (TRUST_PROXY).
	TrustProxy httpx.TrustProxy

	// NodeUpstream is the Node console's internal address while routes
	// move from Node to Go (CONSOLE_NODE_UPSTREAM). nil when Go serves
	// everything.
	NodeUpstream *url.URL

	// DebugRouting adds GET /__bd/routing, where the contract suite asks
	// whether Go or Node answers a route (CONSOLE_DEBUG_ROUTING=1). Test
	// stacks set it; an install does not.
	DebugRouting bool

	// NodeEnv is NODE_ENV, "development" when unset. Production is
	// NodeEnv == "production": error answers hide their detail, files are
	// read once.
	NodeEnv    string
	Production bool

	// Docker says the console runs in a container: /.dockerenv exists or
	// DOCKER is "true". It moves the default folders.
	Docker bool

	// ConsoleDir is the folder of the old console's files (lang, public,
	// protos, wallpapers): CONSOLE_DIR, else the first usual place that
	// has lang/en.json.
	ConsoleDir string
	// DataDir is where the console keeps what it writes: the session
	// secret, uploads (DATA_DIR).
	DataDir string
	// LangDir holds the <code>.json language files (CONSOLE_LANG_DIR).
	LangDir string
	// WebUIDir is the build of the web app (WEB_UI_DIR).
	WebUIDir string
	// PublicDir, ProtosDir, WallpapersDir and UploadsDir are the folders
	// the console serves as files.
	PublicDir     string
	ProtosDir     string
	WallpapersDir string
	UploadsDir    string

	// HTTPS is the console's HTTPS mode (HTTPS_ENABLED): it picks the
	// names of the session and CSRF cookies, marks them Secure and
	// tightens the security headers.
	HTTPS bool
	// HTTPSExact is true when HTTPS_ENABLED is "true" letter for letter,
	// which is what the language cookie's Secure attribute looks at.
	HTTPSExact bool
	// HSTSEnabled is the raw HSTS_ENABLED value and AllowSelfSignedCerts
	// is ALLOW_SELF_SIGNED_CERTS; together with HTTPS they decide whether
	// Strict-Transport-Security is sent (middleware.HSTSEnabled).
	HSTSEnabled          string
	AllowSelfSignedCerts bool

	// DefaultLanguage is the language of a request that names none
	// (DEFAULT_LANGUAGE).
	DefaultLanguage string
	// DeviceScopeDefault is DEVICE_SCOPE_DEFAULT in lower case: "open" or
	// "restricted".
	DeviceScopeDefault string
	// LogLevel is the raw LOG_LEVEL value (kit.NewLogger reads it).
	LogLevel string
	// APIHost is the host and port the console knows the API server by
	// (BETTERDESK_API_URL). Calls into the API carry it as Host.
	APIHost string

	// Getenv reads the settings that packages parse themselves: the
	// session secret and policy (package session) and the rate limits
	// (package limits).
	Getenv func(string) string
}

// probe is how Load looks at the machine. Tests replace it.
type probe struct {
	exists func(path string) bool
	// exeDir is the folder of the server's binary, cwd the working
	// directory; "" when unknown.
	exeDir, cwd string
	windows     bool
}

func systemProbe() probe {
	p := probe{
		exists: func(path string) bool {
			_, err := os.Stat(path)
			return err == nil
		},
		windows: runtime.GOOS == "windows",
	}
	if exe, err := os.Executable(); err == nil {
		p.exeDir = filepath.Dir(exe)
	}
	if cwd, err := os.Getwd(); err == nil {
		p.cwd = cwd
	}
	return p
}

// Load reads the configuration through getenv (os.Getenv in production).
func Load(getenv func(string) string) (*Config, error) {
	return load(getenv, systemProbe())
}

func load(getenv func(string) string, p probe) (*Config, error) {
	cfg := &Config{Host: strings.TrimSpace(getenv("HOST")), Getenv: getenv}
	if cfg.Host == "" {
		cfg.Host = "0.0.0.0"
	}

	if raw := strings.TrimSpace(getenv("CONSOLE_PORT")); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 0 || port > 65535 {
			return nil, fmt.Errorf("CONSOLE_PORT %q is not a port", raw)
		}
		cfg.Port = port
	}

	trust, err := httpx.ParseTrustProxy(getenv("TRUST_PROXY"))
	if err != nil {
		return nil, fmt.Errorf("TRUST_PROXY: %w", err)
	}
	cfg.TrustProxy = trust

	if raw := strings.TrimSpace(getenv("CONSOLE_NODE_UPSTREAM")); raw != "" {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("CONSOLE_NODE_UPSTREAM %q is not an http(s) URL", raw)
		}
		cfg.NodeUpstream = u
	}

	cfg.DebugRouting = strings.TrimSpace(getenv("CONSOLE_DEBUG_ROUTING")) == "1"

	cfg.NodeEnv = orDefault(getenv("NODE_ENV"), "development")
	cfg.Production = cfg.NodeEnv == "production"
	cfg.Docker = getenv("DOCKER") == "true" || p.exists("/.dockerenv")

	cfg.ConsoleDir = consoleDir(getenv, p, cfg.Docker)
	cfg.DataDir = getenv("DATA_DIR")
	if cfg.DataDir == "" {
		cfg.DataDir = filepath.Join(cfg.ConsoleDir, "data")
		if cfg.Docker {
			cfg.DataDir = "/app/data"
		}
	}
	cfg.LangDir = orDefault(getenv("CONSOLE_LANG_DIR"), filepath.Join(cfg.ConsoleDir, "lang"))
	cfg.WebUIDir = webUIDir(getenv, p, cfg.ConsoleDir)
	cfg.PublicDir = filepath.Join(cfg.ConsoleDir, "public")
	cfg.ProtosDir = filepath.Join(cfg.ConsoleDir, "protos")
	cfg.WallpapersDir = filepath.Join(cfg.ConsoleDir, "wallpapers")
	cfg.UploadsDir = filepath.Join(cfg.DataDir, "uploads")

	cfg.HTTPS = isTrue(getenv("HTTPS_ENABLED"))
	cfg.HTTPSExact = getenv("HTTPS_ENABLED") == "true"
	cfg.HSTSEnabled = getenv("HSTS_ENABLED")
	cfg.AllowSelfSignedCerts = isTrue(getenv("ALLOW_SELF_SIGNED_CERTS"))

	cfg.DefaultLanguage = orDefault(getenv("DEFAULT_LANGUAGE"), "en")
	cfg.DeviceScopeDefault = strings.ToLower(orDefault(getenv("DEVICE_SCOPE_DEFAULT"), "open"))
	cfg.LogLevel = getenv("LOG_LEVEL")
	cfg.APIHost = apiHost(getenv)
	return cfg, nil
}

// Addr is the listen address.
func (c *Config) Addr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// isTrue is the old console's reading of a switch: "true" in any letter
// case, nothing else.
func isTrue(value string) bool { return strings.ToLower(value) == "true" }

// consoleDir finds the folder of the old console's files: CONSOLE_DIR,
// else the first usual place that has lang/en.json, else the first usual
// place.
func consoleDir(getenv func(string) string, p probe, docker bool) string {
	if dir := getenv("CONSOLE_DIR"); dir != "" {
		return dir
	}
	var places []string
	if docker {
		places = append(places, "/app")
	}
	for _, base := range []string{p.exeDir, p.cwd} {
		if base != "" {
			places = append(places, filepath.Join(base, "..", "web-nodejs"))
		}
	}
	if p.windows {
		places = append(places, `C:\BetterDeskConsole`)
	} else {
		places = append(places, "/opt/BetterDeskConsole", "/opt/betterdesk", "/var/lib/betterdesk")
	}
	for _, place := range places {
		if p.exists(filepath.Join(place, "lang", "en.json")) {
			return place
		}
	}
	return places[0]
}

// webUIDir finds the build of the web app: WEB_UI_DIR, else web-ui/build
// next to the old console's folder (the Docker image and a source tree),
// else inside it (where the installer builds it), whichever has an
// index.html.
func webUIDir(getenv func(string) string, p probe, consoleDir string) string {
	if dir := getenv("WEB_UI_DIR"); dir != "" {
		return dir
	}
	beside := filepath.Join(consoleDir, "..", "web-ui", "build")
	inside := filepath.Join(consoleDir, "web-ui", "build")
	if !p.exists(filepath.Join(beside, "index.html")) && p.exists(filepath.Join(inside, "index.html")) {
		return inside
	}
	return beside
}

// apiHost is the host and port of the API address the old console was
// configured with: BETTERDESK_API_URL, else HBBS_API_URL, else
// 127.0.0.1:21114, as that address writes them.
func apiHost(getenv func(string) string) string {
	raw := orDefault(getenv("BETTERDESK_API_URL"), getenv("HBBS_API_URL"))
	if raw == "" {
		return "127.0.0.1:21114"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "127.0.0.1:21114"
	}
	return u.Host
}
