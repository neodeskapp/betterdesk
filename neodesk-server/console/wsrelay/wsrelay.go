// Package wsrelay is the web remote client's way to the signal and relay
// servers (services/wsRelay.js of the old console). The browser's RustDesk
// client opens /ws/rendezvous, which the console bridges to a TCP
// connection to the signal server, and /ws/relay, bridged to the relay
// server. The console passes the bytes on in both directions; with
// ?transport=message on /ws/relay each websocket message is one RustDesk
// frame and the console adds and removes the BytesCodec header.
//
// Who may open a bridge: a signed-in account whose role has
// device.connect, or a visitor with a guest access link for one device
// (?guest= or ?t=, else the guest cookie, with ?peer_id= and
// ?guest_session=), which the API server must find good. A guest's frames
// are read: every request to punch a hole or to relay must name the
// link's device, the first one uses up one use of the link, the link is
// checked again every 15 seconds and at its end, and taking the link back
// (InvalidateGuestGrant) ends its bridges at once.
//
// The bridge dials the signal and relay servers over TCP even though they
// run in this process. Its first frame to the signal server is the panel
// proof (Options.SignalProof), a secret the signal server made at start:
// with it, from an address of PANEL_SIGNAL_PROXY_CIDRS (default loopback),
// the signal server lets the bridge start connections as the panel's web
// client without a registered peer. The proof of a signed-in account's
// bridge names the account, so the signal server applies the account's
// client features to the sessions it starts; a guest's names the account
// that made the link (the link's created_by_id). A signal server in another
// process does not know the secret and treats the bridge as any client. The relay
// counts and pairs connections by their remote address, so a connection
// handed over in memory would change its decisions; for the same reason a
// relay address on loopback is replaced by the machine's first other IPv4
// address: the relay treats loopback connections as its admin interface.
//
// Differences from the old service, on purpose: Node handled a guest's
// messages in asynchronous callbacks, so a later frame could reach the
// server while an earlier one still waited for the use of the link to be
// counted (and was sent even when that failed); here a bridge handles its
// messages one after the other. The old service answered 503 when it was
// set up without the session middleware; the Go console always has
// sessions, so that answer does not exist here.
package wsrelay

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// The limits of the old relay proxy.
const (
	// MaxConnectionsPerIP is how many bridges one client address may hold,
	// over both paths together.
	MaxConnectionsPerIP = 5
	// IdleTimeout ends a bridge that passed nothing in either direction
	// for this long.
	IdleTimeout = 120 * time.Second
	// GuestRevalidationInterval is how often a guest's link is checked
	// again while the bridge is open.
	GuestRevalidationInterval = 15 * time.Second
	// guestRelayBuffer is how much a guest may send on /ws/relay before
	// its relay request decodes.
	guestRelayBuffer = 1 << 20
	// validateTimeout is the limit of a link check with the API server.
	validateTimeout = 5 * time.Second
	// SessionCheck is how often a signed-in bridge's session and
	// permission are looked at again.
	SessionCheck = 10 * time.Second
)

// Clock is the time the bridges see: Date.now and setTimeout.
type Clock interface {
	Now() time.Time
	// AfterFunc calls f once after d in its own goroutine, unless the
	// timer is stopped first.
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer is a pending AfterFunc.
type Timer interface {
	// Stop prevents the call; it reports whether it did.
	Stop() bool
}

type realClock struct{}

func (realClock) Now() time.Time                            { return time.Now() }
func (realClock) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }

// InterfaceAddr is one address of a network interface, as
// os.networkInterfaces() listed it.
type InterfaceAddr struct {
	// Family is "IPv4" or "IPv6".
	Family string
	// Address is the address as text.
	Address string
	// Internal is true for the addresses of a loopback interface.
	Internal bool
}

// Options configure the service.
type Options struct {
	// SignalHost and SignalPort are the signal server's TCP address
	// (WS_HBBS_HOST, default localhost; WS_HBBS_PORT, default 21116).
	SignalHost string
	SignalPort int
	// SignalProof returns the frame a bridge writes to the signal server
	// before anything of the browser's (signal.Server.PanelProofFor of
	// this process), for the bridge of the account with the id, the link's
	// creator for a guest, 0 for none: by it the signal server takes the
	// connection for the panel's
	// web client, which needs no registered peer, and knows the account.
	// nil, or an empty frame, writes none, and the signal server treats
	// the bridge as any client.
	SignalProof func(userID int64) []byte
	// RelayHost and RelayPort are the relay server's TCP address
	// (WS_HBBR_HOST, default localhost; WS_HBBR_PORT, default 21117). A
	// loopback host is replaced at New by the first IPv4 address of
	// Interfaces that is not internal.
	RelayHost string
	RelayPort int
	// GoAPI checks guest links (/guest/access-links/validate), counts
	// their uses (/consume) and records guest sessions (/events).
	GoAPI *goapi.Client
	// GuestCookie is the name of the guest cookie (GUEST_COOKIE:
	// nd.guest, neodesk.guest in HTTPS mode). nd.guest and
	// neodesk.guest are read after it.
	GuestCookie string
	// Dial opens the TCP connection to a server. nil: a net.Dialer
	// without a timeout of its own, as the old service had none; the
	// dial ends when the bridge does.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// Clock is real time when nil.
	Clock Clock
	// Interfaces lists the machine's addresses in the order the old
	// console saw them. nil: the interfaces of the net package.
	Interfaces func() []InterfaceAddr
	// Devices looks a device up for the device scope of a signed-in
	// account (the console's device table, remoterelay.Service.Device);
	// nil is no device.
	Devices func(ctx context.Context, id string) (*goapi.Object, error)
	// CanAccess is the account's device scope
	// (devicegroups.Service.UserCanAccessDevice); a nil device passes only
	// for an account without a limit. nil: every account sees every
	// device.
	CanAccess func(ctx context.Context, user *session.User, device *goapi.Object) (bool, error)
	// SessionCheck overrides SessionCheck (tests).
	SessionCheck time.Duration
}

// Service holds the bridges of the two sockets.
type Service struct {
	opts       Options
	clock      Clock
	signalAddr string
	relayAddr  string
	// relayHost is the relay's host after the loopback replacement.
	relayHost string

	mu       sync.Mutex
	perIP    *ws.AddrCounter
	grants   map[string][]*bridge
	consumes map[string]*consumeState
	// counted are the guest sessions whose use of a link was counted, by
	// countedKey, while the use is remembered (see consume).
	counted map[string]countedUse
}

// New returns the service; Register adds its sockets.
func New(opts Options) *Service {
	if opts.Clock == nil {
		opts.Clock = realClock{}
	}
	if opts.Dial == nil {
		var d net.Dialer
		opts.Dial = d.DialContext
	}
	if opts.Interfaces == nil {
		opts.Interfaces = systemInterfaces
	}
	if opts.GuestCookie == "" {
		opts.GuestCookie = "nd.guest"
	}
	if opts.SessionCheck <= 0 {
		opts.SessionCheck = SessionCheck
	}
	signalHost := orText(opts.SignalHost, "localhost")
	signalPort := orPort(opts.SignalPort, 21116)
	relayHost := orText(opts.RelayHost, "localhost")
	relayPort := orPort(opts.RelayPort, 21117)
	if isLoopbackHost(relayHost) {
		if other := nonLoopbackIPv4(opts.Interfaces()); other != "" {
			logf("  WebSocket proxy: hbbr host changed from '%s' to '%s' (avoiding loopback command mode)", relayHost, other)
			relayHost = other
		} else {
			logf("  WebSocket proxy: WARNING - could not find non-loopback IP for hbbr, relay connections may fail!")
		}
	}
	logf("  WebSocket proxy: /ws/rendezvous -> %s:%d", signalHost, signalPort)
	logf("  WebSocket proxy: /ws/relay -> %s:%d", relayHost, relayPort)
	return &Service{
		opts: opts, clock: opts.Clock,
		signalAddr: net.JoinHostPort(signalHost, strconv.Itoa(signalPort)),
		relayAddr:  net.JoinHostPort(relayHost, strconv.Itoa(relayPort)),
		relayHost:  relayHost,
		perIP:      ws.NewAddrCounter(),
		grants:     map[string][]*bridge{},
		consumes:   map[string]*consumeState{},
		counted:    map[string]countedUse{},
	}
}

func orText(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func orPort(p, fallback int) int {
	if p == 0 {
		return fallback
	}
	return p
}

// isLoopbackHost reports a host name the relay would see as loopback.
func isLoopbackHost(host string) bool {
	lower := strings.ToLower(host)
	return lower == "localhost" || lower == "127.0.0.1" || lower == "::1" || strings.HasPrefix(lower, "127.")
}

// nonLoopbackIPv4 is the first IPv4 address that is not internal, "" when
// there is none.
func nonLoopbackIPv4(addrs []InterfaceAddr) string {
	for _, a := range addrs {
		if a.Family == "IPv4" && !a.Internal {
			return a.Address
		}
	}
	return ""
}

// systemInterfaces lists the addresses of the machine's interfaces in
// interface order; those of a loopback interface are internal.
func systemInterfaces() []InterfaceAddr {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []InterfaceAddr
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			family := "IPv6"
			if ipnet.IP.To4() != nil {
				family = "IPv4"
			}
			out = append(out, InterfaceAddr{Family: family, Address: ipnet.IP.String(), Internal: iface.Flags&net.FlagLoopback != 0})
		}
	}
	return out
}

// Register adds /ws/rendezvous and /ws/relay.
func (s *Service) Register(reg *ws.Registry) {
	reg.Handle("/ws/rendezvous", func(u *ws.Upgrade) { s.serve(u, "rendezvous") })
	reg.Handle("/ws/relay", func(u *ws.Upgrade) { s.serve(u, "relay") })
}

func logf(format string, args ...any) { logging.Logf("console", format, args...) }
