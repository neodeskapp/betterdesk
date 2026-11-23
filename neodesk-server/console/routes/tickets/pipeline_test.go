package tickets_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// upload is the body of an attachment upload that is made while it is
// read: {"filename":"big.bin","data":"AAAA..."} with size characters of
// data. It counts what was read of it.
type upload struct {
	src  io.Reader
	size int64
	read int64
}

func newUpload(key string, size int64) *upload {
	head, tail := `{"filename":"big.bin","`+key+`":"`, `"}`
	return &upload{
		src:  io.MultiReader(strings.NewReader(head), io.LimitReader(repeat('A'), size), strings.NewReader(tail)),
		size: int64(len(head)+len(tail)) + size,
	}
}

func (u *upload) Read(p []byte) (int, error) {
	n, err := u.src.Read(p)
	u.read += int64(n)
	return n, err
}

type repeat byte

func (c repeat) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(c)
	}
	return len(p), nil
}

const megabyte = 1 << 20

// send posts an upload body to a path as the role.
func send(w *replay.World, role, method, path string, u *upload, chunked bool) replay.Answer {
	length := u.size
	if chunked {
		length = -1
	}
	return w.Do(replay.Request{Role: role, Method: method, Path: path, Reader: u, ContentLength: length})
}

// The body of an upload is read by the attachment route, after its
// checks. Someone who may not attach anything is refused before a byte
// of the body is read, however large it is and however it is sent.
func TestAnUploadIsRefusedBeforeItsBodyIsRead(t *testing.T) {
	w := replay.NewWorld(t, storetest.Migrated(t), nil)
	if got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/tickets", Body: replay.Text(`{"title":"uploads"}`)}); got.Status != 201 {
		t.Fatalf("create a ticket: %d %s", got.Status, got.Body)
	}
	for _, tt := range []struct {
		role   string
		status int
		body   string
	}{
		{"", 401, `{"error":"Authentication required"}`},
		{"viewer", 403, `{"success":false,"error":"Panel access denied: tickets","code":"panel_access_denied"}`},
		{"guest_issuer", 403, `{"success":false,"error":"Panel access denied: tickets","code":"panel_access_denied"}`},
		{"server_admin", 403, `{"success":false,"error":"Panel access denied: tickets","code":"panel_access_denied"}`},
	} {
		for _, chunked := range []bool{false, true} {
			for _, size := range []int64{megabyte / 2, 3 * megabyte, 30 * megabyte, 40 * megabyte} {
				u := newUpload("data", size)
				got := send(w, tt.role, "POST", "/api/tickets/1/attachments", u, chunked)
				if got.Status != tt.status || got.Body != tt.body {
					t.Errorf("%q uploads %d MB (chunked %v): %d %s, want %d %s", tt.role, size/megabyte, chunked, got.Status, got.Body, tt.status, tt.body)
				}
				if u.read != 0 {
					t.Errorf("%q uploads %d MB (chunked %v): %d bytes of the body were read before the refusal", tt.role, size/megabyte, chunked, u.read)
				}
			}
		}
	}
	// The same body on the route next to it is read by the console's own
	// parser, which comes before every check
	u := newUpload("body", megabyte/2)
	if got := send(w, "", "POST", "/api/tickets/1/comments", u, false); got.Status != 401 || u.read != u.size {
		t.Errorf("a comment by nobody: %d %s, %d of %d bytes read", got.Status, got.Body, u.read, u.size)
	}
	if files, _ := os.ReadDir(filepath.Join(w.Config.ConsoleDir, "data", "attachments")); len(files) != 0 {
		t.Errorf("%d files were stored", len(files))
	}
}

// 35 MB of JSON are read on the attachment route and nowhere else
// (tests/jsonBody.test.js and the upload tests of
// tests/tickets.permissions.test.js).
func TestTheLargeBodyLimitIsTheAttachmentRoutesAlone(t *testing.T) {
	w := replay.NewWorld(t, storetest.Migrated(t), nil)
	if got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/tickets", Body: replay.Text(`{"title":"uploads"}`)}); got.Status != 201 {
		t.Fatalf("create a ticket: %d %s", got.Status, got.Body)
	}
	// The parser's message is the answer, also in production (S17)
	const tooLarge = `{"success":false,"error":"request entity too large"}`
	for _, tt := range []struct {
		name         string
		role, method string
		path         string
		key          string
		size         int64
		chunked      bool
		status       int
		body         string
	}{
		{"a small upload", "operator", "POST", "/api/tickets/1/attachments", "data", 8, false, 201, `{"success":true,"attachment":{"id":1,"filename":"big.bin","size_bytes":6}}`},
		{"4 MB, more than any other route takes", "operator", "POST", "/api/tickets/1/attachments", "data", 4 * megabyte, false, 201, `{"success":true,"attachment":{"id":2,"filename":"big.bin","size_bytes":3145728}}`},
		{"4 MB without a length", "admin", "POST", "/api/tickets/1/attachments", "data", 4 * megabyte, true, 201, `{"success":true,"attachment":{"id":3,"filename":"big.bin","size_bytes":3145728}}`},
		{"a file of 25 MB less a byte, as base64 with its padding", "admin", "POST", "/api/tickets/1/attachments", "data", 25*megabyte*4/3 - 1, false, 201, `{"success":true,"attachment":{"id":4,"filename":"big.bin","size_bytes":26214399}}`},
		{"a file over 25 MB in a body under 35 MB", "admin", "POST", "/api/tickets/1/attachments", "data", 34 * megabyte, false, 413, `{"error":"Attachment too large. Max 25 MB"}`},
		{"a body just under 35 MB", "admin", "POST", "/api/tickets/1/attachments", "data", 35*megabyte - 64, false, 413, `{"error":"Attachment too large. Max 25 MB"}`},
		{"a body just over 35 MB", "admin", "POST", "/api/tickets/1/attachments", "data", 35 * megabyte, false, 413, tooLarge},
		{"a body over 35 MB without a length", "admin", "POST", "/api/tickets/1/attachments", "data", 36 * megabyte, true, 413, tooLarge},
		{"4 MB on the comment route", "admin", "POST", "/api/tickets/1/comments", "body", 4 * megabyte, false, 413, tooLarge},
		{"4 MB on the comment route by nobody", "", "POST", "/api/tickets/1/comments", "body", 4 * megabyte, false, 413, tooLarge},
		{"4 MB on the ticket itself", "admin", "PATCH", "/api/tickets/1", "description", 4 * megabyte, false, 413, tooLarge},
		{"4 MB to the attachment path with another method", "admin", "PUT", "/api/tickets/1/attachments", "data", 4 * megabyte, false, 413, tooLarge},
		{"4 MB to a path below the attachment path", "admin", "POST", "/api/tickets/1/attachments/more", "data", 4 * megabyte, false, 413, tooLarge},
		{"2 MB on the comment route", "admin", "POST", "/api/tickets/1/comments", "body", 2*megabyte - 64, false, 201, `{"success":true,"comment":{"id":1}}`},
	} {
		got := send(w, tt.role, tt.method, tt.path, newUpload(tt.key, tt.size), tt.chunked)
		if got.Status != tt.status || got.Body != tt.body {
			t.Errorf("%s: %s %s as %q: %d %s, want %d %s", tt.name, tt.method, tt.path, tt.role, got.Status, got.Body, tt.status, tt.body)
		}
	}
}

// The upload limiter counts creating and deleting tickets, comments and
// uploads, and the file limiter the attachment list and downloads. Both
// count before anything else of the route runs, so also the requests of
// nobody.
func TestRateLimiters(t *testing.T) {
	w := replay.NewWorld(t, storetest.Migrated(t), func(cfg *config.Config) {
		getenv := cfg.Getenv
		cfg.Getenv = func(key string) string {
			switch key {
			case "UPLOAD_RATE_LIMIT_MAX":
				return "4"
			case "FILE_ACCESS_RATE_LIMIT_MAX":
				return "2"
			}
			return getenv(key)
		}
	})
	do := func(method, path, body string) replay.Answer {
		req := replay.Request{Method: method, Path: path}
		if body != "" {
			req.Body = replay.Text(body)
		}
		return w.Do(req)
	}
	// Routes without a limiter of their own never run out
	for i := 0; i < 8; i++ {
		for _, got := range []replay.Answer{do("GET", "/api/tickets", ""), do("GET", "/api/tickets/stats", ""), do("GET", "/api/tickets/1", ""),
			do("PATCH", "/api/tickets/1", `{}`), do("GET", "/api/tickets/1/comments", "")} {
			if got.Status != 401 {
				t.Fatalf("a route without a limiter answered %d %s", got.Status, got.Body)
			}
		}
	}
	uploads := []replay.Answer{do("POST", "/api/tickets", `{}`), do("DELETE", "/api/tickets/1", ""), do("POST", "/api/tickets/1/comments", `{}`),
		do("POST", "/api/tickets/1/attachments", `{}`)}
	for i, got := range uploads {
		if got.Status != 401 || got.Header.Get("RateLimit-Limit") != "4" {
			t.Errorf("upload route %d within the limit: %d %s, RateLimit-Limit %q", i, got.Status, got.Body, got.Header.Get("RateLimit-Limit"))
		}
	}
	const busy = `{"success":false,"error":"Too many upload requests. Please try again later."}`
	for _, got := range []replay.Answer{do("POST", "/api/tickets", `{}`), do("DELETE", "/api/tickets/1", ""), do("POST", "/api/tickets/1/comments", `{}`),
		do("POST", "/api/tickets/1/attachments", `{}`)} {
		if got.Status != 429 || got.Body != busy {
			t.Errorf("an upload route over the limit: %d %s", got.Status, got.Body)
		}
	}
	for i, got := range []replay.Answer{do("GET", "/api/tickets/1/attachments", ""), do("GET", "/api/tickets/attachments/1", "")} {
		if got.Status != 401 || got.Header.Get("RateLimit-Limit") != "2" {
			t.Errorf("file route %d within the limit: %d %s", i, got.Status, got.Body)
		}
	}
	for _, got := range []replay.Answer{do("GET", "/api/tickets/1/attachments", ""), do("GET", "/api/tickets/attachments/1", "")} {
		if got.Status != 429 || got.Body != `{"success":false,"error":"Too many file requests. Please try again later."}` {
			t.Errorf("a file route over the limit: %d %s", got.Status, got.Body)
		}
	}
}

// An install below a folder whose name starts with a dot serves its
// attachments: only a dot in a name below the attachment folder hides a
// file (BUGS TK10). Such a file is refused with the console's JSON error,
// without the attachment's name (BUGS TK9), as not found (S17).
func TestADownloadBelowADotFolder(t *testing.T) {
	w := replay.NewWorld(t, storetest.Migrated(t), func(cfg *config.Config) {
		cfg.ConsoleDir = filepath.Join(cfg.DataDir, ".hidden", "console")
	})
	if got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/tickets", Body: replay.Text(`{"title":"dots"}`)}); got.Status != 201 {
		t.Fatalf("create: %d %s", got.Status, got.Body)
	}
	if got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/tickets/1/attachments", Body: replay.Text(`{"filename":"a.txt","data":"aGk=","mimetype":"Text/Plain; Format=flowed"}`)}); got.Status != 201 {
		t.Fatalf("attach: %d %s", got.Status, got.Body)
	}
	if got := w.Do(replay.Request{Role: "admin", Path: "/api/tickets/attachments/1"}); got.Status != 200 || got.Body != "hi" {
		t.Errorf("download: %d %s, want the file", got.Status, got.Body)
	}

	if err := os.WriteFile(filepath.Join(uploads(w), ".hidden-file"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.Exec("INSERT INTO ticket_attachments (ticket_id, filename, storage_path, uploaded_by) VALUES (1, 'h.txt', '.hidden-file', 'admin')", nil)
	got := w.Do(replay.Request{Role: "admin", Path: "/api/tickets/attachments/2"})
	if got.Status != 404 || got.Body != `{"success":false,"error":"Not Found"}` {
		t.Errorf("download of a dot file: %d %s", got.Status, got.Body)
	}
	if ct := got.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type of the refusal = %q", ct)
	}
	if cd := got.Header.Get("Content-Disposition"); cd != "" {
		t.Errorf("Content-Disposition of the refusal = %q", cd)
	}
}

// A row holds the name of its file in the attachment folder, and a row
// from before, which holds the absolute path of another install, finds
// its file by the name in the folder where it is now (BUGS TK10).
func TestADownloadAfterTheInstallMoved(t *testing.T) {
	w := replay.NewWorld(t, storetest.Migrated(t), nil)
	if got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/tickets", Body: replay.Text(`{"title":"moved"}`)}); got.Status != 201 {
		t.Fatalf("create: %d %s", got.Status, got.Body)
	}
	if got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/tickets/1/attachments", Body: replay.Text(`{"filename":"a.txt","data":"aGk="}`)}); got.Status != 201 {
		t.Fatalf("attach: %d %s", got.Status, got.Body)
	}
	var name string
	if err := w.DB.QueryRow(t.Context(), "SELECT storage_path FROM ticket_attachments WHERE id = 1").Scan(&name); err != nil || !randomPart.MatchString(name) || strings.ContainsAny(name, `/\`) {
		t.Fatalf("storage_path = %q, %v; want the name of the file alone", name, err)
	}
	for _, old := range []string{"/opt/neodesk/web-nodejs/data/attachments/" + name, `C:\NeoDesk\web-nodejs\data\attachments\` + name} {
		w.Exec("UPDATE ticket_attachments SET storage_path = ? WHERE id = 1", []any{old})
		if got := w.Do(replay.Request{Role: "admin", Path: "/api/tickets/attachments/1"}); got.Status != 200 || got.Body != "hi" {
			t.Errorf("download of the row %s: %d %s, want the file", old, got.Status, got.Body)
		}
	}
	if got := w.Do(replay.Request{Role: "admin", Method: "DELETE", Path: "/api/tickets/1"}); got.Status != 200 {
		t.Fatalf("delete: %d %s", got.Status, got.Body)
	}
	if _, err := os.Stat(filepath.Join(uploads(w), name)); !os.IsNotExist(err) {
		t.Errorf("the file of the deleted ticket is still there: %v", err)
	}
}
