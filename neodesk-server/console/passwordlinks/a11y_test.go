package passwordlinks

import (
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/i18n"
)

// A link's mail says the language its texts are in, on the page and for
// the message's header; Arabic runs right to left.
func TestBuildMailSaysItsLanguage(t *testing.T) {
	texts := i18n.New("../static/files/lang", "en")
	texts.Load()
	for _, c := range []struct{ lang, dir string }{{"en", "ltr"}, {"de", "ltr"}, {"ar", "rtl"}} {
		tr := func(key string, vars map[string]any) string {
			pairs := make([]any, 0, 2*len(vars))
			for k, v := range vars {
				pairs = append(pairs, k, v)
			}
			return texts.Translate(c.lang, key, i18n.V(pairs...))
		}
		for _, purpose := range []string{PurposeInvite, PurposeReset} {
			b := brandmail.Branding{Server: "Acme Desk", Host: "desk.example"}
			m := brandmail.Render(BuildMail(tr, MailFacts{Purpose: purpose, Lang: c.lang, Link: "https://desk.example/set-password#tok", Username: "ada", Sender: "Grace", Expires: time.Now(), Branding: b}), b, "")
			if m.Lang != c.lang || !strings.Contains(m.HTML, `<html lang="`+c.lang+`" dir="`+c.dir+`"`) {
				t.Errorf("%s %s mail: Lang %q, page starts %.120q", c.lang, purpose, m.Lang, m.HTML)
			}
			if m.Text == "" {
				t.Errorf("%s %s mail has no text part", c.lang, purpose)
			}
		}
	}
}
