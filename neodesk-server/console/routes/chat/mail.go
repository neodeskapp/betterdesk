package chat

import (
	"context"
	"database/sql"
	"html"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/chatstore"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
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
// console ends the wait.

// mailPreview is how many characters of the message the mail quotes.
const mailPreview = 80

// waitMailer sends the mails; *mail.Service is one.
type waitMailer interface {
	SendEmail(ctx context.Context, m mail.Email) (bool, error)
	GetPanelBaseURL() string
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
		if d.Mail == nil {
			continue
		}
		appName := "NeoDesk"
		if d.Branding != nil {
			appName = d.Branding.AppName("NeoDesk")
		}
		if err := hub.mailWaiting(ctx, d.Mail, appName, time.Now()); err != nil {
			logging.Logf("console", "WARN: [Chat] mail about waiting people: %v", err)
		}
	}
}

// mailWaiting sends the mails that are due at now.
func (h *Hub) mailWaiting(ctx context.Context, mailer waitMailer, appName string, now time.Time) error {
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
		if len(to) > 0 {
			subject, text, body := h.waitMail(ctx, c, appName, mailer.GetPanelBaseURL(), settings.MailPreview, now)
			if _, err := mailer.SendEmail(ctx, mail.Email{To: to, ToList: true, Subject: subject, Text: &text, HTML: &body}); err != nil {
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
}

func (h *Hub) accounts(ctx context.Context) ([]account, error) {
	db := h.opts.Store.DB()
	rows, err := db.Query(ctx, `SELECT id, username, role, email FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []account
	for rows.Next() {
		var a account
		var role, email sql.NullString
		if err := rows.Scan(&a.ID, &a.Username, &role, &email); err != nil {
			return nil, err
		}
		a.Role, a.Email = role.String, strings.TrimSpace(email.String)
		out = append(out, a)
	}
	return out, rows.Err()
}

// waitRecipients are the addresses of a waiting conversation's mail.
func (h *Hub) waitRecipients(ctx context.Context, c chatstore.Conversation) ([]string, error) {
	all, err := h.accounts(ctx)
	if err != nil {
		return nil, err
	}
	wants := func(a account) bool {
		return a.Email != "" && LoadAccountSettings(ctx, h.opts.Store, a.ID).Mail &&
			h.MayRead(ctx, &session.User{ID: a.ID, Username: a.Username, Role: a.Role}, c.ID)
	}
	var to []string
	if c.AssigneeID != 0 {
		for _, a := range all {
			if a.ID == c.AssigneeID && wants(a) {
				to = append(to, a.Email)
			}
		}
		return to, nil
	}
	for _, a := range all {
		if h.holds(ctx, a.Role, permAssign) && wants(a) {
			to = append(to, a.Email)
		}
	}
	return to, nil
}

// waitMail is the subject and bodies of a waiting conversation's mail.
func (h *Hub) waitMail(ctx context.Context, c chatstore.Conversation, appName, baseURL string, preview bool, now time.Time) (subject, text, body string) {
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
	minutes := int(now.Sub(time.UnixMilli(c.WaitingSinceMs)).Minutes())
	subject = appName + ": " + name + " is waiting for an answer"
	lines := []string{
		"Someone at " + name + " (ID " + c.ID + ") has waited " + strconv.Itoa(minutes) + " minutes for an answer in the chat.",
	}
	quote := ""
	if preview {
		if m, err := h.opts.Store.Get(ctx, c.LastMessageID); err == nil && m.From.Kind == chatstore.KindDevice && !m.Deleted() {
			quote = cutChars(strings.Join(strings.Fields(m.Text), " "), mailPreview)
			if len([]rune(m.Text)) > mailPreview {
				quote += "…"
			}
		}
	}
	if quote != "" {
		lines = append(lines, "", "They wrote: "+quote)
	}
	link := ""
	if baseURL != "" {
		link = strings.TrimRight(baseURL, "/") + "/chat?c=" + url.QueryEscape(c.ID)
		lines = append(lines, "", "Open the conversation: "+link)
	}
	text = strings.Join(lines, "\n")
	body = `<div style="font-family: sans-serif; max-width: 600px;">` +
		`<h2 style="font-size: 18px;">` + html.EscapeString(name) + ` is waiting for an answer</h2>` +
		`<p>` + html.EscapeString(lines[0]) + `</p>`
	if quote != "" {
		body += `<blockquote style="margin: 12px 0; padding: 8px 12px; border-left: 3px solid #2563eb; color: #333;">` + html.EscapeString(quote) + `</blockquote>`
	}
	if link != "" {
		body += `<p><a href="` + html.EscapeString(link) + `">Open the conversation in ` + html.EscapeString(appName) + `</a></p>`
	}
	body += `</div>`
	return subject, text, body
}
