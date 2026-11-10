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
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/auditlog"
	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/limits"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
	"github.com/unitronix/betterdesk-server/console/webui"
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
	"admin":    {ID: 1, Username: "e2e_admin", Role: "admin"},
	"operator": {ID: 5, Username: "e2e_operator", Role: "operator"},
	"viewer":   {ID: 6, Username: "e2e_viewer", Role: "viewer"},
	"pro":      {ID: 8, Username: "e2e_pro", Role: "pro"},
}

func newGroup(t *testing.T, db *store.DB) *group {
	t.Helper()
	ctx := context.Background()
	sessions, err := session.New(ctx, session.Options{DB: db, Secret: "tickets-secret-0123456789abcdef0123456789"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	d := &kit.Deps{
		Router:   kit.NewRouter().Mount("/api/tickets"),
		Config:   &config.Config{ConsoleDir: dir, Production: true},
		Store:    db,
		Sessions: sessions,
		Audit:    auditlog.New(db),
		Log:      kit.NewLogger("error", true),
		Limits:   limits.NewSet(limits.Options{ClientIP: httpx.SocketIP, UserKey: func(*http.Request) string { return "" }, Getenv: func(string) string { return "1000000" }}),
		WebUI:    webui.New(webui.Options{Dir: filepath.Join(dir, "no-build"), Production: true}),
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

func TestTheGroupHasNoDeviceRoutes(t *testing.T) {
	g := newGroup(t, storetest.Migrated(t))
	var paths []string
	for _, route := range g.router.Routes() {
		paths = append(paths, route.Method+" "+route.Path)
		if strings.Contains(route.Path, "/bd") {
			t.Errorf("%s %s is registered: the device routes wait for the device token check", route.Method, route.Path)
		}
	}
	want := "GET /api/tickets, GET /api/tickets/stats, POST /api/tickets, GET /api/tickets/{id}, PATCH /api/tickets/{id}, DELETE /api/tickets/{id}, " +
		"POST /api/tickets/{id}/comments, GET /api/tickets/{id}/comments, POST /api/tickets/{id}/attachments, GET /api/tickets/{id}/attachments, GET /api/tickets/attachments/{aid}"
	if got := strings.Join(paths, ", "); got != want {
		t.Errorf("routes: %s\nwant:   %s", got, want)
	}
	// A request for them, and for an id that is no number, is passed on
	for _, target := range []string{"/api/tickets/bd", "/api/tickets/abc", "/api/tickets/1x/comments", "/api/tickets/attachments/x"} {
		if rec := g.do("admin", "GET", target, ""); rec.Code != 404 || !strings.Contains(rec.Body.String(), "404 page not found") {
			t.Errorf("GET %s: %d %s, want it left to the next route", target, rec.Code, rec.Body.String())
		}
	}
}

// What the routes decide by themselves (tests/tickets.permissions.test.js):
// nobody without a session, reads and comments with device.view, writes
// with device.edit.
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
			g.want(g.do(role, "POST", "/api/tickets/1/comments", `{"body":"hi","is_internal":true}`), 201, "")
		}
		// A viewer's comment is never internal, and a viewer sees no internal one
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
		if len(internal) != 3 || internal[0] || !internal[1] || !internal[2] {
			t.Errorf("internal flags of the comments by viewer, operator, admin: %v", internal)
		}
		var seen struct {
			Comments []struct {
				Author string `json:"author"`
			} `json:"comments"`
		}
		_ = json.Unmarshal(g.do("viewer", "GET", "/api/tickets/1/comments", "").Body.Bytes(), &seen)
		if len(seen.Comments) != 1 || seen.Comments[0].Author != "e2e_viewer" {
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
		g.want(g.do("pro", "POST", "/api/tickets/1/comments", `{"body":"hi"}`), 403, `{"success":false,"error":"Permission denied: device.view"}`)

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
	if err := g.db.QueryRow(context.Background(), "SELECT storage_path FROM ticket_attachments WHERE id = 1").Scan(&stored); err != nil || stored != path {
		t.Errorf("storage_path = %q, %v; want %q", stored, err, path)
	}

	// A row that points out of the folder is not served and not deleted
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := g.db.Exec(context.Background(), "INSERT INTO ticket_attachments (ticket_id, filename, storage_path, uploaded_by) VALUES (1, 'x', ?, 'x')", outside); err != nil {
		t.Fatal(err)
	}
	g.want(g.do("admin", "GET", "/api/tickets/attachments/2", ""), 500, `{"error":"Internal server error"}`)
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

func TestFileBytes(t *testing.T) {
	for _, tt := range []struct{ text, want string }{
		{"aGVsbG8=", "hello"}, {"aGVsbG8", "hello"}, {"aGVs bG8=\n", "hello"}, {"aGk=aGk=", "hi"}, {"=", ""}, {"a", ""}, {"ab", "i"},
		{"a-_b", "k\xef\xdb"}, {"a+/b", "k\xef\xdb"}, {"!!!!", ""}, {"YQ==YQ==", "a"}, {"中中中中", "\xfb\xef\xbe"},
	} {
		if got := decodeBase64(tt.text); string(got) != tt.want {
			t.Errorf("decodeBase64(%q) = %q, want %q", tt.text, got, tt.want)
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

// What an upload carries is read leniently, but never past its end and
// never wrongly where it is well formed: base64 with or without padding,
// in either alphabet, with white space in it, decodes to the bytes it
// stands for.
func FuzzDecodeBase64(f *testing.F) {
	for _, seed := range []string{"", "hello", "\x00\xff\xfe", "a", "ab", "abc", "any carnal pleasure."} {
		f.Add([]byte(seed), "junk=!")
	}
	f.Fuzz(func(t *testing.T, data []byte, text string) {
		for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			encoded := encoding.EncodeToString(data)
			if got := decodeBase64(encoded); !bytes.Equal(got, data) {
				t.Errorf("decodeBase64(%q) = %x, want %x", encoded, got, data)
			}
			spaced := strings.Join(strings.Split(encoded, ""), " \n")
			if got := decodeBase64(spaced); !bytes.Equal(got, data) && !strings.HasSuffix(encoded, "=") {
				t.Errorf("decodeBase64 with white space of %q = %x, want %x", encoded, got, data)
			}
		}
		// Any text decodes to at most three bytes per four characters
		if got := decodeBase64(text); len(got) > len(text)*3/4+3 {
			t.Errorf("decodeBase64(%q) has %d bytes", text, len(got))
		}
		if name := safeName(text); strings.ContainsAny(name, "/\\\x00") || len(name) < len([]rune(text)) {
			t.Errorf("safeName(%q) = %q", text, name)
		}
	})
}
