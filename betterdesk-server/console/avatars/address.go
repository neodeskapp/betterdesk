package avatars

import (
	"net/netip"
	"strings"
)

// refusedV4 are the IPv4 ranges the console never fetches a picture from.
// Private LAN ranges are not among them: an identity provider often runs
// on the console's own network and its picture addresses point there.
var refusedV4 = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),          // "this host"; 0.0.0.0 reaches the local machine
	netip.MustParsePrefix("127.0.0.0/8"),        // loopback
	netip.MustParsePrefix("169.254.0.0/16"),     // link-local, cloud metadata (169.254.169.254)
	netip.MustParsePrefix("100.100.100.200/32"), // Alibaba Cloud metadata
	netip.MustParsePrefix("192.0.0.0/24"),       // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"),      // benchmarking
	netip.MustParsePrefix("224.0.0.0/4"),        // multicast
	netip.MustParsePrefix("240.0.0.0/4"),        // reserved and broadcast
}

// privateV4 are the IPv4 ranges of private networks, refused for a
// picture source that may not reach the console's network (FetchOptions
// RefusePrivate).
var privateV4 = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
}

// IsRefusedAddress reports whether the console must not fetch a picture
// from this address. Text that is no IP address is refused. An IPv6
// address may carry a zone of letters, digits and "-.:".
func IsRefusedAddress(ip string) bool {
	if i := strings.IndexByte(ip, '%'); i >= 0 {
		if !strings.Contains(ip[:i], ":") || !validZone(ip[i+1:]) {
			return true
		}
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return true
	}
	return refusedAddr(addr, false)
}

func validZone(zone string) bool {
	if zone == "" {
		return false
	}
	for i := 0; i < len(zone); i++ {
		c := zone[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-' || c == '.' || c == ':') {
			return false
		}
	}
	return true
}

// refusedAddr is IsRefusedAddress for a parsed address. With private set
// the addresses of private networks are refused too: privateV4, IPv6
// unique local (fc00::/7) and site-local (fec0::/10) addresses, and
// addresses that carry a private IPv4 address.
func refusedAddr(addr netip.Addr, private bool) bool {
	if !addr.IsValid() {
		return true
	}
	if addr.Is4() {
		return refusedV4Addr(addr, private)
	}
	b := addr.As16()
	var g [8]uint16
	for i := range g {
		g[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
	}
	zeroUpTo := func(n int) bool {
		for _, w := range g[:n] {
			if w != 0 {
				return false
			}
		}
		return true
	}
	v4 := func(hi, lo uint16) bool {
		return refusedV4Addr(netip.AddrFrom4([4]byte{byte(hi >> 8), byte(hi), byte(lo >> 8), byte(lo)}), private)
	}
	switch {
	case zeroUpTo(8): // ::
		return true
	case zeroUpTo(7) && g[7] == 1: // ::1
		return true
	case g[0]&0xffc0 == 0xfe80: // fe80::/10 link-local
		return true
	case g[0]&0xff00 == 0xff00: // ff00::/8 multicast
		return true
	case g[0] == 0xfd00 && g[1] == 0x0ec2 && g[2] == 0 && g[3] == 0 && g[4] == 0 && g[5] == 0 && g[6] == 0 && g[7] == 0x254:
		return true // AWS metadata fd00:ec2::254
	case private && (g[0]&0xfe00 == 0xfc00 || g[0]&0xffc0 == 0xfec0): // unique local, site-local
		return true
	}
	// Addresses that carry an IPv4 address are judged by that address
	switch {
	case zeroUpTo(5) && g[5] == 0xffff: // ::ffff:a.b.c.d
		return v4(g[6], g[7])
	case zeroUpTo(6): // ::a.b.c.d
		return v4(g[6], g[7])
	case g[0] == 0x64 && g[1] == 0xff9b && g[2] == 0 && g[3] == 0 && g[4] == 0 && g[5] == 0: // NAT64
		return v4(g[6], g[7])
	case g[0] == 0x2002: // 6to4
		return v4(g[1], g[2])
	}
	return false
}

func refusedV4Addr(addr netip.Addr, private bool) bool {
	for _, prefix := range refusedV4 {
		if prefix.Contains(addr) {
			return true
		}
	}
	if private {
		for _, prefix := range privateV4 {
			if prefix.Contains(addr) {
				return true
			}
		}
	}
	return false
}

// refusedHostnames are names that stand for this machine or for a cloud
// metadata service.
var refusedHostnames = map[string]bool{
	"localhost":                true,
	"localhost.localdomain":    true,
	"metadata.google.internal": true,
	"metadata.goog":            true,
}
