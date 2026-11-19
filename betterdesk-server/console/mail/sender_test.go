package mail

import (
	"context"
	"testing"

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
