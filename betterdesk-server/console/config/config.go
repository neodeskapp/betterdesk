package config

import (
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// Config is the console's configuration.
type Config struct {
	// Host and Port are where the console listens (HOST, CONSOLE_PORT;
	// 5000 when CONSOLE_PORT is unset). Port 0 means the Go console is off.
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
	// WebUIDir is a build of the web app on disk (WEB_UI_DIR), for
	// development. Empty means the build in the binary.
	WebUIDir string
	// PublicDir, ProtosDir, WallpapersDir and UploadsDir are the folders
	// the console serves as files.
	PublicDir     string
	ProtosDir     string
	WallpapersDir string
	UploadsDir    string
	// TransfersDir is where the files of file transfers are written:
	// data/transfers in ConsoleDir, whatever DATA_DIR says.
	TransfersDir string
	// The branding's files, in ConsoleDir whatever DATA_DIR says: the
	// installed fonts (public/fonts), the optional font list
	// (data/google-fonts-cache.json), uploaded fonts before they are
	// installed (data/tmp), the built-in themes (themes), the built-in
	// palettes (config/theme-palettes.json), the default favicon
	// (public/favicon.svg) and the supporters list (config/supporters.json).
	FontsDir       string
	FontCacheFile  string
	TempDir        string
	ThemesDir      string
	PalettesFile   string
	FaviconFile    string
	SupportersFile string

	// HTTPS is the console's HTTPS mode (HTTPS_ENABLED): it picks the
	// names of the session and CSRF cookies, marks them Secure and
	// tightens the security headers. With a certificate the console then
	// listens with TLS on HTTPSPort.
	HTTPS bool
	// HTTPSPort is the port of the TLS listener in HTTPS mode (HTTPS_PORT,
	// default 5443). Port is then the port of the redirect to it.
	HTTPSPort int
	// HTTPRedirect says whether Port redirects to HTTPSPort in HTTPS mode
	// (HTTP_REDIRECT_HTTPS, default true); links in mails start with https
	// when it is on.
	HTTPRedirect bool
	// CertFile, KeyFile and CAFile are the certificate, its private key and
	// an optional bundle of the certificates that issued it
	// (SSL_CERT_PATH, SSL_KEY_PATH, SSL_CA_PATH). The console and the
	// client API port use them.
	CertFile string
	KeyFile  string
	CAFile   string
	// HTTPSExact is true when HTTPS_ENABLED is "true" letter for letter,
	// which is what the language cookie's Secure attribute looks at.
	HTTPSExact bool
	// HSTSEnabled is the raw HSTS_ENABLED value and AllowSelfSignedCerts
	// is ALLOW_SELF_SIGNED_CERTS; together with HTTPS they decide whether
	// Strict-Transport-Security is sent (middleware.HSTSEnabled).
	HSTSEnabled          string
	AllowSelfSignedCerts bool
	// SMTPTLSVerify says whether the mail server's certificate is checked
	// when the saved SMTP settings do not say (SMTP_TLS_VERIFY).
	SMTPTLSVerify bool

	// DefaultLanguage is the language of a request that names none
	// (DEFAULT_LANGUAGE).
	DefaultLanguage string
	// DeviceScopeDefault is DEVICE_SCOPE_DEFAULT in lower case: "open" or
	// "restricted".
	DeviceScopeDefault string
	// PanelGroupingNode is BACKEND_PANEL_GROUPING=node (any letter case,
	// space around it): the device group writes go to the console's own
	// tables. With any other value, or none, the API server writes them
	// (its /api/panel/device-groups routes), and the console's own writer
	// is the fallback for an API server without those routes.
	PanelGroupingNode bool
	// PanelTrust says PANEL_TRUST_SECRET holds more than white space, both
	// as JavaScript trims it (where the Node console signed) and as Go
	// trims it (where the API server checks, api.Server.SetPanelTrustSecret):
	// the device group writes then reach the API server as the signed-in
	// operator instead of as the console's API key. The console signs
	// nothing, since it calls the API server in process.
	PanelTrust bool
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
	// APIEnabled says whether the console listens on it (API_ENABLED,
	// default true).
	APIPort    int
	APIEnabled bool
	// ClientAPIHost is the address the client API port listens on
	// (API_HOST, default 0.0.0.0).
	ClientAPIHost string
	// ClientAPIProxy says whether the client API port passes the client
	// API on to the server's API (RUSTDESK_API_PROXY, default true) or
	// answers it from the console's own routes.
	ClientAPIProxy bool
	// ClientAPITLS is RUSTDESK_API_TLS in lower case, "auto" when unset:
	// false, 0, off and http keep the client API port on plain HTTP;
	// anything else gives it TLS when the certificate loads.
	ClientAPITLS string
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

	// GeneratorEmbedded is BETTERDESK_CLIENT_GENERATOR_MODE=embedded (any
	// letter case, space around it): the support generator builds the
	// client from source instead of using the downloaded templates.
	GeneratorEmbedded bool
	// AgentBuildWorker is false with AGENT_BUILD_WORKER=off.
	AgentBuildWorker bool
	// AgentBuildPoll and AgentBuildCooldown are AGENT_BUILD_POLL_MS
	// (default 5000) and AGENT_BUILD_COOLDOWN_MS (default 1000). A poll
	// value that is zero, negative or no number is one millisecond, as a
	// timer took it; a cooldown like that is none.
	AgentBuildPoll     time.Duration
	AgentBuildCooldown time.Duration
	// AgentArtifactDir is where the finished installers are kept
	// (AGENT_ARTIFACT_DIR, default DataDir/agent-builds).
	AgentArtifactDir string

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
	// isSet reports whether a variable is set, also to the empty text.
	// nil counts a variable as set when getenv gives more than "".
	isSet func(name string) bool
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

// Load reads the configuration through getenv. A variable set to the
// empty text counts as unset.
func Load(getenv func(string) string) (*Config, error) {
	return load(getenv, systemProbe())
}

// LoadEnv reads the configuration from the process environment. It tells
// a variable set to the empty text from an unset one, as the old console
// did for API_ENABLED and RUSTDESK_API_PROXY.
func LoadEnv() (*Config, error) {
	p := systemProbe()
	p.isSet = func(name string) bool {
		_, ok := os.LookupEnv(name)
		return ok
	}
	return load(os.Getenv, p)
}

func load(getenv func(string) string, p probe) (*Config, error) {
	cfg := &Config{Host: strings.TrimSpace(getenv("HOST")), Getenv: getenv}
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}

	cfg.Port = defaultConsolePort
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
	cfg.WebUIDir = getenv("WEB_UI_DIR")
	cfg.PublicDir = filepath.Join(cfg.ConsoleDir, "public")
	cfg.ProtosDir = filepath.Join(cfg.ConsoleDir, "protos")
	cfg.WallpapersDir = filepath.Join(cfg.ConsoleDir, "wallpapers")
	cfg.UploadsDir = filepath.Join(cfg.DataDir, "uploads")
	cfg.TransfersDir = filepath.Join(cfg.ConsoleDir, "data", "transfers")
	cfg.FontsDir = filepath.Join(cfg.ConsoleDir, "public", "fonts")
	cfg.FontCacheFile = filepath.Join(cfg.ConsoleDir, "data", "google-fonts-cache.json")
	cfg.TempDir = filepath.Join(cfg.ConsoleDir, "data", "tmp")
	cfg.ThemesDir = filepath.Join(cfg.ConsoleDir, "themes")
	cfg.PalettesFile = filepath.Join(cfg.ConsoleDir, "config", "theme-palettes.json")
	cfg.FaviconFile = filepath.Join(cfg.ConsoleDir, "public", "favicon.svg")
	cfg.SupportersFile = filepath.Join(cfg.ConsoleDir, "config", "supporters.json")

	cfg.HTTPS = isTrue(getenv("HTTPS_ENABLED"))
	cfg.HTTPSExact = getenv("HTTPS_ENABLED") == "true"
	cfg.HTTPSPort = parseIntOr(getenv("HTTPS_PORT"), 5443)
	cfg.HTTPRedirect = isTrue(orDefault(getenv("HTTP_REDIRECT_HTTPS"), "true"))
	cfg.CertFile = getenv("SSL_CERT_PATH")
	cfg.KeyFile = getenv("SSL_KEY_PATH")
	cfg.CAFile = getenv("SSL_CA_PATH")
	cfg.HSTSEnabled = getenv("HSTS_ENABLED")
	cfg.AllowSelfSignedCerts = isTrue(getenv("ALLOW_SELF_SIGNED_CERTS"))
	cfg.SMTPTLSVerify = isTrue(getenv("SMTP_TLS_VERIFY"))

	cfg.DefaultLanguage = orDefault(getenv("DEFAULT_LANGUAGE"), "en")
	cfg.DeviceScopeDefault = strings.ToLower(orDefault(getenv("DEVICE_SCOPE_DEFAULT"), "open"))
	cfg.PanelGroupingNode = strings.ToLower(jsTrim(getenv("BACKEND_PANEL_GROUPING"))) == "node"
	secret := getenv("PANEL_TRUST_SECRET")
	cfg.PanelTrust = jsTrim(secret) != "" && strings.TrimSpace(secret) != ""
	cfg.LogLevel = getenv("LOG_LEVEL")
	cfg.APIHost = apiHost(getenv)

	cfg.KeysPath = keysPath(getenv, p, cfg.Docker)
	cfg.DBPath = orDefault(getenv("DB_PATH"), filepath.Join(cfg.KeysPath, "db_v2.sqlite3"))
	cfg.PubKeyPath = orDefault(getenv("PUB_KEY_PATH"), filepath.Join(cfg.KeysPath, "id_ed25519.pub"))
	cfg.APIKeyPath = orDefault(getenv("API_KEY_PATH"), filepath.Join(cfg.KeysPath, ".api_key"))
	cfg.APIURL = orDefault(orDefault(getenv("BETTERDESK_API_URL"), getenv("HBBS_API_URL")), defaultAPIURL)
	cfg.GoAPIPort = parseIntOr(getenv("GO_API_PORT"), apiURLPort(cfg.APIURL, defaultGoAPIPort))
	cfg.APIPort = parseIntOr(getenv("API_PORT"), 21121)
	cfg.APIEnabled = isTrue(nullish(getenv, p, "API_ENABLED", "true"))
	cfg.ClientAPIHost = orDefault(getenv("API_HOST"), "0.0.0.0")
	cfg.ClientAPIProxy = isTrue(nullish(getenv, p, "RUSTDESK_API_PROXY", "true"))
	cfg.ClientAPITLS = strings.ToLower(orDefault(getenv("RUSTDESK_API_TLS"), "auto"))
	cfg.SignalHost = orDefault(getenv("WS_HBBS_HOST"), "localhost")
	cfg.SignalPort = parseIntOr(getenv("WS_HBBS_PORT"), 21116)
	cfg.RelayHost = orDefault(getenv("WS_HBBR_HOST"), "localhost")
	cfg.RelayPort = parseIntOr(getenv("WS_HBBR_PORT"), 21117)
	cfg.EnvPath = filepath.Join(cfg.ConsoleDir, ".env")
	cfg.ComposePath = filepath.Join(cfg.ConsoleDir, "..", "docker-compose.yml")
	cfg.GeneratorEmbedded = strings.ToLower(strings.TrimSpace(orDefault(getenv("BETTERDESK_CLIENT_GENERATOR_MODE"), "download"))) == "embedded"
	cfg.AgentBuildWorker = getenv("AGENT_BUILD_WORKER") != "off"
	cfg.AgentBuildPoll = timerDelay(orDefault(getenv("AGENT_BUILD_POLL_MS"), "5000"))
	if ms, ok := parseInt(orDefault(getenv("AGENT_BUILD_COOLDOWN_MS"), "1000")); ok && ms > 0 {
		cfg.AgentBuildCooldown = time.Duration(ms) * time.Millisecond
	}
	cfg.AgentArtifactDir = orDefault(getenv("AGENT_ARTIFACT_DIR"), filepath.Join(cfg.DataDir, "agent-builds"))
	return cfg, nil
}

// parseInt is parseInt(s, 10): ok is false where it gives NaN.
func parseInt(s string) (int64, bool) {
	s = strings.TrimLeft(s, " \t\n\v\f\r")
	negative := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		negative = s[0] == '-'
		s = s[1:]
	}
	var n int64
	digits := 0
	for digits < len(s) && s[digits] >= '0' && s[digits] <= '9' {
		if n < 1<<53 {
			n = n*10 + int64(s[digits]-'0')
		}
		digits++
	}
	if digits == 0 {
		return 0, false
	}
	if negative {
		n = -n
	}
	return n, true
}

// timerDelay is the delay setInterval made of parseInt(s, 10): one
// millisecond for no number, zero, a negative one or one beyond 2^31-1.
func timerDelay(s string) time.Duration {
	ms, ok := parseInt(s)
	if !ok || ms < 1 || ms > 1<<31-1 {
		return time.Millisecond
	}
	return time.Duration(ms) * time.Millisecond
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

// nullish is process.env[name] ?? fallback: the fallback only for a
// variable that is not set at all.
func nullish(getenv func(string) string, p probe, name, fallback string) string {
	value := getenv(name)
	if value != "" {
		return value
	}
	if p.isSet != nil && p.isSet(name) {
		return ""
	}
	return fallback
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

// defaultConsolePort is the console's port when CONSOLE_PORT is unset
// (DECISIONS A27): the port the old console had.
const defaultConsolePort = 5000

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
	// Digits past 2^53 change nothing, as in a JavaScript number; the
	// result is held to what an int holds on this platform
	const ceiling = int64(1) << 53
	var n int64
	digits := 0
	for digits < len(s) && s[digits] >= '0' && s[digits] <= '9' {
		if n < ceiling {
			n = n*10 + int64(s[digits]-'0')
		}
		digits++
	}
	if digits == 0 || n == 0 {
		return fallback
	}
	n = min(n, int64(math.MaxInt))
	if negative {
		return -int(n)
	}
	return int(n)
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

// jsTrim is String.prototype.trim: it removes JavaScript's white space and
// line terminators from both ends.
func jsTrim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		switch r {
		case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
			return true
		}
		return r >= 0x2000 && r <= 0x200A
	})
}
