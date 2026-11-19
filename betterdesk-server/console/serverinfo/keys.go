package serverinfo

import (
	"context"
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
)

const (
	ed25519PublicKeyBytes = 32
	goKeyCacheTTL         = 30 * time.Second
	goKeyTimeout          = 5 * time.Second
)

// NullString is a text that is null in a JSON answer when it is empty: a
// key or a QR code that may not exist.
type NullString string

// MarshalJSON implements json.Marshaler.
func (s NullString) MarshalJSON() ([]byte, error) {
	if s == "" {
		return []byte("null"), nil
	}
	var b strings.Builder
	jsonString(&b, string(s))
	return []byte(b.String()), nil
}

var (
	unresolvedToken   = regexp.MustCompile(`__[^_\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+__`)
	yourPublicKeyText = regexp.MustCompile(`^[Yy][Oo][Uu][Rr][_-]?[Pp][Uu][Bb][Ll][Ii][Cc][_-]?[Kk][Ee][Yy]$`)
)

// IsValidRustDeskPublicKey reports whether value is a server public key a
// client can use: the base64 text of 32 bytes in its canonical form, with
// or without the "=" at the end. White space around it does not count.
// Placeholders and tokens that were never filled in are refused.
func IsValidRustDeskPublicKey(value string) bool {
	key := jsTrim(value)
	if key == "" {
		return false
	}
	if unresolvedToken.MatchString(key) {
		return false
	}
	if strings.Contains(lowerASCII(key), "placeholder") {
		return false
	}
	if yourPublicKeyText.MatchString(key) {
		return false
	}
	if hasJSSpace(key) {
		return false
	}
	unpadded := strings.TrimRight(key, "=")
	decoded, err := base64.RawStdEncoding.DecodeString(unpadded)
	if err != nil || len(decoded) != ed25519PublicKeyBytes {
		return false
	}
	return base64.RawStdEncoding.EncodeToString(decoded) == unpadded
}

// GetPublicKey reads the public key file. A missing, empty or unreadable
// file and a file that holds no valid key all give "".
func (s *Service) GetPublicKey() NullString {
	path := s.opts.PubKeyPath
	if !s.fs.Exists(path) {
		return ""
	}
	text, err := readText(s.fs, path)
	if err != nil {
		s.log.Warn("Could not read public key:", nodeReadError(err, path).Error())
		return ""
	}
	raw := jsTrim(text)
	if raw == "" {
		return ""
	}
	if !IsValidRustDeskPublicKey(raw) {
		s.log.Warn("Public key at " + path + " is not a valid Ed25519 key (length=" +
			strconv.Itoa(utf16Len(raw)) + "); ignoring for client deploy/config.")
		return ""
	}
	return NullString(raw)
}

// fetchPublicKeyFromGo asks the server for the key it signs with.
func (s *Service) fetchPublicKeyFromGo(ctx context.Context) string {
	if s.opts.GoAPI == nil {
		return ""
	}
	resp, err := s.opts.GoAPI.Get(ctx, "/server-key", goapi.Timeout(goKeyTimeout))
	if err != nil {
		s.log.Warn("Could not fetch public key from Go /api/server-key:", err.Error())
		return ""
	}
	key, _ := goapi.Dig(resp.Data(), "key").(string)
	key = jsTrim(key)
	if IsValidRustDeskPublicKey(key) {
		return key
	}
	return ""
}

// ResolvePublicKey returns the server's public key: the one in the key
// file when it is valid, else the one the server reports. The server's
// answer is kept for 30 seconds; a failed request is not. Without a key
// the result is "".
func (s *Service) ResolvePublicKey(ctx context.Context) NullString {
	if fromFile := s.GetPublicKey(); fromFile != "" {
		return fromFile
	}

	now := s.now().UnixMilli()
	s.keyMu.Lock()
	cached, at := s.goKey, s.goKeyAt
	s.keyMu.Unlock()
	if cached != "" && now-at < goKeyCacheTTL.Milliseconds() {
		return NullString(cached)
	}

	fromGo := s.fetchPublicKeyFromGo(ctx)
	s.keyMu.Lock()
	s.goKey, s.goKeyAt = fromGo, now
	s.keyMu.Unlock()
	return NullString(fromGo)
}

// GetAPIKey reads the API key file. With masked set, a key shows "****"
// between its first and last characters: 4 at each end, and at most a
// quarter of the key's length (in UTF-16 units), so a key of 3 or fewer
// shows none. ok is false when the file is missing or cannot be read; an
// empty file gives "" and true.
func (s *Service) GetAPIKey(masked bool) (key string, ok bool) {
	path := s.opts.APIKeyPath
	if !s.fs.Exists(path) {
		return "", false
	}
	text, err := readText(s.fs, path)
	if err != nil {
		s.log.Warn("Could not read API key:", nodeReadError(err, path).Error())
		return "", false
	}
	key = jsTrim(text)
	if n := utf16Len(key); masked && n > 0 {
		shown := min(4, n/4)
		return sliceUnits(key, 0, shown) + "****" + sliceUnits(key, n-shown, n), true
	}
	return key, true
}

// NormalizeHostInput returns the host to put into a client configuration
// for what an operator or a request gave: the bare host name when it can
// be read as one, else the text as given without the white space around
// it, and "localhost" for nothing.
func NormalizeHostInput(serverHost string) string {
	if serverHost == "" {
		return "localhost"
	}
	if normalized := NormalizeServerHost(serverHost); normalized.Valid {
		return normalized.Host
	}
	if trimmed := jsTrim(serverHost); trimmed != "" {
		return trimmed
	}
	return "localhost"
}

// APIURLForHost returns the address of the server's API on host: http or
// https, with the API port unless it is the scheme's own, and an IPv6
// address in brackets.
func (s *Service) APIURLForHost(host string, useHTTPS bool) string {
	host = HostForURL(host)
	port := s.opts.GoAPIPort
	if port == 0 {
		port = s.opts.APIPort
	}
	if port == 0 {
		port = 21114
	}
	switch {
	case useHTTPS && port == 443:
		return "https://" + host
	case !useHTTPS && port == 80:
		return "http://" + host
	case useHTTPS:
		return "https://" + host + ":" + strconv.Itoa(port)
	}
	return "http://" + host + ":" + strconv.Itoa(port)
}

// Endpoints are the addresses a client is given: the ID server, the relay
// and the API. A bare host is Endpoints{Host: host}: the relay is then the
// same host and the API address is built from it.
type Endpoints struct {
	Host  string
	Relay string
	API   string
}

// ConfigPayload is the configuration a RustDesk client imports.
type ConfigPayload struct {
	Host  string `json:"host"`
	Relay string `json:"relay"`
	API   string `json:"api"`
	Key   string `json:"key"`
}

// PayloadOptions change how a payload is built.
type PayloadOptions struct {
	// UseHTTPS picks the scheme of an API address that has to be built.
	// Nil means the scheme of the configured API address.
	UseHTTPS *bool
	// PublicKey is the key to put in. A key that is not valid becomes "".
	// Nil means the key in the key file.
	PublicKey *string
}

// BuildRustDeskConfigPayload builds the client configuration for the
// endpoints with the key from the key file, or the one in opts.
func (s *Service) BuildRustDeskConfigPayload(ep Endpoints, opts PayloadOptions) ConfigPayload {
	var key string
	if opts.PublicKey != nil {
		if IsValidRustDeskPublicKey(*opts.PublicKey) {
			key = jsTrim(*opts.PublicKey)
		}
	} else {
		key = string(s.GetPublicKey())
	}
	useHTTPS := s.DefaultUseHTTPS()
	if opts.UseHTTPS != nil {
		useHTTPS = *opts.UseHTTPS
	}

	host := NormalizeHostInput(ep.Host)
	relay := ep.Relay
	if relay == "" {
		relay = host
	}
	api := ep.API
	if api == "" {
		api = s.APIURLForHost(host, useHTTPS)
	}
	return ConfigPayload{Host: host, Relay: s.RelayEndpointForHost(relay, ""), API: api, Key: key}
}

// ResolveRustDeskConfigPayload is BuildRustDeskConfigPayload with the key
// ResolvePublicKey finds: the key file first, then the server
// (buildRustDeskConfigPayloadAsync in the Node console).
func (s *Service) ResolveRustDeskConfigPayload(ctx context.Context, ep Endpoints, opts PayloadOptions) ConfigPayload {
	if opts.PublicKey == nil {
		key := string(s.ResolvePublicKey(ctx))
		opts.PublicKey = &key
	}
	return s.BuildRustDeskConfigPayload(ep, opts)
}

// EncodeRustDeskCliConfigString returns the text "rustdesk --config"
// and "Import Server Config" take: the payload as JSON, in base64 without
// the "=" at the end, written back to front.
func EncodeRustDeskCliConfigString(p ConfigPayload) string {
	var js strings.Builder
	js.WriteString(`{"host":`)
	jsonString(&js, p.Host)
	js.WriteString(`,"relay":`)
	jsonString(&js, p.Relay)
	js.WriteString(`,"api":`)
	jsonString(&js, p.API)
	js.WriteString(`,"key":`)
	jsonString(&js, p.Key)
	js.WriteByte('}')

	encoded := []byte(base64.RawStdEncoding.EncodeToString([]byte(js.String())))
	for i, j := 0, len(encoded)-1; i < j; i, j = i+1, j-1 {
		encoded[i], encoded[j] = encoded[j], encoded[i]
	}
	return string(encoded)
}

// ConfigURIPrefix starts the link a RustDesk client opens or scans.
const ConfigURIPrefix = "rustdesk://config/"

// EncodeRustDeskConfigURI returns the link form of the configuration:
// "rustdesk://config/" and the text of EncodeRustDeskCliConfigString.
func EncodeRustDeskConfigURI(p ConfigPayload) string {
	return ConfigURIPrefix + EncodeRustDeskCliConfigString(p)
}

// qr renders text, or gives "" with a warning when that fails.
func (s *Service) qr(text, warning string) NullString {
	if s.opts.QR == nil {
		s.log.Warn(warning, "no QR renderer")
		return ""
	}
	dataURL, err := s.opts.QR(text)
	if err != nil {
		s.log.Warn(warning, err.Error())
		return ""
	}
	return NullString(dataURL)
}

// GetServerConfigQR returns a QR code, as a PNG data URL, of the link that
// configures a client for the endpoints. Without a public key, or when the
// code cannot be drawn, the result is "".
func (s *Service) GetServerConfigQR(ctx context.Context, ep Endpoints) NullString {
	key := string(s.ResolvePublicKey(ctx))
	if key == "" {
		return ""
	}
	payload := s.BuildRustDeskConfigPayload(ep, PayloadOptions{PublicKey: &key})
	return s.qr(EncodeRustDeskConfigURI(payload), "Could not generate config QR code:")
}

// GetPublicKeyQR returns a QR code, as a PNG data URL, of the public key
// alone. Without a key, or when the code cannot be drawn, the result is
// "".
func (s *Service) GetPublicKeyQR(ctx context.Context) NullString {
	key := string(s.ResolvePublicKey(ctx))
	if key == "" {
		return ""
	}
	return s.qr(key, "Could not generate QR code:")
}

// ClientConfig holds the fields an operator types into a client, and the
// two forms a client imports. Without a public key both forms are empty.
type ClientConfig struct {
	ServerID           string `json:"server_id"`
	RelayServer        string `json:"relay_server"`
	APIURL             string `json:"api_url"`
	PublicKey          string `json:"public_key"`
	HasPublicKey       bool   `json:"has_public_key"`
	DeployConfigString string `json:"deploy_config_string"`
	ConfigURI          string `json:"config_uri"`
}

// GetClientConfig builds the client fields for the endpoints, with the key
// ResolvePublicKey finds.
func (s *Service) GetClientConfig(ctx context.Context, ep Endpoints) ClientConfig {
	payload := s.ResolveRustDeskConfigPayload(ctx, ep, PayloadOptions{})
	cfg := ClientConfig{
		ServerID:     payload.Host,
		RelayServer:  payload.Relay,
		APIURL:       payload.API,
		PublicKey:    payload.Key,
		HasPublicKey: payload.Key != "",
	}
	if cfg.HasPublicKey {
		cfg.DeployConfigString = EncodeRustDeskCliConfigString(payload)
		cfg.ConfigURI = EncodeRustDeskConfigURI(payload)
	}
	return cfg
}

// ServerConfig says where the console finds the server and its keys.
type ServerConfig struct {
	PublicKey    NullString `json:"publicKey"`
	APIKeyMasked *string    `json:"apiKeyMasked"`
	HbbsAPIURL   string     `json:"hbbsApiUrl"`
	DBPath       string     `json:"dbPath"`
	PubKeyPath   string     `json:"pubKeyPath"`
	APIKeyPath   string     `json:"apiKeyPath"`
}

// GetServerConfig returns the public key, the masked API key and the
// configured paths.
func (s *Service) GetServerConfig(ctx context.Context) ServerConfig {
	cfg := ServerConfig{
		PublicKey:  s.ResolvePublicKey(ctx),
		HbbsAPIURL: s.opts.APIURL,
		DBPath:     s.opts.DBPath,
		PubKeyPath: s.opts.PubKeyPath,
		APIKeyPath: s.opts.APIKeyPath,
	}
	if key, ok := s.GetAPIKey(true); ok {
		cfg.APIKeyMasked = &key
	}
	return cfg
}
