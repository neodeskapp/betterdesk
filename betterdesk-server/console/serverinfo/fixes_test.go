package serverinfo

import (
	"encoding/json"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The recorded Node answers that the Go console gives otherwise on
// purpose: Node's answer was a bug that is fixed here. Each list names
// its row of BUGS.md.

// shortKeyMasks are the masked API keys of the recorded key files of 16
// characters or fewer, by the name of the case (SI4). Node showed a key
// of 8 characters or fewer whole and the first and last 4 characters of
// any longer key; Go shows at most a quarter of a short key at each end.
var shortKeyMasks = map[string]string{
	"placeholder":   "YOU****KEY",
	"short":         "ab****34",
	"nine":          "ab****45",
	"spaces inside": "abc****jkl",
	"non ascii":     "äöü****efg",
	"invalid utf8":  "ab****h�",
	"nul":           "ab****ij",
}

// shortKeyCutMasks are the masked keys of the recorded keys that Node cut
// through a character, by the key (SI4). All of them have 16 characters
// or fewer, so a quarter at each end cuts through none of them.
var shortKeyCutMasks = map[string]string{
	"abc😀defghijk":  "abc****ijk",
	"abcdefghi😀jkl": "abc****jkl",
	"😀😀😀😀😀":         "😀****😀",
}

// fixedKeyRunAnswer is the answer of a recorded step of the key runs as
// the Go console gives it: the 8 character key of the runs masked (SI4).
func fixedKeyRunAnswer(node string) string {
	return strings.Replace(node, `"apiKeyMasked":"abcd1234"`, `"apiKeyMasked":"ab****34"`, 1)
}

// Node's relay answers that UM4 fixes, and the parts of them.
var (
	// a URL taken for a bare IPv6 address: "[https://h:8443/x]:21117"
	nodeRelayOfURL = regexp.MustCompile(`^\[([hH][tT][tT][pP][sS]?://.*)\]:(.*)$`)
	// a port that is not valid kept, and a second one added: "r:0:21117"
	nodeRelayTwoPorts = regexp.MustCompile(`^([^:/\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+):([0-9]+):(.*)$`)
	// the same for a bracketed IPv6 address: "[[fe80::1]:123456]:21117"
	nodeRelayIPv6TwoPorts = regexp.MustCompile(`^\[\[([0-9a-fA-F:]+)\]:([0-9]+)\]:(.*)$`)
	// a host name or bracketed IPv6 address with a port
	hostWithPort = regexp.MustCompile(`^([^:/\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+|\[[0-9a-fA-F:]+\]):([0-9]+)$`)
)

// relayUM4 is the relay of a recorded Node answer as the fixed rule gives
// it (UM4). Node read a URL with a port as a bare IPv6 address and kept a
// port that is not valid, adding a second one. The fixed rule cuts a
// URL's scheme and path and drops a port that is not valid first, and is
// Node's rule (nodeRelayEndpoint) otherwise.
func relayUM4(node string) string {
	if m := nodeRelayOfURL.FindStringSubmatch(node); m != nil {
		address, _, _ := strings.Cut(httpScheme.ReplaceAllString(m[1], ""), "/")
		if p := hostWithPort.FindStringSubmatch(address); p != nil && !isValidPort(p[2]) {
			address = p[1]
		}
		return nodeRelayEndpoint(address, m[2])
	}
	if m := nodeRelayIPv6TwoPorts.FindStringSubmatch(node); m != nil && !isValidPort(m[2]) {
		return "[" + m[1] + "]:" + m[3]
	}
	if m := nodeRelayTwoPorts.FindStringSubmatch(node); m != nil && !isValidPort(m[2]) {
		return m[1] + ":" + m[3]
	}
	return node
}

func isValidPort(digits string) bool {
	n, err := strconv.Atoi(digits)
	return err == nil && len(digits) <= 5 && n >= 1 && n <= 65535
}

// nodeRelayEndpoint is relayEndpointForHost as Node ran it, which the
// recorded vectors pin, with the port to add given.
func nodeRelayEndpoint(raw, relayPort string) string {
	if m := nodeRelayHostPort.FindStringSubmatch(raw); m != nil {
		if n, _ := strconv.Atoi(m[2]); n > 0 && n <= 65535 {
			return m[1] + ":" + strconv.Itoa(n)
		}
	}
	if m := nodeRelayIPv6.FindStringSubmatch(raw); m != nil {
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

var (
	nodeRelayHostPort = regexp.MustCompile(`^([^:/\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+):([0-9]{1,5})$`)
	nodeRelayIPv6     = regexp.MustCompile(`^\[([0-9a-fA-F:]+)\](?::([0-9]{1,5}))?$`)
)

// holdsIPv6Host reports whether the input of a recorded case holds an
// IPv6 address as a host: a text, or a part of one between white space,
// commas and "=", that is such an address after a scheme, a path and a
// port in brackets are cut off. Node cut such a host at its first colon;
// the SI3 fix reads it whole. Those cases may differ from Node's answers;
// TestNormalizeServerHostKeepsIPv6 and TestResolveIPv6Endpoints check
// what Go does with them.
func holdsIPv6Host(t testing.TB, input ...any) bool {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	return anyIPv6Host(decoded)
}

func anyIPv6Host(v any) bool {
	switch t := v.(type) {
	case string:
		for _, part := range hostSeparators.Split(t, -1) {
			if isIPv6HostText(part) {
				return true
			}
		}
	case []any:
		for _, e := range t {
			if anyIPv6Host(e) {
				return true
			}
		}
	case map[string]any:
		for _, e := range t {
			if anyIPv6Host(e) {
				return true
			}
		}
	}
	return false
}

var (
	hostSeparators = regexp.MustCompile(`[\s\p{Z},=\x{feff}]+`)
	schemePrefix   = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`)
	bracketedHost  = regexp.MustCompile(`^\[([^\]]*)\](:[0-9]*)?$`)
)

func isIPv6HostText(text string) bool {
	host := schemePrefix.ReplaceAllString(text, "")
	host, _, _ = strings.Cut(host, "/")
	if m := bracketedHost.FindStringSubmatch(host); m != nil {
		host = m[1]
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Is6()
}

// ipv6Cases lets the recorded cases whose input holds an IPv6 host
// differ from Node's answers (SI3); every other case must match them.
type ipv6Cases struct {
	t testing.TB
}

// mismatches is m for a case without an IPv6 host, and for a case with
// one a collector that drops its differences.
func (c *ipv6Cases) mismatches(m *mismatches, input ...any) *mismatches {
	c.t.Helper()
	if !holdsIPv6Host(c.t, input...) {
		return m
	}
	return &mismatches{t: dropped{c.t}}
}

// dropped takes the differences of a case with an IPv6 host.
type dropped struct{ testing.TB }

func (dropped) Helper()               {}
func (dropped) Errorf(string, ...any) {}
func (dropped) Fatalf(string, ...any) {}
