// Package discovery lets clients on the local network find the console:
// a UDP responder that answers discovery probes with the server's
// identity (LAN), and an mDNS announcement of the service
// _betterdesk._tcp (MDNS).
//
// Both bind ports of their own (UDP 21119 and 5353). While the Node
// console runs behind this one it binds them itself, so the console
// starts them only when it serves everything.
package discovery

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
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
	// Port is the console's port and APIPort the client API port.
	Port    float64
	APIPort float64
	// HTTPS says whether the console is on HTTPS (HTTPS_ENABLED).
	HTTPS bool
	// Version is the product version.
	Version string
	// DiscoveryPort is DISCOVERY_PORT read as parseInt(value, 10) ||
	// 21119.
	DiscoveryPort float64
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

// PanelURL is the console's address in the announcement:
// PANEL_PUBLIC_URL, else scheme, host and the port unless it is the
// scheme's own.
func (s Settings) PanelURL() string {
	if s.PublicURL != "" {
		return decodeUTF8([]byte(s.PublicURL))
	}
	host := s.PublicHost
	if host == "" {
		host = s.Host
	}
	protocol := s.protocol()
	url := protocol + "://" + decodeUTF8([]byte(host))
	if (protocol == "https" && s.Port == 443) || (protocol == "http" && s.Port == 80) {
		return url
	}
	return url + ":" + numberString(s.Port)
}

// Announcement is the answer to a probe, as the old console's
// JSON.stringify wrote it: the machine's name, the product version, the
// ports, the scheme, the public key, the IPv4 addresses that are not
// loopback, the discovery port and the console's address. Hostname,
// interfaces and key are read anew for each answer.
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
	first := true
	for _, iface := range ifaces {
		for _, entry := range iface.Entries {
			if entry.Family != "IPv4" || entry.Internal {
				continue
			}
			if !first {
				out = append(out, ',')
			}
			first = false
			out = appendJSONString(out, entry.Address)
		}
	}
	out = append(out, `],"discoveryPort":`...)
	out = appendJSONNumber(out, s.DiscoveryPort)
	out = append(out, `,"panelUrl":`...)
	out = appendJSONString(out, s.PanelURL())
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
// A port that cannot be bound is logged and the responder stays off;
// nothing it meets ends the process.
func (l *LAN) Run(ctx context.Context) {
	port := l.settings.DiscoveryPort
	conn, err := l.listen(ctx, net.JoinHostPort("0.0.0.0", strconv.Itoa(int(toUint16(port)))))
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
