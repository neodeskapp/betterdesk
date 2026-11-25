package mail

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/mail/message"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail/smtpwire"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ssrfguard"
)

// SMTP is the Transport of a running console: it builds the message
// (package message) and delivers it over a connection of its own (package
// smtpwire), which together is what nodemailer did for the console. The
// zero value works.
type SMTP struct {
	// Now is the clock of the Date header. Nil means time.Now.
	Now func() time.Time
	// Rand gives the random bytes of the MIME boundary and the
	// Message-ID. Nil means crypto/rand.
	Rand io.Reader
	// Wire holds what a connection needs besides the saved settings: the
	// dialer, the resolver, the machine's host name, the root
	// certificates and the timeouts. Its zero fields are nodemailer's
	// defaults. The seven settings fields in it are not read.
	Wire smtpwire.Options
}

// errRefusedRelay is the failure of a connection to an address no mail
// relay has: link-local, where the cloud metadata services answer, or a
// metadata service in another range.
var errRefusedRelay = errors.New("SMTP host refused: link-local and cloud metadata addresses cannot be used")

// refusedRelay reports whether a connection to the address is refused: the
// ranges ssrfguard always blocks, except loopback and the unspecified
// address, which reach this machine as a relay on localhost does. Private
// addresses pass.
func refusedRelay(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.WithZone("").Unmap()
	if ip.IsLoopback() || ip.IsUnspecified() || ip.Is4() && ip.As4()[0] == 0 {
		return false
	}
	return ssrfguard.IsBlockedIP(ip.String(), ssrfguard.Options{AllowPrivate: true})
}

// guardedDial is dial for the addresses refusedRelay lets through, and
// errRefusedRelay for the others. The check runs on the address that is
// connected to, after the host's name was resolved.
func guardedDial(dial func(ctx context.Context, network, address string) (net.Conn, error)) func(ctx context.Context, network, address string) (net.Conn, error) {
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if refusedRelay(address) {
			return nil, errRefusedRelay
		}
		return dial(ctx, network, address)
	}
}

// options joins the injected parts of Wire with the saved settings. The
// connection goes through guardedDial.
func (t *SMTP) options(settings Settings) smtpwire.Options {
	opts := t.Wire
	opts.Dial = guardedDial(opts.Dial)
	opts.Host = settings.Host
	opts.Port = settings.Port
	opts.Secure = settings.Secure
	opts.User = settings.User
	opts.Pass = settings.Pass
	opts.TLSVerify = settings.TLSVerify
	opts.RequireTLS = settings.RequireTLS
	return opts
}

// Send builds the message and delivers it. The message is built before
// anything is connected, so a mail that cannot be built opens no
// connection.
func (t *SMTP) Send(ctx context.Context, settings Settings, mail Outgoing) error {
	built, err := message.Build(message.Mail{
		From:    mail.From,
		To:      mail.To,
		Subject: mail.Subject,
		Text:    mail.Text,
		HTML:    mail.HTML,
		Inline:  mail.Inline,
	}, message.Options{Now: t.Now, Rand: t.Rand})
	if err != nil {
		return err
	}
	envelope := smtpwire.Envelope{From: built.EnvelopeFrom, To: built.EnvelopeTo}
	_, err = smtpwire.Send(ctx, t.options(settings), envelope, built.Raw)
	return err
}

// Verify connects, signs in when the settings have credentials, and
// leaves again.
func (t *SMTP) Verify(ctx context.Context, settings Settings) error {
	return smtpwire.Verify(ctx, t.options(settings))
}
