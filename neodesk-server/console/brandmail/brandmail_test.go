package brandmail

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"regexp"
	"strings"
	"testing"
)

var cids = regexp.MustCompile(`src="cid:([^"]+)"`)

// tr is a Translate that answers the key with its variables, so tests see
// which text went where.
func tr(key string, vars map[string]any) string {
	out := key
	for _, k := range []string{"server", "host", "organisation", "email", "url"} {
		if v, ok := vars[k]; ok {
			out += " " + k + "=" + v.(string)
		}
	}
	return out
}

func sample() Mail {
	return Mail{
		Kind: "sample", T: tr, Lang: "en",
		Subject: "Jana invited you to Acme IT", Preheader: "Set up the app in a few minutes",
		Heading: "Set up remote support", Lead: "Jana Weber from Acme IT invited you.",
		Button: "Set up NeoDesk", Link: "https://desk.acme.example/join/9H83-A6RJ",
		Steps: []string{"Download", "Install"}, CodeLead: "Or enter this code:", Code: "9H83-A6RJ",
		Details: []string{"The link works until 3 December 2026."}, Why: "You get this because Jana invited you.",
	}
}

func neodesk() Branding { return Branding{Server: "NeoDesk", Host: "internal.neode.sk"} }

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 0x80
	}
	img.Set(0, 0, color.Black)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// carried checks that the images a mail shows by Content-ID are exactly
// the ones it carries, each with alt text.
func carried(t *testing.T, built Built) {
	t.Helper()
	shown := map[string]bool{}
	for _, m := range cids.FindAllStringSubmatch(built.HTML, -1) {
		shown[m[1]] = true
	}
	if len(built.Inline) != len(shown) {
		t.Fatalf("HTML shows %v, the mail carries %d images", shown, len(built.Inline))
	}
	for _, img := range built.Inline {
		if !shown[img.ID] {
			t.Errorf("image %s carried but not shown", img.ID)
		}
	}
	for _, tag := range regexp.MustCompile(`<img [^>]*>`).FindAllString(built.HTML, -1) {
		if !strings.Contains(tag, ` alt="`) {
			t.Errorf("image without alt text: %s", tag)
		}
	}
}

// NeoDesk's full logo for a server named NeoDesk, the mark and the name
// for a server with a name of its own, and the uploaded logo, carried in
// the mail, for a server with one. An SVG upload can't be shown: the
// name stands in.
func TestLogo(t *testing.T) {
	own := Render(sample(), neodesk(), "")
	carried(t, own)
	if !strings.Contains(own.HTML, `class="nd-light" src="cid:logo-light@neodesk" alt="NeoDesk" width="152" height="48"`) ||
		!strings.Contains(own.HTML, `<!--[if !mso]><!--><img class="nd-dark nd-dark-logo" src="cid:logo-dark@neodesk"`) {
		t.Errorf("no full logo for light and dark:\n%s", own.HTML)
	}
	if !bytes.Equal(own.Inline[1].Data, mustRead(t, "logo/logo-on-dark-64h.png")) {
		t.Error("the dark logo isn't the brand's dark PNG")
	}

	named := Render(sample(), Branding{Server: "Acme IT"}, "")
	carried(t, named)
	if !strings.Contains(named.HTML, `>Acme IT</td>`) || !bytes.Equal(named.Inline[0].Data, mustRead(t, "logo/mark-on-light-64.png")) {
		t.Errorf("no mark and name:\n%s", named.HTML)
	}

	logo := pngOf(t, 400, 100)
	uploaded := Render(sample(), Branding{Server: "Acme IT", LogoImage: logo, LogoType: "image/png"}, "")
	carried(t, uploaded)
	if len(uploaded.Inline) != 1 || !bytes.Equal(uploaded.Inline[0].Data, logo) || uploaded.Inline[0].ContentType != "image/png" {
		t.Fatalf("the uploaded logo isn't carried as it is: %+v", uploaded.Inline)
	}
	if !strings.Contains(uploaded.HTML, `src="cid:logo@server" alt="Acme IT" width="160" height="40"`) {
		t.Errorf("the uploaded logo isn't shown 40 px high in its proportions:\n%s", uploaded.HTML)
	}
	if strings.Contains(uploaded.HTML, ">Acme IT</td>") {
		t.Error("the name is written out next to a logo")
	}

	svg := Render(sample(), Branding{Server: "Acme IT", LogoImage: []byte("<svg/>"), LogoType: "image/svg+xml"}, "")
	if !strings.Contains(svg.HTML, `>Acme IT</td>`) || strings.Contains(svg.HTML, "cid:logo@server") {
		t.Error("an SVG logo isn't replaced by the name")
	}
}

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	data, err := logoFiles.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The light mail lives in the inline styles; dark mode, in the head's
// style sheet, turns page, text and rules to the brand's dark palette and
// swaps the logos, also for Outlook on the web.
func TestDarkMode(t *testing.T) {
	html := Render(sample(), neodesk(), "").HTML
	head, body, _ := strings.Cut(html, "</head>")
	for _, want := range []string{`<meta name="color-scheme" content="light dark">`, `<meta name="supported-color-schemes" content="light dark">`} {
		if !strings.Contains(head, want) {
			t.Errorf("no %s", want)
		}
	}
	for _, want := range []string{
		"@media (prefers-color-scheme: dark)",
		".nd-page { background-color: #141616 !important; }",
		".nd-ink { color: #e4e7e7 !important; }",
		".nd-muted { color: #a3abad !important; }",
		".nd-light { display: none !important; }",
		".nd-dark { display: block !important;",
		"[data-ogsb] .nd-page", "[data-ogsc] .nd-ink",
	} {
		if !strings.Contains(head, want) {
			t.Errorf("dark style sheet lacks %q", want)
		}
	}
	for _, want := range []string{`background-color: #ffffff`, `class="nd-btn" bgcolor="#1f6feb"`, `color: #ffffff; text-decoration: none`, `font-family: 'IBM Plex Sans'`} {
		if !strings.Contains(body, want) {
			t.Errorf("light body lacks %q", want)
		}
	}
}

// The preheader is hidden text at the top of the body, ahead of
// everything else, with spacing after it.
func TestPreheader(t *testing.T) {
	html := Render(sample(), neodesk(), "").HTML
	_, body, _ := strings.Cut(html, "<body")
	if !regexp.MustCompile(`^[^>]*><div class="nd-preheader" style="display: none;[^"]*mso-hide: all;">Set up the app in a few minutes(&#847;&zwnj;&nbsp;)+</div>`).MatchString(body) {
		t.Errorf("no hidden preheader first in the body:\n%.600s", body)
	}
	m := sample()
	m.Preheader = ""
	if strings.Contains(Render(m, neodesk(), "").HTML, "nd-preheader") {
		t.Error("an empty preheader is written")
	}
}

// The button is drawn in VML for Outlook for Windows and as a padded
// link in a coloured cell for everyone else, both to the same link.
func TestOutlookButton(t *testing.T) {
	html := Render(sample(), neodesk(), "").HTML
	if !regexp.MustCompile(`<!--\[if mso\]><v:roundrect [^>]*href="https://desk\.acme\.example/join/9H83-A6RJ"[^>]*fillcolor="#1f6feb"><w:anchorlock/><center [^>]*color: #ffffff;[^>]*>Set up NeoDesk</center></v:roundrect><!\[endif\]-->`).MatchString(html) {
		t.Errorf("no VML button:\n%s", html)
	}
	if !strings.Contains(html, `<!--[if !mso]><!--><table role="presentation"`) {
		t.Error("the HTML button isn't hidden from Outlook")
	}
	if n := strings.Count(html, `class="nd-on nd-btn-a"`); n != 1 {
		t.Errorf("%d buttons, want 1", n)
	}
}

// The layout is 600 pixels wide and fluid below that, also in Outlook
// for Windows, which ignores max-width.
func TestWidth(t *testing.T) {
	html := Render(sample(), neodesk(), "").HTML
	for _, want := range []string{`max-width: 600px;`, `<!--[if mso]><table role="presentation" width="600"`, `<meta name="viewport" content="width=device-width, initial-scale=1">`, `@media (max-width: 480px)`} {
		if !strings.Contains(html, want) {
			t.Errorf("no %q", want)
		}
	}
}

// A mail loads nothing: no scripts, no style sheets, no images from
// anywhere but the mail itself.
func TestNothingRemote(t *testing.T) {
	for _, b := range []Branding{neodesk(), {Server: "Acme IT", Accent: "#ff0000"}, {Server: "Acme IT", LogoImage: pngOf(t, 10, 10), LogoType: "image/png"}} {
		html := Render(sample(), b, "https://x.example/mail/unsubscribe?t=a").HTML
		for _, bad := range []*regexp.Regexp{
			regexp.MustCompile(`(?i)<script`), regexp.MustCompile(`(?i)<link`), regexp.MustCompile(`(?i)@import`),
			regexp.MustCompile(`(?i)src="(?:https?:)?//`), regexp.MustCompile(`(?i)url\(`), regexp.MustCompile(`(?i)<img [^>]*src="(?:[^c]|c[^i])`),
		} {
			if bad.MatchString(html) {
				t.Errorf("%s: the mail matches %s", b.Server, bad)
			}
		}
	}
}

// The plain text part says what the HTML says: heading, lead, the
// button's text with the link, the steps, the code, the details and the
// footer, and no markup.
func TestTextPart(t *testing.T) {
	built := Render(sample(), Branding{Server: "Acme IT", Host: "desk.acme.example"}, "")
	for _, want := range []string{
		"Set up remote support\n\nJana Weber from Acme IT invited you.\n\n",
		"Set up NeoDesk:\nhttps://desk.acme.example/join/9H83-A6RJ\n\n",
		"1. Download\n2. Install\n\n", "Or enter this code:\n9H83-A6RJ\n\n",
		"The link works until 3 December 2026.\n\n-- \n",
		"mail.footer_sent_named server=Acme IT host=desk.acme.example\n", "You get this because Jana invited you.\n",
	} {
		if !strings.Contains(built.Text, want) {
			t.Errorf("text part lacks %q:\n%s", want, built.Text)
		}
	}
	if strings.ContainsAny(built.Text, "<>") {
		t.Errorf("markup in the text part:\n%s", built.Text)
	}
}

// The footer says which server sent the mail: by its name, or for a
// server that keeps the name NeoDesk as a server that runs NeoDesk (not
// as NeoDesk itself); then who runs it and how to reach them where the
// admin said so, why the reader got the mail and how to turn it off.
func TestFooter(t *testing.T) {
	m := sample()
	m.TurnOff = "Turn these off below."
	m.Unsubscribe = "chat"
	def := Render(m, neodesk(), "https://internal.neode.sk/mail/unsubscribe?t=x")
	if !strings.Contains(def.HTML, ">mail.footer_sent_default server=NeoDesk host=internal.neode.sk</p>") || strings.Contains(def.HTML, "footer_sent_named") {
		t.Errorf("the default name's footer:\n%s", def.HTML)
	}
	for _, want := range []string{">You get this because Jana invited you.</p>", ">Turn these off below.</p>", `href="https://internal.neode.sk/mail/unsubscribe?t=x"`} {
		if !strings.Contains(def.HTML, want) {
			t.Errorf("footer lacks %s", want)
		}
	}
	if !strings.Contains(def.Text, "mail.unsubscribe: https://internal.neode.sk/mail/unsubscribe?t=x\n") {
		t.Errorf("the text part lacks the link that turns it off:\n%s", def.Text)
	}

	b := Branding{Server: "Acme IT", Host: "desk.acme.example", Contact: Contact{Organisation: "Acme GmbH", Email: "it@acme.example", URL: "https://acme.example/help"}}
	html := Render(sample(), b, "").HTML
	for _, want := range []string{
		">mail.footer_sent_named server=Acme IT host=desk.acme.example</p>",
		">mail.footer_run_by organisation=Acme GmbH</p>",
		`mail.footer_contact_both email=<a class="nd-link" href="mailto:it@acme.example"`,
		`<a class="nd-link" href="https://acme.example/help"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("footer lacks %s:\n%s", want, html)
		}
	}
	if strings.Contains(Render(sample(), b, "https://x/unsub").HTML, "x/unsub") {
		t.Error("a mail that is no notification links to an unsubscribe page")
	}
	b.Contact = Contact{URL: "javascript:alert(1)"}
	if strings.Contains(Render(sample(), b, "").HTML, "javascript:") {
		t.Error("a support link that is no web address is written")
	}
}

// Render marks its mails, so the mail service can refuse anything else.
func TestRendered(t *testing.T) {
	if !Render(sample(), neodesk(), "").Rendered() {
		t.Error("a rendered mail isn't marked")
	}
	if (Built{Subject: "s", HTML: "<p>x</p>"}).Rendered() {
		t.Error("a mail built by hand counts as rendered")
	}
}

// The preview shows the images inline and the palette that was asked
// for.
func TestPreview(t *testing.T) {
	built := Render(sample(), neodesk(), "")
	dark, light := Preview(built, true), Preview(built, false)
	if strings.Contains(dark, "cid:") || !strings.Contains(dark, `src="data:image/png;base64,`) {
		t.Error("the preview still points at Content-IDs")
	}
	if !strings.Contains(dark, "@media all {") || !strings.Contains(light, "@media not all {") {
		t.Error("the preview doesn't pin the palette")
	}
}
