// Package brandmail is the layout of the console's mails in the server's
// branding: its logo (NeoDesk's own, carried in the mail as inline images,
// unless the server has a logo image of its own), its accent colour on the
// one button, in a table layout with inline styles as mail clients need
// it, and in the console's dark theme for clients in dark mode. The invite and
// quick support mails (onboard), the password links (passwordlinks), the
// chat's mail about a waiting person (routes/chat) and the alert and help
// request mails (mail) use it.
//
// The page says its language and direction, its heading is a heading and
// its text paragraphs, and the button's text reaches 4.5:1 on the button
// for any accent (WCAG 2.2 AA).
package brandmail

import (
	"bytes"
	"embed"
	"html"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/branding"
	"github.com/neodeskapp/neodesk/neodesk-server/console/i18n"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail/message"
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
	// Lang is the language the texts are in, a code of the language files
	// ("de", "ar"). It goes on the page with its direction, right to left
	// for Arabic and Hebrew.
	Lang    string
	Heading string
	Lead    string
	// Facts are label and value rows below the lead, such as an alert's
	// severity and device: a table with the labels as row headers.
	Facts []Fact
	// Quote is a text the mail quotes, set off below the lead, with
	// QuoteLabel ("They wrote:") above it
	Quote      string
	QuoteLabel string
	// Button is the one button's text, Link where it goes
	Button string
	Link   string
	// Steps are numbered below the button
	Steps []string
	// LinkHint is said above the link written out, for when the button
	// doesn't work
	LinkHint string
	// Small are short lines in small type
	Small []string
	// Code is a code the reader types, on a line of its own in a monospace
	// font below CodeLead, which says what to do with it
	CodeLead string
	Code     string
	Footer   string
}

// Fact is one row of Page.Facts.
type Fact struct{ Label, Value string }

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

// The two text colours a button can have.
const (
	white     = "#ffffff"
	nearBlack = "#111827"
)

func rgbOf(c string) [3]float64 {
	var out [3]float64
	for i := range out {
		v, _ := strconv.ParseUint(c[1+2*i:3+2*i], 16, 8)
		out[i] = float64(v)
	}
	return out
}

func luminance(c string) float64 {
	lum := 0.0
	for i, w := range []float64{0.2126, 0.7152, 0.0722} {
		v := rgbOf(c)[i] / 255
		if v <= 0.03928 {
			v /= 12.92
		} else {
			v = math.Pow((v+0.055)/1.055, 2.4)
		}
		lum += w * v
	}
	return lum
}

// Contrast is the WCAG contrast ratio of two #rrggbb colours.
func Contrast(a, b string) float64 {
	x, y := luminance(a), luminance(b)
	if x < y {
		x, y = y, x
	}
	return (x + 0.05) / (y + 0.05)
}

// OnAccent is the text colour on the accent: white or near black,
// whichever has the higher contrast on it.
func OnAccent(accent string) string {
	if Contrast(white, accent) >= Contrast(nearBlack, accent) {
		return white
	}
	return nearBlack
}

// Button is the button's colour and its text colour for an accent: the
// accent with OnAccent on it where that reaches 4.5:1, else the accent
// made darker, step by step, until white on it does. A mid grey such as
// #7a7a7a reaches 4.29:1 with white and 4.13:1 with near black; its
// button becomes #767676.
func Button(accent string) (bg, fg string) {
	bg = accent
	if on := OnAccent(bg); Contrast(on, bg) >= 4.5 {
		return bg, on
	}
	rgb := rgbOf(accent)
	for f := 0.99; f > 0; f -= 0.01 {
		bg = "#"
		for _, v := range rgb {
			bg += strconv.FormatUint(uint64(math.Round(v*f))|0x100, 16)[1:]
		}
		if Contrast(white, bg) >= 4.5 {
			return bg, white
		}
	}
	return "#000000", white
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

// When is a time as a mail says it: in the server's time zone with the
// zone named and UTC after it ("2026-12-03 16:04 CET (15:04 UTC)"), or in
// UTC alone where the server runs on UTC. The reader doesn't have to know
// which zone the server is in.
func When(t time.Time) string {
	utc := t.UTC()
	local := t.In(time.Local)
	name, offset := local.Zone()
	if offset == 0 && (name == "UTC" || name == "GMT" || name == "") {
		return utc.Format("2006-01-02 15:04") + " UTC"
	}
	if name == "" {
		name = local.Format("-07:00")
	}
	inUTC := utc.Format("15:04")
	if utc.Format("2006-01-02") != local.Format("2006-01-02") {
		inUTC = utc.Format("2006-01-02 15:04")
	}
	return local.Format("2006-01-02 15:04") + " " + name + " (" + inUTC + " UTC)"
}

// The mail's two palettes: the console's light theme, and its dark theme
// for clients that follow the reader's dark mode (Apple Mail, Outlook on
// the web, iOS and macOS, Thunderbird). Clients without it show the light
// one, which is also what the inline styles hold.
type palette struct {
	page, card, rule, ink, muted, quote string
}

var (
	lightPalette = palette{page: "#f3f4f4", card: "#ffffff", rule: "#dde1e2", ink: "#1b1e1f", muted: "#566064", quote: "#f3f4f4"}
	darkPalette  = palette{page: "#0b0d0d", card: "#141616", rule: "#2a2f30", ink: "#e8eaeb", muted: "#a0a8ab", quote: "#1d2021"}
)

// The console's accent in each theme, with its text colour; a mail
// without an accent of its own uses them.
const (
	brandLight   = "#1b62cf"
	brandDark    = "#58a6ff"
	onBrandDark  = "#0c1b1e"
	mailFontList = "'IBM Plex Sans', -apple-system, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif"
	monoFontList = "'IBM Plex Mono', ui-monospace, Menlo, Consolas, monospace"
)

// defaultAccents are the accent colours of the console's own themes: an
// appearance with one of them has no accent of its own.
var defaultAccents = map[string]bool{"#58a6ff": true, "#1b62cf": true, DefaultAccent: true}

// accents is the button colour and its text colour in light and in dark
// mode: the branding's own accent in both, made readable by Button, else
// the console's.
func accents(c string) (light, onLight, dark, onDark string) {
	a := Accent(c)
	if strings.TrimSpace(c) == "" || defaultAccents[a] {
		return brandLight, OnAccent(brandLight), brandDark, onBrandDark
	}
	bg, fg := Button(a)
	return bg, fg, bg, fg
}

//go:embed logo/*.png
var logoFiles embed.FS

// The logo images a mail carries, by Content-ID.
const (
	cidLogoLight = "logo-light@neodesk"
	cidLogoDark  = "logo-dark@neodesk"
)

// ownLogo reports whether the mail shows NeoDesk's logo: no logo image of
// the server's own. ownName says the server keeps the name NeoDesk, which
// gets the full logo; another name gets the mark followed by the name, as
// the console's top bar has it.
func ownLogo(p Page) (own, ownName bool) {
	if Logo(p.Logo) != "" {
		return false, false
	}
	name := strings.TrimSpace(p.Server)
	return true, name == "" || strings.EqualFold(name, "neodesk")
}

func readLogo(name string) []byte {
	data, err := logoFiles.ReadFile(name)
	if err != nil {
		panic("brandmail: embedded logo missing: " + name)
	}
	return data
}

// The light logo's plate: 8 pixels of white around it on screen (16 in
// the image, which is drawn at twice the size), with rounded corners.
const (
	platePad    = 16
	plateRadius = 12
)

var (
	plateOnce sync.Once
	plateData []byte
)

// platedLogo is NeoDesk's light logo on an opaque white plate. The light
// logo has a dark wordmark on a transparent ground, which disappears in
// clients that turn the mail dark by inverting its colours but leave its
// images as they are (the Gmail apps, Outlook for Windows). On the plate
// it stays readable there, and in a light mail the plate is the card's own
// white. The artwork is drawn on the plate pixel for pixel, not changed.
func platedLogo() []byte {
	plateOnce.Do(func() {
		art, err := png.Decode(bytes.NewReader(readLogo("logo/logo-on-light-64h.png")))
		if err != nil {
			panic("brandmail: embedded logo is no PNG")
		}
		b := art.Bounds()
		w, h := b.Dx()+2*platePad, b.Dy()+2*platePad
		out := image.NewNRGBA(image.Rect(0, 0, w, h))
		r := float64(plateRadius)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				// The distance into a corner's rounding, for its edge
				cx := math.Max(math.Max(r-float64(x)-0.5, float64(x)+0.5-float64(w)+r), 0)
				cy := math.Max(math.Max(r-float64(y)-0.5, float64(y)+0.5-float64(h)+r), 0)
				a := math.Min(math.Max(r-math.Hypot(cx, cy)+0.5, 0), 1)
				if cx == 0 || cy == 0 {
					a = 1
				}
				out.SetNRGBA(x, y, color.NRGBA{255, 255, 255, uint8(math.Round(a * 255))})
			}
		}
		draw.Draw(out, b.Sub(b.Min).Add(image.Pt(platePad, platePad)), art, b.Min, draw.Over)
		var buf bytes.Buffer
		if err := png.Encode(&buf, out); err != nil {
			panic("brandmail: " + err.Error())
		}
		plateData = buf.Bytes()
	})
	return plateData
}

// Inline are the images the page's HTML shows by Content-ID: NeoDesk's
// logo for light mode on its plate and for dark mode, or its mark when the
// server has a name of its own. A server with a logo image of its own
// needs none. Pass them with the HTML (mail.Email.Inline), or the logo is
// missing.
func Inline(p Page) []message.Inline {
	own, ownName := ownLogo(p)
	if !own {
		return nil
	}
	light, dark := platedLogo(), readLogo("logo/logo-on-dark-64h.png")
	if !ownName {
		light, dark = readLogo("logo/mark-on-light-64.png"), readLogo("logo/mark-on-dark-64.png")
	}
	return []message.Inline{
		{ID: cidLogoLight, ContentType: "image/png", Filename: "neodesk-logo.png", Data: light},
		{ID: cidLogoDark, ContentType: "image/png", Filename: "neodesk-logo-dark.png", Data: dark},
	}
}

// darkStyle is the head's style sheet: the dark palette for clients that
// follow dark mode, also in the form Outlook on the web reads ([data-ogsc]
// for text, [data-ogsb] for backgrounds), and the swap of the logos.
func darkStyle(accent, onAccent string) string {
	d := darkPalette
	rules := func(prefixText, prefixBg string) string {
		return prefixBg + `.nd-page { background-color: ` + d.page + ` !important; }
` + prefixBg + `.nd-card { background-color: ` + d.card + ` !important; border-color: ` + d.rule + ` !important; }
` + prefixBg + `.nd-quote { background-color: ` + d.quote + ` !important; }
` + prefixBg + `.nd-btn, ` + prefixBg + `.nd-step { background-color: ` + accent + ` !important; }
` + prefixText + `.nd-ink { color: ` + d.ink + ` !important; }
` + prefixText + `.nd-muted { color: ` + d.muted + ` !important; }
` + prefixText + `.nd-name { color: ` + d.ink + ` !important; }
` + prefixText + `.nd-on { color: ` + onAccent + ` !important; }
` + prefixBg + `.nd-rule { border-color: ` + d.rule + ` !important; }
` + prefixBg + `.nd-light { display: none !important; }
` + prefixBg + `.nd-dark { display: block !important; max-height: none !important; overflow: visible !important; }
` + prefixBg + `.nd-dark-logo { width: 136px !important; height: 32px !important; }
` + prefixBg + `.nd-dark-mark { width: 32px !important; height: 32px !important; }
`
	}
	return `:root { color-scheme: light dark; supported-color-schemes: light dark; }
@media (prefers-color-scheme: dark) {
` + rules("", "") + `}
` + rules("[data-ogsc] ", "[data-ogsb] ")
}

// HTML is the page as one centred table of at most 560 pixels, in light
// and in dark mode.
func HTML(p Page) string {
	e := html.EscapeString
	accent, onAccent, darkAccent, onDarkAccent := accents(p.Accent)
	l := lightPalette
	font := "font-family: " + mailFontList + ";"
	link := e(p.Link)
	dir, start := "ltr", "left"
	if i18n.RTL(p.Lang) {
		dir, start = "rtl", "right"
	}
	langAttr := ""
	if p.Lang != "" {
		langAttr = ` lang="` + e(p.Lang) + `"`
	}
	var b strings.Builder
	w := func(s ...string) {
		for _, part := range s {
			b.WriteString(part)
		}
	}
	// para is a paragraph in its own row of the card
	para := func(padding, class, style, content string) {
		w(`<tr><td style="padding: `, padding, `;"><p class="`, class, `" style="margin: 0; `, font, ` `, style, `">`, content, `</p></td></tr>`)
	}
	w(`<!DOCTYPE html><html`, langAttr, ` dir="`, dir, `"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">`)
	w(`<meta name="color-scheme" content="light dark"><meta name="supported-color-schemes" content="light dark"><title>`, e(p.Heading), `</title>`)
	w(`<style>`, darkStyle(darkAccent, onDarkAccent), `</style></head>`)
	w(`<body class="nd-page" style="margin: 0; padding: 0; background-color: `, l.page, `;">`)
	w(`<table role="presentation" class="nd-page" width="100%" cellspacing="0" cellpadding="0" border="0" bgcolor="`, l.page, `" style="background-color: `, l.page, `;"><tr><td align="center" style="padding: 24px 12px;">`)
	// dir and lang again on the card, for clients that drop the <html> element
	w(`<table role="presentation" class="nd-card" dir="`, dir, `"`, langAttr, ` width="560" cellspacing="0" cellpadding="0" border="0" bgcolor="`, l.card, `" style="width: 100%; max-width: 560px; background-color: `, l.card, `; border: 1px solid `, l.rule, `; border-radius: 8px;">`)

	// The logo: the server's own image, else NeoDesk's in a light and a
	// dark version (the dark one hidden unless the client is in dark mode,
	// and never in Outlook for Windows), with the server's name after the
	// mark when it has one of its own. The light logo has a plate of 8
	// pixels, so its cell is 8 pixels smaller on each side.
	own, ownName := ownLogo(p)
	switch {
	case !own:
		w(`<tr><td style="padding: 24px 28px 0 28px;"><img src="`, e(Logo(p.Logo)), `" alt="`, e(p.Server), `" height="40" style="display: block; height: 40px; width: auto; max-width: 240px; border: 0;"></td></tr>`)
	case ownName:
		w(`<tr><td style="padding: 16px 20px 0 20px;">`)
		w(`<img class="nd-light" src="cid:`, cidLogoLight, `" alt="NeoDesk" width="152" height="48" style="display: block; width: 152px; height: 48px; border: 0;">`)
		w(`<!--[if !mso]><!--><img class="nd-dark nd-dark-logo" src="cid:`, cidLogoDark, `" alt="NeoDesk" width="136" height="32" style="display: none; width: 0; height: 0; max-height: 0; overflow: hidden; margin: 8px; border: 0;"><!--<![endif]-->`)
		w(`</td></tr>`)
	default:
		w(`<tr><td style="padding: 24px 28px 0 28px;"><table role="presentation" cellspacing="0" cellpadding="0" border="0"><tr><td valign="middle" style="padding: 0 `)
		if dir == "rtl" {
			w(`0 0 10px;">`)
		} else {
			w(`10px 0 0;">`)
		}
		w(`<img class="nd-light" src="cid:`, cidLogoLight, `" alt="" width="32" height="32" style="display: block; width: 32px; height: 32px; border: 0;">`)
		w(`<!--[if !mso]><!--><img class="nd-dark nd-dark-mark" src="cid:`, cidLogoDark, `" alt="" width="32" height="32" style="display: none; width: 0; height: 0; max-height: 0; overflow: hidden; border: 0;"><!--<![endif]-->`)
		w(`</td><td valign="middle" class="nd-name" style="`, font, ` font-size: 18px; font-weight: 700; color: `, l.ink, `;">`, e(p.Server), `</td></tr></table></td></tr>`)
	}
	w(`<tr><td style="padding: 20px 28px 0 28px;"><h1 class="nd-ink" style="margin: 0; `, font, ` font-size: 22px; line-height: 1.3; font-weight: 700; color: `, l.ink, `;">`, e(p.Heading), `</h1></td></tr>`)
	if p.Lead != "" {
		para("12px 28px 0 28px", "nd-ink", "font-size: 15px; line-height: 1.5; color: "+l.ink+";", e(p.Lead))
	}

	// Label and value rows: a data table, its labels row headers
	if len(p.Facts) > 0 {
		w(`<tr><td style="padding: 16px 28px 0 28px;"><table class="nd-facts" width="100%" cellspacing="0" cellpadding="0" border="0" style="width: 100%; border-collapse: collapse;">`)
		for _, f := range p.Facts {
			rule := `border-top: 1px solid ` + l.rule + `;`
			w(`<tr><th scope="row" class="nd-muted nd-rule" valign="top" style="width: 34%; padding: 8px 12px 8px 0; `, rule, ` text-align: `, start, `; vertical-align: top; `, font, ` font-size: 14px; line-height: 1.5; font-weight: 600; color: `, l.muted, `;">`, e(f.Label), `</th>`)
			w(`<td class="nd-ink nd-rule" valign="top" style="padding: 8px 0; `, rule, ` text-align: `, start, `; vertical-align: top; `, font, ` font-size: 15px; line-height: 1.5; color: `, l.ink, `; white-space: pre-wrap; word-break: break-word;">`, e(f.Value), `</td></tr>`)
		}
		w(`</table></td></tr>`)
	}

	if p.Quote != "" {
		w(`<tr><td style="padding: 12px 28px 0 28px;">`)
		if p.QuoteLabel != "" {
			w(`<p class="nd-muted" style="margin: 0 0 4px 0; `, font, ` font-size: 13px; line-height: 1.5; color: `, l.muted, `;">`, e(p.QuoteLabel), `</p>`)
		}
		w(`<blockquote class="nd-quote nd-ink" style="margin: 0; padding: 8px 12px; border-`, start, `: 3px solid `, accent, `; background-color: `, l.quote, `; `, font, ` font-size: 15px; line-height: 1.5; color: `, l.ink, `;">`, e(p.Quote), `</blockquote></td></tr>`)
	}

	// One button: a cell in the accent colour, so Outlook draws it too. 14
	// pixels above and below a 20 pixel line make it 48 pixels high.
	if p.Button != "" {
		w(`<tr><td style="padding: 24px 28px 0 28px;"><table role="presentation" cellspacing="0" cellpadding="0" border="0"><tr>`)
		w(`<td class="nd-btn" bgcolor="`, accent, `" style="border-radius: 6px; background-color: `, accent, `;">`)
		w(`<a class="nd-on" href="`, link, `" target="_blank" style="display: inline-block; padding: 14px 24px; `, font, ` font-size: 16px; line-height: 20px; font-weight: 600; color: `, onAccent, `; text-decoration: none; border-radius: 6px;">`, e(p.Button), `</a>`)
		w(`</td></tr></table></td></tr>`)
	}

	if len(p.Steps) > 0 {
		w(`<tr><td style="padding: 24px 28px 0 28px;"><table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0">`)
		for i, step := range p.Steps {
			w(`<tr><td width="36" valign="top" style="width: 36px; padding: 0 0 10px 0;">`)
			w(`<table role="presentation" cellspacing="0" cellpadding="0" border="0"><tr><td class="nd-step nd-on" align="center" width="24" height="24" bgcolor="`, accent, `" style="width: 24px; height: 24px; border-radius: 12px; background-color: `, accent, `; `, font, ` font-size: 13px; font-weight: 700; line-height: 24px; color: `, onAccent, `;">`, strconv.Itoa(i+1), `</td></tr></table></td>`)
			w(`<td class="nd-ink" valign="top" style="padding: 2px 0 10px 0; `, font, ` font-size: 15px; line-height: 1.5; color: `, l.ink, `;">`, e(step), `</td></tr>`)
		}
		w(`</table></td></tr>`)
	}

	small := "font-size: 13px; line-height: 1.5; color: " + l.muted + ";"
	if p.LinkHint != "" {
		para("16px 28px 0 28px", "nd-muted", small, e(p.LinkHint)+`<br><a class="nd-ink" href="`+link+`" target="_blank" style="color: `+l.ink+`; word-break: break-all;">`+link+`</a>`)
	}
	for _, line := range p.Small {
		if line != "" {
			para("12px 28px 0 28px", "nd-muted", small, e(line))
		}
	}
	// The code on a line of its own, so a double click or a long press
	// selects only the code
	if p.Code != "" {
		w(`<tr><td style="padding: 12px 28px 0 28px;">`)
		if p.CodeLead != "" {
			w(`<p class="nd-muted" style="margin: 0; `, font, ` `, small, `">`, e(p.CodeLead), `</p>`)
		}
		w(`<p class="nd-ink" style="margin: 4px 0 0 0; font-family: `, monoFontList, `; font-size: 18px; line-height: 1.4; font-weight: 600; letter-spacing: 0.04em; color: `, l.ink, `;" dir="ltr">`, e(p.Code), `</p></td></tr>`)
	}
	if p.Footer != "" {
		w(`<tr><td style="padding: 20px 28px 24px 28px;"><table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0"><tr><td class="nd-rule" style="border-top: 1px solid `, l.rule, `; padding-top: 12px;"><p class="nd-muted" style="margin: 0; `, font, ` font-size: 12px; line-height: 1.5; color: `, l.muted, `;">`, e(p.Footer), `</p></td></tr></table></td></tr>`)
	} else {
		w(`<tr><td style="padding: 0 0 24px 0;"></td></tr>`)
	}

	w(`</table></td></tr></table></body></html>`)
	return b.String()
}
