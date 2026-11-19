package serverinfo

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// The relay address a client is given always carries a port. RustDesk
// 1.4.8 and 1.4.9 clients that get a bare relay host ask the API port for
// the relay, so the relay never pairs and every session ends with "Pair
// timeout".

// DefaultRelayPort is the port of the relay server clients are told to
// dial, as text (DefaultRelayPortOf the service's environment).
func (s *Service) DefaultRelayPort() string {
	return DefaultRelayPortOf(s.env)
}

// DefaultRelayPortOf is the relay port in the environment getenv reads, as
// text: RELAY_PORT, else WS_HBBR_PORT, else HBBR_PORT, the first of them
// that is set, when it reads as a positive number (parseInt); "21117"
// otherwise.
func DefaultRelayPortOf(getenv func(string) string) string {
	raw := getenv("RELAY_PORT")
	if raw == "" {
		raw = getenv("WS_HBBR_PORT")
	}
	if raw == "" {
		raw = getenv("HBBR_PORT")
	}
	if n := jsParseInt(raw); !math.IsNaN(n) && n > 0 {
		return numberString(n)
	}
	return "21117"
}

var (
	relayHostPort = regexp.MustCompile(`^([^:/\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+):([0-9]{1,5})$`)
	relayIPv6     = regexp.MustCompile(`^\[([0-9a-fA-F:]+)\](?::([0-9]{1,5}))?$`)
	// The scheme matches in any case, but only ASCII letters, as a
	// JavaScript pattern with the i flag and without u does
	httpScheme = regexp.MustCompile(`^[hH][tT][tT][pP][sS]?://`)
)

// RelayEndpointForHost is RelayEndpoint with the service's DefaultRelayPort.
func (s *Service) RelayEndpointForHost(hostPort, port string) string {
	return RelayEndpoint(hostPort, port, s.DefaultRelayPort)
}

// RelayEndpoint returns the host:port of the relay for an address that may
// have a port already. An address with a valid port keeps it (a number from
// 1 to 65535, written without leading zeros), an IPv6 address is put in
// brackets, and a URL loses its scheme and path; everything else gets port,
// or the default port when port is "". An empty address gives "". The
// address is trimmed and cut to 300 UTF-16 units first.
func RelayEndpoint(hostPort, port string, defaultPort func() string) string {
	raw := sliceUnits(jsTrim(hostPort), 0, 300)
	if raw == "" {
		return ""
	}
	relayPort := port
	if relayPort == "" {
		relayPort = defaultPort()
	}

	if m := relayHostPort.FindStringSubmatch(raw); m != nil {
		if n, _ := strconv.Atoi(m[2]); n > 0 && n <= 65535 {
			return m[1] + ":" + strconv.Itoa(n)
		}
	}
	if m := relayIPv6.FindStringSubmatch(raw); m != nil {
		usePort := relayPort
		if m[2] != "" {
			if n, _ := strconv.Atoi(m[2]); n > 0 && n <= 65535 {
				usePort = strconv.Itoa(n)
			}
		}
		return "[" + m[1] + "]:" + usePort
	}
	if strings.Count(raw, ":") > 1 {
		return "[" + raw + "]:" + relayPort
	}
	host, _, _ := strings.Cut(httpScheme.ReplaceAllString(raw, ""), "/")
	return host + ":" + relayPort
}
