package automation_test

import (
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// A rule's notify_emails is plain addresses separated by commas, on a new
// rule and in a change (BUGS EM14). A line break, a group or a display
// name would change who gets the alert mail, a space that is no ASCII
// space would start a header line after folding (EM13), and a domain like
// 1.2.3 is rewritten as an IPv4 address (EM16). The old routes stored the
// text as it came.
func TestNotifyEmailsArePlainAddresses(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		seedOne(t, db)
		w := newWorld(t, db)
		const columns = "SELECT id, notify_emails FROM alert_rules ORDER BY id"
		before := w.Rows(columns, nil)
		for _, emails := range []string{
			`"x@y.z\r\nCc: b@c.d"`, `"a@example.com\nb@example.com"`, `"Ops <ops@example.com>"`, `"ops: a@example.com;"`,
			`"\"aaa X-Header: v\"@example.com"`, `"a @example.com"`, `"ops@1.2.3"`, `"ops@[127.0.0.1]"`, `"a@b@c"`, `"ops"`,
			`"a@example.com; b@example.com"`, `"a@example.com b@example.com"`,
		} {
			for _, req := range []replay.Request{
				{Role: "admin", Method: "POST", Path: "/api/automation/rules", Body: replay.Text(`{"name":"Mail","condition_type":"custom","notify_emails":` + emails + `}`)},
				{Role: "admin", Method: "PATCH", Path: "/api/automation/rules/1", Body: replay.Text(`{"notify_emails":` + emails + `}`)},
			} {
				if got := w.Do(req); got.Status != 400 || got.Body != `{"error":"Invalid notify_emails"}` {
					t.Errorf("%s %s with %s: %d %s, want 400", req.Method, req.Path, emails, got.Status, got.Body)
				}
			}
		}
		if after := w.Rows(columns, nil); after != before {
			t.Errorf("the refused rules changed something:\nbefore %s\n after %s", before, after)
		}
		for _, emails := range []string{`""`, `"ops@example.com"`, `" a@example.com , b.c+d@mail.example.org "`, `null`} {
			if got := w.Do(replay.Request{Role: "admin", Method: "PATCH", Path: "/api/automation/rules/1", Body: replay.Text(`{"notify_emails":` + emails + `}`)}); got.Status != 200 {
				t.Errorf("rule 1 with %s: %d %s, want 200", emails, got.Status, got.Body)
			}
		}
	})
}
