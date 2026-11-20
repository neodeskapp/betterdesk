// Package managedconfig reads and writes the allowlisted keys of the
// console's env file (lib/managementCapabilities.js of the old console):
// the keys an operator may set from the panel or the command line, the
// checks each value has to pass, the atomic write with a backup, the
// restore of that backup, and the masking of secrets.
//
// The allowlist is compiled in (allowlist.go): every key the template
// .env.example names, set or commented out. The old console read the
// template from its folder at every request.
package managedconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/jsbind"
)

// Paths are the folders and files the management routes report and
// check, in the order the old console sent them.
type Paths struct {
	ConsolePath string `json:"consolePath"`
	ServerPath  string `json:"serverPath"`
	DataPath    string `json:"dataPath"`
	BackupPath  string `json:"backupPath"`
	EnvPath     string `json:"envPath"`
	ExamplePath string `json:"examplePath"`
}

// Options are the settings of a Config.
type Options struct {
	// ConsoleDir is the console's folder (CONSOLE_DIR). The environment
	// variable BETTERDESK_CONSOLE_PATH wins over it.
	ConsoleDir string
	// EnvFile is the env file the process loaded (BETTERDESK_ENV_FILE).
	// Empty means the file .env in the console's folder.
	EnvFile string
	// Getenv reads BETTERDESK_CONSOLE_PATH, BETTERDESK_PATH,
	// RUSTDESK_DIR, RUSTDESK_PATH and DATA_DIR. Nil reads nothing.
	Getenv func(string) string
	// GOOS decides the default server folder. Empty means runtime.GOOS.
	GOOS string
	// Allowed replaces the compiled allowlist (tests). Nil uses it.
	Allowed []string
	// Now names the backup of a write. Nil means time.Now.
	Now func() time.Time
	// PID names the temporary files of a write. 0 means the process id.
	PID int
}

// Config is the allowlisted part of one env file.
type Config struct {
	opts    Options
	allowed map[string]bool
}

// New returns a Config.
func New(opts Options) *Config {
	if opts.Getenv == nil {
		opts.Getenv = func(string) string { return "" }
	}
	if opts.GOOS == "" {
		opts.GOOS = runtime.GOOS
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.PID == 0 {
		opts.PID = os.Getpid()
	}
	list := opts.Allowed
	if list == nil {
		list = allowlist
	}
	allowed := make(map[string]bool, len(list))
	for _, key := range list {
		allowed[key] = true
	}
	return &Config{opts: opts, allowed: allowed}
}

// Paths resolves the folders as the old console did (resolvePaths): the
// console folder from BETTERDESK_CONSOLE_PATH or the configured one, the
// server folder from BETTERDESK_PATH, RUSTDESK_DIR or RUSTDESK_PATH with
// the platform's default, the data folder from DATA_DIR or the console's
// data folder, the backups below it, and the env file.
func (c *Config) Paths() Paths {
	getenv := c.opts.Getenv
	consolePath := firstSet(getenv("BETTERDESK_CONSOLE_PATH"), c.opts.ConsoleDir)
	serverPath := firstSet(getenv("BETTERDESK_PATH"), getenv("RUSTDESK_DIR"), getenv("RUSTDESK_PATH"))
	if serverPath == "" {
		serverPath = "/opt/betterdesk"
		if c.opts.GOOS == "windows" {
			serverPath = `C:\BetterDesk`
		}
	}
	dataPath := firstSet(getenv("DATA_DIR"), filepath.Join(consolePath, "data"))
	envPath := c.opts.EnvFile
	if envPath == "" {
		envPath = filepath.Join(consolePath, ".env")
	}
	return Paths{
		ConsolePath: resolve(consolePath),
		ServerPath:  resolve(serverPath),
		DataPath:    resolve(dataPath),
		BackupPath:  resolve(filepath.Join(dataPath, "backups")),
		EnvPath:     resolve(envPath),
		ExamplePath: resolve(filepath.Join(consolePath, ".env.example")),
	}
}

func firstSet(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// resolve is path.resolve: an absolute, clean path.
func resolve(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return abs
}

// AllowedKeys lists the allowlist, sorted.
func (c *Config) AllowedKeys() []string {
	keys := make([]string, 0, len(c.allowed))
	for key := range c.allowed {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// The keys whose values get more than the general checks.
var (
	booleanKeys = setOf("API_ENABLED", "RUSTDESK_API_PROXY", "RUSTDESK_API_DISABLE_TOTP",
		"RUSTDESK_API_DISABLE_TOTP_ACKNOWLEDGED", "SMTP_SECURE", "SMTP_TLS_VERIFY", "TRUST_PROXY",
		"HTTPS_ENABLED", "HTTP_REDIRECT_HTTPS", "P2P_FIRST", "ALWAYS_USE_RELAY", "SAME_NAT_RELAY",
		"ALLOW_SHARED_NAT_INITIATOR", "LOGGED_IN_ONLY_INITIATOR", "OPERATOR_ONLY_OUTBOUND",
		"CDAP_ENABLED", "CDAP_TLS", "CDAP_TLS_REQUIRED", "BILLING_REQUIRE_SYNCED_CLOCK",
		"BILLING_TRUST_OS_NTP", "BILLING_REQUIRE_WORK_REPORT")
	portKeys = setOf("PORT", "HTTPS_PORT", "SMTP_PORT", "API_PORT", "GO_API_PORT", "CLIENT_API_PORT", "CHAT_PORT")
	pathKeys = setOf("RUSTDESK_DIR", "KEYS_PATH", "PUB_KEY_PATH", "API_KEY_PATH", "DB_PATH", "DATA_DIR",
		"SSL_CERT_PATH", "SSL_KEY_PATH", "SSL_CA_PATH")
	enumValues = map[string]map[string]bool{
		"DB_TYPE":              setOf("sqlite", "postgres", "postgresql"),
		"NODE_ENV":             setOf("development", "test", "production"),
		"LOG_LEVEL":            setOf("error", "warn", "info", "debug"),
		"SERVER_BACKEND":       setOf("betterdesk", "rustdesk"),
		"RUSTDESK_API_TLS":     setOf("auto", "true", "false"),
		"UPDATE_GITHUB_BRANCH": setOf("main", "dev"),
		"CDAP_TLS":             setOf("Y", "N", "y", "n", "true", "false", "1", "0"),
	}
	booleanWords = setOf("true", "false", "1", "0", "yes", "no", "on", "off", "y", "n")
	secretKey    = regexp.MustCompile(`(?i)(PASSWORD|PASS|SECRET|TOKEN|API_KEY|PRIVATE_KEY|DATABASE_URL)`)
	portValue    = regexp.MustCompile(`^[0-9]{1,5}$`)
)

func setOf(values ...string) map[string]bool {
	m := make(map[string]bool, len(values))
	for _, v := range values {
		m[v] = true
	}
	return m
}

// IsSecret reports a key whose value is never shown.
func IsSecret(key string) bool { return secretKey.MatchString(key) }

// Mask is the value as the routes show it: "********" for a secret.
func Mask(key, value string) string {
	if IsSecret(key) {
		return "********"
	}
	return value
}

// ValidationError is a refused change. The routes answer it with 400.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

func invalid(format string, args ...any) error {
	return &ValidationError{Message: fmt.Sprintf(format, args...)}
}

// Change is one key and the value a request gave it: a string, a
// float64, a bool, or anything else (which is refused).
type Change struct {
	Key   string
	Value any
}

// Setting is a checked change: the key and its value as text.
type Setting struct {
	Key   string
	Value string
}

// Validate checks every change in order and returns them as text
// (validateEnvChanges). The first change that fails ends the check with
// its message. Changes is nil for a body that had no object there.
func (c *Config) Validate(changes []Change, isObject bool) ([]Setting, error) {
	if !isObject {
		return nil, invalid("Configuration changes must be an object")
	}
	var out []Setting
	for _, change := range changes {
		if !c.allowed[change.Key] {
			name := change.Key
			if name == "" {
				name = "(empty)"
			}
			return nil, invalid("Environment key is not allowlisted: %s", name)
		}
		value, err := c.validateValue(change.Key, change.Value)
		if err != nil {
			return nil, err
		}
		out = append(out, Setting{Key: change.Key, Value: value})
	}
	if len(out) == 0 {
		return nil, invalid("No configuration changes supplied")
	}
	return out, nil
}

func (c *Config) validateValue(key string, value any) (string, error) {
	var raw string
	switch v := value.(type) {
	case string:
		raw = v
	case float64:
		raw = jsbind.NumberString(v)
	case bool:
		raw = strconv.FormatBool(v)
	default:
		return "", invalid("Invalid value for %s", key)
	}
	if strings.ContainsAny(raw, "\x00\r\n") {
		return "", invalid("Invalid control character in %s", key)
	}
	if pathKeys[key] {
		if !filepath.IsAbs(raw) {
			return "", invalid("Path must be absolute for %s", key)
		}
		p := c.Paths()
		inside := false
		for _, root := range []string{p.ConsolePath, p.ServerPath, p.DataPath, "/etc/letsencrypt"} {
			if within(raw, root) {
				inside = true
				break
			}
		}
		if !inside {
			return "", invalid("Path is outside BetterDesk roots for %s", key)
		}
	}
	if portKeys[key] {
		n, _ := strconv.Atoi(raw)
		if !portValue.MatchString(raw) || n < 1 || n > 65535 {
			return "", invalid("Invalid port for %s", key)
		}
	}
	if allowed, ok := enumValues[key]; ok && !allowed[raw] {
		return "", invalid("Invalid value for %s", key)
	}
	if booleanKeys[key] && !booleanWords[strings.ToLower(raw)] {
		return "", invalid("Invalid boolean for %s", key)
	}
	return raw, nil
}

// within reports candidate equal to root or below it, after both are
// made absolute and clean.
func within(candidate, root string) bool {
	c, r := resolve(candidate), resolve(root)
	return c == r || strings.HasPrefix(c, r+string(filepath.Separator))
}

// ReadEnvFile reads the KEY=value lines of an env file: a missing file
// is empty, a later line wins.
func ReadEnvFile(path string) (map[string]string, error) {
	values := map[string]string{}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return values, nil
	}
	if err != nil {
		return nil, err
	}
	for _, line := range splitLines(string(data)) {
		if key, value, ok := envLine(line); ok {
			values[key] = value
		}
	}
	return values, nil
}

// splitLines is text.split(/\r?\n/).
func splitLines(text string) []string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSuffix(line, "\r")
	}
	return lines
}

// envLine matches /^\s*([A-Z][A-Z0-9_]*)=(.*)$/: white space, a key that
// starts with a capital letter, "=" and the rest.
func envLine(line string) (key, value string, ok bool) {
	rest := strings.TrimLeftFunc(line, jsbind.IsSpace)
	eq := strings.IndexByte(rest, '=')
	if eq <= 0 {
		return "", "", false
	}
	key = rest[:eq]
	if key[0] < 'A' || key[0] > 'Z' {
		return "", "", false
	}
	for i := 1; i < len(key); i++ {
		ch := key[i]
		if !(ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_') {
			return "", "", false
		}
	}
	return key, rest[eq+1:], true
}

// Applied is what a write did.
type Applied struct {
	EnvPath    string
	BackupPath string
	// Changed lists the keys in the order of the request.
	Changed []string
	// Values are the new values, secrets masked, in the same order.
	Values []Setting
}

// Apply writes checked settings into the env file (applyEnvChanges): a
// line of a key is replaced where it is, a key without a line is added
// at the end, the old content goes to <env file>.bak.<time> first, the
// new one is written to a temporary file and renamed over the env file,
// both with mode 0600. Line ends become "\n" and the file ends with one.
func (c *Config) Apply(settings []Setting) (Applied, error) {
	p := c.Paths()
	existing, err := os.ReadFile(p.EnvPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Applied{}, fmt.Errorf("read %s: %w", p.EnvPath, err)
	}
	var lines []string
	if len(existing) > 0 {
		lines = splitLines(string(existing))
	}
	values := map[string]string{}
	for _, s := range settings {
		values[s.Key] = s.Value
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(lines)+len(settings))
	for _, line := range lines {
		key, _, ok := envLine(line)
		if value, changed := values[key]; ok && changed {
			seen[key] = true
			line = key + "=" + value
		}
		out = append(out, line)
	}
	var changed []string
	var shown []Setting
	added := map[string]bool{}
	for _, s := range settings {
		if added[s.Key] {
			continue
		}
		added[s.Key] = true
		changed = append(changed, s.Key)
		shown = append(shown, Setting{Key: s.Key, Value: Mask(s.Key, values[s.Key])})
		if !seen[s.Key] {
			out = append(out, s.Key+"="+values[s.Key])
		}
	}

	if err := os.MkdirAll(filepath.Dir(p.EnvPath), 0o755); err != nil {
		return Applied{}, fmt.Errorf("create the folder of %s: %w", p.EnvPath, err)
	}
	stamp := strings.NewReplacer(":", "-", ".", "-").Replace(c.opts.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
	backup := p.EnvPath + ".bak." + stamp
	if err := os.WriteFile(backup, existing, 0o600); err != nil {
		return Applied{}, fmt.Errorf("write %s: %w", backup, err)
	}
	tmp := p.EnvPath + ".tmp-" + strconv.Itoa(c.opts.PID)
	text := strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil {
		return Applied{}, fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, p.EnvPath); err != nil {
		_ = os.Remove(tmp)
		return Applied{}, fmt.Errorf("replace %s: %w", p.EnvPath, err)
	}
	_ = os.Chmod(p.EnvPath, 0o600)
	return Applied{EnvPath: p.EnvPath, BackupPath: backup, Changed: changed, Values: shown}, nil
}

// RestoreBackup puts a backup Apply made back over the env file
// (restoreEnvBackup). The backup must be named after the env file of
// these paths, be an absolute clean path and exist.
func (c *Config) RestoreBackup(backupPath string) error {
	p := c.Paths()
	if !strings.HasPrefix(backupPath, p.EnvPath+".bak.") || resolve(backupPath) != backupPath ||
		strings.ContainsAny(backupPath[len(p.EnvPath)+len(".bak."):], `/\`) {
		return errors.New("Invalid environment backup path")
	}
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return errors.New("Invalid environment backup path")
	}
	tmp := p.EnvPath + ".rollback-" + strconv.Itoa(c.opts.PID)
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, p.EnvPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", p.EnvPath, err)
	}
	_ = os.Chmod(p.EnvPath, 0o600)
	return nil
}

// HasSchema reports whether the allowlist has keys (the check
// "config-schema").
func (c *Config) HasSchema() bool { return len(c.allowed) > 0 }
