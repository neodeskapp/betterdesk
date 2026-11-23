package tickets

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
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

// TestScopeAndInternalComments: an operator whose device scope is limited
// (here by a restricted default and a grant of device A) reads, lists,
// counts, changes and comments only the tickets of device A and those of
// no device, and writes no ticket for another device; the ticket of
// device C answers as one that does not exist on every route. Internal
// comments are for accounts with device.edit: a guest_issuer, who may read
// tickets, gets none (BUGS RV62, A98).
func TestScopeAndInternalComments(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		g := newGroup(t, db)
		ctx := context.Background()
		for _, q := range []string{
			`INSERT INTO settings (key, value) VALUES ('device_scope_default', 'restricted')`,
			`INSERT INTO users (id, username, password_hash, role) VALUES (5, 'e2e_operator', 'x', 'operator')`,
			`INSERT INTO user_peer_grants (user_id, peer_id) VALUES (5, 'A')`,
		} {
			if _, err := db.Exec(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		g.want(g.do("admin", "POST", "/api/tickets", `{"title":"About A","device_id":"A"}`), 201, "")
		g.want(g.do("admin", "POST", "/api/tickets", `{"title":"About C","device_id":"C"}`), 201, "")
		g.want(g.do("admin", "POST", "/api/tickets", `{"title":"About nothing"}`), 201, "")
		for _, id := range []string{"1", "2", "3"} {
			g.want(g.do("admin", "POST", "/api/tickets/"+id+"/comments", `{"body":"public `+id+`"}`), 201, "")
			g.want(g.do("admin", "POST", "/api/tickets/"+id+"/comments", `{"body":"internal `+id+`","is_internal":true}`), 201, "")
			g.want(g.do("admin", "POST", "/api/tickets/"+id+"/attachments", `{"filename":"file`+id+`.txt","data":"aGVsbG8="}`), 201, "")
		}

		titles := func(body string) string {
			var list struct {
				Tickets []struct {
					Title string `json:"title"`
				} `json:"tickets"`
			}
			if err := json.Unmarshal([]byte(body), &list); err != nil {
				t.Fatalf("list: %s", body)
			}
			var out []string
			for _, item := range list.Tickets {
				out = append(out, item.Title)
			}
			return strings.Join(out, ",")
		}
		if rec := g.do("operator", "GET", "/api/tickets", ""); rec.Code != 200 || titles(rec.Body.String()) != "About nothing,About A" {
			t.Errorf("the operator's list: %d %s", rec.Code, rec.Body.String())
		}
		if rec := g.do("admin", "GET", "/api/tickets", ""); titles(rec.Body.String()) != "About nothing,About C,About A" {
			t.Errorf("the admin's list: %s", rec.Body.String())
		}
		g.want(g.do("operator", "GET", "/api/tickets/stats", ""), 200, `{"total":2,"open":2,"in_progress":0,"resolved":0,"closed":0}`)
		g.want(g.do("operator", "GET", "/api/tickets/2", ""), 404, `{"error":"Ticket not found"}`)
		g.want(g.do("operator", "GET", "/api/tickets/2/comments", ""), 200, `{"comments":[]}`)
		g.want(g.do("operator", "GET", "/api/tickets/2/attachments", ""), 200, `{"attachments":[]}`)
		g.want(g.do("operator", "GET", "/api/tickets/attachments/2", ""), 404, `{"error":"Attachment not found"}`)
		g.want(g.do("operator", "PATCH", "/api/tickets/2", `{"status":"closed"}`), 404, `{"error":"Ticket not found"}`)
		g.want(g.do("operator", "DELETE", "/api/tickets/2", ""), 404, `{"error":"Ticket not found"}`)
		g.want(g.do("operator", "POST", "/api/tickets/2/comments", `{"body":"hi"}`), 404, "")
		g.want(g.do("operator", "POST", "/api/tickets/2/attachments", `{"filename":"x.txt","data":"aGVsbG8="}`), 404, "")
		const outside = `{"error":"Device is outside your device scope"}`
		g.want(g.do("operator", "POST", "/api/tickets", `{"title":"Mine","device_id":"C"}`), 403, outside)
		g.want(g.do("operator", "PATCH", "/api/tickets/1", `{"device_id":"C"}`), 403, outside)
		if got := rows(t, db, "SELECT title || ':' || COALESCE(device_id, '') || ':' || COALESCE(status, '') FROM tickets ORDER BY id"); got != "About A:A:open,About C:C:open,About nothing::open" {
			t.Errorf("the tickets after the refusals: %s", got)
		}

		for _, id := range []string{"1", "3"} {
			if rec := g.do("operator", "GET", "/api/tickets/"+id, ""); rec.Code != 200 || !contains(rec.Body.String(), "internal "+id, "file"+id+".txt") {
				t.Errorf("ticket %s as the operator: %d %s", id, rec.Code, rec.Body.String())
			}
		}
		g.want(g.do("operator", "PATCH", "/api/tickets/1", `{"status":"in_progress","device_id":""}`), 200, `{"success":true}`)
		g.want(g.do("operator", "POST", "/api/tickets", `{"title":"Mine","device_id":"A"}`), 201, "")

		for _, path := range []string{"/api/tickets/3", "/api/tickets/3/comments"} {
			rec := g.do("guest_issuer", "GET", path, "")
			if rec.Code != 200 || !contains(rec.Body.String(), "public 3") || contains(rec.Body.String(), "internal 3") {
				t.Errorf("%s as guest_issuer: %d %s", path, rec.Code, rec.Body.String())
			}
		}
	})
}

// rows is the first column of every row of a query, joined by commas.
func rows(t *testing.T, db *store.DB, query string) string {
	t.Helper()
	result, err := db.Query(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	var out []string
	for result.Next() {
		var s string
		if err := result.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return strings.Join(out, ",")
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
