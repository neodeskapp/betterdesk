package tickets

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/auditlog"
	"github.com/neodeskapp/neodesk/neodesk-server/console/authn"
	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/devicegroups"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/limits"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/webui"
)

// group is the ticket routes alone: the body parser, the session and the
// route table, without the rest of the console's pipeline. So a test
// reaches what the routes themselves decide, also where the panel access
// guard of the console would have answered first.
type group struct {
	t       *testing.T
	db      *store.DB
	h       *handlers
	router  *kit.Router
	serve   http.Handler
	cookies map[string]string
}

var testUsers = map[string]session.User{
	"admin":        {ID: 1, Username: "e2e_admin", Role: "admin"},
	"operator":     {ID: 5, Username: "e2e_operator", Role: "operator"},
	"viewer":       {ID: 6, Username: "e2e_viewer", Role: "viewer"},
	"guest_issuer": {ID: 7, Username: "e2e_guest_issuer", Role: "guest_issuer"},
	"pro":          {ID: 8, Username: "e2e_pro", Role: "pro"},
}

// peers stands in for the API server of the device layer: it knows the
// devices A, B and C.
var peers = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/api/peers" {
		_, _ = w.Write([]byte(`{"peers":[{"id":"A","hostname":"a"},{"id":"B","hostname":"b"},{"id":"C","hostname":"c"}]}`))
		return
	}
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"error":"not found"}`))
})

func newGroup(t *testing.T, db *store.DB) *group {
	t.Helper()
	ctx := context.Background()
	sessions, err := session.New(ctx, session.Options{DB: db, Secret: "tickets-secret-0123456789abcdef0123456789"})
	if err != nil {
		t.Fatal(err)
	}
	devices, err := authn.New(authn.Options{DB: db, GoAPI: goapi.New(http.NotFoundHandler())})
	if err != nil {
		t.Fatal(err)
	}
	if err := Ensure(ctx, db); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	scopes := access.New(db, access.NewDBRoleSource(nil), access.Options{})
	groups := devicegroups.New(devicegroups.Options{DB: db, Access: scopes})
	d := &kit.Deps{
		Access:       scopes,
		DeviceGroups: groups,
		Backend:      goapi.NewBackend(goapi.New(peers), devicegroups.DeviceStore{Groups: groups}),
		Authn:        devices,
		Router:       kit.NewRouter().Mount("/api/tickets"),
		Config:       &config.Config{ConsoleDir: dir, Production: true},
		Store:        db,
		Sessions:     sessions,
		Audit:        auditlog.New(db),
		Log:          kit.NewLogger("error", true),
		Limits:       limits.NewSet(limits.Options{ClientIP: httpx.SocketIP, UserKey: func(*http.Request) string { return "" }, Getenv: func(string) string { return "1000000" }}),
		WebUI:        webui.New(webui.Options{Dir: filepath.Join(dir, "no-build"), Production: true}),
	}
	g := &group{t: t, db: db, h: newHandlers(d), cookies: map[string]string{}}
	register(d, g.h)
	root := kit.NewRouter()
	root.Get("/signin/{role}", func(w http.ResponseWriter, r *http.Request) {
		s := kit.Session(r)
		if err := s.Regenerate(); err != nil {
			t.Error(err)
		}
		if _, err := s.Establish(testUsers[r.PathValue("role")], session.Meta{}); err != nil {
			t.Error(err)
		}
	})
	g.router = d.Router
	g.serve = middleware.Bodies(middleware.BodyOptions{})(sessions.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = kit.Begin(r, httpx.TrustProxy{}, nil)
		if !root.Serve(w, r) && !d.Router.Serve(w, r) {
			http.NotFound(w, r)
		}
	})))
	return g
}

// do sends a request as the role ("" for nobody) with a JSON body.
func (g *group) do(role, method, target, body string) *httptest.ResponseRecorder {
	g.t.Helper()
	if role != "" && g.cookies[role] == "" {
		rec := httptest.NewRecorder()
		g.serve.ServeHTTP(rec, httptest.NewRequest("GET", "/signin/"+role, nil))
		g.cookies[role], _, _ = strings.Cut(rec.Header().Get("Set-Cookie"), ";")
		if g.cookies[role] == "" {
			g.t.Fatalf("sign in as %s set no cookie", role)
		}
	}
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.RemoteAddr = "203.0.113.9:1234"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if role != "" {
		req.Header.Set("Cookie", g.cookies[role])
	}
	rec := httptest.NewRecorder()
	g.serve.ServeHTTP(rec, req)
	return rec
}

func (g *group) want(rec *httptest.ResponseRecorder, status int, body string) {
	g.t.Helper()
	if rec.Code != status || (body != "" && rec.Body.String() != body) {
		g.t.Errorf("got %d %s, want %d %s", rec.Code, rec.Body.String(), status, body)
	}
}

// The device routes come last, as in the Node file: an address the
// routes with a number pass on reaches them.
func TestTheDeviceRoutesComeLast(t *testing.T) {
	g := newGroup(t, storetest.Migrated(t))
	var paths []string
	for _, route := range g.router.Routes() {
		paths = append(paths, route.Method+" "+route.Path)
	}
	want := "GET /api/tickets, GET /api/tickets/stats, POST /api/tickets, GET /api/tickets/{id}, PATCH /api/tickets/{id}, DELETE /api/tickets/{id}, " +
		"POST /api/tickets/{id}/comments, GET /api/tickets/{id}/comments, POST /api/tickets/{id}/attachments, GET /api/tickets/{id}/attachments, GET /api/tickets/attachments/{aid}, " +
		"POST /api/tickets/nd, GET /api/tickets/nd, GET /api/tickets/nd/{id}, POST /api/tickets/nd/{id}/comments, GET /api/tickets/nd/{id}/attachments/{aid}, POST /api/tickets/nd/{id}/read"
	if got := strings.Join(paths, ", "); got != want {
		t.Errorf("routes: %s\nwant:   %s", got, want)
	}
	// A session is no device token
	for _, method := range []string{"GET", "POST"} {
		g.want(g.do("admin", method, "/api/tickets/nd", `{"title":"x"}`), 401, `{"error":"Bearer access token required"}`)
	}
	// An id that is no number is passed on
	for _, target := range []string{"/api/tickets/abc", "/api/tickets/1x/comments", "/api/tickets/attachments/x"} {
		if rec := g.do("admin", "GET", target, ""); rec.Code != 404 || !strings.Contains(rec.Body.String(), "404 page not found") {
			t.Errorf("GET %s: %d %s, want it left to the next route", target, rec.Code, rec.Body.String())
		}
	}
}

// An id of digits beyond what an id column holds names no ticket and no
// attachment on either database: 404 for the ticket's routes and the
// download, no comments and no attachments for the lists, where
// PostgreSQL failed with 500 (BUGS C7). Leading zeros still name the row.
func TestIDsBeyondTheColumnNameNoTicket(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		g := newGroup(t, db)
		g.want(g.do("operator", "POST", "/api/tickets", `{"title":"Printer","assigned_to":"e2e_viewer"}`), 201, `{"success":true,"ticket":{"id":1,"title":"Printer"}}`)
		notFound := `{"error":"Ticket not found"}`
		for _, id := range []string{"2147483648", "4294967297", "99999999999999999999", "99999999999999999999999", "1" + strings.Repeat("0", 400)} {
			g.want(g.do("admin", "GET", "/api/tickets/"+id, ""), 404, notFound)
			g.want(g.do("admin", "PATCH", "/api/tickets/"+id, `{"title":"x"}`), 404, notFound)
			g.want(g.do("admin", "POST", "/api/tickets/"+id+"/comments", `{"body":"x"}`), 404, notFound)
			g.want(g.do("admin", "POST", "/api/tickets/"+id+"/attachments", `{"filename":"a","data":"aGk="}`), 404, notFound)
			g.want(g.do("admin", "GET", "/api/tickets/"+id+"/comments", ""), 200, `{"comments":[]}`)
			g.want(g.do("admin", "GET", "/api/tickets/"+id+"/attachments", ""), 200, `{"attachments":[]}`)
			g.want(g.do("admin", "GET", "/api/tickets/attachments/"+id, ""), 404, `{"error":"Attachment not found"}`)
			g.want(g.do("admin", "DELETE", "/api/tickets/"+id, ""), 404, notFound)
		}
		if rec := g.do("viewer", "GET", "/api/tickets/0000000001", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"title":"Printer"`) {
			t.Errorf("ticket 0000000001: %d %s", rec.Code, rec.Body.String())
		}
		g.want(g.do("admin", "GET", "/api/tickets/2147483647", ""), 404, notFound)
	})
}

// What the routes decide by themselves (tests/tickets.permissions.test.js):
// nobody without a session, reads with device.view, writes and comments
// with device.edit, as the panel access guard in front of them asks
// (DECISIONS A43, BUGS TK5).
func TestWhoMayReadCommentAndWrite(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		g := newGroup(t, db)
		g.want(g.do("", "GET", "/api/tickets", ""), 401, `{"error":"Authentication required"}`)
		g.want(g.do("", "POST", "/api/tickets", `{"title":"x"}`), 401, `{"error":"Authentication required"}`)
		g.want(g.do("", "POST", "/api/tickets/1/attachments", `{"filename":"a","data":"aGk="}`), 401, `{"error":"Authentication required"}`)

		g.want(g.do("operator", "POST", "/api/tickets", `{"title":"Printer","assigned_to":"e2e_viewer"}`), 201, `{"success":true,"ticket":{"id":1,"title":"Printer"}}`)
		for _, role := range []string{"viewer", "operator", "admin"} {
			g.want(g.do(role, "GET", "/api/tickets", ""), 200, "")
			g.want(g.do(role, "GET", "/api/tickets/1", ""), 200, "")
			g.want(g.do(role, "GET", "/api/tickets/1/comments", ""), 200, "")
		}
		g.want(g.do("viewer", "POST", "/api/tickets/1/comments", `{"body":"hi"}`), 403, `{"success":false,"error":"Permission denied: device.edit"}`)
		g.want(g.do("operator", "POST", "/api/tickets/1/comments", `{"body":"public"}`), 201, "")
		g.want(g.do("admin", "POST", "/api/tickets/1/comments", `{"body":"hi","is_internal":true}`), 201, "")
		// A viewer sees no internal comment
		var internal []bool
		rows, err := db.Query(context.Background(), "SELECT author, is_internal FROM ticket_comments ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var author string
			var flag store.Bool
			if err := rows.Scan(&author, &flag); err != nil {
				t.Fatal(err)
			}
			internal = append(internal, flag.True())
		}
		rows.Close()
		if len(internal) != 2 || internal[0] || !internal[1] {
			t.Errorf("internal flags of the comments by operator, admin: %v", internal)
		}
		var seen struct {
			Comments []struct {
				Author string `json:"author"`
			} `json:"comments"`
		}
		_ = json.Unmarshal(g.do("viewer", "GET", "/api/tickets/1/comments", "").Body.Bytes(), &seen)
		if len(seen.Comments) != 1 || seen.Comments[0].Author != "e2e_operator" {
			t.Errorf("a viewer sees the comments %+v", seen.Comments)
		}

		denied := `{"success":false,"error":"Permission denied: device.edit"}`
		g.want(g.do("viewer", "POST", "/api/tickets", `{"title":"x"}`), 403, denied)
		g.want(g.do("viewer", "PATCH", "/api/tickets/1", `{"status":"closed"}`), 403, denied)
		g.want(g.do("viewer", "POST", "/api/tickets/1/attachments", `{"filename":"a","data":"aGk="}`), 403, denied)
		g.want(g.do("viewer", "DELETE", "/api/tickets/1", ""), 403, denied)
		for _, target := range []string{"/api/tickets", "/api/tickets/1", "/api/tickets/1/comments", "/api/tickets/stats", "/api/tickets/1/attachments", "/api/tickets/attachments/1"} {
			g.want(g.do("pro", "GET", target, ""), 403, `{"success":false,"error":"Permission denied: device.view"}`)
		}
		g.want(g.do("pro", "POST", "/api/tickets/1/comments", `{"body":"hi"}`), 403, `{"success":false,"error":"Permission denied: device.edit"}`)

		// PATCH sets and clears the device (tests/dbAdapter.ticketDevice.test.js)
		device := func() store.NullString {
			var id store.NullString
			if err := db.QueryRow(context.Background(), "SELECT device_id FROM tickets WHERE id = 1").Scan(&id); err != nil {
				t.Fatal(err)
			}
			return id
		}
		g.want(g.do("operator", "PATCH", "/api/tickets/1", `{"device_id":"900100001"}`), 200, `{"success":true}`)
		if got := device(); got.String != "900100001" {
			t.Errorf("device after setting it: %+v", got)
		}
		g.want(g.do("operator", "PATCH", "/api/tickets/1", `{"device_id":null}`), 200, `{"success":true}`)
		if got := device(); got.Valid {
			t.Errorf("device after clearing it: %+v", got)
		}
		g.want(g.do("operator", "DELETE", "/api/tickets/1", ""), 200, `{"success":true}`)
	})
}

func TestSLADueIsCountedOnTheLocalClock(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no time zone data")
	}
	old := time.Local
	time.Local = berlin
	defer func() { time.Local = old }()

	h := &handlers{}
	for _, tt := range []struct {
		now, priority, want string
	}{
		{"2026-11-10T08:30:15.123456Z", "critical", "2026-11-10T12:30:15.123Z"},
		{"2026-11-10T08:30:15.123456Z", "high", "2026-11-10T16:30:15.123Z"},
		{"2026-11-10T08:30:15.123456Z", "medium", "2026-11-11T08:30:15.123Z"},
		{"2026-11-10T08:30:15.123456Z", "low", "2026-11-13T08:30:15.123Z"},
		{"2026-11-10T08:30:15.123456Z", "", "2026-11-11T08:30:15.123Z"},
		{"2026-11-10T08:30:15.123456Z", "urgent", "2026-11-11T08:30:15.123Z"},
		// The night the clocks go forward: four hours later on the clock
		// are three hours later in time
		{"2026-03-28T23:30:00Z", "critical", "2026-03-29T02:30:00.000Z"},
		// The night they go back: 72 hours on the clock are 73 in time
		{"2026-10-23T10:00:00Z", "low", "2026-10-26T11:00:00.000Z"},
	} {
		now, _ := time.Parse(time.RFC3339Nano, tt.now)
		h.now = func() time.Time { return now }
		if got := h.slaDue(tt.priority); got != tt.want {
			t.Errorf("slaDue(%q) at %s = %s, want %s", tt.priority, tt.now, got, tt.want)
		}
	}
}

func TestANewTicketGetsItsDueTime(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		g := newGroup(t, db)
		now := time.Date(2030, 1, 2, 3, 4, 5, 678e6, time.UTC)
		g.h.now = func() time.Time { return now }
		g.want(g.do("admin", "POST", "/api/tickets", `{"title":"due","priority":"high"}`), 201, "")
		var detail struct {
			SLA string `json:"sla_due_at"`
		}
		_ = json.Unmarshal(g.do("admin", "GET", "/api/tickets/1", "").Body.Bytes(), &detail)
		want := store.ISOTime(now.Add(8 * time.Hour))
		if time.Local != time.UTC && time.Local.String() != "UTC" {
			want = g.h.slaDue("high")
		}
		if detail.SLA != want {
			t.Errorf("sla_due_at = %q, want %q", detail.SLA, want)
		}
	})
}

func TestAnUploadIsStoredUnderTheTicketAndARandomName(t *testing.T) {
	g := newGroup(t, storetest.Migrated(t))
	g.h.random = func(b []byte) (int, error) {
		for i := range b {
			b[i] = byte(i + 0xa0)
		}
		return len(b), nil
	}
	if info, err := os.Stat(g.h.uploads); err != nil || !info.IsDir() {
		t.Fatalf("the attachment folder was not made: %v", err)
	}
	g.want(g.do("admin", "POST", "/api/tickets", `{"title":"files"}`), 201, "")
	g.want(g.do("admin", "POST", "/api/tickets/1/attachments", `{"filename":"re port/📎.txt","data":"aGVsbG8="}`), 201,
		`{"success":true,"attachment":{"id":1,"filename":"re_port___.txt","size_bytes":5}}`)
	path := filepath.Join(g.h.uploads, "1_a0a1a2a3a4a5a6a7_re_port___.txt")
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "hello" {
		t.Fatalf("stored file: %q, %v", content, err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm()&0o111 != 0 || info.Mode().Perm()&0o600 != 0o600 {
		t.Errorf("mode of the stored file: %v", info.Mode())
	}
	var stored string
	if err := g.db.QueryRow(context.Background(), "SELECT storage_path FROM ticket_attachments WHERE id = 1").Scan(&stored); err != nil || stored != filepath.Base(path) {
		t.Errorf("storage_path = %q, %v; want %q", stored, err, filepath.Base(path))
	}

	// A row that points out of the folder is read by its name in the folder:
	// the file there is missing, and the one outside is not deleted
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := g.db.Exec(context.Background(), "INSERT INTO ticket_attachments (ticket_id, filename, storage_path, uploaded_by) VALUES (1, 'x', ?, 'x')", outside); err != nil {
		t.Fatal(err)
	}
	g.want(g.do("admin", "GET", "/api/tickets/attachments/2", ""), 404, `{"error":"Attachment file missing"}`)
	link := filepath.Join(g.h.uploads, "1_link")
	if err := os.Symlink(outside, link); err == nil {
		if _, err := g.db.Exec(context.Background(), "INSERT INTO ticket_attachments (ticket_id, filename, storage_path, uploaded_by) VALUES (1, 'l', ?, 'x')", link); err != nil {
			t.Fatal(err)
		}
		g.want(g.do("admin", "GET", "/api/tickets/attachments/3", ""), 500, `{"error":"Internal server error"}`)
	}
	g.want(g.do("admin", "DELETE", "/api/tickets/1", ""), 200, `{"success":true}`)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the ticket's file is still there: %v", err)
	}
	if content, err := os.ReadFile(outside); err != nil || string(content) != "secret" {
		t.Errorf("a file outside the attachment folder was touched: %q, %v", content, err)
	}
}

// File data is standard base64 with its padding; white space in it is
// skipped, and anything else is no file (BUGS TK7).
func TestFileBytes(t *testing.T) {
	for _, tt := range []struct{ text, want string }{
		{"aGVsbG8=", "hello"}, {"aGVs bG8=\n", "hello"}, {"\taGk=\r\n", "hi"}, {"a+/b", "k\xef\xdb"}, {"AAAA", "\x00\x00\x00"},
	} {
		if got, err := fileBytes(jsbind.String(tt.text)); err != nil || string(got) != tt.want {
			t.Errorf("fileBytes(%q) = %q, %v; want %q", tt.text, got, err, tt.want)
		}
	}
	for _, text := range []string{"aGVsbG8", "aGk=aGk=", "=", "a", "ab", "a-_b", "!!!!", "YQ==YQ==", "中中中中", "", " \n", "aGk=!"} {
		if got, err := fileBytes(jsbind.String(text)); err == nil {
			t.Errorf("fileBytes(%q) = %q, want an error", text, got)
		}
	}
	if _, err := fileBytes(jsbind.Number(5)); err == nil {
		t.Error("a number is no file")
	}
	if _, err := fileBytes(jsbind.Bool(true)); err == nil {
		t.Error("a flag is no file")
	}
	if got := safeName("a b/c\\d.txt\U0001F4CEü-_.Z9"); got != "a_b_c_d.txt___-_.Z9" {
		t.Errorf("safeName = %q", got)
	}
}

// The file data of an upload is base64 text. A list, an object that looks
// like a list or a stored buffer, a number or a flag is refused before
// anything is made of it: an object with a large length costs no memory
// (BUGS TK6).
func TestAttachmentDataMustBeText(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		g := newGroup(t, db)
		g.want(g.do("admin", "POST", "/api/tickets", `{"title":"files"}`), 201, "")
		refused := `{"error":"Filename and data (base64) are required"}`
		for _, data := range []string{
			`{"length":4000000}`, `{"length":1000000000}`, `[104,105]`, `{"type":"Buffer","data":[104,105]}`, `{"0":65,"length":1}`, `{}`, `5`, `true`,
		} {
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			g.want(g.do("admin", "POST", "/api/tickets/1/attachments", `{"filename":"x.bin","data":`+data+`}`), 400, refused)
			runtime.ReadMemStats(&after)
			if grown := after.TotalAlloc - before.TotalAlloc; grown > 64<<20 {
				t.Errorf("data %s: the upload allocated %d MB", data, grown>>20)
			}
		}
		if files, err := os.ReadDir(g.h.uploads); err != nil || len(files) != 0 {
			t.Errorf("files stored: %d, %v", len(files), err)
		}
		var rows int
		if err := db.QueryRow(context.Background(), "SELECT COUNT(*) FROM ticket_attachments").Scan(&rows); err != nil || rows != 0 {
			t.Errorf("attachment rows: %d, %v", rows, err)
		}
		g.want(g.do("admin", "POST", "/api/tickets/1/attachments", `{"filename":"x.bin","data":"aGk="}`), 201, `{"success":true,"attachment":{"id":1,"filename":"x.bin","size_bytes":2}}`)
	})
}

// A title and a comment are text with something in it: any other value is
// 400, on create and in a change, and a change keeps the title it had
// (BUGS TK2).
func TestTitleAndCommentThatAreNoText(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		g := newGroup(t, db)
		g.want(g.do("admin", "POST", "/api/tickets", `{"title":"Kept"}`), 201, "")
		title := `{"error":"Title is required"}`
		for _, value := range []string{`5`, `true`, `["x"]`, `[]`, `{"a":1}`} {
			g.want(g.do("admin", "POST", "/api/tickets", `{"title":`+value+`}`), 400, title)
			g.want(g.do("admin", "POST", "/api/tickets/1/comments", `{"body":`+value+`}`), 400, `{"error":"Comment body is required"}`)
		}
		for _, value := range []string{`null`, `""`, `"  "`, `5`, `["x"]`} {
			g.want(g.do("admin", "PATCH", "/api/tickets/1", `{"title":`+value+`}`), 400, title)
		}
		var kept string
		if err := db.QueryRow(context.Background(), "SELECT title FROM tickets WHERE id = 1").Scan(&kept); err != nil || kept != "Kept" {
			t.Errorf("title after the refused changes: %q, %v", kept, err)
		}
		for table, want := range map[string]int{"tickets": 1, "ticket_comments": 0} {
			var n int
			if err := db.QueryRow(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil || n != want {
				t.Errorf("rows in %s: %d, %v; want %d", table, n, err, want)
			}
		}
		g.want(g.do("admin", "PATCH", "/api/tickets/1", `{"title":" Renamed "}`), 200, `{"success":true}`)
	})
}

// Every member of a ticket has its type, on create and in a change, so a
// value goes in the same on both databases or not at all (BUGS TK1).
func TestTicketMembersOfTheWrongType(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		g := newGroup(t, db)
		for body, want := range map[string]string{
			`{"title":"x","description":5}`:                        `{"error":"Invalid description"}`,
			`{"title":"x","description":true}`:                     `{"error":"Invalid description"}`,
			`{"title":"x","description":{},"device_id":["a","b"]}`: `{"error":"Invalid description"}`,
			`{"title":"x","device_id":["a","b"]}`:                  `{"error":"Invalid device_id"}`,
			`{"title":"x","device_id":0}`:                          `{"error":"Invalid device_id"}`,
			`{"title":"x","assigned_to":7}`:                        `{"error":"Invalid assigned_to"}`,
			`{"title":"x","assigned_to":{"a":1}}`:                  `{"error":"Invalid assigned_to"}`,
		} {
			g.want(g.do("admin", "POST", "/api/tickets", body), 400, want)
		}
		var n int
		if err := db.QueryRow(context.Background(), "SELECT COUNT(*) FROM tickets").Scan(&n); err != nil || n != 0 {
			t.Fatalf("tickets stored: %d, %v", n, err)
		}
		g.want(g.do("admin", "POST", "/api/tickets", `{"title":"Kept","description":null,"device_id":"","assigned_to":null}`), 201, "")
		for body, want := range map[string]string{
			`{"description":5}`:              `{"error":"Invalid description"}`,
			`{"description":["a"]}`:          `{"error":"Invalid description"}`,
			`{"assigned_to":7}`:              `{"error":"Invalid assigned_to"}`,
			`{"category":"anything goes"}`:   `{"error":"Invalid category. Valid: general, hardware, software, network, access, other"}`,
			`{"category":null}`:              `{"error":"Invalid category. Valid: general, hardware, software, network, access, other"}`,
			`{"sla_due_at":"x"}`:             `{"error":"Invalid sla_due_at"}`,
			`{"sla_due_at":""}`:              `{"error":"Invalid sla_due_at"}`,
			`{"sla_due_at":5}`:               `{"error":"Invalid sla_due_at"}`,
			`{"sla_due_at":"2031-05-06"}`:    `{"error":"Invalid sla_due_at"}`,
			`{"status":"open","category":5}`: `{"error":"Invalid category. Valid: general, hardware, software, network, access, other"}`,
		} {
			g.want(g.do("admin", "PATCH", "/api/tickets/1", body), 400, want)
		}
		type row struct {
			Category, Due string
			Description   store.NullString
			Assigned      store.NullString
		}
		read := func() row {
			var r row
			var due store.Time
			if err := store.SelectRow(context.Background(), db, "SELECT description, category, assigned_to, sla_due_at FROM tickets WHERE id = 1").Scan(&r.Description, &r.Category, &r.Assigned, &due); err != nil {
				t.Fatal(err)
			}
			text, _ := json.Marshal(due)
			r.Due = string(text)
			return r
		}
		before := read()
		if before.Description.String != "" || before.Category != "general" || before.Assigned.Valid {
			t.Errorf("the ticket as created: %+v", before)
		}
		// A time is stored in UTC with milliseconds, on both databases
		g.want(g.do("admin", "PATCH", "/api/tickets/1", `{"sla_due_at":"2031-05-06T09:08:09+02:00","description":null,"category":"network","assigned_to":"e2e_viewer"}`), 200, `{"success":true}`)
		after := read()
		if after.Due != `"2031-05-06T07:08:09.000Z"` || after.Category != "network" || after.Assigned.String != "e2e_viewer" {
			t.Errorf("the ticket after a change: %+v", after)
		}
		g.want(g.do("admin", "PATCH", "/api/tickets/1", `{"sla_due_at":null}`), 200, `{"success":true}`)
		if got := read(); got.Due != "null" {
			t.Errorf("sla_due_at after null: %s", got.Due)
		}
	})
}

// A change checks status and priority whenever the body has them: an
// empty text, null, 0 or false is refused, so every ticket keeps a status
// the statistics count (BUGS TK3).
func TestUpdateChecksStatusWhenPresent(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		g := newGroup(t, db)
		g.want(g.do("admin", "POST", "/api/tickets", `{"title":"Counted"}`), 201, "")
		for _, value := range []string{`""`, `null`, `0`, `false`} {
			g.want(g.do("admin", "PATCH", "/api/tickets/1", `{"status":`+value+`}`), 400, `{"error":"Invalid status. Valid: open, in_progress, resolved, closed"}`)
			g.want(g.do("admin", "PATCH", "/api/tickets/1", `{"priority":`+value+`}`), 400, `{"error":"Invalid priority. Valid: low, medium, high, critical"}`)
		}
		g.want(g.do("admin", "GET", "/api/tickets/stats", ""), 200, `{"total":1,"open":1,"in_progress":0,"resolved":0,"closed":0}`)
		var priority string
		if err := db.QueryRow(context.Background(), "SELECT priority FROM tickets WHERE id = 1").Scan(&priority); err != nil || priority != "medium" {
			t.Errorf("priority after the refused changes: %q, %v", priority, err)
		}
	})
}

// A list filter is one text: given twice, in brackets or as an object it
// is 400 on both databases, where SQLite failed and PostgreSQL found
// nothing (BUGS TK4). A viewer's assigned_to is not read.
func TestListFiltersTakeOneTextEach(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		g := newGroup(t, db)
		g.want(g.do("admin", "POST", "/api/tickets", `{"title":"Listed","assigned_to":"e2e_viewer"}`), 201, "")
		for query, want := range map[string]string{
			"status=open&status=closed":         `{"error":"Invalid status"}`,
			"status[]=open":                     `{"error":"Invalid status"}`,
			"status[x]=1&priority=a&priority=b": `{"error":"Invalid status"}`,
			"status=open&priority=a&priority=b": `{"error":"Invalid priority"}`,
			"category[0]=general":               `{"error":"Invalid category"}`,
			"assigned_to=a&assigned_to=b":       `{"error":"Invalid assigned_to"}`,
			"device_id[a][b]=c":                 `{"error":"Invalid device_id"}`,
		} {
			g.want(g.do("admin", "GET", "/api/tickets?"+query, ""), 400, want)
		}
		for _, query := range []string{"status=open", "status=", "search=Listed&search=x"} {
			if rec := g.do("admin", "GET", "/api/tickets?"+query, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"total":1`) {
				t.Errorf("?%s: %d %s", query, rec.Code, rec.Body.String())
			}
		}
		if rec := g.do("viewer", "GET", "/api/tickets?assigned_to[]=x&assigned_to=y", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"total":1`) {
			t.Errorf("a viewer's list: %d %s", rec.Code, rec.Body.String())
		}
	})
}

// File data is standard base64, with or without white space in it; text
// that is no base64, or that holds no bytes, is refused (BUGS TK7).
func TestAttachmentNotBase64IsRefused(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		g := newGroup(t, db)
		g.want(g.do("admin", "POST", "/api/tickets", `{"title":"files"}`), 201, "")
		refused := `{"error":"Filename and data (base64) are required"}`
		for _, data := range []string{"a G-k_!=Zm9v", "!!!!", "=", "a", "ab", "aGVsbG8", "a-_b", "aGk=aGk=", "éééé", " ", "YQ=YQ"} {
			body, _ := json.Marshal(map[string]string{"filename": "x.bin", "data": data})
			g.want(g.do("admin", "POST", "/api/tickets/1/attachments", string(body)), 400, refused)
		}
		if files, err := os.ReadDir(g.h.uploads); err != nil || len(files) != 0 {
			t.Errorf("files stored: %d, %v", len(files), err)
		}
		g.want(g.do("admin", "POST", "/api/tickets/1/attachments", `{"filename":"x.bin","data":"aGVs bG8=\n"}`), 201, `{"success":true,"attachment":{"id":1,"filename":"x.bin","size_bytes":5}}`)
	})
}

// A file whose row cannot be written does not stay in the folder.
func TestAFailedUploadLeavesNoFile(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		g := newGroup(t, db)
		g.want(g.do("admin", "POST", "/api/tickets", `{"title":"files"}`), 201, "")
		if _, err := db.Exec(context.Background(), "ALTER TABLE ticket_attachments RENAME TO ticket_attachments_gone"); err != nil {
			t.Fatal(err)
		}
		g.want(g.do("admin", "POST", "/api/tickets/1/attachments", `{"filename":"x.bin","data":"aGk="}`), 500, `{"error":"Internal server error"}`)
		if files, err := os.ReadDir(g.h.uploads); err != nil || len(files) != 0 {
			t.Errorf("files left: %d, %v", len(files), err)
		}
	})
}

// An upload's type is text: other values are refused, and a text that is
// no media type is stored as application/octet-stream. A row that holds
// such a type from before is sent as application/octet-stream (BUGS TK8).
func TestDownloadWithAnOddStoredType(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		g := newGroup(t, db)
		g.want(g.do("admin", "POST", "/api/tickets", `{"title":"files"}`), 201, "")
		for _, mimetype := range []string{`5`, `true`, `["text/plain"]`, `{}`} {
			g.want(g.do("admin", "POST", "/api/tickets/1/attachments", `{"filename":"a.txt","data":"aGk=","mimetype":`+mimetype+`}`), 400, `{"error":"Invalid mimetype"}`)
		}
		for i, mimetype := range []string{`"text/html\r\nX: 1"`, `"tëxt/plain"`, `" "`, `"plain"`, `"text/plain; charset=utf-8"`} {
			g.want(g.do("admin", "POST", "/api/tickets/1/attachments", `{"filename":"a.txt","data":"aGk=","mimetype":`+mimetype+`}`), 201, "")
			want := "application/octet-stream"
			if i == 4 {
				want = "text/plain; charset=utf-8"
			}
			var stored string
			if err := db.QueryRow(context.Background(), "SELECT mimetype FROM ticket_attachments WHERE id = ?", i+1).Scan(&stored); err != nil || stored != want {
				t.Errorf("type stored for %s: %q, %v; want %q", mimetype, stored, err, want)
			}
		}
		if _, err := db.Exec(context.Background(), "UPDATE ticket_attachments SET mimetype = ? WHERE id = 1", "text/html\r\nX: 1"); err != nil {
			t.Fatal(err)
		}
		rec := g.do("admin", "GET", "/api/tickets/attachments/1", "")
		if rec.Code != 200 || rec.Body.String() != "hi" || rec.Header().Get("Content-Type") != "application/octet-stream" || rec.Header().Get("Content-Disposition") != `attachment; filename="a.txt"` {
			t.Errorf("download of a row with a type no header can carry: %d %q %v", rec.Code, rec.Body.String(), rec.Header())
		}
	})
}

// A download the file sender refuses (a range beyond the end, a condition
// that fails) answers the console's JSON error as JSON, without the
// attachment's name (BUGS TK9). The error says what failed, also in
// production (S17).
func TestFailedRangeAnswersJSON(t *testing.T) {
	g := newGroup(t, storetest.Migrated(t))
	g.want(g.do("admin", "POST", "/api/tickets", `{"title":"files"}`), 201, "")
	g.want(g.do("admin", "POST", "/api/tickets/1/attachments", `{"filename":"a.txt","data":"aGVsbG8gdGlja2V0","mimetype":"text/plain"}`), 201, "")
	for _, c := range []struct {
		name, value string
		status      int
		body        string
	}{
		{"Range", "bytes=50-60", 416, `{"success":false,"error":"Range Not Satisfiable"}`},
		{"If-Match", `"nothing"`, 412, `{"success":false,"error":"Precondition Failed"}`},
	} {
		req := httptest.NewRequest("GET", "/api/tickets/attachments/1", nil)
		req.Header.Set("Cookie", g.cookies["admin"])
		req.Header.Set(c.name, c.value)
		rec := httptest.NewRecorder()
		g.serve.ServeHTTP(rec, req)
		if rec.Code != c.status || rec.Body.String() != c.body {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body.String())
		}
		if ct, cd := rec.Header().Get("Content-Type"), rec.Header().Get("Content-Disposition"); ct != "application/json; charset=utf-8" || cd != "" {
			t.Errorf("%s: Content-Type %q, Content-Disposition %q", c.name, ct, cd)
		}
	}
}

// Tickets, comments and attachments made in the same second keep an order:
// tickets newest id first, comments and attachments oldest first (BUGS
// TK11).
func TestListsOfTheSameSecondKeepTheirOrder(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		g := newGroup(t, db)
		for _, title := range []string{"one", "two", "three"} {
			g.want(g.do("admin", "POST", "/api/tickets", `{"title":"`+title+`"}`), 201, "")
		}
		for _, body := range []string{"first", "second", "third"} {
			g.want(g.do("admin", "POST", "/api/tickets/1/comments", `{"body":"`+body+`"}`), 201, "")
			g.want(g.do("admin", "POST", "/api/tickets/1/attachments", `{"filename":"`+body+`.txt","data":"aGk="}`), 201, "")
		}
		// The rows of each table in the reverse order of their ids, all in
		// one second
		for _, table := range []string{"tickets", "ticket_comments", "ticket_attachments"} {
			if _, err := db.Exec(context.Background(), "UPDATE "+table+" SET created_at = "+db.Pick("'2001-01-01 00:00:00'", "'2001-01-01T00:00:00Z'")); err != nil {
				t.Fatal(err)
			}
		}
		var list struct {
			Tickets []struct {
				ID int `json:"id"`
			} `json:"tickets"`
		}
		_ = json.Unmarshal(g.do("admin", "GET", "/api/tickets", "").Body.Bytes(), &list)
		if len(list.Tickets) != 3 || list.Tickets[0].ID != 3 || list.Tickets[1].ID != 2 || list.Tickets[2].ID != 1 {
			t.Errorf("tickets: %+v, want ids 3, 2, 1", list.Tickets)
		}
		var detail struct {
			Comments []struct {
				Body string `json:"body"`
			} `json:"comments"`
			Attachments []struct {
				Filename string `json:"filename"`
			} `json:"attachments"`
		}
		_ = json.Unmarshal(g.do("admin", "GET", "/api/tickets/1", "").Body.Bytes(), &detail)
		if len(detail.Comments) != 3 || detail.Comments[0].Body != "first" || detail.Comments[2].Body != "third" {
			t.Errorf("comments: %+v", detail.Comments)
		}
		if len(detail.Attachments) != 3 || detail.Attachments[0].Filename != "first.txt" || detail.Attachments[2].Filename != "third.txt" {
			t.Errorf("attachments: %+v", detail.Attachments)
		}
	})
}

// unread is a request body that counts what is read of it.
type unread struct {
	left int64
	read int64
}

func (u *unread) Read(p []byte) (int, error) {
	if u.left <= 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > u.left {
		n = u.left
	}
	for i := int64(0); i < n; i++ {
		p[i] = 'A'
	}
	u.left -= n
	u.read += n
	return int(n), nil
}

// The route itself reads the body of an upload last: after the session
// check and after the permission check. (In the console the panel access
// guard refuses a viewer even earlier; this test runs the route alone.)
func TestTheRouteChecksBeforeItReadsAnUpload(t *testing.T) {
	g := newGroup(t, storetest.Migrated(t))
	g.want(g.do("admin", "POST", "/api/tickets", `{"title":"uploads"}`), 201, "")
	for _, tt := range []struct {
		role   string
		status int
		body   string
	}{
		{"", 401, `{"error":"Authentication required"}`},
		{"viewer", 403, `{"success":false,"error":"Permission denied: device.edit"}`},
		{"pro", 403, `{"success":false,"error":"Permission denied: device.edit"}`},
	} {
		g.do(tt.role, "GET", "/api/tickets/stats", "")
		body := &unread{left: 30 << 20}
		req := httptest.NewRequest("POST", "/api/tickets/1/attachments", body)
		req.ContentLength = body.left
		req.Header.Set("Content-Type", "application/json")
		if tt.role != "" {
			req.Header.Set("Cookie", g.cookies[tt.role])
		}
		rec := httptest.NewRecorder()
		g.serve.ServeHTTP(rec, req)
		g.want(rec, tt.status, tt.body)
		if body.read != 0 {
			t.Errorf("%q: %d bytes of the upload were read before the refusal", tt.role, body.read)
		}
	}
	// Someone who may attach gets the body read, and refused as no JSON
	body := &unread{left: 1 << 20}
	req := httptest.NewRequest("POST", "/api/tickets/1/attachments", body)
	req.ContentLength = body.left
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", g.cookies["admin"])
	rec := httptest.NewRecorder()
	g.serve.ServeHTTP(rec, req)
	if rec.Code != 400 || body.read != 1<<20 {
		t.Errorf("an upload that is no JSON by admin: %d %s, %d bytes read", rec.Code, rec.Body.String(), body.read)
	}
}

// What an upload carries decodes to the bytes it stands for when it is
// standard base64, also with white space in it, and any text gives at
// most three bytes per four characters.
func FuzzFileBytes(f *testing.F) {
	for _, seed := range []string{"", "hello", "\x00\xff\xfe", "a", "ab", "abc", "any carnal pleasure."} {
		f.Add([]byte(seed), "junk=!")
	}
	f.Fuzz(func(t *testing.T, data []byte, text string) {
		encoded := base64.StdEncoding.EncodeToString(data)
		spaced := strings.Join(strings.Split(encoded, ""), " \n")
		for _, form := range []string{encoded, spaced} {
			got, err := fileBytes(jsbind.String(form))
			if len(data) == 0 && err == nil {
				t.Errorf("fileBytes(%q) = %x, want an error for no bytes", form, got)
			}
			if len(data) > 0 && (err != nil || !bytes.Equal(got, data)) {
				t.Errorf("fileBytes(%q) = %x, %v; want %x", form, got, err, data)
			}
		}
		if got, err := fileBytes(jsbind.String(text)); err == nil && len(got) > len(text)*3/4 {
			t.Errorf("fileBytes(%q) has %d bytes", text, len(got))
		}
		if name := safeName(text); strings.ContainsAny(name, "/\\\x00") || len(name) < len([]rune(text)) {
			t.Errorf("safeName(%q) = %q", text, name)
		}
	})
}
