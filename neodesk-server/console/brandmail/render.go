package brandmail

import (
	"encoding/base64"
	"html"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/i18n"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail/message"
)

// Translate gives the text of a language key with its {name}
// placeholders filled from vars, in the language a mail is written in.
type Translate func(key string, vars map[string]any) string

// Mail is what one mail says, in the reader's language, from top to
// bottom. Empty parts are left out. Render lays it out.
type Mail struct {
	// Kind names the mail ("invite", "password-reset") for the previews
	// and the tests.
	Kind string
	// T gives the layout's own texts (the footer, the line above the
	// link) in Lang, a code of the language files ("de", "ar"). Right to
	// left languages get a mirrored layout.
	T    Translate
	Lang string
	// Subject says what this is and who it is from; Preheader is the line
	// a client shows after it in the inbox, and adds to it.
	Subject   string
	Preheader string
	// Heading and Lead say who is writing, what happened and what the
	// reader should do.
	Heading string
	Lead    string
	// Facts are label and value rows below the lead (an alert's device).
	Facts []Fact
	// Quote is a text the mail quotes, with QuoteLabel ("They wrote:").
	QuoteLabel string
	Quote      string
	// Button is the one primary action and Link where it goes. The link
	// is written out below the button.
	Button string
	Link   string
	// Steps are numbered after the button.
	Steps []string
	// CodeLead says what to do with Code, which is shown on a line of its
	// own in the one form the mail uses everywhere.
	CodeLead string
	Code     string
	// Details are short paragraphs after the action: until when a link
	// works, what to do when it wasn't the reader.
	Details []string
	// Why says why the reader gets this mail; TurnOff, for notifications,
	// how to stop them. Both go into the footer.
	Why     string
	TurnOff string
	// TurnOffNoLink stands in for TurnOff when the mail has no link that
	// turns it off (the console has no public address).
	TurnOffNoLink string
	// Automatic marks a mail the server sends on its own, not because a
	// person sent it to the reader (Auto-Submitted: auto-generated).
	Automatic bool
	// Unsubscribe names the kind of a recurring notification ("chat",
	// "alert", "help"): the mail service then adds List-Unsubscribe for
	// it, one mail per recipient.
	Unsubscribe string
	// ReplyTo is where replies go: the person who sent an invite. Empty is
	// the support address of the mail settings.
	ReplyTo string
	// Host overrides the branding's host in the footer.
	Host string
}

// Built is a rendered mail, ready for the mail service.
type Built struct {
	Kind      string
	Subject   string
	Preheader string
	Text      string
	HTML      string
	Inline    []message.Inline
	Lang      string
	Automatic bool
	ReplyTo   string
	// Unsubscribe is the kind of notification, UnsubscribeURL the link
	// that turns it off for this recipient.
	Unsubscribe    string
	UnsubscribeURL string
	rendered       bool
}

// Rendered reports whether the mail came out of Render, the only way
// the mail service sends one.
func (b Built) Rendered() bool { return b.rendered }

// Render lays out a mail in the server's branding. unsubscribeURL is the
// recipient's link that turns the notification off, "" for none.
func Render(m Mail, b Branding, unsubscribeURL string) Built {
	if m.Host != "" {
		b.Host = m.Host
	}
	if m.T == nil {
		m.T = func(key string, _ map[string]any) string { return key }
	}
	if m.Unsubscribe == "" {
		unsubscribeURL = ""
	}
	r := renderer{m: m, b: b, unsubscribe: unsubscribeURL}
	return Built{
		Kind: m.Kind, Subject: oneLine(m.Subject), Preheader: oneLine(m.Preheader),
		Text: r.text(), HTML: r.html(), Inline: Inline(b), Lang: m.Lang,
		Automatic: m.Automatic, ReplyTo: m.ReplyTo,
		Unsubscribe: m.Unsubscribe, UnsubscribeURL: unsubscribeURL, rendered: true,
	}
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

type renderer struct {
	m           Mail
	b           Branding
	unsubscribe string
}

// A footer line with its links: each {email} or {url} in it is a link.
type footerLine struct {
	text  string
	links map[string]string // placeholder text -> address
}

// footer is what the footer says: which server sent the mail, who runs
// it and how to reach them, why the reader got it, how to turn it off.
func (r renderer) footer() []footerLine {
	t, b := r.m.T, r.b
	var lines []footerLine
	vars := map[string]any{"server": b.Name(), "host": b.Host}
	switch {
	case b.OwnName() && b.Host != "":
		lines = append(lines, footerLine{text: t("mail.footer_sent_named", vars)})
	case b.OwnName():
		lines = append(lines, footerLine{text: t("mail.footer_sent_named_nohost", vars)})
	case b.Host != "":
		lines = append(lines, footerLine{text: t("mail.footer_sent_default", vars)})
	default:
		lines = append(lines, footerLine{text: t("mail.footer_sent_default_nohost", vars)})
	}
	c := b.Contact
	if org := strings.TrimSpace(c.Organisation); org != "" {
		lines = append(lines, footerLine{text: t("mail.footer_run_by", map[string]any{"organisation": org})})
	}
	email, link := strings.TrimSpace(c.Email), safeURL(c.URL)
	links := map[string]string{}
	if email != "" {
		links[email] = "mailto:" + email
	}
	if link != "" {
		links[link] = link
	}
	switch {
	case email != "" && link != "":
		lines = append(lines, footerLine{text: t("mail.footer_contact_both", map[string]any{"email": email, "url": link}), links: links})
	case email != "":
		lines = append(lines, footerLine{text: t("mail.footer_contact_email", map[string]any{"email": email}), links: links})
	case link != "":
		lines = append(lines, footerLine{text: t("mail.footer_contact_url", map[string]any{"url": link}), links: links})
	}
	if r.m.Why != "" {
		lines = append(lines, footerLine{text: r.m.Why})
	}
	turnOff := r.m.TurnOff
	if r.m.Unsubscribe != "" && r.unsubscribe == "" {
		turnOff = r.m.TurnOffNoLink
	}
	if turnOff != "" {
		lines = append(lines, footerLine{text: turnOff})
	}
	return lines
}

// safeURL is an http or https address, else "".
func safeURL(u string) string {
	u = strings.TrimSpace(u)
	low := strings.ToLower(u)
	if (strings.HasPrefix(low, "https://") || strings.HasPrefix(low, "http://")) && !strings.ContainsAny(u, " \"'<>") {
		return u
	}
	return ""
}

func (r renderer) linkHint() string { return r.m.T("mail.link_hint", nil) }

func (r renderer) unsubscribeText() string { return r.m.T("mail.unsubscribe", nil) }

// text is the plain text part: everything the HTML says, in the same
// order, with the link written out.
func (r renderer) text() string {
	m := r.m
	var parts []string
	add := func(lines ...string) {
		var kept []string
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				kept = append(kept, l)
			}
		}
		if len(kept) > 0 {
			parts = append(parts, strings.Join(kept, "\n"))
		}
	}
	add(m.Heading)
	add(m.Lead)
	var facts []string
	for _, f := range m.Facts {
		facts = append(facts, f.Label+": "+f.Value)
	}
	add(facts...)
	if m.Quote != "" {
		quoted := []string{m.QuoteLabel}
		for _, l := range strings.Split(m.Quote, "\n") {
			quoted = append(quoted, "> "+l)
		}
		add(quoted...)
	}
	if m.Button != "" && m.Link != "" {
		add(m.Button+":", m.Link)
	}
	var steps []string
	for i, s := range m.Steps {
		steps = append(steps, strconv.Itoa(i+1)+". "+s)
	}
	add(steps...)
	if m.Code != "" {
		add(m.CodeLead, m.Code)
	}
	for _, d := range m.Details {
		add(d)
	}
	var foot []string
	for _, l := range r.footer() {
		foot = append(foot, l.text)
	}
	if r.unsubscribe != "" {
		foot = append(foot, r.unsubscribeText()+": "+r.unsubscribe)
	}
	text := strings.Join(parts, "\n\n")
	if len(foot) > 0 {
		text += "\n\n-- \n" + strings.Join(foot, "\n")
	}
	return text + "\n"
}

var esc = html.EscapeString

// withLinks is a footer line as HTML, each of its addresses a link.
func withLinks(l footerLine, style string) string {
	out := esc(l.text)
	for shown, href := range l.links {
		out = strings.ReplaceAll(out, esc(shown), `<a class="nd-link" href="`+esc(href)+`" style="`+style+`">`+esc(shown)+`</a>`)
	}
	return out
}

// darkStyle is the head's style sheet: the dark palette for clients that
// follow dark mode, also in the form Outlook on the web and Outlook.com
// read ([data-ogsc] for text, [data-ogsb] for backgrounds), the swap of
// the logos, and the narrow layout for phones.
func darkStyle(d Palette) string {
	rules := func(text, bg string) string {
		return bg + `.nd-page { background-color: ` + d.Page + ` !important; }
` + bg + `.nd-quote { background-color: ` + d.Quote + ` !important; }
` + bg + `.nd-btn, ` + bg + `.nd-step { background-color: ` + d.Button + ` !important; border-color: ` + d.Button + ` !important; }
` + text + `.nd-ink { color: ` + d.Ink + ` !important; }
` + text + `.nd-muted { color: ` + d.Muted + ` !important; }
` + text + `.nd-link { color: ` + d.Link + ` !important; }
` + text + `.nd-on { color: ` + d.OnButton + ` !important; }
` + bg + `.nd-rule { border-color: ` + d.Rule + ` !important; }
` + bg + `.nd-light { display: none !important; }
` + bg + `.nd-dark { display: block !important; max-height: none !important; overflow: visible !important; }
` + bg + `.nd-dark-logo { width: 136px !important; height: 32px !important; }
` + bg + `.nd-dark-mark { width: 32px !important; height: 32px !important; }
`
	}
	return `:root { color-scheme: light dark; supported-color-schemes: light dark; }
@media (prefers-color-scheme: dark) {
` + rules("", "") + `}
` + rules("[data-ogsc] ", "[data-ogsb] ") + `@media (max-width: 480px) {
.nd-h1 { font-size: 20px !important; }
.nd-pad { padding-left: 0 !important; padding-right: 0 !important; }
}
`
}

// html is the HTML part.
func (r renderer) html() string {
	m, b := r.m, r.b
	light, dark := Palettes(b.Accent)
	l := light
	font := "font-family: " + mailFontList + ";"
	dir, start := "ltr", "left"
	if i18n.RTL(m.Lang) {
		dir, start = "rtl", "right"
	}
	langAttr := ""
	if m.Lang != "" {
		langAttr = ` lang="` + esc(m.Lang) + `"`
	}
	link := esc(m.Link)
	var s strings.Builder
	w := func(parts ...string) {
		for _, p := range parts {
			s.WriteString(p)
		}
	}
	row := func(padding string, content ...string) {
		w(`<tr><td class="nd-pad" style="padding: `, padding, `; text-align: `, start, `;">`)
		w(content...)
		w(`</td></tr>`)
	}
	para := func(class, style, content string) string {
		return `<p class="` + class + `" style="margin: 0; ` + font + ` ` + style + `">` + content + `</p>`
	}
	body := "font-size: 16px; line-height: 1.5; color: " + l.Ink + ";"
	small := "font-size: 14px; line-height: 1.5; color: " + l.Muted + ";"

	w(`<!DOCTYPE html><html`, langAttr, ` dir="`, dir, `" xmlns="http://www.w3.org/1999/xhtml" xmlns:v="urn:schemas-microsoft-com:vml" xmlns:o="urn:schemas-microsoft-com:office:office"><head>`)
	w(`<meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta http-equiv="X-UA-Compatible" content="IE=edge">`)
	w(`<meta name="color-scheme" content="light dark"><meta name="supported-color-schemes" content="light dark"><meta name="x-apple-disable-message-reformatting">`)
	w(`<title>`, esc(m.Subject), `</title>`)
	w(`<!--[if mso]><xml><o:OfficeDocumentSettings><o:PixelsPerInch>96</o:PixelsPerInch></o:OfficeDocumentSettings></xml><![endif]-->`)
	w(`<style>`, darkStyle(dark), `</style></head>`)
	w(`<body class="nd-page" style="margin: 0; padding: 0; width: 100%; background-color: `, l.Page, `;">`)
	// The preheader: the inbox line after the subject, hidden in the mail,
	// with spacing after it so clients don't run the body's text into it
	if m.Preheader != "" {
		w(`<div class="nd-preheader" style="display: none; font-size: 1px; line-height: 1px; max-height: 0; max-width: 0; opacity: 0; overflow: hidden; mso-hide: all;">`, esc(oneLine(m.Preheader)), strings.Repeat("&#847;&zwnj;&nbsp;", 40), `</div>`)
	}
	w(`<table role="presentation" class="nd-page" width="100%" cellspacing="0" cellpadding="0" border="0" bgcolor="`, l.Page, `" style="width: 100%; background-color: `, l.Page, `;"><tr><td align="center" style="padding: 0 16px;">`)
	w(`<!--[if mso]><table role="presentation" width="600" align="center" cellspacing="0" cellpadding="0" border="0"><tr><td><![endif]-->`)
	w(`<table role="presentation" class="nd-page" dir="`, dir, `"`, langAttr, ` width="100%" cellspacing="0" cellpadding="0" border="0" style="width: 100%; max-width: 600px; margin: 0 auto;">`)

	// What a client shows for a logo it doesn't load: its alt text in the
	// mail's font
	altStyle := font + " font-size: 19px; line-height: 1.2; font-weight: 700; color: " + l.Ink + ";"
	// The header: the logo, with a rule below
	w(`<tr><td class="nd-rule" style="padding: 28px 0 20px 0; border-bottom: 1px solid `, l.Rule, `; text-align: `, start, `;">`)
	switch kindOf(b) {
	case logoOwn:
		lw, lh := ownLogoSize(b)
		// On a white plate in both modes: the mail can't know whether the
		// logo reads on dark
		w(`<table role="presentation" cellspacing="0" cellpadding="0" border="0" style="margin-`, start, `: -6px;"><tr><td bgcolor="#ffffff" style="background-color: #ffffff; padding: 6px; border-radius: 4px;">`)
		w(`<img src="cid:`, cidLogoOwn, `" alt="`, esc(b.Name()), `" width="`, strconv.Itoa(lw), `" height="`, strconv.Itoa(lh), `" style="display: block; width: `, strconv.Itoa(lw), `px; height: `, strconv.Itoa(lh), `px; border: 0; `, altStyle, `">`)
		w(`</td></tr></table>`)
	case logoNeoDesk:
		w(`<img class="nd-light" src="cid:`, cidLogoLight, `" alt="NeoDesk" width="152" height="48" style="display: block; width: 152px; height: 48px; margin: -8px; border: 0; `, altStyle, `">`)
		w(`<!--[if !mso]><!--><img class="nd-dark nd-dark-logo" src="cid:`, cidLogoDark, `" alt="NeoDesk" width="136" height="32" style="display: none; width: 0; height: 0; max-height: 0; overflow: hidden; border: 0;"><!--<![endif]-->`)
	default:
		gap := `padding: 0 10px 0 0;`
		if dir == "rtl" {
			gap = `padding: 0 0 0 10px;`
		}
		w(`<table role="presentation" cellspacing="0" cellpadding="0" border="0"><tr><td valign="middle" style="`, gap, `">`)
		w(`<img class="nd-light" src="cid:`, cidLogoLight, `" alt="" width="32" height="32" style="display: block; width: 32px; height: 32px; border: 0;">`)
		w(`<!--[if !mso]><!--><img class="nd-dark nd-dark-mark" src="cid:`, cidLogoDark, `" alt="" width="32" height="32" style="display: none; width: 0; height: 0; max-height: 0; overflow: hidden; border: 0;"><!--<![endif]-->`)
		w(`</td><td valign="middle" class="nd-ink" style="`, font, ` font-size: 19px; line-height: 1.2; font-weight: 700; color: `, l.Ink, `;">`, esc(b.Name()), `</td></tr></table>`)
	}
	w(`</td></tr>`)

	row("28px 0 0 0", `<h1 class="nd-ink nd-h1" style="margin: 0; `, font, ` font-size: 24px; line-height: 1.3; font-weight: 700; color: `, l.Ink, `;">`, esc(m.Heading), `</h1>`)
	if m.Lead != "" {
		row("12px 0 0 0", para("nd-ink", body, esc(m.Lead)))
	}

	if len(m.Facts) > 0 {
		w(`<tr><td style="padding: 20px 0 0 0;"><table class="nd-facts" width="100%" cellspacing="0" cellpadding="0" border="0" style="width: 100%; border-collapse: collapse;">`)
		for _, f := range m.Facts {
			rule := `border-top: 1px solid ` + l.Rule + `;`
			w(`<tr><th scope="row" class="nd-muted nd-rule" valign="top" style="width: 34%; padding: 8px 12px 8px 0; `, rule, ` text-align: `, start, `; vertical-align: top; `, font, ` font-size: 14px; line-height: 1.5; font-weight: 600; color: `, l.Muted, `;">`, esc(f.Label), `</th>`)
			w(`<td class="nd-ink nd-rule" valign="top" style="padding: 8px 0; `, rule, ` text-align: `, start, `; vertical-align: top; `, font, ` font-size: 15px; line-height: 1.5; color: `, l.Ink, `; white-space: pre-wrap; word-break: break-word;">`, esc(f.Value), `</td></tr>`)
		}
		w(`</table></td></tr>`)
	}

	if m.Quote != "" {
		w(`<tr><td style="padding: 16px 0 0 0;">`)
		if m.QuoteLabel != "" {
			w(para("nd-muted", small+" margin-bottom: 4px;", esc(m.QuoteLabel)))
		}
		w(`<blockquote class="nd-quote nd-ink" style="margin: 0; padding: 10px 14px; border-`, start, `: 3px solid `, l.Button, `; background-color: `, l.Quote, `; `, font, ` font-size: 15px; line-height: 1.5; color: `, l.Ink, `; white-space: pre-wrap;">`, esc(m.Quote), `</blockquote></td></tr>`)
	}

	// The one button: VML for Outlook for Windows, a link padded inside a
	// cell in the accent colour for everyone else. 48 pixels high.
	if m.Button != "" && m.Link != "" {
		width := 48 + 9*utf8.RuneCountInString(m.Button)
		if width > 520 {
			width = 520
		}
		w(`<tr><td style="padding: 28px 0 0 0; text-align: `, start, `;">`)
		w(`<!--[if mso]><v:roundrect xmlns:v="urn:schemas-microsoft-com:vml" xmlns:w="urn:schemas-microsoft-com:office:word" href="`, link, `" style="height: 48px; v-text-anchor: middle; width: `, strconv.Itoa(width), `px;" arcsize="9%" strokecolor="`, l.Button, `" fillcolor="`, l.Button, `"><w:anchorlock/><center style="color: `, l.OnButton, `; font-family: 'Segoe UI', Arial, sans-serif; font-size: 16px; font-weight: 600;">`, esc(m.Button), `</center></v:roundrect><![endif]-->`)
		w(`<!--[if !mso]><!--><table role="presentation" cellspacing="0" cellpadding="0" border="0" style="display: inline-table;"><tr>`)
		w(`<td class="nd-btn" bgcolor="`, l.Button, `" style="border-radius: 4px; background-color: `, l.Button, `; border: 1px solid `, l.Button, `;">`)
		w(`<a class="nd-on nd-btn-a" href="`, link, `" target="_blank" style="display: inline-block; padding: 13px 24px; `, font, ` font-size: 16px; line-height: 20px; font-weight: 600; color: `, l.OnButton, `; text-decoration: none; border-radius: 4px;">`, esc(m.Button), `</a>`)
		w(`</td></tr></table><!--<![endif]-->`)
		w(`</td></tr>`)
		row("16px 0 0 0", para("nd-muted", small, esc(r.linkHint())+`<br><a class="nd-link" href="`+link+`" target="_blank" style="color: `+l.Link+`; text-decoration: underline; word-break: break-all;">`+link+`</a>`))
	}

	if len(m.Steps) > 0 {
		w(`<tr><td style="padding: 24px 0 0 0;"><table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0">`)
		for i, step := range m.Steps {
			gap := "padding: 0 12px 12px 0;"
			if dir == "rtl" {
				gap = "padding: 0 0 12px 12px;"
			}
			w(`<tr><td width="28" valign="top" style="width: 28px; `, gap, `">`)
			w(`<table role="presentation" cellspacing="0" cellpadding="0" border="0"><tr><td class="nd-step nd-on" align="center" width="26" height="26" bgcolor="`, l.Button, `" style="width: 26px; height: 26px; border-radius: 4px; background-color: `, l.Button, `; `, font, ` font-size: 14px; font-weight: 700; line-height: 26px; color: `, l.OnButton, `;">`, strconv.Itoa(i+1), `</td></tr></table></td>`)
			w(`<td class="nd-ink" valign="top" style="padding: 2px 0 12px 0; text-align: `, start, `; `, font, ` `, body, `">`, esc(step), `</td></tr>`)
		}
		w(`</table></td></tr>`)
	}

	// The code on a line of its own, so a double click or a long press
	// selects only the code
	if m.Code != "" {
		w(`<tr><td style="padding: 20px 0 0 0; text-align: `, start, `;">`)
		if m.CodeLead != "" {
			w(para("nd-muted", small, esc(m.CodeLead)))
		}
		w(`<p class="nd-ink" dir="ltr" style="margin: 6px 0 0 0; font-family: `, monoFontList, `; font-size: 22px; line-height: 1.3; font-weight: 600; letter-spacing: 0.06em; color: `, l.Ink, `; text-align: `, start, `;">`, esc(m.Code), `</p></td></tr>`)
	}

	for _, d := range m.Details {
		if strings.TrimSpace(d) != "" {
			row("16px 0 0 0", para("nd-muted", small, esc(d)))
		}
	}

	// The footer: which server sent it, who runs it, why, how to stop it
	foot := r.footer()
	footStyle := "font-size: 13px; line-height: 1.5; color: " + l.Muted + ";"
	linkStyle := "color: " + l.Muted + "; text-decoration: underline;"
	w(`<tr><td style="padding: 32px 0 0 0;"><table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0"><tr><td class="nd-rule" style="padding: 16px 0 32px 0; border-top: 1px solid `, l.Rule, `; text-align: `, start, `;">`)
	for i, line := range foot {
		margin := "margin: 0;"
		if i > 0 {
			margin = "margin: 6px 0 0 0;"
		}
		w(`<p class="nd-muted" style="`, margin, ` `, font, ` `, footStyle, `">`, withLinks(line, linkStyle), `</p>`)
	}
	if r.unsubscribe != "" {
		w(`<p class="nd-muted" style="margin: 6px 0 0 0; `, font, ` `, footStyle, `"><a class="nd-link" href="`, esc(r.unsubscribe), `" target="_blank" style="`, linkStyle, `">`, esc(r.unsubscribeText()), `</a></p>`)
	}
	w(`</td></tr></table></td></tr>`)

	w(`</table><!--[if mso]></td></tr></table><![endif]--></td></tr></table></body></html>`)
	return s.String()
}

// Preview is a rendered mail's HTML as a browser shows it on its own: the
// inline images as data addresses, and with dark set, the dark palette
// whatever the browser's own setting.
func Preview(m Built, dark bool) string {
	out := m.HTML
	for _, img := range m.Inline {
		out = strings.ReplaceAll(out, `"cid:`+img.ID+`"`, `"data:`+img.ContentType+`;base64,`+base64.StdEncoding.EncodeToString(img.Data)+`"`)
	}
	if dark {
		out = strings.Replace(out, "@media (prefers-color-scheme: dark) {", "@media all {", 1)
	} else {
		out = strings.Replace(out, "@media (prefers-color-scheme: dark) {", "@media not all {", 1)
	}
	return out
}
