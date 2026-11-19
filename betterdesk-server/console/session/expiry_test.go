package session

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// TestIdleRowAnswersSessionIdle: a signed-in session whose row is past the
// idle time when it is loaded gets the answer of an ended session: 401
// with the code session_idle, the cookie cleared, the audit entry, and the
// row ended and emptied (BUGS.md S7).
func TestIdleRowAnswersSessionIdle(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		pair := f.signIn(alice)
		sid := f.sidOf(pair)
		f.clock.Advance(31 * time.Minute)
		called := false
		rec := f.serve(get("/api/devices", pair), func(http.ResponseWriter, *http.Request) { called = true })
		if called {
			t.Error("the handler ran for an ended session")
		}
		want := `{"success":false,"error":"Session expired. Please log in again.","code":"session_idle"}`
		if rec.Code != http.StatusUnauthorized || rec.Body.String() != want {
			t.Fatalf("answer %d %s\nwant 401 %s", rec.Code, rec.Body.String(), want)
		}
		if got := strings.Join(setCookies(rec), "\n"); !strings.Contains(got, "bd.sid=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT") {
			t.Errorf("the cookie is not cleared: %q", got)
		}
		row := f.row(sid)
		if row.active || row.reason.String != ReasonIdle || row.json != "{}" {
			t.Errorf("row: %+v", row)
		}
		if len(f.audits) != 1 || f.audits[0].userID != alice.ID || f.audits[0].action != "session_expired" || f.audits[0].details != "reason=idle" {
			t.Errorf("audit: %+v", f.audits)
		}
		// The next request has no session and is not answered by the
		// middleware again
		f.audits = nil
		if rec := f.serve(get("/api/devices", pair), nil); rec.Code != http.StatusNoContent || len(f.audits) != 0 {
			t.Errorf("the request after: %d, audits %+v", rec.Code, f.audits)
		}
	})
}
