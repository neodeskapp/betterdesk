// Package guardtest says where console/ssrfguard answers otherwise than
// the Node guard (web-nodejs/lib/ssrfGuard.js) on purpose, and which row
// of BUGS.md that is. Tests that replay what the Node console answered use
// it to tell a fixed bug from a regression.
//
// Both rules are written here without the guard's code: the guard's from
// the ranges alone, Node's as lib/ssrfGuard.js has it.
package guardtest

import (
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

func prefixes(texts ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(texts))
	for i, text := range texts {
		out[i] = netip.MustParsePrefix(text)
	}
	return out
}

var (
	always  = prefixes("127.0.0.0/8", "169.254.0.0/16", "::1/128", "fe80::/10")
	private = prefixes("0.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "198.18.0.0/15", "fc00::/7")
)

// address reads ip as the guard must see it: without a zone, and an IPv4
// address carried in an IPv6 one (::ffff:a.b.c.d, ::a.b.c.d) as that IPv4
// address.
func address(ip string) (netip.Addr, bool) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return netip.Addr{}, false
	}
	addr = addr.WithZone("")
	if addr.Is4In6() {
		return addr.Unmap(), true
	}
	b := addr.As16()
	if addr.Is6() && !addr.IsUnspecified() && !addr.IsLoopback() && [12]byte(b[:12]) == [12]byte{} {
		return netip.AddrFrom4([4]byte(b[12:])), true
	}
	return addr, true
}

func within(addr netip.Addr, ranges []netip.Prefix) bool {
	for _, r := range ranges {
		if r.Contains(addr) {
			return true
		}
	}
	return false
}

// Blocked is what the guard answers for an address now: true when it must
// not be reached.
func Blocked(ip string, allowPrivate bool) bool {
	addr, ok := address(ip)
	if !ok {
		return true
	}
	return within(addr, always) || !allowPrivate && within(addr, private)
}

// Node's net.isIPv4 and net.isIPv6.
const (
	v4Seg = `(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])`
	v4Str = `(?:` + v4Seg + `\.){3}` + v4Seg
	v6Seg = `(?:[0-9a-fA-F]{1,4})`
)

var (
	ipv4Reg = regexp.MustCompile(`^` + v4Str + `$`)
	ipv6Reg = regexp.MustCompile(`^(?:` +
		`(?:` + v6Seg + `:){7}(?:` + v6Seg + `|:)|` +
		`(?:` + v6Seg + `:){6}(?:` + v4Str + `|:` + v6Seg + `|:)|` +
		`(?:` + v6Seg + `:){5}(?::` + v4Str + `|(?::` + v6Seg + `){1,2}|:)|` +
		`(?:` + v6Seg + `:){4}(?:(?::` + v6Seg + `){0,1}:` + v4Str + `|(?::` + v6Seg + `){1,3}|:)|` +
		`(?:` + v6Seg + `:){3}(?:(?::` + v6Seg + `){0,2}:` + v4Str + `|(?::` + v6Seg + `){1,4}|:)|` +
		`(?:` + v6Seg + `:){2}(?:(?::` + v6Seg + `){0,3}:` + v4Str + `|(?::` + v6Seg + `){1,5}|:)|` +
		`(?:` + v6Seg + `:){1}(?:(?::` + v6Seg + `){0,4}:` + v4Str + `|(?::` + v6Seg + `){1,6}|:)|` +
		`(?::(?:(?::` + v6Seg + `){0,5}:` + v4Str + `|(?::` + v6Seg + `){1,7}|:))` +
		`)(?:%[0-9a-zA-Z\-.:]{1,})?$`)
)

func isIPv4(s string) bool { return ipv4Reg.MatchString(s) }
func isIPv6(s string) bool { return ipv6Reg.MatchString(s) }

// NodeBlocked is isBlockedIp of the Node guard, which compared text.
func NodeBlocked(ip string, allowPrivate bool) bool {
	if ip == "" {
		return true
	}
	if isIPv4(ip) {
		parts := strings.Split(ip, ".")
		a, _ := strconv.Atoi(parts[0])
		b, _ := strconv.Atoi(parts[1])
		switch {
		case a == 127, a == 169 && b == 254:
			return true
		case allowPrivate:
			return false
		}
		return a == 0 || a == 10 || a == 172 && b >= 16 && b <= 31 || a == 192 && b == 168 ||
			a == 100 && b >= 64 && b <= 127 || a == 198 && (b == 18 || b == 19)
	}
	if !isIPv6(ip) {
		return false
	}
	lower := strings.ToLower(ip)
	switch {
	case lower == "::1", strings.HasPrefix(lower, "fe80:"):
		return true
	case !allowPrivate && (strings.HasPrefix(lower, "fc") || strings.HasPrefix(lower, "fd")):
		return true
	}
	if mapped, ok := strings.CutPrefix(lower, "::ffff:"); ok && isIPv4(mapped) {
		return NodeBlocked(mapped, allowPrivate)
	}
	return false
}

// Change names the row of BUGS.md whose fix makes the guard answer
// otherwise for an address than the Node guard did: "" when the two agree.
func Change(ip string, allowPrivate bool) string {
	now, before := Blocked(ip, allowPrivate), NodeBlocked(ip, allowPrivate)
	switch {
	case now == before:
		return ""
	case !now:
		// Node refused any text that starts with fc or fd
		return "SC6"
	}
	if _, ok := address(ip); !ok {
		return "SC5"
	}
	return "SC1"
}

// Refused reads the address out of a refusal of the guard that names one:
// "Blocked IP address: <address>" and "Hostname <name> resolves to blocked
// address <address>".
func Refused(message string) (string, bool) {
	if address, ok := strings.CutPrefix(message, "Blocked IP address: "); ok {
		return address, true
	}
	if rest, ok := strings.CutPrefix(message, "Hostname "); ok {
		if _, address, ok := strings.Cut(rest, " resolves to blocked address "); ok {
			return address, true
		}
	}
	return "", false
}

// MessageChange names the row of BUGS.md that explains why a check of the
// guard ends with goErr where Node's ended with nodeErr ("" stands for a
// check that passed). It is "" when the two agree and "unexplained" when
// no fixed row explains the difference: either Node refused an address the
// guard lets through now, or the guard refuses an address Node let
// through.
func MessageChange(nodeErr, goErr string, allowPrivate bool) string {
	if nodeErr == goErr {
		return ""
	}
	if address, ok := Refused(nodeErr); ok {
		if bug := Change(address, allowPrivate); bug == "SC6" {
			return bug
		}
	}
	if address, ok := Refused(goErr); ok {
		if bug := Change(address, allowPrivate); bug != "" && bug != "SC6" {
			return bug
		}
	}
	return "unexplained"
}
