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

// The mail says its language and direction on <html> and on the card,
// so a screen reader picks the right voice and Arabic runs right to left.
func TestLangAndDir(t *testing.T) {
	for _, c := range []struct{ lang, dir string }{{"de", "ltr"}, {"en", "ltr"}, {"ar", "rtl"}, {"he", "rtl"}} {
		html := HTML(Page{Branding: Branding{Server: "Acme Desk"}, Lang: c.lang, Heading: "Hi", Lead: "Text", Quote: "q", QuoteLabel: "They wrote:"})
		if want := `<html lang="` + c.lang + `" dir="` + c.dir + `">`; !strings.Contains(html, want) {
			t.Errorf("%s: no %s in %.200s", c.lang, want, html)
		}
		if !strings.Contains(html, `class="nd-card" dir="`+c.dir+`" lang="`+c.lang+`"`) {
			t.Errorf("%s: the card doesn't carry dir and lang", c.lang)
		}
	}
	// Right to left, the quote's bar and the gap after the mark change sides
	ar := HTML(Page{Branding: Branding{Server: "Acme Desk"}, Lang: "ar", Heading: "Hi", Quote: "q", QuoteLabel: "l"})
	if !strings.Contains(ar, "border-right: 3px solid") || strings.Contains(ar, "border-left: 3px solid") {
		t.Error("the quote's bar is not on the right in an Arabic mail")
	}
}

// The heading is a heading, the lead and the small lines are paragraphs.
func TestHeadingAndParagraphs(t *testing.T) {
	html := HTML(Page{Branding: Branding{Server: "NeoDesk"}, Lang: "en", Heading: "Welcome to Acme", Lead: "The lead.", Small: []string{"Small one."}, Footer: "Foot."})
	for _, re := range []string{`<h1 [^>]*>Welcome to Acme</h1>`, `<p [^>]*>The lead\.</p>`, `<p [^>]*>Small one\.</p>`, `<p [^>]*>Foot\.</p>`} {
		if !regexp.MustCompile(re).MatchString(html) {
			t.Errorf("no %s in the mail", re)
		}
	}
	if regexp.MustCompile(`<td [^>]*>Welcome to Acme</td>`).MatchString(html) {
		t.Error("the heading is still bare text in a cell")
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

var (
	buttonBg = regexp.MustCompile(`class="nd-btn" bgcolor="(#[0-9a-f]{6})"`)
	buttonFg = regexp.MustCompile(`<a class="nd-on" [^>]*color: (#[0-9a-f]{6});`)
	darkBtn  = regexp.MustCompile(`\.nd-btn, \.nd-step \{ background-color: (#[0-9a-f]{6})`)
	darkOn   = regexp.MustCompile(`\.nd-on \{ color: (#[0-9a-f]{6})`)
)

// The button's text reaches 4.5:1 on the button for any accent: the
// better of white and near black, and a darker button where neither does.
func TestButtonContrast(t *testing.T) {
	if got := OnAccent("#7a7a7a"); got != "#ffffff" {
		t.Errorf("OnAccent(#7a7a7a) = %s; white (4.29:1) reads better than near black (4.13:1)", got)
	}
	for _, accent := range []string{"#7a7a7a", "#767676", "#808080", "#ff0000", "#00ff00", "#ffff00", "#ff7f50", "#1b62cf", "#58a6ff", "#000000", "#ffffff", "#7c3aed", "#e11d48"} {
		html := HTML(Page{Branding: Branding{Server: "NeoDesk", Accent: accent}, Lang: "en", Heading: "Hi", Button: "Go", Link: "https://x.example"})
		bg, fg := buttonBg.FindStringSubmatch(html), buttonFg.FindStringSubmatch(html)
		if bg == nil || fg == nil {
			t.Fatalf("%s: no button colours in %s", accent, html)
		}
		if r := wcagContrast(bg[1], fg[1]); r < 4.5 {
			t.Errorf("%s: button text %s on %s is %.2f:1", accent, fg[1], bg[1], r)
		}
		dbg, dfg := darkBtn.FindStringSubmatch(html), darkOn.FindStringSubmatch(html)
		if dbg == nil || dfg == nil {
			t.Fatalf("%s: no dark button colours", accent)
		}
		if r := wcagContrast(dbg[1], dfg[1]); r < 4.5 {
			t.Errorf("%s: dark mode button text %s on %s is %.2f:1", accent, dfg[1], dbg[1], r)
		}
	}
}

// A quote is a blockquote with a lead-in that says who wrote it.
func TestQuoteLabelled(t *testing.T) {
	html := HTML(Page{Branding: Branding{Server: "NeoDesk"}, Lang: "en", Heading: "Hi", Quote: "My screen is black", QuoteLabel: "They wrote:"})
	if !regexp.MustCompile(`They wrote:</p><blockquote [^>]*>My screen is black</blockquote>`).MatchString(html) {
		t.Errorf("the quote is no labelled blockquote:\n%s", html)
	}
}

// NeoDesk's light logo sits on an opaque white plate, so clients that
// invert the mail's colours but not its images still show it; the
// artwork's own pixels are unchanged on the plate.
func TestLogoPlate(t *testing.T) {
	images := Inline(Page{Branding: Branding{Server: "NeoDesk"}})
	plate, err := png.Decode(bytes.NewReader(images[0].Data))
	if err != nil {
		t.Fatal(err)
	}
	art, _ := png.Decode(bytes.NewReader(mustRead(t, "logo/logo-on-light-64h.png")))
	pb, ab := plate.Bounds(), art.Bounds()
	if pb.Dx() <= ab.Dx() || pb.Dy() <= ab.Dy() {
		t.Fatalf("the light logo is %v, no plate around the %v artwork", pb, ab)
	}
	if _, _, _, a := plate.At(pb.Dx()/2, 2).RGBA(); a != 0xffff {
		t.Error("the plate above the logo is not opaque")
	}
	if r, g, b, _ := plate.At(pb.Dx()/2, 2).RGBA(); r != 0xffff || g != 0xffff || b != 0xffff {
		t.Error("the plate is not white")
	}
	// Where the artwork is opaque, the plate shows exactly its pixels
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
	if dark := images[1].Data; !bytes.Equal(dark, mustRead(t, "logo/logo-on-dark-64h.png")) {
		t.Error("the dark logo is no longer the brand's own file")
	}
	html := HTML(Page{Branding: Branding{Server: "NeoDesk"}, Heading: "Hi"})
	if !strings.Contains(html, `src="cid:logo-light@neodesk" alt="NeoDesk" width="152" height="48"`) {
		t.Errorf("the plated logo isn't shown at its size:\n%s", html)
	}
}

// A code the reader types is a line of its own in a monospace font, under
// the sentence that says what to do with it.
func TestCodeOwnLine(t *testing.T) {
	html := HTML(Page{Branding: Branding{Server: "NeoDesk"}, Lang: "en", Heading: "Hi", CodeLead: "Already have NeoDesk? Enter this code:", Code: "ABCD-1234"})
	if !regexp.MustCompile(`>Already have NeoDesk\? Enter this code:</p><p [^>]*font-family: 'IBM Plex Mono'[^>]*font-size: 18px[^>]*>ABCD-1234</p>`).MatchString(html) {
		t.Errorf("the code is not on a line of its own:\n%s", html)
	}
}

// The button is at least 44 pixels high: 14 pixels above and below a 20
// pixel line.
func TestButtonHeight(t *testing.T) {
	html := HTML(Page{Branding: Branding{Server: "NeoDesk"}, Heading: "Hi", Button: "Go", Link: "https://x.example"})
	if !strings.Contains(html, "padding: 14px 24px;") || !strings.Contains(html, "line-height: 20px;") {
		t.Errorf("the button is under 44 px:\n%s", html)
	}
}

// Label and value rows are a table whose labels are row headers.
func TestFacts(t *testing.T) {
	html := HTML(Page{Branding: Branding{Server: "NeoDesk"}, Lang: "en", Heading: "Alert", Facts: []Fact{{"Severity", "Critical"}, {"Device", "PC-01 <b>"}}})
	if !regexp.MustCompile(`<table class="nd-facts"[^>]*><tr><th scope="row"[^>]*>Severity</th><td [^>]*>Critical</td></tr><tr><th scope="row"[^>]*>Device</th><td [^>]*>PC-01 &lt;b&gt;</td></tr></table>`).MatchString(html) {
		t.Errorf("no table with row headers:\n%s", html)
	}
	if strings.Contains(html, `<table class="nd-facts" role="presentation"`) {
		t.Error("the facts table is marked as layout")
	}
}

// A time in a mail names its zone: the server's, with UTC after it, or UTC
// alone.
func TestWhen(t *testing.T) {
	at := time.Date(2026, 12, 3, 15, 4, 0, 0, time.UTC)
	old := time.Local
	defer func() { time.Local = old }()
	time.Local = time.UTC
	if got := When(at); got != "2026-12-03 15:04 UTC" {
		t.Errorf("When in UTC = %q", got)
	}
	time.Local = time.FixedZone("CET", 3600)
	if got := When(at); got != "2026-12-03 16:04 CET (15:04 UTC)" {
		t.Errorf("When in CET = %q", got)
	}
}
