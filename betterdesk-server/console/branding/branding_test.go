package branding

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// newService is a service on db with the console's files from
// testdata/console and a fixed clock.
func newService(t testing.TB, db *store.DB) *Service {
	t.Helper()
	opts := fixtureOptions(t.TempDir())
	opts.DB = db
	opts.Now = func() time.Time { return fixedNow }
	opts.Location = time.UTC
	opts.Rand = &countingReader{next: 0x0123456789abcdef}
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// countingReader stands in for the random source: every eight bytes read
// are the next number, so upload names are known and differ.
type countingReader struct{ next uint64 }

func (c *countingReader) Read(p []byte) (int, error) {
	for i := 0; i+8 <= len(p); i += 8 {
		binary.BigEndian.PutUint64(p[i:], c.next)
		c.next++
	}
	return len(p) - len(p)%8, nil
}

// keyList is the keys of an object as JSON, in their order.
func keyList(o *Object) string {
	keys := []any{}
	for _, key := range o.Keys() {
		keys = append(keys, key)
	}
	return Stringify(keys)
}

func mustParse(t testing.TB, text string) any {
	t.Helper()
	v, err := ParseJSON([]byte(text))
	if err != nil {
		t.Fatalf("parse %s: %v", text, err)
	}
	return v
}

// setRows replaces the stored settings and reloads the cache.
func setRows(t testing.TB, s *Service, rows ...[2]string) {
	t.Helper()
	ctx := context.Background()
	db := s.opts.DB
	if _, err := db.Exec(ctx, "DELETE FROM branding_config"); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if _, err := db.Exec(ctx, "INSERT INTO branding_config (key, value, updated_at) VALUES (?, ?, "+store.NowSQL(db.Dialect())+")", row[0], row[1]); err != nil {
			t.Fatal(err)
		}
	}
	s.InvalidateCache()
	s.LoadBranding(ctx)
}

func storedValue(t testing.TB, s *Service, key string) (string, bool) {
	t.Helper()
	rows, err := getBrandingConfig(context.Background(), s.opts.DB)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.key == key {
			return row.value, true
		}
	}
	return "", false
}

func mustCSS(t testing.TB, s *Service) string {
	t.Helper()
	css, err := s.GenerateThemeCSS()
	if err != nil {
		t.Fatal(err)
	}
	return css
}

func TestGenerateThemeCSSDefaults(t *testing.T) {
	s := oracleService(t)
	css := mustCSS(t, s)
	light, _ := s.palettes.Get("light")
	for _, want := range []string{
		":root {\n",
		"--bg-primary: " + light.(*Object).Text("bgPrimary") + ";",
		"--surface-glass-blur: 0px;",
		"--surface-glass-bg-secondary: var(--bg-secondary);",
		"--sidebar-glass-bg-rail: var(--sidebar-rail-bg);",
		"--sidebar-glass-bg-flyout: var(--sidebar-flyout-bg);",
		"--color-primary: var(--accent-blue);",
		"--color-surface: var(--bg-secondary);",
		"--focus-ring-color: var(--accent-blue-muted);",
		"--ux35-topbar-bg: #161b22;",
		"html { color-scheme: light dark; }\n",
		"    color-scheme: light dark;\n",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("the default stylesheet lacks %q", want)
		}
	}
	// the system mode: light first, dark when the system asks for it
	if !regexp.MustCompile(`@media \(prefers-color-scheme: dark\) \{\s*:root \{[^}]*--bg-primary: #141616`).MatchString(css) {
		t.Errorf("no dark palette under prefers-color-scheme in:\n%s", css)
	}
	if strings.Contains(css, "body.app-page") || strings.Contains(css, "custom branding CSS") {
		t.Errorf("the default stylesheet has a wallpaper or custom CSS:\n%s", css)
	}
}

func TestThemeModesCSS(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db)
		ctx := context.Background()
		for _, tc := range []struct {
			mode, scheme, bgPrimary string
			media                   bool
		}{
			{"light", "light", "#ffffff", false},
			{"dark", "dark", "#141616", false},
			{"system", "light dark", "#ffffff", true},
			{"auto", "dark", "#141616", false},
			{"nonsense", "dark", "#141616", false},
			{"custom", "dark", "#112233", false},
		} {
			updates := object("themeMode", tc.mode, "colors", object("bgPrimary", "#112233", "accentBlueMuted", "#102030"))
			if err := s.SaveBranding(ctx, updates); err != nil {
				t.Fatal(err)
			}
			css := mustCSS(t, s)
			if !strings.Contains(css, "html { color-scheme: "+tc.scheme+"; }") {
				t.Errorf("%s: color-scheme is not %q", tc.mode, tc.scheme)
			}
			if !strings.Contains(css, ":root {\n    --bg-primary: "+tc.bgPrimary+";") {
				t.Errorf("%s: --bg-primary is not %s:\n%s", tc.mode, tc.bgPrimary, css[:200])
			}
			if got := strings.Contains(css, "@media (prefers-color-scheme: dark)"); got != tc.media {
				t.Errorf("%s: dark media block present = %v, want %v", tc.mode, got, tc.media)
			}
			// a muted colour given as hex becomes translucent, in custom mode only
			if got := strings.Contains(css, "--accent-blue-muted: rgba(16, 32, 48, 0.15);"); got != (tc.mode == "custom") {
				t.Errorf("%s: own muted colour used = %v", tc.mode, got)
			}
		}
	})
}

func TestThemePalettes(t *testing.T) {
	s := oracleService(t)
	palettes := s.GetThemePalettes()
	dark, _ := palettes.Get("dark")
	dark.(*Object).Set("bgPrimary", "#000000")
	again, _ := s.GetThemePalettes().Get("dark")
	if got := again.(*Object).Text("bgPrimary"); got != "#141616" {
		t.Errorf("a change to a returned palette reached the built-in one: bgPrimary = %s", got)
	}
	// muted palette colours are translucent tints of their accent
	for _, mode := range []string{"dark", "light"} {
		p := s.palette(mode)
		for _, key := range p.Keys() {
			if strings.HasSuffix(key, "Muted") && !regexp.MustCompile(`^rgba\(\d{1,3}, \d{1,3}, \d{1,3}, 0\.15\)$`).MatchString(p.Text(key)) {
				t.Errorf("%s %s = %q is not a translucent tint", mode, key, p.Text(key))
			}
		}
		muted, _, _ := HexToMutedRGBA(p.Text("accentBlue"), 0.15)
		if muted != p.Text("accentBlueMuted") {
			t.Errorf("%s: accentBlueMuted %q is not the tint of accentBlue (%q)", mode, p.Text("accentBlueMuted"), muted)
		}
	}

	for _, tc := range []struct{ branding, key, want string }{
		{`{"themeMode":"light","colors":{"bgPrimary":"#0d1117","textPrimary":"#e6edf3"}}`, "bgPrimary", "#ffffff"},
		{`{"themeMode":"light","colors":{}}`, "accentBlue", "#1b62cf"},
		{`{"themeMode":"dark","colors":{"bgPrimary":"#123456"}}`, "bgPrimary", "#141616"},
		{`{"themeMode":"system","colors":{"bgPrimary":"#112233"}}`, "bgPrimary", "#ffffff"},
		{`{"themeMode":"custom","colors":{"bgPrimary":" #112233 ","textPrimary":"#abcdef"}}`, "bgPrimary", "#112233"},
		{`{"themeMode":"custom","colors":{"bgPrimary":"#112233"}}`, "bgSecondary", "#1b1e1e"},
		{`{"themeMode":"custom","colors":{"bgPrimary":"   "}}`, "bgPrimary", "#141616"},
		{`{"colors":{"bgPrimary":"#112233"}}`, "bgPrimary", "#141616"},
	} {
		colors, err := s.ResolveThemeColors(mustParse(t, tc.branding))
		if err != nil {
			t.Fatal(err)
		}
		if got := colors.Text(tc.key); got != tc.want {
			t.Errorf("ResolveThemeColors(%s).%s = %q, want %q", tc.branding, tc.key, got, tc.want)
		}
	}
}

func TestNormalizers(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"auto", "dark"}, {"light", "light"}, {"custom", "custom"}, {"system", "system"}, {"nope", "dark"}, {"", "dark"}, {"Light", "dark"},
	} {
		if got, _ := NormalizeThemeMode(tc.in); got != tc.want {
			t.Errorf("NormalizeThemeMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	for _, tc := range []struct{ in, want string }{
		{"classic", "classic"}, {"single", "single"}, {" Slides ", "slides"}, {"nope", "classic"}, {"", "classic"},
	} {
		if got, _ := NormalizeLoginPromoMode(tc.in); got != tc.want {
			t.Errorf("NormalizeLoginPromoMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got, _ := ResolveLoginPromoMode("slides", false); got != "classic" {
		t.Errorf("a mode that was never saved resolves to %q, want classic", got)
	}
	if got, _ := ResolveLoginPromoMode("slides", true); got != "slides" {
		t.Errorf("a saved mode resolves to %q, want slides", got)
	}
	if got, _ := ResolveLoginPromoMode("single", true); got != "single" {
		t.Errorf("a saved mode resolves to %q, want single", got)
	}
	for _, tc := range []struct{ in, want string }{
		{"contain", "contain"}, {" repeat ", "repeat"}, {"center", "center"}, {"auto", "auto"}, {"cover", "cover"}, {"fill", "cover"}, {"", "cover"}, {"Cover", "cover"},
	} {
		if got, _ := NormalizeBackgroundSize(tc.in); got != tc.want {
			t.Errorf("NormalizeBackgroundSize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if _, err := NormalizeThemeMode(mustParse(t, `{"toString":1}`)); Message(err) != "Cannot convert object to primitive value" {
		t.Errorf("an object that cannot be text: error %v", err)
	}
}

func TestHexToMutedRGBA(t *testing.T) {
	for _, tc := range []struct {
		hex   any
		alpha float64
		want  string
		ok    bool
	}{
		{"#58a6ff", 0.15, "rgba(88, 166, 255, 0.15)", true},
		{"#0969da", 0.15, "rgba(9, 105, 218, 0.15)", true},
		{"58a6ff", 0.15, "rgba(88, 166, 255, 0.15)", true},
		{"#58a6ff", 0.1, "rgba(88, 166, 255, 0.1)", true},
		{" #58A6FF ", 1, "rgba(88, 166, 255, 1)", true},
		{"#fff", 0.15, "", false},
		{"not-a-color", 0.15, "", false},
		{"##58a6ff", 0.15, "", false},
		{nil, 0.15, "", false},
	} {
		got, ok, err := HexToMutedRGBA(tc.hex, tc.alpha)
		if err != nil || got != tc.want || ok != tc.ok {
			t.Errorf("HexToMutedRGBA(%v, %v) = %q, %v, %v; want %q, %v", tc.hex, tc.alpha, got, ok, err, tc.want, tc.ok)
		}
	}
}

func TestLoginSlides(t *testing.T) {
	defaults := DefaultLoginSlides()
	if len(defaults) != 4 || defaults[0].(*Object).Text("slideKey") != "free" {
		t.Fatalf("default slides: %s", Stringify(defaults))
	}
	for _, slide := range defaults {
		if !strings.HasPrefix(slide.(*Object).Text("imageUrl"), "/img/login-slides/") {
			t.Errorf("default slide image %q", slide.(*Object).Text("imageUrl"))
		}
	}

	many := []any{}
	for i := 0; i < 15; i++ {
		n := strconv.Itoa(i)
		many = append(many, object("id", "s"+n, "imageUrl", "/uploads/bg-"+n+".jpg", "title", "T"+n, "body", "B"+n, "overlay", "40"))
	}
	capped, err := SanitizeLoginSlides(many)
	if err != nil || len(capped) != MaxLoginSlides || capped[0].(*Object).Text("title") != "T0" || capped[9].(*Object).Text("id") != "s9" {
		t.Errorf("15 slides became %d: %s (%v)", len(capped), clip(Stringify(capped)), err)
	}

	got, err := SanitizeLoginSlides(mustParse(t, `[
		{"id":"bad","imageUrl":"javascript:alert(1)","title":"x"},
		{"id":"proto","imageUrl":"//evil.example/x.png"},
		{"id":"up","imageUrl":"/a/../b.png"},
		{"id":"ok","imageUrl":"/uploads/ok.jpg","title":"Safe","body":"Hi","overlay":"99"},
		{"id":"My Slide!","slideKey":" FREE ","imageUrl":"https://Example.com/a b.jpg","overlay":-5},
		{"slideKey":"own key","imageUrl":"","overlay":"x"},
		"text", null, 5
	]`))
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"id":"ok","slideKey":"","order":0,"imageUrl":"/uploads/ok.jpg","title":"Safe","body":"Hi","overlay":"90"},` +
		`{"id":"MySlide","slideKey":"free","order":1,"imageUrl":"https://example.com/a%20b.jpg","title":"","body":"","overlay":"0"},` +
		`{"id":"ownkey","slideKey":"ownkey","order":2,"imageUrl":"","title":"","body":"","overlay":"null"}]`
	if Stringify(got) != want {
		t.Errorf("sanitised slides\n got  %s\n want %s", Stringify(got), want)
	}

	for _, raw := range []any{nil, Undefined, "", "[", "[]", "{}", []any{}, 5.0, []any{"x"}, mustParse(t, `[{"imageUrl":"data:x"}]`)} {
		slides, err := SanitizeLoginSlides(raw)
		if err != nil || Stringify(slides) != Stringify(DefaultLoginSlides()) {
			t.Errorf("SanitizeLoginSlides(%s) = %s (%v), want the built-in slides", Stringify(raw), clip(Stringify(slides)), err)
		}
	}
	// text that is a JSON list is read as one
	fromText, _ := SanitizeLoginSlides(`[{"id":"a","imageUrl":"/x.png"}]`)
	if len(fromText) != 1 || fromText[0].(*Object).Text("id") != "a" {
		t.Errorf("slides from JSON text: %s", Stringify(fromText))
	}
	long := strings.Repeat("x", 500)
	cut, _ := SanitizeLoginSlides([]any{object("id", long, "slideKey", strings.Repeat("k", 33), "imageUrl", "/x.png", "title", long, "body", long)})
	slide := cut[0].(*Object)
	if len(slide.Text("id")) != 64 || slide.Text("slideKey") != "" || len(slide.Text("title")) != 120 || len(slide.Text("body")) != 400 {
		t.Errorf("limits: id %d, key %q, title %d, body %d", len(slide.Text("id")), slide.Text("slideKey"), len(slide.Text("title")), len(slide.Text("body")))
	}
}

func TestSaveCleansValues(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db)
		ctx := context.Background()
		updates := mustParse(t, `{
			"appName": 42,
			"faviconSvg": "<svg onload=\"alert(1)\"><script>alert(1)</script><rect width=\"4\" height=\"4\"/></svg>",
			"logoSvg": "<svg><a href=\"javascript:alert(1)\">x</a><use xlink:href=\"#a\"/><style>*{}</style></svg>",
			"logoUrl": "javascript:alert(1)",
			"bgImageUrl": "https://Example.COM:443/a b.png",
			"loginBgImageUrl": "//evil.example/x.png",
			"bgColor": "red;}body{display:none}",
			"bgGradient": "linear-gradient(135deg, #111 0%, #222 100%); background: url(x)",
			"glassColor": "#ABCDEF<script>",
			"bgSize": "stretch",
			"themeMode": "auto",
			"loginPromoMode": " SLIDES ",
			"customCss": "@import url(x.css); body { color: red } </style><script>alert(1)</script>",
			"colors": {"bgPrimary": "#fff;}x{", "nope": "#000", "accentBlue": "expression(alert(1))"},
			"loginSlides": [{"id": "one", "imageUrl": "/uploads/a.jpg"}],
			"notASetting": "x"
		}`)
		if err := s.SaveBranding(ctx, updates); err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]string{
			"appName":    "42",
			"faviconSvg": `<svg><rect width="4" height="4"/></svg>`,
			// the cut of a script link leaves its closing quote behind
			"logoSvg":                 `<svg><a  href="#"">x</a></svg>`,
			"bgImageUrl":              "https://example.com/a%20b.png",
			"bgColor":                 "redbodydisplaynone",
			"bgGradient":              "linear-gradient(135deg, #111 0%, #222 100%) background x)",
			"glassColor":              "#ABCDEFscript",
			"bgSize":                  "cover",
			"themeMode":               "dark",
			"loginPromoMode":          "slides",
			"customCss":               " body { color: red } alert(1)",
			"colors":                  `{"bgPrimary":"#fffx","accentBlue":"alert(1))"}`,
			"loginSlides":             `[{"id":"one","slideKey":"","order":0,"imageUrl":"/uploads/a.jpg","title":"","body":"","overlay":"null"}]`,
			"appearanceSchemaVersion": "2",
		} {
			if got, _ := storedValue(t, s, key); got != want {
				t.Errorf("stored %s = %q, want %q", key, got, want)
			}
		}
		for _, key := range []string{"logoUrl", "loginBgImageUrl", "notASetting"} {
			if got, ok := storedValue(t, s, key); ok {
				t.Errorf("%s was stored as %q, it should have been skipped", key, got)
			}
		}
		// what was skipped keeps its old value
		if err := s.SaveBranding(ctx, object("logoUrl", "/uploads/logo-a.png")); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveBranding(ctx, object("logoUrl", "data:image/png;base64,AAAA", "footerText", "kept")); err != nil {
			t.Fatal(err)
		}
		if got := s.GetBranding().Text("logoUrl"); got != "/uploads/logo-a.png" {
			t.Errorf("logoUrl after a refused value = %q, want the old one", got)
		}
		if got := s.GetBranding().Text("footerText"); got != "kept" {
			t.Errorf("footerText = %q: the other settings of the refused save were not stored", got)
		}
		// a value that cannot become text fails before anything is written
		err := s.SaveBranding(ctx, mustParse(t, `{"footerText":"lost","appName":{"toString":1}}`))
		var own *Error
		if !errors.As(err, &own) || own.Name != "TypeError" || own.Message != "Cannot convert object to primitive value" {
			t.Errorf("saving an object as a name: %v", err)
		}
		if got := s.GetBranding().Text("footerText"); got != "kept" {
			t.Errorf("footerText = %q after a failed save", got)
		}
	})
}

func TestNestedAppearanceInput(t *testing.T) {
	flat, err := FlattenAppearanceInput(mustParse(t, `{
		"appearance": {
			"identity": {"appName": "Nested", "unknown": 1},
			"console": {"bgSize": " contain ", "bgType": "image"},
			"palette": {"themeMode": "custom", "colors": {"bgPrimary": "#111111"}, "semantic": {"primary": "#222222", "border": "#333333", "nope": "#444444"}}
		},
		"appName": "ignored when appearance is given?",
		"footerText": "flat keys beside the sections count"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	// the keys come in the order of the sections, then the flat ones
	want := `{"appName":"ignored when appearance is given?","themeMode":"custom",` +
		`"colors":{"bgPrimary":"#111111","accentBlue":"#222222","borderPrimary":"#333333"},"bgType":"image","bgSize":"contain","footerText":"flat keys beside the sections count","appearanceSchemaVersion":"2"}`
	if Stringify(flat) != want {
		t.Errorf("flattened\n got  %s\n want %s", Stringify(flat), want)
	}
	for _, input := range []any{nil, "x", 5.0, true, Undefined} {
		if flat, _ := FlattenAppearanceInput(input); flat.Len() != 0 {
			t.Errorf("FlattenAppearanceInput(%v) = %s, want {}", input, Stringify(flat))
		}
	}
	if flat, _ := FlattenAppearanceInput([]any{}); Stringify(flat) != `{"appearanceSchemaVersion":"2"}` {
		t.Errorf("a list flattens to %s", Stringify(flat))
	}
}

func TestWallpaperCSS(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db)
		setRows(t, s, [2]string{"bgType", "image"}, [2]string{"bgImageUrl", "/uploads/bg-test.jpg"}, [2]string{"bgOverlay", "20"}, [2]string{"bgSize", "cover"}, [2]string{"bgBlur", "12.5"})
		css := mustCSS(t, s)
		for _, want := range []string{
			"body.app-page::before {\n    content: '';\n    position: fixed;\n    inset: 0;\n    z-index: 0;\n    pointer-events: none;\n    background: url(\"/uploads/bg-test.jpg\");\n    background-size: cover; background-repeat: no-repeat; background-position: center;\n    filter: blur(12.5px);\n    transform: scale(1.05);\n}\n",
			"body.app-page::after {\n    content: '';\n    position: fixed;\n    inset: 0;\n    z-index: 0;\n    background: rgba(0, 0, 0, 0.20);\n    pointer-events: none;\n}\n",
			"body.app-page .app-layout,\nbody.app-page .ux35-shell,",
			"body.app-page .main-content,\nbody.app-page .ux35-content { background: transparent; }\n",
			// the sign-in page and the remote page take the console's wallpaper over
			"body.login-page::before {\n    content: '';\n    position: fixed;\n    inset: 0;\n    z-index: -2;\n    background: url(\"/uploads/bg-test.jpg\");",
			"body.login-page::after {",
			"body.rd-desk-body::before {",
			"body.rd-desk-body::after {",
		} {
			if !strings.Contains(css, want) {
				t.Errorf("the stylesheet lacks %q in:\n%s", want, css)
			}
		}

		setRows(t, s, [2]string{"bgType", "color"}, [2]string{"bgColor", "#102030"}, [2]string{"loginBgType", "none"}, [2]string{"rdclientBgType", "gradient"},
			[2]string{"rdclientBgGradient", "linear-gradient(#000, #fff)"}, [2]string{"rdclientBgOverlay", "150"}, [2]string{"loginPromoMode", "single"}, [2]string{"loginBgOverlay", "33.335"})
		css = mustCSS(t, s)
		for want, present := range map[string]bool{
			"background: #102030;\n    \n}\n":                    true,
			"body.app-page::after":                               false,
			"body.login-page::before":                            false,
			"body.login-page.login-layout-single .login-promo {": false,
			"body.login-page.login-layout-single .login-promo::after {\n    content: '';\n    position: absolute;\n    inset: 0;\n    background: rgba(0, 0, 0, 0.33);":                               true,
			"body.rd-desk-body::before {\n    content: '';\n    position: fixed;\n    inset: 0;\n    z-index: 0;\n    pointer-events: none;\n    background: linear-gradient(#000, #fff);\n    \n}\n": true,
			"background: rgba(0, 0, 0, 0.95);": true,
		} {
			if strings.Contains(css, want) != present {
				t.Errorf("%q present = %v, want %v in:\n%s", want, !present, present, css)
			}
		}
	})
}

func TestGlassCSS(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db)
		setRows(t, s, [2]string{"glassEnabled", "true"}, [2]string{"glassColor", "#102030"}, [2]string{"glassBlur", "99"}, [2]string{"glassOpacity", "12.5"}, [2]string{"themeMode", "dark"})
		css := mustCSS(t, s)
		for _, want := range []string{
			"--surface-glass-blur: 40px;",
			"--surface-glass-saturate: 1.2;",
			// 12.5 % is exactly 0.125, which rounds up
			"--surface-glass-bg-secondary: rgba(16, 32, 48, 0.13);",
			"--surface-glass-bg-tertiary: rgba(16, 32, 48, 0.21);",
			"--surface-glass-bg-elevated: rgba(16, 32, 48, 0.28);",
			"--surface-glass-border: rgba(16, 32, 48, 0.47);",
			"--sidebar-glass-bg-rail: rgba(16, 32, 48, 0.32);",
			"--sidebar-glass-bg-flyout: rgba(16, 32, 48, 0.42);",
		} {
			if !strings.Contains(css, want) {
				t.Errorf("the stylesheet lacks %q", want)
			}
		}
		// without a usable glass colour the surface colour of the palette is taken
		setRows(t, s, [2]string{"glassEnabled", "yes"}, [2]string{"glassColor", "red"}, [2]string{"glassBlur", "x"}, [2]string{"glassOpacity", ""}, [2]string{"themeMode", "light"})
		css = mustCSS(t, s)
		for _, want := range []string{"--surface-glass-blur: 16px;", "--surface-glass-bg-secondary: rgba(243, 244, 244, 0.55);"} {
			if !strings.Contains(css, want) {
				t.Errorf("the stylesheet lacks %q", want)
			}
		}
	})
}

func TestCustomCSSAtTheEnd(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db)
		if err := s.SaveBranding(context.Background(), object("customCss", ".x { color: red }\n@import 'y';")); err != nil {
			t.Fatal(err)
		}
		css := mustCSS(t, s)
		if !strings.HasSuffix(css, "\n/* --- custom branding CSS --- */\n.x { color: red }\n\n") {
			t.Errorf("the stylesheet does not end with the custom CSS: %q", css[len(css)-80:])
		}
	})
}

func TestStoredValuesCannotLeaveTheirDeclaration(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db)
		ctx := context.Background()
		attack := "red;}</style><script>alert(1)</script>body{x:y"
		updates := object(
			"themeMode", "custom", "bgType", "gradient", "bgGradient", attack, "loginBgType", "color", "loginBgColor", attack,
			"rdclientBgType", "image", "rdclientBgImageUrl", `https://example.com/a")}body{x:y}\`, "glassEnabled", "true", "glassColor", attack,
			"fontHeading", "A'}\nbody{x:y}\\", "fontBody", `B"</style>`, "bgBlur", attack, "bgOverlay", attack, "glassBlur", attack,
			"colors", object("bgPrimary", attack, "accentBlueMuted", "#12;}"),
		)
		if err := s.SaveBranding(ctx, updates); err != nil {
			t.Fatal(err)
		}
		css := mustCSS(t, s)
		clean := "red/stylescriptalert(1)/scriptbodyxy"
		for _, want := range []string{
			"    --bg-primary: " + clean + ";\n",
			"    background: " + clean + ";\n",
			`    background: url("https://example.com/a%22)%7Dbody%7Bx:y%7D/");` + "\n",
			`    --font-heading: 'A\'}\a body{x:y}\\', sans-serif;` + "\n",
			`    --font-family: 'B\"</style>', -apple-system,`,
			"    --accent-blue-muted: #12;\n",
		} {
			if !strings.Contains(css, want) {
				t.Errorf("the stylesheet lacks %q in:\n%s", want, css)
			}
		}
		checkCSSStructure(t, css, "")
	})
}

func TestFontCSS(t *testing.T) {
	s := oracleService(t)
	escaped, _ := EscapeCSSString("Bad');}\nbody{color:red}\\Font\x00\x7f\"")
	if want := `Bad\');}\a body{color:red}\\Font\0 \7f \"`; escaped != want {
		t.Errorf("EscapeCSSString = %q, want %q", escaped, want)
	}
	css, err := s.GenerateFontCSS("Heading');}\nbody{color:red}", "Body\\Font")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`--font-heading: 'Heading\');}\a body{color:red}', sans-serif;`,
		`--font-family: 'Body\\Font', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;`,
		"\nh1, h2, h3, h4, h5, h6,\n.page-title,",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("font CSS lacks %q in:\n%s", want, css)
		}
	}
	if strings.Contains(css, "\nbody{color:red}") {
		t.Errorf("a line break of the font name reached the stylesheet:\n%s", css)
	}
	if css, _ := s.GenerateFontCSS("", "  "); css != "" {
		t.Errorf("no fonts: %q", css)
	}
	if css, _ := s.GenerateFontCSS("", "Body"); strings.Contains(css, "h1, h2") || !strings.HasPrefix(css, ":root {\n    --font-family: 'Body',") {
		t.Errorf("body font only: %q", css)
	}

	// an installed font is imported, once when it is both fonts
	dir := filepath.Join(s.opts.FontsDir, "my-font")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"font.css", "my-font-400.woff2", "my-font-700.woff2", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	css, _ = s.GenerateFontCSS(" My Font ", "My Font")
	if strings.Count(css, "@import url('/fonts/my-font/font.css');") != 1 || !strings.HasPrefix(css, "@import url('/fonts/my-font/font.css');\n\n:root {\n") {
		t.Errorf("import of an installed font:\n%s", css)
	}
	local, err := s.GetLocalFont("My  Font")
	if err != nil || Stringify(local) != `{"available":true,"cssPath":"/fonts/my-font/font.css","weights":["400","700"],"fileCount":2}` {
		t.Errorf("GetLocalFont = %s, %v", Stringify(local), err)
	}
	// a font whose name has no ASCII letter or digit is not installed, and
	// the stylesheet names it (BUGS BR2)
	if css, err := s.GenerateFontCSS("微软雅黑", "Шрифт"); err != nil || strings.Contains(css, "@import") ||
		!strings.Contains(css, "--font-heading: '微软雅黑', sans-serif;") || !strings.Contains(css, "--font-family: 'Шрифт', -apple-system") {
		t.Errorf("font names without ASCII: %q, %v", css, err)
	}
	if font, err := s.GetLocalFont("你好"); font != nil || err != nil {
		t.Errorf("GetLocalFont of a name without ASCII: %s, %v", Stringify(font), err)
	}
}

func TestFontNames(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Open Sans", "open-sans"}, {"  JetBrains   Mono ", "-jetbrains-mono-"}, {"A.B/C\\D", "abcd"}, {"Über Größe", "ber-gre"}, {"a\u00a0b\tc", "a-b-c"}, {"x--y", "x--y"},
	} {
		got, err := SanitizeFontName(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("SanitizeFontName(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	for _, in := range []any{"", "..", "/", "微软雅黑", nil, Undefined, 0.0} {
		if _, err := SanitizeFontName(in); Message(err) != "Invalid font family name" {
			t.Errorf("SanitizeFontName(%v): error %v, want Invalid font family name", in, err)
		}
	}
}

func TestFontFolderMustStayInside(t *testing.T) {
	s := oracleService(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "font.css"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.opts.FontsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s.opts.FontsDir, "escape")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	if _, err := s.GetLocalFont("escape"); Message(err) != "Path outside allowed directory" {
		t.Errorf("a font folder that links outside: %v", err)
	}
	if deleted, err := s.DeleteLocalFont("escape"); deleted || Message(err) != "Path outside allowed directory" {
		t.Errorf("deleting through the link: %v, %v", deleted, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "font.css")); err != nil {
		t.Errorf("the file outside is gone: %v", err)
	}
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "a\x00b"} {
		if err := try(func() { resolveChildPath(s.opts.FontsDir, name) }); Message(err) != "Invalid path segment" {
			t.Errorf("resolveChildPath(%q): %v", name, err)
		}
	}
	if err := try(func() { resolveChildPath(s.opts.FontsDir, "..hidden") }); Message(err) != "Path outside allowed directory" {
		t.Errorf("resolveChildPath(..hidden): %v", err)
	}
}

func TestFaviconAndLogo(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db)
		ctx := context.Background()
		file, err := os.ReadFile(filepath.Join("testdata", "console", "public", "favicon.svg"))
		if err != nil {
			t.Fatal(err)
		}
		// whatever the palette, the default favicon is the file
		setRows(t, s, [2]string{"colors", `{"accentBlue":"#123456","bgPrimary":"\"/><script>alert(1)</script>"}`})
		if got := s.GenerateFavicon(); got != string(file) || !strings.Contains(got, `viewBox="9.5 9.5 45 45"`) || strings.Contains(got, "#123456") {
			t.Errorf("default favicon:\n%s", got)
		}
		custom := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><circle cx="8" cy="8" r="7" fill="#ff0000"/></svg>`
		setRows(t, s, [2]string{"faviconSvg", custom})
		if got := s.GenerateFavicon(); got != custom {
			t.Errorf("own favicon: %s", got)
		}
		setRows(t, s, [2]string{"faviconSvg", "  \n"})
		if got := s.GenerateFavicon(); got != string(file) {
			t.Errorf("a blank favicon setting does not give the default")
		}

		if DefaultBranding().Text("logoType") != "default" {
			t.Errorf("default logoType = %q", DefaultBranding().Text("logoType"))
		}
		if err := s.ResetBranding(ctx); err != nil {
			t.Fatal(err)
		}
		if got := s.GetBranding().Text("logoType"); got != "default" {
			t.Errorf("logoType after reset = %q", got)
		}
		for _, tc := range []struct {
			rows [][2]string
			want string
		}{
			{[][2]string{{"logoType", "icon"}, {"logoIcon", "dns"}}, "default"},
			{[][2]string{{"logoType", "image"}, {"logoUrl", "/img/betterdesk_icon.png"}}, "default"},
			{[][2]string{{"logoType", "image"}, {"logoUrl", ""}}, "default"},
			{[][2]string{{"logoType", "svg"}, {"logoSvg", ""}, {"logoIcon", " "}}, "default"},
			{[][2]string{{"logoType", "text"}, {"logoText", "Acme"}}, "text"},
			{[][2]string{{"logoType", "icon"}, {"logoIcon", "cloud"}}, "icon"},
			{[][2]string{{"logoType", "image"}, {"logoUrl", "/uploads/logo-abc.png"}}, "image"},
			{[][2]string{{"logoType", "svg"}, {"logoSvg", `<svg viewBox="0 0 4 4"><rect width="4" height="4"/></svg>`}}, "svg"},
		} {
			setRows(t, s, tc.rows...)
			if got := s.GetBranding().Text("logoType"); got != tc.want {
				t.Errorf("rows %v: logoType = %q, want %q", tc.rows, got, tc.want)
			}
		}
	})
}

func TestFaviconFallback(t *testing.T) {
	opts := fixtureOptions(t.TempDir())
	opts.FaviconFile = filepath.Join(t.TempDir(), "missing.svg")
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.GenerateFavicon(); got != defaultFaviconFallback || !strings.HasPrefix(got, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="9.5 9.5 45 45">`) {
		t.Errorf("favicon without the file: %s", got)
	}
}

func TestAppearanceAndReadability(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db)
		ctx := context.Background()
		s.LoadBranding(ctx)
		model, err := s.GetAppearanceModel(nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := keyList(model); got != `["version","type","revision","identity","typography","palette","backgrounds","surfaces","login","footer","advanced"]` {
			t.Errorf("sections of the appearance model: %s", got)
		}
		if model.Text("type") != "betterdesk-appearance" || model.Text("version") != "2.0" || model.Text("revision") != s.Revision() {
			t.Errorf("head of the appearance model: %s", clip(Stringify(model)))
		}
		if got := Stringify(property(model, "backgrounds")); got != `{"console":{"type":"none","color":"","gradient":"","imageUrl":"","blur":"","overlay":"","size":"cover"},`+
			`"login":{"type":"inherit","color":"","gradient":"","imageUrl":"","overlay":"","size":"cover"},`+
			`"agent":{"type":"none","color":"","gradient":"","imageUrl":"","overlay":"","size":"cover"},`+
			`"rdclient":{"type":"inherit","color":"","gradient":"","imageUrl":"","overlay":"","size":"cover"}}` {
			t.Errorf("default backgrounds: %s", got)
		}

		public, err := s.GetPublicAppearance()
		if err != nil {
			t.Fatal(err)
		}
		if got := keyList(public); got != `["version","revision","product","identity","palette","surfaces","background","readability"]` {
			t.Errorf("keys of the public appearance: %s", got)
		}
		if got := Stringify(property(public, "palette")); got != `{"mode":"system","primary":"#58a6ff","primaryHover":"#79c0ff","background":"#0d1117","surface":"#161b22","surfaceRaised":"#21262d","text":"#e6edf3","muted":"#8b949e","border":"#30363d","danger":"#f85149","warning":"#d29922","success":"#2ea44f"}` {
			t.Errorf("default public palette: %s", got)
		}
		if strings.Contains(Stringify(public), "customCss") {
			t.Errorf("the public appearance mentions the custom CSS")
		}

		low := DefaultBranding()
		colors, _ := low.Get("colors")
		for key, value := range map[string]string{"bgPrimary": "#000000", "bgSecondary": "#000000", "textPrimary": "#111111", "textSecondary": "#767676", "accentBlue": "#ffff00"} {
			colors.(*Object).Set(key, value)
		}
		low.Set("bgType", "image")
		low.Set("bgOverlay", "24.9")
		low.Set("loginBgType", "gradient")
		low.Set("loginBgOverlay", "25")
		readability, err := s.AssessAppearanceReadability(low)
		if err != nil {
			t.Fatal(err)
		}
		want := `{"ok":false,"issues":[` +
			`{"id":"page-text","severity":"error","ratio":1.11,"minimum":4.5,"messageKey":"branding.readability_contrast","messageParams":{"item":"page-text","ratio":"1.11","minimum":4.5},"message":"page-text contrast ratio 1.11 is below 4.5:1"},` +
			`{"id":"card-text","severity":"error","ratio":1.11,"minimum":4.5,"messageKey":"branding.readability_contrast","messageParams":{"item":"card-text","ratio":"1.11","minimum":4.5},"message":"card-text contrast ratio 1.11 is below 4.5:1"},` +
			`{"id":"button-primary","severity":"error","ratio":1.07,"minimum":3,"messageKey":"branding.readability_contrast","messageParams":{"item":"button-primary","ratio":"1.07","minimum":3},"message":"button-primary contrast ratio 1.07 is below 3:1"},` +
			`{"id":"console-background","severity":"warning","messageKey":"branding.readability_background","messageParams":{"item":"console-background","type":"image"},"message":"console-background uses image with a low overlay; text may be hard to read."}]}`
		if Stringify(readability) != want {
			t.Errorf("readability\n got  %s\n want %s", Stringify(readability), want)
		}
		good, _ := s.AssessAppearanceReadability(mustParse(t, `{"colors":{"textPrimary":"#ffffff","bgPrimary":"#000000","bgSecondary":"#767676","textSecondary":"#ffffff","accentBlue":"#0000ff"}}`))
		if Stringify(good) != `{"ok":true,"issues":[]}` {
			t.Errorf("readability of a readable palette: %s", Stringify(good))
		}
		if ratio, ok := contrastRatio("#000000", "#ffffff"); !ok || ratio != 21 {
			t.Errorf("contrast of black on white = %v, %v", ratio, ok)
		}
	})
}

func TestExportAndImport(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db)
		ctx := context.Background()
		if err := s.SaveBranding(ctx, object("appName", "Exported", "themeMode", "custom", "colors", object("bgPrimary", "#010203"), "bgType", "color", "bgColor", "#040506", "customCss", ".a{}")); err != nil {
			t.Fatal(err)
		}
		preset, err := s.ExportPreset()
		if err != nil {
			t.Fatal(err)
		}
		if got := keyList(preset); got != `["version","type","appearance","branding"]` || preset.Text("version") != "2.0" || preset.Text("type") != "betterdesk-theme" {
			t.Errorf("export: keys %s", got)
		}
		exported := mustParse(t, Stringify(preset))
		if err := s.ResetBranding(ctx); err != nil {
			t.Fatal(err)
		}
		if ok, err := s.ImportPreset(ctx, exported); !ok || err != nil {
			t.Fatalf("import of an export: %v, %v", ok, err)
		}
		b := s.GetBranding()
		colors, _ := b.Get("colors")
		// An import reads the appearance part of an export and finds only
		// the settings whose names are the same in both shapes: identity,
		// fonts, colours, glass. Theme mode, backgrounds, sign-in texts and
		// custom CSS do not come back (BUGS BR5).
		if b.Text("appName") != "Exported" || colors.(*Object).Text("bgPrimary") != "#010203" {
			t.Errorf("after the import, what should have come back: %s", clip(Stringify(b)))
		}
		if b.Text("themeMode") != "system" || b.Text("bgType") != "none" || b.Text("bgColor") != "" || b.Text("customCss") != "" {
			t.Errorf("after the import, what the Node console lost too: themeMode %q, bgType %q, bgColor %q, customCss %q",
				b.Text("themeMode"), b.Text("bgType"), b.Text("bgColor"), b.Text("customCss"))
		}
		// with the flat part alone everything comes back
		if ok, err := s.ImportPreset(ctx, object("type", "betterdesk-theme", "branding", property(exported, "branding"))); !ok || err != nil {
			t.Fatalf("import of the flat part: %v, %v", ok, err)
		}
		if b := s.GetBranding(); b.Text("themeMode") != "custom" || b.Text("bgType") != "color" || b.Text("bgColor") != "#040506" || b.Text("customCss") != ".a{}" {
			t.Errorf("after the import of the flat part: %s", clip(Stringify(b)))
		}
		// the flat part is used when there is no appearance part
		flatOnly := object("type", "betterdesk-theme", "branding", object("appName", strings.Repeat("n", 600), "customCss", ".b{}", "logoSvg", strings.Repeat("<g>", 20000)))
		if ok, err := s.ImportPreset(ctx, flatOnly); !ok || err != nil {
			t.Fatal(ok, err)
		}
		b = s.GetBranding()
		if len(b.Text("appName")) != 500 || b.Text("customCss") != ".b{}" || len(b.Text("logoSvg")) != 50000 {
			t.Errorf("length limits of an import: appName %d, customCss %q, logoSvg %d", len(b.Text("appName")), b.Text("customCss"), len(b.Text("logoSvg")))
		}
		for _, bad := range []any{nil, "x", 5.0, object(), object("type", "betterdesk-theme"), object("type", "other", "branding", object()), []any{}} {
			if ok, err := s.ImportPreset(ctx, bad); ok || err != nil {
				t.Errorf("ImportPreset(%s) = %v, %v; want false", Stringify(bad), ok, err)
			}
		}
		_, err = s.ImportPreset(ctx, mustParse(t, `{"type":"betterdesk-theme","branding":{"colors":"red"}}`))
		if Message(err) != "Cannot use 'in' operator to search for 'bgPrimary' in red" {
			t.Errorf("import with colours that are text: %v", err)
		}
	})
}

func TestRevision(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db)
		ctx := context.Background()
		clock := strconv.FormatInt(fixedNow.UnixMilli(), 36)
		if got := s.Revision(); got != "0" {
			t.Errorf("revision before the first load = %q, want 0", got)
		}
		s.LoadBranding(ctx)
		if got := s.Revision(); got != clock {
			t.Errorf("revision of an empty table = %q, want the time %q", got, clock)
		}
		if err := s.SaveBranding(ctx, object("appName", "X")); err != nil {
			t.Fatal(err)
		}
		format := `^\d{4}-\d\d-\d\d\d{6}$`
		if db.IsPostgres() {
			format = `^(Mon|Tue|Wed|Thu|Fri|Sat|Sun)(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\d{2}\d{4}\d{6}GMT0000CoordinatedUniversalTime$`
		}
		saved := s.Revision()
		if !regexp.MustCompile(format).MatchString(saved) {
			t.Errorf("revision after a save = %q, want the form %s", saved, format)
		}
		// the same on a fresh service: it comes from the table, not from memory
		again := newService(t, db)
		again.LoadBranding(ctx)
		if again.Revision() != saved {
			t.Errorf("revision of a second service = %q, want %q", again.Revision(), saved)
		}
		if err := s.ResetBranding(ctx); err != nil {
			t.Fatal(err)
		}
		if got := s.Revision(); got != clock {
			t.Errorf("revision after a reset = %q, want the time %q", got, clock)
		}
	})
}

func TestJSDateString(t *testing.T) {
	berlin := time.FixedZone("CET", 3600)
	long := func(time.Time) string { return "Central European Standard Time" }
	for _, tc := range []struct {
		when time.Time
		zone func(time.Time) string
		want string
	}{
		{time.Date(2026, 11, 10, 1, 5, 51, 694e6, time.UTC), defaultZoneName, "Tue Nov 10 2026 01:05:51 GMT+0000 (Coordinated Universal Time)"},
		{time.Date(2026, 1, 2, 23, 59, 59, 0, time.UTC).In(berlin), long, "Sat Jan 03 2026 00:59:59 GMT+0100 (Central European Standard Time)"},
		{time.Date(2026, 1, 2, 23, 59, 59, 0, time.UTC).In(berlin), defaultZoneName, "Sat Jan 03 2026 00:59:59 GMT+0100 (CET)"},
		{time.Date(2026, 7, 4, 12, 0, 0, 0, time.FixedZone("", -9*3600-30*60)), defaultZoneName, "Sat Jul 04 2026 12:00:00 GMT-0930 ()"},
		{time.Date(999, 12, 31, 0, 0, 0, 0, time.UTC), defaultZoneName, "Tue Dec 31 0999 00:00:00 GMT+0000 (Coordinated Universal Time)"},
	} {
		if got := jsDateString(tc.when, tc.zone); got != tc.want {
			t.Errorf("jsDateString(%v) = %q, want %q", tc.when, got, tc.want)
		}
	}
}

func TestLoadFallsBackToDefaults(t *testing.T) {
	db := storetest.Migrated(t)
	s := newService(t, db)
	ctx := context.Background()
	if err := s.SaveBranding(ctx, object("appName", "Stored")); err != nil {
		t.Fatal(err)
	}
	var logged []string
	s.opts.Logf = func(format string, args ...any) { logged = append(logged, format) }
	if _, err := db.Exec(ctx, "ALTER TABLE branding_config RENAME TO branding_config_gone"); err != nil {
		t.Fatal(err)
	}
	got := s.LoadBranding(ctx)
	if got.Text("appName") != "BetterDesk" || s.GetBranding().Text("appName") != "BetterDesk" {
		t.Errorf("settings without the table: appName %q", got.Text("appName"))
	}
	if s.Revision() != strconv.FormatInt(fixedNow.UnixMilli(), 36) {
		t.Errorf("revision without the table = %q", s.Revision())
	}
	if len(logged) != 1 || !strings.HasPrefix(logged[0], "[Branding] Failed to load from DB, using defaults") {
		t.Errorf("log lines: %q", logged)
	}
	if err := s.SaveBranding(ctx, object("appName", "X")); err == nil {
		t.Errorf("a save without the table did not fail")
	}
}

// A settings table from before 3.0 has no updated_at column, and the
// console cannot add it (BUGS D17): saves fail there and the revision is
// the time.
func TestTableWithoutUpdatedAt(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	for _, stmt := range []string{"DROP TABLE branding_config", "CREATE TABLE branding_config (key TEXT PRIMARY KEY, value TEXT NOT NULL)", "INSERT INTO branding_config (key, value) VALUES ('appName', 'Old')"} {
		if _, err := db.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	s := newService(t, db)
	s.LoadBranding(ctx)
	if got := s.GetBranding().Text("appName"); got != "Old" {
		t.Errorf("appName = %q, want the stored one", got)
	}
	if s.Revision() != strconv.FormatInt(fixedNow.UnixMilli(), 36) {
		t.Errorf("revision = %q, want the time", s.Revision())
	}
	if err := s.SaveBranding(ctx, object("appName", "New")); err == nil {
		t.Errorf("a save into a table without updated_at did not fail")
	}
}

func TestOddRows(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db)
		setRows(t, s,
			[2]string{"colors", `{"bgPrimary":"#111111","extra":"kept","__proto__":{"x":1},"0":"zero"}`},
			[2]string{"unknownKey", "ignored"}, [2]string{"constructor", "kept as a key"}, [2]string{"__proto__", "ignored"},
			[2]string{"loginPromoMode", "SINGLE"}, [2]string{"loginSlides", "not json"}, [2]string{"appName", ""},
		)
		b := s.GetBranding()
		colors, _ := b.Get("colors")
		keys := colors.(*Object).Keys()
		if keys[0] != "0" || keys[len(keys)-1] != "extra" || colors.(*Object).Has("__proto__") || colors.(*Object).Text("bgPrimary") != "#111111" {
			t.Errorf("colours: %s", Stringify(colors))
		}
		// a key every JavaScript object has is no setting either (BUGS BR12)
		if all := b.Keys(); b.Has("unknownKey") || b.Has("__proto__") || b.Has("constructor") || len(all) != len(DefaultBranding().Keys()) {
			t.Errorf("keys of the settings: %s", Stringify(all))
		}
		if b.Text("loginPromoMode") != "single" || b.Text("appName") != "" {
			t.Errorf("mode %q, name %q", b.Text("loginPromoMode"), b.Text("appName"))
		}
		if slides, _ := b.Get("loginSlides"); Stringify(slides) != Stringify(DefaultLoginSlides()) {
			t.Errorf("slides from text that is no JSON: %s", Stringify(slides))
		}
		// colours that are no JSON leave the defaults
		setRows(t, s, [2]string{"colors", "{"})
		colors, _ = s.GetBranding().Get("colors")
		if colors.(*Object).Len() != len(colorKeys) || colors.(*Object).Text("bgPrimary") != "" {
			t.Errorf("colours from broken JSON: %s", Stringify(colors))
		}
		// a slide list that cannot be read keeps the default slides and
		// takes nothing else with it (BUGS BR12)
		setRows(t, s, [2]string{"appName", "Kept"}, [2]string{"loginSlides", `[{"imageUrl":{"toString":1}}]`}, [2]string{"footerText", "Also kept"})
		b = s.GetBranding()
		if slides, _ := b.Get("loginSlides"); b.Text("appName") != "Kept" || b.Text("footerText") != "Also kept" || Stringify(slides) != Stringify(DefaultLoginSlides()) {
			t.Errorf("settings with a slide list that cannot be read: %s %s %s", b.Text("appName"), b.Text("footerText"), Stringify(slides))
		}
	})
}

func TestProfiles(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db)
		ctx := context.Background()
		s.LoadBranding(ctx)

		if _, err := s.CreateProfile(ctx, "   ", "", nil); Message(err) != "Profile name is required" {
			t.Errorf("a blank name: %v", err)
		}
		first, err := s.CreateProfile(ctx, "  "+strings.Repeat("n", 90)+"  ", strings.Repeat("d", 250), nil)
		if err != nil || first != 1 {
			t.Fatalf("create: %d, %v", first, err)
		}
		theme := mustParse(t, `{"version":"1.0","type":"betterdesk-theme","branding":{"appName":"From Profile","themeMode":"light"}}`)
		second, err := s.CreateProfile(ctx, "Default", "", theme)
		if err != nil || second != 2 {
			t.Fatalf("create: %d, %v", second, err)
		}
		// a name that is taken, told by the database's error code (BUGS BR11)
		if _, err = s.CreateProfile(ctx, "Default", "", nil); err != ErrProfileNameTaken {
			t.Errorf("a name that is taken: %v", err)
		}

		list, err := s.ListProfiles(ctx)
		if err != nil || len(list) != 2 {
			t.Fatalf("list: %v, %v", list, err)
		}
		// sorted by name; the long name was cut at 80, the description at 200
		if list[0].Name != "Default" || list[1].Name != strings.Repeat("n", 80) || len(list[1].Description.String) != 200 || list[0].IsActive.True() {
			t.Errorf("list: %s", clip(Stringify([]any{list[0], list[1]})))
		}
		timeForm := `"\d{4}-\d\d-\d\d \d\d:\d\d:\d\d"`
		if db.IsPostgres() {
			timeForm = `"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z"`
		}
		if got := Stringify(list[0]); !regexp.MustCompile(`^\{"id":2,"name":"Default","description":"","is_active":0,"created_at":` + timeForm + `,"updated_at":` + timeForm + `\}$`).MatchString(got) {
			t.Errorf("a profile in JSON: %s", got)
		}

		// the profile made without a theme holds the settings of that moment
		row, _, _ := getBrandingProfile(ctx, db, first)
		if !strings.HasPrefix(row.Data, `{"version":"2.0","type":"betterdesk-theme","appearance":{"version":"2.0","type":"betterdesk-appearance","revision":"`) || !strings.Contains(row.Data, `"branding":{"appName":"BetterDesk"`) {
			t.Errorf("stored settings of a profile: %s", clip(row.Data))
		}

		if applied, err := s.ApplyProfile(ctx, 99); applied || err != nil {
			t.Errorf("apply a missing profile: %v, %v", applied, err)
		}
		if applied, err := s.ApplyProfile(ctx, second); !applied || err != nil {
			t.Fatalf("apply: %v, %v", applied, err)
		}
		if b := s.GetBranding(); b.Text("appName") != "From Profile" || b.Text("themeMode") != "light" {
			t.Errorf("settings after applying a profile: %s", clip(Stringify(b)))
		}
		list, _ = s.ListProfiles(ctx)
		if !list[0].IsActive.True() || list[1].IsActive.True() {
			t.Errorf("the applied profile is not the only active one: %s", Stringify([]any{list[0].IsActive, list[1].IsActive}))
		}
		if _, err := s.DeleteProfile(ctx, second); Message(err) != "Cannot delete the active profile" {
			t.Errorf("delete the active profile: %v", err)
		}
		if deleted, err := s.DeleteProfile(ctx, 99); deleted || err != nil {
			t.Errorf("delete a missing profile: %v, %v", deleted, err)
		}

		// duplicates: "(copy)", then a number
		for i, want := range []string{"Default (copy)", "Default (copy) 2", "Default (copy) 3"} {
			id, err := s.DuplicateProfile(ctx, second, "")
			if err != nil {
				t.Fatal(err)
			}
			copyRow, found, _ := getBrandingProfile(ctx, db, id)
			if !found || copyRow.Name != want || copyRow.IsActive.True() {
				t.Errorf("duplicate %d is named %q (active %v), want %q", i, copyRow.Name, copyRow.IsActive.True(), want)
			}
			if copyRow.Data != `{"version":"1.0","type":"betterdesk-theme","branding":{"appName":"From Profile","themeMode":"light"}}` {
				t.Errorf("duplicate %d holds %s", i, copyRow.Data)
			}
		}
		if id, err := s.DuplicateProfile(ctx, second, "  Own Name "); err != nil {
			t.Fatal(err)
		} else if row, _, _ := getBrandingProfile(ctx, db, id); row.Name != "Own Name" {
			t.Errorf("duplicate with a name is called %q", row.Name)
		}
		if _, err := s.DuplicateProfile(ctx, 99, ""); Message(err) != "Profile not found" {
			t.Errorf("duplicate a missing profile: %v", err)
		}
		for _, name := range []any{5.0, nil, []any{"x"}} {
			if _, err := s.DuplicateProfile(ctx, second, name); err != ErrInvalidProfileName {
				t.Errorf("duplicate with %s as name: %v", Stringify(name), err)
			}
		}

		// updates: what is not given stays
		if err := s.UpdateProfile(ctx, second, ProfileUpdate{Description: "now described"}); err != nil {
			t.Fatal(err)
		}
		row, _, _ = getBrandingProfile(ctx, db, second)
		if row.Name != "Default" || row.Description.String != "now described" || !strings.Contains(row.Data, "From Profile") {
			t.Errorf("after a description update: %q, %q, %s", row.Name, row.Description.String, row.Data)
		}
		if err := s.UpdateProfile(ctx, second, ProfileUpdate{Name: " Renamed ", Branding: object("appName", "Changed")}); err != nil {
			t.Fatal(err)
		}
		row, _, _ = getBrandingProfile(ctx, db, second)
		if row.Name != "Renamed" || row.Data != `{"version":"1.0","type":"betterdesk-theme","branding":{"appName":"Changed"}}` {
			t.Errorf("after a rename with settings: %q, %s", row.Name, row.Data)
		}
		if err := s.UpdateProfile(ctx, 99, ProfileUpdate{}); Message(err) != "Profile not found" {
			t.Errorf("update a missing profile: %v", err)
		}
		if _, err := db.Exec(ctx, "UPDATE branding_profiles SET data = 'xyz' WHERE id = ?", first); err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateProfile(ctx, first, ProfileUpdate{Name: "x"}); err != ErrUnreadableProfile {
			t.Errorf("update a profile whose settings are no JSON: %v", err)
		}
		if err := s.UpdateProfile(ctx, first, ProfileUpdate{Name: "Renamed", Branding: object("appName", "x")}); err != ErrProfileNameTaken {
			t.Errorf("rename to a taken name: %v", err)
		}
		if err := s.UpdateProfile(ctx, first, ProfileUpdate{Description: mustParse(t, `{"toString":1}`)}); err != ErrInvalidProfileDescription {
			t.Errorf("a description that is no text: %v", err)
		}
		if applied, err := s.ApplyProfile(ctx, first); applied || err != nil {
			t.Errorf("apply a profile whose settings are no JSON: %v, %v", applied, err)
		}

		if db.IsPostgres() {
			if _, err := s.ApplyProfile(ctx, 3000000000); Message(err) != `value "3000000000" is out of range for type integer` {
				t.Errorf("an id beyond the column: %v", err)
			}
		} else if applied, err := s.ApplyProfile(ctx, 3000000000); applied || err != nil {
			t.Errorf("an id nobody has: %v, %v", applied, err)
		}
	})
}

func TestUploads(t *testing.T) {
	db := storetest.Migrated(t)
	s := newService(t, db)
	ctx := context.Background()
	s.LoadBranding(ctx)

	for mime, want := range map[string]bool{
		"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/svg+xml": true,
		"image/PNG": false, "image/bmp": false, "image/svg": false, "text/html": false, "": false, "image/png; x": false, " image/png": false, "image/pngx": false,
	} {
		if got := AcceptImageUpload(mime); got != want {
			t.Errorf("AcceptImageUpload(%q) = %v, want %v", mime, got, want)
		}
	}
	for _, tc := range []struct {
		name, mime string
		want       bool
	}{
		{"a.ttf", "", true}, {"a.WOFF2", "x/y", true}, {"a.otf", "font/ttf", true}, {"a.bin", "font/woff2", true}, {"a", "application/font-woff2", true}, {"a", "application/x-font-ttf", true},
		{"a.otf", "font/otf", false}, {"a.woff", "font/woff", false}, {"a.ttf.png", "image/png", false}, {"ttf", "", false}, {"a.ttf ", "", false}, {"a.ſvg", "FONT/TTF", false},
	} {
		if got := AcceptFontUpload(tc.name, tc.mime); got != tc.want {
			t.Errorf("AcceptFontUpload(%q, %q) = %v, want %v", tc.name, tc.mime, got, tc.want)
		}
	}

	// the test's random source counts up from 0123456789abcdef
	for i, tc := range []struct{ mimeType, ext string }{
		{"image/png", ".png"}, {"image/jpeg", ".jpg"}, {"image/gif", ".gif"}, {"image/webp", ".webp"}, {"image/svg+xml", ".svg"}, {"image/png", ".png"},
	} {
		want := "logo-0123456789abcde" + string("f0123456789"[i]) + tc.ext
		if i > 0 {
			want = "logo-0123456789abcdf" + string("0123456789"[i-1]) + tc.ext
		}
		name, err := s.StoreLogo(tc.mimeType, strings.NewReader("data"))
		if err != nil || name != want {
			t.Errorf("StoreLogo(%q) = %q, %v; want %q", tc.mimeType, name, err, want)
		}
		if data, err := os.ReadFile(filepath.Join(s.opts.UploadsDir, name)); err != nil || string(data) != "data" {
			t.Errorf("the stored logo %s: %q, %v", name, data, err)
		}
	}
	if name, err := s.StoreBackground("image/jpeg", strings.NewReader("x")); err != nil || name != "bg-0123456789abcdf5.jpg" {
		t.Errorf("StoreBackground = %q, %v", name, err)
	}

	// the limits: exactly the limit is fine, one byte more is not and leaves no file
	before, _ := os.ReadDir(s.opts.UploadsDir)
	if _, err := s.StoreLogo("image/png", bytes.NewReader(make([]byte, LogoMaxBytes+1))); !errors.Is(err, ErrFileTooLarge) {
		t.Errorf("a logo over the limit: %v", err)
	}
	if _, err := s.StoreBackground("image/jpeg", bytes.NewReader(make([]byte, BackgroundMaxBytes+1))); !errors.Is(err, ErrFileTooLarge) {
		t.Errorf("a background over the limit: %v", err)
	}
	if after, _ := os.ReadDir(s.opts.UploadsDir); len(after) != len(before) {
		t.Errorf("a refused upload left a file: %d entries, were %d", len(after), len(before))
	}
	if name, err := s.StoreLogo("image/gif", bytes.NewReader(make([]byte, LogoMaxBytes))); err != nil {
		t.Errorf("a logo of exactly the limit: %v", err)
	} else if info, _ := os.Stat(filepath.Join(s.opts.UploadsDir, name)); info.Size() != LogoMaxBytes {
		t.Errorf("stored %d bytes of %d", info.Size(), LogoMaxBytes)
	}
	if LogoMaxBytes != 2<<20 || BackgroundMaxBytes != 8<<20 || FontMaxBytes != 5<<20 {
		t.Errorf("limits: %d, %d, %d", LogoMaxBytes, BackgroundMaxBytes, FontMaxBytes)
	}
}

func TestRemovePreviousLogo(t *testing.T) {
	db := storetest.Migrated(t)
	s := newService(t, db)
	ctx := context.Background()
	put := func(name string) string {
		path := filepath.Join(s.opts.UploadsDir, name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for _, tc := range []struct {
		logoURL, file, newName string
		removed                bool
	}{
		{"/uploads/logo-00000000000000aa.png", "logo-00000000000000aa.png", "logo-new.png", true},
		{"/uploads/logo-00000000000000AB.SVG", "logo-00000000000000AB.SVG", "logo-new.png", true},
		{"/uploads/x/logo-00000000000000ac.jpeg/", "logo-00000000000000ac.jpeg", "logo-new.png", true},
		{"/uploads/logo-00000000000000ad.png", "logo-00000000000000ad.png", "logo-00000000000000ad.png", false},
		{"/uploads/logo-00000000000000ae.html", "logo-00000000000000ae.html", "logo-new.png", false},
		{"/uploads/logo-short.png", "logo-short.png", "logo-new.png", false},
		{"/uploads/bg-00000000000000af.png", "bg-00000000000000af.png", "logo-new.png", false},
		{"https://example.com/uploads/logo-00000000000000b0.png", "logo-00000000000000b0.png", "logo-new.png", false},
		{"/img/logo-00000000000000b1.png", "logo-00000000000000b1.png", "logo-new.png", false},
	} {
		path := put(tc.file)
		if err := s.SaveBranding(ctx, object("logoUrl", tc.logoURL)); err != nil {
			t.Fatal(err)
		}
		s.RemovePreviousLogo(tc.newName)
		if removed := !exists(path); removed != tc.removed {
			t.Errorf("logoUrl %q, new logo %q: removed = %v, want %v", tc.logoURL, tc.newName, removed, tc.removed)
		}
	}
}

func TestListBackgrounds(t *testing.T) {
	s := oracleService(t)
	for name, age := range map[string]time.Duration{
		"bg-0123456789abcdef.png": 3 * time.Hour, "bg-0123456789ABCDEF.JPEG": time.Hour, "bg-00000000000000aa.webp": 2 * time.Hour,
		"bg-0123456789abcdef.svg": 0, "bg-123.png": 0, "logo-0123456789abcdef.png": 0, "bg-0123456789abcdeg.png": 0, "xbg-0123456789abcdef.png": 0, "bg-0123456789abcdef.png.bak": 0,
	} {
		path := filepath.Join(s.opts.UploadsDir, name)
		if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		when := time.Date(2026, 1, 2, 3, 4, 5, 678_500_000, time.UTC).Add(-age)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(s.opts.UploadsDir, "bg-00000000000000bb.png"), 0o755); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListBackgrounds()
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"name":"bg-0123456789ABCDEF.JPEG","url":"/uploads/bg-0123456789ABCDEF.JPEG","size":24,"updatedAt":"2026-01-02T02:04:05.679Z"},` +
		`{"name":"bg-00000000000000aa.webp","url":"/uploads/bg-00000000000000aa.webp","size":24,"updatedAt":"2026-01-02T01:04:05.679Z"},` +
		`{"name":"bg-0123456789abcdef.png","url":"/uploads/bg-0123456789abcdef.png","size":23,"updatedAt":"2026-01-02T00:04:05.679Z"}]`
	if Stringify(list) != want {
		t.Errorf("backgrounds\n got  %s\n want %s", Stringify(list), want)
	}
	if err := os.RemoveAll(s.opts.UploadsDir); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListBackgrounds(); err == nil || !strings.HasPrefix(Message(err), "ENOENT: no such file or directory, scandir '") {
		t.Errorf("backgrounds without the folder: %v", err)
	}
}

func TestApplyBackground(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db)
		ctx := context.Background()
		for target, typeKey := range map[string]string{"bgImageUrl": "bgType", "loginBgImageUrl": "loginBgType", "agentBgImageUrl": "agentBgType", "rdclientBgImageUrl": "rdclientBgType"} {
			branding, applied, err := s.ApplyBackground(ctx, target, "/uploads/bg-1.png")
			if err != nil || !applied || branding.Text(target) != "/uploads/bg-1.png" || branding.Text(typeKey) != "image" {
				t.Errorf("ApplyBackground(%s): %v, %v, %s=%q %s=%q", target, applied, err, target, branding.Text(target), typeKey, branding.Text(typeKey))
			}
		}
		// also a name every JavaScript object has is no target (BUGS BR10)
		for _, target := range []string{"", "logoUrl", "bgType", "BGIMAGEURL", " bgImageUrl", "constructor", "__proto__", "toString", "hasOwnProperty"} {
			if branding, applied, err := s.ApplyBackground(ctx, target, "/uploads/bg-2.png"); applied || err != nil || branding != nil {
				t.Errorf("ApplyBackground(%q) = %v, %v", target, applied, err)
			}
		}
	})
}

func TestThemes(t *testing.T) {
	db := storetest.Migrated(t)
	s := newService(t, db)
	ctx := context.Background()
	list, err := s.ListThemes()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, theme := range list {
		o := theme.(*Object)
		names[o.Text("id")] = o.Text("name")
		if got := keyList(o); got != `["id","name","description","colors"]` {
			t.Errorf("keys of a theme: %s", got)
		}
	}
	if names["default"] != "BetterDesk Dark" || names["light"] != "BetterDesk Light" || names["insolve"] != "insolve Core Server" {
		t.Errorf("built-in themes: %v", names)
	}
	if result, err := s.ApplyTheme(ctx, "light"); result != ThemeApplied || err != nil {
		t.Fatalf("apply light: %v, %v", result, err)
	}
	// the built-in themes bring colours and names, no theme mode
	colors, _ := s.GetBranding().Get("colors")
	if got := colors.(*Object).Text("bgSecondary"); got != "#f6f8fa" || s.GetBranding().Text("themeMode") != "system" {
		t.Errorf("after applying the light theme: bgSecondary %q, themeMode %q", got, s.GetBranding().Text("themeMode"))
	}
	for id, want := range map[string]ThemeResult{"missing": ThemeNotFound, "../light": ThemeInvalidID, "a b": ThemeInvalidID, "": ThemeInvalidID, "light.json": ThemeInvalidID} {
		if result, err := s.ApplyTheme(ctx, id); result != want || err != nil {
			t.Errorf("ApplyTheme(%q) = %v, %v; want %v", id, result, err, want)
		}
	}
	missing := newService(t, db)
	missing.opts.ThemesDir = filepath.Join(t.TempDir(), "none")
	if list, err := missing.ListThemes(); err != nil || len(list) != 0 {
		t.Errorf("themes without the folder: %v, %v", list, err)
	}
}

func TestSupporters(t *testing.T) {
	s := oracleService(t)
	got := s.LoadSupporters()
	if keyList(got) != `["honorary","individuals","links"]` {
		t.Errorf("keys: %s", keyList(got))
	}
	honorary, _ := got.Get("honorary")
	if list := honorary.([]any); len(list) == 0 || list[0].(*Object).Text("name") != "INSOLVE" {
		t.Errorf("honorary supporters: %s", Stringify(honorary))
	}
	links, _ := got.Get("links")
	if keyList(links.(*Object)) != `["repo","issues","sponsors","buyMeACoffee","sponsorsMd"]` {
		t.Errorf("links: %s", Stringify(links))
	}
	if s.LoadSupporters() != got {
		t.Errorf("a second call read the file again")
	}
	s.opts.SupportersFile = filepath.Join(t.TempDir(), "missing.json")
	if s.LoadSupporters() != got {
		t.Errorf("the kept answer was dropped without a reset")
	}
	var logged int
	s.opts.Logf = func(string, ...any) { logged++ }
	s.ResetSupportersCache()
	empty := s.LoadSupporters()
	if Stringify(empty) != `{"honorary":[],"individuals":[],"links":{"repo":"https://github.com/UNITRONIX/BetterDesk","issues":"https://github.com/UNITRONIX/BetterDesk/issues","sponsors":"https://github.com/sponsors/UNITRONIX","buyMeACoffee":"https://buymeacoffee.com/unitronix","sponsorsMd":"https://github.com/UNITRONIX/BetterDesk/blob/main/SPONSORS.md"}}` || logged != 1 {
		t.Errorf("supporters without the file: %s (%d log lines)", Stringify(empty), logged)
	}
}

func TestNewNeedsThePalettes(t *testing.T) {
	opts := fixtureOptions(t.TempDir())
	opts.PalettesFile = filepath.Join(t.TempDir(), "missing.json")
	if _, err := New(opts); err == nil {
		t.Errorf("New without the palettes file did not fail")
	}
	broken := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(broken, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts.PalettesFile = broken
	if _, err := New(opts); err == nil {
		t.Errorf("New with broken palettes did not fail")
	}
	opts = fixtureOptions(t.TempDir())
	if _, err := New(opts); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(opts.UploadsDir); err != nil || !info.IsDir() {
		t.Errorf("New did not create the uploads folder: %v", err)
	}
}

func TestJSON(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`{"b":1,"a":[true,null,1.50,1e21,1e-7,-0],"10":"x","2":"y","b":2}`, `{"2":"y","10":"x","b":2,"a":[true,null,1.5,1e+21,1e-7,0]}`},
		{`"\ud83d\ude00 \ud83d x \u2028\u0007"`, "\"\U0001F600 \\ud83d x \u2028\\u0007\""},
		{` [ ] `, `[]`},
	} {
		v, err := ParseJSON([]byte(tc.in))
		if err != nil || Stringify(v) != tc.want {
			t.Errorf("ParseJSON(%s) → %s, %v; want %s", tc.in, Stringify(v), err, tc.want)
		}
	}
	for in, want := range map[string]string{
		``:        "Unexpected end of JSON input",
		`{"a":}`:  `Unexpected token '}', "{"a":}" is not valid JSON`,
		`[1,]`:    `Unexpected token ']', "[1,]" is not valid JSON`,
		`{'a':1}`: "Expected property name or '}' in JSON at position 1 (line 1 column 2)",
		`nul`:     "Unexpected end of JSON input",
		`nulx`:    `Unexpected token 'x', "nulx" is not valid JSON`,
		`1 2`:     "Unexpected non-whitespace character after JSON at position 2 (line 1 column 3)",
		strings.Repeat("[", MaxJSONDepth+1) + strings.Repeat("]", MaxJSONDepth+1): "Maximum call stack size exceeded",
	} {
		if _, err := ParseJSON([]byte(in)); Message(err) != want {
			t.Errorf("ParseJSON(%s): %q, want %q", clip(in), Message(err), want)
		}
	}
	if v, err := ParseJSON([]byte(strings.Repeat("[", MaxJSONDepth) + strings.Repeat("]", MaxJSONDepth))); err != nil || v == nil {
		t.Errorf("JSON nested exactly as deep as allowed: %v", err)
	}
	// broken UTF-8 becomes U+FFFD, a missing value is left out of objects and null in lists
	if v, _ := ParseJSON([]byte("\"a\xffb\"")); v != "a\uFFFDb" {
		t.Errorf("broken UTF-8: %q", v)
	}
	if got := Stringify(object("a", Undefined, "b", []any{Undefined, 1.0})); got != `{"b":[null,1]}` {
		t.Errorf("undefined in JSON: %s", got)
	}
	if Message(nil) != "" || Message(errors.New("plain")) != "plain" || Message(wrap("doing", &Error{Message: "inner"})) != "inner" || Message(wrap("doing", errors.New("x"))) != "doing: x" {
		t.Errorf("Message does not give the text a route would send")
	}
}

func TestToFixed2(t *testing.T) {
	for in, want := range map[float64]string{
		0: "0.00", 0.125: "0.13", 0.375: "0.38", 0.135: "0.14", 1.005: "1.00", 2.5: "2.50", 0.004: "0.00", 0.005: "0.01", 0.995: "0.99", 0.95: "0.95", 21: "21.00", -0.125: "-0.13", 1e21: "1e+21", 0.1 + 0.2: "0.30",
	} {
		if got := toFixed2(in); got != want {
			t.Errorf("toFixed2(%v) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]float64{" 12px": 12, "1e3": 1000, ".5": 0.5, "5.": 5, "-.5e-2x": -0.005, "+7": 7, "0x10": 0, "1e": 1, "1e+": 1, "00012": 12} {
		if got := parseFloat(in); got != want {
			t.Errorf("parseFloat(%q) = %v, want %v", in, got, want)
		}
	}
	for _, in := range []any{"", "px", "-", ".", "e5", "Infinity", "-Infinity", "NaN", nil, Undefined, true} {
		if _, ok := clampNumber(in, 0, 100); ok {
			t.Errorf("clampNumber(%v) found a number", in)
		}
	}
}

// The cache is replaced, never changed, so readers need no lock of their
// own. Run with -race.
func TestConcurrentUse(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db)
		ctx := context.Background()
		s.LoadBranding(ctx)
		var wg sync.WaitGroup
		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for i := 0; i < 15; i++ {
					updates := object("appName", "writer "+strconv.Itoa(w), "themeMode", "custom", "colors", mustParse(t, `{"0":"index key","bgPrimary":"#010203"}`))
					if err := s.SaveBranding(ctx, updates); err != nil {
						t.Error(err)
						return
					}
				}
			}(w)
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 60; i++ {
					if name := s.GetBranding().Text("appName"); name != "BetterDesk" && !strings.HasPrefix(name, "writer ") {
						t.Errorf("appName = %q", name)
					}
					if _, err := s.GenerateThemeCSS(); err != nil {
						t.Error(err)
					}
					if _, err := s.GetPublicAppearance(); err != nil {
						t.Error(err)
					}
					if preset, err := s.ExportPreset(); err != nil {
						t.Error(err)
					} else {
						_ = Stringify(preset)
					}
					_ = s.Revision()
					_ = s.LoadSupporters()
				}
			}()
		}
		wg.Wait()
	})
}

func TestHelpersForRoutes(t *testing.T) {
	body := mustParse(t, `{"name":"  Night  ","count":12.5,"list":[1,null,["a"]],"flag":true,"nothing":null,"odd":{"toString":1},"plain":{}}`)
	for key, want := range map[string]string{"name": "  Night  ", "count": "12.5", "list": "1,,a", "flag": "true", "nothing": "null", "missing": "undefined", "plain": "[object Object]"} {
		if got, err := ToString(Field(body, key)); err != nil || got != want {
			t.Errorf("ToString(body.%s) = %q, %v; want %q", key, got, err, want)
		}
	}
	if _, err := ToString(Field(body, "odd")); Message(err) != "Cannot convert object to primitive value" {
		t.Errorf("ToString of an object with a toString key: %v", err)
	}
	for _, notAnObject := range []any{nil, Undefined, "text", 5.0, []any{"x"}, (*Object)(nil)} {
		if got := Field(notAnObject, "name"); got != Undefined {
			t.Errorf("Field(%v) = %v, want Undefined", notAnObject, got)
		}
	}
	if got := Trim(" \t\u00A0\uFEFF\u2028 x y \u3000\n"); got != "x y" {
		t.Errorf("Trim = %q", got)
	}
	if Truthy(nil) || Truthy(Undefined) || Truthy("") || Truthy(0.0) || Truthy(false) || !Truthy("0") || !Truthy([]any{}) || !Truthy(NewObject()) {
		t.Errorf("Truthy does not follow JavaScript")
	}
	// values of the caller inside an answer
	type row struct {
		ID   int    `json:"id"`
		Note string `json:"note"`
	}
	answer := object("success", true, "font", (*Object)(nil), "rows", []row{{1, "<b>"}}, "count", 3)
	if got := Stringify(answer); got != `{"success":true,"font":null,"rows":[{"id":1,"note":"<b>"}],"count":3}` {
		t.Errorf("an answer with Go values: %s", got)
	}
}

// What GET /api/auth/login-page and GET /api/console/bootstrap send of
// the branding (auth.routes.js:91, console.routes.js:94 and :245).
func TestSignInAndBootstrapBranding(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db)
		ctx := context.Background()

		// before the first load and on an untouched console: the defaults
		for _, loaded := range []bool{false, true} {
			if loaded {
				s.LoadBranding(ctx)
			}
			if got := Stringify(s.SignInBranding()); got != `{"logoType":"default","logoSvg":"","logoUrl":"","logoText":"","logoTextAccent":"","loginTitle":"","loginSubtitle":"","loginPromoMode":"classic","loginSlides":[],"footerText":"","showPoweredBy":true}` {
				t.Errorf("sign-in branding of an untouched console (loaded %v): %s", loaded, got)
			}
			want := `{"appName":"BetterDesk","appDescription":"BetterDesk Server Management","themeMode":"system","logoType":"default","logoIcon":"dns","logoSvg":"","logoUrl":"","logoText":"","logoTextAccent":"","footerText":"","showPoweredBy":true,"revision":"` + s.Revision() + `"}`
			if got := Stringify(s.BootstrapBranding("Configured")); got != want {
				t.Errorf("bootstrap branding of an untouched console (loaded %v):\n got  %s\n want %s", loaded, got, want)
			}
		}
		if s.AppName("Configured") != "BetterDesk" || s.ThemeMode() != "system" {
			t.Errorf("AppName %q, ThemeMode %q", s.AppName("Configured"), s.ThemeMode())
		}
		palettes := s.BootPalettes()
		light, _ := palettes.Get("light")
		dark, _ := palettes.Get("dark")
		if keyList(palettes) != `["light","dark"]` || light.(*Object).Text("bgPrimary") != "#ffffff" || dark.(*Object).Text("bgPrimary") != "#141616" || light.(*Object).Len() != len(colorKeys) {
			t.Errorf("boot palettes: %s", clip(Stringify(palettes)))
		}

		updates := mustParse(t, `{
			"appName": "Acme Remote", "appDescription": "", "themeMode": "light", "logoType": "text", "logoText": "Acme", "logoTextAccent": "Remote",
			"logoSvg": "<svg><rect/></svg>", "logoUrl": "/uploads/logo-1.png", "logoIcon": "", "loginTitle": "Welcome", "loginSubtitle": "Sign in",
			"footerText": "(c) Acme", "showPoweredBy": "false", "loginPromoMode": "slides",
			"loginSlides": [{"id": "one", "slideKey": "free", "imageUrl": "/uploads/bg-1.jpg", "title": "T", "body": "B", "overlay": "12.5"}, {"imageUrl": "https://example.com/a.jpg"}]
		}`)
		if err := s.SaveBranding(ctx, updates); err != nil {
			t.Fatal(err)
		}
		wantSignIn := `{"logoType":"text","logoSvg":"<svg><rect/></svg>","logoUrl":"/uploads/logo-1.png","logoText":"Acme","logoTextAccent":"Remote","loginTitle":"Welcome","loginSubtitle":"Sign in","loginPromoMode":"slides",` +
			`"loginSlides":[{"id":"one","slideKey":"free","imageUrl":"/uploads/bg-1.jpg","title":"T","body":"B","overlay":12.5},{"id":"slide-2","slideKey":"","imageUrl":"https://example.com/a.jpg","title":"","body":"","overlay":0}],` +
			`"footerText":"(c) Acme","showPoweredBy":false}`
		if got := Stringify(s.SignInBranding()); got != wantSignIn {
			t.Errorf("sign-in branding:\n got  %s\n want %s", got, wantSignIn)
		}
		wantBoot := `{"appName":"Acme Remote","appDescription":"","themeMode":"light","logoType":"text","logoIcon":"","logoSvg":"<svg><rect/></svg>","logoUrl":"/uploads/logo-1.png","logoText":"Acme","logoTextAccent":"Remote","footerText":"(c) Acme","showPoweredBy":false,"revision":"` + s.Revision() + `"}`
		if got := Stringify(s.BootstrapBranding("Configured")); got != wantBoot {
			t.Errorf("bootstrap branding:\n got  %s\n want %s", got, wantBoot)
		}

		// the slides are only sent in the slides layout; empty settings fall back
		if err := s.SaveBranding(ctx, object("loginPromoMode", "single", "appName", "", "showPoweredBy", "no")); err != nil {
			t.Fatal(err)
		}
		signIn := s.SignInBranding()
		if slides, _ := signIn.Get("loginSlides"); signIn.Text("loginPromoMode") != "single" || Stringify(slides) != "[]" || Stringify(property(signIn, "showPoweredBy")) != "true" {
			t.Errorf("sign-in branding in the single layout: %s", Stringify(signIn))
		}
		if s.AppName("Configured") != "Configured" || s.BootstrapBranding("Configured").Text("appName") != "Configured" {
			t.Errorf("an empty appName does not fall back to the configured name")
		}
		// rows written by something else: an empty logo type and theme mode
		setRows(t, s, [2]string{"logoType", ""}, [2]string{"logoIcon", "cloud"}, [2]string{"themeMode", ""}, [2]string{"loginPromoMode", "odd"})
		if got := Stringify(property(s.BootstrapBranding("C"), "logoType")); got != "null" || s.ThemeMode() != "system" {
			t.Errorf("an empty logoType in the bootstrap is %s, themeMode %q", got, s.ThemeMode())
		}
		if got := s.SignInBranding(); got.Text("logoType") != "default" || got.Text("loginPromoMode") != "classic" {
			t.Errorf("sign-in branding with an empty logo type: %s", Stringify(got))
		}
	})
}
