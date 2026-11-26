package accessibility_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

const defaults = `{"reduceMotion":"system","moreContrast":"system","underlineLinks":false,"readableFont":false,"noSingleKeyShortcuts":false,"pauseLiveUpdates":false}`

func data(t *testing.T, res routetest.Answer) string {
	t.Helper()
	if res.Status != http.StatusOK {
		t.Fatalf("status %d: %s", res.Status, res.Body)
	}
	var out struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.Body), &out); err != nil || !out.Success {
		t.Fatalf("answer %s (%v)", res.Body, err)
	}
	return string(out.Data)
}

// Every account that signs in keeps its own settings: the defaults until
// it saves, a change of some keys keeps the others, and one account's
// change is not another's.
func TestEveryAccountKeepsItsOwnSettings(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		for _, role := range []string{"guest_issuer", "viewer", "operator", "admin"} {
			a := w.As(role)
			if got := data(t, a.Do("GET", "/api/accessibility", "")); got != defaults {
				t.Errorf("%s before a change: %s", role, got)
			}
			got := data(t, a.Do("PUT", "/api/accessibility", `{"moreContrast":"on","underlineLinks":true}`))
			want := `{"reduceMotion":"system","moreContrast":"on","underlineLinks":true,"readableFont":false,"noSingleKeyShortcuts":false,"pauseLiveUpdates":false}`
			if got != want {
				t.Errorf("%s after a change: %s", role, got)
			}
			got = data(t, a.Do("PUT", "/api/accessibility", `{"pauseLiveUpdates":true,"reduceMotion":"off"}`))
			want = `{"reduceMotion":"off","moreContrast":"on","underlineLinks":true,"readableFont":false,"noSingleKeyShortcuts":false,"pauseLiveUpdates":true}`
			if got != want {
				t.Errorf("%s after a second change: %s", role, got)
			}
			if got := data(t, a.Do("GET", "/api/accessibility", "")); got != want {
				t.Errorf("%s read back: %s", role, got)
			}
		}
		if got := data(t, w.As("super_admin").Do("GET", "/api/accessibility", "")); got != defaults {
			t.Errorf("another account sees the changes: %s", got)
		}
		rows, err := db.Query(t.Context(), `SELECT user_id FROM user_preferences ORDER BY user_id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		if len(ids) != 4 || ids[0] != routetest.RoleIDs["admin"] {
			t.Errorf("rows for %v, want the four accounts that saved", ids)
		}
	})
}

// A change the server does not understand is refused as a whole and the
// stored settings stay. Signed out there are none to read or change, and
// a write needs the CSRF token.
func TestRefusedChanges(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	a := w.As("viewer")
	data(t, a.Do("PUT", "/api/accessibility", `{"readableFont":true}`))
	for _, body := range []string{
		`{"readableFont":false,"moreContrast":"high"}`,
		`{"readableFont":false,"fontScale":120}`,
		`{"readableFont":false,"underlineLinks":"yes"}`,
		`{"readableFont":null}`,
		`[]`,
		`"on"`,
	} {
		if res := a.Do("PUT", "/api/accessibility", body); res.Status != http.StatusBadRequest {
			t.Errorf("%s: %d %s", body, res.Status, res.Body)
		}
	}
	want := `{"reduceMotion":"system","moreContrast":"system","underlineLinks":false,"readableFont":true,"noSingleKeyShortcuts":false,"pauseLiveUpdates":false}`
	if got := data(t, a.Do("GET", "/api/accessibility", "")); got != want {
		t.Errorf("after the refused changes: %s", got)
	}

	out := w.SignedOut()
	if res := out.Do("GET", "/api/accessibility", ""); res.Status != http.StatusUnauthorized {
		t.Errorf("signed out GET: %d %s", res.Status, res.Body)
	}
	if res := out.Do("PUT", "/api/accessibility", `{"readableFont":false}`); res.Status != http.StatusUnauthorized && res.Status != http.StatusForbidden {
		t.Errorf("signed out PUT: %d %s", res.Status, res.Body)
	}
	if res := a.Do("PUT", "/api/accessibility", `{"readableFont":false}`, "X-CSRF-Token", "wrong"); res.Status != http.StatusForbidden {
		t.Errorf("PUT without the right CSRF token: %d %s", res.Status, res.Body)
	}
}
