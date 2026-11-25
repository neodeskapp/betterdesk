// Package brandmail is the layout of the console's mails in the server's
// branding: its logo or name, its accent colour on the one button, in a
// table layout with inline styles as mail clients need it. The invite and
// quick support mails (onboard) and the chat's mail about a waiting
// person (routes/chat) use it.
package brandmail

import (
	"html"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/branding"
)

// Branding is what a mail shows of the server.
type Branding struct {
	// Server is the console's name (branding), shown when there is no logo
	Server string
	// Accent is the branding's accent colour (#rrggbb); anything else is
	// the default blue.
	Accent string
	// Logo is the address of the branding logo as a PNG, JPEG or GIF over
	// HTTPS; anything else leaves the logo out for the server's name.
	Logo string
}

// Page is one mail's HTML part, from top to bottom. Empty parts are left
// out.
type Page struct {
	Branding
	Heading string
	Lead    string
	// Quote is a text the mail quotes, set off below the lead
	Quote string
	// Button is the one button's text, Link where it goes
	Button string
	Link   string
	// Steps are numbered below the button
	Steps []string
	// LinkHint is said above the link written out, for when the button
	// doesn't work
	LinkHint string
	// Small are short lines in small type
	Small  []string
	Footer string
}

// DefaultAccent is the button colour without a usable branding colour.
const DefaultAccent = "#1d4ed8"

var (
	hexColour = regexp.MustCompile(`^#([0-9a-fA-F]{6}|[0-9a-fA-F]{3})$`)
	rasterExt = regexp.MustCompile(`(?i)\.(png|jpe?g|gif)$`)
)

// Accent is the accent as #rrggbb in lower case, the default for
// anything that is not a colour.
func Accent(c string) string {
	c = strings.TrimSpace(c)
	if !hexColour.MatchString(c) {
		return DefaultAccent
	}
	c = strings.ToLower(c)
	if len(c) == 4 {
		c = "#" + strings.Repeat(c[1:2], 2) + strings.Repeat(c[2:3], 2) + strings.Repeat(c[3:4], 2)
	}
	return c
}

// OnAccent is the text colour on the accent: white where it reads
// (contrast 4.5:1 or more), else near black.
func OnAccent(accent string) string {
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

// Logo is the logo's address when a mail client can show it: an absolute
// HTTPS address of a PNG, JPEG or GIF (mail clients drop SVG and many
// block plain HTTP), else "".
func Logo(logo string) string {
	if strings.ContainsAny(logo, "\"'<> ") {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(logo))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || !rasterExt.MatchString(u.Path) {
		return ""
	}
	return u.String()
}

// Of is the branding of the console's mails: its name, the accent colour
// of its appearance and its logo when the logo is an image, made absolute
// with base (the console's address) when it is a path. Without a
// branding service it is "NeoDesk" in the default colours.
func Of(b *branding.Service, base string) Branding {
	out := Branding{Server: "NeoDesk"}
	if b == nil {
		return out
	}
	out.Server = b.AppName("NeoDesk")
	appearance, err := b.GetPublicAppearance()
	if err != nil || appearance == nil {
		return out
	}
	text := func(section, key string) string {
		obj, _ := appearance.Get(section)
		o, ok := obj.(*branding.Object)
		if !ok {
			return ""
		}
		v, _ := o.Get(key)
		s, _ := v.(string)
		return strings.TrimSpace(s)
	}
	out.Accent = text("palette", "primary")
	if text("identity", "logoType") == "image" {
		out.Logo = text("identity", "logoUrl")
		if strings.HasPrefix(out.Logo, "/") && !strings.HasPrefix(out.Logo, "//") {
			out.Logo = strings.TrimRight(base, "/") + out.Logo
		}
	}
	return out
}

// The mail's colours and fonts, inline on every element.
const (
	mailFont  = "font-family: -apple-system, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif;"
	mailInk   = "#1f2933"
	mailMuted = "#6b7280"
	mailRule  = "#e5e7eb"
	mailPage  = "#f4f5f7"
)

// HTML is the page as one centred table of at most 560 pixels.
func HTML(p Page) string {
	e := html.EscapeString
	accent := Accent(p.Accent)
	onAccent := OnAccent(accent)
	link := e(p.Link)
	var b strings.Builder
	w := func(s ...string) {
		for _, part := range s {
			b.WriteString(part)
		}
	}
	cell := func(padding, style, content string) {
		w(`<tr><td style="padding: `, padding, `; `, mailFont, ` `, style, `">`, content, `</td></tr>`)
	}
	w(`<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="color-scheme" content="light"><title>`, e(p.Heading), `</title></head>`)
	w(`<body style="margin: 0; padding: 0; background-color: `, mailPage, `;">`)
	w(`<table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0" bgcolor="`, mailPage, `" style="background-color: `, mailPage, `;"><tr><td align="center" style="padding: 24px 12px;">`)
	w(`<table role="presentation" width="560" cellspacing="0" cellpadding="0" border="0" bgcolor="#ffffff" style="width: 100%; max-width: 560px; background-color: #ffffff; border: 1px solid `, mailRule, `; border-radius: 8px;">`)

	// The server's logo, or its name in the accent colour
	if logo := Logo(p.Logo); logo != "" {
		w(`<tr><td style="padding: 24px 28px 0 28px;"><img src="`, e(logo), `" alt="`, e(p.Server), `" height="40" style="display: block; height: 40px; width: auto; max-width: 240px; border: 0;"></td></tr>`)
	} else {
		cell("24px 28px 0 28px", "font-size: 16px; font-weight: 700; color: "+accent+";", e(p.Server))
	}
	cell("16px 28px 0 28px", "font-size: 22px; line-height: 1.3; font-weight: 700; color: "+mailInk+";", e(p.Heading))
	cell("12px 28px 0 28px", "font-size: 15px; line-height: 1.5; color: "+mailInk+";", e(p.Lead))
	if p.Quote != "" {
		cell("12px 28px 0 28px", "font-size: 15px; line-height: 1.5; color: "+mailInk+";",
			`<div style="padding: 8px 12px; border-left: 3px solid `+accent+`; background-color: `+mailPage+`;">`+e(p.Quote)+`</div>`)
	}

	// One button: a cell in the accent colour, so Outlook draws it too
	if p.Button != "" {
		w(`<tr><td style="padding: 24px 28px 0 28px;"><table role="presentation" cellspacing="0" cellpadding="0" border="0"><tr>`)
		w(`<td bgcolor="`, accent, `" style="border-radius: 6px; background-color: `, accent, `;">`)
		w(`<a href="`, link, `" target="_blank" style="display: inline-block; padding: 12px 24px; `, mailFont, ` font-size: 16px; font-weight: 600; color: `, onAccent, `; text-decoration: none; border-radius: 6px;">`, e(p.Button), `</a>`)
		w(`</td></tr></table></td></tr>`)
	}

	if len(p.Steps) > 0 {
		w(`<tr><td style="padding: 24px 28px 0 28px;"><table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0">`)
		for i, step := range p.Steps {
			w(`<tr><td width="36" valign="top" style="width: 36px; padding: 0 0 10px 0;">`)
			w(`<table role="presentation" cellspacing="0" cellpadding="0" border="0"><tr><td align="center" width="24" height="24" bgcolor="`, accent, `" style="width: 24px; height: 24px; border-radius: 12px; background-color: `, accent, `; `, mailFont, ` font-size: 13px; font-weight: 700; line-height: 24px; color: `, onAccent, `;">`, strconv.Itoa(i+1), `</td></tr></table></td>`)
			w(`<td valign="top" style="padding: 2px 0 10px 0; `, mailFont, ` font-size: 15px; line-height: 1.5; color: `, mailInk, `;">`, e(step), `</td></tr>`)
		}
		w(`</table></td></tr>`)
	}

	small := "font-size: 13px; line-height: 1.5; color: " + mailMuted + ";"
	if p.LinkHint != "" {
		cell("16px 28px 0 28px", small, e(p.LinkHint)+`<br><a href="`+link+`" target="_blank" style="color: `+mailInk+`; word-break: break-all;">`+link+`</a>`)
	}
	for _, line := range p.Small {
		if line != "" {
			cell("12px 28px 0 28px", small, e(line))
		}
	}
	if p.Footer != "" {
		w(`<tr><td style="padding: 20px 28px 24px 28px;"><table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0"><tr><td style="border-top: 1px solid `, mailRule, `; padding-top: 12px; `, mailFont, ` font-size: 12px; line-height: 1.5; color: `, mailMuted, `;">`, e(p.Footer), `</td></tr></table></td></tr>`)
	} else {
		w(`<tr><td style="padding: 0 0 24px 0;"></td></tr>`)
	}

	w(`</table></td></tr></table></body></html>`)
	return b.String()
}
