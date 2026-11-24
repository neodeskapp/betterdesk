package branding

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// brandedTheme is a theme file of a brand, as an admin may add one to the
// themes folder: its own theme mode, name, text logo, fonts and colours.
const brandedTheme = `{
  "version": "1.0",
  "type": "neodesk-theme",
  "displayName": "Example Core Server",
  "branding": {
    "themeMode": "custom",
    "appName": "Example Core Server",
    "appDescription": "Example Device Management",
    "logoType": "text",
    "logoText": "example",
    "logoTextAccent": "Core Server",
    "logoIcon": "",
    "logoSvg": "",
    "logoUrl": "",
    "faviconSvg": "",
    "fontHeading": "Space Grotesk",
    "fontBody": "Inter",
    "colors": {
      "bgPrimary": "#0c0f14",
      "bgSecondary": "#12161e",
      "bgTertiary": "#1a1f2b",
      "bgElevated": "#252b38",
      "textPrimary": "#e8ecf1",
      "textSecondary": "#8a94a6",
      "accentBlue": "#ffd600",
      "accentBlueHover": "#ffe040",
      "accentBlueMuted": "#c8a800",
      "accentGreen": "#2ea44f",
      "accentGreenHover": "#3fb950",
      "accentGreenMuted": "#2ea44f",
      "accentRed": "#f85149",
      "accentRedHover": "#ff6b6b",
      "accentRedMuted": "#f85149",
      "accentYellow": "#ffd600",
      "accentYellowHover": "#ffe040",
      "accentYellowMuted": "#c8a800",
      "accentPurple": "#a371f7",
      "accentPurpleHover": "#bc8cff",
      "accentPurpleMuted": "#a371f7",
      "borderPrimary": "#6e7481",
      "borderSecondary": "#1a1f2b"
    }
  }
}`

// shippedThemes is a service on db with the theme files the console ships
// (console/static/files/themes), not the recorded copies, and brandedTheme
// beside them as branded.json.
func shippedThemes(t testing.TB, db *store.DB) *Service {
	t.Helper()
	dir := t.TempDir()
	opts := fixtureOptions(dir)
	opts.DB = db
	opts.ThemesDir = filepath.Join(dir, "themes")
	if err := os.MkdirAll(opts.ThemesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shipped := filepath.Join("..", "static", "files", "themes")
	entries, err := os.ReadDir(shipped)
	if err != nil || len(entries) == 0 {
		t.Fatalf("theme files: %v", err)
	}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(shipped, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(opts.ThemesDir, entry.Name()), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(opts.ThemesDir, "branded.json"), []byte(brandedTheme), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func applyShipped(t testing.TB, s *Service, id string) {
	t.Helper()
	result, err := s.ApplyTheme(context.Background(), id)
	if err != nil || result != ThemeApplied {
		t.Fatalf("ApplyTheme(%q) = %v, %v", id, result, err)
	}
}

// A built-in theme shows its colours (BUGS L364): the theme files set the
// theme mode, and the NeoDesk themes take back the name, logo and fonts a
// branded theme set.
func TestBuiltinThemesShowTheirColours(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := shippedThemes(t, db)

		applyShipped(t, s, "branded")
		b := s.GetBranding()
		if got := b.Text("themeMode"); got != "custom" {
			t.Errorf("after the branded theme the theme mode is %q, want custom", got)
		}
		if got := b.Text("appName"); got != "Example Core Server" {
			t.Errorf("after the branded theme the name is %q", got)
		}
		if css := mustCSS(t, s); !strings.Contains(stripConsoleRules(css), "--accent-blue: #ffd600;") {
			t.Errorf("after the branded theme the accent is not the theme's yellow:\n%s", css)
		}

		applyShipped(t, s, "light")
		b = s.GetBranding()
		for key, want := range map[string]string{
			"themeMode": "light", "appName": "NeoDesk", "logoType": "default",
			"logoText": "", "logoTextAccent": "", "fontHeading": "", "fontBody": "",
		} {
			if got := b.Text(key); got != want {
				t.Errorf("after NeoDesk Light %s = %q, want %q", key, got, want)
			}
		}

		applyShipped(t, s, "default")
		if got := s.GetBranding().Text("themeMode"); got != "dark" {
			t.Errorf("after NeoDesk Dark the theme mode is %q, want dark", got)
		}
	})
}

// mixHex is a #rrggbb colour moved toward black (to < 0) or white (to > 0)
// by the share given, each channel rounded.
func mixHexForTest(hex string, toward byte, share float64) string {
	c, _ := hexToRGB(hex)
	mix := func(v int) int {
		return int(float64(v)+(float64(toward)-float64(v))*share+0.5) - 0
	}
	return "#" + hex2(mix(c.r)) + hex2(mix(c.g)) + hex2(mix(c.b))
}

func hex2(v int) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[v>>4], digits[v&15]})
}

// Custom colours get hover and tint shades made from them (BUGS L365),
// not those of the built-in dark palette: darker on a light page, lighter
// on a dark one. Shades a theme file stored stay.
func TestCustomColoursHaveTheirOwnHoverShades(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db)
		setRows(t, s, [2]string{"themeMode", "custom"},
			[2]string{"colors", `{"bgPrimary":"#ffffff","accentBlue":"#6633cc","accentRed":"#b00020","accentGreen":"#1c7a3b","accentGreenHover":"#123456"}`})
		css := mustCSS(t, s)
		darkBlueHover, _ := s.palette("dark").Get("accentBlueHover")
		for _, want := range []string{
			"--accent-blue-hover: " + mixHexForTest("#6633cc", 0, 0.15) + ";",
			"--accent-blue-muted: rgba(102, 51, 204, 0.15);",
			"--accent-red-hover: " + mixHexForTest("#b00020", 0, 0.15) + ";",
		} {
			if i := strings.Index(css, want); i < 0 || i < strings.Index(css, consoleRulesMark) {
				t.Errorf("the rules for the console's pages lack %q in:\n%s", want, css)
			}
		}
		if mark := strings.Index(css, consoleRulesMark); mark >= 0 && strings.Contains(css[mark:], "--accent-green-hover") {
			t.Errorf("a stored hover shade was replaced:\n%s", css)
		}
		if darkBlueHover == mixHexForTest("#6633cc", 0, 0.15) {
			t.Fatal("the test colour gives the dark palette's hover")
		}

		// a dark page: lighter shades
		setRows(t, s, [2]string{"themeMode", "custom"}, [2]string{"colors", `{"bgPrimary":"#101418","accentBlue":"#6633cc"}`})
		if css := mustCSS(t, s); !strings.Contains(css, "--accent-blue-hover: "+mixHexForTest("#6633cc", 255, 0.2)+";") {
			t.Errorf("on a dark page the hover is not lighter:\n%s", css)
		}

		// not custom: the built-in palettes, nothing added
		setRows(t, s, [2]string{"themeMode", "light"}, [2]string{"colors", `{"accentBlue":"#6633cc"}`})
		if css := mustCSS(t, s); strings.Contains(css, consoleRulesMark) {
			t.Errorf("the light theme got shades of stored colours:\n%s", css)
		}
	})
}
