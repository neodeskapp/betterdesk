// Package mail is the console's mail service: where the SMTP settings come
// from, the alert mail and the help request mail, the three settings
// handlers behind the SMTP form, and the log of notifications already
// sent.
//
// The package builds no MIME message and speaks no SMTP. It hands a
// Transport what the console handed to nodemailer: the settings of the
// connection and the parts of the message.
//
// Where the settings come from: the row "smtp_config" of the settings
// table when it holds a JSON object with a host, else the SMTP_*
// environment variables when SMTP_HOST is set, read at every use. The
// stored JSON was written by a request with few checks, so each member can
// be of any JSON type; the package treats them as the console's JavaScript
// did.
package mail

import (
	"context"
	"strings"
	"sync"

	"github.com/unitronix/betterdesk-server/console/auditlog"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/logging"
)

// Settings is what the console passes to nodemailer.createTransport.
type Settings struct {
	// Host is options.host: a host name or an address, as stored.
	Host string
	// Port is options.port: the stored port, 587 when none is set. Always
	// from 1 to 65535.
	Port int
	// Secure is options.secure: TLS from the first byte.
	Secure bool
	// User and Pass are options.auth. They are set only when the
	// configuration has both.
	User, Pass string
	// TLSVerify is options.tls.rejectUnauthorized: whether the server's
	// certificate has to be valid.
	TLSVerify bool
	// RequireTLS is options.requireTLS for a connection with credentials:
	// the login goes out only after TLS is on.
	RequireTLS bool
}

// Outgoing is what the console passes to transporter.sendMail.
type Outgoing struct {
	From string
	// To has one element when `to` was a string, and one per element of an
	// array. It is nil when `to` was not set.
	To []string
	// ToList says `to` was an array.
	ToList  bool
	Subject string
	// Text and HTML are nil where the console passed undefined.
	Text *string
	HTML *string
}

// Transport does what nodemailer did: Send delivers one message over a
// connection of its own, Verify connects and signs in without sending.
// An error's text is nodemailer's err.message.
type Transport interface {
	Send(ctx context.Context, settings Settings, mail Outgoing) error
	Verify(ctx context.Context, settings Settings) error
}

// Logger takes the lines the service logs. *kit.Logger is one.
type Logger interface {
	Info(args ...any)
	Warn(args ...any)
	Error(args ...any)
}

// Options are the settings of a Service. Each field names what the Node
// console read for it.
type Options struct {
	// DB is the console's database: the settings table and the
	// notification log.
	DB *store.DB
	// Transport sends the mails. Nil means mail is off, which behaves as
	// the console did without nodemailer installed: nothing is sent and a
	// connection test answers "No SMTP configuration".
	Transport Transport
	// Getenv reads the environment at every use: SMTP_HOST, SMTP_PORT,
	// SMTP_SECURE, SMTP_USER, SMTP_PASS, SMTP_FROM, SMTP_ALERT_EMAIL,
	// PANEL_PUBLIC_URL and PUBLIC_URL. Nil means none is set.
	Getenv func(string) string
	// TLSVerify is whether the mail server's certificate is checked when
	// the configuration does not say. The console sets it from
	// SMTP_TLS_VERIFY, which is on unless the variable is "false".
	TLSVerify bool
	// RequireTLS keeps a login off a connection without TLS, for every
	// configuration. The console sets it from SMTP_REQUIRE_TLS, which is on
	// unless the variable is "false".
	RequireTLS bool
	// Host is config.host: HOST, default "127.0.0.1".
	Host string
	// Port is config.port: the port the console listens on (PORT, default
	// 5000).
	Port int
	// HTTPRedirect is config.httpRedirect: HTTP_REDIRECT_HTTPS (ParseFlag
	// with the default "true").
	HTTPRedirect bool
	// Logger takes what the service logs. Nil logs through the logging
	// package under "Email".
	Logger Logger
}

// ParseFlag reads a true-or-false setting as the console's config did:
// (value || fallback).toLowerCase() === 'true'. Only the word "true", in
// any letter case and with nothing around it, is true.
func ParseFlag(value, fallback string) bool {
	if value == "" {
		value = fallback
	}
	return strings.EqualFold(value, "true")
}

// Service is the mail service of one console. It is safe for concurrent
// use.
type Service struct {
	opts   Options
	log    Logger
	getenv func(string) string

	// mu guards current: the configuration the service last set a
	// transporter up for.
	mu      sync.Mutex
	current *transporter
}

// New returns a mail service. It starts nothing.
func New(opts Options) *Service {
	s := &Service{opts: opts, log: opts.Logger, getenv: opts.Getenv}
	if s.log == nil {
		s.log = defaultLogger{}
	}
	if s.getenv == nil {
		s.getenv = func(string) string { return "" }
	}
	return s
}

// ResetTransporter forgets the configuration the service last worked
// with, so the next mail or connection test announces its settings in the
// log again ("SMTP transporter configured: host:port").
//
// In the Node console this dropped the cached nodemailer transporter. That
// object held no connection (without a pool nodemailer connects per
// message), only the options it was made with, and a new one was made
// whenever the configuration's JSON changed. The one thing the cache
// showed was that log line: once per configuration, and again after a
// reset. The service keeps exactly that.
func (s *Service) ResetTransporter() {
	s.mu.Lock()
	s.current = nil
	s.mu.Unlock()
}

// defaultLogger writes to the logging package under "Email".
type defaultLogger struct{}

func (defaultLogger) Info(args ...any)  { logLine("", args) }
func (defaultLogger) Warn(args ...any)  { logLine("WARN: ", args) }
func (defaultLogger) Error(args ...any) { logLine("ERROR: ", args) }

func logLine(level string, args []any) {
	parts := make([]string, len(args))
	for i, arg := range args {
		switch v := arg.(type) {
		case string:
			parts[i] = v
		case error:
			parts[i] = v.Error()
		}
	}
	logging.Logf("Email", "%s%s", level, auditlog.SanitizeLogValue(strings.Join(parts, " ")))
}
