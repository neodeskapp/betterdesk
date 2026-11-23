package branding

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// cssSelectors are the selectors of every rule the stylesheet can have
// outside the administrator's own CSS, with white space collapsed.
var cssSelectors = map[string]bool{
	":root":                               true,
	"@media (prefers-color-scheme: dark)": true,
	"html":                                true,
	"h1, h2, h3, h4, h5, h6, .page-title, .settings-section-title, .login-title, .sidebar-logo-text, .brand-text-logo, .desktop-login-brand": true,
	".ux35-shell .ux35-sidebar, .ux35-shell .ux35-sidebar :is(h1, h2, h3, h4, h5, h6), .ux35-shell .ux35-topbar":                             true,
	"body.app-page::before": true,
	"body.app-page::after":  true,
	"body.app-page":         true,
	"body.app-page .app-layout, body.app-page .ux35-shell, body.app-page #desktop-shell, body.app-page #modal-container, body.app-page #toast-container": true,
	"body.app-page .main-wrapper, body.app-page .app-layout, body.app-page .ux35-shell":                                                                  true,
	"body.app-page .main-content, body.app-page .ux35-content":                                                                                           true,
	"body.login-page::before":                                 true,
	"body.login-page::after":                                  true,
	"body.login-page":                                         true,
	"body.login-page.login-layout-single .login-promo":        true,
	"body.login-page.login-layout-single .login-promo::after": true,
	"body.rd-desk-body::before":                               true,
	"body.rd-desk-body::after":                                true,
	"body.rd-desk-body":                                       true,
	"body.rd-desk-body .rd-desk-app":                          true,
	// the rules for this console's pages (consoleCSS)
	"body:has(> #bd-root .shell)":         true,
	"body:has(> #bd-root .shell)::before": true,
	"body:has(> #bd-root .shell)::after":  true,
	"#bd-root .shell":                     true,
	"body:has(> #bd-root .desk)":          true,
	"body:has(> #bd-root .desk)::before":  true,
	"body:has(> #bd-root .desk)::after":   true,
	"#bd-root .desk":                      true,
	"html:root":                           true,
}

var (
	reCSSProperty = regexp.MustCompile(`^(--[a-z0-9-]+|content|position|inset|z-index|pointer-events|background|background-size|background-repeat|background-position|background-color|filter|transform|color-scheme|isolation|font-family)$`)
	reWhitespace  = regexp.MustCompile(`\s+`)
)

// errBrokenString is what cssStructure answers when a quoted string of the
// stylesheet is cut by a line break.
var errBrokenString = errors.New("a line break inside a quoted string")

// cssStructure checks the invariant of the generated stylesheet, for
// settings that were stored through SaveBranding:
//
// The stylesheet is the fixed rules of this package, in which stored
// values appear only (a) as the value of a declaration or (b) inside a
// quoted string that the fixed text opens and closes, followed, when there
// is custom CSS, by exactly the sanitised custom CSS after its marker
// line. Looking at everything before that marker and leaving out comments
// and quoted strings:
//
//   - every rule has a selector from cssSelectors, braces are balanced and
//     nest only for the one @media block;
//   - every declaration has a property from the fixed list and no second
//     colon, so a value never starts a declaration, a rule or a block;
//   - there is no "<" at all, so no "</style";
//   - no quoted string holds a raw line break (which would end it early).
//
// A "}" or "</style" that a value brings along can therefore only sit
// inside a quoted string, where it is text. In the custom CSS a "<" is
// never followed by a ">", so it holds no complete tag.
func cssStructure(css, customCSS string) error {
	if trim(customCSS) != "" {
		tail := "\n/* --- custom branding CSS --- */\n" + customCSS + "\n"
		if !strings.HasSuffix(css, tail) {
			return errors.New("the stylesheet does not end with the sanitised custom CSS")
		}
		css = strings.TrimSuffix(css, tail)
		if open := strings.IndexByte(customCSS, '<'); open >= 0 && strings.IndexByte(customCSS[open:], '>') >= 0 {
			return errors.New("the custom CSS holds a complete tag")
		}
	}
	depth := 0
	var segment strings.Builder // text since the last boundary, strings and comments left out
	declaration := func() error {
		text := strings.TrimSpace(segment.String())
		segment.Reset()
		if text == "" {
			return nil
		}
		property, value, found := strings.Cut(text, ":")
		if !found || !reCSSProperty.MatchString(strings.TrimSpace(property)) {
			return errors.New("not a declaration of this package: " + clip(text))
		}
		if strings.Contains(value, ":") {
			return errors.New("a second colon in the declaration: " + clip(text))
		}
		return nil
	}
	for i := 0; i < len(css); i++ {
		c := css[i]
		switch {
		case c == '/' && i+1 < len(css) && css[i+1] == '*':
			end := strings.Index(css[i+2:], "*/")
			if end < 0 {
				return errors.New("a comment that does not end")
			}
			i += end + 3
		case c == '\'' || c == '"':
			j := i + 1
			for ; j < len(css) && css[j] != c; j++ {
				switch css[j] {
				case '\\':
					j++
				case '\n', '\r', '\f':
					return errBrokenString
				}
			}
			if j >= len(css) {
				return errors.New("a quoted string that does not end")
			}
			i = j
		case c == '<':
			return errors.New(`a "<" outside a quoted string`)
		case c == '{':
			selector := reWhitespace.ReplaceAllString(strings.TrimSpace(segment.String()), " ")
			segment.Reset()
			if !cssSelectors[selector] {
				return errors.New("a rule with the selector " + clip(selector))
			}
			depth++
			if depth > 2 || (depth == 2 && selector != ":root") {
				return errors.New("a block inside a block: " + clip(selector))
			}
		case c == '}':
			if depth == 0 {
				return errors.New(`a "}" that closes nothing`)
			}
			if err := declaration(); err != nil {
				return err
			}
			depth--
		case c == ';':
			if depth == 0 {
				if text := strings.TrimSpace(segment.String()); text != "@import url()" {
					return errors.New("a statement outside every rule: " + clip(text))
				}
				segment.Reset()
			} else if err := declaration(); err != nil {
				return err
			}
		default:
			segment.WriteByte(c)
		}
	}
	if depth != 0 {
		return errors.New("a block that is not closed")
	}
	if rest := strings.TrimSpace(segment.String()); rest != "" {
		return errors.New("text after the last rule: " + clip(rest))
	}
	return nil
}

// checkCSSStructure fails the test when the stylesheet breaks the
// invariant of cssStructure.
func checkCSSStructure(t testing.TB, css, customCSS string) {
	t.Helper()
	if err := cssStructure(css, customCSS); err != nil {
		t.Errorf("%v in:\n%s", err, css)
	}
}

func TestCSSStructureCheckerNoticesBreakouts(t *testing.T) {
	good := mustCSS(t, oracleService(t))
	if err := cssStructure(good, ""); err != nil {
		t.Fatalf("the default stylesheet: %v", err)
	}
	for name, bad := range map[string]string{
		"an extra rule":            strings.Replace(good, "html {", "body{x:y}\nhtml {", 1),
		"a closed block":           strings.Replace(good, "--bg-primary: #ffffff;", "--bg-primary: red;}body{x:y", 1),
		"a second declaration":     strings.Replace(good, "--bg-primary: #ffffff;", "--bg-primary: red; behavior: url(x);", 1),
		"a colon in a value":       strings.Replace(good, "--bg-primary: #ffffff;", "--bg-primary: red\n color:blue;", 1),
		"a tag":                    strings.Replace(good, "--bg-primary: #ffffff;", "--bg-primary: </style>;", 1),
		"a nested block":           strings.Replace(good, "--bg-primary: #ffffff;", "--bg-primary: x; html { y: z }", 1),
		"an import of something":   "@import url(http://evil.example/x.css);\n" + good,
		"text at the end":          good + "x",
		"an unclosed block":        good + ":root {",
		"a string that runs on":    good + ":root { --x: 'abc; }",
		"a custom part not at end": good + "\n/* --- custom branding CSS --- */\nx{}\n",
		"a broken string":          strings.Replace(good, "html {", ":root { background: url(\"/a\n}\nbody{x:y}\"); }\nhtml {", 1),
	} {
		if err := cssStructure(bad, ""); err == nil {
			t.Errorf("%s was not noticed", name)
		}
	}
	if err := cssStructure(good+"\n/* --- custom branding CSS --- */\na<b>c{}\n", "a<b>c{}"); err == nil {
		t.Errorf("a tag in the custom CSS was not noticed")
	}
	if err := cssStructure(good+"\n/* --- custom branding CSS --- */\n</style x{}\n", "</style x{}"); err != nil {
		t.Errorf("custom CSS without a complete tag: %v", err)
	}
}

// fuzzService is one service per fuzzing process, on its own SQLite file.
var fuzzService = sync.OnceValues(func() (*Service, error) {
	dir, err := os.MkdirTemp("", "branding-fuzz-")
	if err != nil {
		return nil, err
	}
	db, err := store.OpenSQLite(filepath.Join(dir, "console.sqlite3"))
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		return nil, err
	}
	if _, err := db.Exec(ctx, "PRAGMA synchronous = OFF"); err != nil {
		return nil, err
	}
	opts := fixtureOptions(dir)
	opts.DB = db
	opts.Now = func() time.Time { return fixedNow }
	return New(opts)
})

// vectorSeeds adds recorded arguments of the oracle as seeds.
func vectorSeeds(f *testing.F, file, function string, limit int, add func(arg any)) {
	f.Helper()
	raw, err := os.Open(filepath.Join("testdata", file+".json.gz"))
	if err != nil {
		f.Fatal(err)
	}
	defer raw.Close()
	unzipped, err := gzip.NewReader(raw)
	if err != nil {
		f.Fatal(err)
	}
	body, err := io.ReadAll(unzipped)
	if err != nil {
		f.Fatal(err)
	}
	data, err := parseJSON(string(body))
	if err != nil {
		f.Fatal(err)
	}
	vectors, _ := property(data, function).([]any)
	step := len(vectors)/limit + 1
	for i := 0; i < len(vectors); i += step {
		if a := args(vectors[i]); len(a) > 0 {
			add(a[0])
		}
	}
}

// ownError reports whether err is one of the failures this package
// raises itself.
func ownError(err error, names ...string) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	for _, name := range names {
		if e.Name == name {
			return true
		}
	}
	return false
}

// FuzzBrandingSettings saves arbitrary JSON as settings and checks what
// comes out: no panic, only the failures JavaScript had, answers that are
// JSON, and a stylesheet that keeps the invariant of cssStructure.
func FuzzBrandingSettings(f *testing.F) {
	for _, seed := range []string{
		`{}`, `[]`, `null`, `"x"`, `{"appName":"A","themeMode":"custom","colors":{"bgPrimary":"red;}body{x:y}"}}`,
		`{"bgType":"image","bgImageUrl":"/x\")}body{x:y}"}`, `{"bgType":"image","bgImageUrl":"/a\n}\nbody{display:none}"}`,
		`{"customCss":"</style><script>alert(1)</script>@im@import;port url(x);"}`, `{"fontHeading":"A');}\nbody{}","fontBody":"微软"}`,
		`{"appearance":{"palette":{"semantic":{"primary":"#fff","constructor":1}},"login":{"loginSlides":[{"imageUrl":{"toString":1}}]}}}`,
		`{"glassEnabled":"true","glassColor":"#ABCDEF","glassOpacity":12.5,"glassBlur":[7]}`, `{"loginPromoMode":"single","loginBgType":"gradient","loginBgGradient":"linear-gradient(red, blue)","loginBgOverlay":"1e2"}`,
		`{"rdclientBgType":"image","rdclientBgImageUrl":"https://exämple.com/a b?c#d","rdclientBgSize":"repeat"}`,
	} {
		f.Add([]byte(seed))
	}
	vectorSeeds(f, "pure", "flattenAppearanceInput", 300, func(arg any) { f.Add([]byte(Stringify(arg))) })

	f.Fuzz(func(t *testing.T, data []byte) {
		settings, err := ParseJSON(data)
		if err != nil {
			if !ownError(err, "SyntaxError", "RangeError") {
				t.Fatalf("ParseJSON: %v", err)
			}
			return
		}
		// the functions that take whatever a caller hands them
		probe := oracleServiceForFuzz(t)
		for name, call := range map[string]func() (any, error){
			"FlattenAppearanceInput":      func() (any, error) { return FlattenAppearanceInput(settings) },
			"SanitizeLoginSlides":         func() (any, error) { return SanitizeLoginSlides(settings) },
			"GetAppearanceModel":          func() (any, error) { return probe.GetAppearanceModel(settings) },
			"AssessAppearanceReadability": func() (any, error) { return probe.AssessAppearanceReadability(settings) },
			"ResolveThemeColors":          func() (any, error) { return probe.ResolveThemeColors(settings) },
			"UsesDefaultLogo":             func() (any, error) { return UsesDefaultLogo(settings) },
		} {
			value, err := call()
			if err != nil && !ownError(err, "TypeError") {
				t.Fatalf("%s: %v", name, err)
			}
			if err == nil {
				if _, err := parseJSON(Stringify(value)); err != nil {
					t.Fatalf("%s answered something that is no JSON: %v", name, err)
				}
			}
		}

		s, err := fuzzService()
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		if err := s.ResetBranding(ctx); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveBranding(ctx, settings); err != nil {
			if !ownError(err, "TypeError") {
				t.Fatalf("SaveBranding: %v", err)
			}
			return
		}
		branding := s.GetBranding()
		for name, call := range map[string]func() (any, error){
			"GetAppearanceModel":          func() (any, error) { return s.GetAppearanceModel(nil) },
			"AssessAppearanceReadability": func() (any, error) { return s.AssessAppearanceReadability(nil) },
			"GetPublicAppearance":         func() (any, error) { return s.GetPublicAppearance() },
			"ExportPreset":                func() (any, error) { return s.ExportPreset() },
			"GetBranding":                 func() (any, error) { return branding, nil },
		} {
			value, err := call()
			if err != nil {
				t.Fatalf("%s after a save: %v", name, err)
			}
			if _, err := parseJSON(Stringify(value)); err != nil {
				t.Fatalf("%s answered something that is no JSON: %v", name, err)
			}
		}
		// every stored value is text, colours are an object of text, slides a list
		for _, key := range branding.Keys() {
			switch value, _ := branding.Get(key); key {
			case "colors":
				entries(value, func(name string, colour any) {
					if _, ok := colour.(string); !ok {
						t.Fatalf("colour %s is %T", name, colour)
					}
				})
			case "loginSlides":
				if slides, ok := value.([]any); !ok || len(slides) == 0 || len(slides) > MaxLoginSlides {
					t.Fatalf("loginSlides = %s", Stringify(value))
				}
			default:
				if _, ok := value.(string); !ok {
					t.Fatalf("%s is %T", key, value)
				}
			}
		}
		for _, key := range []string{"logoUrl", "bgImageUrl", "loginBgImageUrl", "agentBgImageUrl", "rdclientBgImageUrl"} {
			stored := branding.Text(key)
			if again, ok := validateBrandingURL(stored); !ok || again != stored {
				t.Fatalf("stored %s %q does not pass its own check again (%q, %v)", key, stored, again, ok)
			}
		}
		for _, key := range []string{"logoSvg", "faviconSvg"} {
			if problems := svgProblems(branding.Text(key)); len(problems) > 0 {
				t.Fatalf("stored %s %q: %s", key, branding.Text(key), strings.Join(problems, "; "))
			}
		}

		css, err := s.GenerateThemeCSS()
		if err != nil {
			t.Fatalf("GenerateThemeCSS: %v", err)
		}
		custom, err := SanitizeCustomCSS(branding.Text("customCss"))
		if err != nil {
			t.Fatal(err)
		}
		if err := cssStructure(css, custom); err != nil {
			t.Fatalf("%v in:\n%s", err, css)
		}
	})
}

// oracleServiceForFuzz is a service without a database.
var fuzzProbe = sync.OnceValues(func() (*Service, error) {
	dir, err := os.MkdirTemp("", "branding-fuzz-probe-")
	if err != nil {
		return nil, err
	}
	return New(fixtureOptions(dir))
})

func oracleServiceForFuzz(t *testing.T) *Service {
	t.Helper()
	s, err := fuzzProbe()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// FuzzBrandingURL checks the picture address check: what it accepts is
// either a path on this server or an http(s) URL without spaces, control
// characters or angle brackets that it writes the same way when asked
// again, and the url("...") made of it has no quote or backslash inside.
// (A host may hold a double quote; url("...") writes it as %22.)
func FuzzBrandingURL(f *testing.F) {
	for _, seed := range []string{"", "/uploads/a.png", "//x", "/a/../b", "https://example.com/a b", "http://[::1]:80/", "javascript:alert(1)", "https://xn--bcher-kva.example/", "https://Bücher.example/\"x\\y", "http://0x7f.1/", "HTTPS://user:pw@EXAMPLE.com:443/%zz?q=\"#'"} {
		f.Add(seed)
	}
	vectorSeeds(f, "pure", "validateBrandingUrl", 400, func(arg any) {
		if text, ok := arg.(string); ok {
			f.Add(text)
		}
	})
	vectorSeeds(f, "idna", "validateBrandingUrl", 300, func(arg any) {
		if text, ok := arg.(string); ok {
			f.Add(text)
		}
	})
	f.Fuzz(func(t *testing.T, input string) {
		// the routes hand over text from JSON: broken UTF-8 has become U+FFFD
		input = decodeUTF8([]byte(input))
		out, ok, err := ValidateBrandingURL(input)
		if err != nil {
			t.Fatalf("error %v", err)
		}
		if !ok || out == "" {
			if out != "" {
				t.Fatalf("refused, but answered %q", out)
			}
			return
		}
		if strings.HasPrefix(out, "/") {
			if strings.HasPrefix(out, "//") || strings.Contains(out, "..") || out != trim(input) {
				t.Fatalf("path %q from %q", out, input)
			}
		} else {
			rest, isHTTP := strings.CutPrefix(out, "http://")
			if !isHTTP {
				rest, isHTTP = strings.CutPrefix(out, "https://")
			}
			if host, _, _ := strings.Cut(rest, "/"); !isHTTP || host == "" || strings.ContainsAny(host, "?#") {
				t.Fatalf("%q from %q is no http(s) URL with a host and a path", out, input)
			}
			for i := 0; i < len(out); i++ {
				if c := out[i]; c <= 0x20 || c >= 0x7F || c == '<' || c == '>' {
					t.Fatalf("%q from %q has the byte %#x", out, input, c)
				}
			}
			if again, ok, _ := ValidateBrandingURL(out); !ok || again != out {
				t.Fatalf("%q from %q becomes %q when checked again", out, input, again)
			}
		}
		value, err := BuildBackgroundValue("image", nil, nil, input)
		if err != nil || !strings.HasPrefix(value, `url("`) || !strings.HasSuffix(value, `")`) {
			t.Fatalf("background value %q, %v", value, err)
		}
		if inner := value[5 : len(value)-2]; strings.ContainsAny(inner, "\"\\") {
			t.Fatalf("background value %q has a quote or backslash inside", value)
		}
	})
}

// FuzzSanitizers checks the cleaning of SVG markup, colours and custom
// CSS: no panic, and the promises each of them makes.
func FuzzSanitizers(f *testing.F) {
	for _, seed := range []string{"", "<svg onload=x><script>alert(1)</script></svg>", "<scr<script>ipt>", "red;}body{}", "expexpression(ression(", "@im@import;port", "</style", "<a href=\"javascript:x\">", "ſcript", "url( 'data:x' )",
		`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><a xlink:href="java&#115;cript:x"><use href="#a"/></a></svg>`,
		`<svg><style>.a{fill:url(#g)}</style><title>t</title><g style="fill:red"><text>x &amp; y</text></g></svg>`} {
		f.Add(seed)
	}
	for _, svg := range shippedSVGs(f) {
		f.Add(svg)
	}
	for _, function := range []string{"sanitizeSvg", "sanitizeCustomCss", "sanitizeCssColorValue"} {
		vectorSeeds(f, "pure", function, 150, func(arg any) {
			if text, ok := arg.(string); ok && len(text) < 4000 {
				f.Add(text)
			}
		})
	}
	f.Fuzz(func(t *testing.T, input string) {
		input = decodeUTF8([]byte(input))

		// one SVG document with nothing that runs or loads, or nothing at
		// all, which cleaning again leaves as it is (BUGS BR13)
		svg := SanitizeSVG(input)
		if problems := svgProblems(svg); len(problems) > 0 {
			t.Fatalf("SanitizeSVG(%q) = %q: %s", input, svg, strings.Join(problems, "; "))
		}
		if again := SanitizeSVG(svg); again != svg {
			t.Fatalf("SanitizeSVG(%q) = %q, cleaned again %q", input, svg, again)
		}

		colour, err := SanitizeCSSColorValue(input)
		if err != nil {
			t.Fatal(err)
		}
		if utf16Len(colour) > 400 {
			t.Fatalf("colour of %d characters", utf16Len(colour))
		}
		for _, r := range colour {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("#%.,()/-", r) || isJSSpace(r)) {
				t.Fatalf("SanitizeCSSColorValue(%q) = %q has %q", input, colour, r)
			}
		}

		css, err := SanitizeCustomCSS(input)
		if err != nil {
			t.Fatal(err)
		}
		if utf16Len(css) > 20000 {
			t.Fatalf("custom CSS of %d characters", utf16Len(css))
		}
		if open := strings.IndexByte(css, '<'); open >= 0 && strings.IndexByte(css[open:], '>') >= 0 {
			t.Fatalf("SanitizeCustomCSS(%q) = %q holds a complete tag", input, css)
		}

		escaped, err := EscapeCSSString(input)
		if err != nil {
			t.Fatal(err)
		}
		if cssStructure(":root { --x: '"+escaped+"'; }", "") != nil {
			t.Fatalf("EscapeCSSString(%q) = %q does not stay inside its quotes", input, escaped)
		}
	})
}

// FuzzFontFile uploads arbitrary bytes under arbitrary names as a font
// and as a picture, and checks that files only appear where they belong.
func FuzzFontFile(f *testing.F) {
	raw := loadVectors(f, "files")
	entries(property(raw, "fixtures"), func(name string, text any) {
		data, _ := base64.StdEncoding.DecodeString(toString(text))
		f.Add("Seed Font", name, "font/ttf", data)
		f.Add("", name, "", data)
	})
	for _, name := range []string{"a.ttf", "a.TTF", "a.woff2", ".ttf", "a/b.ttf", `a\b.woff2`, "..", "x.", "a.ttf\x00.png", "ü.ttf", "CON.ttf", strings.Repeat("n", 300) + ".woff2"} {
		f.Add("Für 'Fonts' \\ \n", name, "application/octet-stream", []byte("data"))
		f.Add("Für 'Fonts' \\ \n", name, "application/octet-stream", []byte("OTTO data"))
	}
	for _, mimeType := range []string{"image/png", "image/jpeg", "image/gif", "image/webp", "image/svg+xml", "image/svg"} {
		f.Add("", "page.html", mimeType, []byte("<script>x</script>"))
	}
	f.Fuzz(func(t *testing.T, family, fileName, mimeType string, content []byte) {
		family, fileName = decodeUTF8([]byte(family)), decodeUTF8([]byte(fileName))
		if len(content) > 1<<16 {
			content = content[:1<<16]
		}
		dir := t.TempDir()
		s, err := New(fixtureOptions(dir))
		if err != nil {
			t.Fatal(err)
		}
		// what the upload reader of the routes hands over: a name without folders
		fileName = posixBasename(strings.ReplaceAll(fileName, `\`, "/"))
		if !AcceptFontUpload(fileName, mimeType) {
			lower := strings.ToLower(fileName)
			if strings.HasSuffix(lower, ".ttf") || strings.HasSuffix(lower, ".woff2") {
				t.Fatalf("%q was refused as a font", fileName)
			}
		} else {
			font, err := s.UploadFont(family, fileName, content)
			inFonts := tree(t, s.opts.FontsDir)
			kind, isFont := fontKindOf(content)
			if err != nil {
				nameRefused := Message(err) == "Font family name is required" || Message(err) == "Invalid font family name"
				if !ownError(err, "Error") || !(nameRefused || !isFont && Message(err) == NotAFont) {
					t.Fatalf("UploadFont(%q, %q): %v", family, fileName, err)
				}
				if len(inFonts) != 0 {
					t.Fatalf("a refused font left %s", Stringify(inFonts))
				}
			} else {
				safe := font.Text("safeName")
				if !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(safe) {
					t.Fatalf("safeName %q", safe)
				}
				// stored by what the bytes are, whatever the name (BUGS BR4)
				if !isFont {
					t.Fatalf("%q was installed as a font", content)
				}
				stored := safe + kind.suffix
				if len(inFonts) != 4 || inFonts[0] != safe+"/ = <dir>" {
					t.Fatalf("the fonts folder after one upload: %s", Stringify(inFonts))
				}
				if data, err := os.ReadFile(filepath.Join(s.opts.FontsDir, safe, stored)); err != nil || !bytes.Equal(data, content) {
					t.Fatalf("the stored font %s: %v", stored, err)
				}
				css, _ := os.ReadFile(filepath.Join(s.opts.FontsDir, safe, "font.css"))
				if err := cssStructureOfFontFace(string(css)); err != nil {
					t.Fatalf("font.css %q: %v", css, err)
				}
				meta, err := os.ReadFile(filepath.Join(s.opts.FontsDir, safe, "meta.json"))
				parsed, parseErr := ParseJSON(meta)
				if err != nil || parseErr != nil || property(parsed, "family") != font.Text("family") || property(parsed, "source") != "upload" {
					t.Fatalf("meta.json %q: %v %v", meta, err, parseErr)
				}
				if left := tree(t, s.opts.TempDir); len(left) != 0 {
					t.Fatalf("the temporary file stayed: %s", Stringify(left))
				}
				list, err := s.ListLocalFonts()
				if err != nil || len(list) != 1 || property(list[0], "safeName") != safe {
					t.Fatalf("installed fonts: %s, %v", Stringify(list), err)
				}
				if _, err := s.GenerateFontCSS(font.Text("family"), ""); err != nil {
					t.Fatalf("the stylesheet for the uploaded font: %v", err)
				}
			}
		}

		// the same file as a logo and as a background
		for prefix, store := range map[string]func(string, io.Reader) (string, error){"logo-": s.StoreLogo, "bg-": s.StoreBackground} {
			name, err := store(mimeType, bytes.NewReader(content))
			if err != nil {
				if err != ErrInvalidImageType || AcceptImageUpload(mimeType) {
					t.Fatalf("a %s upload as %q: %v", prefix, mimeType, err)
				}
				if left := tree(t, s.opts.UploadsDir); len(left) != 0 {
					t.Fatalf("a refused upload left %s", Stringify(left))
				}
				continue
			}
			if !AcceptImageUpload(mimeType) || !strings.HasPrefix(name, prefix) || !regexp.MustCompile(`^[0-9a-f]{16}\.(png|jpg|gif|webp|svg)$`).MatchString(name[len(prefix):]) {
				t.Fatalf("%q as %q stored as %q", fileName, mimeType, name)
			}
			data, err := os.ReadFile(filepath.Join(s.opts.UploadsDir, name))
			if err != nil || !bytes.Equal(data, content) {
				t.Fatalf("the stored upload %q: %v", name, err)
			}
			if err := os.Remove(filepath.Join(s.opts.UploadsDir, name)); err != nil {
				t.Fatal(err)
			}
		}
		if left, _ := os.ReadDir(dir); len(left) > 3 {
			t.Fatalf("unexpected entries beside data, public and config: %v", left)
		}
	})
}

// cssStructureOfFontFace checks a font.css with one @font-face rule.
func cssStructureOfFontFace(css string) error {
	if !strings.HasPrefix(css, "@font-face {\n    font-family: '") || !strings.HasSuffix(css, "');\n}") {
		return errors.New("not the @font-face rule of this package")
	}
	body := strings.TrimSuffix(strings.TrimPrefix(css, "@font-face {"), "}")
	depth := 0
	for i := 0; i < len(body); i++ {
		switch c := body[i]; {
		case c == '\'':
			j := i + 1
			for ; j < len(body) && body[j] != '\''; j++ {
				if body[j] == '\\' {
					j++
				} else if body[j] == '\n' || body[j] == '\r' || body[j] == '\f' {
					return errBrokenString
				}
			}
			i = j
		case c == '{' || c == '}':
			depth++
		}
	}
	if depth != 0 {
		return errors.New("a brace outside the quoted strings")
	}
	return nil
}

// FuzzConfigFiles feeds arbitrary text to everything that reads a file
// someone can edit: the supporters list, the font cache, a theme, the
// meta file of a font, and JSON as such.
func FuzzConfigFiles(f *testing.F) {
	for _, seed := range []string{``, `{`, `null`, `[]`, `{"honorary":[1],"links":["a"]}`, `[{"family":"Zeta","category":"serif"},null,{"family":5}]`,
		`{"type":"neodesk-theme","branding":{"appName":"T","colors":"red"}}`, `{"type":"neodesk-theme","displayName":0,"branding":{"colors":{"bgPrimary":{"toString":1}}}}`,
		`{"family":["x"],"category":{}}`, "\uFEFF[]", strings.Repeat("[", 6000)} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if _, err := ParseJSON(data); err != nil && !ownError(err, "SyntaxError", "RangeError") {
			t.Fatalf("ParseJSON: %v", err)
		}
		dir := t.TempDir()
		opts := fixtureOptions(dir)
		opts.ThemesDir = filepath.Join(dir, "themes")
		opts.SupportersFile = filepath.Join(dir, "supporters.json")
		for _, file := range []string{opts.SupportersFile, opts.FontCacheFile, filepath.Join(opts.ThemesDir, "fuzz.json"), filepath.Join(opts.FontsDir, "fuzz", "meta.json")} {
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(opts.FontsDir, "fuzz", "font.css"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		probe, err := fuzzService()
		if err != nil {
			t.Fatal(err)
		}
		opts.DB = probe.opts.DB
		s, err := New(opts)
		if err != nil {
			t.Fatal(err)
		}
		supporters := s.LoadSupporters()
		if keyList(supporters) != `["honorary","individuals","links"]` {
			t.Fatalf("supporters: %s", Stringify(supporters))
		}
		for _, key := range []string{"honorary", "individuals"} {
			if _, ok := property(supporters, key).([]any); !ok {
				t.Fatalf("supporters.%s is no list", key)
			}
		}
		for _, call := range []func() (any, error){
			func() (any, error) { return s.SearchGoogleFonts("", "all") },
			func() (any, error) { return s.SearchGoogleFonts("a", "serif") },
			func() (any, error) { return s.ListThemes() },
			func() (any, error) { return s.ListLocalFonts() },
		} {
			value, err := call()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := parseJSON(Stringify(value)); err != nil {
				t.Fatalf("an answer that is no JSON: %v", err)
			}
		}
		if fonts, _ := s.SearchGoogleFonts("", "all"); len(fonts) > 80 || len(fonts) < 60 {
			t.Fatalf("%d fonts found", len(fonts))
		}
		ctx := context.Background()
		result, err := s.ApplyTheme(ctx, "fuzz")
		switch {
		case err != nil:
			if !ownError(err, "TypeError", "SyntaxError", "RangeError") {
				t.Fatalf("ApplyTheme: %v", err)
			}
		case result != ThemeApplied && result != ThemeInvalidFormat:
			t.Fatalf("ApplyTheme = %v", result)
		}
	})
}
