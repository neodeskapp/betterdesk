package chat

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/chatstore"
	"github.com/neodeskapp/neodesk/neodesk-server/console/i18n"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/people"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// The mail when nobody answers (A242 c): a person at a computer who has
// waited the set minutes (chat settings, 15 by default, 0 for never) for
// an answer gets the console's attention by mail. It goes to whoever has
// the conversation, else to everyone with chat.assign who sees the
// computer and has chat mails on. It names the computer and links to the
// conversation; it quotes the first 80 characters of the waiting message
// unless an admin turned that off. One mail per wait: an answer from the
// console ends the wait. Each recipient reads it in the language of the
// account (users.preferred_language), English without one; recipients of
// one language share a mail.

// mailPreview is how many characters of the message the mail quotes.
const mailPreview = 80

// waitMailer sends the mails; *mail.Service is one.
type waitMailer interface {
	SendEmail(ctx context.Context, m mail.Email) (bool, error)
	GetPanelBaseURL() string
}

// mailTexts are the console's texts in each language; *i18n.Bundle is
// one.
type mailTexts interface {
	Translate(lang, key string, vars i18n.Vars) string
}

// RunWaitMail checks every minute for people who wait, until ctx ends.
func RunWaitMail(ctx context.Context, d *kit.Deps) {
	hub := HubOf(d)
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if d.Mail == nil || d.I18n == nil || d.I18n.Bundle == nil {
			continue
		}
		if err := hub.mailWaiting(ctx, d.Mail, brandmail.Of(d.Branding).Name(), d.I18n.Bundle, time.Now()); err != nil {
			logging.Logf("console", "WARN: [Chat] mail about waiting people: %v", err)
		}
	}
}

// mailWaiting sends the mails that are due at now: one per recipient,
// in the account's language and time zone, with a link that turns them
// off. server is the server's name.
func (h *Hub) mailWaiting(ctx context.Context, mailer waitMailer, server string, texts mailTexts, now time.Time) error {
	st := h.opts.Store
	settings := LoadServerSettings(ctx, st)
	if settings.MailAfterMinutes <= 0 {
		return nil
	}
	due, err := st.Unanswered(ctx, now.Add(-time.Duration(settings.MailAfterMinutes)*time.Minute).UnixMilli())
	if err != nil {
		return err
	}
	for _, c := range due {
		to, err := h.waitRecipients(ctx, c)
		if err != nil {
			return err
		}
		for _, a := range to {
			t := func(key string, vars map[string]any) string {
				pairs := make([]any, 0, 2*len(vars))
				for k, v := range vars {
					pairs = append(pairs, k, v)
				}
				return texts.Translate(a.Lang, key, i18n.V(pairs...))
			}
			// The language the texts came from: the account's, or the default one
			pageLang := t("meta.lang", nil)
			if pageLang == "meta.lang" {
				pageLang = ""
			}
			zone := brandmail.Zone(people.TimeZone(ctx, st.DB(), a.ID))
			m := h.waitMail(ctx, c, server, pageLang, mailer.GetPanelBaseURL(), settings.MailPreview, t, zone)
			if _, err := mailer.SendEmail(ctx, mail.Email{To: []string{a.Email}, Mail: m}); err != nil {
				return err
			}
		}
		// One mail per wait, also when nobody could be told
		if err := st.MarkMailed(ctx, c.ID); err != nil {
			return err
		}
	}
	return nil
}

// account is a console account the mail may go to.
type account struct {
	ID       int64
	Username string
	Role     string
	Email    string
	// Lang is the account's language, "" for the console's default
	Lang string
}

func (h *Hub) accounts(ctx context.Context) ([]account, error) {
	db := h.opts.Store.DB()
	rows, err := db.Query(ctx, `SELECT id, username, role, email, preferred_language FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []account
	for rows.Next() {
		var a account
		var role, email, lang sql.NullString
		if err := rows.Scan(&a.ID, &a.Username, &role, &email, &lang); err != nil {
			return nil, err
		}
		a.Role, a.Email, a.Lang = role.String, strings.TrimSpace(email.String), strings.TrimSpace(lang.String)
		out = append(out, a)
	}
	return out, rows.Err()
}

// waitRecipients are the accounts a waiting conversation's mail goes to.
func (h *Hub) waitRecipients(ctx context.Context, c chatstore.Conversation) ([]account, error) {
	all, err := h.accounts(ctx)
	if err != nil {
		return nil, err
	}
	wants := func(a account) bool {
		return a.Email != "" && LoadAccountSettings(ctx, h.opts.Store, a.ID).Mail &&
			h.MayRead(ctx, &session.User{ID: a.ID, Username: a.Username, Role: a.Role}, c.ID)
	}
	var to []account
	if c.AssigneeID != 0 {
		for _, a := range all {
			if a.ID == c.AssigneeID && wants(a) {
				to = append(to, a)
			}
		}
		return to, nil
	}
	for _, a := range all {
		if h.holds(ctx, a.Role, permAssign) && wants(a) {
			to = append(to, a)
		}
	}
	return to, nil
}

// waitMail is the mail about a waiting conversation in the language of
// t (lang): who waits since when, the start of their message unless an
// admin turned that off, and a button to the conversation.
func (h *Hub) waitMail(ctx context.Context, c chatstore.Conversation, server, lang, baseURL string, preview bool,
	t func(key string, vars map[string]any) string, zone *time.Location) brandmail.Mail {
	name := c.ID
	if h.opts.Scope.Device != nil {
		if d := h.opts.Scope.Device(ctx, c.ID); d != nil {
			for _, key := range []string{"display_name", "hostname"} {
				if v, _ := d.Get(key).(string); strings.TrimSpace(v) != "" {
					name = strings.TrimSpace(v)
					break
				}
			}
		}
	}
	since := brandmail.DateTime(time.UnixMilli(c.WaitingSinceMs), lang, zone)
	vars := map[string]any{"name": name, "id": c.ID, "time": since, "server": server}
	m := brandmail.Mail{
		Kind: "chat", T: t, Lang: lang, Automatic: true, Unsubscribe: mail.NotifyChat,
		Subject: t("mail.chat.subject", vars), Preheader: t("mail.chat.preheader", vars),
		Heading: t("mail.chat.heading", vars), Lead: t("mail.chat.lead", vars),
		Why: t("mail.chat.why", vars), TurnOff: t("mail.chat.turn_off", vars), TurnOffNoLink: t("mail.chat.turn_off_nolink", vars),
	}
	if preview {
		if msg, err := h.opts.Store.Get(ctx, c.LastMessageID); err == nil && msg.From.Kind == chatstore.KindDevice && !msg.Deleted() {
			quote := cutChars(strings.Join(strings.Fields(msg.Text), " "), mailPreview)
			if len([]rune(msg.Text)) > mailPreview {
				quote += "…"
			}
			if quote != "" {
				m.Quote, m.QuoteLabel = quote, t("mail.chat.quote_label", nil)
				m.Preheader = quote
			}
		}
	}
	if baseURL != "" {
		m.Button, m.Link = t("mail.chat.button", vars), strings.TrimRight(baseURL, "/")+"/chat?c="+url.QueryEscape(c.ID)
	}
	return m
}

// PreviewWaitMail is the mail with made-up facts, for the mail previews.
func PreviewWaitMail(t func(key string, vars map[string]any) string, lang, baseURL, server string, since time.Time) brandmail.Mail {
	h := &Hub{}
	c := chatstore.Conversation{ID: "123456789", WaitingSinceMs: since.UnixMilli()}
	m := h.waitMail(context.Background(), c, server, lang, baseURL, false, t, time.Local)
	vars := map[string]any{"name": "Reception PC", "id": c.ID, "time": brandmail.DateTime(since, lang, time.Local), "server": server}
	m.Subject, m.Heading, m.Lead = t("mail.chat.subject", vars), t("mail.chat.heading", vars), t("mail.chat.lead", vars)
	m.Quote, m.QuoteLabel, m.Preheader = "My printer stopped working after the update", t("mail.chat.quote_label", nil), "My printer stopped working after the update"
	return m
}
