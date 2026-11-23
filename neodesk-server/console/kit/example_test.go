package kit_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/auditlog"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/limits"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// The group of the package comment, as it would stand in
// console/routes/notes. It is compiled and run here so that the example
// stays true.

type handlers struct {
	d   *kit.Deps
	log *kit.Logger
}

// register adds the group's routes (Register in a real group).
func register(d *kit.Deps) {
	h := &handlers{d: d, log: d.Log.Child("NOTES")}
	r := d.Router
	r.Get("/api/notes/{device}", h.list, d.RequireAuth, d.RequirePermission("device.view"))
	r.Post("/api/notes/{device}", h.add, d.Limits.Upload.Middleware, d.RequireAuth, d.RequirePermission("device.edit"))
}

// added is the answer to a new note. A struct keeps the keys in the order
// the old route sent them; a map would sort them.
type added struct {
	Success bool      `json:"success"`
	Data    addedData `json:"data"`
}

type addedData struct {
	ID int64 `json:"id"`
}

type note struct {
	ID     int64  `json:"id"`
	Author string `json:"author"`
	Text   string `json:"text"`
}

// list answers GET /api/notes/{device}.
func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	notes, err := listNotes(r.Context(), h.d.Store, r.PathValue("device"))
	if err != nil {
		h.log.Error("List notes error:", err)
		kit.Fail(w, r, http.StatusInternalServerError, "Failed to load notes")
		return
	}
	kit.OK(w, r, notes)
}

// add answers POST /api/notes/{device} with the body {"text":"..."}.
func (h *handlers) add(w http.ResponseWriter, r *http.Request) {
	device := r.PathValue("device")
	text := strings.TrimSpace(kit.Body(r).Text("text", ""))
	if text == "" {
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "notes.text_required", nil))
		return
	}
	if h.d.GoAPI.GetPeer(r.Context(), device) == nil {
		kit.Fail(w, r, http.StatusNotFound, "Device not found")
		return
	}
	user := kit.User(r)
	id, err := insertNote(r.Context(), h.d.Store, device, user.Username, text)
	if err != nil {
		h.log.Error("Add note error:", err)
		kit.Fail(w, r, http.StatusInternalServerError, "Failed to add note")
		return
	}
	_ = h.d.LogAction(r, "note_added", "Note added to device "+device)
	kit.JSON(w, r, http.StatusCreated, added{Success: true, Data: addedData{ID: id}})
}

func insertNote(ctx context.Context, db *store.DB, device, author, text string) (int64, error) {
	id, err := db.Insert(ctx, "INSERT INTO device_notes (device_id, author, text) VALUES (?, ?, ?)", device, author, text)
	if err != nil {
		return 0, fmt.Errorf("insert note for device %s: %w", device, err)
	}
	return id, nil
}

func listNotes(ctx context.Context, db *store.DB, device string) ([]note, error) {
	rows, err := db.Query(ctx, "SELECT id, author, text FROM device_notes WHERE device_id = ? ORDER BY id", device)
	if err != nil {
		return nil, fmt.Errorf("list notes of device %s: %w", device, err)
	}
	defer rows.Close()
	notes := []note{}
	for rows.Next() {
		var n note
		if err := rows.Scan(&n.ID, &n.Author, &n.Text); err != nil {
			return nil, fmt.Errorf("read a note of device %s: %w", device, err)
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}

// fakeAPI is an API server that knows one device.
func fakeAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/api/peers/900100001" {
		_, _ = w.Write([]byte(`{"id":"900100001","hostname":"pc-1"}`))
		return
	}
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"error":"Peer not found"}`))
}

func TestExampleGroup(t *testing.T) {
	db := storetest.SQLite(t)
	ctx := context.Background()
	for _, ddl := range []string{
		"CREATE TABLE device_notes (id INTEGER PRIMARY KEY AUTOINCREMENT, device_id TEXT, author TEXT, text TEXT)",
		"CREATE TABLE audit_log (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER, action TEXT NOT NULL, details TEXT, ip_address TEXT, created_at TEXT DEFAULT (datetime('now')))",
	} {
		if _, err := db.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	sessions, err := session.New(ctx, session.Options{DB: db, Secret: "example-secret-0123456789abcdef0123456789"})
	if err != nil {
		t.Fatal(err)
	}
	d := &kit.Deps{
		Router:   kit.NewRouter(),
		Store:    db,
		Sessions: sessions,
		GoAPI:    goapi.New(http.HandlerFunc(fakeAPI)),
		Audit:    auditlog.New(db),
		Log:      kit.NewLogger("error", true),
		Limits:   limits.NewSet(limits.Options{ClientIP: httpx.SocketIP, UserKey: func(*http.Request) string { return "" }}),
	}
	register(d)
	d.Router.Get("/signin/{role}", func(w http.ResponseWriter, r *http.Request) {
		s := kit.Session(r)
		if err := s.Regenerate(); err != nil {
			t.Error(err)
		}
		ids := map[string]int64{"admin": 7, "viewer": 8}
		role := r.PathValue("role")
		if _, err := s.Establish(session.User{ID: ids[role], Username: "e2e_" + role, Role: role}, session.Meta{}); err != nil {
			t.Error(err)
		}
	})

	// The steps of the pipeline a route needs in a test: the body, the
	// session and the request accessors. Without the pipeline's step 10
	// the guards decide by the built-in role map.
	steps := middleware.Bodies(middleware.BodyOptions{})(sessions.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !d.Router.Serve(w, kit.Begin(r, httpx.TrustProxy{}, nil)) {
			http.NotFound(w, r)
		}
	})))
	call := func(cookie, method, target, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.RemoteAddr = "203.0.113.7:5555"
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		steps.ServeHTTP(rec, req)
		return rec
	}
	signIn := func(role string) string {
		pair, _, _ := strings.Cut(call("", "GET", "/signin/"+role, "").Header().Get("Set-Cookie"), ";")
		if !strings.HasPrefix(pair, "nd.sid=") {
			t.Fatalf("sign in as %s set the cookie %q", role, pair)
		}
		return pair
	}
	admin, viewer := signIn("admin"), signIn("viewer")

	if rec := call("", "GET", "/api/notes/900100001", ""); rec.Code != 401 || rec.Body.String() != `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}` {
		t.Errorf("signed out: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(viewer, "POST", "/api/notes/900100001", `{"text":"x"}`); rec.Code != 403 || rec.Body.String() != `{"success":false,"error":"Permission denied: device.edit"}` {
		t.Errorf("viewer adds: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(admin, "POST", "/api/notes/900100001", `{"text":"  "}`); rec.Code != 400 || rec.Body.String() != `{"success":false,"error":"notes.text_required"}` {
		t.Errorf("empty text: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(admin, "POST", "/api/notes/1", `{"text":"x"}`); rec.Code != 404 || rec.Body.String() != `{"success":false,"error":"Device not found"}` {
		t.Errorf("unknown device: %d %s", rec.Code, rec.Body.String())
	}
	rec := call(admin, "POST", "/API/Notes/900100001/", `{"text":" fan is loud "}`)
	if rec.Code != 201 || rec.Body.String() != `{"success":true,"data":{"id":1}}` {
		t.Errorf("add: %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("RateLimit-Limit") != "30" {
		t.Errorf("the upload limiter did not count the write: %v", rec.Header())
	}
	if rec := call(viewer, "GET", "/api/notes/900100001", ""); rec.Code != 200 || rec.Body.String() != `{"success":true,"data":[{"id":1,"author":"e2e_admin","text":"fan is loud"}]}` {
		t.Errorf("list: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(viewer, "GET", "/api/notes/2", ""); rec.Body.String() != `{"success":true,"data":[]}` {
		t.Errorf("empty list: %s", rec.Body.String())
	}

	var user int64
	var action, details, ip string
	if err := db.QueryRow(ctx, "SELECT user_id, action, details, ip_address FROM audit_log").Scan(&user, &action, &details, &ip); err != nil {
		t.Fatal(err)
	}
	if user != 7 || action != "note_added" || details != "Note added to device 900100001" || ip != "203.0.113.7" {
		t.Errorf("audit row = %d %q %q %q", user, action, details, ip)
	}
}
