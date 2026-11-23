package settingsbackup_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/backup"
	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

type world struct {
	*routetest.World
	keys string
	env  string
}

func newWorld(t *testing.T, db *store.DB, env map[string]string) *world {
	t.Helper()
	keys := filepath.Join(t.TempDir(), "keys")
	app := filepath.Join(t.TempDir(), "console")
	for _, dir := range []string{keys, app} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	envFile := filepath.Join(app, ".env")
	w := routetest.New(t, routetest.Options{DB: db, Env: env, Config: func(cfg *config.Config) {
		cfg.ConsoleDir = app
		cfg.EnvPath = envFile
		cfg.KeysPath = keys
		cfg.PubKeyPath = filepath.Join(keys, "id_ed25519.pub")
		cfg.APIKeyPath = filepath.Join(keys, ".api_key")
		cfg.DBPath = filepath.Join(keys, "db_v2.sqlite3")
	}})
	for name, content := range map[string]string{filepath.Join(keys, "id_ed25519"): "private", filepath.Join(keys, ".api_key"): "key", envFile: "A=1\n"} {
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return &world{World: w, keys: keys, env: envFile}
}

// shown is an answer's body for a failure message, unless it is a backup.
func shown(res routetest.Answer) string {
	if !strings.Contains(res.Header.Get("Content-Type"), "json") || strings.Contains(res.Header.Get("Content-Disposition"), "attachment") {
		return fmt.Sprintf("(%d bytes of %s)", len(res.Body), res.Header.Get("Content-Type"))
	}
	return res.Body
}

var routes = []struct{ method, path, body string }{
	{"GET", "/api/settings/backup/stats", ""},
	{"GET", "/api/settings/backup", ""},
	{"GET", "/api/settings/backup/schedule", ""},
	{"GET", "/api/settings/backup/schedule/status", ""},
	{"PUT", "/api/settings/backup/schedule", "{}"},
	{"POST", "/api/settings/backup/schedule/run", "{}"},
	{"POST", "/api/settings/backup/destination/test", "{}"},
	{"POST", "/api/settings/restore", "{}"},
	{"GET", "/api/settings/backup/retention", ""},
	{"PUT", "/api/settings/backup/retention", `{"count":1}`},
}

// Every route needs a session and server.config; a refused request changes
// nothing. The operator is given read access to the settings section by a
// policy row, which since upstream 3.5.264 cannot reach above what the
// operator's role allows there: nothing, as the section's permissions are
// server.config.
func TestRoutesNeedServerConfig(t *testing.T) {
	w := newWorld(t, nil, nil)
	if _, err := w.DB.Exec(context.Background(), `INSERT INTO panel_access_policies (subject_type, subject_id, resource_key, access_level, updated_at)
		VALUES ('user', '5', 'settings', 'read', '2019-01-01 00:00:00')`); err != nil {
		t.Fatal(err)
	}
	for _, r := range routes {
		if res := w.SignedOut().Do(r.method, r.path, r.body); res.Status != http.StatusUnauthorized {
			t.Errorf("%s %s signed out: %d %s", r.method, r.path, res.Status, shown(res))
		}
		// A policy row cannot give a role more than its permissions (audit
		// H7): the operator, without server.config, has no access to the
		// settings section whatever the row says, so the panel access guard
		// refuses every method before the route's own check.
		want := `{"success":false,"error":"Panel access denied: settings","code":"panel_access_denied"}`
		if res := w.As("operator").Do(r.method, r.path, r.body); res.Status != http.StatusForbidden || res.Body != want {
			t.Errorf("%s %s as operator: %d %s", r.method, r.path, res.Status, shown(res))
		}
	}
	if rows := w.AuditRows(); len(rows) != 0 {
		t.Errorf("audit rows %q", rows)
	}
	if _, err := os.Stat(filepath.Join(w.DataDir, "scheduled-backups")); err == nil {
		t.Error("a refused run wrote a backup")
	}
	for _, role := range []string{"server_admin", "super_admin", "admin"} {
		if res := w.As(role).Do("GET", "/api/settings/backup/schedule", ""); res.Status != http.StatusOK {
			t.Errorf("%s: %d", role, res.Status)
		}
	}
}

func TestDownloads(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db, nil)
		admin := w.As("admin")
		res := admin.Do("GET", "/api/settings/backup", "")
		if res.Status != 200 || res.Header.Get("Content-Type") != "application/gzip" {
			t.Fatalf("%d %q", res.Status, res.Header.Get("Content-Type"))
		}
		if !regexp.MustCompile(`^attachment; filename="neodesk-backup-\d{4}-\d\d-\d\d-\d\d-\d\d-\d\d\.tar\.gz"$`).MatchString(res.Header.Get("Content-Disposition")) {
			t.Errorf("disposition %q", res.Header.Get("Content-Disposition"))
		}
		if res.Header.Get("ETag") == "" || res.Header.Get("Content-Encoding") != "" {
			t.Errorf("headers %v", res.Header)
		}
		files, err := backup.ExtractTarGz([]byte(res.Body), backup.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		names := strings.Join(files.Names(), ",")
		if !strings.HasPrefix(names, "manifest.json,console/data.json,") || !strings.Contains(names, "console/.env,") ||
			!strings.Contains(names, "goserver/id_ed25519,") || !strings.HasSuffix(names, ",README.txt") {
			t.Errorf("entries %s", names)
		}
		mb := regexp.MustCompile(`^backup_created\|Full backup downloaded \(\d+\.\d\d MB\)\|127\.0\.0\.1\|1$`)
		if rows := w.AuditRows(); len(rows) != 1 || !mb.MatchString(rows[0]) {
			t.Errorf("audit %q", rows)
		}

		// sent as a file: never compressed
		res = admin.Do("GET", "/api/settings/backup?format=json", "", "Accept-Encoding", "gzip, br")
		if res.Status != 200 || res.Header.Get("Content-Type") != "application/json; charset=utf-8" || res.Header.Get("Content-Encoding") != "" {
			t.Fatalf("%d %q %s", res.Status, res.Header.Get("Content-Type"), shown(res))
		}
		if !regexp.MustCompile(`^attachment; filename="neodesk-config-\d{4}-\d\d-\d\d\.json"$`).MatchString(res.Header.Get("Content-Disposition")) {
			t.Errorf("disposition %q", res.Header.Get("Content-Disposition"))
		}
		if !strings.HasPrefix(res.Body, "{\n  \"_format\": \"neodesk-backup\",\n  \"_version\": 1,\n  \"_created\": \"") ||
			!strings.Contains(res.Body, "\n  \"_backend\": \"neodesk\",\n  \"console\": {\n    \"settings\": ") {
			t.Errorf("snapshot does not start the way Node's does (%d bytes)", len(res.Body))
		}
		kb := regexp.MustCompile(`^backup_created\|Config snapshot downloaded \(\d+\.\d KB\)\|127\.0\.0\.1\|1$`)
		if rows := w.AuditRows(); len(rows) != 2 || !kb.MatchString(rows[1]) {
			t.Errorf("audit %q", rows)
		}
		// format is compared as it came: anything but the one text "json"
		// is the archive
		for _, q := range []string{"?format=JSON", "?format=json&format=json", "?format[]=json"} {
			if res := admin.Do("GET", "/api/settings/backup"+q, ""); res.Header.Get("Content-Type") != "application/gzip" {
				t.Errorf("%s: %q", q, res.Header.Get("Content-Type"))
			}
		}
	})
}

func multipartBody(t *testing.T, field, name string, content []byte, fields map[string]string) (string, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	if name != "" {
		fw, err := mw.CreateFormFile(field, name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write(content)
	}
	_ = mw.Close()
	return buf.String(), mw.FormDataContentType()
}

func TestRestore(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db, nil)
		admin := w.As("admin")
		post := func(field, name string, content []byte, fields map[string]string) routetest.Answer {
			body, ct := multipartBody(t, field, name, content, fields)
			return admin.Do("POST", "/api/settings/restore", body, "Content-Type", ct)
		}
		ctx := context.Background()
		if _, err := db.Exec(ctx, `INSERT INTO folders (name) VALUES ('in the backup')`); err != nil {
			t.Fatal(err)
		}
		archive := []byte(admin.Do("GET", "/api/settings/backup", "").Body)
		if _, err := db.Exec(ctx, `UPDATE folders SET name = 'changed'`); err != nil {
			t.Fatal(err)
		}

		for _, c := range []struct {
			name   string
			res    routetest.Answer
			status int
			body   string
		}{
			{"no file", admin.Do("POST", "/api/settings/restore", "{}"), 400, ""},
			{"not json", post("backup", "b.json", []byte("nope"), nil), 400, ""},
			{"not a backup", post("backup", "b.json", []byte(`{"_version":3}`), nil), 400,
				`{"success":false,"error":"Invalid backup file: missing or wrong _format field; Unsupported backup version: 3 (max supported: 1); Invalid backup file: missing console section"}`},
			{"broken archive", post("backup", "b.tar.gz", archive[:len(archive)/2], nil), 400,
				`{"success":false,"error":"Invalid backup archive: Not a valid gzip/tar archive: unexpected end of file"}`},
			{"other field", post("file", "b.json", []byte("{}"), nil), 500, ""},
			// an archive's name on a file that is no gzip file: it was read
			// as a settings file and refused as "not valid JSON" (BUGS L408)
			{"named an archive, not gzip", post("backup", "b.tar.gz", []byte(`{"_format":"neodesk-backup"}`), nil), 400,
				`{"success":false,"error":"settings.backup_tab.not_archive"}`},
			{"snapshot, nothing chosen", post("backup", "b.json", []byte(`{"_format":"neodesk-backup","_version":1,"_created":null,"console":{}}`),
				map[string]string{"restoreSettings": "false", "restoreBranding": "false", "restoreFolders": "false", "restoreGroups": "false", "restoreAddressBooks": "false"}), 200,
				`{"success":true,"data":{"type":"config","restored":[],"skipped":["settings","branding","users","folders","groups","addressBooks"],"warnings":[],"backupDate":null}}`},
		} {
			if c.res.Status != c.status || (c.body != "" && c.res.Body != c.body) {
				t.Errorf("%s: %d %s", c.name, c.res.Status, c.res.Body)
			}
		}
		var name string
		if err := db.QueryRow(ctx, `SELECT name FROM folders`).Scan(&name); err != nil || name != "changed" {
			t.Fatalf("refused restores changed the folder: %q %v", name, err)
		}

		res := post("backup", "b.tar.gz", archive, map[string]string{"restoreUploads": "no"})
		var out struct {
			Success bool `json:"success"`
			Data    struct {
				Type            string   `json:"type"`
				Restored        []string `json:"restored"`
				Skipped         []string `json:"skipped"`
				Warnings        []string `json:"warnings"`
				RequiresRestart bool     `json:"requiresRestart"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(res.Body), &out); err != nil || res.Status != 200 || out.Data.Type != "full" ||
			len(out.Data.Restored) != 1 || !strings.HasPrefix(out.Data.Restored[0], "database (") || len(out.Data.Warnings) != 0 ||
			strings.Join(out.Data.Skipped, ",") != "uploads,secrets/keys,.env,goserver-db" || out.Data.RequiresRestart {
			t.Fatalf("restore: %d %s", res.Status, res.Body)
		}
		if !strings.Contains(res.Body, `"data":{"type":"full","restored":[`) {
			t.Errorf("key order: %s", res.Body)
		}
		if err := db.QueryRow(ctx, `SELECT name FROM folders`).Scan(&name); err != nil || name != "in the backup" {
			t.Errorf("folder after the restore: %q %v", name, err)
		}
		// the audit log came back with the database, then got the restore
		rows := w.AuditRows()
		last := rows[len(rows)-1]
		if !strings.HasPrefix(last, "backup_restored|Full restore | Restored: database (") || !strings.HasSuffix(last, " | Skipped: uploads, secrets/keys, .env, goserver-db|127.0.0.1|1") {
			t.Errorf("audit %q", rows)
		}
		// the session of the account that restored is still there
		if res := admin.Do("GET", "/api/settings/backup/retention", ""); res.Status != 200 {
			t.Errorf("after the restore: %d", res.Status)
		}
	})
}

// BK13: when only the audit entry after a restore fails, the restore is
// done: the answer is its result, not a server error. A download still
// needs its audit entry, since the archive holds the server's secrets.
func TestRestoreAnswersWhenTheAuditFails(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db, nil)
		admin := w.As("admin")
		ctx := context.Background()
		archive := []byte(admin.Do("GET", "/api/settings/backup", "").Body)
		if _, err := db.Exec(ctx, `INSERT INTO folders (name) VALUES ('after the backup')`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `ALTER TABLE audit_log RENAME TO audit_log_away`); err != nil {
			t.Fatal(err)
		}
		post := func(name string, content []byte) routetest.Answer {
			body, ct := multipartBody(t, "backup", name, content, nil)
			return admin.Do("POST", "/api/settings/restore", body, "Content-Type", ct)
		}
		res := post("b.tar.gz", archive)
		if res.Status != 200 || !strings.HasPrefix(res.Body, `{"success":true,"data":{"type":"full","restored":["database (`) {
			t.Errorf("full restore: %d %s", res.Status, res.Body)
		}
		var folders int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM folders WHERE name = 'after the backup'`).Scan(&folders); err != nil || folders != 0 {
			t.Errorf("the restore did not run: %d %v", folders, err)
		}
		res = post("b.json", []byte(`{"_format":"neodesk-backup","_version":1,"_created":"2026-11-10T08:00:00.000Z","console":{"folders":[{"name":"from the snapshot"}]}}`))
		if res.Status != 200 || res.Body != `{"success":true,"data":{"type":"config","restored":["folders","groups"],"skipped":["settings","branding","users","addressBooks"],"warnings":[],"backupDate":"2026-11-10T08:00:00.000Z"}}` {
			t.Errorf("snapshot restore: %d %s", res.Status, res.Body)
		}
		for _, q := range []string{"", "?format=json"} {
			if res := admin.Do("GET", "/api/settings/backup"+q, ""); res.Status != 500 {
				t.Errorf("download%s without an audit entry: %d %s", q, res.Status, shown(res))
			}
		}
	})
}

// cancelAtEnd is a request body that cancels the request's context once
// its last byte was read: the browser goes away while the restore runs.
type cancelAtEnd struct {
	r      *strings.Reader
	cancel context.CancelFunc
}

func (c *cancelAtEnd) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if c.r.Len() == 0 {
		c.cancel()
	}
	return n, err
}

// RV16: a restore runs to its end when the browser goes away after the
// upload: every table of the archive comes back, and the audit entry is
// written.
func TestRestoreOutlivesTheRequest(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db, nil)
		ctx := context.Background()
		if _, err := db.Exec(ctx, `INSERT INTO folders (name) VALUES ('in the backup')`); err != nil {
			t.Fatal(err)
		}
		archive := []byte(w.As("admin").Do("GET", "/api/settings/backup", "").Body)
		if _, err := db.Exec(ctx, `UPDATE folders SET name = 'changed'`); err != nil {
			t.Fatal(err)
		}

		serve := func(req *http.Request) *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			w.Server.ServeHTTP(rec, req)
			return rec
		}
		signin := serve(httptest.NewRequest("GET", "/__test/signin/admin", nil))
		var cookies []string
		for _, c := range signin.Result().Cookies() {
			cookies = append(cookies, c.Name+"="+c.Value)
		}
		cookie := strings.Join(cookies, "; ")
		tokenReq := httptest.NewRequest("GET", "/__test/token", nil)
		tokenReq.Header.Set("Cookie", cookie)
		var token struct{ Token string }
		if err := json.Unmarshal(serve(tokenReq).Body.Bytes(), &token); err != nil || token.Token == "" {
			t.Fatalf("token: %v", err)
		}

		body, ct := multipartBody(t, "backup", "b.tar.gz", archive, nil)
		reqCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		req := httptest.NewRequest("POST", "/api/settings/restore", nil).WithContext(reqCtx)
		req.Body = io.NopCloser(&cancelAtEnd{r: strings.NewReader(body), cancel: cancel})
		req.ContentLength = int64(len(body))
		req.Header.Set("Content-Type", ct)
		req.Header.Set("Cookie", cookie)
		req.Header.Set("X-CSRF-Token", token.Token)
		res := serve(req)
		if reqCtx.Err() == nil {
			t.Fatal("the request's context was not cancelled")
		}
		if res.Code != 200 || !strings.HasPrefix(res.Body.String(), `{"success":true,"data":{"type":"full","restored":["database (`) ||
			!strings.Contains(res.Body.String(), `"warnings":[]`) {
			t.Errorf("restore: %d %s", res.Code, res.Body.String())
		}
		var name string
		if err := db.QueryRow(ctx, `SELECT name FROM folders`).Scan(&name); err != nil || name != "in the backup" {
			t.Errorf("folder after the restore: %q %v", name, err)
		}
		rows := w.AuditRows()
		if last := rows[len(rows)-1]; !strings.HasPrefix(last, "backup_restored|Full restore | Restored: database (") {
			t.Errorf("audit %q", rows)
		}
	})
}

// TestRestoreArchiveLimits: an uploaded archive with more entries than
// BACKUP_MAX_ENTRIES, one that unpacks past BACKUP_MAX_DECOMPRESSED_BYTES
// and one with an entry past its end are refused with 400 and the
// reader's message, as the old route answered them. A limit that is no
// whole number of at least 1 leaves the default in force (UM42): the old
// route refused every archive for -1 and let a third file through for
// 1.5.
func TestRestoreArchiveLimits(t *testing.T) {
	archive, err := backup.CreateTarGz([]backup.Entry{
		{Name: "manifest.json", Data: []byte(`{"_format":"neodesk-full-backup","_version":1}`)},
		{Name: "a", Data: []byte("1")}, {Name: "b", Data: []byte("2")},
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	var cut bytes.Buffer
	zw := gzip.NewWriter(&cut)
	header, err := backup.CreateTarGz([]backup.Entry{{Name: "cut.bin", Data: make([]byte, 600)}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	tar, err := gunzipAll(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = zw.Write(tar[:512+100])
	_ = zw.Close()
	const prefix = `{"success":false,"error":"Invalid backup archive: Not a valid gzip/tar archive: `
	for _, c := range []struct {
		env  map[string]string
		body []byte
		want string
	}{
		{map[string]string{"BACKUP_MAX_ENTRIES": "2"}, archive, `Backup archive has more than 2 entries"}`},
		{map[string]string{"BACKUP_MAX_ENTRIES": " 1.5 "}, archive, ""},
		{map[string]string{"BACKUP_MAX_DECOMPRESSED_BYTES": "0x800"}, archive, `Cannot create a Buffer larger than 2048 bytes"}`},
		{map[string]string{"BACKUP_MAX_DECOMPRESSED_BYTES": "-1"}, archive, ""},
		{nil, cut.Bytes(), `Backup archive entry cut.bin extends past the end of the archive"}`},
	} {
		w := newWorld(t, nil, c.env)
		body, ct := multipartBody(t, "backup", "b.tar.gz", c.body, nil)
		res := w.As("admin").Do("POST", "/api/settings/restore", body, "Content-Type", ct)
		if c.want == "" {
			if res.Status != 200 || !strings.HasPrefix(res.Body, `{"success":true,"data":{"type":"full",`) {
				t.Errorf("%v: %d %s", c.env, res.Status, res.Body)
			}
			continue
		}
		if res.Status != 400 || res.Body != prefix+c.want {
			t.Errorf("%v: %d %s", c.env, res.Status, res.Body)
		}
	}
}

func gunzipAll(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(zr)
}

func TestRetention(t *testing.T) {
	w := newWorld(t, nil, map[string]string{"BACKUP_RETENTION_COUNT": " 9x"})
	admin := w.As("admin")
	get := func() string { return admin.Do("GET", "/api/settings/backup/retention", "").Body }
	if got := get(); got != `{"success":true,"data":{"count":9,"source":"environment"}}` {
		t.Errorf("from the environment: %s", got)
	}
	for _, c := range [][2]string{
		{`{"count":-1}`, `{"success":false,"error":"count must be an integer between 0 and 1000"}`},
		{`{"count":1001}`, `{"success":false,"error":"count must be an integer between 0 and 1000"}`},
		{`{"count":"x"}`, `{"success":false,"error":"count must be an integer between 0 and 1000"}`},
		{`{}`, `{"success":false,"error":"count must be an integer between 0 and 1000"}`},
		{`{"count":"1000.9"}`, `{"success":true,"data":{"count":1000}}`},
		{`{"count":1e21}`, `{"success":true,"data":{"count":1}}`},
		{`{"count":["4"]}`, `{"success":true,"data":{"count":4}}`},
		{`{"count":0}`, `{"success":true,"data":{"count":0}}`},
	} {
		if res := admin.Do("PUT", "/api/settings/backup/retention", c[0]); res.Body != c[1] {
			t.Errorf("%s: %d %s", c[0], res.Status, res.Body)
		}
	}
	if got := get(); got != `{"success":true,"data":{"count":0,"source":"database"}}` {
		t.Errorf("after 0: %s", got)
	}
	admin.Do("PUT", "/api/settings/backup/retention", `{"count":5}`)
	rows := w.AuditRows()
	if rows[len(rows)-1] != "backup_retention_changed|Backup retention set to 5|127.0.0.1|1" || !contains(rows, "backup_retention_changed|Backup retention set to unlimited|127.0.0.1|1") {
		t.Errorf("audit %q", rows)
	}
	if _, err := w.DB.Exec(context.Background(), `UPDATE settings SET value = 'abc' WHERE key = 'backup_retention_count'`); err != nil {
		t.Fatal(err)
	}
	if got := get(); got != `{"success":true,"data":{"count":0,"source":"unset"}}` {
		t.Errorf("a stored value that is no number: %s", got)
	}
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

func TestSchedule(t *testing.T) {
	w := newWorld(t, nil, nil)
	admin := w.As("admin")
	if got := admin.Do("GET", "/api/settings/backup/schedule", "").Body; got != `{"success":true,"data":{"schedule":{"enabled":false,"intervalHours":24,"retentionCount":7,"destinationType":"local","destinationPath":""},"status":{"lastAttemptAt":null,"lastSuccessAt":null,"lastFile":null,"lastSha256":null,"lastValidation":null,"lastError":null}}}` {
		t.Errorf("default: %s", got)
	}
	dest := filepath.Join(w.DataDir, "scheduled-backups")
	if got := admin.Do("PUT", "/api/settings/backup/schedule", `{"enabled":"true","intervalHours":"2.5","retentionCount":"2"}`).Body; got != `{"success":true,"data":{"enabled":true,"intervalHours":3,"retentionCount":2,"destinationType":"local","destinationPath":"`+dest+`"}}` {
		t.Errorf("save: %s", got)
	}
	// Archives to keep that isn't a whole number from 1 to 365 is refused
	// with what is wrong, not saved as 1, 7 or 365 (L418)
	for _, keep := range []string{`0`, `400`, `-3`, `2.5`, `"abc"`, `""`, `null`} {
		res := admin.Do("PUT", "/api/settings/backup/schedule", `{"enabled":true,"retentionCount":`+keep+`}`)
		if res.Status != 400 || res.Body != `{"success":false,"error":"settings.backup_tab.retention_error"}` {
			t.Errorf("keep %s: %d %s", keep, res.Status, res.Body)
		}
	}
	if got := admin.Do("GET", "/api/settings/backup/schedule", "").Body; !strings.Contains(got, `"retentionCount":2,`) {
		t.Errorf("a refused save changed the schedule: %s", got)
	}
	if res := admin.Do("PUT", "/api/settings/backup/schedule", `{"destinationType":"path","destinationPath":"rel"}`); res.Status != 400 || res.Body != `{"success":false,"error":"Backup destination path must be absolute"}` {
		t.Errorf("relative: %d %s", res.Status, res.Body)
	}
	res := admin.Do("POST", "/api/settings/backup/schedule/run", "{}")
	var run struct {
		Data struct {
			Filename string `json:"filename"`
			SHA256   string `json:"sha256"`
			Deleted  []string
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.Body), &run); err != nil || res.Status != 200 || filepath.Dir(run.Data.Filename) != dest || len(run.Data.SHA256) != 64 ||
		!strings.Contains(res.Body, `"validation":{"valid":true,"components":["console/data.json",`) {
		t.Fatalf("run: %d %s", res.Status, res.Body)
	}
	if _, err := os.Stat(run.Data.Filename + ".sha256"); err != nil {
		t.Error(err)
	}
	rows := w.AuditRows()
	if rows[len(rows)-1] != "backup_created|Scheduled full backup created ("+run.Data.Filename+")|127.0.0.1|1" {
		t.Errorf("audit %q", rows)
	}
	status := admin.Do("GET", "/api/settings/backup/schedule/status", "").Body
	if !strings.Contains(status, `"lastFile":"`+run.Data.Filename+`"`) || !strings.Contains(status, `"lastPruned":0`) {
		t.Errorf("status %s", status)
	}
	if got := admin.Do("POST", "/api/settings/backup/destination/test", `{}`).Body; got != `{"success":true,"data":{"valid":true,"destination":"`+dest+`"}}` {
		t.Errorf("destination: %s", got)
	}
}

// The network share login is gone: backups go to a folder, where the
// operating system mounts a share. Its three routes are unknown paths, and
// a save of the schedule stores no share (L122).
func TestNoNetworkShareLogin(t *testing.T) {
	w := newWorld(t, nil, nil)
	admin := w.As("admin")
	for _, r := range []struct{ method, path, body string }{
		{"GET", "/api/settings/backup/smb", ""},
		{"PUT", "/api/settings/backup/smb", `{"host":"nas","share":"b","password":"hunter2"}`},
		{"POST", "/api/settings/backup/smb/test", `{"host":"nas","share":"b","password":"hunter2"}`},
	} {
		if res := admin.Do(r.method, r.path, r.body); res.Status != http.StatusNotFound {
			t.Errorf("%s %s: %d %s", r.method, r.path, res.Status, shown(res))
		}
	}
	var n int
	if err := w.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM settings WHERE key = 'scheduled_backup_smb'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("stored shares: %d %v", n, err)
	}
}

// TestFullBackupIsForSuperAdmins: a full backup holds every secret of the
// server: the database with the token signing secret, the password hashes
// and the API keys, the key pair and the API key file. Downloading one,
// restoring one or sending one to a destination is for the roles that
// already hold every permission; a server admin with server.config would
// otherwise read its way to a super admin's token (L387).
func TestFullBackupIsForSuperAdmins(t *testing.T) {
	w := newWorld(t, nil, nil)
	serverAdmin := w.As("server_admin")
	const refusal = `{"success":false,"error":"Only a super admin can download, restore or send a full backup: it holds every secret of the server"}`
	for _, r := range []struct{ method, path, body string }{
		{"GET", "/api/settings/backup", ""},
		{"POST", "/api/settings/restore", `{}`},
		{"PUT", "/api/settings/backup/schedule", `{"enabled":true}`},
		{"POST", "/api/settings/backup/schedule/run", `{}`},
		{"POST", "/api/settings/backup/destination/test", `{}`},
	} {
		if res := serverAdmin.Do(r.method, r.path, r.body); res.Status != http.StatusForbidden || res.Body != refusal {
			t.Errorf("%s %s as server_admin: %d %s", r.method, r.path, res.Status, shown(res))
		}
	}
	if res := serverAdmin.Do("GET", "/api/settings/backup/schedule", ""); res.Status != http.StatusOK {
		t.Errorf("server_admin reads the schedule: %d", res.Status)
	}
	for _, role := range []string{"super_admin", "admin"} {
		if res := w.As(role).Do("GET", "/api/settings/backup", ""); res.Status != http.StatusOK {
			t.Errorf("%s downloads a backup: %d %s", role, res.Status, shown(res))
		}
	}
}
