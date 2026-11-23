package session

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
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
		if got := strings.Join(setCookies(rec), "\n"); !strings.Contains(got, "nd.sid=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT") {
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

// TestUnreadableStoredSessionIsNoSession: a stored session the console
// cannot read (not JSON, no cookie object) is no session: the request goes
// on without one and the row ends, instead of an error on every request of
// that browser (BUGS.md S21).
func TestUnreadableStoredSessionIsNoSession(t *testing.T) {
	cases := []string{`not json`, `[]`, `"text"`, `5`, `null`, `{"userId":7}`, `{"cookie":null}`, `{"cookie":5}`, `{"cookie":"x"}`, `{"cookie":{}} trailing`, `{"cookie":{"expires":}}`}
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		now := f.clock.Now().UnixMilli()
		for i, text := range cases {
			sid := "broken-" + string(rune('a'+i))
			f.mustSet(sid, record{json: text, expiresAt: now + 60_000, createdAt: now, lastActivityAt: now})
			var userID int64 = -1
			rec := f.serve(get("/", f.cookieFor(sid)), func(w http.ResponseWriter, r *http.Request) {
				userID = From(r).UserID()
				w.WriteHeader(http.StatusNoContent)
			})
			if rec.Code != http.StatusNoContent || userID != 0 {
				t.Errorf("%s: status %d, user %d; want the handler without a session", text, rec.Code, userID)
			}
			if row := f.row(sid); row.active || row.reason.String != ReasonUnreadable {
				t.Errorf("%s: row %+v; want it ended as unreadable", text, row)
			}
			// The browser's next request finds the row ended and goes on
			if rec := f.serve(get("/", f.cookieFor(sid)), nil); rec.Code != http.StatusNoContent {
				t.Errorf("%s: the next request: %d", text, rec.Code)
			}
		}
		if len(f.errs) != 0 {
			t.Errorf("OnError was called: %v", f.errs)
		}
		// An array as the cookie object is read as a cookie without
		// attributes: that session goes on
		f.mustSet("array", record{json: `{"cookie":[],"k":1}`, expiresAt: now + 60_000, createdAt: now, lastActivityAt: now})
		if rec := f.serve(get("/", f.cookieFor("array")), nil); rec.Code != http.StatusNoContent || !f.row("array").active {
			t.Errorf("status %d for a cookie array, row %+v", rec.Code, f.row("array"))
		}
	})
}
