package mail

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// A save with a host names its sender: without one it is refused with 400
// and nothing is stored, where the old handler stored the sender
// "betterdesk@localhost", which most mail servers refuse (BUGS EM9).
func TestSaveNeedsASender(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, _, _ := newTestService(db, nil, Options{})
		for _, body := range []string{`{"host":"h"}`, `{"host":"h","from":null}`, `{"host":"h","from":""}`, `{"host":"h","from":" \t "}`} {
			answer := s.PutSMTPSettings(ctx, bodyOf(body))
			if answer.Status != 400 || marshal(t, answer.Body) != `{"error":"SMTP sender is required"}` || answer.Audit != nil {
				t.Errorf("%s: %d %s, want 400", body, answer.Status, marshal(t, answer.Body))
			}
		}
		if got := stored(t, db); got != "" {
			t.Fatalf("a save without a sender stored %s", got)
		}
		if answer := s.PutSMTPSettings(ctx, bodyOf(`{"host":"h","from":"alerts@example.com"}`)); answer.Status != 200 {
			t.Fatalf("a save with a sender: %d %s", answer.Status, marshal(t, answer.Body))
		}
		if got, want := stored(t, db), `{"host":"h","port":587,"secure":false,"user":"","pass":"","from":"alerts@example.com","alert_email":""}`; got != want {
			t.Errorf("stored %s, want %s", got, want)
		}
	})
}

// A sender has at most 998 characters, the longest line of a mail header:
// every alert and help request mail builds its address, and a longer one
// is refused with 400 (BUGS RV36).
func TestSaveRefusesASenderTooLong(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, _, _ := newTestService(db, nil, Options{})
		// "alerts@" and ".org" are 11 characters, each \u00e9 is one
		long := "alerts@" + strings.Repeat("\u00e9", 987) + ".org"
		if answer := s.PutSMTPSettings(ctx, bodyOf(`{"host":"h","from":"`+long+`"}`)); answer.Status != 200 {
			t.Fatalf("a sender of 998 characters: %d %s", answer.Status, marshal(t, answer.Body))
		}
		before := stored(t, db)
		for _, from := range []string{long + "x", "Alerts <alerts@" + strings.Repeat("\u0301", 300000) + ".org>"} {
			answer := s.PutSMTPSettings(ctx, bodyOf(`{"host":"h","from":"`+from+`"}`))
			if answer.Status != 400 || marshal(t, answer.Body) != `{"error":"SMTP sender is too long"}` || answer.Audit != nil {
				t.Errorf("a sender of %d characters: %d %s, want 400", utf8.RuneCountInString(from), answer.Status, marshal(t, answer.Body))
			}
		}
		if got := stored(t, db); got != before {
			t.Fatalf("a refused save stored %s", got)
		}
	})
}
