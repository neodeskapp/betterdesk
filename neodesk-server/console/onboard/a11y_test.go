package onboard

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/i18n"
)

var (
	bundleOnce sync.Once
	bundle     *i18n.Bundle
)

// inLanguage is the console's texts in one language, as the server loads
// them.
func inLanguage(t *testing.T, lang string) Translate {
	t.Helper()
	bundleOnce.Do(func() {
		bundle = i18n.New(filepath.Join("..", "static", "files", "lang"), "en")
		bundle.Load()
	})
	b := bundle
	return func(key string, vars map[string]any) string {
		pairs := make([]any, 0, 2*len(vars))
		for k, v := range vars {
			pairs = append(pairs, k, v)
		}
		return b.Translate(lang, key, i18n.V(pairs...))
	}
}

// The mail says the language its texts are in; Arabic runs right to left.
// No text is left as a key.
func TestTheMailSaysItsLanguage(t *testing.T) {
	for _, c := range []struct{ lang, dir string }{{"en", "ltr"}, {"de", "ltr"}, {"ar", "rtl"}} {
		for _, kind := range []string{KindJoin, KindSupport} {
			f := welcomeFacts(c.lang)
			f.Kind = kind
			m := BuildMail(inLanguage(t, c.lang), f)
			built := brandmail.Render(m, f.Branding, "")
			if built.Lang != c.lang || !strings.Contains(built.HTML, `<html lang="`+c.lang+`" dir="`+c.dir+`"`) {
				t.Errorf("%s %s mail: Lang %q, page starts %.120q", c.lang, kind, built.Lang, built.HTML)
			}
			if strings.Contains(built.Text, "mail.") || strings.Contains(built.HTML, ">mail.") {
				t.Errorf("%s %s mail has a raw key:\n%s", c.lang, kind, built.Text)
			}
		}
	}
}
