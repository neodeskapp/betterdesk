package settings

import (
	"database/sql"
	"errors"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/mailcatalog"
)

// The routes of the mails under /api/settings/email (server.config): who
// the mails say they are from, a preview of every mail, "Send me this
// mail", and the addresses that turned a notification off. The link in a
// notification mail that turns it off is /mail/unsubscribe, for anyone
// with the link.

// registerMails adds the routes.
func (h *handlers) registerMails(server kit.Middleware) {
	d, r := h.d, h.d.Router
	r.Get("/api/settings/email/identity", h.getIdentity, d.RequireAuth, server)
	r.Put("/api/settings/email/identity", h.saveIdentity, d.RequireAuth, server)
	r.Get("/api/settings/email/previews", h.previewList, d.RequireAuth, server)
	r.Get("/api/settings/email/previews/{kind}", h.preview, d.RequireAuth, server)
	r.Post("/api/settings/email/previews/{kind}/send", h.sendPreview, d.RequireAuth, server)
	r.Get("/api/settings/email/unsubscribed", h.unsubscribedList, d.RequireAuth, server)
	r.Post("/api/settings/email/unsubscribed/resubscribe", h.resubscribe, d.RequireAuth, server)
	// Before the CSRF gate: a mail client's one-click POST carries no token,
	// and the signed link is what allows it
	if d.Early != nil {
		d.Early.Get(mail.UnsubscribePath, h.unsubscribePage)
		d.Early.Post(mail.UnsubscribePath, h.unsubscribe)
	}
}

type identityAnswer struct {
	Identity        mail.Identity `json:"identity"`
	DefaultFromName string        `json:"default_from_name"`
}

func (h *handlers) identityAnswer(r *http.Request) identityAnswer {
	brand := h.d.Mail.Branding(r.Context())
	return identityAnswer{Identity: h.d.Mail.LoadIdentity(r.Context()), DefaultFromName: mail.FromName(mail.Identity{}, brand)}
}

// getIdentity answers GET /api/settings/email/identity.
func (h *handlers) getIdentity(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, h.identityAnswer(r))
}

// identityRefusals are the texts of a refused field, by error.
var identityRefusals = map[error]string{
	mail.ErrIdentityTooLong: "settings.email.err_identity_too_long",
	mail.ErrIdentityEmail:   "settings.email.err_identity_email",
	mail.ErrIdentityURL:     "settings.email.err_identity_url",
}

// saveIdentity answers PUT /api/settings/email/identity with the body
// {from_name, organisation, support_email, support_url}.
func (h *handlers) saveIdentity(w http.ResponseWriter, r *http.Request) {
	var id mail.Identity
	if err := kit.Bind(r, &id); err != nil {
		kit.FailCode(w, r, http.StatusBadRequest, "Invalid body", "invalid_body")
		return
	}
	if _, err := h.d.Mail.SaveIdentity(r.Context(), id); err != nil {
		var refused *mail.IdentityError
		if errors.As(err, &refused) {
			kit.JSON(w, r, http.StatusBadRequest, smtpRefused{Error: kit.T(r, identityRefusals[refused.Err], nil), Field: refused.Field})
			return
		}
		h.log.Error("Save the mail identity:", err.Error())
		kit.Fail(w, r, http.StatusInternalServerError, "Server error")
		return
	}
	_ = h.d.LogAction(r, "mail_identity_saved", "Saved the sender and footer of the mails")
	kit.JSON(w, r, http.StatusOK, h.identityAnswer(r))
}

type previewKind struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
}

type language struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// previewList answers GET /api/settings/email/previews: the mails, with
// their names in the request's language, and the languages.
func (h *handlers) previewList(w http.ResponseWriter, r *http.Request) {
	kinds := make([]previewKind, 0, len(mailcatalog.Kinds))
	for _, k := range mailcatalog.Kinds {
		kinds = append(kinds, previewKind{Kind: k, Title: kit.T(r, "settings.email.preview_kind_"+strings.ReplaceAll(k, "-", "_"), nil)})
	}
	var langs []language
	if h.d.I18n != nil && h.d.I18n.Bundle != nil {
		list, _ := h.d.I18n.Bundle.AvailableList()
		for _, l := range list {
			code, _ := l.Get("code")
			name, _ := l.Get("native")
			c, _ := code.(string)
			n, _ := name.(string)
			if n == "" {
				n = c
			}
			if c != "" {
				langs = append(langs, language{Code: c, Name: n})
			}
		}
	}
	kit.JSON(w, r, http.StatusOK, map[string]any{"kinds": kinds, "languages": langs, "default_language": kit.Lang(r)})
}

// source is what the samples are made with: the console's address and
// name, and the signed-in admin as the reader.
func (h *handlers) source(r *http.Request) mailcatalog.Source {
	base := h.d.Mail.GetPanelBaseURL()
	if base == "" {
		scheme := "http"
		if kit.Secure(r) {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	user := ""
	if u := kit.User(r); u != nil {
		user = u.Username
	}
	return mailcatalog.Source{Bundle: h.d.I18n.Bundle, Mail: h.d.Mail, Base: base, Server: h.d.Mail.Branding(r.Context()).Name(), Reader: user, Now: time.Now()}
}

type previewAnswer struct {
	Kind      string `json:"kind"`
	Lang      string `json:"lang"`
	From      string `json:"from"`
	Subject   string `json:"subject"`
	Preheader string `json:"preheader"`
	HTML      string `json:"html"`
	Text      string `json:"text"`
}

// preview answers GET /api/settings/email/previews/{kind}?lang=&scheme=:
// the mail as the reader gets it with the branding of now, its HTML with
// the images inline and in the light or dark palette.
func (h *handlers) preview(w http.ResponseWriter, r *http.Request) {
	m, err := h.source(r).Build(r.Context(), r.PathValue("kind"), h.previewLang(r, r.URL.Query().Get("lang")))
	if errors.Is(err, mailcatalog.ErrUnknown) {
		kit.FailCode(w, r, http.StatusNotFound, "No such mail", "not_found")
		return
	}
	if err != nil {
		kit.Fail(w, r, http.StatusInternalServerError, "Server error")
		return
	}
	ctx := r.Context()
	unsub := ""
	if kind, ok := mailcatalog.Notifications[r.PathValue("kind")]; ok {
		unsub = h.d.Mail.UnsubscribeURL(ctx, kind, "preview@example.com")
	}
	built := brandmail.Render(m, h.d.Mail.Branding(ctx), unsub)
	kit.JSON(w, r, http.StatusOK, previewAnswer{
		Kind: r.PathValue("kind"), Lang: built.Lang, From: mail.FromName(h.d.Mail.LoadIdentity(ctx), h.d.Mail.Branding(ctx)),
		Subject: built.Subject, Preheader: built.Preheader, HTML: brandmail.Preview(built, r.URL.Query().Get("scheme") == "dark"), Text: built.Text,
	})
}

// previewLang is the language asked for when the console has it, else
// the request's.
func (h *handlers) previewLang(r *http.Request, lang string) string {
	if lang != "" && h.d.I18n != nil && h.d.I18n.Bundle != nil && h.d.I18n.Bundle.Has(lang) {
		return lang
	}
	return kit.Lang(r)
}

// sendPreview answers POST /api/settings/email/previews/{kind}/send with
// {lang}: the mail goes to the signed-in admin's own address.
func (h *handlers) sendPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Lang string `json:"lang"`
	}
	_ = kit.Bind(r, &body)
	to := h.ownEmail(r)
	if to == "" {
		kit.FailCode(w, r, http.StatusConflict, kit.T(r, "settings.email.preview_no_address", nil), "no_address")
		return
	}
	m, err := h.source(r).Build(r.Context(), r.PathValue("kind"), h.previewLang(r, body.Lang))
	if errors.Is(err, mailcatalog.ErrUnknown) {
		kit.FailCode(w, r, http.StatusNotFound, "No such mail", "not_found")
		return
	}
	if err != nil {
		kit.Fail(w, r, http.StatusInternalServerError, "Server error")
		return
	}
	if h.d.Mail.LoadSMTPConfig(r.Context()) == nil {
		kit.FailCode(w, r, http.StatusConflict, kit.T(r, "settings.email.preview_failed", nil), "smtp_not_configured")
		return
	}
	// A sample is no notification of the admin's: it can't turn theirs off
	m.Unsubscribe = ""
	if sent, _ := h.d.Mail.SendEmail(r.Context(), mail.Email{To: []string{to}, Mail: m}); !sent {
		kit.FailCode(w, r, http.StatusBadGateway, kit.T(r, "settings.email.preview_failed", nil), "mail_failed")
		return
	}
	_ = h.d.LogAction(r, "mail_preview_sent", "Sent the mail "+r.PathValue("kind")+" to their own address")
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "to": to})
}

// ownEmail is the signed-in account's address.
func (h *handlers) ownEmail(r *http.Request) string {
	if h.d.Store == nil || kit.UserID(r) == 0 {
		return ""
	}
	var email sql.NullString
	if err := h.d.Store.QueryRow(r.Context(), `SELECT email FROM users WHERE id = ?`, kit.UserID(r)).Scan(&email); err != nil {
		return ""
	}
	return strings.TrimSpace(email.String)
}

// unsubscribedList answers GET /api/settings/email/unsubscribed.
func (h *handlers) unsubscribedList(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, map[string]any{"unsubscribed": h.d.Mail.UnsubscribedList(r.Context())})
}

// resubscribe answers POST /api/settings/email/unsubscribed/resubscribe
// with {kind, address}: the address gets that kind of mail again.
func (h *handlers) resubscribe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind    string `json:"kind"`
		Address string `json:"address"`
	}
	if err := kit.Bind(r, &body); err != nil || body.Address == "" {
		kit.FailCode(w, r, http.StatusBadRequest, "Invalid body", "invalid_body")
		return
	}
	if err := h.d.Mail.SetUnsubscribed(r.Context(), body.Kind, body.Address, false); err != nil {
		kit.FailCode(w, r, http.StatusBadRequest, "Unknown notification", "invalid_kind")
		return
	}
	_ = h.d.LogAction(r, "mail_resubscribed", "Turned "+body.Kind+" mails on again for an address")
	kit.JSON(w, r, http.StatusOK, map[string]any{"unsubscribed": h.d.Mail.UnsubscribedList(r.Context())})
}

// unsubscribePage answers GET /mail/unsubscribe?t=: what the link turns
// off, and a button that does it. Opening the link changes nothing, so a
// mail scanner that follows links turns nothing off.
func (h *handlers) unsubscribePage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("t")
	kind, addr, ok := h.d.Mail.ReadUnsubscribeToken(r.Context(), token)
	t, lang := h.pageTexts(r, addr)
	if !ok {
		h.unsubscribeAnswer(w, http.StatusNotFound, lang, t("mail.unsubscribe_page.title"), t("mail.unsubscribe_page.invalid"), "")
		return
	}
	form := `<form method="post" action="` + html.EscapeString(mail.UnsubscribePath+"?t="+token) + `"><button type="submit">` + html.EscapeString(t("mail.unsubscribe_page.button")) + `</button></form>`
	h.unsubscribeAnswer(w, http.StatusOK, lang, t("mail.unsubscribe_page.title"), strings.ReplaceAll(t("mail.unsubscribe_page.confirm_"+kind), "{address}", addr), form)
}

// unsubscribe answers POST /mail/unsubscribe?t=: the one-click POST of a
// mail client (RFC 8058) and the page's button both turn it off.
func (h *handlers) unsubscribe(w http.ResponseWriter, r *http.Request) {
	kind, addr, ok := h.d.Mail.ReadUnsubscribeToken(r.Context(), r.URL.Query().Get("t"))
	t, lang := h.pageTexts(r, addr)
	if !ok {
		h.unsubscribeAnswer(w, http.StatusNotFound, lang, t("mail.unsubscribe_page.title"), t("mail.unsubscribe_page.invalid"), "")
		return
	}
	if err := h.d.Mail.SetUnsubscribed(r.Context(), kind, addr, true); err != nil {
		h.log.Error("Unsubscribe:", err.Error())
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	h.unsubscribeAnswer(w, http.StatusOK, lang, t("mail.unsubscribe_page.title"), strings.ReplaceAll(t("mail.unsubscribe_page.done"), "{address}", addr), "")
}

// pageTexts are the page's texts in the language of the account with the
// address, else the console's default.
func (h *handlers) pageTexts(r *http.Request, addr string) (func(string) string, string) {
	tr, lang := h.d.Mail.Translator(h.d.Mail.ReaderOf(r.Context(), addr).Lang)
	return func(key string) string { return tr(key, nil) }, lang
}

// unsubscribeAnswer is the small page of the link, in the brand's
// palette.
func (h *handlers) unsubscribeAnswer(w http.ResponseWriter, status int, lang, title, text, form string) {
	e := html.EscapeString
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`<!DOCTYPE html><html lang="` + e(lang) + `"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="color-scheme" content="light dark"><title>` + e(title) + `</title></head>` +
		`<body style="margin: 0; padding: 48px 16px; font-family: 'IBM Plex Sans', -apple-system, 'Segoe UI', Roboto, Arial, sans-serif; line-height: 1.5;"><main style="max-width: 560px; margin: 0 auto;"><h1 style="font-size: 24px;">` + e(title) + `</h1><p>` + e(text) + `</p>` + form + `</main></body></html>`))
}
