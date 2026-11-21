package tickets

import (
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// TestAViewerReadsOnlyItsOwnTickets: a viewer reads a ticket, its
// comments, its attachments and their files only when the ticket is
// assigned to it; another ticket answers as one that does not exist, on
// every route that names a ticket or an attachment, and the counts take
// only the viewer's tickets. Other roles read every ticket (BUGS RV62).
func TestAViewerReadsOnlyItsOwnTickets(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		g := newGroup(t, db)
		g.want(g.do("admin", "POST", "/api/tickets", `{"title":"Secret","description":"about C","device_id":"C","assigned_to":"e2e_operator"}`), 201, "")
		g.want(g.do("admin", "POST", "/api/tickets", `{"title":"Mine","assigned_to":"e2e_viewer"}`), 201, "")
		g.want(g.do("admin", "POST", "/api/tickets", `{"title":"Nobody's"}`), 201, "")
		for _, id := range []string{"1", "2", "3"} {
			g.want(g.do("admin", "POST", "/api/tickets/"+id+"/comments", `{"body":"comment on `+id+`"}`), 201, "")
			g.want(g.do("admin", "POST", "/api/tickets/"+id+"/attachments", `{"filename":"file`+id+`.txt","data":"aGVsbG8="}`), 201, "")
		}
		for _, id := range []string{"1", "3"} {
			g.want(g.do("viewer", "GET", "/api/tickets/"+id, ""), 404, `{"error":"Ticket not found"}`)
			g.want(g.do("viewer", "GET", "/api/tickets/"+id+"/comments", ""), 200, `{"comments":[]}`)
			g.want(g.do("viewer", "GET", "/api/tickets/"+id+"/attachments", ""), 200, `{"attachments":[]}`)
			g.want(g.do("viewer", "GET", "/api/tickets/attachments/"+id, ""), 404, `{"error":"Attachment not found"}`)
		}
		g.want(g.do("viewer", "GET", "/api/tickets/stats", ""), 200, `{"total":1,"open":1,"in_progress":0,"resolved":0,"closed":0}`)
		mine := g.do("viewer", "GET", "/api/tickets/2", "")
		if mine.Code != 200 || !contains(mine.Body.String(), `"title":"Mine"`, `comment on 2`, `file2.txt`) {
			t.Errorf("the viewer's own ticket: %d %s", mine.Code, mine.Body.String())
		}
		if rec := g.do("viewer", "GET", "/api/tickets/2/comments", ""); !contains(rec.Body.String(), "comment on 2") {
			t.Errorf("the comments of the viewer's own ticket: %d %s", rec.Code, rec.Body.String())
		}
		if rec := g.do("viewer", "GET", "/api/tickets/2/attachments", ""); !contains(rec.Body.String(), "file2.txt") {
			t.Errorf("the attachments of the viewer's own ticket: %d %s", rec.Code, rec.Body.String())
		}
		g.want(g.do("viewer", "GET", "/api/tickets/attachments/2", ""), 200, "hello")
		for _, role := range []string{"operator", "admin"} {
			if rec := g.do(role, "GET", "/api/tickets/1", ""); rec.Code != 200 || !contains(rec.Body.String(), `"title":"Secret"`, "comment on 1", "file1.txt") {
				t.Errorf("ticket 1 as %s: %d %s", role, rec.Code, rec.Body.String())
			}
			g.want(g.do(role, "GET", "/api/tickets/attachments/1", ""), 200, "hello")
			g.want(g.do(role, "GET", "/api/tickets/stats", ""), 200, `{"total":3,"open":3,"in_progress":0,"resolved":0,"closed":0}`)
		}
	})
}

// contains reports whether s holds every part.
func contains(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
