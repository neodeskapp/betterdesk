package mail

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
)

// identityKey is the settings row of the mail identity.
const identityKey = "mail_identity"

// Identity is who the console's mails say they are from, set next to the
// SMTP settings: the sender's display name, and who runs the server and
// how to reach them, for the footer and for replies. Every field may be
// empty.
type Identity struct {
	// FromName is the From header's display name. Empty is "NeoDesk" for
	// a server that keeps that name, else "<name> via NeoDesk".
	FromName string `json:"from_name"`
	// Organisation runs the server ("Acme IT").
	Organisation string `json:"organisation"`
	// SupportEmail is where people get help; replies go there too unless
	// a mail names the person who sent it.
	SupportEmail string `json:"support_email"`
	// SupportURL is a page where people get help.
	SupportURL string `json:"support_url"`
}

// The longest value a field may have, in characters.
const identityFieldMax = 200

// Refusals of SaveIdentity, by field.
var (
	ErrIdentityTooLong = errors.New("too_long")
	ErrIdentityEmail   = errors.New("invalid_email")
	ErrIdentityURL     = errors.New("invalid_url")
)

// IdentityError is a refused field.
type IdentityError struct {
	Field string
	Err   error
}

func (e *IdentityError) Error() string { return e.Field + ": " + e.Err.Error() }
func (e *IdentityError) Unwrap() error { return e.Err }

// Check trims the fields and refuses what can't go into a mail: a value
// over 200 characters or with a line break, a support address that is no
// single address, a support page that is no http or https address.
func (i *Identity) Check() error {
	fields := []struct {
		name string
		v    *string
	}{{"from_name", &i.FromName}, {"organisation", &i.Organisation}, {"support_email", &i.SupportEmail}, {"support_url", &i.SupportURL}}
	for _, f := range fields {
		*f.v = strings.TrimSpace(*f.v)
		if utf8.RuneCountInString(*f.v) > identityFieldMax || strings.ContainsAny(*f.v, "\r\n\x00") {
			return &IdentityError{f.name, ErrIdentityTooLong}
		}
	}
	if i.SupportEmail != "" && !plainAddress(i.SupportEmail) {
		return &IdentityError{"support_email", ErrIdentityEmail}
	}
	if i.SupportURL != "" {
		u, err := url.Parse(i.SupportURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || strings.ContainsAny(i.SupportURL, " \"'<>") {
			return &IdentityError{"support_url", ErrIdentityURL}
		}
	}
	return nil
}

// LoadIdentity reads the mail identity; a missing or unreadable row is
// the empty one.
func (s *Service) LoadIdentity(ctx context.Context) Identity {
	var out Identity
	if s.opts.DB == nil {
		return out
	}
	raw, ok, err := getSetting(ctx, s.opts.DB, identityKey)
	if err != nil || !ok || raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	if out.Check() != nil {
		return Identity{}
	}
	return out
}

// SaveIdentity checks and stores the mail identity.
func (s *Service) SaveIdentity(ctx context.Context, i Identity) (Identity, error) {
	if err := i.Check(); err != nil {
		return i, err
	}
	text, err := json.Marshal(i)
	if err != nil {
		return i, err
	}
	if s.opts.DB == nil {
		return i, errors.New("no database")
	}
	return i, setSetting(ctx, s.opts.DB, identityKey, string(text))
}

// clientBranding reads the client branding's company name, e-mail and
// website (Client Branding, in server_config), which stand in for the
// identity's empty fields: admins fill them in for the desktop clients.
func (s *Service) clientBranding(ctx context.Context) (company, email, website string) {
	if s.opts.DB == nil {
		return "", "", ""
	}
	read := func(key string) string {
		var v sql.NullString
		if err := s.opts.DB.QueryRow(ctx, `SELECT value FROM server_config WHERE key = ?`, key).Scan(&v); err != nil {
			return ""
		}
		return strings.TrimSpace(v.String)
	}
	return read("branding_company_name"), read("branding_email"), read("branding_website")
}

// Contact is who runs the server for a mail's footer: the identity's
// fields, each empty one taken from the client branding when that has a
// usable value.
func (s *Service) Contact(ctx context.Context) brandmail.Contact {
	id := s.LoadIdentity(ctx)
	c := brandmail.Contact{Organisation: id.Organisation, Email: id.SupportEmail, URL: id.SupportURL}
	company, email, website := s.clientBranding(ctx)
	fallback := Identity{Organisation: company, SupportEmail: email, SupportURL: website}
	if c.Organisation == "" && utf8.RuneCountInString(company) <= identityFieldMax && !strings.ContainsAny(company, "\r\n") {
		c.Organisation = company
	}
	if c.Email == "" && fallback.SupportEmail != "" && plainAddress(fallback.SupportEmail) {
		c.Email = fallback.SupportEmail
	}
	if c.URL == "" && fallback.SupportURL != "" {
		probe := Identity{SupportURL: fallback.SupportURL}
		if probe.Check() == nil {
			c.URL = probe.SupportURL
		}
	}
	return c
}

// FromName is the display name of the From header: the identity's when
// set, else "NeoDesk" for a server that keeps that name and "<name> via
// NeoDesk" for one with its own.
func FromName(id Identity, b brandmail.Branding) string {
	if id.FromName != "" {
		return id.FromName
	}
	if b.OwnName() {
		return b.Name() + " via NeoDesk"
	}
	return brandmail.DefaultServer
}

// withName is a sender with a display name: the configured sender as it
// is when it already has one ("Acme <it@acme.example>"), else the name
// before the address in quotes.
func withName(sender, name string) string {
	if name == "" || strings.Contains(sender, "<") || strings.Contains(sender, ",") {
		return sender
	}
	quoted := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(name)
	return `"` + quoted + `" <` + strings.TrimSpace(sender) + `>`
}
