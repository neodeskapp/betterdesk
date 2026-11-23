package mail

import (
	"context"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// A save that turns the certificate check off (tlsVerify false where the
// stored settings, or the console's default for settings that do not say,
// check it) asks for the password again, as another host does: the mask
// or no password is refused and nothing is stored. A save that keeps the
// check off, or turns it on, keeps the stored password, and so does one
// with nothing stored to keep (BUGS L57).
func TestSaveAsksForThePasswordWhenTheCertificateCheckGoesOff(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		const refusal = `{"error":"SMTP password is required when tlsVerify is turned off"}`
		save := func(s *Service, body string) Answer {
			t.Helper()
			return s.PutSMTPSettings(ctx, bodyOf(body))
		}
		refused := func(s *Service, body string) {
			t.Helper()
			before := stored(t, db)
			if answer := save(s, body); answer.Status != 400 || marshal(t, answer.Body) != refusal || answer.Audit != nil {
				t.Errorf("%s: %d %s, want 400 %s", body, answer.Status, marshal(t, answer.Body), refusal)
			}
			if got := stored(t, db); got != before {
				t.Errorf("%s: a refused save stored %s", body, got)
			}
		}
		taken := func(s *Service, body, pass string) {
			t.Helper()
			if answer := save(s, body); answer.Status != 200 {
				t.Fatalf("%s: %d %s", body, answer.Status, marshal(t, answer.Body))
			}
			if got := stored(t, db); !strings.Contains(got, `"pass":"`+pass+`"`) {
				t.Fatalf("%s: stored %s, want the password %s", body, got, pass)
			}
		}
		const account = `"host":"mail.example.org","port":465,"secure":true,"user":"alerts","from":"f@example.org"`

		checking, transport, _ := newTestService(db, nil, Options{TLSVerify: true})
		taken(checking, `{`+account+`,"pass":"first-secret"}`, "first-secret")
		refused(checking, `{`+account+`,"pass":"********","tlsVerify":false}`)
		refused(checking, `{`+account+`,"tlsVerify":false}`)
		refused(checking, `{`+account+`,"pass":null,"tlsVerify":false}`)
		checking.TestSMTPSettings(ctx)
		if len(transport.verifies) != 1 || transport.verifies[0].Pass != "first-secret" || !transport.verifies[0].TLSVerify {
			t.Fatalf("the test connected with %+v", transport.verifies)
		}
		taken(checking, `{`+account+`,"pass":"********","tlsVerify":true}`, "first-secret")
		refused(checking, `{`+account+`,"pass":"********","tlsVerify":false}`)

		// With the password typed again the check goes off; a save that
		// keeps it off or turns it on again keeps the password
		taken(checking, `{`+account+`,"pass":"second-secret","tlsVerify":false}`, "second-secret")
		taken(checking, `{`+account+`,"pass":"********","tlsVerify":false}`, "second-secret")
		taken(checking, `{`+account+`,"pass":"********"}`, "second-secret")

		// Where the console checks no certificate by default, settings
		// that say true lose the check when a save leaves tlsVerify out
		lax, _, _ := newTestService(db, nil, Options{TLSVerify: false})
		taken(lax, `{`+account+`,"pass":"********","tlsVerify":true}`, "second-secret")
		refused(lax, `{`+account+`,"pass":"********"}`)
		taken(lax, `{`+account+`,"pass":"********","tlsVerify":true}`, "second-secret")

		// Nothing stored, nothing to keep
		storeConfig(t, db, `{"host":"mail.example.org","port":465,"user":"alerts","pass":"","from":"f@example.org"}`)
		taken(checking, `{`+account+`,"pass":"********","tlsVerify":false}`, "")
	})
}
