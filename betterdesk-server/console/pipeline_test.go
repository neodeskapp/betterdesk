package console

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/kit"
)

// goOnly is a console without the Node console behind it, with a probe
// route in each stage.
func goOnly(t *testing.T, o setup) *world {
	t.Helper()
	w := newWorld(t, o)
	w.s.Router().Get("/api/probe", probe)
	w.s.Router().Post("/api/probe", probe)
	w.s.Router().Get("/page/probe", probe)
	w.s.Router().Post("/page/probe", probe)
	w.s.Early().Get("/api/early", probe)
	w.s.Early().Post("/api/early", probe)
	return w
}

func TestStepOrderIsTheOneOfTheNodeConsole(t *testing.T) {
	w := goOnly(t, setup{})
	var got []string
	last := 0
	for _, st := range w.s.steps() {
		if st.n < last {
			t.Errorf("step %d %q comes after step %d", st.n, st.name, last)
		}
		last = st.n
		if st.use == nil {
			t.Errorf("step %d %q is off without the Node console", st.n, st.name)
		}
		got = append(got, strconv.Itoa(st.n)+" "+st.name)
	}
	want := "1 security headers|3 CORS|4 bodies|6 session|8 CSRF token|9 static folders|10 permissions and panel access|" +
		"11 rate limiters|12 client and device API routes|14 request language|15 cache header|16 CSRF gate|" +
		"17 compression mark|18 JSON content type|18 routes|19 web app"
	if strings.Join(got, "|") != want {
		t.Errorf("steps = %s\nwant    %s", strings.Join(got, "|"), want)
	}

	mixed := newWorld(t, setup{node: true})
	for _, st := range mixed.s.steps() {
		off := st.n == 9 || st.n == 19
		if (st.use == nil) != off {
			t.Errorf("with the Node console, step %d %q off = %v, want %v", st.n, st.name, st.use == nil, off)
		}
	}
}

func TestStep1SecurityHeadersAreOnEveryAnswer(t *testing.T) {
	w := goOnly(t, setup{})
	a := w.agent()
	for name, res := range map[string]answer{
		"a route":        a.get("/api/probe"),
		"the 404":        a.get("/api/nothing"),
		"a body error":   a.do("POST", "/api/probe", "{broken", "Content-Type", "application/json"),
		"a preflight":    a.do("OPTIONS", "/api/probe", ""),
		"a CSRF refusal": a.do("POST", "/api/probe", "{}", "Content-Type", "application/json"),
	} {
		t.Run(name, func(t *testing.T) { wantSecurityHeaders(t, res) })
	}
	if got := a.get("/api/nothing").header.Get("X-Powered-By"); got != "" {
		t.Errorf("X-Powered-By = %q", got)
	}
}

func TestStep3CORSAnswersPreflightsBeforeAnythingElse(t *testing.T) {
	w := goOnly(t, setup{})
	// A broken body and a missing token do not matter: the preflight ends at step 3
	res := w.agent().do("OPTIONS", "/api/probe", "{broken", "Origin", "tauri://localhost", "Content-Type", "application/json")
	if res.status != 204 || res.body != "" {
		t.Fatalf("status %d body %q, want 204 without a body", res.status, res.body)
	}
	if res.header.Get("Access-Control-Allow-Origin") != "tauri://localhost" || res.header.Get("Access-Control-Allow-Credentials") != "true" ||
		res.header.Get("Access-Control-Allow-Headers") != "Content-Type, Authorization, X-API-Key, X-CSRF-Token" {
		t.Errorf("CORS headers = %v", res.header)
	}
	if len(res.header.Values("Set-Cookie")) != 0 {
		t.Errorf("the preflight set cookies %q: it must end before the CSRF token step", res.header.Values("Set-Cookie"))
	}
	if res.header.Get("RateLimit-Limit") != "" {
		t.Error("the preflight was counted by a rate limiter")
	}

	// A request from the desktop client's origin gets the headers and goes on
	res = w.agent().get("/api/probe", "Origin", "https://tauri.localhost")
	if res.status != 200 || res.header.Get("Access-Control-Allow-Origin") != "https://tauri.localhost" {
		t.Errorf("status %d, Allow-Origin %q", res.status, res.header.Get("Access-Control-Allow-Origin"))
	}
	if res := w.agent().get("/api/probe", "Origin", "https://evil.example"); res.header.Get("Access-Control-Allow-Origin") != "" {
		t.Error("a foreign origin was allowed")
	}
	// Outside /api/ there is no CORS: OPTIONS gets the route's method list
	res = w.agent().do("OPTIONS", "/page/probe", "", "Origin", "tauri://localhost")
	if res.status != 200 || res.header.Get("Allow") != "GET,HEAD,POST" || res.header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("OPTIONS on a page route: %d Allow=%q", res.status, res.header.Get("Allow"))
	}
}

func TestStep4BodyErrorSkipsTheRestAndIsAnsweredByTheErrorHandler(t *testing.T) {
	w := goOnly(t, setup{})
	a := w.agent().signIn("admin")
	before := len(a.cookies)

	res := a.do("POST", "/api/probe", "{broken", "Content-Type", "application/json")
	if res.status != 400 {
		t.Fatalf("status %d, want the parser's 400: %s", res.status, res.body)
	}
	body := res.json(t)
	if body["success"] != false || !strings.HasPrefix(body["error"].(string), "invalid JSON") {
		t.Errorf("body = %s, want the error handler's JSON with the parser's message", res.body)
	}
	if got := res.header.Values("Set-Cookie"); len(got) != 0 {
		t.Errorf("Set-Cookie = %q: the session step must not run after a body error", got)
	}
	if res.header.Get("RateLimit-Limit") != "" {
		t.Error("the request was counted by a rate limiter")
	}
	if res.header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want the one of step 1", res.header.Get("Cache-Control"))
	}
	if len(a.cookies) != before {
		t.Error("cookies changed")
	}

	big := `{"a":"` + strings.Repeat("x", 2<<20) + `"}`
	res = a.do("POST", "/api/probe", big, "Content-Type", "application/json")
	if res.status != 413 || res.json(t)["error"] != "request entity too large" {
		t.Errorf("status %d body %.100s, want 413", res.status, res.body)
	}

	prod := goOnly(t, setup{production: true})
	res = prod.agent().do("POST", "/api/probe", "{broken", "Content-Type", "application/json")
	if res.status != 400 || res.body != `{"success":false,"error":"Internal Server Error"}` {
		t.Errorf("in production: %d %s", res.status, res.body)
	}
}

func TestStep4BodyReachesTheHandler(t *testing.T) {
	w := goOnly(t, setup{})
	res := w.agent().post("/api/probe", `{"name":"x","n":1}`)
	if res.status != 200 || !strings.Contains(res.body, `"body":{"name":"x","n":1}`) {
		t.Errorf("status %d body %s", res.status, res.body)
	}
}

func TestStep6SessionIsLoadedAndRenewed(t *testing.T) {
	w := goOnly(t, setup{})
	a := w.agent()
	if res := a.get("/api/probe"); res.json(t)["user"] != float64(0) || res.cookie("bd.sid") != "" {
		t.Errorf("signed out: body %s, session cookie %q", res.body, res.cookie("bd.sid"))
	}
	a.signIn("operator")
	res := a.get("/api/probe")
	if res.json(t)["user"] != float64(roleIDs["operator"]) {
		t.Errorf("the handler saw user %v", res.json(t)["user"])
	}
	cookie := res.cookie("bd.sid")
	if !strings.Contains(cookie, "; Path=/; Expires=") || !strings.HasSuffix(cookie, "; HttpOnly; SameSite=Lax") {
		t.Errorf("the answer does not renew the session cookie: %q", cookie)
	}
}

func TestStep7IdleSessionIsEndedAndAudited(t *testing.T) {
	w := goOnly(t, setup{})
	a := w.agent().signIn("operator")

	// The row still looks fresh; the time inside the session is two hours old
	ctx := context.Background()
	var sid, data string
	if err := w.db.QueryRow(ctx, "SELECT sid, session_json FROM panel_sessions WHERE user_id = ?", roleIDs["operator"]).Scan(&sid, &data); err != nil {
		t.Fatal(err)
	}
	old := strconv.FormatInt(time.Now().Add(-2*time.Hour).UnixMilli(), 10)
	aged := regexp.MustCompile(`"lastActivityAt":\d+`).ReplaceAllString(data, `"lastActivityAt":`+old)
	if aged == data {
		t.Fatalf("no lastActivityAt in %s", data)
	}
	if _, err := w.db.Exec(ctx, "UPDATE panel_sessions SET session_json = ? WHERE sid = ?", aged, sid); err != nil {
		t.Fatal(err)
	}

	res := a.get("/api/probe")
	if res.status != 401 || res.body != `{"success":false,"error":"Session expired. Please log in again.","code":"session_idle"}` {
		t.Fatalf("got %d %s", res.status, res.body)
	}
	if res.header.Get("RateLimit-Limit") != "" {
		t.Error("the expired request went on to the rate limiters")
	}
	rows := w.auditRows()
	if len(rows) != 1 || rows[0] != "session_expired|reason=idle|127.0.0.1|"+strconv.FormatInt(roleIDs["operator"], 10) {
		t.Errorf("audit log = %q", rows)
	}
	if res := a.get("/api/probe"); res.json(t)["user"] != float64(0) {
		t.Error("the session still works")
	}
}

func TestStep7AddressChangeIsAudited(t *testing.T) {
	w := goOnly(t, setup{trustProxy: "loopback"})
	a := w.agent().signIn("viewer", "X-Forwarded-For", "203.0.113.5")
	a.get("/api/probe", "X-Forwarded-For", "203.0.113.5")
	if rows := w.auditRows(); len(rows) != 0 {
		t.Fatalf("audit log before the change = %q", rows)
	}
	res := a.get("/api/probe", "X-Forwarded-For", "203.0.113.6", "User-Agent", "probe/1")
	if res.json(t)["ip"] != "203.0.113.6" {
		t.Errorf("the handler saw address %v", res.json(t)["ip"])
	}
	a.get("/api/probe", "X-Forwarded-For", "203.0.113.6", "User-Agent", "probe/1")
	rows := w.auditRows()
	want := "session_ip_change|IP change during session: loginIp=203.0.113.5 currentIp=203.0.113.6 ua=probe/1|203.0.113.6|" + strconv.FormatInt(roleIDs["viewer"], 10)
	if len(rows) != 1 || rows[0] != want {
		t.Errorf("audit log = %q\nwant one row %q", rows, want)
	}
}

func TestStep8CSRFTokenIsHandedOutOnEveryRead(t *testing.T) {
	w := goOnly(t, setup{})
	a := w.agent()
	res := a.get("/api/probe")
	cookie := res.cookie("__csrf_h")
	if !regexp.MustCompile(`^__csrf_h=[0-9a-f]{128}%7C[0-9a-f]{64}; Path=/; HttpOnly; SameSite=Lax$`).MatchString(cookie) {
		t.Errorf("CSRF cookie = %q", cookie)
	}
	if res.json(t)["token"] != true {
		t.Error("the handler has no CSRF token")
	}
	if res := a.get("/api/nothing"); res.status != 404 || res.cookie("__csrf_h") == "" {
		t.Errorf("the 404 (status %d) carries no CSRF cookie", res.status)
	}
	if res := w.agent().do("POST", "/api/early", "{}", "Content-Type", "application/json"); res.cookie("__csrf_h") != "" {
		t.Error("a write got a CSRF cookie")
	}
}

// publicFolder makes a folder of static files.
func publicFolder(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"favicon.svg":   "<svg/>",
		"api/file.json": `{"file":true}`,
		"img/logo.png":  "png",
		"notes.txt":     "notes",
	} {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestStep9StaticFilesComeBeforeGuardLimitersAndCacheHeader(t *testing.T) {
	w := goOnly(t, setup{public: publicFolder(t)})
	a := w.agent().signIn("viewer")

	res := a.get("/favicon.svg")
	if res.status != 200 || res.body != "<svg/>" || res.header.Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("file: %d %q %q", res.status, res.body, res.header.Get("Content-Type"))
	}
	// The folder's max-age replaces the Cache-Control of step 1 (BUGS
	// S15): none outside production, a week in production
	if res.header.Get("Cache-Control") != "public, max-age=0" {
		t.Errorf("Cache-Control of a file = %q, want the folder's max-age", res.header.Get("Cache-Control"))
	}
	if res.cookie("__csrf_h") == "" || res.cookie("bd.sid") == "" {
		t.Errorf("a file answer must carry the cookies of steps 6 and 8: %q", res.header.Values("Set-Cookie"))
	}
	for _, target := range []string{"/img/logo.png", "/notes.txt"} {
		if res := a.get(target); res.header.Get("Cache-Control") != "public, max-age=0" {
			t.Errorf("Cache-Control of %s = %q", target, res.header.Get("Cache-Control"))
		}
	}
	production := goOnly(t, setup{public: publicFolder(t), production: true}).agent()
	for _, target := range []string{"/favicon.svg", "/img/logo.png", "/notes.txt"} {
		if res := production.get(target); res.status != 200 || res.header.Get("Cache-Control") != "public, max-age=604800" {
			t.Errorf("production, %s: %d, Cache-Control %q", target, res.status, res.header.Get("Cache-Control"))
		}
	}
	// A file under /api/ is answered before the limiters count
	res = a.get("/api/file.json")
	if res.status != 200 || res.body != `{"file":true}` || res.header.Get("RateLimit-Limit") != "" {
		t.Errorf("file under /api/: %d %q, RateLimit-Limit %q", res.status, res.body, res.header.Get("RateLimit-Limit"))
	}
	// What is no file goes on
	if res := a.get("/api/probe"); res.status != 200 || res.header.Get("RateLimit-Limit") == "" {
		t.Errorf("route behind the static step: %d", res.status)
	}
}

func TestStep10PanelAccessGuardRunsBeforeLimitersAndRoutes(t *testing.T) {
	w := goOnly(t, setup{})
	reached := false
	w.s.Router().Get("/api/users/probe", func(rw http.ResponseWriter, r *http.Request) { reached = true; probe(rw, r) })
	w.s.Router().Get("/api/devices/probe/{a}/{b}/{c}", probe)

	viewer := w.agent().signIn("viewer")
	res := viewer.get("/api/users/probe")
	if res.status != 403 || res.body != `{"success":false,"error":"Panel access denied: users","code":"panel_access_denied"}` {
		t.Fatalf("viewer on a users path: %d %s", res.status, res.body)
	}
	if reached {
		t.Error("the route ran behind a refusing guard")
	}
	if res.header.Get("RateLimit-Limit") != "" {
		t.Error("the refused request was counted: the guard must come before the limiters")
	}
	if res.header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want step 1's: the guard answers before step 15", res.header.Get("Cache-Control"))
	}

	res = viewer.get("/api/devices/probe/a/b/c")
	body := res.json(t)
	if res.status != 200 || body["section"] != "devices" || body["loaded"] != true {
		t.Errorf("viewer on a devices path: %d %s", res.status, res.body)
	}
	if res := w.agent().signIn("admin").get("/api/users/probe"); res.status != 200 || !reached {
		t.Errorf("admin on a users path: %d", res.status)
	}
	// Signed out the guard has nothing to say; the route's own guard answers
	if res := w.agent().get("/api/users/probe"); res.status != 200 {
		t.Errorf("signed out: %d %s", res.status, res.body)
	}
}

func TestStep11LimiterAnswersBeforeTheRoutesOwnGuard(t *testing.T) {
	w := goOnly(t, setup{env: map[string]string{"RATE_LIMIT_MAX": "2"}})
	d := w.s.Deps()
	w.s.Router().Get("/api/guarded", probe, d.RequireAuth)
	a := w.agent()
	for i := 1; i <= 2; i++ {
		res := a.get("/api/guarded")
		if res.status != 401 || res.header.Get("RateLimit-Limit") != "2" || res.header.Get("RateLimit-Remaining") != strconv.Itoa(2-i) {
			t.Fatalf("request %d: %d, RateLimit %q/%q", i, res.status, res.header.Get("RateLimit-Remaining"), res.header.Get("RateLimit-Limit"))
		}
	}
	res := a.get("/api/guarded")
	if res.status != 429 || res.body != `{"success":false,"error":"Too many requests. Please try again later."}` {
		t.Fatalf("third request: %d %s, want the limiter's 429 and not the guard's 401", res.status, res.body)
	}
	if res.header.Get("Retry-After") == "" || res.header.Get("RateLimit-Policy") != "2;w=60" {
		t.Errorf("429 headers = %v", res.header)
	}
	// The early stage sits behind the limiters too
	if res := a.get("/api/early"); res.status != 429 {
		t.Errorf("early route past the limit: %d, want 429", res.status)
	}
	// Paths outside /api/ are not counted
	if res := a.get("/page/probe"); res.status != 200 || res.header.Get("RateLimit-Limit") != "" {
		t.Errorf("page route: %d, RateLimit-Limit %q", res.status, res.header.Get("RateLimit-Limit"))
	}
}

func TestStep12EarlyStageHasNoLanguageNoCSRFCheckNoCompression(t *testing.T) {
	w := goOnly(t, setup{})
	a := w.agent().signIn("admin")

	// A write without a token passes, with a session or without
	for name, who := range map[string]*agent{"signed in": a, "signed out": w.agent()} {
		res := who.do("POST", "/api/early?lang=de", `{"a":1}`, "Content-Type", "application/json")
		if res.status != 200 {
			t.Fatalf("%s: POST to the early stage without a token: %d %s", name, res.status, res.body)
		}
		if res.json(t)["lang"] != "" || res.cookie("betterdesk_lang") != "" {
			t.Errorf("%s: the early stage has a request language: %s, cookie %q", name, res.body, res.cookie("betterdesk_lang"))
		}
		if res.header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want step 1's", name, res.header.Get("Cache-Control"))
		}
	}
	// The early stage does not ask for JSON either
	if res := w.agent().do("POST", "/api/early", "a=1", "Content-Type", "text/plain"); res.status != 200 {
		t.Errorf("POST text/plain to the early stage: %d", res.status)
	}
	res := a.get("/api/early?pad=1", "Accept-Encoding", "gzip")
	if res.header.Get("Content-Encoding") != "" || len(res.body) < 2000 {
		t.Errorf("an early answer was compressed: %q", res.header.Get("Content-Encoding"))
	}
	if res.json(t)["user"] != float64(roleIDs["admin"]) || res.json(t)["loaded"] != true || res.header.Get("RateLimit-Limit") == "" {
		t.Errorf("the early stage must sit behind session, permissions and limiters: %.200s", res.body)
	}
}

func TestStep12PassOnLeavesTheRequestToTheLaterStage(t *testing.T) {
	w := goOnly(t, setup{})
	bearer := func(rw http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			kit.PassOn(rw, r)
			return
		}
		kit.JSON(rw, r, http.StatusOK, map[string]string{"stage": "early"})
	}
	w.s.Early().Get("/api/client-devices", bearer)
	w.s.Early().Post("/api/client-devices", bearer)
	w.s.Early().Get("/api/only-early", bearer)
	w.s.Router().Get("/api/client-devices", probe)
	w.s.Router().Post("/api/client-devices", probe)

	a := w.agent()
	if res := a.get("/api/client-devices", "Authorization", "Bearer x"); res.body != `{"stage":"early"}` {
		t.Errorf("with a bearer token: %s", res.body)
	}
	res := a.get("/api/client-devices?lang=de&pad=1", "Accept-Encoding", "gzip")
	if res.status != 200 || res.header.Get("Content-Encoding") != "gzip" || res.cookie("betterdesk_lang") == "" ||
		res.header.Get("Cache-Control") != "no-cache, no-store, must-revalidate" {
		t.Errorf("without one the console route must answer, with language, cache header and compression: %d %v", res.status, res.header)
	}
	// ... and with the CSRF check the early stage does not have
	if res := a.do("POST", "/api/client-devices", "{}", "Content-Type", "application/json"); res.status != 403 {
		t.Errorf("POST without a bearer token and without a CSRF token: %d, want 403", res.status)
	}
	if res := a.do("POST", "/api/client-devices", "{}", "Content-Type", "application/json", "Authorization", "Bearer x"); res.status != 200 {
		t.Errorf("POST with a bearer token: %d", res.status)
	}
	// Nobody takes it: the 404
	if res := a.get("/api/only-early"); res.status != 404 || res.body != `{"success":false,"error":"Not Found"}` {
		t.Errorf("passed on with no later route: %d %s", res.status, res.body)
	}
}

func TestStep14RequestLanguage(t *testing.T) {
	w := goOnly(t, setup{})
	a := w.agent()
	if res := a.get("/api/probe"); res.json(t)["lang"] != "en" {
		t.Errorf("default language: %s", res.body)
	}
	res := a.get("/api/probe?lang=de")
	if res.json(t)["lang"] != "de" || !strings.HasPrefix(res.cookie("betterdesk_lang"), "betterdesk_lang=de; Max-Age=31536000; Path=/; Expires=") {
		t.Errorf("?lang=de: %s, cookie %q", res.body, res.cookie("betterdesk_lang"))
	}
	if res := a.get("/api/probe", "Accept-Language", "pl,en;q=0.5"); res.json(t)["lang"] != "de" {
		t.Errorf("the cookie must win over Accept-Language: %s", res.body)
	}
	if res := w.agent().get("/api/probe", "Accept-Language", "pl,en;q=0.5"); res.json(t)["lang"] != "pl" {
		t.Errorf("Accept-Language: %s", res.body)
	}
	prefers := w.agent()
	prefers.get("/__test/signin/viewer?prefers=fr")
	if res := prefers.get("/api/probe", "Accept-Language", "pl"); res.json(t)["lang"] != "fr" {
		t.Errorf("the account's stored language: %s", res.body)
	}
	// A value that is not text counts as not given: the cookie decides
	res = a.get("/api/probe?lang[toString]=x")
	if res.status != 200 || res.json(t)["lang"] != "de" {
		t.Errorf("?lang[toString]=x: %d %s", res.status, res.body)
	}
	// The language is picked before the CSRF gate refuses
	res = w.agent().do("POST", "/api/probe?lang=de", "{}", "Content-Type", "application/json")
	if res.status != 403 || res.cookie("betterdesk_lang") == "" {
		t.Errorf("refused write with ?lang=de: %d, cookie %q", res.status, res.cookie("betterdesk_lang"))
	}
}

func TestStep15CacheHeaderIsOnRoutesAndNotOnFileNames(t *testing.T) {
	w := goOnly(t, setup{})
	w.s.Router().Get("/api/export.js", probe)
	a := w.agent()
	const noStore = "no-cache, no-store, must-revalidate"
	if got := a.get("/api/probe").header.Get("Cache-Control"); got != noStore {
		t.Errorf("API answer: Cache-Control = %q", got)
	}
	if got := a.get("/page/probe").header.Get("Cache-Control"); got != noStore {
		t.Errorf("page route: Cache-Control = %q", got)
	}
	if got := a.get("/api/nothing").header.Get("Cache-Control"); got != noStore {
		t.Errorf("404: Cache-Control = %q", got)
	}
	if got := a.do("POST", "/api/probe", "{}", "Content-Type", "application/json").header.Get("Cache-Control"); got != noStore {
		t.Errorf("CSRF refusal: Cache-Control = %q: the header is set before the gate", got)
	}
	if got := a.get("/api/export.js").header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("a path that ends like a file: Cache-Control = %q, want step 1's", got)
	}
}

func TestStep16CSRFGate(t *testing.T) {
	w := goOnly(t, setup{})
	w.s.Router().Post("/api/bd/thing", probe)
	const refusal = `{"success":false,"error":"Invalid CSRF token. Please refresh the page and try again. If you recently disabled SSL, clear your browser cookies for this site and reload."}`

	a := w.agent()
	res := a.do("POST", "/api/probe", `{"a":1}`, "Content-Type", "application/json")
	if res.status != 403 || res.body != refusal {
		t.Fatalf("write without a token: %d %s", res.status, res.body)
	}
	if res := a.post("/api/probe", `{"a":1}`); res.status != 200 {
		t.Errorf("write with the token: %d %s", res.status, res.body)
	}
	if res := a.do("POST", "/api/probe", `{"_csrf":"`+a.csrf()+`"}`, "Content-Type", "application/json"); res.status != 200 {
		t.Errorf("write with the token in the body: %d %s", res.status, res.body)
	}
	if res := a.do("POST", "/api/probe", `{}`, "Content-Type", "application/json", "X-CSRF-Token", strings.Repeat("0", 128)); res.status != 403 {
		t.Errorf("write with a wrong token: %d", res.status)
	}

	// Under /api/bd/ a request without a session is a device and passes; with one it is a browser
	if res := w.agent().do("POST", "/api/bd/thing", `{}`, "Content-Type", "application/json"); res.status != 200 {
		t.Errorf("/api/bd/ without a session: %d %s", res.status, res.body)
	}
	signedIn := w.agent().signIn("admin")
	if res := signedIn.do("POST", "/api/bd/thing", `{}`, "Content-Type", "application/json"); res.status != 403 || res.body != refusal {
		t.Errorf("/api/bd/ with a session and no token: %d %s", res.status, res.body)
	}
	if res := signedIn.post("/api/bd/thing", `{}`); res.status != 200 {
		t.Errorf("/api/bd/ with a session and the token: %d", res.status)
	}

	// A page write is refused in plain text when there is no build
	res = w.agent().do("POST", "/page/probe", "a=1", "Content-Type", "application/x-www-form-urlencoded", "Accept", "text/html")
	if res.status != 403 || !strings.HasPrefix(res.body, "Invalid or missing CSRF token. Please refresh the page and try again.") ||
		!strings.HasPrefix(res.header.Get("Content-Type"), "text/plain") {
		t.Errorf("page write without a token: %d %q %q", res.status, res.header.Get("Content-Type"), res.body)
	}
}

func TestStep17CompressionIsForAPIAnswersOfTheLaterStage(t *testing.T) {
	w := goOnly(t, setup{})
	a := w.agent()
	res := a.get("/api/probe?pad=1", "Accept-Encoding", "gzip, deflate, br")
	if res.header.Get("Content-Encoding") != "gzip" || res.header.Get("Vary") != "Accept-Encoding" {
		t.Errorf("large API answer: Content-Encoding %q, Vary %q", res.header.Get("Content-Encoding"), res.header.Get("Vary"))
	}
	if res := a.get("/api/probe", "Accept-Encoding", "gzip"); res.header.Get("Content-Encoding") != "" {
		t.Error("a small answer was compressed")
	}
	if res := a.get("/api/probe?pad=1"); res.header.Get("Content-Encoding") != "" {
		t.Error("compressed for a client that did not ask")
	}
	if res := a.get("/page/probe?pad=1", "Accept-Encoding", "gzip"); res.header.Get("Content-Encoding") != "" {
		t.Error("an answer outside /api/ was compressed")
	}
}

func TestStep18JSONRuleComesAfterTheCSRFGate(t *testing.T) {
	w := goOnly(t, setup{})
	a := w.agent()
	res := a.do("POST", "/api/probe", "a=1", "Content-Type", "text/plain", "X-CSRF-Token", a.csrf())
	if res.status != 415 || res.body != `{"success":false,"error":"Content-Type must be application/json"}` {
		t.Errorf("text/plain with a token: %d %s", res.status, res.body)
	}
	if res := a.do("POST", "/api/probe", "a=1", "Content-Type", "text/plain"); res.status != 403 {
		t.Errorf("text/plain without a token: %d, want the gate's 403 first", res.status)
	}
	// The rule covers paths without a route too, and not pages
	if res := a.do("POST", "/api/nothing", "a=1", "Content-Type", "text/plain", "X-CSRF-Token", a.csrf()); res.status != 415 {
		t.Errorf("no route: %d, want 415 before the 404", res.status)
	}
	if res := a.do("POST", "/page/probe", "a=1", "Content-Type", "application/x-www-form-urlencoded", "X-CSRF-Token", a.csrf()); res.status != 200 || !strings.Contains(res.body, `"body":{"a":"1"}`) {
		t.Errorf("form to a page route: %d %s", res.status, res.body)
	}
}

func TestStep18FirstRouteThatFitsAnswers(t *testing.T) {
	w := goOnly(t, setup{})
	name := func(n string) http.HandlerFunc {
		return func(rw http.ResponseWriter, r *http.Request) { kit.JSON(rw, r, 200, map[string]string{"route": n}) }
	}
	w.s.Router().Get("/api/things/{id}", name("by id"))
	w.s.Router().Get("/api/things/stats", name("stats"))
	if res := w.agent().get("/api/things/stats"); res.body != `{"route":"by id"}` {
		t.Errorf("got %s, want the route registered first", res.body)
	}
	// A parameter that does not decode goes to the error handler with 400
	res := w.agent().get("/api/things/%ff")
	if res.status != 400 || res.body != `{"success":false,"error":"Failed to decode param '%ff'"}` {
		t.Errorf("bad parameter: %d %s", res.status, res.body)
	}
}

func TestStep19WebAppAnswersWhatNoRouteDoes(t *testing.T) {
	w := goOnly(t, setup{build: true})
	res := w.agent().get("/login", "Accept", "text/html")
	if res.status != 200 || !strings.Contains(res.body, "console app") || !strings.HasPrefix(res.header.Get("Content-Security-Policy"), "default-src 'self'; script-src 'self' 'sha256-abc='") {
		t.Errorf("the sign-in page: %d, policy %q", res.status, res.header.Get("Content-Security-Policy"))
	}
	if res.header.Get("Cache-Control") != "no-store" {
		t.Errorf("page Cache-Control = %q", res.header.Get("Cache-Control"))
	}
	res = w.agent().get("/devices", "Accept", "text/html")
	if res.status != 302 || res.header.Get("Location") != "/login?return=%2Fdevices" {
		t.Errorf("a console page signed out: %d to %q", res.status, res.header.Get("Location"))
	}
	if res := w.agent().signIn("viewer").get("/devices", "Accept", "text/html"); res.status != 200 || !strings.Contains(res.body, "console app") {
		t.Errorf("a console page signed in: %d", res.status)
	}
	res = w.agent().get("/_ui/immutable/assets/app.t3st.css")
	if res.status != 200 || res.header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("a hashed file: %d %q", res.status, res.header.Get("Cache-Control"))
	}
	// Routes come first
	if res := w.agent().get("/page/probe"); !strings.Contains(res.body, `"route":"GET /page/probe"`) {
		t.Errorf("a route lost against the web app: %.100s", res.body)
	}
}

func TestStep20NotFound(t *testing.T) {
	plain := goOnly(t, setup{})
	res := plain.agent().get("/api/nothing")
	if res.status != 404 || res.body != `{"success":false,"error":"Not Found"}` {
		t.Errorf("API: %d %s", res.status, res.body)
	}
	res = plain.agent().get("/nothing", "Accept", "text/html")
	if res.status != 404 || res.body != "Not Found" || !strings.HasPrefix(res.header.Get("Content-Type"), "text/plain") {
		t.Errorf("page without a build: %d %q", res.status, res.body)
	}
	if res := plain.agent().do("DELETE", "/page/probe", "", "X-CSRF-Token", "x"); res.status != 403 {
		t.Errorf("a write to a path without that method still passes the gate first: %d", res.status)
	}

	built := goOnly(t, setup{build: true})
	res = built.agent().do("POST", "/nothing", "", "Accept", "text/html", "X-CSRF-Token", built.agent().csrf())
	if res.status != 403 {
		t.Errorf("POST with another browser's token: %d", res.status)
	}
	a := built.agent()
	res = a.do("POST", "/nothing", "", "Accept", "text/html", "X-CSRF-Token", a.csrf())
	if res.status != 404 || !strings.Contains(res.body, `<meta name="bd-error" content="404">`) {
		t.Errorf("page with a build: %d %.200s", res.status, res.body)
	}
}
