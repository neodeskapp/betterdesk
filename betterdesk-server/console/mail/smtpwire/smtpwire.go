// Package smtpwire sends one message over SMTP, or checks that a server
// accepts the configured login, and does on the wire what nodemailer 10.4.0
// does for the options the console stores: the same commands in the same
// order for every server behaviour, the same choice of port, address, TLS
// and login mechanism, the same timeouts and the same error texts.
//
// Every call opens its own connection and closes it again. A message is sent
// as: connect, greeting, EHLO (HELO when EHLO is refused), STARTTLS when the
// server offers it, login when there are credentials and the server allows
// it, MAIL FROM, RCPT TO, DATA, the message, close (no QUIT). Verify stops
// after the login and sends QUIT.
//
// Credentials and TLS: STARTTLS is used whenever the server offers it, and a
// refused or failed upgrade ends the call. With RequireTLS, a call with
// credentials asks for STARTTLS also when the server does not offer it and
// does not fall back to HELO, so the login never goes out in plain text
// (nodemailer's requireTLS). Without RequireTLS, a server that does not offer
// STARTTLS (or only answers HELO) gets the login in plain text.
//
// # Where it differs from nodemailer
//
//   - Replies that wait behind a continued reply ("250-...") the server never
//     ends count against the 1 MiB reply limit. nodemailer keeps them without
//     a limit.
//   - Replies that arrived before the server closed are handled before the
//     close is. nodemailer may see the close first, depending on timing.
//   - Error texts made by OpenSSL are Go's texts here: a peer that does not
//     speak TLS, TLS alerts, and certificate failures other than the ones
//     listed below. The texts for "self-signed certificate", "self-signed
//     certificate in certificate chain", "unable to verify the first
//     certificate", "unable to get local issuer certificate", "certificate
//     has expired", "certificate is not yet valid", "unsuitable certificate
//     purpose", "key usage does not include certificate signing" and
//     "Hostname/IP does not match certificate's altnames: ..." are Node's,
//     as are "connect ECONNREFUSED 127.0.0.1:587", "getaddrinfo ENOTFOUND
//     host", "read ECONNRESET" and the like.
//   - A refused certificate ends the TLS handshake. Node finishes the
//     handshake and then drops the connection. No command is sent either way.
//   - Trust comes from the roots of the operating system, not from the list
//     of roots built into Node (plus NODE_EXTRA_CA_CERTS). The TLS versions
//     and cipher suites are those of crypto/tls, which has no RSA or DHE key
//     exchange by default. A certificate crypto/x509 cannot parse (a name
//     outside ASCII in it, for one) fails the handshake even without
//     TLSVerify.
//   - A host name outside ASCII is not converted to punycode, neither for the
//     lookup nor for the certificate check. A trailing dot of the host is not
//     part of the server name sent with TLS (SNI).
//   - The A and AAAA lookups of the default resolver read the hosts file
//     first. There is no cache of lookups, so an address is never reused for
//     five minutes or kept when a later lookup fails. The interfaces of the
//     machine are read for every call, not once per process.
//   - A call ends early with the context's error when its context is done.
package smtpwire

import (
	"context"
	"crypto/x509"
	"net"
	"time"
)

// Timeouts nodemailer uses when the options name none.
const (
	DefaultConnectionTimeout = 2 * time.Minute
	DefaultGreetingTimeout   = 30 * time.Second
	DefaultSocketTimeout     = 10 * time.Minute
	DefaultDNSTimeout        = 30 * time.Second
)

// closeTimeout is how long a closed connection waits for the server to close
// its side before it is dropped.
const closeTimeout = 5 * time.Second

// Resolver looks up the addresses of the SMTP host. *net.Resolver has both
// methods.
type Resolver interface {
	// LookupIP returns the A records (network "ip4") or the AAAA records
	// (network "ip6") of host.
	LookupIP(ctx context.Context, network, host string) ([]net.IP, error)
	// LookupIPAddr is the lookup of the operating system, hosts file
	// included. It is asked when the records gave no address.
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// Options says where and how to connect. The first seven fields are the
// console's mail settings (the six it stores and RequireTLS); the rest is
// injected and has nodemailer's defaults when zero.
type Options struct {
	// Host is a name or an IP address. "" is "localhost".
	Host string
	// Port to connect to. 0 is 465 when Secure is set and 587 otherwise. A
	// port outside 0..65535 fails after the host was resolved, as it does in
	// Node.
	Port int
	// Secure starts TLS right after connecting (implicit TLS). Without it
	// the session starts in plain text and is upgraded with STARTTLS when
	// the server offers that.
	Secure bool
	// User and Pass are used only when both are non-empty, and only when the
	// server announces AUTH (or answers HELO only).
	User, Pass string
	// TLSVerify checks the server's certificate chain and name
	// (tls.rejectUnauthorized). Without it any certificate is accepted.
	TLSVerify bool
	// RequireTLS keeps the credentials off a connection without TLS: when
	// they are used and the connection is not secure, STARTTLS is sent
	// whether the server offers it or not, and a server that refuses EHLO
	// ends the call instead of getting HELO (nodemailer's requireTLS).
	// Without credentials it changes nothing.
	RequireTLS bool

	// Dial opens a TCP connection to "ip:port". Default: a net.Dialer.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// Resolver finds the addresses of Host. Default: net.DefaultResolver.
	Resolver Resolver
	// Families says which address families the machine can use. Default:
	// a family counts when an interface other than loopback has an address
	// of it, and both count when there is no interface at all.
	Families func() (ipv4, ipv6 bool)
	// Hostname is the machine's host name, the source of the EHLO name.
	// Default: os.Hostname.
	Hostname func() (string, error)
	// RootCAs are the certificates TLSVerify trusts. Default: the roots of
	// the operating system.
	RootCAs *x509.CertPool
	// ConnectionTimeout covers the DNS lookup, connecting and, with Secure,
	// the TLS handshake. It also limits the wait for the greeting and the
	// STARTTLS handshake. Default 2 minutes.
	ConnectionTimeout time.Duration
	// GreetingTimeout is the wait for the greeting and the time a STARTTLS
	// handshake may take. Default 30 seconds.
	GreetingTimeout time.Duration
	// SocketTimeout is how long the connection may be silent in both
	// directions. Default 10 minutes.
	SocketTimeout time.Duration
	// DNSTimeout limits the DNS lookup. Default 30 seconds.
	DNSTimeout time.Duration
	// Now is the clock certificates are checked against. Default: time.Now.
	Now func() time.Time
	// Shuffle orders the addresses of one family. Default: random.
	Shuffle func(n int, swap func(i, j int))
}

// Envelope is the SMTP envelope of a message.
type Envelope struct {
	// From is the sender address, "" for the empty sender.
	From string
	// To are the recipient addresses.
	To []string
	// Use8BitMime asks for BODY=8BITMIME when the server announces
	// 8BITMIME. A message built on the console's path never sets it.
	Use8BitMime bool
	// Size is announced as SIZE=n when the server announces SIZE, and a
	// message larger than the server's limit is not sent. 0 is unknown. A
	// message built on the console's path never sets it.
	Size int64
}

// Info is the result of a message the server accepted.
type Info struct {
	// Accepted and Rejected are the recipients the server took and refused.
	Accepted []string
	Rejected []string
	// RejectedErrors holds the reply for every refused recipient.
	RejectedErrors []*Error
	// Response is the server's reply to the message.
	Response string
	// EnvelopeFrom and EnvelopeTo are the addresses as they were sent.
	EnvelopeFrom string
	EnvelopeTo   []string
	// Ehlo lists the lines of the EHLO reply after the first.
	Ehlo []string
	// MessageSize counts the bytes sent after DATA, the end line included.
	MessageSize int64
	// EnvelopeTime is the time from MAIL FROM to the reply to DATA,
	// MessageTime the time from there to the final reply.
	EnvelopeTime time.Duration
	MessageTime  time.Duration
	// ServerMessageID is the Message-ID a server assigned in its final
	// reply (Outlook.com, Amazon SES), "" when it named none.
	ServerMessageID string
}

// Error is a failed call. Error() is nodemailer's err.message.
type Error struct {
	// Message is the text an admin sees.
	Message string
	// Code names the kind of failure: ECONNECTION, ETIMEDOUT, ESOCKET,
	// EDNS, ETLS, EPROTOCOL, EENVELOPE, EMESSAGE, EAUTH.
	Code string
	// Response is the server reply that caused the failure, "" when there
	// was none. ResponseCode is the number it starts with, 0 without one.
	Response     string
	ResponseCode int
	// Command is the step that failed: CONN, EHLO, HELO, STARTTLS,
	// "AUTH PLAIN", "MAIL FROM", "RCPT TO", DATA or API.
	Command string
	// Recipient is set on the entries of RejectedErrors.
	Recipient string
	// Rejected and RejectedErrors are set when every recipient was refused.
	Rejected       []string
	RejectedErrors []*Error

	cause error
}

func (e *Error) Error() string { return e.Message }

// Unwrap returns the network or TLS error behind a connection failure.
func (e *Error) Unwrap() error { return e.cause }

// Send delivers one message: message is the full message as it is to be
// transmitted, the DATA encoding (line ends, dot-stuffing, end line) is
// applied here. It returns an *Error when the server or the connection
// fails, and the context's error when ctx ends first.
func Send(ctx context.Context, opts Options, env Envelope, message []byte) (*Info, error) {
	c := newClient(ctx, opts)
	c.envelope = &env
	c.message = message
	c.run(c.sendMessage)
	return c.info, c.err
}

// Verify connects and logs in, then says QUIT. It reports success as soon as
// QUIT is written; the connection is closed in the background when the
// server answers, closes or stays silent for the socket timeout.
func Verify(ctx context.Context, opts Options) error {
	c := newClient(ctx, opts)
	c.run(c.quit)
	return c.err
}
