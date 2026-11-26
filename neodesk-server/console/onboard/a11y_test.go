package onboard

import (
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

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

// The mail says the language its texts are in, on the page and for the
// message's header; Arabic runs right to left.
func TestTheMailSaysItsLanguage(t *testing.T) {
	for _, c := range []struct{ lang, dir string }{{"en", "ltr"}, {"de", "ltr"}, {"ar", "rtl"}} {
		for _, kind := range []string{KindJoin, KindSupport} {
			f := welcomeFacts()
			f.Kind = kind
			m := BuildMail(inLanguage(t, c.lang), f)
			if m.Lang != c.lang || !strings.Contains(m.HTML, `<html lang="`+c.lang+`" dir="`+c.dir+`">`) {
				t.Errorf("%s %s mail: Lang %q, page starts %.120q", c.lang, kind, m.Lang, m.HTML)
			}
			if m.Text == "" {
				t.Errorf("%s %s mail has no text part", c.lang, kind)
			}
		}
	}
}

// The invite's code is a line of its own under the sentence that says
// where to enter it, in both parts, so it can be selected alone.
func TestTheInviteCodeHasALineOfItsOwn(t *testing.T) {
	for _, lang := range []string{"en", "de"} {
		m := BuildMail(inLanguage(t, lang), welcomeFacts())
		if !regexp.MustCompile(`:</p><p [^>]*font-family: 'IBM Plex Mono'[^>]*>ABCD-2345</p>`).MatchString(m.HTML) {
			t.Errorf("%s: the code is not on a line of its own:\n%s", lang, m.HTML)
		}
		if !strings.Contains(m.Text, ":\nABCD-2345\n") {
			t.Errorf("%s: the text part's code is not on a line of its own:\n%s", lang, m.Text)
		}
		if strings.Count(m.HTML, "ABCD-2345") != 1 {
			t.Errorf("%s: the code is in the page %d times", lang, strings.Count(m.HTML, "ABCD-2345"))
		}
	}
}
