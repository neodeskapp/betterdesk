package mail

import (
	"context"
	"errors"
	"math"
	"strconv"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/internal/envvar"
)

// settingKey is the row of the settings table with the SMTP configuration.
const settingKey = "smtp_config"

// MaskedPass stands for a stored password in every answer of the settings
// routes. A save that sends it back keeps the stored password.
const MaskedPass = "********"

// defaultFrom is the sender when the configuration names none.
const defaultFrom = "neodesk@localhost"

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
	cfg, _ := s.loadSMTP(ctx)
	return cfg
}

// Where the SMTP configuration in use comes from (SMTPSource).
const (
	SMTPFromStore = "stored"      // saved in the console
	SMTPFromEnv   = "environment" // SMTP_HOST and the other SMTP_ variables
	SMTPNone      = ""            // there is none
)

// SMTPEnvVars are the variables an SMTP configuration from the
// environment is read from, by the configuration's field.
var SMTPEnvVars = map[string]string{
	"host": "SMTP_HOST", "port": "SMTP_PORT", "secure": "SMTP_SECURE", "user": "SMTP_USER",
	"pass": "SMTP_PASS", "from": "SMTP_FROM", "alert_email": "SMTP_ALERT_EMAIL",
}

// The defaults of an SMTP configuration from the environment, by field.
const (
	DefaultSMTPPort   = 587
	DefaultSMTPSecure = false
	DefaultSMTPFrom   = defaultFrom
)

// SMTPSource says where the SMTP configuration in use comes from, as
// LoadSMTPConfig picks it: the stored one wins over the environment.
func (s *Service) SMTPSource(ctx context.Context) string {
	_, source := s.loadSMTP(ctx)
	return source
}

// loadSMTP is LoadSMTPConfig with where the configuration came from.
func (s *Service) loadSMTP(ctx context.Context) (*Config, string) {
	if text, ok, err := getSetting(ctx, s.opts.DB, settingKey); err == nil && ok {
		if cfg := configFromText(text); cfg != nil {
			return cfg, SMTPFromStore
		}
	}
	host := s.getenv(SMTPEnvVars["host"])
	if host == "" {
		return nil, SMTPNone
	}
	orDefault := func(name, fallback string) string {
		if v := s.getenv(name); v != "" {
			return v
		}
		return fallback
	}
	return &Config{doc: objectValue(
		"host", stringValue(host),
		"port", numberValue(parseInt10(orDefault("SMTP_PORT", strconv.Itoa(DefaultSMTPPort)))),
		"secure", boolValue(envvar.SwitchOr(s.getenv("SMTP_SECURE"), DefaultSMTPSecure)),
		"user", stringValue(s.getenv("SMTP_USER")),
		"pass", stringValue(s.getenv("SMTP_PASS")),
		"from", stringValue(orDefault("SMTP_FROM", defaultFrom)),
		"alert_email", stringValue(s.getenv("SMTP_ALERT_EMAIL")),
		"tlsVerify", boolValue(s.opts.TLSVerify),
	)}, SMTPFromEnv
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
	// refusal is set when no connection is opened with these options:
	// errInvalidConfig, or the error nodemailer failed with for
	// credentials it could not make text of. The Transport is not called
	// then.
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

// errInvalidConfig is what sending and the connection test fail with for
// a stored configuration whose host is no text or whose port is no whole
// number from 1 to 65535: a row edited by hand or written by another
// version, which no save stores.
var errInvalidConfig = errors.New("Invalid SMTP configuration")

// newTransporter works out what nodemailer makes of the configuration.
//
// The host has to be text and the port a number, or text of one, that is
// whole and from 1 to 65535; a port that is not set (missing, null, 0,
// false, the empty text, NaN) is 587. Anything else makes the
// configuration invalid. nodemailer connects with TLS for any
// options.secure that is truthy (also the text "false"), and signs in
// with user and pass as text.
func (s *Service) newTransporter(cfg *Config) *transporter {
	t := &transporter{config: cfg, signature: cfg.signature()}
	t.settings.Secure = cfg.get("secure").truthy()
	t.settings.TLSVerify = s.tlsVerify(cfg)
	t.settings.RequireTLS = s.opts.RequireTLS

	port := 587.0
	if p := cfg.get("port"); p.truthy() {
		n := math.NaN()
		if p.kind == kindNumber || p.kind == kindString {
			n, _ = p.toNumber()
		}
		if n != math.Trunc(n) || n < 1 || n > 65535 {
			t.refusal = errInvalidConfig
			return t
		}
		port = n
	}
	host := cfg.get("host")
	if host.kind != kindString {
		t.refusal = errInvalidConfig
		return t
	}
	t.settings.Host = wellFormed(host.str)
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

// transporterFor is getTransporter: the transporter of the configuration
// in use, nil when there is no configuration or mail is off.
//
// The service remembers the configuration it last set a transporter up
// for and logs "SMTP transporter configured" with its host and port when
// another one comes along; an invalid configuration is not logged.
func (s *Service) transporterFor(ctx context.Context) *transporter {
	if s.opts.Transport == nil {
		s.log.Warn("nodemailer not installed — email disabled")
		return nil
	}
	cfg := s.LoadSMTPConfig(ctx)
	if cfg == nil {
		return nil
	}
	signature := cfg.signature()

	s.mu.Lock()
	if s.current != nil && s.current.signature == signature {
		t := s.current
		s.mu.Unlock()
		return t
	}
	t := s.newTransporter(cfg)
	s.current = t
	s.mu.Unlock()

	if t.refusal != errInvalidConfig {
		// A valid port is a number, text or not set, so it makes text
		port, _ := cfg.get("port").toString()
		s.log.Info("SMTP transporter configured: " + t.settings.Host + ":" + wellFormed(port))
	}
	return t
}
