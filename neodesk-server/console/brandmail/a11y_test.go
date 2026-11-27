package brandmail

import (
	"bytes"
	"image"
	"image/png"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The mail says its language and direction on <html> and on the content
// table, so a screen reader picks the right voice and Arabic runs right
// to left with the quote's bar and the step numbers on the right.
func TestLangAndDir(t *testing.T) {
	for _, c := range []struct{ lang, dir string }{{"de", "ltr"}, {"en", "ltr"}, {"ar", "rtl"}, {"he", "rtl"}} {
		m := sample()
		m.Lang, m.Quote, m.QuoteLabel = c.lang, "q", "They wrote:"
		html := Render(m, Branding{Server: "Acme IT"}, "").HTML
		if want := `<html lang="` + c.lang + `" dir="` + c.dir + `"`; !strings.Contains(html, want) {
			t.Errorf("%s: no %s in %.200s", c.lang, want, html)
		}
		if !strings.Contains(html, `class="nd-page" dir="`+c.dir+`" lang="`+c.lang+`"`) {
			t.Errorf("%s: the content table doesn't carry dir and lang", c.lang)
		}
	}
	m := sample()
	m.Lang, m.Quote = "ar", "q"
	ar := Render(m, Branding{Server: "Acme IT"}, "").HTML
	if !strings.Contains(ar, "border-right: 3px solid") || strings.Contains(ar, "border-left: 3px solid") || !strings.Contains(ar, "text-align: right;") {
		t.Error("the Arabic mail isn't mirrored")
	}
}

// The heading is a heading, the texts are paragraphs, layout tables say
// they are layout.
func TestHeadingAndParagraphs(t *testing.T) {
	html := Render(sample(), neodesk(), "").HTML
	for _, re := range []string{`<h1 [^>]*>Set up remote support</h1>`, `<p [^>]*>Jana Weber from Acme IT invited you\.</p>`, `<p [^>]*>The link works until 3 December 2026\.</p>`} {
		if !regexp.MustCompile(re).MatchString(html) {
			t.Errorf("no %s in the mail", re)
		}
	}
	if n := strings.Count(html, "<h1"); n != 1 {
		t.Errorf("%d headings", n)
	}
	for _, tag := range regexp.MustCompile(`<table [^>]*>`).FindAllString(html, -1) {
		if !strings.Contains(tag, `role="presentation"`) {
			t.Errorf("layout table without role: %s", tag)
		}
	}
}

func wcagLuminance(hex string) float64 {
	l := 0.0
	for i, w := range []float64{0.2126, 0.7152, 0.0722} {
		v, _ := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		c := float64(v) / 255
		if c <= 0.03928 {
			c /= 12.92
		} else {
			c = math.Pow((c+0.055)/1.055, 2.4)
		}
		l += w * c
	}
	return l
}

func wcagContrast(a, b string) float64 {
	x, y := wcagLuminance(a), wcagLuminance(b)
	if x < y {
		x, y = y, x
	}
	return (x + 0.05) / (y + 0.05)
}

// Every colour pair of both palettes reaches WCAG 2.2 AA for any accent:
// text 4.5:1 on the page and on the quote, the button's text 4.5:1 on
// the button, and the button 3:1 on the page. The accents include a very
// light and a very dark one, which the button has to move away from.
func TestPaletteContrast(t *testing.T) {
	accents := []string{"", "#1f6feb", "#58a6ff", "#fefefe", "#fff8dc", "#ffff00", "#050505", "#0b0d0d", "#141616", "#7a7a7a", "#808080", "#ff0000", "#00ff00", "#ff7f50", "#7c3aed", "#e11d48", "#0f766e", "not a colour"}
	for _, accent := range accents {
		light, dark := Palettes(accent)
		for name, p := range map[string]Palette{"light": light, "dark": dark} {
			pairs := []struct {
				what   string
				fg, bg string
				min    float64
			}{
				{"text on the page", p.Ink, p.Page, 4.5}, {"muted text on the page", p.Muted, p.Page, 4.5},
				{"links on the page", p.Link, p.Page, 4.5}, {"text in a quote", p.Ink, p.Quote, 4.5},
				{"muted text in a quote", p.Muted, p.Quote, 4.5},
				{"button text on the button", p.OnButton, p.Button, 4.5}, {"the button on the page", p.Button, p.Page, 3},
			}
			for _, c := range pairs {
				if r := wcagContrast(c.fg, c.bg); r < c.min {
					t.Errorf("accent %q, %s palette: %s %s on %s is %.2f:1, want %.1f:1", accent, name, c.what, c.fg, c.bg, r, c.min)
				}
			}
		}
		// What the mail writes is the palette
		html := Render(Mail{T: tr, Subject: "s", Heading: "h", Button: "Go", Link: "https://x.example"}, Branding{Server: "NeoDesk", Accent: accent}, "").HTML
		if !strings.Contains(html, `bgcolor="`+light.Button+`"`) || !strings.Contains(html, `.nd-btn, .nd-step { background-color: `+dark.Button+` !important;`) || !strings.Contains(html, `.nd-on { color: `+dark.OnButton+` !important; }`) {
			t.Errorf("accent %q: the mail doesn't use its palette", accent)
		}
	}
	// The brand's blue is the default, kept as it is
	if light, _ := Palettes(""); light.Button != "#1f6feb" || light.OnButton != "#ffffff" {
		t.Errorf("default button %s on %s", light.OnButton, light.Button)
	}
}

// A quote is a blockquote with a lead-in that says who wrote it.
func TestQuoteLabelled(t *testing.T) {
	m := sample()
	m.Quote, m.QuoteLabel = "My screen is black", "They wrote:"
	if !regexp.MustCompile(`They wrote:</p><blockquote [^>]*>My screen is black</blockquote>`).MatchString(Render(m, neodesk(), "").HTML) {
		t.Error("the quote is no labelled blockquote")
	}
}

// NeoDesk's light logo sits on an opaque white plate, so clients that
// invert the mail's colours but not its images still show it; the
// artwork's own pixels are unchanged on the plate.
func TestLogoPlate(t *testing.T) {
	images := Inline(neodesk())
	plate, err := png.Decode(bytes.NewReader(images[0].Data))
	if err != nil {
		t.Fatal(err)
	}
	art, _ := png.Decode(bytes.NewReader(mustRead(t, "logo/logo-on-light-64h.png")))
	pb, ab := plate.Bounds(), art.Bounds()
	if pb.Dx() <= ab.Dx() || pb.Dy() <= ab.Dy() {
		t.Fatalf("the light logo is %v, no plate around the %v artwork", pb, ab)
	}
	if r, g, b, a := plate.At(pb.Dx()/2, 2).RGBA(); a != 0xffff || r != 0xffff || g != 0xffff || b != 0xffff {
		t.Error("the plate is not opaque white")
	}
	off := image.Pt((pb.Dx()-ab.Dx())/2, (pb.Dy()-ab.Dy())/2)
	same := 0
	for y := ab.Min.Y; y < ab.Max.Y; y++ {
		for x := ab.Min.X; x < ab.Max.X; x++ {
			ar, ag, abl, aa := art.At(x, y).RGBA()
			if aa != 0xffff {
				continue
			}
			pr, pg, pbl, _ := plate.At(x+off.X, y+off.Y).RGBA()
			if pr != ar || pg != ag || pbl != abl {
				t.Fatalf("pixel %d,%d of the artwork changed on the plate", x, y)
			}
			same++
		}
	}
	if same == 0 {
		t.Fatal("no opaque pixel of the artwork found")
	}
}

// A code the reader types is a line of its own in a monospace font, under
// the sentence that says what to do with it.
func TestCodeOwnLine(t *testing.T) {
	if !regexp.MustCompile(`>Or enter this code:</p><p class="nd-ink" dir="ltr" style="[^"]*font-family: 'IBM Plex Mono'[^"]*">9H83-A6RJ</p>`).MatchString(Render(sample(), neodesk(), "").HTML) {
		t.Error("the code is not on a line of its own")
	}
}

// The button is at least 44 pixels high: 13 pixels and a border above
// and below a 20 pixel line, 48 in Outlook.
func TestButtonHeight(t *testing.T) {
	html := Render(sample(), neodesk(), "").HTML
	if !strings.Contains(html, "padding: 13px 24px;") || !strings.Contains(html, "line-height: 20px;") || !strings.Contains(html, "height: 48px;") {
		t.Error("the button is under 44 px")
	}
}

// Label and value rows are a table whose labels are row headers.
func TestFacts(t *testing.T) {
	m := sample()
	m.Facts = []Fact{{"Severity", "Critical"}, {"Device", "PC-01 <b>"}}
	html := Render(m, neodesk(), "").HTML
	if !regexp.MustCompile(`<table class="nd-facts"[^>]*><tr><th scope="row"[^>]*>Severity</th><td [^>]*>Critical</td></tr><tr><th scope="row"[^>]*>Device</th><td [^>]*>PC-01 &lt;b&gt;</td></tr></table>`).MatchString(html) {
		t.Errorf("no table with row headers:\n%s", html)
	}
}

// Times are said the reader's way, in the reader's zone with its name,
// never as a bare UTC timestamp.
func TestDateTime(t *testing.T) {
	at := time.Date(2026, 12, 3, 0, 12, 0, 0, time.UTC)
	berlin := Zone("Europe/Berlin")
	for _, c := range []struct{ lang, want string }{
		{"en", "3 December 2026, 01:12 CET"},
		{"de", "3. Dezember 2026, 01:12 Uhr CET"},
		{"fr", "3 décembre 2026 à 01:12 CET"},
		{"pl", "3 grudnia 2026, 01:12 CET"},
		{"ja", "2026年12月3日 01:12 CET"},
		{"xx", "3 December 2026, 01:12 CET"},
	} {
		if got := DateTime(at, c.lang, berlin); got != c.want {
			t.Errorf("%s: %q, want %q", c.lang, got, c.want)
		}
	}
	if got := DateTime(at, "en", time.UTC); got != "3 December 2026, 00:12 UTC" {
		t.Errorf("UTC: %q", got)
	}
	if got := DateTime(at, "en", Zone("Asia/Kolkata")); got != "3 December 2026, 05:42 IST" {
		t.Errorf("Kolkata: %q", got)
	}
	if got := DateTime(at, "en", time.FixedZone("", 3*3600+1800)); got != "3 December 2026, 03:42 UTC+3:30" {
		t.Errorf("a zone without a name: %q", got)
	}
	if Zone("No/Such_Zone") != time.Local || Zone("") != time.Local {
		t.Error("an unknown zone isn't the server's")
	}
	if got := Date(at, "de", berlin); got != "3. Dezember 2026" {
		t.Errorf("Date: %q", got)
	}
}
