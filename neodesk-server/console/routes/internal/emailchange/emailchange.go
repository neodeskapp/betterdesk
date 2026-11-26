// Package emailchange changes the email address of a console account, for
// the routes that let an admin (Users, Edit) or the person (Your account,
// the operator profile) do it.
//
// A new address takes effect only once its owner opens the link mailed
// to it (lifecycle.ConfirmEmailLink, one-time, 24 hours). Until then the
// old address stays, the account shows the pending one, and the old
// address gets a notice unless the server setting
// security_notice_email_changed is "false". When the console can't send
// mail, nothing could be confirmed: the address changes at once, and the
// audit log says it was not confirmed. Taking the address away, or only
// changing its case, needs no confirmation either. An account from a
// directory or single sign-on gets its address from there and is refused.
//
// Every change that takes effect ends the account's other sessions and
// its client access tokens, and its password links (a reset link went to
// the old address).
package emailchange

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/lifecycle"
	"github.com/neodeskapp/neodesk/neodesk-server/console/passwordlinks"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/accountmail"
)

// NoticeKey is the server_config key of the switch for the notice to the
// old address; anything but "false" leaves it on.
const NoticeKey = "security_notice_email_changed"

// Target is the account whose address changes.
type Target struct {
	ID       int64
	Username string
	// Email is the address it has now.
	Email string
	// Provider is local, ldap or oidc.
	Provider string
}

// Refusal is a change the route answers with Status and the text of Key.
type Refusal struct {
	Status int
	Key    string
	Vars   map[string]any
}

func (r *Refusal) Error() string { return r.Key }

// Outcome is what a request did.
type Outcome struct {
	// Changed: the address changed at once.
	Changed bool
	// Pending: the new address waits for its confirmation.
	Pending *lifecycle.Change
}

// Changer changes addresses for one route group.
type Changer struct {
	D     *kit.Deps
	Life  *lifecycle.Store
	Links *passwordlinks.Store
	// Invited, when set, reports whether the account was invited and
	// never signed in. Such an account's new address needs no separate
	// confirmation: SendInvite mails a new invite there, and the invite
	// proves the address.
	Invited    func(ctx context.Context, t Target) (bool, error)
	SendInvite func(r *http.Request, t Target, email string) error
}

// External reports whether the account signs in with a directory or
// single sign-on.
func (t Target) External() bool {
	p := strings.ToLower(strings.TrimSpace(t.Provider))
	return p != "" && p != "local"
}

// providerName is how the texts name the account's provider.
func (t Target) providerName() string {
	if strings.EqualFold(strings.TrimSpace(t.Provider), "ldap") {
		return "LDAP"
	}
	return "single sign-on"
}

// Refuse is the refusal of a change of an account from a directory or
// single sign-on, or nil.
func Refuse(t Target, email string) error {
	if t.External() && email != t.Email {
		return &Refusal{Status: http.StatusBadRequest, Key: "users.email_from_provider", Vars: map[string]any{"provider": t.providerName()}}
	}
	return nil
}

// Request asks for the account's address to become email (normalized,
// "" to take it away). self says the account changes its own.
func (c *Changer) Request(r *http.Request, t Target, email string, self bool) (Outcome, error) {
	ctx := r.Context()
	if email == t.Email {
		return Outcome{}, nil
	}
	if err := Refuse(t, email); err != nil {
		return Outcome{}, err
	}
	if email == "" || strings.EqualFold(email, t.Email) || !accountmail.Ready(ctx, c.D) {
		how := "without confirmation, the console can't send mail"
		switch {
		case email == "":
			how = "removed"
		case strings.EqualFold(email, t.Email):
			how = "only the case changed"
		}
		if err := c.apply(r, t, email, self, how); err != nil {
			return Outcome{}, err
		}
		return Outcome{Changed: true}, nil
	}
	if c.Invited != nil && c.SendInvite != nil && !self {
		invited, err := c.Invited(ctx, t)
		if err != nil {
			return Outcome{}, err
		}
		if invited {
			// The new address gets a new invite; the old link stops working
			if err := c.apply(r, t, email, self, "the new invite confirms it"); err != nil {
				return Outcome{}, err
			}
			if err := c.SendInvite(r, t, email); err != nil {
				return Outcome{}, err
			}
			return Outcome{Changed: true}, nil
		}
	}
	change, err := c.issue(r, t, email, self)
	if err != nil {
		return Outcome{}, err
	}
	if t.Email != "" && NoticeOn(ctx, c.D) {
		c.notice(r, t, email)
	}
	details := "Asked to change the email address of user " + t.Username + " to " + email + "; a confirmation link went to the new address"
	if err := c.D.LogAccountAction(r, t.ID, "email_change_requested", details); err != nil {
		return Outcome{}, err
	}
	return Outcome{Pending: change}, nil
}

// Resend mails a new confirmation link for the account's pending address;
// the link before stops working. No second notice goes to the old address.
func (c *Changer) Resend(r *http.Request, t Target, self bool) (*lifecycle.Change, error) {
	ctx := r.Context()
	pending, err := c.Life.PendingEmail(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	if pending == nil {
		return nil, &Refusal{Status: http.StatusConflict, Key: "users.email_change_none"}
	}
	if !accountmail.Ready(ctx, c.D) {
		return nil, &Refusal{Status: http.StatusConflict, Key: "users.invite_needs_email"}
	}
	change, err := c.issue(r, t, pending.Email, self)
	if err != nil {
		return nil, err
	}
	if err := c.D.LogAccountAction(r, t.ID, "email_change_requested", "Sent a new confirmation link for the email address of user "+t.Username+" to "+pending.Email); err != nil {
		return nil, err
	}
	return change, nil
}

// Cancel ends the account's pending change; the address stays as it is.
func (c *Changer) Cancel(r *http.Request, t Target) error {
	had, err := c.Life.CancelEmailChange(r.Context(), t.ID)
	if err != nil {
		return err
	}
	if !had {
		return &Refusal{Status: http.StatusConflict, Key: "users.email_change_none"}
	}
	return c.D.LogAccountAction(r, t.ID, "email_change_cancelled", "Cancelled the email address change of user "+t.Username)
}

// apply sets the address at once.
func (c *Changer) apply(r *http.Request, t Target, email string, self bool, how string) error {
	ctx := r.Context()
	if _, err := c.D.Store.Exec(ctx, `UPDATE users SET email = ? WHERE id = ?`, email, t.ID); err != nil {
		return err
	}
	if _, err := c.Life.CancelEmailChange(ctx, t.ID); err != nil {
		return err
	}
	if err := EndOthers(r, c.D, c.Links, t.ID, self); err != nil {
		return err
	}
	if t.Email != "" && NoticeOn(ctx, c.D) && accountmail.Ready(ctx, c.D) {
		c.changedNotice(r, t, email)
	}
	details := "Changed the email address of user " + t.Username + " from " + orNone(t.Email) + " to " + orNone(email) + " (" + how + ")"
	return c.D.LogAccountAction(r, t.ID, "email_changed", details)
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// EndOthers ends the account's sessions but the caller's own (when it is
// the account's), its client access tokens and its password links, after
// its address changed.
func EndOthers(r *http.Request, d *kit.Deps, links *passwordlinks.Store, id int64, self bool) error {
	ctx := r.Context()
	keep := ""
	if self && kit.UserID(r) == id {
		keep = kit.Session(r).ID()
	}
	if _, err := d.Sessions.EndAccount(ctx, id, keep); err != nil {
		return err
	}
	if err := d.Authn.RevokeClientTokens(ctx, id, "", ""); err != nil {
		return err
	}
	return links.Revoke(ctx, id)
}

// issue makes the link and mails it to the new address. A mail that
// doesn't go out ends the change again.
func (c *Changer) issue(r *http.Request, t Target, email string, self bool) (*lifecycle.Change, error) {
	ctx := r.Context()
	by := ""
	if u := kit.User(r); u != nil {
		by = u.Username
	}
	token, expires, err := c.Life.IssueEmailChange(ctx, t.ID, email, by)
	if err != nil {
		return nil, err
	}
	lang := accountmail.LangOf(ctx, c.D, t.ID)
	m := ConfirmMail(accountmail.In(c.D, lang), Facts{
		Base: base(r, c.D), Username: t.Username, Email: email, Sender: senderName(r, c.D), Self: self,
		Expires: expires.UTC().Format("2006-01-02 15:04") + " UTC",
	}, accountmail.Branding(c.D, base(r, c.D)), token)
	delivered, err := accountmail.Send(ctx, c.D, email, m)
	if err != nil || !delivered {
		if _, cancelErr := c.Life.CancelEmailChange(ctx, t.ID); cancelErr != nil {
			return nil, cancelErr
		}
		if err != nil {
			c.D.Log.Error("Email change mail error:", err.Error())
		}
		return nil, &Refusal{Status: http.StatusBadGateway, Key: "users.email_confirm_mail_failed"}
	}
	return &lifecycle.Change{UserID: t.ID, Username: t.Username, Email: email, ExpiresAt: expires.UTC()}, nil
}

// notice tells the old address that a change was asked for.
func (c *Changer) notice(r *http.Request, t Target, email string) {
	c.sendNotice(r, t, email, "email_change.notice_lead")
}

// changedNotice tells the old address that the address changed.
func (c *Changer) changedNotice(r *http.Request, t Target, email string) {
	if email == "" {
		c.sendNotice(r, t, email, "email_change.notice_removed_lead")
		return
	}
	c.sendNotice(r, t, email, "email_change.notice_changed_lead")
}

func (c *Changer) sendNotice(r *http.Request, t Target, email, leadKey string) {
	ctx := r.Context()
	lang := accountmail.LangOf(ctx, c.D, t.ID)
	m := NoticeMail(accountmail.In(c.D, lang), Facts{Base: base(r, c.D), Username: t.Username, Email: email}, accountmail.Branding(c.D, base(r, c.D)), leadKey)
	if _, err := accountmail.Send(ctx, c.D, t.Email, m); err != nil {
		c.D.Log.Error("Email change notice error:", err.Error())
	}
}

// NoticeOn reports whether the old address gets a notice: the switch is
// not "false". A database without the table has it on.
func NoticeOn(ctx context.Context, d *kit.Deps) bool {
	var value string
	err := d.Store.QueryRow(ctx, `SELECT value FROM server_config WHERE key = ?`, NoticeKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return true
	}
	return strings.TrimSpace(strings.ToLower(value)) != "false"
}

// base is the console's address for the links of a request.
func base(r *http.Request, d *kit.Deps) string {
	scheme := "http"
	if kit.Secure(r) {
		scheme = "https"
	}
	return accountmail.Base(d, scheme+"://"+r.Host)
}

// senderName is the signed-in account's first and last name, else its
// username.
func senderName(r *http.Request, d *kit.Deps) string {
	fallback := ""
	if u := kit.User(r); u != nil {
		fallback = u.Username
	}
	var first, last sql.NullString
	if err := d.Store.QueryRow(r.Context(), `SELECT first_name, last_name FROM users WHERE id = ?`, kit.UserID(r)).Scan(&first, &last); err != nil {
		return fallback
	}
	if name := strings.TrimSpace(strings.TrimSpace(first.String) + " " + strings.TrimSpace(last.String)); name != "" {
		return name
	}
	return fallback
}

// Facts are what the mails say.
type Facts struct {
	Base, Username, Email, Sender, Expires string
	Self                                   bool
}

// ConfirmMail is the mail to the new address with the link that confirms
// it.
func ConfirmMail(t accountmail.Translate, f Facts, b brandmail.Branding, token string) accountmail.Built {
	vars := map[string]any{"server": b.Server, "sender": f.Sender, "username": f.Username, "email": f.Email, "expires": f.Expires, "host": accountmail.Host(f.Base)}
	lead := t("email_change.confirm_lead", vars)
	if f.Self {
		lead = t("email_change.confirm_lead_self", vars)
	}
	return accountmail.Build(b, accountmail.Page{
		Subject: t("email_change.confirm_subject", vars), Heading: t("email_change.confirm_heading", vars), Lead: lead,
		Button: t("email_change.confirm_button", vars), Link: lifecycle.ConfirmEmailLink(f.Base, token),
		LinkHint: t("password_link.mail_link_hint", vars),
		Small:    []string{t("password_link.mail_username", vars), t("email_change.confirm_expires", vars), t("email_change.confirm_ignore", vars)},
		Footer:   t("password_link.mail_footer", vars),
	})
}

// NoticeMail is the notice to the old address.
func NoticeMail(t accountmail.Translate, f Facts, b brandmail.Branding, leadKey string) accountmail.Built {
	vars := map[string]any{"server": b.Server, "username": f.Username, "email": f.Email, "host": accountmail.Host(f.Base)}
	return accountmail.Build(b, accountmail.Page{
		Subject: t("email_change.notice_subject", vars), Heading: t("email_change.notice_heading", vars), Lead: t(leadKey, vars),
		Button: t("email_change.notice_button", vars), Link: f.Base + "/login",
		Small:  []string{t("email_change.notice_not_you", vars)},
		Footer: t("password_link.mail_footer", vars),
	})
}
