package brandmail

import (
	"bytes"
	"image/png"
	"regexp"
	"strings"
	"testing"
)

var cids = regexp.MustCompile(`src="cid:([^"]+)"`)

// The images a page shows by Content-ID are exactly the ones Inline
// carries, and each is a PNG.
func carried(t *testing.T, p Page) {
	t.Helper()
	html := HTML(p)
	shown := map[string]bool{}
	for _, m := range cids.FindAllStringSubmatch(html, -1) {
		shown[m[1]] = true
	}
	images := Inline(p)
	if len(images) != len(shown) {
		t.Fatalf("HTML shows %v, Inline carries %d images", shown, len(images))
	}
	for _, img := range images {
		if !shown[img.ID] {
			t.Errorf("image %s carried but not shown", img.ID)
		}
		if _, err := png.Decode(bytes.NewReader(img.Data)); err != nil || img.ContentType != "image/png" {
			t.Errorf("image %s is no PNG: %v", img.ID, err)
		}
	}
}

// A server named NeoDesk without a logo image gets NeoDesk's full logo,
// one for light mode (on its plate, see TestLogoPlate) and one for dark
// mode, carried in the mail; another name gets the mark and the name; a
// logo image of its own goes in by its address and nothing is carried.
func TestLogo(t *testing.T) {
	own := Page{Branding: Branding{Server: "NeoDesk"}, Heading: "Hi"}
	carried(t, own)
	html := HTML(own)
	if !strings.Contains(html, `class="nd-light" src="cid:logo-light@neodesk" alt="NeoDesk" width="152" height="48"`) ||
		!strings.Contains(html, `<!--[if !mso]><!--><img class="nd-dark nd-dark-logo" src="cid:logo-dark@neodesk"`) {
		t.Errorf("no full logo for light and dark:\n%s", html)
	}
	if !bytes.Equal(Inline(own)[1].Data, mustRead(t, "logo/logo-on-dark-64h.png")) {
		t.Error("the dark logo isn't the brand's dark PNG")
	}

	named := Page{Branding: Branding{Server: "Acme Desk"}, Heading: "Hi"}
	carried(t, named)
	if html := HTML(named); !strings.Contains(html, `>Acme Desk</td>`) || !strings.Contains(html, `width="32" height="32"`) {
		t.Errorf("no mark and name:\n%s", html)
	}
	if !bytes.Equal(Inline(named)[0].Data, mustRead(t, "logo/mark-on-light-64.png")) {
		t.Error("another name doesn't get the mark")
	}

	image := Page{Branding: Branding{Server: "Acme Desk", Logo: "https://acme.example/logo.png"}, Heading: "Hi"}
	carried(t, image)
	if html := HTML(image); !strings.Contains(html, `src="https://acme.example/logo.png"`) || strings.Contains(html, "cid:") {
		t.Errorf("the server's own logo isn't used:\n%s", html)
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
// style sheet, turns page, card, text and rules to the console's dark
// theme, the button to the dark accent and swaps the logos, also for
// Outlook on the web.
func TestDarkMode(t *testing.T) {
	html := HTML(Page{Branding: Branding{Server: "NeoDesk"}, Heading: "Hi", Lead: "Text", Button: "Go", Link: "https://x.example", Footer: "Foot"})
	head, body, _ := strings.Cut(html, "</head>")
	if !strings.Contains(head, `<meta name="color-scheme" content="light dark">`) {
		t.Error("no color-scheme meta")
	}
	for _, want := range []string{
		"@media (prefers-color-scheme: dark)",
		".nd-page { background-color: #0b0d0d !important; }",
		".nd-card { background-color: #141616 !important;",
		".nd-ink { color: #e8eaeb !important; }",
		".nd-btn, .nd-step { background-color: #58a6ff !important; }",
		".nd-on { color: #0c1b1e !important; }",
		".nd-light { display: none !important; }",
		".nd-dark { display: block !important;",
		".nd-dark-logo { width: 136px !important; height: 32px !important; }",
		"[data-ogsb] .nd-card", "[data-ogsc] .nd-ink",
	} {
		if !strings.Contains(head, want) {
			t.Errorf("dark style sheet lacks %q", want)
		}
	}
	for _, want := range []string{`class="nd-card"`, `background-color: #ffffff`, `class="nd-btn" bgcolor="#1b62cf"`, `color: #ffffff; text-decoration: none`, `font-family: 'IBM Plex Sans'`} {
		if !strings.Contains(body, want) {
			t.Errorf("light body lacks %q", want)
		}
	}

	// An accent of the server's own is the button in both modes
	custom := HTML(Page{Branding: Branding{Server: "NeoDesk", Accent: "#7c3aed"}, Heading: "Hi", Button: "Go", Link: "https://x.example"})
	if !strings.Contains(custom, `bgcolor="#7c3aed"`) || !strings.Contains(custom, ".nd-btn, .nd-step { background-color: #7c3aed !important; }") {
		t.Errorf("a custom accent isn't kept in dark mode:\n%s", custom)
	}
}
