package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/chatstore"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// inboxWorld is the chat's HTTP routes of version 2 on a test database
// with the scope accounts (scope_test.go).
type inboxWorld struct {
	t       *testing.T
	db      *store.DB
	st      *chatstore.Store
	handler http.Handler
	// audits are the audit entries the routes wrote: action and details
	audits []string
	// auditLog are the same entries with the account that wrote them
	auditLog []auditEntry
}

// auditEntry is one audit entry the routes wrote.
type auditEntry struct {
	by      int64
	action  string
	details map[string]any
}

func newInboxWorld(t *testing.T, db *store.DB) *inboxWorld {
	t.Helper()
	ctx := context.Background()
	if err := chatstore.Ensure(ctx, db); err != nil {
		t.Fatal(err)
	}
	st := chatstore.New(db, nil)
	scope := realScope(t, db)
	w := &inboxWorld{t: t, db: db, st: st}
	hub := NewHub(HubOptions{Store: st, Scope: scope, IsAdmin: func(role string) bool { return role == "admin" },
		Holds: func(_ context.Context, role, p string) (bool, error) { return p != permAssign || role != "viewer", nil },
		Audit: func(_ context.Context, by int64, action string, details map[string]any) {
			b, _ := json.Marshal(details)
			w.audits = append(w.audits, action+" "+string(b))
			w.auditLog = append(w.auditLog, auditEntry{by: by, action: action, details: details})
		}})
	d := &kit.Deps{Router: kit.NewRouter(), Store: db, Chat: st}
	registerInbox(d, hub).user = testUser
	handler := middleware.Bodies(middleware.BodyOptions{})(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		r = kit.Begin(r, httpx.TrustProxy{}, nil)
		who := &access.Context{}
		if user := testUser(r); user != nil {
			who = &access.Context{UserID: user.ID, HasUser: true, Role: user.Role}
		}
		kit.SetAccess(r, who)
		if !d.Router.Serve(rw, r) {
			http.NotFound(rw, r)
		}
	}))
	w.handler = handler
	return w
}

// send asks the routes with a JSON body as one of the scope accounts.
func (w *inboxWorld) send(who, method, target, body string) (int, map[string]any) {
	w.t.Helper()
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Test-User", who)
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, r)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// get asks the routes as one of the scope accounts and decodes the answer.
func (w *inboxWorld) get(who, target string) (int, map[string]any) {
	w.t.Helper()
	r := httptest.NewRequest("GET", target, nil)
	r.Header.Set("X-Test-User", who)
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, r)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// legacy writes messages as the chat of version 1 stored them and runs the
// import of the first start.
func (w *inboxWorld) legacy(rows ...[3]string) {
	w.t.Helper()
	ctx := context.Background()
	for _, r := range rows {
		if _, err := w.db.Exec(ctx, `INSERT INTO chat_messages (conversation_id, from_id, from_name, text) VALUES (?, 'operator', ?, ?)`, r[0], r[1], r[2]); err != nil {
			w.t.Fatal(err)
		}
	}
	if _, err := w.st.ImportLegacy(ctx); err != nil {
		w.t.Fatal(err)
	}
}

// TestTeamListShowsOldMessagesToAdmins: the team list names the old
// messages of version 1 (history that maps to no two accounts) for admins,
// and for nobody else; the two people of an imported direct message find
// it among their conversations.
func TestTeamListShowsOldMessagesToAdmins(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newInboxWorld(t, db)
		w.legacy([3]string{"operator:ghost", "anna", "to someone gone"}, [3]string{"operator:ben", "anna", "for ben"})

		status, admin := w.get("admin", "/api/chat/team")
		old, _ := admin["old"].([]any)
		if status != 200 || len(old) != 1 {
			t.Fatalf("admin's team list: %d %v", status, admin)
		}
		first := old[0].(map[string]any)
		last, _ := first["last_message"].(map[string]any)
		if first["id"] != "old:operator:ghost" || first["title"] != "ghost" || last["text"] != "to someone gone" {
			t.Errorf("admin's old messages: %v", first)
		}
		for _, who := range []string{"anna", "ben"} {
			status, list := w.get(who, "/api/chat/team")
			if _, ok := list["old"]; status != 200 || ok {
				t.Errorf("%s's team list has old messages: %d %v", who, status, list)
			}
			convs, _ := list["conversations"].([]any)
			if len(convs) != 1 || convs[0].(map[string]any)["id"] != "dm:5:6" {
				t.Errorf("%s's conversations: %v", who, convs)
			}
		}
		if status, _ := w.get("anna", "/api/chat/conversations/old:operator:ghost"); status != 404 {
			t.Errorf("anna opening the old messages: %d", status)
		}
		if status, _ := w.get("admin", "/api/chat/conversations/old:operator:ghost"); status != 200 {
			t.Errorf("admin opening the old messages: %d", status)
		}
	})
}
