// Package emailchange changes the email address of a console account, for
// the routes that let an admin (Users, Edit) or the person (Your account,
// the operator profile) do it.
//
// A new address takes effect only once its owner opens the link mailed
// to it (lifecycle.ConfirmEmailLink, one-time, 24 hours). Until then the
// old address stays, the account shows the pending one, and the old
// address gets a notice unless the server setting
// security_notice_email_changed is "false". When the console can't send
// mail, nothing could be confirmed: an admin's change of another account
// takes effect at once, and the audit log says it was not confirmed; a
// change of one's own address is refused (A272) with the code NeedsMail,
// so nobody moves their account to an address they haven't shown is
// theirs. Taking the address away, or only changing its case, needs no
// confirmation either. An account from a
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
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/lifecycle"
	"github.com/neodeskapp/neodesk/neodesk-server/console/passwordlinks"
	"github.com/neodeskapp/neodesk/neodesk-server/console/people"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/accountmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/securitymail"
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

// Refusal is a change the route answers with Status and the text of Key,
// and with Code, when it has one, for the console to tell it apart.
type Refusal struct {
	Status int
	Code   string
	Key    string
	Vars   map[string]any
}

// NeedsMail is the code of a refused change of one's own address on a
// console without email settings, which can't send the confirmation link.
const NeedsMail = "email_change_needs_mail"

// NotSent is the code of a change whose confirmation mail the mail server
// didn't take (502): the address stays as it was.
const NotSent = "email_confirmation_not_sent"

// RestSaved is the text of a refused address when the request's other
// fields were saved (A274): they are, only the address stays, and why.
func RestSaved(r *http.Request, why string) string {
	return kit.T(r, "users.email_not_changed_rest_saved", kit.Vars("reason", why))
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
	ready := accountmail.Ready(ctx, c.D)
	confirmable := email != "" && !strings.EqualFold(email, t.Email)
	if self && confirmable && !ready {
		return Outcome{}, &Refusal{Status: http.StatusConflict, Code: NeedsMail, Key: "users.email_change_needs_mail"}
	}
	if !confirmable || !ready {
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
	tr, lang, zone := securitymail.ReaderOf(ctx, c.D, t.ID)
	m := ConfirmMail(tr, Facts{
		Base: base(r, c.D), Username: t.Username, Email: email, Sender: senderName(r, c.D), Self: self,
		Expires: expires, Lang: lang, Zone: zone,
	}, token)
	delivered, err := accountmail.Send(ctx, c.D, email, m)
	if err != nil || !delivered {
		if _, cancelErr := c.Life.CancelEmailChange(ctx, t.ID); cancelErr != nil {
			return nil, cancelErr
		}
		if err != nil {
			c.D.Log.Error("Email change mail error:", err.Error())
		}
		return nil, &Refusal{Status: http.StatusBadGateway, Code: NotSent, Key: "users.email_confirm_mail_failed"}
	}
	return &lifecycle.Change{UserID: t.ID, Username: t.Username, Email: email, ExpiresAt: expires.UTC()}, nil
}

// notice tells the old address that a change was asked for.
func (c *Changer) notice(r *http.Request, t Target, email string) {
	c.sendNotice(r, t, email, "")
}

// changedNotice tells the old address that the address changed.
func (c *Changer) changedNotice(r *http.Request, t Target, email string) {
	if email == "" {
		c.sendNotice(r, t, email, "removed")
		return
	}
	c.sendNotice(r, t, email, "changed")
}

func (c *Changer) sendNotice(r *http.Request, t Target, email, variant string) {
	ctx := r.Context()
	tr, lang, zone := securitymail.ReaderOf(ctx, c.D, t.ID)
	m := NoticeMail(tr, Facts{Base: base(r, c.D), Username: t.Username, Email: email, Lang: lang, Zone: zone}, variant)
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
	return people.NameOf(r.Context(), d.Store, kit.UserID(r), fallback)
}

// Facts are what the mails say.
type Facts struct {
	Base, Username, Email, Sender string
	Self                          bool
	// Expires is when the link stops working; Lang and Zone are the
	// reader's language and time zone.
	Expires time.Time
	Lang    string
	Zone    *time.Location
}

// ConfirmMail is the mail to the new address with the link that confirms
// it.
func ConfirmMail(t brandmail.Translate, f Facts, token string) brandmail.Mail {
	host := brandmail.HostOf(f.Base)
	vars := map[string]any{"sender": f.Sender, "username": f.Username, "email": f.Email, "host": host, "expires": brandmail.DateTime(f.Expires, f.Lang, f.Zone)}
	lead := t("mail.email_confirm.lead", vars)
	if f.Self {
		lead = t("mail.email_confirm.lead_self", vars)
	}
	return brandmail.Mail{
		Kind: "email-confirm", T: t, Lang: f.Lang, Host: host,
		Subject: t("mail.email_confirm.subject", vars), Preheader: t("mail.email_confirm.preheader", vars),
		Heading: t("mail.email_confirm.heading", vars), Lead: lead,
		Button: t("mail.email_confirm.button", vars), Link: lifecycle.ConfirmEmailLink(f.Base, token),
		Details: []string{t("mail.account_invite.username", vars), t("mail.email_confirm.expires", vars), t("mail.email_confirm.ignore", vars)},
		Why:     t("mail.email_confirm.why", vars),
	}
}

// NoticeMail is the notice to the old address: the address is being
// changed (variant ""), was changed ("changed") or was removed
// ("removed").
func NoticeMail(t brandmail.Translate, f Facts, variant string) brandmail.Mail {
	host := brandmail.HostOf(f.Base)
	vars := map[string]any{"username": f.Username, "email": f.Email, "host": host}
	suffix := ""
	if variant != "" {
		suffix = "_" + variant
	}
	heading := "email_change.notice_heading"
	if variant != "" {
		heading = "mail.email_notice.subject" + suffix
	}
	return brandmail.Mail{
		Kind: "email-notice", T: t, Lang: f.Lang, Host: host, Automatic: true,
		Subject: t("mail.email_notice.subject"+suffix, vars), Preheader: t("mail.email_notice.preheader", vars),
		Heading: t(heading, vars), Lead: t("mail.email_notice.lead"+suffix, vars),
		Button: t("mail.email_notice.button", vars), Link: f.Base + "/login",
		Details: []string{t("mail.email_notice.not_you", vars)},
		Why:     t("mail.email_notice.why", vars),
	}
}
