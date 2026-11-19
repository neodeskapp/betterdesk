package console

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// seen is one request the fake Node console received.
type seen struct {
	Server string `json:"server"`
	Method string `json:"method"`
	URI    string `json:"uri"`
	Host   string `json:"host"`
	XFF    string `json:"xff"`
	Proto  string `json:"proto"`
	XFHost string `json:"xfhost"`
	RealIP string `json:"realip"`
	Cookie string `json:"cookie"`
	Type   string `json:"type"`
	Body   string `json:"body"`
}

// fakeNode stands in for the Node console: it answers 418 with what it
// received, and remembers every request.
type fakeNode struct {
	*httptest.Server
	mu       sync.Mutex
	requests []seen
}

func (n *fakeNode) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.requests)
}

func newFakeNode(t *testing.T) *fakeNode {
	t.Helper()
	node := &fakeNode{}
	mux := http.NewServeMux()
	echoSocket := func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("upstream accept: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		typ, msg, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		_ = conn.Write(r.Context(), typ, append([]byte("node:"), msg...))
	}
	mux.HandleFunc("/ws/echo", echoSocket)
	mux.HandleFunc("/api/socket", echoSocket)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got := seen{
			Server: "node", Method: r.Method, URI: r.RequestURI, Host: r.Host,
			XFF: r.Header.Get("X-Forwarded-For"), Proto: r.Header.Get("X-Forwarded-Proto"),
			XFHost: r.Header.Get("X-Forwarded-Host"), RealIP: r.Header.Get("X-Real-IP"),
			Cookie: r.Header.Get("Cookie"), Type: r.Header.Get("Content-Type"), Body: string(body),
		}
		node.mu.Lock()
		node.requests = append(node.requests, got)
		node.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Node", "1")
		w.Header().Add("Set-Cookie", "node.sid=abc; Path=/; HttpOnly")
		w.WriteHeader(http.StatusTeapot)
		_ = json.NewEncoder(w).Encode(got)
	})
	node.Server = httptest.NewServer(mux)
	t.Cleanup(node.Close)
	return node
}

// setup says what kind of console a test wants.
type setup struct {
	// node puts a fake Node console behind the Go one.
	node bool
	// build serves the web app from the test build of package webui.
	build bool
	// public is the folder served as static files, "" for none.
	public     string
	trustProxy string
	production bool
	https      bool
	debug      bool
	env        map[string]string
	// langDir replaces the language files of the source tree.
	langDir string
	// db is the migrated database to run on; nil means a new SQLite one.
	db *store.DB
	// api is the API server's handler; nil answers 404 to everything.
	api http.Handler
	// configure changes the configuration before the console is built.
	configure func(*config.Config)
}

// world is a console server on a fresh database with a test client.
type world struct {
	t     *testing.T
	s     *Server
	db    *store.DB
	front *httptest.Server
	node  *fakeNode
}

const testSecret = "test-secret-0123456789abcdef0123456789abcdef"

// roleIDs gives every test account its users row id.
var roleIDs = map[string]int64{
	"admin": 1, "super_admin": 2, "server_admin": 3, "global_admin": 4,
	"operator": 5, "viewer": 6, "guest_issuer": 7, "pro": 8,
}

func newWorld(t *testing.T, o setup) *world {
	t.Helper()
	db := o.db
	if db == nil {
		db = storetest.Migrated(t)
	}
	for role, id := range roleIDs {
		if _, err := db.Exec(context.Background(), "INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, 'x', ?)", id, "e2e_"+role, role); err != nil {
			t.Fatalf("insert user: %v", err)
		}
	}
	data := t.TempDir()
	env := map[string]string{"SESSION_SECRET": testSecret}
	for k, v := range o.env {
		env[k] = v
	}
	trust, err := httpx.ParseTrustProxy(o.trustProxy)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Host:            "127.0.0.1",
		TrustProxy:      trust,
		DebugRouting:    o.debug,
		NodeEnv:         "development",
		Production:      o.production,
		DataDir:         data,
		UploadsDir:      filepath.Join(data, "uploads"),
		LangDir:         filepath.Join("static", "files", "lang"),
		WebUIDir:        filepath.Join(data, "no-build"),
		PublicDir:       o.public,
		HTTPS:           o.https,
		DefaultLanguage: "en",
		Getenv:          func(key string) string { return env[key] },
	}
	if o.langDir != "" {
		cfg.LangDir = o.langDir
	}
	if o.production {
		cfg.NodeEnv = "production"
	}
	if o.build {
		cfg.WebUIDir = filepath.Join("webui", "testdata", "site")
	}
	if o.configure != nil {
		o.configure(cfg)
	}
	apiHandler := o.api
	if apiHandler == nil {
		apiHandler = http.NotFoundHandler()
	}
	w := &world{t: t, db: db}
	if o.node {
		w.node = newFakeNode(t)
		cfg.NodeUpstream, _ = url.Parse(w.node.URL)
	}
	s, err := New(context.Background(), Options{Config: cfg, Store: db, API: apiHandler})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	w.s = s
	t.Cleanup(s.Stop)

	d := s.Deps()
	s.Router().Get("/__test/signin/{role}", func(rw http.ResponseWriter, r *http.Request) {
		role := r.PathValue("role")
		sess := kit.Session(r)
		if err := sess.Regenerate(); err != nil {
			t.Errorf("regenerate: %v", err)
		}
		user := session.User{ID: roleIDs[role], Username: "e2e_" + role, Role: role, PreferredLanguage: r.URL.Query().Get("prefers")}
		if _, err := sess.Establish(user, d.Sessions.ClientMeta(r, "")); err != nil {
			t.Errorf("establish: %v", err)
		}
		kit.OK(rw, r, nil)
	})
	s.Router().Get("/__test/token", func(rw http.ResponseWriter, r *http.Request) {
		kit.JSON(rw, r, http.StatusOK, map[string]string{"token": kit.CSRFToken(r)})
	})

	w.front = httptest.NewServer(s)
	t.Cleanup(w.front.Close)
	return w
}

// answer is what the console answered.
type answer struct {
	status int
	header http.Header
	body   string
}

// json decodes the body as an object.
func (a answer) json(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(a.body), &out); err != nil {
		t.Fatalf("answer is not a JSON object: %v\n%s", err, a.body)
	}
	return out
}

// cookie returns the Set-Cookie header of the answer for a cookie name,
// "" when the answer sets none.
func (a answer) cookie(name string) string {
	for _, line := range a.header.Values("Set-Cookie") {
		if strings.HasPrefix(line, name+"=") {
			return line
		}
	}
	return ""
}

// agent is a browser: it keeps cookies between requests.
type agent struct {
	w       *world
	cookies map[string]string
	token   string
	// always is a list of header name, value pairs sent with every request.
	always []string
}

func (w *world) agent() *agent { return &agent{w: w, cookies: map[string]string{}} }

var plainClient = &http.Client{
	Transport:     &http.Transport{DisableCompression: true},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	Timeout:       20 * time.Second,
}

// do sends a request. header is a list of name, value pairs.
func (a *agent) do(method, target, body string, header ...string) answer {
	a.w.t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, a.w.front.URL+target, reader)
	if err != nil {
		a.w.t.Fatal(err)
	}
	// "+Cookie" adds one cookie to the agent's for this request, in place
	// of the agent's cookie of that name
	header = append(append([]string{}, a.always...), header...)
	extra := map[string]string{}
	for i := 0; i+1 < len(header); i += 2 {
		switch {
		case strings.EqualFold(header[i], "Host"):
			req.Host = header[i+1]
		case header[i] == "+Cookie":
			name, _, _ := strings.Cut(header[i+1], "=")
			extra[name] = header[i+1]
		default:
			req.Header.Add(header[i], header[i+1])
		}
	}
	if req.Header.Get("Cookie") == "" {
		var pairs []string
		for name, value := range a.cookies {
			if _, replaced := extra[name]; !replaced {
				pairs = append(pairs, name+"="+value)
			}
		}
		for _, pair := range extra {
			pairs = append(pairs, pair)
		}
		if len(pairs) > 0 {
			req.Header.Set("Cookie", strings.Join(pairs, "; "))
		}
	}
	res, err := plainClient.Do(req)
	if err != nil {
		a.w.t.Fatalf("%s %s: %v", method, target, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	for _, c := range res.Cookies() {
		if c.MaxAge < 0 || (!c.Expires.IsZero() && c.Expires.Before(time.Now())) {
			delete(a.cookies, c.Name)
			continue
		}
		a.cookies[c.Name] = c.Value
	}
	return answer{status: res.StatusCode, header: res.Header, body: string(raw)}
}

func (a *agent) get(target string, header ...string) answer {
	a.w.t.Helper()
	return a.do("GET", target, "", header...)
}

// post sends a JSON body with the agent's CSRF token.
func (a *agent) post(target, body string, header ...string) answer {
	a.w.t.Helper()
	header = append([]string{"Content-Type", "application/json", "X-CSRF-Token", a.csrf()}, header...)
	return a.do("POST", target, body, header...)
}

// signIn signs the agent in as the test account of a role.
func (a *agent) signIn(role string, header ...string) *agent {
	a.w.t.Helper()
	if res := a.get("/__test/signin/"+role, header...); res.status != 200 {
		a.w.t.Fatalf("sign in as %s: %d %s", role, res.status, res.body)
	}
	return a
}

// csrf fetches the agent's CSRF token once.
func (a *agent) csrf() string {
	a.w.t.Helper()
	if a.token == "" {
		a.token, _ = a.get("/__test/token").json(a.w.t)["token"].(string)
		if a.token == "" {
			a.w.t.Fatal("no CSRF token")
		}
	}
	return a.token
}

// probe is a handler that reports what a route sees of the request.
func probe(w http.ResponseWriter, r *http.Request) {
	body, _ := kit.Body(r).MarshalJSON()
	out := map[string]any{
		"route":   r.Method + " " + r.URL.Path,
		"lang":    kit.Lang(r),
		"user":    kit.UserID(r),
		"section": kit.Access(r).Section,
		"loaded":  kit.Access(r).Loaded,
		"token":   kit.CSRFToken(r) != "",
		"ip":      kit.ClientIP(r),
		"body":    json.RawMessage(body),
	}
	if n := len(r.URL.Query().Get("pad")); n > 0 {
		out["pad"] = strings.Repeat("x", 2000)
	}
	kit.JSON(w, r, http.StatusOK, out)
}

// auditRows lists the audit log as "action|details|ip|user".
func (w *world) auditRows() []string {
	w.t.Helper()
	rows, err := w.db.Query(context.Background(), "SELECT action, COALESCE(details, ''), COALESCE(ip_address, ''), COALESCE(user_id, 0) FROM audit_log ORDER BY id")
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var action, details, ip string
		var user int64
		if err := rows.Scan(&action, &details, &ip, &user); err != nil {
			w.t.Fatal(err)
		}
		out = append(out, strings.Join([]string{action, details, ip, strconv.FormatInt(user, 10)}, "|"))
	}
	return out
}

// securityHeaders are the headers step 1 puts on every answer.
var securityHeaders = map[string]string{
	"Content-Security-Policy":           "default-src 'self';script-src 'self';script-src-attr 'none';style-src 'self' 'unsafe-inline';font-src 'self';img-src 'self' data: blob:;media-src 'self' blob:;connect-src 'self' ws:;frame-src 'none';object-src 'none';worker-src 'self' blob:;base-uri 'self';form-action 'self';frame-ancestors 'none'",
	"Cross-Origin-Resource-Policy":      "same-origin",
	"Referrer-Policy":                   "strict-origin-when-cross-origin",
	"X-Content-Type-Options":            "nosniff",
	"X-Dns-Prefetch-Control":            "off",
	"X-Download-Options":                "noopen",
	"X-Frame-Options":                   "DENY",
	"X-Permitted-Cross-Domain-Policies": "none",
	"X-Xss-Protection":                  "0",
	"Permissions-Policy":                "geolocation=(), microphone=(self), camera=(), payment=(), usb=(), accelerometer=(), gyroscope=(), magnetometer=()",
}

func wantSecurityHeaders(t *testing.T, a answer) {
	t.Helper()
	for name, want := range securityHeaders {
		if got := a.header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}
