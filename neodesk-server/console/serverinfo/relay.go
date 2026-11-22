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
	relayHostPort = regexp.MustCompile(`^([^:/\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+):([0-9]+)$`)
	relayIPv6     = regexp.MustCompile(`^\[([0-9a-fA-F:]+)\](?::([0-9]+))?$`)
	// The scheme matches in any case, but only ASCII letters, as a
	// JavaScript pattern with the i flag and without u does
	httpScheme = regexp.MustCompile(`^[hH][tT][tT][pP][sS]?://`)
)

// RelayEndpointForHost is RelayEndpoint with the service's DefaultRelayPort.
func (s *Service) RelayEndpointForHost(hostPort, port string) string {
	return RelayEndpoint(hostPort, port, s.DefaultRelayPort)
}

// RelayEndpoint returns the host:port of the relay for an address that may
// have a port already. A URL loses its scheme and path first. An address
// with a valid port keeps it (a number from 1 to 65535, written without
// leading zeros), one whose port is not valid gets port in its place, an
// IPv6 address is put in brackets, and everything else gets port; port ""
// stands for the default port. An empty address gives "". The address is
// trimmed and cut to 300 UTF-16 units first.
func RelayEndpoint(hostPort, port string, defaultPort func() string) string {
	raw := sliceUnits(jsTrim(hostPort), 0, 300)
	if raw == "" {
		return ""
	}
	relayPort := port
	if relayPort == "" {
		relayPort = defaultPort()
	}
	if httpScheme.MatchString(raw) {
		raw, _, _ = strings.Cut(httpScheme.ReplaceAllString(raw, ""), "/")
	}

	if m := relayHostPort.FindStringSubmatch(raw); m != nil {
		if n, ok := validPort(m[2]); ok {
			return m[1] + ":" + n
		}
		return m[1] + ":" + relayPort
	}
	if m := relayIPv6.FindStringSubmatch(raw); m != nil {
		usePort := relayPort
		if n, ok := validPort(m[2]); ok {
			usePort = n
		}
		return "[" + m[1] + "]:" + usePort
	}
	if strings.Count(raw, ":") > 1 {
		return "[" + raw + "]:" + relayPort
	}
	host, _, _ := strings.Cut(raw, "/")
	return host + ":" + relayPort
}

// validPort returns the port that digits name when it is one from 1 to
// 65535 in at most five digits, written without leading zeros.
func validPort(digits string) (string, bool) {
	if digits == "" || len(digits) > 5 {
		return "", false
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 1 || n > 65535 {
		return "", false
	}
	return strconv.Itoa(n), true
}
