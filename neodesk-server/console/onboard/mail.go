package onboard

import (
	"fmt"
	"html"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Translate gives the text of a language key with its placeholders
// filled, in the language the mail is written in.
type Translate func(key string, vars map[string]any) string

// Mail is an invite or quick support mail: a subject, a text part and the
// same as an HTML part.
type Mail struct {
	Subject string
	Text    string
	HTML    string
}

// MailFacts are what a mail says.
type MailFacts struct {
	// Kind is KindJoin or KindSupport.
	Kind string
	// Link is the code's link; Code the code as people read it.
	Link string
	Code string
	// Server is the console's name (branding), Sender the account's name,
	// Host the console's host name.
	Server string
	Sender string
	Host   string
	// Expires is when the link stops working, as the reader sees it.
	Expires string
	// Company is whose support helps the person (the server's name).
	Company string
	// Accent is the branding's accent colour (#rrggbb); anything else is
	// the default blue.
	Accent string
	// Logo is the address of the branding logo as a PNG, JPEG or GIF over
	// HTTPS; anything else leaves the logo out for the server's name.
	Logo string
}

// defaultAccent is the button colour without a usable branding colour.
const defaultAccent = "#1d4ed8"

var (
	hexColour = regexp.MustCompile(`^#([0-9a-fA-F]{6}|[0-9a-fA-F]{3})$`)
	rasterExt = regexp.MustCompile(`(?i)\.(png|jpe?g|gif)$`)
)

// mailAccent is the accent as #rrggbb in lower case, the default for
// anything that is not a colour.
func mailAccent(c string) string {
	c = strings.TrimSpace(c)
	if !hexColour.MatchString(c) {
		return defaultAccent
	}
	c = strings.ToLower(c)
	if len(c) == 4 {
		c = "#" + strings.Repeat(c[1:2], 2) + strings.Repeat(c[2:3], 2) + strings.Repeat(c[3:4], 2)
	}
	return c
}

// buttonText is the text colour on the accent: white where it reads
// (contrast 4.5:1 or more), else near black.
func buttonText(accent string) string {
	lum := 0.0
	for i, w := range []float64{0.2126, 0.7152, 0.0722} {
		v, _ := strconv.ParseUint(accent[1+2*i:3+2*i], 16, 8)
		c := float64(v) / 255
		if c <= 0.03928 {
			c /= 12.92
		} else {
			c = math.Pow((c+0.055)/1.055, 2.4)
		}
		lum += w * c
	}
	if 1.05/(lum+0.05) >= 4.5 {
		return "#ffffff"
	}
	return "#111827"
}

// mailLogo is the logo's address when a mail client can show it: an
// absolute HTTPS address of a PNG, JPEG or GIF (mail clients drop SVG and
// many block plain HTTP), else "".
func mailLogo(logo string) string {
	if strings.ContainsAny(logo, "\"'<> ") {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(logo))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || !rasterExt.MatchString(u.Path) {
		return ""
	}
	return u.String()
}

// BuildMail writes the mail of a code. An invite is the welcome mail:
// who invited the person to which server, one button (Set up NeoDesk),
// the three steps, the link for when the button does not work, until
// when it works and the code for a NeoDesk that is already installed. A
// quick support mail says who wants to help and what to do, with the same
// button. Both carry the server's logo or name and its accent colour, in
// a table layout with inline styles, as mail clients need it; nothing in
// them but the link opens anything. The keys are below onboard.mail in
// the language files.
func BuildMail(t Translate, f MailFacts) Mail {
	company := f.Company
	if company == "" {
		company = f.Server
	}
	vars := map[string]any{"server": f.Server, "sender": f.Sender, "code": f.Code, "expires": f.Expires, "host": f.Host, "company": company}
	var subject, heading, lead, button, expires, code string
	var steps []string
	if f.Kind == KindSupport {
		subject = t("onboard.mail.support_subject", vars)
		heading = t("onboard.mail.support_heading", vars)
		lead = t("onboard.mail.support_intro", vars) + " " + t("onboard.mail.support_steps", vars)
		button = t("onboard.mail.support_button", vars)
		expires = t("onboard.mail.support_expires", vars)
	} else {
		subject = t("onboard.mail.welcome_subject", vars)
		heading = subject
		lead = t("onboard.mail.welcome_lead", vars)
		button = t("onboard.mail.welcome_button", vars)
		steps = []string{t("onboard.mail.welcome_step_1", vars), t("onboard.mail.welcome_step_2", vars), t("onboard.mail.welcome_step_3", vars)}
		expires = t("onboard.mail.invite_expires", vars)
		code = t("onboard.mail.invite_code", vars)
	}
	linkHint := t("onboard.mail.welcome_link", vars)
	footer := t("onboard.mail.footer", vars)

	return Mail{
		Subject: subject,
		Text:    mailText(heading, lead, steps, button, f.Link, expires, code, footer),
		HTML:    mailHTML(f, heading, lead, steps, button, linkHint, expires, code, footer),
	}
}

// mailText is the plain text part.
func mailText(heading, lead string, steps []string, button, link, expires, code, footer string) string {
	lines := []string{heading, "", lead, ""}
	for i, step := range steps {
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, step))
	}
	if len(steps) > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, button+": "+link, "", expires)
	if code != "" {
		lines = append(lines, "", code)
	}
	lines = append(lines, "", "--", footer)
	return strings.Join(lines, "\n") + "\n"
}

// The mail's colours and fonts, inline on every element.
const (
	mailFont  = "font-family: -apple-system, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif;"
	mailInk   = "#1f2933"
	mailMuted = "#6b7280"
	mailRule  = "#e5e7eb"
	mailPage  = "#f4f5f7"
)

// mailHTML is the HTML part: one centred table of at most 560 pixels.
func mailHTML(f MailFacts, heading, lead string, steps []string, button, linkHint, expires, code, footer string) string {
	e := html.EscapeString
	accent := mailAccent(f.Accent)
	onAccent := buttonText(accent)
	link := e(f.Link)
	var b strings.Builder
	w := func(s ...string) {
		for _, part := range s {
			b.WriteString(part)
		}
	}
	cell := func(padding, style, content string) {
		w(`<tr><td style="padding: `, padding, `; `, mailFont, ` `, style, `">`, content, `</td></tr>`)
	}
	w(`<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="color-scheme" content="light"><title>`, e(heading), `</title></head>`)
	w(`<body style="margin: 0; padding: 0; background-color: `, mailPage, `;">`)
	w(`<table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0" bgcolor="`, mailPage, `" style="background-color: `, mailPage, `;"><tr><td align="center" style="padding: 24px 12px;">`)
	w(`<table role="presentation" width="560" cellspacing="0" cellpadding="0" border="0" bgcolor="#ffffff" style="width: 100%; max-width: 560px; background-color: #ffffff; border: 1px solid `, mailRule, `; border-radius: 8px;">`)

	// The server's logo, or its name in the accent colour
	if logo := mailLogo(f.Logo); logo != "" {
		w(`<tr><td style="padding: 24px 28px 0 28px;"><img src="`, e(logo), `" alt="`, e(f.Server), `" height="40" style="display: block; height: 40px; width: auto; max-width: 240px; border: 0;"></td></tr>`)
	} else {
		cell("24px 28px 0 28px", "font-size: 16px; font-weight: 700; color: "+accent+";", e(f.Server))
	}
	cell("16px 28px 0 28px", "font-size: 22px; line-height: 1.3; font-weight: 700; color: "+mailInk+";", e(heading))
	cell("12px 28px 0 28px", "font-size: 15px; line-height: 1.5; color: "+mailInk+";", e(lead))

	// One button: a cell in the accent colour, so Outlook draws it too
	w(`<tr><td style="padding: 24px 28px 0 28px;"><table role="presentation" cellspacing="0" cellpadding="0" border="0"><tr>`)
	w(`<td bgcolor="`, accent, `" style="border-radius: 6px; background-color: `, accent, `;">`)
	w(`<a href="`, link, `" target="_blank" style="display: inline-block; padding: 12px 24px; `, mailFont, ` font-size: 16px; font-weight: 600; color: `, onAccent, `; text-decoration: none; border-radius: 6px;">`, e(button), `</a>`)
	w(`</td></tr></table></td></tr>`)

	if len(steps) > 0 {
		w(`<tr><td style="padding: 24px 28px 0 28px;"><table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0">`)
		for i, step := range steps {
			w(`<tr><td width="36" valign="top" style="width: 36px; padding: 0 0 10px 0;">`)
			w(`<table role="presentation" cellspacing="0" cellpadding="0" border="0"><tr><td align="center" width="24" height="24" bgcolor="`, accent, `" style="width: 24px; height: 24px; border-radius: 12px; background-color: `, accent, `; `, mailFont, ` font-size: 13px; font-weight: 700; line-height: 24px; color: `, onAccent, `;">`, strconv.Itoa(i+1), `</td></tr></table></td>`)
			w(`<td valign="top" style="padding: 2px 0 10px 0; `, mailFont, ` font-size: 15px; line-height: 1.5; color: `, mailInk, `;">`, e(step), `</td></tr>`)
		}
		w(`</table></td></tr>`)
	}

	small := "font-size: 13px; line-height: 1.5; color: " + mailMuted + ";"
	cell("16px 28px 0 28px", small, e(linkHint)+`<br><a href="`+link+`" target="_blank" style="color: `+mailInk+`; word-break: break-all;">`+link+`</a>`)
	cell("12px 28px 0 28px", small, e(expires))
	if code != "" {
		cell("12px 28px 0 28px", small, e(code))
	}
	w(`<tr><td style="padding: 20px 28px 24px 28px;"><table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0"><tr><td style="border-top: 1px solid `, mailRule, `; padding-top: 12px; `, mailFont, ` font-size: 12px; line-height: 1.5; color: `, mailMuted, `;">`, e(footer), `</td></tr></table></td></tr>`)

	w(`</table></td></tr></table></body></html>`)
	return b.String()
}
