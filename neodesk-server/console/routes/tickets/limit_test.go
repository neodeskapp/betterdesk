package tickets

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestListGivesTheNewestTicketsOnly: the ticket list gives the 500 newest
// tickets that pass its filters, not the whole table (BUGS RV66).
func TestListGivesTheNewestTicketsOnly(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		g := newGroup(t, db)
		ctx := context.Background()
		start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		for i := range 501 {
			at := start.Add(time.Duration(i) * time.Second).Format(time.DateTime)
			if _, err := db.Exec(ctx, `INSERT INTO tickets (title, created_by, created_at) VALUES (?, 'e2e_admin', ?)`, fmt.Sprintf("T%d", i), at); err != nil {
				t.Fatal(err)
			}
		}
		rec := g.do("admin", "GET", "/api/tickets", "")
		var list struct {
			Tickets []struct {
				Title string `json:"title"`
			} `json:"tickets"`
			Total int `json:"total"`
		}
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &list) != nil {
			t.Fatalf("GET /api/tickets: %d %.300s", rec.Code, rec.Body.String())
		}
		if n := len(list.Tickets); n != 500 || list.Total != 500 || list.Tickets[0].Title != "T500" || list.Tickets[n-1].Title != "T1" {
			t.Errorf("the list: %d tickets, total %d, want the 500 newest, T500 to T1", n, list.Total)
		}
	})
}
