package serverinfo

import (
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
)

var (
	portSuffix  = regexp.MustCompile(`^:[0-9]+$`)
	hostPattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9.-]{0,253}[a-zA-Z0-9])?$`)
	ipv4Pattern = regexp.MustCompile(`^(?:[0-9]{1,3}\.){3}[0-9]{1,3}$`)
)

// HostResult is what NormalizeServerHost makes of an operator's input.
type HostResult struct {
	Valid bool `json:"valid"`
	// Error is "server_host_required" or "server_host_invalid" when Valid
	// is false.
	Error string `json:"error,omitempty"`
	Host  string `json:"host"`
}

// NormalizeServerHost reads a host name, an IPv4 address or an IPv6
// address out of what an operator typed: white space around it, a
// leading "http://" or "https://", everything from the first "/" on and a
// port after the host are dropped. An IPv6 address, bare or in brackets
// ("[2001:db8::1]:21116"), is the address without brackets; any other
// text loses everything from its first ":" on. At most 253 characters of
// the input are read.
func NormalizeServerHost(input string) HostResult {
	host := sliceUnits(jsTrim(input), 0, 253)
	if host == "" {
		return HostResult{Error: "server_host_required"}
	}
	host = trimHTTPScheme(host)
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	if addr, ok := ipv6Host(host); ok {
		return HostResult{Valid: true, Host: addr}
	}
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")

	if !hostPattern.MatchString(host) && !ipv4Pattern.MatchString(host) {
		return HostResult{Error: "server_host_invalid"}
	}
	return HostResult{Valid: true, Host: host}
}

// ipv6Host reads an IPv6 address without a zone, bare ("2001:db8::1") or
// in brackets with a port or without ("[2001:db8::1]:21116"), and returns
// it without the brackets.
func ipv6Host(host string) (string, bool) {
	text := host
	if strings.HasPrefix(host, "[") {
		end := strings.IndexByte(host, ']')
		if end < 0 {
			return "", false
		}
		if rest := host[end+1:]; rest != "" && !portSuffix.MatchString(rest) {
			return "", false
		}
		text = host[1:end]
	}
	addr, err := netip.ParseAddr(text)
	if err != nil || !addr.Is6() || addr.Zone() != "" {
		return "", false
	}
	return text, true
}

// HostForURL is a host as it stands in a URL: an IPv6 address without a
// zone in brackets, anything else as it is.
func HostForURL(host string) string {
	if addr, err := netip.ParseAddr(host); err == nil && addr.Is6() && addr.Zone() == "" {
		return "[" + host + "]"
	}
	return host
}

// trimHTTPScheme removes "http://" or "https://" in any letter case from
// the start of s.
func trimHTTPScheme(s string) string {
	for _, scheme := range []string{"http://", "https://"} {
		if len(s) >= len(scheme) && lowerASCII(s[:len(scheme)]) == scheme {
			return s[len(scheme):]
		}
	}
	return s
}

// hasHTTPScheme reports whether s starts with "http://" or "https://" in
// any letter case.
func hasHTTPScheme(s string) bool {
	return trimHTTPScheme(s) != s
}

// apiURL parses the configured API address, with "http://" put before an
// address that has no scheme.
func (s *Service) apiURL() (jsURL, bool) {
	raw := s.opts.APIURL
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	return parseJSURL(raw)
}

// DefaultServerHost returns the host of the configured API address, or ""
// when the address cannot be read.
func (s *Service) DefaultServerHost() string {
	u, ok := s.apiURL()
	if !ok {
		return ""
	}
	return u.hostname
}

// DefaultUseHTTPS reports whether the configured API address is an https
// address; while the console listens on the client API port, whether that
// port has TLS.
func (s *Service) DefaultUseHTTPS() bool {
	if s.opts.ClientAPIPort != 0 {
		return s.opts.ClientAPIHTTPS
	}
	u, ok := s.apiURL()
	return ok && u.protocol == "https:"
}

// RequestHost is what a request says about the host it was sent to.
type RequestHost struct {
	// ForwardedHost is the X-Forwarded-Host header; several are joined
	// with ", ".
	ForwardedHost string
	// Host is the Host header.
	Host string
}

// RequestHostOf reads the two headers from a request. The forwarded host
// is taken as sent, whether or not the console trusts the proxy.
func RequestHostOf(r *http.Request) *RequestHost {
	if r == nil {
		return nil
	}
	return &RequestHost{
		ForwardedHost: httpx.Latin1(strings.Join(r.Header.Values("X-Forwarded-Host"), ", ")),
		Host:          httpx.Latin1(r.Host),
	}
}

// StripRequestHost returns the host of a Host or X-Forwarded-Host value:
// the first of a list, without its port, an IPv6 address without its
// brackets. An empty value is "localhost".
func StripRequestHost(rawHost string) string {
	if rawHost == "" {
		rawHost = "localhost"
	}
	first, _, _ := strings.Cut(rawHost, ",")
	first = jsTrim(first)

	if strings.HasPrefix(first, "[") {
		if end := strings.IndexByte(first, ']'); end > 0 {
			return first[1:end]
		}
		return first
	}
	if strings.Count(first, ":") == 1 {
		host, _, _ := strings.Cut(first, ":")
		return host
	}
	return first
}

// IsPhoneUnreachableHost reports whether a phone that scans a
// configuration code will not reach host: this machine's own names and
// addresses, and names that end in ".local".
func IsPhoneUnreachableHost(host string) bool {
	h := lowerASCII(jsTrim(host))
	switch h {
	case "":
		return false
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	return strings.HasSuffix(h, ".local")
}

// The values of EndpointSources.
const (
	SourceSaved     = "saved"
	SourceEnv       = "env"
	SourceQuery     = "query"
	SourcePanelHost = "panel_host"
	SourceConfig    = "config"
	SourceRequest   = "request"
	SourceDefault   = "default"
	SourceDerived   = "derived"
)

// EndpointSources says where each address came from: "saved" for a public
// endpoint setting saved in the console, "env" for one the environment or
// the console's env file sets, "query" for the host a caller asked for,
// "panel_host" for PANEL_PUBLIC_HOST, "config" for the host of the
// configured API address, "request" for the request's own host, "default"
// for localhost. The API address is "derived" when it was built from a
// host that is a public endpoint setting.
type EndpointSources struct {
	Host  string `json:"host"`
	Relay string `json:"relay"`
	API   string `json:"api"`
}

// ResolvedEndpoints are the addresses to give a client, and how they came
// about.
type ResolvedEndpoints struct {
	Host    string          `json:"host"`
	Relay   string          `json:"relay"`
	API     string          `json:"api"`
	Sources EndpointSources `json:"sources"`
	// EnvOverrideActive is set when at least one public endpoint setting
	// has a value.
	EnvOverrideActive bool `json:"env_override_active"`
}

// Endpoints returns the three addresses.
func (r ResolvedEndpoints) Endpoints() Endpoints {
	return Endpoints{Host: r.Host, Relay: r.Relay, API: r.API}
}

// ReadPanelPublicHost returns PANEL_PUBLIC_HOST (ReadPanelPublicHostValue).
func (s *Service) ReadPanelPublicHost() string {
	return s.ReadPanelPublicHostValue()
}

// isMachineLocalHost reports whether a client elsewhere cannot reach host:
// a loopback address, 0.0.0.0 or ::, or a name without a dot (localhost,
// a container's name). An IPv6 address may be in brackets.
func isMachineLocalHost(host string) bool {
	h := strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if addr, err := netip.ParseAddr(h); err == nil {
		return addr.IsLoopback() || addr.IsUnspecified()
	}
	return !strings.Contains(h, ".")
}

// clientFacingHost picks the host and names its source. idSource is where
// the public server ID setting comes from. The host of the console's own
// API address gives way to the request's host when no client elsewhere can
// reach it (isMachineLocalHost).
func (s *Service) clientFacingHost(req *RequestHost, queryHost string, public PublicEndpoints, idSource, panelHost string) (host, source string) {
	if public.PublicServerID != "" {
		return public.PublicServerID, idSource
	}
	if queryHost != "" {
		if normalized := NormalizeServerHost(queryHost); normalized.Valid {
			return normalized.Host, SourceQuery
		}
	}
	if panelHost != "" {
		if normalized := NormalizeServerHost(panelHost); normalized.Valid {
			return normalized.Host, SourcePanelHost
		}
	}
	if fromAPI := s.DefaultServerHost(); fromAPI != "" && (req == nil || !isMachineLocalHost(fromAPI)) {
		return fromAPI, SourceConfig
	}
	if req != nil {
		raw := req.ForwardedHost
		if raw == "" {
			raw = req.Host
		}
		return StripRequestHost(raw), SourceRequest
	}
	return "localhost", SourceDefault
}

// ResolveClientFacingHost returns the host a client should use, the first
// of: the public server ID setting, queryHost when it is a valid host,
// PANEL_PUBLIC_HOST when it is one, the host of the configured API
// address unless a request came and that host is one only this machine
// reaches, the host of the request, "localhost". req may be nil.
func (s *Service) ResolveClientFacingHost(req *RequestHost, queryHost string) string {
	settings := s.readPublicEnv()
	public, from := settings.endpoints()
	host, _ := s.clientFacingHost(req, queryHost, public, from.id, settings.panelHost())
	return host
}

// ResolveRustDeskEndpoints returns the ID server, relay and API address to
// give a client. The relay and the API address follow the host unless
// their own public endpoint setting has a value. The relay always carries
// a port (RelayEndpointForHost).
func (s *Service) ResolveRustDeskEndpoints(req *RequestHost, queryHost string) ResolvedEndpoints {
	settings := s.readPublicEnv()
	public, from := settings.endpoints()
	host, hostSource := s.clientFacingHost(req, queryHost, public, from.id, settings.panelHost())

	relay := host
	out := ResolvedEndpoints{
		Host:              host,
		Sources:           EndpointSources{Host: hostSource, Relay: hostSource, API: hostSource},
		EnvOverrideActive: IsEnvOverrideActive(public),
	}
	if public.PublicRelayServer != "" {
		relay, out.Sources.Relay = public.PublicRelayServer, from.relay
	}
	out.Relay = s.RelayEndpointForHost(relay, "")
	switch {
	case public.PublicAPIURL != "":
		out.API, out.Sources.API = public.PublicAPIURL, from.api
	default:
		out.API = s.APIURLForHost(host, s.DefaultUseHTTPS())
		if hostSource == SourceEnv || hostSource == SourceSaved {
			out.Sources.API = SourceDerived
		}
	}
	return out
}

// DefaultAPIPort returns the port of the configured API address as text:
// the one in the address, else "443" for https and "21114" for anything
// else, also for an address that cannot be read. While the console
// listens on the client API port it is that port.
func (s *Service) DefaultAPIPort() string {
	if s.opts.ClientAPIPort != 0 {
		return strconv.Itoa(s.opts.ClientAPIPort)
	}
	u, ok := s.apiURL()
	switch {
	case !ok:
		return "21114"
	case u.port != "":
		return u.port
	case u.protocol == "https:":
		return "443"
	}
	return "21114"
}
