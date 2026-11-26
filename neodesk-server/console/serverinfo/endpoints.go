package serverinfo

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/internal/envvar"
)

// The public endpoints are the addresses an operator sets for clients when
// they differ from where the console runs: the ID server, the relay and
// the API address. A save in the console keeps them in a file in the data
// folder, which outlives a new container, and mirrors them in the
// console's ".env" file. A value saved in the console wins over the
// environment, which is the first default (DECISIONS A105), unless
// PUBLIC_ENDPOINTS_ENV_OVERRIDE is on.

const (
	// DurableBasename is the name of the public endpoints file in the data
	// folder.
	DurableBasename = "public-endpoints.env"
	// PanelPublicHostKey is the variable that names the host of the
	// console itself.
	PanelPublicHostKey = "PANEL_PUBLIC_HOST"

	// The variables of the three public endpoints.
	EnvPublicServerID    = "PUBLIC_SERVER_ID"
	EnvPublicRelayServer = "PUBLIC_RELAY_SERVER"
	EnvPublicAPIURL      = "PUBLIC_API_URL"
	// EnvPublicEndpointsOverride on makes a public endpoint the environment
	// sets win over the one saved in the console.
	EnvPublicEndpointsOverride = "PUBLIC_ENDPOINTS_ENV_OVERRIDE"
)

// durableKeys are the variables kept in the public endpoints file.
var durableKeys = []string{EnvPublicServerID, EnvPublicRelayServer, EnvPublicAPIURL, PanelPublicHostKey}

// PublicEndpoints are the three settings. An empty one is not set.
type PublicEndpoints struct {
	PublicServerID    string `json:"public_server_id"`
	PublicRelayServer string `json:"public_relay_server"`
	PublicAPIURL      string `json:"public_api_url"`
}

// PublicEndpointsInput are the three settings as a request body gave them:
// any JSON value, or Undefined for a missing field.
type PublicEndpointsInput struct {
	PublicServerID    any
	PublicRelayServer any
	PublicAPIURL      any
}

// Errors of the public endpoint settings. Their texts are fixed: routes
// and the audit log compare them.
var (
	// ErrInvalidPublicHost: the ID server or the relay is not a host name,
	// or a value holds a line break or a NUL character.
	ErrInvalidPublicHost = errors.New("invalid_public_host")
	// ErrInvalidPublicAPIURL: the API address is not an http or https URL.
	ErrInvalidPublicAPIURL = errors.New("invalid_public_api_url")
	// ErrEndpointEnvLocked: the save changes an address the environment
	// sets while PUBLIC_ENDPOINTS_ENV_OVERRIDE is on (EnvLockedEndpoints).
	ErrEndpointEnvLocked = errors.New("public_endpoint_env_locked")

	// The errors of NormalizeSettings, which ValidateSettings turns into
	// the two above.
	ErrInvalidHost     = errors.New("invalid_host")
	ErrInvalidAPIURL   = errors.New("invalid_api_url")
	ErrInvalidEnvValue = errors.New("invalid_env_value")
)

// ParseEnvFile reads env file text into a map: the lines KEY=value, with
// the key trimmed and the value read by EnvValue. Comments, blank lines
// and lines without a key are skipped; a key set twice has its last value.
func ParseEnvFile(content string) map[string]string {
	out := map[string]string{}
	for _, line := range splitEnvLines(content) {
		trimmed := jsTrim(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		eq := strings.IndexByte(trimmed, '=')
		if eq <= 0 {
			continue
		}
		key := jsTrim(trimmed[:eq])
		if key == "__proto__" {
			continue
		}
		out[key] = EnvValue(trimmed[eq+1:])
	}
	return out
}

// readEnvFileMap reads an env file. A missing or unreadable file is empty.
func (s *Service) readEnvFileMap(path string) map[string]string {
	if !s.fs.Exists(path) {
		return map[string]string{}
	}
	text, err := readText(s.fs, path)
	if err != nil {
		return map[string]string{}
	}
	return ParseEnvFile(text)
}

// publicEnv is one look at the three places the public endpoint settings
// and PANEL_PUBLIC_HOST come from, taken while no save is under way.
type publicEnv struct {
	env, durable, legacy map[string]string
	// override is PUBLIC_ENDPOINTS_ENV_OVERRIDE on
	override bool
}

// readPublicEnv takes that look.
func (s *Service) readPublicEnv() publicEnv {
	s.fileMu.RLock()
	defer s.fileMu.RUnlock()
	return publicEnv{
		env:      s.envValues(durableKeys),
		durable:  s.readEnvFileMap(s.durableEnvPath()),
		legacy:   s.readEnvFileMap(s.opts.EnvPath),
		override: envvar.SwitchOr(s.getenv(EnvPublicEndpointsOverride), false),
	}
}

// lookup returns the value of a public endpoint setting and where it comes
// from, the first that is not blank of: the environment when
// PUBLIC_ENDPOINTS_ENV_OVERRIDE is on (SourceEnv), the value saved in the
// console (the public endpoints file, SourceSaved), the environment and
// the console's env file (SourceEnv). Source is "" for no value.
func (p publicEnv) lookup(key string) (value, source string) {
	env := jsTrim(p.env[key])
	if p.override && env != "" {
		return env, SourceEnv
	}
	if v := jsTrim(p.durable[key]); v != "" {
		return v, SourceSaved
	}
	if env != "" {
		return env, SourceEnv
	}
	if v := jsTrim(p.legacy[key]); v != "" {
		return v, SourceEnv
	}
	return "", ""
}

// panelHost returns PANEL_PUBLIC_HOST, which the console does not save:
// from the environment when it is not blank there, else from the public
// endpoints file, else from the console's env file.
func (p publicEnv) panelHost() string {
	if v := jsTrim(p.env[PanelPublicHostKey]); v != "" {
		return v
	}
	if v := jsTrim(p.durable[PanelPublicHostKey]); v != "" {
		return v
	}
	return jsTrim(p.legacy[PanelPublicHostKey])
}

// publicSources says where each public endpoint setting comes from
// (lookup).
type publicSources struct{ id, relay, api string }

func (p publicEnv) endpoints() (PublicEndpoints, publicSources) {
	var out PublicEndpoints
	var from publicSources
	out.PublicServerID, from.id = p.lookup(EnvPublicServerID)
	out.PublicRelayServer, from.relay = p.lookup(EnvPublicRelayServer)
	out.PublicAPIURL, from.api = p.lookup(EnvPublicAPIURL)
	return out, from
}

// hasEnvInjection reports whether value could add a line to an env file.
func hasEnvInjection(value string) bool {
	return strings.ContainsAny(value, "\r\n\x00")
}

func normalizeHostField(value any) (string, error) {
	raw := ""
	if truthy(value) {
		raw = jsTrim(jsString(value))
	}
	if raw == "" {
		return "", nil
	}
	if hasEnvInjection(raw) {
		return "", ErrInvalidEnvValue
	}
	normalized := NormalizeServerHost(raw)
	if !normalized.Valid {
		return "", ErrInvalidHost
	}
	return normalized.Host, nil
}

// maxAPIURLLength is the longest public API address taken, in UTF-16
// units. Longer ones are refused before they are parsed: the host name
// mapping takes time with the square of a long host that is not ASCII.
const maxAPIURLLength = 2048

func normalizeAPIURL(value any) (string, error) {
	raw := ""
	if truthy(value) {
		raw = jsTrim(jsString(value))
	}
	if raw == "" {
		return "", nil
	}
	if hasEnvInjection(raw) {
		return "", ErrInvalidEnvValue
	}
	if !hasHTTPScheme(raw) || utf16Len(raw) > maxAPIURLLength {
		return "", ErrInvalidAPIURL
	}
	u, ok := parseJSURL(raw)
	if !ok || (u.protocol != "http:" && u.protocol != "https:") {
		return "", ErrInvalidAPIURL
	}
	out := u.protocol + "//" + u.hostname
	if u.port != "" {
		out += ":" + u.port
	}
	if u.pathname != "/" {
		out += strings.TrimRight(u.pathname, "/")
	}
	out += u.search
	if hasEnvInjection(out) {
		return "", ErrInvalidEnvValue
	}
	return out, nil
}

// NormalizeSettings brings the three settings into the form they are
// stored in: the ID server and the relay as bare host names, the API
// address as scheme, host, port, path and query without a slash at the
// end. Its errors are ErrInvalidHost, ErrInvalidAPIURL and
// ErrInvalidEnvValue.
func NormalizeSettings(in PublicEndpointsInput) (PublicEndpoints, error) {
	var out PublicEndpoints
	var err error
	if out.PublicServerID, err = normalizeHostField(in.PublicServerID); err != nil {
		return PublicEndpoints{}, err
	}
	if out.PublicRelayServer, err = normalizeHostField(in.PublicRelayServer); err != nil {
		return PublicEndpoints{}, err
	}
	if out.PublicAPIURL, err = normalizeAPIURL(in.PublicAPIURL); err != nil {
		return PublicEndpoints{}, apiURLError{err}
	}
	return out, nil
}

// apiURLError is an error of the API address field.
type apiURLError struct{ error }

func (e apiURLError) Unwrap() error { return e.error }

// ValidateSettings is NormalizeSettings with the errors a route answers
// with: ErrInvalidPublicAPIURL for anything wrong with the API address,
// a line break or NUL character included, and ErrInvalidPublicHost for
// anything wrong with the ID server or the relay.
func ValidateSettings(in PublicEndpointsInput) (PublicEndpoints, error) {
	out, err := NormalizeSettings(in)
	var apiErr apiURLError
	switch {
	case errors.As(err, &apiErr):
		return PublicEndpoints{}, ErrInvalidPublicAPIURL
	case errors.Is(err, ErrInvalidHost), errors.Is(err, ErrInvalidEnvValue):
		return PublicEndpoints{}, ErrInvalidPublicHost
	}
	return out, err
}

// IsEnvOverrideActive reports whether at least one setting has a value.
func IsEnvOverrideActive(p PublicEndpoints) bool {
	return p.PublicServerID != "" || p.PublicRelayServer != "" || p.PublicAPIURL != ""
}

type envValue struct{ key, value string }

// writeEnvFileAtomic replaces an env file through a temporary file beside
// it, and leaves it readable by its owner only.
func (s *Service) writeEnvFileAtomic(path, content string) error {
	dir := filepath.Dir(path)
	if err := s.fs.MkdirAll(dir, 0o777); err != nil {
		return nodeError(err, "mkdir", dir)
	}
	tmp := filepath.Join(dir, "."+filepath.Base(path)+"."+strconv.Itoa(os.Getpid())+".tmp")
	if err := s.fs.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return nodeError(err, "open", tmp)
	}
	if err := s.fs.Rename(tmp, path); err != nil {
		return nodeError(err, "rename", tmp+"' -> '"+path)
	}
	// A file system that cannot change modes keeps the file as renamed.
	_ = s.fs.Chmod(path, 0o600)
	return nil
}

// upsertKeysToFile sets the values in an env file, which it creates when
// it is missing.
func (s *Service) upsertKeysToFile(path string, values []envValue) error {
	content := ""
	if s.fs.Exists(path) {
		text, err := readText(s.fs, path)
		if err != nil {
			return nodeReadError(err, path)
		}
		content = text
	}
	for _, v := range values {
		if hasEnvInjection(v.value) {
			return ErrInvalidEnvValue
		}
		content = UpsertEnvKey(content, v.key, v.value)
	}
	return s.writeEnvFileAtomic(path, content)
}

// ReadPublicEndpointEnv returns the three settings in force: for each one
// the value saved in the console, else the one of the environment, else
// the one of the console's env file; the environment first when
// PUBLIC_ENDPOINTS_ENV_OVERRIDE is on.
func (s *Service) ReadPublicEndpointEnv() PublicEndpoints {
	public, _ := s.readPublicEnv().endpoints()
	return public
}

// ReadPanelPublicHostValue returns PANEL_PUBLIC_HOST: from the
// environment, else the public endpoints file, else the console's env
// file.
func (s *Service) ReadPanelPublicHostValue() string {
	return s.readPublicEnv().panelHost()
}

// GetPublicEndpointSettings is ReadPublicEndpointEnv under the name the
// settings route calls.
func (s *Service) GetPublicEndpointSettings() PublicEndpoints {
	return s.ReadPublicEndpointEnv()
}

// SyncProcessEnv makes the settings the values the service reads as the
// process environment from now on: a setting with a value replaces the
// variable, an empty one removes it.
func (s *Service) SyncProcessEnv(p PublicEndpoints) {
	s.envMu.Lock()
	defer s.envMu.Unlock()
	if s.envSaved == nil {
		s.envSaved = map[string]*string{}
	}
	for key, value := range map[string]string{
		EnvPublicServerID:    p.PublicServerID,
		EnvPublicRelayServer: p.PublicRelayServer,
		EnvPublicAPIURL:      p.PublicAPIURL,
	} {
		if value == "" {
			s.envSaved[key] = nil
			continue
		}
		s.envSaved[key] = &value
	}
}

// WritePublicEndpointSettingsToEnv validates the settings and stores them:
// first in the public endpoints file, then in the console's env file, then
// over the environment values the service reads. It returns the settings
// as stored. The public endpoints file is where they last; the console's
// env file only mirrors them, so a failure to write it (read-only in a
// container) is logged and the save goes on. After a save the relay in
// force goes to Options.OnPublicRelay.
func (s *Service) WritePublicEndpointSettingsToEnv(in PublicEndpointsInput) (PublicEndpoints, error) {
	normalized, err := s.storePublicEndpoints(in)
	if err != nil {
		return PublicEndpoints{}, err
	}
	if s.opts.OnPublicRelay != nil {
		s.opts.OnPublicRelay(s.ReadPublicEndpointEnv().PublicRelayServer)
	}
	return normalized, nil
}

// storePublicEndpoints is the save of WritePublicEndpointSettingsToEnv.
func (s *Service) storePublicEndpoints(in PublicEndpointsInput) (PublicEndpoints, error) {
	normalized, err := ValidateSettings(in)
	if err != nil {
		return PublicEndpoints{}, err
	}
	values := []envValue{
		{EnvPublicServerID, normalized.PublicServerID},
		{EnvPublicRelayServer, normalized.PublicRelayServer},
		{EnvPublicAPIURL, normalized.PublicAPIURL},
	}

	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	if err := s.upsertKeysToFile(s.durableEnvPath(), values); err != nil {
		return PublicEndpoints{}, err
	}
	if err := s.upsertKeysToFile(s.opts.EnvPath, values); err != nil {
		s.log.Warn("Could not copy the public endpoints into "+s.opts.EnvPath+":", err.Error())
	}
	s.SyncProcessEnv(normalized)
	return normalized, nil
}

// SavePublicEndpointSettings is WritePublicEndpointSettingsToEnv under the
// name the settings route calls, but a save that changes an address the
// environment sets while PUBLIC_ENDPOINTS_ENV_OVERRIDE is on fails with
// ErrEndpointEnvLocked: the next start would put the environment's value
// back (BUGS L386).
func (s *Service) SavePublicEndpointSettings(in PublicEndpointsInput) (PublicEndpoints, error) {
	if locked := s.EnvLockedEndpoints(); len(locked) > 0 {
		next, err := ValidateSettings(in)
		if err != nil {
			return PublicEndpoints{}, err
		}
		env, _ := NormalizeSettings(PublicEndpointsInput{
			PublicServerID:    jsTrim(s.getenv(EnvPublicServerID)),
			PublicRelayServer: jsTrim(s.getenv(EnvPublicRelayServer)),
			PublicAPIURL:      jsTrim(s.getenv(EnvPublicAPIURL)),
		})
		for _, field := range locked {
			if next.field(field) != env.field(field) {
				return PublicEndpoints{}, ErrEndpointEnvLocked
			}
		}
	}
	return s.WritePublicEndpointSettingsToEnv(in)
}

// EnvLockedEndpoints names the addresses (public_server_id,
// public_relay_server, public_api_url) the environment sets while
// PUBLIC_ENDPOINTS_ENV_OVERRIDE is on: the server takes them from there
// at every start, whatever the console saved.
func (s *Service) EnvLockedEndpoints() []string {
	locked := []string{}
	if !envvar.SwitchOr(s.getenv(EnvPublicEndpointsOverride), false) {
		return locked
	}
	for _, f := range [][2]string{
		{"public_server_id", EnvPublicServerID},
		{"public_relay_server", EnvPublicRelayServer},
		{"public_api_url", EnvPublicAPIURL},
	} {
		if jsTrim(s.getenv(f[1])) != "" {
			locked = append(locked, f[0])
		}
	}
	return locked
}

// PublicEndpointSources says where each public endpoint setting in force
// comes from, by its name in a request (public_server_id,
// public_relay_server, public_api_url), as ReadPublicEndpointEnv picks
// it: SourceEnv, SourceSaved, or "" when none is set.
func (s *Service) PublicEndpointSources() map[string]string {
	_, from := s.readPublicEnv().endpoints()
	return map[string]string{"public_server_id": from.id, "public_relay_server": from.relay, "public_api_url": from.api}
}

// PublicEndpointEnvVars are the variables of the public endpoint
// settings, by their names in a request.
var PublicEndpointEnvVars = map[string]string{
	"public_server_id": EnvPublicServerID, "public_relay_server": EnvPublicRelayServer, "public_api_url": EnvPublicAPIURL,
}

// field is one of the three addresses by its name in a request.
func (p PublicEndpoints) field(name string) string {
	switch name {
	case "public_server_id":
		return p.PublicServerID
	case "public_relay_server":
		return p.PublicRelayServer
	}
	return p.PublicAPIURL
}
