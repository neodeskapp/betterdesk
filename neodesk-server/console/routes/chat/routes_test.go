package chat

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/limits"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
)

const (
	signedOut = `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`
	noAccess  = `{"success":false,"error":"Permission denied: chat.access"}`
	failed    = `{"success":false,"error":"Internal Server Error"}`
	// firstID is the id of the first upload in a world: its random bytes
	// count up from zero
	firstID  = "000102030405060708090a0b0c0d0e0f"
	secondID = "101112131415161718191a1b1c1d1e1f"
)

// counter gives the bytes 0, 1, 2, ... as random bytes.
type counter struct{ next byte }

func (c *counter) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = c.next
		c.next++
	}
	return len(p), nil
}

// world is the group on a router, with its files in a folder of the test
// and a stand-in for the API server.
type world struct {
	t       *testing.T
	dir     string
	h       *handlers
	handler http.Handler
	mu      sync.Mutex
	calls   []string
	// api answers a call to the API server; nil answers 404
	api func(w http.ResponseWriter, r *http.Request)
}

func newWorld(t *testing.T) *world {
	t.Helper()
	return newWorldIn(t, filepath.Join(t.TempDir(), "data", "chat-files"))
}

func newWorldIn(t *testing.T, dir string) *world {
	t.Helper()
	return newWorldWith(t, dir, nil)
}

// testLimits are the console's rate limits with the settings of env.
func testLimits(env map[string]string) *limits.Set {
	return limits.NewSet(limits.Options{ClientIP: httpx.SocketIP, Getenv: func(key string) string { return env[key] }})
}

// newWorldWith is a world in dir with the rate limits env sets.
func newWorldWith(t *testing.T, dir string, env map[string]string) *world {
	t.Helper()
	w := &world{t: t, dir: dir}
	api := goapi.New(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		w.calls = append(w.calls, r.Method+" "+r.RequestURI)
		answer := w.api
		w.mu.Unlock()
		if answer == nil {
			http.NotFound(rw, r)
			return
		}
		answer(rw, r)
	}))
	d := &kit.Deps{Router: kit.NewRouter(), GoAPI: api, Limits: testLimits(env)}
	h := register(d, dir)
	h.random = &counter{}
	h.user = testUser
	w.h = h
	w.handler = middleware.Bodies(middleware.BodyOptions{})(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
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
	return w
}

// testUser is the account of a test request: one of scopeAccounts named
// by X-Test-User, else account 7 with the role X-Test-Role names, else
// nobody.
func testUser(r *http.Request) *session.User {
	if a, ok := scopeAccounts[r.Header.Get("X-Test-User")]; ok {
		return &a
	}
	if role := r.Header.Get("X-Test-Role"); role != "" {
		return &session.User{ID: 7, Username: "tester", Role: role}
	}
	return nil
}

func (w *world) send(role string, r *http.Request) *httptest.ResponseRecorder {
	if role != "" {
		r.Header.Set("X-Test-Role", role)
	}
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, r)
	return rec
}

// get sends a GET or HEAD request with extra headers as name, value pairs.
func (w *world) get(role, method, target string, header ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	return w.send(role, r)
}

// part is one part of a multipart form: a file when it has a file name.
type part struct {
	field, fileName string
	content         []byte
}

// upload posts a multipart form to the upload route.
func (w *world) upload(role string, parts ...part) *httptest.ResponseRecorder {
	w.t.Helper()
	return w.send(role, w.uploadRequest(parts...))
}

// uploadBy posts a multipart form as one of scopeAccounts.
func (w *world) uploadBy(who string, parts ...part) *httptest.ResponseRecorder {
	w.t.Helper()
	r := w.uploadRequest(parts...)
	r.Header.Set("X-Test-User", who)
	return w.send("", r)
}

func (w *world) uploadRequest(parts ...part) *http.Request {
	w.t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for _, p := range parts {
		header := textproto.MIMEHeader{}
		disposition := `form-data; name="` + p.field + `"`
		if p.fileName != "" {
			disposition += `; filename="` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(p.fileName) + `"`
			header.Set("Content-Type", "application/octet-stream")
		}
		header.Set("Content-Disposition", disposition)
		out, err := form.CreatePart(header)
		if err != nil {
			w.t.Fatal(err)
		}
		if _, err := out.Write(p.content); err != nil {
			w.t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		w.t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/chat/upload", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	return r
}

// stored lists the names in the files folder, without the links folder.
func (w *world) stored() []string {
	w.t.Helper()
	entries, err := os.ReadDir(w.dir)
	if err != nil && !os.IsNotExist(err) {
		w.t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		if entry.Name() != linksFolder {
			names = append(names, entry.Name())
		}
	}
	return names
}

// put writes a file into the files folder.
func (w *world) put(name, content string) {
	w.t.Helper()
	if err := os.MkdirAll(w.dir, 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.dir, name), []byte(content), 0o644); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) sent() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	calls := strings.Join(w.calls, "\n")
	w.calls = nil
	return calls
}

func expect(t *testing.T, what string, rec *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	if rec.Code != status || rec.Body.String() != body {
		t.Errorf("%s: %d %s, want %d %s", what, rec.Code, rec.Body.String(), status, body)
	}
}

func TestRoutesInTheOrderOfTheOldFile(t *testing.T) {
	d := &kit.Deps{Router: kit.NewRouter(), GoAPI: goapi.New(http.NotFoundHandler()), Limits: testLimits(nil)}
	register(d, t.TempDir())
	var got []string
	for _, route := range d.Router.Routes() {
		got = append(got, route.Method+" "+route.Path)
	}
	want := "POST /api/chat/upload\nGET /api/chat/files/{fileId}\nGET /api/chat/search"
	if strings.Join(got, "\n") != want {
		t.Errorf("routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
	}
}

// Every route needs a session and chat.access, as the chat websocket
// does. A refused upload is not read and leaves nothing behind.
func TestRoutesNeedChatAccess(t *testing.T) {
	w := newWorld(t)
	w.put(firstID+".txt", "secret")
	file := part{"file", "note.txt", []byte("x")}
	requests := map[string]func(role string) *httptest.ResponseRecorder{
		"upload":   func(role string) *httptest.ResponseRecorder { return w.upload(role, file) },
		"download": func(role string) *httptest.ResponseRecorder { return w.get(role, "GET", "/api/chat/files/"+firstID) },
		"short id": func(role string) *httptest.ResponseRecorder { return w.get(role, "GET", "/api/chat/files/abc123") },
		"search":   func(role string) *httptest.ResponseRecorder { return w.get(role, "GET", "/api/chat/search?q=hi") },
	}
	for name, request := range requests {
		expect(t, name+" signed out", request(""), 401, signedOut)
		expect(t, name+" as pro", request("pro"), 403, `{"success":false,"error":"Pro accounts can only access the RustDesk desktop client API"}`)
		for _, role := range []string{"server_admin", "guest_issuer"} {
			expect(t, name+" as "+role, request(role), 403, noAccess)
		}
	}
	if got := w.stored(); len(got) != 1 {
		t.Errorf("a refused upload left a file: %v", got)
	}
	if got := w.sent(); got != "" {
		t.Errorf("a refused search reached the API server: %s", got)
	}
	for _, role := range []string{"viewer", "operator", "global_admin", "admin", "super_admin"} {
		expect(t, "download as "+role, w.get(role, "GET", "/api/chat/files/"+firstID), 200, "secret")
		expect(t, "search as "+role, w.get(role, "GET", "/api/chat/search?q=hi"), 200, `{"success":true,"messages":[]}`)
	}
}

func TestUpload(t *testing.T) {
	w := newWorld(t)
	if _, err := os.Stat(w.dir); !os.IsNotExist(err) {
		t.Fatalf("the files folder exists before the first upload: %v", err)
	}
	rec := w.upload("viewer", part{"note", "", []byte("a field before the file")}, part{"file", "Meeting Notes.TXT", []byte("contract chat file\n")})
	expect(t, "upload", rec, 200, `{"success":true,"file_id":"`+firstID+`","filename":"`+firstID+`.TXT","size":19,"url":"/api/chat/files/`+firstID+`"}`)
	if got, err := os.ReadFile(filepath.Join(w.dir, firstID+".TXT")); err != nil || string(got) != "contract chat file\n" {
		t.Errorf("the stored file: %q, %v", got, err)
	}

	rec = w.upload("operator", part{"file", "README", nil})
	expect(t, "a name without an extension, no bytes", rec, 200, `{"success":true,"file_id":"`+secondID+`","filename":"`+secondID+`","size":0,"url":"/api/chat/files/`+secondID+`"}`)

	names := []struct{ name, ext string }{
		{"archive.tar.verylongextension", ".verylonge"},
		{".bashrc", ""},
		{"trailing.", "."},
		{`we"ird name.a b`, ".a b"},
		// The name is read as UTF-8, where Node read it as Latin-1 (BUGS M5)
		{"x.\xc3\xa9t\xc3\xa9", ".été"},
	}
	for _, c := range names {
		before := len(w.stored())
		rec := w.upload("admin", part{"file", c.name, []byte("x")})
		if rec.Code != 200 {
			t.Errorf("upload of %q: %d %s", c.name, rec.Code, rec.Body.String())
			continue
		}
		stored := w.stored()
		if len(stored) != before+1 {
			t.Errorf("upload of %q: %d files, want %d", c.name, len(stored), before+1)
		}
		found := false
		for _, name := range stored {
			if len(name) >= 32 && name[32:] == c.ext && strings.Contains(rec.Body.String(), `"filename":`+string(mustJSON(t, name))) {
				found = true
			}
		}
		if !found {
			t.Errorf("upload of %q: no stored file ends in %q and is named in %s; files: %q", c.name, c.ext, rec.Body.String(), stored)
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := httpx.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestUploadRefused(t *testing.T) {
	w := newWorld(t)
	expect(t, "a form without a file", w.upload("admin", part{"note", "", []byte("x")}), 400, `{"success":false,"error":"No file provided"}`)
	expect(t, "a file part without a name", w.upload("admin", part{"file", "", []byte("x")}), 400, `{"success":false,"error":"No file provided"}`)

	r := httptest.NewRequest("POST", "/api/chat/upload", strings.NewReader(`{"file":"x"}`))
	r.Header.Set("Content-Type", "application/json")
	expect(t, "a JSON body", w.send("admin", r), 400, `{"success":false,"error":"No file provided"}`)
	expect(t, "no body", w.send("admin", httptest.NewRequest("POST", "/api/chat/upload", nil)), 400, `{"success":false,"error":"No file provided"}`)

	// What the form parser refuses is answered by the console's error
	// handler, and the file that was being written is removed
	expect(t, "a file in another field", w.upload("admin", part{"other", "a.txt", []byte("x")}), 500, failed)
	expect(t, "a second file", w.upload("admin", part{"file", "a.txt", []byte("x")}, part{"file", "b.txt", []byte("y")}), 500, failed)
	r = httptest.NewRequest("POST", "/api/chat/upload", strings.NewReader("--x\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.txt\"\r\n\r\ncut off"))
	r.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	expect(t, "a body that ends early", w.send("admin", r), 500, failed)
	r = httptest.NewRequest("POST", "/api/chat/upload", strings.NewReader("x"))
	r.Header.Set("Content-Type", "multipart/form-data")
	expect(t, "no boundary", w.send("admin", r), 500, failed)
	if got := w.stored(); len(got) != 0 {
		t.Errorf("refused uploads left files: %v", got)
	}
}

func TestUploadSizeLimit(t *testing.T) {
	w := newWorld(t)
	atLimit := make([]byte, maxFileSize)
	rec := w.upload("admin", part{"file", "big.bin", atLimit})
	expect(t, "50 MB", rec, 200, `{"success":true,"file_id":"`+firstID+`","filename":"`+firstID+`.bin","size":52428800,"url":"/api/chat/files/`+firstID+`"}`)
	if err := os.Remove(filepath.Join(w.dir, firstID+".bin")); err != nil {
		t.Fatal(err)
	}
	expect(t, "50 MB and one byte", w.upload("admin", part{"file", "big.bin", append(atLimit, 0)}), 500, failed)
	if got := w.stored(); len(got) != 0 {
		t.Errorf("the file over the limit was kept: %v", got)
	}
}

func TestUploadWhenTheFolderCannotBeMade(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	w := newWorldIn(t, filepath.Join(blocker, "chat-files"))
	expect(t, "upload", w.upload("admin", part{"file", "a.txt", []byte("x")}), 500, failed)
	expect(t, "download", w.get("admin", "GET", "/api/chat/files/"+firstID), 500, failed)
}

// Uploads count against the upload limit of the address, also those that
// are refused later; one over the limit is not read.
func TestUploadGoesThroughTheUploadLimiter(t *testing.T) {
	w := newWorldWith(t, filepath.Join(t.TempDir(), "chat-files"), map[string]string{"UPLOAD_RATE_LIMIT_MAX": "2"})
	file := part{"file", "a.txt", []byte("x")}
	expect(t, "first", w.upload("viewer", file), 200, `{"success":true,"file_id":"`+firstID+`","filename":"`+firstID+`.txt","size":1,"url":"/api/chat/files/`+firstID+`"}`)
	expect(t, "second, signed out", w.upload("", file), 401, signedOut)
	rec := w.upload("viewer", file)
	expect(t, "third", rec, 429, `{"success":false,"error":"Too many upload requests. Please try again later."}`)
	if rec.Header().Get("RateLimit-Limit") != "2" {
		t.Errorf("third: RateLimit-Limit %q", rec.Header().Get("RateLimit-Limit"))
	}
	if got := w.stored(); len(got) != 1 {
		t.Errorf("stored %q, want the first upload only", got)
	}
	expect(t, "a download", w.get("viewer", "GET", "/api/chat/files/"+firstID), 200, "x")
}

// Uploads stop once the files hold the folder's limit: a file that would
// take them over it is removed again, and with the folder full an upload
// is refused before its body is read.
func TestUploadRefusedWhenTheFolderIsFull(t *testing.T) {
	const full = `{"success":false,"error":"Chat file storage is full"}`
	w := newWorld(t)
	w.h.maxFolder = 10
	w.put("ffffffffffffffffffffffffffffffff.txt", "123456")
	expect(t, "over the limit", w.upload("admin", part{"file", "a.txt", []byte("12345")}), 507, full)
	if got := w.stored(); len(got) != 1 {
		t.Errorf("stored %q, want the file that was there", got)
	}
	expect(t, "up to the limit", w.upload("admin", part{"file", "b.txt", []byte("1234")}), 200,
		`{"success":true,"file_id":"`+secondID+`","filename":"`+secondID+`.txt","size":4,"url":"/api/chat/files/`+secondID+`"}`)
	r := w.uploadRequest(part{"file", "c.txt", nil})
	r.Body = io.NopCloser(readerFunc(func([]byte) (int, error) {
		t.Error("the body of an upload into a full folder was read")
		return 0, io.EOF
	}))
	expect(t, "a full folder", w.send("admin", r), 507, full)
	if got := w.stored(); len(got) != 2 {
		t.Errorf("stored %q, want two files", got)
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func TestDownload(t *testing.T) {
	w := newWorld(t)
	expect(t, "before the folder exists", w.get("viewer", "GET", "/api/chat/files/"+firstID), 404, `{"success":false,"error":"File not found"}`)
	expect(t, "upload", w.upload("viewer", part{"file", "note.txt", []byte("contract chat file\n")}),
		200, `{"success":true,"file_id":"`+firstID+`","filename":"`+firstID+`.txt","size":19,"url":"/api/chat/files/`+firstID+`"}`)

	rec := w.get("operator", "GET", "/api/chat/files/"+firstID)
	expect(t, "download", rec, 200, "contract chat file\n")
	h := rec.Header()
	want := map[string]string{
		"Content-Disposition": `attachment; filename="` + firstID + `.txt"`,
		"Content-Type":        "text/plain; charset=UTF-8",
		"Content-Length":      "19",
		"Accept-Ranges":       "bytes",
		"Cache-Control":       "public, max-age=0",
	}
	for name, value := range want {
		if h.Get(name) != value {
			t.Errorf("download: %s is %q, want %q", name, h.Get(name), value)
		}
	}
	etag := h.Get("ETag")
	if !strings.HasPrefix(etag, `W/"13-`) || h.Get("Last-Modified") == "" {
		t.Errorf("download: ETag %q, Last-Modified %q", etag, h.Get("Last-Modified"))
	}

	expect(t, "the id in capitals", w.get("viewer", "GET", "/api/chat/files/"+strings.ToUpper(firstID)), 200, "contract chat file\n")
	head := w.get("viewer", "HEAD", "/api/chat/files/"+firstID)
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != "19" || head.Header().Get("Content-Disposition") == "" {
		t.Errorf("HEAD: %d %q %v", head.Code, head.Body.String(), head.Header())
	}
	cached := w.get("viewer", "GET", "/api/chat/files/"+firstID, "If-None-Match", etag)
	if cached.Code != 304 || cached.Body.Len() != 0 || cached.Header().Get("Content-Disposition") == "" {
		t.Errorf("If-None-Match: %d %q %v", cached.Code, cached.Body.String(), cached.Header())
	}
	ranged := w.get("viewer", "GET", "/api/chat/files/"+firstID, "Range", "bytes=9-12")
	if ranged.Code != 206 || ranged.Body.String() != "chat" || ranged.Header().Get("Content-Range") != "bytes 9-12/19" {
		t.Errorf("Range: %d %q %v", ranged.Code, ranged.Body.String(), ranged.Header())
	}
	// What the file sender refuses goes to the console's error handler,
	// which says why also in production (S17)
	beyond := w.get("viewer", "GET", "/api/chat/files/"+firstID, "Range", "bytes=100-200")
	expect(t, "a range beyond the file", beyond, 416, `{"success":false,"error":"Range Not Satisfiable"}`)
	if beyond.Header().Get("Content-Range") != "bytes */19" {
		t.Errorf("a range beyond the file: Content-Range %q", beyond.Header().Get("Content-Range"))
	}
	expect(t, "If-Match that does not hold", w.get("viewer", "GET", "/api/chat/files/"+firstID, "If-Match", `"other"`), 412, `{"success":false,"error":"Precondition Failed"}`)
}

// Only a whole id finds its file. A prefix used to be enough, which let
// anyone with chat access walk the uploads one character at a time.
func TestDownloadNeedsTheWholeID(t *testing.T) {
	w := newWorld(t)
	w.put(firstID+".txt", "secret")
	w.put(secondID, "bare")
	w.put("2"+firstID[1:]+"x.txt", "id followed by a letter")
	w.put("3"+firstID[1:]+".tar.gz", "two dots")
	w.put("note.txt", "no id at all")

	const invalid = `{"success":false,"error":"Invalid file ID"}`
	const missing = `{"success":false,"error":"File not found"}`
	expect(t, "the whole id", w.get("viewer", "GET", "/api/chat/files/"+firstID), 200, "secret")
	for _, n := range []int{1, 4, 31} {
		expect(t, "a prefix", w.get("viewer", "GET", "/api/chat/files/"+firstID[:n]), 400, invalid)
	}
	expect(t, "a longer id", w.get("viewer", "GET", "/api/chat/files/"+firstID+"00"), 400, invalid)
	expect(t, "the stored name", w.get("viewer", "GET", "/api/chat/files/"+firstID+".txt"), 400, invalid)
	expect(t, "not hex", w.get("viewer", "GET", "/api/chat/files/"+strings.Repeat("g", 32)), 400, invalid)
	expect(t, "a name in the folder", w.get("viewer", "GET", "/api/chat/files/note.txt"), 400, invalid)
	expect(t, "a way out of the folder", w.get("viewer", "GET", "/api/chat/files/..%2F..%2Fetc%2Fpasswd"), 400, invalid)
	expect(t, "an unknown id", w.get("viewer", "GET", "/api/chat/files/ffffffffffffffffffffffffffffffff"), 404, missing)
	expect(t, "an id followed by a letter", w.get("viewer", "GET", "/api/chat/files/2"+firstID[1:]), 404, missing)

	bare := w.get("viewer", "GET", "/api/chat/files/"+secondID)
	expect(t, "a file without an extension", bare, 200, "bare")
	if bare.Header().Get("Content-Type") != "application/octet-stream" || bare.Header().Get("Content-Disposition") != `attachment; filename="`+secondID+`"` {
		t.Errorf("a file without an extension: %v", bare.Header())
	}
	dotted := w.get("viewer", "GET", "/api/chat/files/3"+firstID[1:])
	expect(t, "a file with two dots", dotted, 200, "two dots")
	if dotted.Header().Get("Content-Type") != "application/gzip" {
		t.Errorf("a file with two dots: %v", dotted.Header())
	}
}

// A download the file sender refuses after it found the file (a range
// beyond the end, an If-Match that does not hold) answers JSON, without
// the file's type and name.
func TestRefusedDownloadAnswersWithoutTheFileHeaders(t *testing.T) {
	w := newWorld(t)
	w.put(firstID+".txt", "contract chat file\n")
	for what, header := range map[string][]string{
		"a range beyond the file":        {"Range", "bytes=100-200"},
		"an If-Match that does not hold": {"If-Match", `"other"`},
	} {
		rec := w.get("viewer", "GET", "/api/chat/files/"+firstID, header...)
		if rec.Code != 416 && rec.Code != 412 {
			t.Errorf("%s: %d", what, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
			t.Errorf("%s: Content-Type %q", what, got)
		}
		if got := rec.Header().Get("Content-Disposition"); got != "" {
			t.Errorf("%s: Content-Disposition %q", what, got)
		}
	}
}

// A console installed below a folder whose name starts with a dot sends
// its chat files: only the name below the chat folder is looked at.
func TestDownloadFromAHiddenFolder(t *testing.T) {
	w := newWorldIn(t, filepath.Join(t.TempDir(), ".console", "data", "chat-files"))
	w.put(secondID+".txt", "secret")
	expect(t, "download", w.get("viewer", "GET", "/api/chat/files/"+secondID), 200, "secret")
	expect(t, "an unknown id", w.get("viewer", "GET", "/api/chat/files/ffffffffffffffffffffffffffffffff"), 404, `{"success":false,"error":"File not found"}`)
	expect(t, "upload", w.upload("viewer", part{"file", "a.txt", []byte("x")}), 200,
		`{"success":true,"file_id":"`+firstID+`","filename":"`+firstID+`.txt","size":1,"url":"/api/chat/files/`+firstID+`"}`)
	expect(t, "download of the upload", w.get("viewer", "GET", "/api/chat/files/"+firstID), 200, "x")
}

func TestSearch(t *testing.T) {
	w := newWorld(t)
	const nothing = `{"success":true,"messages":[]}`

	for _, target := range []string{"/api/chat/search", "/api/chat/search?q=", "/api/chat/search?conversation_id=c1"} {
		expect(t, target, w.get("viewer", "GET", target), 200, nothing)
		if got := w.sent(); got != "" {
			t.Errorf("%s reached the API server: %s", target, got)
		}
	}

	// The API server has no route at the path the call goes to
	expect(t, "a search", w.get("viewer", "GET", "/api/chat/search?q=hello+world&conversation_id=c1"), 200, nothing)
	if got, want := w.sent(), "GET /api/api/chat/search?q=hello+world&conversation_id=c1&limit=50"; got != want {
		t.Errorf("the API server got %q, want %q", got, want)
	}
	expect(t, "without a conversation", w.get("viewer", "GET", "/api/chat/search?q=hi"), 200, nothing)
	if got, want := w.sent(), "GET /api/api/chat/search?q=hi&conversation_id=&limit=50"; got != want {
		t.Errorf("the API server got %q, want %q", got, want)
	}
	long := strings.Repeat("a", 198)
	expect(t, "a long text given twice", w.get("viewer", "GET", "/api/chat/search?q="+long+"&q=more&conversation_id=a&conversation_id=b"), 200, nothing)
	if got, want := w.sent(), "GET /api/api/chat/search?q="+long+"%2Cm&conversation_id%5B%5D=a&conversation_id%5B%5D=b&limit=50"; got != want {
		t.Errorf("the API server got %q, want %q", got, want)
	}
	// A text cut in the middle of a character cannot be sent on
	expect(t, "a cut through a character", w.get("viewer", "GET", "/api/chat/search?q="+strings.Repeat("a", 199)+"%F0%9F%98%80tail"), 200, nothing)
	if got := w.sent(); got != "" {
		t.Errorf("a text cut through a character reached the API server: %s", got)
	}
	expect(t, "a cut behind such a character", w.get("viewer", "GET", "/api/chat/search?q="+strings.Repeat("a", 198)+"%F0%9F%98%80tail"), 200, nothing)
	if got, want := w.sent(), "GET /api/api/chat/search?q="+long+"%F0%9F%98%80&conversation_id=&limit=50"; got != want {
		t.Errorf("the API server got %q, want %q", got, want)
	}

	// Were the route there, its messages would be passed on
	answers := []struct {
		status     int
		body, want string
	}{
		{200, `{"messages":[{"id":1,"text":"hello"},{"id":2,"text":"hello again"}]}`, `{"success":true,"messages":[{"id":1,"text":"hello"},{"id":2,"text":"hello again"}]}`},
		{200, `{"messages":null}`, nothing},
		{200, `{}`, nothing},
		{200, `[]`, nothing},
		{200, `not json`, nothing},
		{500, `{"messages":[{"id":1}]}`, nothing},
		{403, `{"error":"forbidden"}`, nothing},
	}
	for _, a := range answers {
		w.mu.Lock()
		w.api = func(rw http.ResponseWriter, r *http.Request) {
			rw.Header().Set("Content-Type", "application/json")
			rw.WriteHeader(a.status)
			_, _ = io.WriteString(rw, a.body)
		}
		w.mu.Unlock()
		expect(t, "the API server answers "+a.body, w.get("viewer", "GET", "/api/chat/search?q=hello"), 200, a.want)
	}
}
