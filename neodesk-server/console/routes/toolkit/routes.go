// Package toolkit holds the tools of the console's toolkit page: a
// certificate check, hashes, a password generator, base64 and URL
// encoding, a token decoder, wake-on-LAN and the DNS records of a domain.
//
// The network tools (certificate check, DNS records) only reach hosts
// that pass console/ssrfguard in its mode for administrators: private
// addresses are allowed, loopback and link-local are not.
package toolkit

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"time"

	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/ssrfguard"
)

// Records reads the DNS records of a name. *net.Resolver is one.
type Records interface {
	LookupIP(ctx context.Context, network, host string) ([]net.IP, error)
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
	LookupTXT(ctx context.Context, name string) ([]string, error)
	LookupNS(ctx context.Context, name string) ([]*net.NS, error)
}

// Network is every way the group reaches out. Tests replace the parts
// they need; a nil part is the real one.
type Network struct {
	// Lookup resolves a host name to its addresses, for the guard.
	// Default: ssrfguard.SystemLookup.
	Lookup ssrfguard.LookupFunc
	// Dial opens the TCP connection of a certificate check. Default: a
	// net.Dialer.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// Handshake connects to port of one of the addresses the guard checked
	// for host and returns the state of the TLS connection it got, whatever
	// certificate the server shows. Default: Dial and crypto/tls.
	Handshake func(ctx context.Context, host string, addresses []string, port int) (*tls.ConnectionState, error)
	// Roots are the certificate authorities a certificate check trusts.
	// nil means the system's.
	Roots *x509.CertPool
	// Records reads DNS records. Default: net.DefaultResolver.
	Records Records
	// SendUDP sends one UDP packet from a new IPv4 socket that may send to
	// broadcast addresses. Default: sendUDP.
	SendUDP func(ctx context.Context, packet []byte, to *net.UDPAddr) error
}

// Options are what the group takes besides the console's services.
type Options struct {
	Network Network
	// Now is the clock. nil means time.Now.
	Now func() time.Time
	// Random is the source of the password generator. nil means
	// crypto/rand.
	Random io.Reader
	// SSLTimeout is how long a certificate check may take to connect and
	// shake hands. 0 means 8 seconds.
	SSLTimeout time.Duration
}

type handlers struct {
	log    *kit.Logger
	guard  *ssrfguard.Guard
	net    Network
	now    func() time.Time
	random io.Reader
	// sslTimeout bounds connecting and the handshake of a certificate check
	sslTimeout time.Duration
}

// Register adds the group's routes.
func Register(d *kit.Deps) { RegisterWith(d, Options{}) }

// RegisterWith is Register with the network, the clock and the random
// source of the caller.
func RegisterWith(d *kit.Deps, o Options) {
	h := newHandlers(d.Log.Child("TOOLKIT"), o)
	r := d.Router
	// Every tool asks device.connect, the toolkit section's permission
	connect := d.RequirePermission("device.connect")
	r.Post("/api/toolkit/ssl", h.ssl, d.RequireAuth, connect)
	r.Post("/api/toolkit/hash", h.hash, d.RequireAuth, connect)
	r.Post("/api/toolkit/password", h.password, d.RequireAuth, connect)
	r.Post("/api/toolkit/base64", h.base64, d.RequireAuth, connect)
	r.Post("/api/toolkit/wol", h.wol, d.RequireAuth, connect)
	r.Post("/api/toolkit/whois", h.whois, d.RequireAuth, connect)
	r.Post("/api/toolkit/urlencode", h.urlencode, d.RequireAuth, connect)
	r.Post("/api/toolkit/jwt-decode", h.jwtDecode, d.RequireAuth, connect)
}

// newHandlers fills in the real network, clock and random source where
// the options name none.
func newHandlers(log *kit.Logger, o Options) *handlers {
	if o.Network.Lookup == nil {
		o.Network.Lookup = ssrfguard.SystemLookup
	}
	if o.Network.Dial == nil {
		o.Network.Dial = (&net.Dialer{}).DialContext
	}
	if o.Network.Records == nil {
		o.Network.Records = net.DefaultResolver
	}
	if o.Network.SendUDP == nil {
		o.Network.SendUDP = sendUDP
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Random == nil {
		o.Random = rand.Reader
	}
	if o.SSLTimeout == 0 {
		o.SSLTimeout = sslTimeout
	}
	h := &handlers{
		log:        log,
		guard:      &ssrfguard.Guard{Lookup: o.Network.Lookup},
		net:        o.Network,
		now:        o.Now,
		random:     o.Random,
		sslTimeout: o.SSLTimeout,
	}
	if h.net.Handshake == nil {
		h.net.Handshake = h.handshake
	}
	return h
}
