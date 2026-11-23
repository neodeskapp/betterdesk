package branding

import (
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// The console's pages have no body.app-page, no body.rd-desk-body and read
// --font, not --font-family (BUGS L358, L359, L361): the stylesheet has to
// name what the pages have.

// stripConsoleRules is the stylesheet without the rules for this
// console's pages: what Node wrote.
func stripConsoleRules(css string) string {
	start := strings.Index(css, consoleRulesMark)
	if start < 0 {
		return css
	}
	end := strings.Index(css[start:], "\n/* --- custom branding CSS --- */\n")
	if end < 0 {
		return css[:start]
	}
	return css[:start] + css[start+end:]
}

func TestConsoleBackgroundIsBehindTheConsolePages(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db)
		setRows(t, s, [2]string{"bgType", "image"}, [2]string{"bgImageUrl", "/uploads/bg-test.jpg"}, [2]string{"bgOverlay", "20"},
			[2]string{"bgSize", "contain"}, [2]string{"bgBlur", "8"}, [2]string{"rdclientBgType", "none"})
		css := mustCSS(t, s)
		for _, want := range []string{
			"body:has(> #bd-root .shell) { isolation: isolate; }\n",
			"body:has(> #bd-root .shell)::before {\n    content: '';\n    position: fixed;\n    inset: 0;\n    z-index: -1;\n    pointer-events: none;\n" +
				"    background: url(\"/uploads/bg-test.jpg\");\n    background-size: contain; background-repeat: no-repeat; background-position: center;\n" +
				"    filter: blur(8px);\n    transform: scale(1.05);\n}\n",
			"body:has(> #bd-root .shell)::after {\n    content: '';\n    position: fixed;\n    inset: 0;\n    z-index: -1;\n    pointer-events: none;\n    background: rgba(0, 0, 0, 0.20);\n}\n",
			"#bd-root .shell { background: transparent; }\n",
		} {
			if !strings.Contains(css, want) {
				t.Errorf("the stylesheet lacks %q in:\n%s", want, css)
			}
		}
		if strings.Contains(css, ".desk") {
			t.Errorf("the web remote client's background is None, yet the stylesheet paints .desk:\n%s", css)
		}

		// no console background: nothing for the shell
		setRows(t, s, [2]string{"bgType", "none"}, [2]string{"bgColor", "#102030"})
		if css := mustCSS(t, s); strings.Contains(css, ".shell") || strings.Contains(css, consoleRulesMark) {
			t.Errorf("without a console background the stylesheet still paints the shell:\n%s", css)
		}
	})
}

func TestWebRemoteClientBackgroundIsBehindTheDeviceList(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db)
		setRows(t, s, [2]string{"rdclientBgType", "color"}, [2]string{"rdclientBgColor", "#203040"}, [2]string{"rdclientBgOverlay", "40"})
		css := mustCSS(t, s)
		for _, want := range []string{
			"body:has(> #bd-root .desk)::before {\n    content: '';\n    position: fixed;\n    inset: 0;\n    z-index: -1;\n    pointer-events: none;\n    background: #203040;\n}\n",
			"body:has(> #bd-root .desk)::after {",
			"background: rgba(0, 0, 0, 0.40);",
			"#bd-root .desk { background: transparent; }\n",
		} {
			if !strings.Contains(css, want) {
				t.Errorf("the stylesheet lacks %q in:\n%s", want, css)
			}
		}
		if strings.Contains(css, ".shell") {
			t.Errorf("only the web remote client has a background, yet the stylesheet paints the console:\n%s", css)
		}

		// Same as console takes the console's background
		setRows(t, s, [2]string{"bgType", "gradient"}, [2]string{"bgGradient", "linear-gradient(#000, #fff)"})
		css = mustCSS(t, s)
		for _, scope := range []string{"body:has(> #bd-root .shell)::before", "body:has(> #bd-root .desk)::before"} {
			if !strings.Contains(css, scope+" {\n    content: '';\n    position: fixed;\n    inset: 0;\n    z-index: -1;\n    pointer-events: none;\n    background: linear-gradient(#000, #fff);\n}\n") {
				t.Errorf("the stylesheet lacks the gradient for %s in:\n%s", scope, css)
			}
		}
	})
}

func TestTextFontIsTheConsoleFont(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db)
		setRows(t, s, [2]string{"fontBody", " Lora's Body "})
		css := mustCSS(t, s)
		want := "html:root {\n    --font: 'Lora\\'s Body', 'IBM Plex Sans', system-ui, -apple-system, 'Segoe UI', Roboto, 'Noto Sans', sans-serif;\n}\n"
		if !strings.Contains(css, want) {
			t.Errorf("the stylesheet lacks %q in:\n%s", want, css)
		}
		// the rules come before the administrator's own CSS, which can still override them
		setRows(t, s, [2]string{"fontBody", "Lora"}, [2]string{"customCss", ".x { color: red }"})
		css = mustCSS(t, s)
		if font, custom := strings.Index(css, "--font: 'Lora'"), strings.Index(css, "custom branding CSS"); font < 0 || custom < font {
			t.Errorf("the console font is not before the custom CSS:\n%s", css)
		}
		if got := stripConsoleRules(css); strings.Contains(got, "html:root") || !strings.Contains(got, ".x { color: red }") {
			t.Errorf("stripConsoleRules left %q", got)
		}
	})
}
