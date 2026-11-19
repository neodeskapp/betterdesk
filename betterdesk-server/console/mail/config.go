package mail

import (
	"context"
	"errors"
	"math"
	"net/netip"

	"github.com/unitronix/betterdesk-server/console/jsbind"
)

// settingKey is the row of the settings table with the SMTP configuration.
const settingKey = "smtp_config"

// MaskedPass stands for a stored password in every answer of the settings
// routes. A save that sends it back keeps the stored password.
const MaskedPass = "********"

// defaultFrom is the sender when the configuration names none.
const defaultFrom = "betterdesk@localhost"

// Config is an SMTP configuration: the stored one, or the one made from
// the environment. Its members are JavaScript values, as the console had
// them.
//
// As JSON it is what the settings page gets: the password is never in it,
// only "********" when one is set.
type Config struct {
	doc value
}

func (c *Config) get(name string) value { return c.doc.member(name) }

// signature is JSON.stringify(config): two configurations with the same
// signature were the same configuration to the console.
func (c *Config) signature() string { return c.doc.json() }

// MarshalJSON writes the configuration as GET .../smtp answers it:
// configured, host, port, secure, user, pass ("********" or ""), from,
// alert_email and, when the configuration holds it as a flag, tlsVerify.
// A port the configuration does not have is left out. (No configuration at all is
// {"configured":false}; GetSMTPSettings answers that for a nil *Config,
// which encoding/json would write as null.)
func (c *Config) MarshalJSON() ([]byte, error) {
	if c == nil || !c.get("host").truthy() {
		return []byte(`{"configured":false}`), nil
	}
	or := func(name string) value {
		if v := c.get(name); v.truthy() {
			return v
		}
		return stringValue("")
	}
	pass := ""
	if c.get("pass").truthy() {
		pass = MaskedPass
	}
	out := objectValue(
		"configured", boolValue(true),
		"host", c.get("host"),
		"port", c.get("port"),
		"secure", boolValue(c.get("secure").truthy()),
		"user", or("user"),
		"pass", stringValue(pass),
		"from", or("from"),
		"alert_email", or("alert_email"),
	)
	if v := c.get("tlsVerify"); v.kind == kindBool {
		out.set("tlsVerify", v)
	}
	return out.appendJSON(nil), nil
}

// configFromText reads the stored row: a configuration when the text is a
// JSON object whose host is set, nil for everything else.
func configFromText(text string) *Config {
	if text == "" {
		return nil
	}
	doc, err := parseJSON(text)
	if err != nil || !doc.member("host").truthy() {
		return nil
	}
	return &Config{doc: doc}
}

// LoadSMTPConfig returns the SMTP configuration in use, or nil when there
// is none: the stored one when the row holds a JSON object whose host is
// set, else the one of the environment when SMTP_HOST is set. A row that
// cannot be read or is no such object counts as missing.
func (s *Service) LoadSMTPConfig(ctx context.Context) *Config {
	if text, ok, err := getSetting(ctx, s.opts.DB, settingKey); err == nil && ok {
		if cfg := configFromText(text); cfg != nil {
			return cfg
		}
	}
	host := s.getenv("SMTP_HOST")
	if host == "" {
		return nil
	}
	orDefault := func(name, fallback string) string {
		if v := s.getenv(name); v != "" {
			return v
		}
		return fallback
	}
	return &Config{doc: objectValue(
		"host", stringValue(host),
		"port", numberValue(parseInt10(orDefault("SMTP_PORT", "587"))),
		"secure", boolValue(s.getenv("SMTP_SECURE") == "true"),
		"user", stringValue(s.getenv("SMTP_USER")),
		"pass", stringValue(s.getenv("SMTP_PASS")),
		"from", stringValue(orDefault("SMTP_FROM", defaultFrom)),
		"alert_email", stringValue(s.getenv("SMTP_ALERT_EMAIL")),
		"tlsVerify", boolValue(s.opts.TLSVerify),
	)}
}

// GetAlertEmail returns the address alerts and warnings go to when nothing
// else names one: the configuration's alert_email without the white space
// around it, or "" when there is none. The error is the TypeError the
// console threw for a stored value it could not make text of.
func (s *Service) GetAlertEmail(ctx context.Context) (string, error) {
	cfg := s.LoadSMTPConfig(ctx)
	if cfg == nil || !cfg.get("alert_email").truthy() {
		return "", nil
	}
	text, err := cfg.get("alert_email").toString()
	if err != nil {
		return "", err
	}
	return wellFormed(jsbind.Trim(text)), nil
}

// transporter is a configuration made ready for sending: what
// nodemailer.createTransport was given, and what nodemailer did with it.
type transporter struct {
	config    *Config
	signature string
	settings  Settings
	// refusal is set when nodemailer could not have opened a connection
	// with these options: the message of the error it failed with (or, for
	// the values that ended the Node process, of the exception). The
	// Transport is not called then.
	refusal error
}

// tlsVerify is options.tls.rejectUnauthorized: the configuration's
// tlsVerify when it is true or false, else the console's default. A value
// of another type (null, a number, text) was never written by a save and
// does not turn the check off.
func (s *Service) tlsVerify(cfg *Config) bool {
	if v := cfg.get("tlsVerify"); v.kind == kindBool {
		return v.flag
	}
	return s.opts.TLSVerify
}

// newTransporter works out what nodemailer makes of the configuration.
//
// nodemailer takes options.host as it is and fails on anything but text
// when it looks the name up; it reads options.port with Number() and
// falls back on 465 or 587 for what is no number; it connects with TLS
// for any options.secure that is truthy (also the text "false"); and it
// signs in with user and pass as text.
func (s *Service) newTransporter(cfg *Config) *transporter {
	t := &transporter{config: cfg, signature: cfg.signature()}
	t.settings.Secure = cfg.get("secure").truthy()
	t.settings.TLSVerify = s.tlsVerify(cfg)
	t.settings.RequireTLS = s.opts.RequireTLS

	// The port is read when the connection object is made, before
	// anything else.
	port := 587.0
	if p := cfg.get("port"); p.truthy() {
		n, err := p.toNumber()
		if err != nil {
			t.refusal = err
			return t
		}
		port = n
	}
	if port == 0 || math.IsNaN(port) {
		port = 587
		if t.settings.Secure {
			port = 465
		}
	}

	host := cfg.get("host")
	if host.kind != kindString {
		t.refusal = hostRefusal(host)
		return t
	}
	t.settings.Host = wellFormed(host.str)

	if port != math.Trunc(port) || port < 0 || port > 65535 {
		t.refusal = errors.New("Port should be >= 0 and < 65536. Received type number (" + jsbind.NumberString(port) + ").")
		return t
	}
	t.settings.Port = int(port)

	if user, pass := cfg.get("user"), cfg.get("pass"); user.truthy() && pass.truthy() {
		userText, err := user.toString()
		if err != nil {
			t.refusal = err
			return t
		}
		passText, err := pass.toString()
		if err != nil {
			t.refusal = err
			return t
		}
		t.settings.User, t.settings.Pass = wellFormed(userText), wellFormed(passText)
	}
	return t
}

// hostRefusal is the error Node raised for a host that is no text. A list
// that reads as an IP address gets as far as the socket, which refuses
// it; everything else fails at the name lookup.
func hostRefusal(host value) error {
	text, err := host.toString()
	if err != nil {
		return err
	}
	if host.kind == kindArray {
		if _, err := netip.ParseAddr(text); err == nil {
			return errors.New(`The "options.host" property must be of type string. Received an instance of Array`)
		}
	}
	received := ""
	switch host.kind {
	case kindNumber:
		received = "type number (" + text + ")"
	case kindBool:
		received = "type boolean (" + text + ")"
	case kindArray:
		received = "an instance of Array"
	default:
		received = "an instance of Object"
	}
	return errors.New(`The "name" argument must be of type string. Received ` + received)
}

// transporterFor is getTransporter: the transporter of the configuration
// in use, nil when there is no configuration or mail is off.
//
// The service remembers the configuration it last set a transporter up
// for and logs "SMTP transporter configured" when another one comes
// along. The error is the TypeError the console threw while writing that
// line for a host or port it could not make text of; the configuration
// counts as set up all the same, so the next call goes through.
func (s *Service) transporterFor(ctx context.Context) (*transporter, error) {
	if s.opts.Transport == nil {
		s.log.Warn("nodemailer not installed — email disabled")
		return nil, nil
	}
	cfg := s.LoadSMTPConfig(ctx)
	if cfg == nil {
		return nil, nil
	}
	signature := cfg.signature()

	s.mu.Lock()
	if s.current != nil && s.current.signature == signature {
		t := s.current
		s.mu.Unlock()
		return t, nil
	}
	t := s.newTransporter(cfg)
	s.current = t
	s.mu.Unlock()

	host, err := cfg.get("host").toString()
	if err != nil {
		return nil, err
	}
	port, err := cfg.get("port").toString()
	if err != nil {
		return nil, err
	}
	s.log.Info("SMTP transporter configured: " + wellFormed(host) + ":" + wellFormed(port))
	return t, nil
}
