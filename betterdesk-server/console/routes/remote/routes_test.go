package remote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/auditlog"
	"github.com/unitronix/betterdesk-server/console/authn"
	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/guestaccess"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/i18n"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/limits"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/remoterelay"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
	"github.com/unitronix/betterdesk-server/console/webui"
)

// account is the session of a vector's request.
type account struct {
	UserID int64 `json:"userId"`
	User   struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
		Role     string `json:"role"`
	} `json:"user"`
}

type request struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Session *account          `json:"session"`
	Type    string            `json:"type"`
	Body    *string           `json:"body"`
	Headers map[string]string `json:"headers"`
}

type apiAnswer struct {
	Status int    `json:"status"`
	Type   string `json:"type"`
	Body   string `json:"body"`
	Hang   bool   `json:"hang"`
}

// scene is what a vector scripts around the routes (testdata/nodeoracle/run.js).
type scene struct {
	Devices        map[string]json.RawMessage `json:"devices"`
	API            map[string]apiAnswer       `json:"api"`
	Relay          *relayScene                `json:"relay"`
	ServerKey      json.RawMessage            `json:"serverKey"`
	ConnectionMode json.RawMessage            `json:"connectionMode"`
	Branding       json.RawMessage            `json:"branding"`
	Mock           *string                    `json:"mock"`
}

type relayScene struct {
	Sessions []string                             `json:"sessions"`
	States   map[string]*remoterelay.SessionState `json:"states"`
}

type apiCall struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Body   string `json:"body"`
}

type relayCall struct {
	Call string `json:"call"`
	ID   string `json:"id,omitempty"`
}

type vector struct {
	Request      request     `json:"request"`
	World        scene       `json:"world"`
	Status       int         `json:"status"`
	Type         string      `json:"type"`
	CacheControl string      `json:"cacheControl"`
	Location     string      `json:"location"`
	Cookies      []string    `json:"cookies"`
	RateLimit    string      `json:"rateLimit"`
	Calls        []apiCall   `json:"calls"`
	RelayCalls   []relayCall `json:"relayCalls"`
	DeviceCalls  []string    `json:"deviceCalls"`
	Body         string      `json:"body"`
}

// thrown is a scripted value that makes the call fail.
func thrown(raw json.RawMessage) bool {
	var t struct {
		Throws *string `json:"throws"`
	}
	return json.Unmarshal(raw, &t) == nil && t.Throws != nil
}

// world runs the group as the console's pipeline does around it: bodies,
// the session, the early stage, the request language, the routes. The
// services around the routes are the scripted ones of the vector.
type world struct {
	t     *testing.T
	deps  *kit.Deps
	steps http.Handler
	mock  *mockScript

	mu          sync.Mutex
	scene       scene
	calls       []apiCall
	relayCalls  []relayCall
	deviceCalls []string
	issued      int
	// scope is the device scope of the world's accounts: whether the
	// account may see the device (nil for a device that was not found).
	// nil lets every account see every device, as in the vectors.
	scope func(user *session.User, device *goapi.Object) bool

	cookies map[string]string
}

// The token rows, the same as in testdata/nodeoracle/run.js.
var tokenRows = [][2]string{{"tok-device-a", "dev-a"}, {"tok-device-b", "dev-b"}, {"tok-numeric", "123456"}, {"tok-unbound", ""}, {"tok-seeded", "900100001"}}

// nodeLangDir is the console's language folder, which the oracle read as
// well (then in web-nodejs/lang).
func nodeLangDir(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "static", "files", "lang")
}

func newWorld(t *testing.T, db *store.DB, appVersion string, mockOn bool) *world {
	t.Helper()
	w := &world{t: t, cookies: map[string]string{}}
	if mockOn {
		w.mock = newMockScript(time.Unix(1700000000, 0))
	}
	ctx := context.Background()
	sessions, err := session.New(ctx, session.Options{DB: db, Secret: "remote-test-secret-0123456789abcdef0123456789"})
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	bundle := i18n.New(nodeLangDir(t), "en")
	bundle.Logf = func(string, ...any) {}
	bundle.Load()
	api := goapi.New(http.HandlerFunc(w.api))
	w.deps = &kit.Deps{
		Router:   kit.NewRouter(),
		Early:    kit.NewRouter(),
		Store:    db,
		Sessions: sessions,
		GoAPI:    api,
		Audit:    auditlog.New(db),
		Log:      kit.NewLogger("silent", true),
		Limits: limits.NewSet(limits.Options{
			ClientIP: httpx.SocketIP,
			UserKey:  func(*http.Request) string { return "" },
			Getenv:   func(string) string { return "100000000" },
		}),
		WebUI:       webui.New(webui.Options{}),
		GuestAccess: guestaccess.New(guestaccess.Options{GoAPI: api}),
		I18n:        &i18n.Handlers{Bundle: bundle},
	}
	service, err := authn.New(authn.Options{DB: db, GoAPI: goapi.New(http.NotFoundHandler())})
	if err != nil {
		t.Fatal(err)
	}
	w.deps.Authn = service
	if _, err := db.Exec(ctx, "INSERT INTO users (id, username, password_hash, role) VALUES (900, 'device-owner', 'x', 'operator')"); err != nil {
		t.Fatal(err)
	}
	for _, row := range tokenRows {
		if _, err := db.Exec(ctx, "INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at) VALUES (?, ?, 900, ?, '', '2999-01-01 00:00:00')",
			row[0], authn.HashAccessToken(row[0]), row[1]); err != nil {
			t.Fatal(err)
		}
	}
	src := w.sources(appVersion)
	registerAgentToken(w.deps, src)
	register(w.deps, src)
	w.deps.Router.Get("/__test/signin", signIn(t))
	w.steps = middleware.Bodies(middleware.BodyOptions{})(sessions.Middleware(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		r = kit.Begin(r, httpx.TrustProxy{}, bundle)
		if w.deps.Early.Serve(rw, r) {
			return
		}
		lang, err := w.deps.I18n.Language(rw, r)
		if err != nil {
			t.Fatalf("request language: %v", err)
		}
		kit.SetLanguage(r, lang)
		if !w.deps.Router.Serve(rw, r) {
			httpx.WriteBody(rw, r, http.StatusNotFound, "text/plain; charset=utf-8", []byte("no route"))
		}
	})))
	return w
}

// sources are the vector's scripted services.
func (w *world) sources(appVersion string) sources {
	return sources{
		relay: (*fakeRelay)(w),
		serverKey: func(context.Context) (string, error) {
			raw := w.current().ServerKey
			if len(raw) == 0 {
				return "server-key-base64=", nil
			}
			if thrown(raw) {
				return "", errors.New("no key")
			}
			var key string
			_ = json.Unmarshal(raw, &key)
			return key, nil
		},
		connectionMode: func(context.Context) (serverinfo.ConnectionMode, error) {
			raw := w.current().ConnectionMode
			if len(raw) == 0 {
				return serverinfo.ConnectionMode{Mode: "p2p_first", P2PFallbackMs: 2000, SameNATRelay: true}, nil
			}
			if thrown(raw) {
				return serverinfo.ConnectionMode{}, errors.New("unit unreadable")
			}
			var mode struct {
				Mode                    string  `json:"mode"`
				P2PFallbackMs           float64 `json:"p2p_fallback_ms"`
				SameNATRelay            bool    `json:"same_nat_relay"`
				AllowSharedNATInitiator bool    `json:"allow_shared_nat_initiator"`
			}
			if err := json.Unmarshal(raw, &mode); err != nil {
				w.t.Fatalf("connection mode %s: %v", raw, err)
			}
			return serverinfo.ConnectionMode{Mode: mode.Mode, P2PFallbackMs: serverinfo.Number(mode.P2PFallbackMs), SameNATRelay: mode.SameNATRelay, AllowSharedNATInitiator: mode.AllowSharedNATInitiator}, nil
		},
		branding: func(context.Context) *branding.Object {
			raw := w.current().Branding
			if len(raw) == 0 {
				return branding.DefaultBranding()
			}
			if thrown(raw) {
				return branding.NewObject()
			}
			value, err := branding.ParseJSON(raw)
			if err != nil {
				w.t.Fatalf("branding %s: %v", raw, err)
			}
			if o, ok := value.(*branding.Object); ok {
				return o
			}
			return branding.NewObject()
		},
		languages:  w.deps.I18n.Bundle.AvailableList,
		appVersion: appVersion,
		mock:       w.mock,
		canAccess: func(_ context.Context, user *session.User, device *goapi.Object) (bool, error) {
			w.mu.Lock()
			scope := w.scope
			w.mu.Unlock()
			return scope == nil || scope(user, device), nil
		},
	}
}

func (w *world) current() scene {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.scene
}

// api is the scripted API server: an answer per method and path.
func (w *world) api(rw http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	w.mu.Lock()
	w.calls = append(w.calls, apiCall{Method: r.Method, URL: r.RequestURI, Body: string(body)})
	path, _, _ := strings.Cut(r.RequestURI, "?")
	answer, ok := w.scene.API[r.Method+" "+path]
	w.mu.Unlock()
	if !ok {
		answer = apiAnswer{Status: http.StatusNotFound, Type: "application/json", Body: `{"error":"not found"}`}
	}
	if answer.Hang {
		panic(http.ErrAbortHandler)
	}
	if answer.Type != "" {
		rw.Header().Set("Content-Type", answer.Type)
	}
	rw.WriteHeader(answer.Status)
	_, _ = io.WriteString(rw, answer.Body)
}

// fakeRelay is the remote relay of the vector.
type fakeRelay world

func (f *fakeRelay) w() *world { return (*world)(f) }

func (f *fakeRelay) IssueRemoteAgentToken(deviceID string) (string, int) {
	w := f.w()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.issued++
	w.relayCalls = append(w.relayCalls, relayCall{Call: "issue", ID: deviceID})
	sum := sha256.Sum256([]byte("token-" + strconv.Itoa(w.issued)))
	return hex.EncodeToString(sum[:]), 60
}

func (f *fakeRelay) SessionState(deviceID string) *remoterelay.SessionState {
	w := f.w()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.relayCalls = append(w.relayCalls, relayCall{Call: "state", ID: deviceID})
	if w.scene.Relay == nil {
		return nil
	}
	return w.scene.Relay.States[deviceID]
}

func (f *fakeRelay) ActiveSessions() []string {
	w := f.w()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.relayCalls = append(w.relayCalls, relayCall{Call: "sessions"})
	if w.scene.Relay == nil || w.scene.Relay.Sessions == nil {
		return []string{}
	}
	return w.scene.Relay.Sessions
}

func (f *fakeRelay) Device(_ context.Context, id string) (*goapi.Object, error) {
	w := f.w()
	w.mu.Lock()
	w.deviceCalls = append(w.deviceCalls, id)
	raw, ok := w.scene.Devices[id]
	w.mu.Unlock()
	if !ok {
		return nil, nil
	}
	if thrown(raw) {
		return nil, errors.New("database is locked")
	}
	value, err := goapi.Parse(raw)
	if err != nil {
		return nil, err
	}
	o, _ := value.(*goapi.Object)
	return o, nil
}

// cookie signs the account in and returns its session cookie. A pro
// account is signed in for every request: the guards end its session.
func (w *world) cookie(a *account) string {
	key := strconv.FormatInt(a.UserID, 10) + "|" + a.User.Username + "|" + a.User.Role
	if pair, ok := w.cookies[key]; ok {
		return pair
	}
	q := url.Values{"id": {strconv.FormatInt(a.UserID, 10)}, "username": {a.User.Username}, "role": {a.User.Role}}
	rec := httptest.NewRecorder()
	w.steps.ServeHTTP(rec, httptest.NewRequest("GET", "/__test/signin?"+q.Encode(), nil))
	pair, _, _ := strings.Cut(rec.Header().Get("Set-Cookie"), ";")
	if pair == "" {
		w.t.Fatalf("signing in as %s set no cookie", a.User.Username)
	}
	if a.User.Role != "pro" {
		w.cookies[key] = pair
	}
	return pair
}

// do sends a vector's request in the vector's world.
func (w *world) do(v *vector) *httptest.ResponseRecorder {
	w.mu.Lock()
	w.scene, w.calls, w.relayCalls, w.deviceCalls = v.World, nil, nil, nil
	w.mu.Unlock()
	var body io.Reader
	if v.Request.Body != nil {
		body = strings.NewReader(*v.Request.Body)
	}
	method := v.Request.Method
	if method == "" {
		method = http.MethodGet
	}
	u, err := url.ParseRequestURI(v.Request.Path)
	if err != nil {
		w.t.Fatalf("the path %q of a vector is no request target: %v", v.Request.Path, err)
	}
	r := httptest.NewRequest(method, "/", body)
	r.URL, r.RequestURI = u, v.Request.Path
	if v.Request.Type != "" {
		r.Header.Set("Content-Type", v.Request.Type)
	}
	cookies := []string{}
	for name, value := range v.Request.Headers {
		if strings.EqualFold(name, "Cookie") {
			cookies = append(cookies, value)
			continue
		}
		r.Header.Set(name, value)
	}
	if v.Request.Session != nil {
		cookies = append(cookies, w.cookie(v.Request.Session))
	}
	if len(cookies) > 0 {
		r.Header.Set("Cookie", strings.Join(cookies, "; "))
	}
	rec := httptest.NewRecorder()
	w.steps.ServeHTTP(rec, r)
	return rec
}

func jsTrim(s string) string { return jsbind.Trim(s) }

var (
	cookieExpires = regexp.MustCompile(`Expires=[A-Za-z]{3}, [0-9]{2} [A-Za-z]{3} [0-9]{4} [0-9:]{8} GMT`)
	listIndex     = regexp.MustCompile(`\[[0-9]+\]`)
)

// groupCookies are the cookies the group and the language step set, with
// the day a cookie ends left out; the session cookie of the test belongs
// to neither.
func groupCookies(header []string) string {
	var out []string
	for _, value := range header {
		if strings.HasPrefix(value, "bd.sid=") || strings.HasPrefix(value, "betterdesk.sid=") {
			continue
		}
		out = append(out, cookieExpires.ReplaceAllString(value, "Expires=<date>"))
	}
	return strings.Join(out, " | ")
}

// describeCalls writes the calls the API server got, with the query of
// each as the API server reads it: the Go client escapes a comma where the
// Node client did not, which changes nothing for the server.
func describeCalls(calls []apiCall) string {
	out := make([]apiCall, len(calls))
	for i, call := range calls {
		out[i] = call
		path, query, hasQuery := strings.Cut(call.URL, "?")
		if !hasQuery {
			continue
		}
		var pairs []string
		for _, pair := range strings.Split(query, "&") {
			name, value, _ := strings.Cut(pair, "=")
			name, _ = url.QueryUnescape(name)
			value, _ = url.QueryUnescape(value)
			pairs = append(pairs, strconv.Quote(listIndex.ReplaceAllString(name, "[]"))+"="+strconv.Quote(value))
		}
		out[i].URL = path + "?" + strings.Join(pairs, "&")
	}
	text, _ := json.Marshal(out)
	return string(text)
}

func loadVectors(t *testing.T) []vector {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors []vector
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) < 1000 {
		t.Fatalf("%d vectors, the file should hold more than 1000", len(vectors))
	}
	return vectors
}

// appVersionOf is the product version the Node console answered with.
func appVersionOf(t *testing.T, vectors []vector) string {
	for _, v := range vectors {
		var page struct {
			AppVersion *string `json:"appVersion"`
		}
		if v.Request.Path == "/api/remote/login-info" && json.Unmarshal([]byte(v.Body), &page) == nil && page.AppVersion != nil {
			return *page.AppVersion
		}
	}
	t.Fatal("no vector names the product version")
	return ""
}

// TestNodeVectors replays what routes/remote.routes.js and the agent token
// route of routes/bd-api.routes.js answered (testdata/nodeoracle/run.js):
// status, Content-Type, Cache-Control, Location, the cookies, the rate
// limit and the body, and what they asked the API server, the device
// table and the remote relay, on SQLite and PostgreSQL. A vector whose
// answer a bug fix changed on purpose is held to another vector's answer
// (answeredAs) or to the answer it gets since the fix (refusedSessions).
func TestNodeVectors(t *testing.T) {
	vectors := loadVectors(t)
	version := appVersionOf(t, vectors)
	twins := answeredAs(t, vectors)
	refused := refusedSessions(t, vectors)
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db, version, false)
		on := newWorld(t, storetest.Migrated(t), version, true)
		failures := 0
		for i := range vectors {
			v := &vectors[i]
			if dotSegment(v.Request.Path) {
				// No Go route takes a path with a dot segment (DECISIONS A24)
				if rec := w.do(v); rec.Code != http.StatusNotFound {
					t.Errorf("vector %d: %s answered %d, want 404 for a dot segment", i, v.Request.Path, rec.Code)
				}
				continue
			}
			target := w
			if v.World.Mock != nil && jsTrim(*v.World.Mock) == "1" {
				target = on
			}
			rec := target.do(v)
			expected := v
			if j, ok := twins[i]; ok {
				expected = &vectors[j]
				// The relay numbers the tokens it issues: count those Node
				// had it issue for this vector, so later tokens keep theirs.
				target.mu.Lock()
				target.issued += issues(v) - issues(expected)
				target.mu.Unlock()
			} else if fixed, ok := refused[i]; ok {
				expected = &fixed
			}
			if problems := compare(target, expected, rec); len(problems) > 0 {
				failures++
				if failures <= 25 {
					t.Errorf("vector %d: %s %s:\n  %s", i, v.Request.Method, v.Request.Path, strings.Join(problems, "\n  "))
				}
			}
		}
		if failures > 25 {
			t.Errorf("%d of %d vectors differ; the first 25 are shown", failures, len(vectors))
		}
	})
}

func compare(w *world, v *vector, rec *httptest.ResponseRecorder) []string {
	var problems []string
	if rec.Code != v.Status {
		problems = append(problems, "status "+strconv.Itoa(rec.Code)+", want "+strconv.Itoa(v.Status))
	}
	for _, h := range []struct{ name, want string }{
		{"Content-Type", v.Type}, {"Cache-Control", v.CacheControl}, {"Location", v.Location}, {"RateLimit-Limit", v.RateLimit},
	} {
		if got := rec.Header().Get(h.name); got != h.want {
			problems = append(problems, h.name+" "+strconv.Quote(got)+", want "+strconv.Quote(h.want))
		}
	}
	if got, want := groupCookies(rec.Header().Values("Set-Cookie")), groupCookies(v.Cookies); got != want {
		problems = append(problems, "Set-Cookie "+strconv.Quote(got)+"\n      want "+strconv.Quote(want))
	}
	if got := rec.Body.String(); got != v.Body {
		problems = append(problems, "body "+strconv.Quote(got)+"\n      want "+strconv.Quote(v.Body))
	}
	w.mu.Lock()
	calls, relayCalls, deviceCalls := w.calls, w.relayCalls, w.deviceCalls
	w.mu.Unlock()
	if got, want := describeCalls(calls), describeCalls(v.Calls); got != want {
		problems = append(problems, "API calls "+got+"\n      want "+want)
	}
	if got, want := describeJSON(relayCalls), describeJSON(v.RelayCalls); got != want {
		problems = append(problems, "relay calls "+got+", want "+want)
	}
	if got, want := describeJSON(deviceCalls), describeJSON(v.DeviceCalls); got != want {
		problems = append(problems, "device lookups "+got+", want "+want)
	}
	return problems
}

// dotSegment reports a path with a segment that is . or ..
func dotSegment(target string) bool {
	path, _, _ := strings.Cut(target, "?")
	for _, part := range strings.Split(path, "/") {
		if part == "." || part == ".." {
			return true
		}
	}
	return false
}

func describeJSON(v any) string {
	text, _ := json.Marshal(v)
	if string(text) == "null" {
		return "[]"
	}
	return string(text)
}

// signIn is the test route that starts a session for the account in its
// query.
func signIn(t *testing.T) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		id, _ := strconv.ParseInt(q.Get("id"), 10, 64)
		s := kit.Session(r)
		if err := s.Regenerate(); err != nil {
			t.Errorf("regenerate: %v", err)
		}
		if _, err := s.Establish(session.User{ID: id, Username: q.Get("username"), Role: q.Get("role")}, session.Meta{}); err != nil {
			t.Errorf("establish: %v", err)
		}
	}
}
