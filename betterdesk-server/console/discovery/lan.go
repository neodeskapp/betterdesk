// Package discovery lets clients on the local network find the console:
// a UDP responder that answers discovery probes with the server's
// identity (LAN), and an mDNS announcement of the service
// _betterdesk._tcp (MDNS).
//
// Both bind ports of their own (UDP 21119 and 5353). The console starts
// the LAN responder with its background jobs, and the mDNS announcement
// only when PANEL_MDNS asks for it.
package discovery

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/unitronix/betterdesk-server/logging"
)

// protocolVersion is the version field of probes and announcements.
const protocolVersion = 1

// Settings describe the console in an announcement.
type Settings struct {
	// Hostname returns the machine's name; os.Hostname when nil.
	Hostname func() (string, error)
	// Interfaces lists the network interfaces in the system's order;
	// SystemInterfaces when nil.
	Interfaces func() ([]Interface, error)
	// PubKeyPath is the server's public key file (PUB_KEY_PATH, else
	// id_ed25519.pub in KEYS_PATH). Its text goes out trimmed; a missing
	// or unreadable file sends "".
	PubKeyPath string
	// Host is HOST as written, "127.0.0.1" when unset.
	Host string
	// PublicHost is PANEL_PUBLIC_HOST: the host of the console's address
	// in the announcement, Host when empty.
	PublicHost string
	// PublicURL is PANEL_PUBLIC_URL: the console's address in the
	// announcement as written, built from the host, port and scheme when
	// empty.
	PublicURL string
	// Port is the port of the console's listener and APIPort the client
	// API port.
	Port    float64
	APIPort float64
	// HTTPS says whether the console's listener is on HTTPS.
	HTTPS bool
	// Version is the product version.
	Version string
	// DiscoveryPort is DISCOVERY_PORT read as parseInt(value, 10) ||
	// 21119, and 21119 for a number outside 1 to 65535.
	DiscoveryPort float64
	// BadDiscoveryPort is the DISCOVERY_PORT that was no port, for the
	// warning; "" when there was none.
	BadDiscoveryPort string
	// MDNS is PANEL_MDNS as written: "off" or "0" turn the mDNS
	// announcement off.
	MDNS string
}

// FromEnv reads the settings that come from the environment: HOST,
// PANEL_PUBLIC_HOST, PANEL_PUBLIC_URL, DISCOVERY_PORT and PANEL_MDNS. The
// caller fills in what the console's configuration knows.
func FromEnv(getenv func(string) string, s Settings) Settings {
	s.Host = getenv("HOST")
	if s.Host == "" {
		s.Host = "127.0.0.1"
	}
	s.PublicHost = getenv("PANEL_PUBLIC_HOST")
	s.PublicURL = getenv("PANEL_PUBLIC_URL")
	s.DiscoveryPort = parseIntOr(getenv("DISCOVERY_PORT"), 21119)
	if !(s.DiscoveryPort >= 1 && s.DiscoveryPort <= 65535) {
		s.BadDiscoveryPort, s.DiscoveryPort = getenv("DISCOVERY_PORT"), 21119
	}
	s.MDNS = getenv("PANEL_MDNS")
	return s
}

func (s Settings) hostname() (string, error) {
	if s.Hostname != nil {
		return s.Hostname()
	}
	return os.Hostname()
}

func (s Settings) interfaces() ([]Interface, error) {
	list := SystemInterfaces
	if s.Interfaces != nil {
		list = s.Interfaces
	}
	ifaces, err := list()
	if err != nil {
		return nil, err
	}
	return NodeOrder(ifaces), nil
}

// protocol is the console's scheme.
func (s Settings) protocol() string {
	if s.HTTPS {
		return "https"
	}
	return "http"
}

// ErrNoAddress says there is no address of the console that others on the
// network could open: it listens on loopback only, or on every address of
// a machine without a network address, and PANEL_PUBLIC_URL and
// PANEL_PUBLIC_HOST are unset. Nothing is announced then.
var ErrNoAddress = errors.New("the console listens on no address others can reach and neither PANEL_PUBLIC_URL nor PANEL_PUBLIC_HOST is set")

// LoopbackOnly reports whether the console listens on loopback only
// (HOST is a loopback address or localhost) and nothing names a public
// address.
func (s Settings) LoopbackOnly() bool {
	if s.PublicURL != "" || s.PublicHost != "" {
		return false
	}
	host := strings.TrimSuffix(strings.TrimPrefix(s.Host, "["), "]")
	ip := net.ParseIP(host)
	return strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()
}

// PanelURL is the console's address in the announcement:
// PANEL_PUBLIC_URL, else scheme, host and the port unless it is the
// scheme's own. The host is PANEL_PUBLIC_HOST, else HOST; a console that
// listens on every address (0.0.0.0, ::) is announced at the first of
// addresses, the machine's IPv4 addresses that are not loopback.
func (s Settings) PanelURL(addresses []string) (string, error) {
	if s.PublicURL != "" {
		return decodeUTF8([]byte(s.PublicURL)), nil
	}
	host := s.PublicHost
	if host == "" {
		switch strings.TrimSuffix(strings.TrimPrefix(s.Host, "["), "]") {
		case "0.0.0.0", "::", "":
			if len(addresses) == 0 {
				return "", ErrNoAddress
			}
			host = addresses[0]
		default:
			if s.LoopbackOnly() {
				return "", ErrNoAddress
			}
			host = s.Host
		}
	}
	protocol := s.protocol()
	url := protocol + "://" + decodeUTF8([]byte(host))
	if (protocol == "https" && s.Port == 443) || (protocol == "http" && s.Port == 80) {
		return url, nil
	}
	return url + ":" + numberString(s.Port), nil
}

// addresses are the IPv4 addresses of the interfaces that are not
// loopback.
func addresses(ifaces []Interface) []string {
	var out []string
	for _, iface := range ifaces {
		for _, entry := range iface.Entries {
			if entry.Family == "IPv4" && !entry.Internal {
				out = append(out, entry.Address)
			}
		}
	}
	return out
}

// Announcement is the answer to a probe, as the old console's
// JSON.stringify wrote it: the machine's name, the product version, the
// ports, the scheme, the public key, the IPv4 addresses that are not
// loopback, the discovery port and the console's address. Hostname,
// interfaces and key are read anew for each answer. Without an address
// to announce it is ErrNoAddress.
func (s Settings) Announcement() ([]byte, error) {
	publicKey := ""
	if raw, err := os.ReadFile(s.PubKeyPath); err == nil {
		publicKey = jsTrim(decodeUTF8(raw))
	}
	ifaces, err := s.interfaces()
	if err != nil {
		return nil, err
	}
	name, err := s.hostname()
	if err != nil {
		return nil, err
	}
	list := addresses(ifaces)
	panelURL, err := s.PanelURL(list)
	if err != nil {
		return nil, err
	}
	out := []byte(`{"type":"betterdesk-announce","version":` + strconv.Itoa(protocolVersion) + `,"server":{"name":`)
	out = appendJSONString(out, decodeUTF8([]byte(name)))
	out = append(out, `,"version":`...)
	out = appendJSONString(out, s.Version)
	out = append(out, `,"port":`...)
	out = appendJSONNumber(out, s.Port)
	out = append(out, `,"apiPort":`...)
	out = appendJSONNumber(out, s.APIPort)
	out = append(out, `,"protocol":`...)
	out = appendJSONString(out, s.protocol())
	out = append(out, `,"publicKey":`...)
	out = appendJSONString(out, publicKey)
	out = append(out, `,"addresses":[`...)
	for i, address := range list {
		if i > 0 {
			out = append(out, ',')
		}
		out = appendJSONString(out, address)
	}
	out = append(out, `],"discoveryPort":`...)
	out = appendJSONNumber(out, s.DiscoveryPort)
	out = append(out, `,"panelUrl":`...)
	out = appendJSONString(out, panelURL)
	return append(out, "}}"...), nil
}

// The responder answers a sender at most answersPerSecond times a
// second, and keeps count of at most maxSenders senders a second; a
// sender beyond them waits for the next second.
const (
	answersPerSecond = 5
	maxSenders       = 1024
	// networksFor is how long the networks of the host's interfaces are
	// kept before they are read again.
	networksFor = 10 * time.Second
)

// carrierGradeNAT is the shared address space of RFC 6598.
var carrierGradeNAT = &net.IPNet{IP: net.IPv4(100, 64, 0, 0).To4(), Mask: net.CIDRMask(10, 32)}

// LAN is the UDP responder.
type LAN struct {
	settings Settings
	logf     func(format string, args ...any)
	listen   func(ctx context.Context, address string) (net.PacketConn, error)
	now      func() time.Time

	// second is the second the counts in answered belong to
	second   time.Time
	answered map[string]int
	// networks are those of the host's interfaces, read at networksAt
	networks   []*net.IPNet
	networksAt time.Time
}

// NewLAN returns a responder for the settings. Its lines go to the log
// under "console".
func NewLAN(settings Settings) *LAN {
	return &LAN{
		settings: settings,
		logf:     func(format string, args ...any) { logging.Logf("console", format, args...) },
		listen: func(ctx context.Context, address string) (net.PacketConn, error) {
			lc := net.ListenConfig{Control: reuseAddress}
			return lc.ListenPacket(ctx, "udp4", address)
		},
		now:      time.Now,
		answered: map[string]int{},
	}
}

// Run binds 0.0.0.0 at the discovery port (an address others may bind
// too, as Node's reuseAddr allowed) and answers probes until ctx ends.
// A console that listens on loopback only, with no public address set,
// is not announced: Run logs that and returns. A port that cannot be
// bound is logged and the responder stays off; nothing it meets ends the
// process.
func (l *LAN) Run(ctx context.Context) {
	if l.settings.LoopbackOnly() {
		l.logf("  LAN Discovery off: %v", ErrNoAddress)
		return
	}
	port := l.settings.DiscoveryPort
	if l.settings.BadDiscoveryPort != "" {
		l.logf("WARN: LAN Discovery: DISCOVERY_PORT=%q is no port (1-65535); using %s", l.settings.BadDiscoveryPort, numberString(port))
	}
	conn, err := l.listen(ctx, net.JoinHostPort("0.0.0.0", strconv.Itoa(int(port))))
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			l.logf("WARN: LAN Discovery: port %s already in use, service disabled", numberString(port))
		} else {
			l.logf("ERROR: LAN Discovery error: %v", err)
		}
		return
	}
	l.logf("  LAN Discovery active on UDP port %s", numberString(port))
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer conn.Close()
	buf := make([]byte, 64*1024)
	for {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			l.logf("ERROR: LAN Discovery error: %v", err)
			continue
		}
		l.answer(conn, buf[:n], from)
	}
}

// answer sends the announcement to the sender of a probe. Only senders on
// a local network get one, a few times a second at most: the answer is
// about ten times the probe's size, and must not be sent to an address a
// stranger wrote into a probe. Anything that goes wrong on the way is
// dropped silently, as Node did, except a failed send.
func (l *LAN) answer(conn net.PacketConn, msg []byte, from net.Addr) {
	if !IsProbe(msg) {
		return
	}
	udp, ok := from.(*net.UDPAddr)
	if !ok || !l.local(udp.IP) || !l.allow(udp.IP) {
		return
	}
	reply, err := l.settings.Announcement()
	if err != nil {
		return
	}
	if _, err := conn.WriteTo(reply, from); err != nil {
		l.logf("WARN: LAN Discovery: failed to send reply: %v", err)
	}
}

// local reports whether ip is on a local network: loopback, a private,
// link-local or carrier-grade NAT range, or the network of one of the
// host's interfaces.
func (l *LAN) local(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || carrierGradeNAT.Contains(ip) {
		return true
	}
	now := l.now()
	if l.networksAt.IsZero() || now.Sub(l.networksAt) >= networksFor || now.Before(l.networksAt) {
		l.networks, l.networksAt = nil, now
		if ifaces, err := l.settings.interfaces(); err == nil {
			for _, iface := range ifaces {
				for _, entry := range iface.Entries {
					if network := entry.network(); network != nil {
						l.networks = append(l.networks, network)
					}
				}
			}
		}
	}
	for _, network := range l.networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// network is the network the address is on, nil when its prefix is not
// known.
func (a Address) network() *net.IPNet {
	ip := net.ParseIP(a.Address)
	if ip == nil || a.Prefix <= 0 {
		return nil
	}
	bits := 8 * net.IPv6len
	if v4 := ip.To4(); v4 != nil {
		ip, bits = v4, 8*net.IPv4len
	}
	if a.Prefix > bits {
		return nil
	}
	mask := net.CIDRMask(a.Prefix, bits)
	return &net.IPNet{IP: ip.Mask(mask), Mask: mask}
}

// allow counts an answer to ip and reports whether it may go out.
func (l *LAN) allow(ip net.IP) bool {
	second := l.now().Truncate(time.Second)
	if !second.Equal(l.second) {
		l.second = second
		clear(l.answered)
	}
	key := ip.String()
	n, known := l.answered[key]
	if n >= answersPerSecond || (!known && len(l.answered) >= maxSenders) {
		return false
	}
	l.answered[key] = n + 1
	return true
}
