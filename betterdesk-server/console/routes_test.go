package console

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"io"
	"mime/multipart"
	"net/textproto"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// routesWorld is a console with the moved groups only, and a probe.
func routesWorld(t *testing.T, o setup) *world {
	t.Helper()
	w := newWorld(t, o)
	w.s.Router().Get("/api/probe", probe)
	return w
}

// copyLang copies some language files of the source tree into a folder
// the test may write to.
func copyLang(t *testing.T, codes ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, code := range codes {
		raw, err := os.ReadFile(filepath.Join("..", "..", "web-nodejs", "lang", code+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, code+".json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRegisteredRoutesAreTheMovedGroupsInOrder(t *testing.T) {
	w := newWorld(t, setup{})
	var got []string
	for _, route := range w.s.Router().Routes() {
		if strings.HasPrefix(route.Path, "/__test/") {
			continue
		}
		got = append(got, route.Method+" "+route.Path)
	}
	want := []string{
		"GET /health",
		"GET /api/console/strings/{code}",
		"POST /api/ui-shell",
		"GET /api/ui-shell",
		"GET /api/i18n/languages",
		"GET /api/i18n/translations/{code}",
		"POST /api/i18n/set/{code}",
		"GET /api/i18n/validate/{code}",
		"POST /api/i18n/upload",
		"DELETE /api/i18n/{code}",
		"GET /api/panel/languages",
		"GET /api/panel/languages/{code}/missing",
		"POST /api/panel/languages/{code}/fix",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if n := len(w.s.Early().Routes()); n != 0 {
		t.Errorf("%d early routes, want none yet", n)
	}
}

func TestHealth(t *testing.T) {
	w := routesWorld(t, setup{})
	before := time.Now().UTC().Truncate(time.Millisecond)
	res := w.agent().get("/health")
	if res.status != 200 || res.header.Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("got %d %q", res.status, res.header.Get("Content-Type"))
	}
	match := regexp.MustCompile(`^\{"status":"ok","timestamp":"(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z)"\}$`).FindStringSubmatch(res.body)
	if match == nil {
		t.Fatalf("body = %s", res.body)
	}
	at, err := time.Parse("2006-01-02T15:04:05.000Z", match[1])
	if err != nil || at.Before(before) || at.After(time.Now().Add(time.Second)) {
		t.Errorf("timestamp %s is not now (%v)", match[1], err)
	}
	if res.header.Get("Cache-Control") != "no-cache, no-store, must-revalidate" || res.header.Get("ETag") == "" || res.header.Get("RateLimit-Limit") != "" {
		t.Errorf("headers = %v", res.header)
	}
	if head := w.agent().do("HEAD", "/health", ""); head.status != 200 || head.body != "" {
		t.Errorf("HEAD: %d %q", head.status, head.body)
	}
	if post := w.agent().do("POST", "/health", ""); post.status != 403 {
		t.Errorf("POST without a token: %d, want the CSRF gate", post.status)
	}
	// A pro account and a signed-out visitor get the same answer
	if res := w.agent().signIn("pro").get("/health"); res.status != 200 {
		t.Errorf("as pro: %d", res.status)
	}
}

func TestUIShell(t *testing.T) {
	w := routesWorld(t, setup{})
	const signedOut = `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`
	out := w.agent()
	if res := out.get("/api/ui-shell"); res.status != 401 || res.body != signedOut {
		t.Errorf("GET signed out: %d %s", res.status, res.body)
	}
	if res := out.post("/api/ui-shell", `{"shell":"ux36"}`); res.status != 401 || res.body != signedOut || res.cookie("bd_ui_shell") != "" {
		t.Errorf("POST signed out: %d %s", res.status, res.body)
	}
	if res := w.agent().signIn("pro").get("/api/ui-shell"); res.status != 403 {
		t.Errorf("GET as pro: %d", res.status)
	}

	for _, role := range []string{"admin", "viewer", "guest_issuer"} {
		a := w.agent().signIn(role)
		if res := a.get("/api/ui-shell"); res.status != 200 || res.body != `{"success":true,"data":{"shell":"ux35","supported":["ux35","ux36"]}}` {
			t.Errorf("%s GET without a cookie: %d %s", role, res.status, res.body)
		}
		if res := a.do("POST", "/api/ui-shell", `{"shell":"ux36"}`, "Content-Type", "application/json"); res.status != 403 {
			t.Errorf("%s POST without a CSRF token: %d", role, res.status)
		}
		res := a.post("/api/ui-shell", `{"shell":"  UX36\n"}`)
		if res.status != 200 || res.body != `{"success":true,"data":{"shell":"ux36"}}` {
			t.Fatalf("%s POST: %d %s", role, res.status, res.body)
		}
		cookie := res.cookie("bd_ui_shell")
		if !regexp.MustCompile(`^bd_ui_shell=ux36; Max-Age=31536000; Path=/; Expires=\w{3}, \d\d \w{3} \d{4} \d\d:\d\d:\d\d GMT; SameSite=Lax$`).MatchString(cookie) {
			t.Errorf("%s cookie = %q", role, cookie)
		}
		expires, err := time.Parse(time.RFC1123, strings.TrimSuffix(strings.SplitN(cookie, "Expires=", 2)[1], "; SameSite=Lax"))
		if err != nil || expires.Sub(time.Now()) < 364*24*time.Hour || expires.Sub(time.Now()) > 366*24*time.Hour {
			t.Errorf("%s cookie expires %v (%v), want in a year", role, expires, err)
		}
		if res := a.get("/api/ui-shell"); res.body != `{"success":true,"data":{"shell":"ux36","supported":["ux35","ux36"]}}` {
			t.Errorf("%s GET with the cookie: %s", role, res.body)
		}
		for _, cookie := range []string{"bd_ui_shell=classic", `bd_ui_shell=j:{"a":1}`, "bd_ui_shell="} {
			if res := a.get("/api/ui-shell", "+Cookie", cookie); !strings.Contains(res.body, `"shell":"ux35"`) {
				t.Errorf("%s GET with %s: %s", role, cookie, res.body)
			}
		}
		if res := a.get("/api/ui-shell", "+Cookie", "bd_ui_shell=%20UX36"); !strings.Contains(res.body, `"shell":"ux36"`) {
			t.Errorf("%s GET with a cookie in other spelling: %s", role, res.body)
		}
		for _, body := range []string{`{"shell":"ux37"}`, `{"shell":36}`, `{"shell":["ux36"]}`, `{}`, `[]`, `{"shell":null}`} {
			res := a.post("/api/ui-shell", body)
			if res.status != 400 || res.body != `{"success":false,"error":"Unsupported shell. Allowed: ux35, ux36"}` || res.cookie("bd_ui_shell") != "" {
				t.Errorf("%s POST %s: %d %s", role, body, res.status, res.body)
			}
		}
		if res := a.do("POST", "/api/ui-shell", "shell=ux36", "Content-Type", "application/x-www-form-urlencoded", "X-CSRF-Token", a.csrf()); res.status != 415 {
			t.Errorf("%s POST as a form: %d", role, res.status)
		}
	}
}

func TestUIShellCookieIsSecureInHTTPSMode(t *testing.T) {
	w := routesWorld(t, setup{https: true, trustProxy: "loopback"})
	a := w.agent().signIn("admin", "X-Forwarded-Proto", "https")
	token, _ := a.get("/__test/token", "X-Forwarded-Proto", "https").json(t)["token"].(string)
	res := a.do("POST", "/api/ui-shell", `{"shell":"ux35"}`, "Content-Type", "application/json", "X-CSRF-Token", token, "X-Forwarded-Proto", "https")
	if res.status != 200 || !strings.HasSuffix(res.cookie("bd_ui_shell"), "; Secure; SameSite=Lax") {
		t.Errorf("got %d, cookie %q", res.status, res.cookie("bd_ui_shell"))
	}
}

func TestConsoleStrings(t *testing.T) {
	w := routesWorld(t, setup{})
	a := w.agent()
	res := a.get("/api/console/strings/de")
	etag := res.header.Get("ETag")
	if res.status != 200 || !regexp.MustCompile(`^"[0-9a-f]{16}"$`).MatchString(etag) || res.header.Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("got %d, ETag %q, type %q", res.status, etag, res.header.Get("Content-Type"))
	}
	if res.header.Get("Cache-Control") != "no-cache" || res.header.Get("Vary") != "Accept-Encoding" || res.header.Get("Content-Encoding") != "" {
		t.Errorf("headers = %v", res.header)
	}
	if !strings.HasPrefix(res.body, "{") || strings.Contains(res.body[:20], "success") {
		t.Errorf("body starts %.60s, want the texts without an envelope", res.body)
	}
	revision := strings.Trim(etag, `"`)
	if got := a.get("/api/console/strings/de?v=" + revision).header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("with the revision: Cache-Control = %q", got)
	}
	if got := a.get("/api/console/strings/de?v=old").header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("with an old revision: Cache-Control = %q", got)
	}
	if not := a.get("/api/console/strings/de", "If-None-Match", etag); not.status != 304 || not.body != "" {
		t.Errorf("If-None-Match: %d with %d bytes", not.status, len(not.body))
	}
	zipped := a.get("/api/console/strings/de", "Accept-Encoding", "gzip, deflate, br")
	if zipped.header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("Accept-Encoding gzip: Content-Encoding = %q", zipped.header.Get("Content-Encoding"))
	}
	reader, err := gzip.NewReader(strings.NewReader(zipped.body))
	if err != nil {
		t.Fatal(err)
	}
	if plain, _ := io.ReadAll(reader); string(plain) != res.body {
		t.Error("the compressed body is not the plain one")
	}
	for _, code := range []string{"xx", "DE", "de.json", "..%2Fde"} {
		if res := a.get("/api/console/strings/" + code); res.status != 404 || res.body != `{"success":false,"error":"Language not found"}` {
			t.Errorf("code %s: %d %s", code, res.status, res.body)
		}
	}
	if res := w.agent().signIn("pro").get("/api/console/strings/en"); res.status != 200 {
		t.Errorf("as pro: %d", res.status)
	}
}

func TestI18nPublicRoutes(t *testing.T) {
	w := routesWorld(t, setup{})
	a := w.agent()
	res := a.get("/api/i18n/languages")
	body := res.json(t)
	list, _ := body["list"].([]any)
	if res.status != 200 || body["success"] != true || len(list) < 20 {
		t.Fatalf("languages: %d with %d entries", res.status, len(list))
	}
	if data, _ := body["data"].(map[string]any); data["de"] == nil || data["en"] == nil {
		t.Errorf("languages: data lacks de or en")
	}
	res = a.get("/api/i18n/translations/de")
	if res.status != 200 || !strings.HasPrefix(res.body, `{"success":true,"data":{`) {
		t.Errorf("translations/de: %d %.60s", res.status, res.body)
	}
	if zipped := a.get("/api/i18n/translations/de", "Accept-Encoding", "gzip"); zipped.header.Get("Content-Encoding") != "gzip" {
		t.Error("the large translations answer is not compressed")
	}
	if res := a.get("/api/i18n/translations/xx"); res.status != 404 || res.body != `{"success":false,"error":"Language not found"}` {
		t.Errorf("translations/xx: %d %s", res.status, res.body)
	}
	if res := a.get("/api/i18n/translations/de.json"); res.status != 400 || res.body != `{"success":false,"error":"Invalid language code"}` {
		t.Errorf("translations/de.json: %d %s", res.status, res.body)
	}
	const signedOut = `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`
	if res := a.get("/api/i18n/validate/de"); res.status != 401 || res.body != signedOut {
		t.Errorf("validate signed out: %d %s", res.status, res.body)
	}
	if res := w.agent().signIn("viewer").get("/api/i18n/validate/de"); res.status != 200 || res.json(t)["success"] != true {
		t.Errorf("validate as viewer: %d %.80s", res.status, res.body)
	}
}

func TestI18nSetStoresTheChoice(t *testing.T) {
	w := routesWorld(t, setup{})
	language := func(role string) sql.NullString {
		var lang sql.NullString
		if err := w.db.QueryRow(context.Background(), "SELECT preferred_language FROM users WHERE id = ?", roleIDs[role]).Scan(&lang); err != nil {
			t.Fatal(err)
		}
		return lang
	}

	// Signed out: the cookie only
	out := w.agent()
	if res := out.do("POST", "/api/i18n/set/de", "", "Content-Type", "application/json", "Content-Length", "0"); res.status != 403 {
		t.Errorf("without a CSRF token: %d", res.status)
	}
	res := out.post("/api/i18n/set/de", "{}")
	if res.status != 200 || res.body != `{"success":true}` {
		t.Fatalf("signed out: %d %s", res.status, res.body)
	}
	if cookie := res.cookie("betterdesk_lang"); !strings.HasPrefix(cookie, "betterdesk_lang=de; Max-Age=31536000; Path=/; Expires=") || !strings.HasSuffix(cookie, "GMT; SameSite=Lax") {
		t.Errorf("cookie = %q", cookie)
	}
	if res := out.post("/api/i18n/set/xx", "{}"); res.status != 400 || res.body != `{"success":false,"error":"Language not supported"}` || res.cookie("betterdesk_lang") != "" {
		t.Errorf("unknown language: %d %s", res.status, res.body)
	}

	// Signed in: the users row and the session too
	a := w.agent().signIn("operator")
	if language("operator").Valid {
		t.Fatal("the account has a language before it chose one")
	}
	if res := a.post("/api/i18n/set/pl", "{}"); res.status != 200 {
		t.Fatalf("signed in: %d %s", res.status, res.body)
	}
	if got := language("operator"); got.String != "pl" {
		t.Errorf("users.preferred_language = %q, want pl", got.String)
	}
	if language("viewer").Valid {
		t.Error("another account's row was written")
	}
	delete(a.cookies, "betterdesk_lang")
	if res := a.get("/api/probe", "Accept-Language", "de"); res.json(t)["lang"] != "pl" {
		t.Errorf("the session does not carry the choice: %s", res.body)
	}
	var data string
	if err := w.db.QueryRow(context.Background(), "SELECT session_json FROM panel_sessions WHERE user_id = ? AND is_active = 1", roleIDs["operator"]).Scan(&data); err != nil {
		t.Fatal(err)
	}
	if want := `"user":{"id":` + strconv.FormatInt(roleIDs["operator"], 10) + `,"username":"e2e_operator","role":"operator","preferred_language":"pl"}`; !strings.Contains(data, want) {
		t.Errorf("stored session = %s\nwant it to hold %s", data, want)
	}

	prod := routesWorld(t, setup{production: true})
	if cookie := prod.agent().post("/api/i18n/set/de", "{}").cookie("betterdesk_lang"); !strings.HasSuffix(cookie, "; Secure; SameSite=Lax") {
		t.Errorf("in production the cookie is %q, want Secure", cookie)
	}
}

// upload builds a multipart body with one file.
func upload(t *testing.T, filename, contentType, content string) (body, header string) {
	t.Helper()
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	part, err := form.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="file"; filename="` + filename + `"`},
		"Content-Type":        {contentType},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte(content))
	_ = form.Close()
	return buf.String(), form.FormDataContentType()
}

func TestI18nUploadAndDelete(t *testing.T) {
	w := routesWorld(t, setup{langDir: copyLang(t, "en", "pl", "de")})
	file := `{"_meta":{"code":"fr","name":"French","native":"Français","flag":"fr"},"nav":{"devices":"Appareils"}}`
	body, contentType := upload(t, "anything.json", "application/json", file)
	send := func(a *agent) answer {
		return a.do("POST", "/api/i18n/upload", body, "Content-Type", contentType, "X-CSRF-Token", a.csrf())
	}

	if res := send(w.agent()); res.status != 401 || !strings.Contains(res.body, `"code":"session_required"`) {
		t.Errorf("upload signed out: %d %s", res.status, res.body)
	}
	for _, role := range []string{"viewer", "operator", "global_admin"} {
		if res := send(w.agent().signIn(role)); res.status != 403 || res.body != `{"success":false,"error":"Permission denied: server.config"}` {
			t.Errorf("upload as %s: %d %s", role, res.status, res.body)
		}
	}
	admin := w.agent().signIn("server_admin")
	if res := admin.do("POST", "/api/i18n/upload", body, "Content-Type", contentType); res.status != 403 || !strings.Contains(res.body, "Invalid CSRF token") {
		t.Errorf("upload without a CSRF token: %d %s", res.status, res.body)
	}
	if _, err := os.Stat(filepath.Join(w.s.cfg.LangDir, "fr.json")); err == nil {
		t.Fatal("a refused upload wrote the file")
	}

	res := send(admin)
	if res.status != 200 || res.body != `{"success":true,"data":{"code":"fr","meta":{"code":"fr","name":"French","native":"Français","flag":"fr","rtl":false}}}` {
		t.Fatalf("upload: %d %s", res.status, res.body)
	}
	if saved, err := os.ReadFile(filepath.Join(w.s.cfg.LangDir, "fr.json")); err != nil || !strings.Contains(string(saved), `"devices": "Appareils"`) {
		t.Errorf("fr.json = %.100s (%v)", saved, err)
	}
	if res := admin.get("/api/i18n/translations/fr"); res.status != 200 {
		t.Errorf("the uploaded language is not served: %d", res.status)
	}
	id := strconv.FormatInt(roleIDs["server_admin"], 10)
	if rows := w.auditRows(); len(rows) != 1 || rows[0] != "language_uploaded|Language fr uploaded|127.0.0.1|"+id {
		t.Errorf("audit log after the upload = %q", rows)
	}

	if res := w.agent().signIn("viewer").do("DELETE", "/api/i18n/fr", "", "X-CSRF-Token", w.agent().csrf()); res.status != 403 {
		t.Errorf("delete with another browser's token: %d", res.status)
	}
	viewer := w.agent().signIn("viewer")
	if res := viewer.do("DELETE", "/api/i18n/fr", "", "X-CSRF-Token", viewer.csrf()); res.status != 403 || res.body != `{"success":false,"error":"Permission denied: server.config"}` {
		t.Errorf("delete as viewer: %d %s", res.status, res.body)
	}
	if res := admin.do("DELETE", "/api/i18n/en", "", "X-CSRF-Token", admin.csrf()); res.status != 400 {
		t.Errorf("delete English: %d %s", res.status, res.body)
	}
	if res := admin.do("DELETE", "/api/i18n/fr", "", "X-CSRF-Token", admin.csrf()); res.status != 200 || res.body != `{"success":true}` {
		t.Fatalf("delete: %d %s", res.status, res.body)
	}
	if _, err := os.Stat(filepath.Join(w.s.cfg.LangDir, "fr.json")); err == nil {
		t.Error("fr.json is still there")
	}
	if rows := w.auditRows(); len(rows) != 2 || rows[1] != "language_deleted|Language fr deleted|127.0.0.1|"+id {
		t.Errorf("audit log after the delete = %q", rows)
	}

	// An upload over the file size limit is refused before anything is written
	large, largeType := upload(t, "big.json", "application/json", `{"_meta":{"code":"sv"},"x":"`+strings.Repeat("a", 1<<20)+`"}`)
	res = admin.do("POST", "/api/i18n/upload", large, "Content-Type", largeType, "X-CSRF-Token", admin.csrf())
	if res.status != 500 || res.json(t)["success"] != false {
		t.Errorf("upload over the limit: %d %.100s", res.status, res.body)
	}
	if _, err := os.Stat(filepath.Join(w.s.cfg.LangDir, "sv.json")); err == nil {
		t.Error("the oversized upload was written")
	}
}

func TestLanguagesOverview(t *testing.T) {
	w := routesWorld(t, setup{})
	const signedOut = `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`
	paths := []string{"/api/panel/languages", "/api/panel/languages/de/missing"}
	for _, path := range paths {
		if res := w.agent().get(path); res.status != 401 || res.body != signedOut {
			t.Errorf("%s signed out: %d %s", path, res.status, res.body)
		}
		for _, role := range []string{"viewer", "operator", "global_admin", "guest_issuer"} {
			if res := w.agent().signIn(role).get(path); res.status != 403 || res.body != `{"success":false,"error":"Permission denied: server.config"}` {
				t.Errorf("%s as %s: %d %s", path, role, res.status, res.body)
			}
		}
		if res := w.agent().signIn("pro").get(path); res.status != 403 || !strings.Contains(res.body, "Pro accounts") {
			t.Errorf("%s as pro: %d %s", path, res.status, res.body)
		}
	}
	for _, role := range []string{"admin", "super_admin", "server_admin"} {
		a := w.agent().signIn(role)
		res := a.get("/api/panel/languages")
		body := res.json(t)
		if list, _ := body["languages"].([]any); res.status != 200 || len(list) < 20 || body["refKeyCount"].(float64) < 100 {
			t.Errorf("overview as %s: %d %.100s", role, res.status, res.body)
		}
		if res.header.Get("RateLimit-Limit") != "120" {
			t.Errorf("overview as %s: RateLimit-Limit = %q, want the file limiter's 120", role, res.header.Get("RateLimit-Limit"))
		}
		res = a.get("/api/panel/languages/de/missing")
		if missing := res.json(t); res.status != 200 || missing["code"] != "de" {
			t.Errorf("missing as %s: %d %.100s", role, res.status, res.body)
		}
		res = a.post("/api/panel/languages/de/fix", "{}")
		if res.status != 410 || !strings.HasPrefix(res.body, `{"error":"Automatic language fixing is disabled.`) {
			t.Errorf("fix as %s: %d %s", role, res.status, res.body)
		}
		if res.header.Get("RateLimit-Limit") != "30" {
			t.Errorf("fix as %s: RateLimit-Limit = %q, want the upload limiter's 30", role, res.header.Get("RateLimit-Limit"))
		}
	}
	if res := w.agent().post("/api/panel/languages/de/fix", "{}"); res.status != 401 {
		t.Errorf("fix signed out: %d", res.status)
	}
}

func TestLanguagesLimiterComesBeforeTheGuards(t *testing.T) {
	w := routesWorld(t, setup{env: map[string]string{"FILE_ACCESS_RATE_LIMIT_MAX": "2", "UPLOAD_RATE_LIMIT_MAX": "1"}})
	a := w.agent()
	for i := 0; i < 2; i++ {
		if res := a.get("/api/panel/languages"); res.status != 401 {
			t.Fatalf("request %d: %d", i+1, res.status)
		}
	}
	res := a.get("/api/panel/languages/de/missing")
	if res.status != 429 || res.body != `{"success":false,"error":"Too many file requests. Please try again later."}` {
		t.Errorf("third file request: %d %s, want the limiter before the sign-in check", res.status, res.body)
	}
	a.post("/api/panel/languages/de/fix", "{}")
	res = a.post("/api/panel/languages/de/fix", "{}")
	if res.status != 429 || res.body != `{"success":false,"error":"Too many upload requests. Please try again later."}` {
		t.Errorf("second fix: %d %s", res.status, res.body)
	}
}
