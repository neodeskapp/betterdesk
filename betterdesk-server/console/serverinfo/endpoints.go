package serverinfo

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The public endpoints are the addresses an operator sets for clients when
// they differ from where the console runs: the ID server, the relay and
// the API address. They are kept in a file in the data folder, which
// outlives a new container, and mirrored in the console's ".env" file.

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
}

// readPublicEnv takes that look, after the one-time copy into the public
// endpoints file.
func (s *Service) readPublicEnv() publicEnv {
	s.EnsureMigratedPublicEndpoints()
	s.fileMu.RLock()
	defer s.fileMu.RUnlock()
	return publicEnv{
		env:     s.envValues(durableKeys),
		durable: s.readEnvFileMap(s.durableEnvPath()),
		legacy:  s.readEnvFileMap(s.opts.EnvPath),
	}
}

// get returns the value of key: from the environment when it is not blank
// there, else from the public endpoints file, else from the console's env
// file.
func (p publicEnv) get(key string) string {
	if v := jsTrim(p.env[key]); v != "" {
		return v
	}
	if v := jsTrim(p.durable[key]); v != "" {
		return v
	}
	return jsTrim(p.legacy[key])
}

func (p publicEnv) endpoints() PublicEndpoints {
	return PublicEndpoints{
		PublicServerID:    p.get(EnvPublicServerID),
		PublicRelayServer: p.get(EnvPublicRelayServer),
		PublicAPIURL:      p.get(EnvPublicAPIURL),
	}
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

// EnsureMigratedPublicEndpoints fills the public endpoints file once per
// process when it has none of its variables: from the environment and the
// console's env file. A file that has a value is left alone.
func (s *Service) EnsureMigratedPublicEndpoints() {
	s.envMu.Lock()
	done := s.migrated
	s.migrated = true
	s.envMu.Unlock()
	if done {
		return
	}
	s.fileMu.Lock()
	defer s.fileMu.Unlock()

	durablePath := s.durableEnvPath()
	durable := s.readEnvFileMap(durablePath)
	for _, key := range durableKeys {
		if jsTrim(durable[key]) != "" {
			return
		}
	}

	legacy := s.readEnvFileMap(s.opts.EnvPath)
	var values []envValue
	for _, key := range durableKeys {
		value := jsTrim(s.env(key))
		if value == "" {
			value = jsTrim(legacy[key])
		}
		if value != "" && !hasEnvInjection(value) {
			values = append(values, envValue{key, value})
		}
	}
	if len(values) == 0 {
		return
	}
	if err := s.upsertKeysToFile(durablePath, values); err != nil {
		s.log.Warn("[public-endpoints] durable migration failed:", err.Error())
	}
}

// ReadPublicEndpointEnv returns the three settings in force. For each one
// the environment wins when it is not blank there, then the public
// endpoints file, then the console's env file.
func (s *Service) ReadPublicEndpointEnv() PublicEndpoints {
	return s.readPublicEnv().endpoints()
}

// ReadPanelPublicHostValue returns PANEL_PUBLIC_HOST by the same order.
func (s *Service) ReadPanelPublicHostValue() string {
	return s.readPublicEnv().get(PanelPublicHostKey)
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
// container) is logged and the save goes on.
func (s *Service) WritePublicEndpointSettingsToEnv(in PublicEndpointsInput) (PublicEndpoints, error) {
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
// name the settings route calls.
func (s *Service) SavePublicEndpointSettings(in PublicEndpointsInput) (PublicEndpoints, error) {
	return s.WritePublicEndpointSettingsToEnv(in)
}
