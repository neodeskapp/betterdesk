// Package httpx holds the HTTP plumbing every console package shares: how
// the client's address and scheme are derived, JSON answers and body limits.
package httpx

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// TrustProxy decides which hops in front of the console may speak for the
// client. It follows the TRUST_PROXY setting of the console:
//
//	unset, false, 0, no, off   trust nobody: the socket address is the client
//	true, 1, yes, y, on        trust one hop
//	a number n                 trust n hops
//	loopback, linklocal, uniquelocal, addresses or CIDR ranges, comma separated
//	                           trust hops whose address is in the list
type TrustProxy struct {
	hops int
	nets []*net.IPNet
}

var namedRanges = map[string][]string{
	"loopback":    {"127.0.0.1/8", "::1/128"},
	"linklocal":   {"169.254.0.0/16", "fe80::/10"},
	"uniquelocal": {"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"},
}

// ParseTrustProxy reads a TRUST_PROXY value.
func ParseTrustProxy(raw string) (TrustProxy, error) {
	trimmed := strings.TrimSpace(raw)
	switch strings.ToLower(trimmed) {
	case "", "false", "0", "no", "off":
		return TrustProxy{}, nil
	case "true", "1", "yes", "y", "on":
		return TrustProxy{hops: 1}, nil
	}
	if n, err := strconv.Atoi(trimmed); err == nil && strconv.Itoa(n) == trimmed {
		if n < 0 {
			n = 0
		}
		return TrustProxy{hops: n}, nil
	}
	var tp TrustProxy
	for _, part := range strings.Split(trimmed, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		ranges, ok := namedRanges[strings.ToLower(part)]
		if !ok {
			ranges = []string{part}
		}
		for _, cidr := range ranges {
			ipnet, err := parseRange(cidr)
			if err != nil {
				return TrustProxy{}, fmt.Errorf("trust proxy %q: %w", part, err)
			}
			tp.nets = append(tp.nets, ipnet)
		}
	}
	return tp, nil
}

func parseRange(s string) (*net.IPNet, error) {
	if !strings.Contains(s, "/") {
		ip := net.ParseIP(s)
		if ip == nil {
			return nil, fmt.Errorf("invalid IP address: %s", s)
		}
		bits := 128
		if ip.To4() != nil {
			bits = 32
		}
		return &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}, nil
	}
	_, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		return nil, fmt.Errorf("invalid range: %s", s)
	}
	return ipnet, nil
}

// Enabled reports whether any hop is trusted.
func (tp TrustProxy) Enabled() bool { return tp.hops > 0 || len(tp.nets) > 0 }

// trusts reports whether the hop with this address, i hops away from the
// console (0 is the socket), may speak for the hop before it.
func (tp TrustProxy) trusts(addr string, i int) bool {
	if tp.hops > 0 {
		return i < tp.hops
	}
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range tp.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// SocketIP is the address of the connection, without the port.
func SocketIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ClientIP is the client's address: the socket address, or with trusted
// proxies the first address in the chain (socket, then X-Forwarded-For from
// right to left) that is not a trusted hop. When every hop is trusted it is
// the leftmost address.
func (tp TrustProxy) ClientIP(r *http.Request) string {
	socket := SocketIP(r)
	if !tp.Enabled() {
		return socket
	}
	var forwarded []string
	for _, part := range strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",") {
		if part = strings.TrimSpace(part); part != "" {
			forwarded = append(forwarded, part)
		}
	}
	// The socket first, then X-Forwarded-For from right to left
	chain := make([]string, 0, len(forwarded)+1)
	chain = append(chain, socket)
	for i := len(forwarded) - 1; i >= 0; i-- {
		chain = append(chain, forwarded[i])
	}
	for i, addr := range chain {
		if i == len(chain)-1 || !tp.trusts(addr, i) {
			return addr
		}
	}
	return socket
}

// Scheme is "https" when the connection is TLS, or when the socket is a
// trusted proxy that says so in X-Forwarded-Proto; "http" otherwise.
func (tp TrustProxy) Scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if !tp.Enabled() || !tp.trusts(SocketIP(r), 0) {
		return "http"
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if i := strings.IndexByte(proto, ','); i >= 0 {
		proto = proto[:i]
	}
	if proto = strings.TrimSpace(proto); proto != "" {
		return strings.ToLower(proto)
	}
	return "http"
}

// Host is the host the client asked for: X-Forwarded-Host from a trusted
// proxy, the Host header otherwise.
func (tp TrustProxy) Host(r *http.Request) string {
	if tp.Enabled() && tp.trusts(SocketIP(r), 0) {
		host := r.Header.Get("X-Forwarded-Host")
		if i := strings.IndexByte(host, ','); i >= 0 {
			host = host[:i]
		}
		if host = strings.TrimSpace(host); host != "" {
			return host
		}
	}
	return r.Host
}
