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

	// KeysPath is the folder the server writes its keys to (KEYS_PATH,
	// RUSTDESK_DIR, RUSTDESK_PATH, else the first usual place).
	KeysPath string
	// DBPath is the database file as the old console named it (DB_PATH,
	// else db_v2.sqlite3 in KeysPath). It is only reported.
	DBPath string
	// PubKeyPath and APIKeyPath are the server's public key file and the
	// API key file (PUB_KEY_PATH, API_KEY_PATH, else id_ed25519.pub and
	// .api_key in KeysPath).
	PubKeyPath string
	APIKeyPath string
	// APIURL is the address of the server's API as configured
	// (BETTERDESK_API_URL, else HBBS_API_URL, else
	// http://127.0.0.1:21114/api), as written.
	APIURL string
	// GoAPIPort is the port of the server's API: GO_API_PORT, else the
	// port of APIURL, else 21114.
	GoAPIPort int
	// APIPort is the client API port (API_PORT, default 21121), and
	// APIEnabled says whether the old console listened on it
	// (API_ENABLED).
	APIPort    int
	APIEnabled bool
	// SignalHost and SignalPort are where the console reaches the signal
	// server, RelayHost and RelayPort the relay (WS_HBBS_HOST,
	// WS_HBBS_PORT, WS_HBBR_HOST, WS_HBBR_PORT).
	SignalHost string
	SignalPort int
	RelayHost  string
	RelayPort  int
	// EnvPath is the old console's own .env file and ComposePath the
	// compose file in the folder above the console.
	EnvPath     string
	ComposePath string

	// Getenv reads the settings that packages parse themselves: the
	// session secret and policy (package session), the rate limits
	// (package limits) and the sign-in settings (package authn).
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

	cfg.KeysPath = keysPath(getenv, p, cfg.Docker)
	cfg.DBPath = orDefault(getenv("DB_PATH"), filepath.Join(cfg.KeysPath, "db_v2.sqlite3"))
	cfg.PubKeyPath = orDefault(getenv("PUB_KEY_PATH"), filepath.Join(cfg.KeysPath, "id_ed25519.pub"))
	cfg.APIKeyPath = orDefault(getenv("API_KEY_PATH"), filepath.Join(cfg.KeysPath, ".api_key"))
	cfg.APIURL = orDefault(orDefault(getenv("BETTERDESK_API_URL"), getenv("HBBS_API_URL")), defaultAPIURL)
	cfg.GoAPIPort = parseIntOr(getenv("GO_API_PORT"), apiURLPort(cfg.APIURL, defaultGoAPIPort))
	cfg.APIPort = parseIntOr(getenv("API_PORT"), 21121)
	cfg.APIEnabled = isTrue(orDefault(getenv("API_ENABLED"), "true"))
	cfg.SignalHost = orDefault(getenv("WS_HBBS_HOST"), "localhost")
	cfg.SignalPort = parseIntOr(getenv("WS_HBBS_PORT"), 21116)
	cfg.RelayHost = orDefault(getenv("WS_HBBR_HOST"), "localhost")
	cfg.RelayPort = parseIntOr(getenv("WS_HBBR_PORT"), 21117)
	cfg.EnvPath = filepath.Join(cfg.ConsoleDir, ".env")
	cfg.ComposePath = filepath.Join(cfg.ConsoleDir, "..", "docker-compose.yml")
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

// The API address and port of an install that sets neither.
const (
	defaultAPIURL    = "http://127.0.0.1:21114/api"
	defaultGoAPIPort = 21114
)

// keysPath finds the folder the server keeps its keys in: KEYS_PATH, else
// RUSTDESK_DIR, else RUSTDESK_PATH, else /opt/rustdesk in a container,
// else the first of the usual two folders that has the private key
// (id_ed25519), else the newer of the two.
func keysPath(getenv func(string) string, p probe, docker bool) string {
	for _, name := range []string{"KEYS_PATH", "RUSTDESK_DIR", "RUSTDESK_PATH"} {
		if dir := getenv(name); dir != "" {
			return dir
		}
	}
	if docker {
		return "/opt/rustdesk"
	}
	places, separator := []string{"/opt/betterdesk", "/opt/rustdesk"}, "/"
	if p.windows {
		places, separator = []string{`C:\BetterDesk`, `C:\RustDesk`}, `\`
	}
	for _, place := range places {
		if p.exists(place + separator + "id_ed25519") {
			return place
		}
	}
	return places[0]
}

// parseIntOr is parseInt(s, 10) || fallback, which is how the old console
// read its numeric settings: leading spaces and a sign are fine, digits
// are read up to the first other character, and a value that is not a
// number or is zero gives the fallback.
func parseIntOr(s string, fallback int) int {
	s = strings.TrimLeft(s, " \t\n\v\f\r")
	negative := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		negative = s[0] == '-'
		s = s[1:]
	}
	const ceiling = 1 << 53
	n, digits := 0, 0
	for digits < len(s) && s[digits] >= '0' && s[digits] <= '9' {
		if n < ceiling {
			n = n*10 + int(s[digits]-'0')
		}
		digits++
	}
	if digits == 0 || n == 0 {
		return fallback
	}
	if negative {
		return -n
	}
	return n
}

// apiURLPort is the port of an API address as the old console read it:
// the one the address names, else 443 for https and 80 for any other
// scheme, and fallback for a text that is not an address.
func apiURLPort(raw string, fallback int) int {
	raw = strings.TrimFunc(raw, func(r rune) bool { return r <= ' ' })
	raw = strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return fallback
	}
	scheme := strings.ToLower(u.Scheme)
	standard, known := schemePorts[scheme]
	if known && u.Hostname() == "" {
		return fallback
	}
	if text := u.Port(); text != "" {
		port, err := strconv.Atoi(text)
		if err != nil || port > 65535 {
			return fallback
		}
		if !known || port != standard {
			return port
		}
	}
	if scheme == "https" {
		return 443
	}
	return 80
}

// schemePorts are the ports an address of these schemes need not name.
var schemePorts = map[string]int{"ftp": 21, "http": 80, "https": 443, "ws": 80, "wss": 443}
