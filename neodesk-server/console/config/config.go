package config

import (
	"fmt"
	"io/fs"
	"math"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/static"
	"github.com/neodeskapp/neodesk/neodesk-server/internal/envvar"
	"github.com/neodeskapp/neodesk/neodesk-server/internal/productversion"
)

// Config is the console's configuration.
type Config struct {
	// Host and Port are where the console listens (HOST, CONSOLE_PORT;
	// 5000 when CONSOLE_PORT is unset). Port 0 means the Go console is off.
	Host string
	Port int
	// PortUnset says CONSOLE_PORT was not set: the console is on only
	// because A27 turns it on, so a port that is taken leaves the server
	// running without its console (DECISIONS A62).
	PortUnset bool

	// TrustProxy says which reverse proxies may speak for the client
	// (TRUST_PROXY).
	TrustProxy httpx.TrustProxy

	// DebugRouting adds GET /__bd/routing, where the contract suite asks
	// who answers a route (CONSOLE_DEBUG_ROUTING on). Test stacks set it;
	// an install does not.
	DebugRouting bool

	// NodeEnv is NODE_ENV, "development" when unset. Production is
	// NodeEnv == "production": error answers hide their detail, files are
	// read once.
	NodeEnv    string
	Production bool

	// Docker says the console runs in a container: /.dockerenv exists or
	// DOCKER is on. It moves the default folders.
	Docker bool

	// ConsoleDir is the folder that holds the console's state: its .env
	// and data/ (CONSOLE_DIR, else the first usual place that has a .env
	// or the language files of a Node console install, else the first
	// usual place).
	ConsoleDir string
	// DataDir is where the console keeps what it writes: the session
	// secret, uploads, installed fonts (DATA_DIR).
	DataDir string
	// Version is the product version the console reports: the one built
	// into the binary.
	Version string

	// StaticDir is a folder on disk with the console's own files in the
	// shape of console/static/files (CONSOLE_STATIC_DIR), read in place of
	// the files in the binary, for development.
	StaticDir string
	// Static is the console's own files in the binary (package static).
	// The folders and files below that are empty are read from it. With
	// CONSOLE_STATIC_DIR they name its folders and Static is nil.
	Static fs.FS
	// LangDir holds the <code>.json language files on disk
	// (CONSOLE_LANG_DIR, else lang in StaticDir). Empty: lang in Static.
	LangDir string
	// PublicDir and ProtosDir are served as files at the root and at
	// /protos: public and protos in StaticDir, or empty for Static's.
	PublicDir string
	ProtosDir string
	// The branding's built-in files: the theme presets (themes), the
	// palettes (config/theme-palettes.json), the default favicon
	// (public/favicon.svg) and the supporters list
	// (config/supporters.json), in StaticDir or, when empty, in Static.
	ThemesDir      string
	PalettesFile   string
	FaviconFile    string
	SupportersFile string

	// WebUIDir is a build of the web app on disk (WEB_UI_DIR), for
	// development. Empty means the build in the binary.
	WebUIDir string
	// WallpapersDir and UploadsDir are folders on disk the console serves
	// as files: wallpapers in ConsoleDir and uploads in DataDir.
	WallpapersDir string
	UploadsDir    string
	// TransfersDir is where the files of file transfers are written:
	// data/transfers in ConsoleDir, whatever DATA_DIR says.
	TransfersDir string
	// The branding's state: the installed fonts (fonts in DataDir), and in
	// ConsoleDir whatever DATA_DIR says the optional font list
	// (data/google-fonts-cache.json) and uploaded fonts before they are
	// installed (data/tmp). OldFontsDir is where the Node console
	// installed fonts (public/fonts in ConsoleDir); the console copies it
	// into FontsDir once.
	FontsDir      string
	OldFontsDir   string
	FontCacheFile string
	TempDir       string

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
	// HSTSEnabled is HSTS_ENABLED: "true" or "false" for an on or off
	// value, as written otherwise. AllowSelfSignedCerts is
	// ALLOW_SELF_SIGNED_CERTS. Together with HTTPS they decide whether
	// Strict-Transport-Security is sent (middleware.HSTSEnabled).
	HSTSEnabled          string
	AllowSelfSignedCerts bool
	// SMTPTLSVerify says whether the mail server's certificate is checked
	// when the saved SMTP settings do not say: yes unless SMTP_TLS_VERIFY
	// is off.
	SMTPTLSVerify bool
	// SMTPRequireTLS says whether a login to the mail server has to go
	// over TLS (STARTTLS or a TLS port): yes unless SMTP_REQUIRE_TLS is
	// off, for a mail server on a trusted network.
	SMTPRequireTLS bool

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
	// (NEODESK_API_URL). Calls into the API carry it as Host.
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
	// (NEODESK_API_URL, else HBBS_API_URL, else
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
	// ClientAPITLS is RUSTDESK_API_TLS in lower case, "auto" when unset and
	// "false" for any off value: "false" and "http" keep the client API
	// port on plain HTTP; anything else gives it TLS when the certificate
	// loads.
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

	// GeneratorEmbedded is NEODESK_CLIENT_GENERATOR_MODE=embedded (any
	// letter case, space around it): the support generator builds the
	// client from source instead of using the downloaded templates.
	GeneratorEmbedded bool
	// AgentBuildWorker is false when AGENT_BUILD_WORKER is off.
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
	exists  func(path string) bool
	windows bool
	// isSet reports whether a variable is set, also to the empty text.
	// nil counts a variable as set when getenv gives more than "".
	isSet func(name string) bool
}

func systemProbe() probe {
	return probe{
		exists: func(path string) bool {
			_, err := os.Stat(path)
			return err == nil
		},
		windows: runtime.GOOS == "windows",
	}
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
	cfg.PortUnset = strings.TrimSpace(getenv("CONSOLE_PORT")) == ""
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

	cfg.DebugRouting = envvar.SwitchOr(getenv("CONSOLE_DEBUG_ROUTING"), false)

	cfg.NodeEnv = orDefault(getenv("NODE_ENV"), "development")
	cfg.Production = cfg.NodeEnv == "production"
	cfg.Docker = envvar.SwitchOr(getenv("DOCKER"), false) || p.exists("/.dockerenv")

	cfg.ConsoleDir = consoleDir(getenv, p, cfg.Docker)
	cfg.DataDir = getenv("DATA_DIR")
	if cfg.DataDir == "" {
		cfg.DataDir = filepath.Join(cfg.ConsoleDir, "data")
		if cfg.Docker {
			cfg.DataDir = "/app/data"
		}
	}
	cfg.Version = productversion.Product()

	if cfg.StaticDir = getenv("CONSOLE_STATIC_DIR"); cfg.StaticDir != "" {
		cfg.LangDir = filepath.Join(cfg.StaticDir, "lang")
		cfg.PublicDir = filepath.Join(cfg.StaticDir, "public")
		cfg.ProtosDir = filepath.Join(cfg.StaticDir, "protos")
		cfg.ThemesDir = filepath.Join(cfg.StaticDir, "themes")
		cfg.PalettesFile = filepath.Join(cfg.StaticDir, "config", "theme-palettes.json")
		cfg.FaviconFile = filepath.Join(cfg.StaticDir, "public", "favicon.svg")
		cfg.SupportersFile = filepath.Join(cfg.StaticDir, "config", "supporters.json")
	} else {
		tree, err := static.Files()
		if err != nil {
			return nil, fmt.Errorf("the console's files in the binary: %w", err)
		}
		cfg.Static = tree
	}
	cfg.LangDir = orDefault(getenv("CONSOLE_LANG_DIR"), cfg.LangDir)

	cfg.WebUIDir = getenv("WEB_UI_DIR")
	cfg.WallpapersDir = filepath.Join(cfg.ConsoleDir, "wallpapers")
	cfg.UploadsDir = filepath.Join(cfg.DataDir, "uploads")
	cfg.TransfersDir = filepath.Join(cfg.ConsoleDir, "data", "transfers")
	cfg.FontsDir = filepath.Join(cfg.DataDir, "fonts")
	cfg.OldFontsDir = filepath.Join(cfg.ConsoleDir, "public", "fonts")
	cfg.FontCacheFile = filepath.Join(cfg.ConsoleDir, "data", "google-fonts-cache.json")
	cfg.TempDir = filepath.Join(cfg.ConsoleDir, "data", "tmp")

	cfg.HTTPS = isOn(getenv("HTTPS_ENABLED"))
	cfg.HTTPSPort = parseIntOr(getenv("HTTPS_PORT"), 5443)
	cfg.HTTPRedirect = isOn(orDefault(getenv("HTTP_REDIRECT_HTTPS"), "true"))
	cfg.CertFile = getenv("SSL_CERT_PATH")
	cfg.KeyFile = getenv("SSL_KEY_PATH")
	cfg.CAFile = getenv("SSL_CA_PATH")
	cfg.HSTSEnabled = switchText(getenv("HSTS_ENABLED"))
	cfg.AllowSelfSignedCerts = isOn(getenv("ALLOW_SELF_SIGNED_CERTS"))
	cfg.SMTPTLSVerify = envvar.SwitchOr(getenv("SMTP_TLS_VERIFY"), true)
	cfg.SMTPRequireTLS = envvar.SwitchOr(getenv("SMTP_REQUIRE_TLS"), true)

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
	cfg.APIURL = orDefault(orDefault(getenv("NEODESK_API_URL"), getenv("HBBS_API_URL")), defaultAPIURL)
	cfg.GoAPIPort = parseIntOr(getenv("GO_API_PORT"), apiURLPort(cfg.APIURL, defaultGoAPIPort))
	cfg.APIPort = parseIntOr(getenv("API_PORT"), 21121)
	cfg.APIEnabled = isOn(nullish(getenv, p, "API_ENABLED", "true"))
	cfg.ClientAPIHost = orDefault(getenv("API_HOST"), "0.0.0.0")
	cfg.ClientAPIProxy = isOn(nullish(getenv, p, "RUSTDESK_API_PROXY", "true"))
	cfg.ClientAPITLS = strings.ToLower(orDefault(getenv("RUSTDESK_API_TLS"), "auto"))
	if on, ok := envvar.Switch(cfg.ClientAPITLS); ok && !on {
		cfg.ClientAPITLS = "false"
	}
	cfg.SignalHost = orDefault(getenv("WS_HBBS_HOST"), "localhost")
	cfg.SignalPort = parseIntOr(getenv("WS_HBBS_PORT"), 21116)
	cfg.RelayHost = orDefault(getenv("WS_HBBR_HOST"), "localhost")
	cfg.RelayPort = parseIntOr(getenv("WS_HBBR_PORT"), 21117)
	cfg.EnvPath = filepath.Join(cfg.ConsoleDir, ".env")
	cfg.ComposePath = filepath.Join(cfg.ConsoleDir, "..", "docker-compose.yml")
	cfg.GeneratorEmbedded = strings.ToLower(strings.TrimSpace(orDefault(getenv("NEODESK_CLIENT_GENERATOR_MODE"), "download"))) == "embedded"
	cfg.AgentBuildWorker = envvar.SwitchOr(getenv("AGENT_BUILD_WORKER"), true)
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

// isOn reads a switch that only an on value turns on (envvar.Switch):
// anything else, the empty text included, is off.
func isOn(value string) bool { return envvar.SwitchOr(value, false) }

// switchText is "true" for an on value, "false" for an off value and the
// value itself otherwise.
func switchText(value string) string {
	on, ok := envvar.Switch(value)
	switch {
	case !ok:
		return value
	case on:
		return "true"
	}
	return "false"
}

// consoleDir finds the folder that holds the console's state:
// CONSOLE_DIR, else the first usual place that has a .env or, where a
// Node console was installed, lang/en.json, else the first usual place.
func consoleDir(getenv func(string) string, p probe, docker bool) string {
	if dir := getenv("CONSOLE_DIR"); dir != "" {
		return dir
	}
	var places []string
	if docker {
		places = append(places, "/app")
	}
	if p.windows {
		places = append(places, `C:\NeoDeskConsole`)
	} else {
		places = append(places, "/opt/NeoDeskConsole", "/opt/neodesk", "/var/lib/neodesk")
	}
	for _, place := range places {
		if p.exists(filepath.Join(place, ".env")) || p.exists(filepath.Join(place, "lang", "en.json")) {
			return place
		}
	}
	return places[0]
}

// apiHost is the host and port of the API address the old console was
// configured with: NEODESK_API_URL, else HBBS_API_URL, else
// 127.0.0.1:21114, as that address writes them.
func apiHost(getenv func(string) string) string {
	raw := orDefault(getenv("NEODESK_API_URL"), getenv("HBBS_API_URL"))
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
	places, separator := []string{"/opt/neodesk", "/opt/rustdesk"}, "/"
	if p.windows {
		places, separator = []string{`C:\NeoDesk`, `C:\RustDesk`}, `\`
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
