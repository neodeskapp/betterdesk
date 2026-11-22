// Package replay runs route groups in tests the way the console runs
// them, behind the whole request pipeline, and replays what the Node
// console answered to the same requests on the same rows (the fixed
// recordings a group keeps in testdata, made while Node still ran).
package replay

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console"
	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/store"
	serverdb "github.com/unitronix/betterdesk-server/db"
)

// Users are the accounts a test can act as, by role. The recordings use
// the same ids and names.
var Users = map[string]session.User{
	"admin":        {ID: 1, Username: "e2e_admin", Role: "admin"},
	"super_admin":  {ID: 2, Username: "e2e_super_admin", Role: "super_admin"},
	"server_admin": {ID: 3, Username: "e2e_server_admin", Role: "server_admin"},
	"global_admin": {ID: 4, Username: "e2e_global_admin", Role: "global_admin"},
	"operator":     {ID: 5, Username: "e2e_operator", Role: "operator"},
	"viewer":       {ID: 6, Username: "e2e_viewer", Role: "viewer"},
	"guest_issuer": {ID: 7, Username: "e2e_guest_issuer", Role: "guest_issuer"},
	"pro":          {ID: 8, Username: "e2e_pro", Role: "pro"},
}

// World is a console on a test database, with one browser per role.
type World struct {
	T      *testing.T
	DB     *store.DB
	Server *console.Server
	Config *config.Config

	// Vars are texts put in place of marks in the statement parameters of
	// a recording: "<UPLOADS>" for the folder a group writes files to.
	Vars map[string]string

	browsers map[string]*browser
}

type browser struct {
	cookies map[string]string
	token   string
}

// NewWorld builds a console with every moved route group on db, which has
// the console's schema already. The rate limits are as high as on the
// test instances. configure may change the configuration before the
// console is built.
func NewWorld(t *testing.T, db *store.DB, configure func(cfg *config.Config)) *World {
	t.Helper()
	return NewWorldWithRoles(t, db, configure, nil)
}

// RoleOverrides are permissions taken from or given to roles, as the
// server's role_permissions table holds them: role, then permission, then
// whether the role has it.
type RoleOverrides map[string]map[string]bool

// ListRolePermissions implements access.OverrideStore.
func (o RoleOverrides) ListRolePermissions(role string) ([]*serverdb.RolePermission, error) {
	var out []*serverdb.RolePermission
	for permission, granted := range o[role] {
		out = append(out, &serverdb.RolePermission{Role: role, Permission: permission, Granted: granted})
	}
	return out, nil
}

// SetRolePermission implements access.OverrideStore.
func (o RoleOverrides) SetRolePermission(role, permission string, granted bool) error {
	if o[role] == nil {
		o[role] = map[string]bool{}
	}
	o[role][permission] = granted
	return nil
}

// DeleteRolePermission implements access.OverrideStore.
func (o RoleOverrides) DeleteRolePermission(role, permission string) error {
	delete(o[role], permission)
	return nil
}

// NewWorldWithRoles is NewWorld for a console whose roles differ from the
// built-in ones by the overrides.
func NewWorldWithRoles(t *testing.T, db *store.DB, configure func(cfg *config.Config), roles RoleOverrides) *World {
	t.Helper()
	return NewWorldWithAPI(t, db, configure, roles, nil)
}

// NewWorldWithAPI is NewWorldWithRoles for a console whose calls into the
// API server (package goapi) reach api. With nil every call is answered
// 404, as in NewWorld.
func NewWorldWithAPI(t *testing.T, db *store.DB, configure func(cfg *config.Config), roles RoleOverrides, api http.Handler) *World {
	t.Helper()
	if api == nil {
		api = http.NotFoundHandler()
	}
	ctx := context.Background()
	for _, user := range Users {
		if _, err := db.Exec(ctx, "INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, 'x', ?)", user.ID, user.Username, user.Role); err != nil {
			t.Fatalf("insert user %s: %v", user.Username, err)
		}
	}
	data := t.TempDir()
	env := map[string]string{
		"SESSION_SECRET": "replay-secret-0123456789abcdef0123456789abcdef",
		"RATE_LIMIT_MAX": "1000000", "PANEL_READ_RATE_LIMIT_MAX": "1000000", "WIDGET_RATE_LIMIT_MAX": "1000000",
		"LOGIN_RATE_LIMIT_MAX": "1000000", "UPLOAD_RATE_LIMIT_MAX": "1000000", "FILE_ACCESS_RATE_LIMIT_MAX": "1000000",
	}
	consoleDir := filepath.Join(data, "console")
	cfg := &config.Config{
		Host:            "127.0.0.1",
		NodeEnv:         "production",
		Production:      true,
		ConsoleDir:      consoleDir,
		DataDir:         data,
		UploadsDir:      filepath.Join(data, "uploads"),
		LangDir:         langDir(t),
		WebUIDir:        filepath.Join(data, "no-build"),
		DefaultLanguage: "en",
		LogLevel:        "silent",
		Getenv:          func(key string) string { return env[key] },
	}
	if configure != nil {
		configure(cfg)
	}
	options := console.Options{Config: cfg, Store: db, API: api}
	if roles != nil {
		options.Roles = roles
	}
	s, err := console.New(ctx, options)
	if err != nil {
		t.Fatalf("build the console: %v", err)
	}
	t.Cleanup(s.Stop)
	d := s.Deps()
	s.Router().Get("/__test/signin/{role}", func(w http.ResponseWriter, r *http.Request) {
		sess := kit.Session(r)
		if err := sess.Regenerate(); err != nil {
			t.Errorf("regenerate the session: %v", err)
		}
		if _, err := sess.Establish(Users[r.PathValue("role")], d.Sessions.ClientMeta(r, "")); err != nil {
			t.Errorf("establish the session: %v", err)
		}
		kit.OK(w, r, nil)
	})
	s.Router().Get("/__test/token", func(w http.ResponseWriter, r *http.Request) {
		kit.JSON(w, r, http.StatusOK, map[string]string{"token": kit.CSRFToken(r)})
	})
	return &World{T: t, DB: db, Server: s, Config: cfg, Vars: map[string]string{}, browsers: map[string]*browser{}}
}

// langDir finds the console's language files from the test's folder. A
// copy of the server without them gets an empty folder: the console then
// has no texts but its keys.
func langDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(dir, "console", "static", "files", "lang")
		if _, err := os.Stat(filepath.Join(candidate, "en.json")); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return t.TempDir()
		}
		dir = parent
	}
}

// Answer is what the console answered.
type Answer struct {
	Status int
	Header http.Header
	Body   string
}

// Request is one request of a test.
type Request struct {
	// Role is the account that sends it; "" is nobody.
	Role   string
	Method string
	// Path is the request target as it goes on the wire, query included.
	Path string
	// Body is sent as JSON unless Headers names another Content-Type.
	// nil sends no body.
	Body    *string
	Headers map[string]string
	// NoCSRF leaves out the CSRF token of a write.
	NoCSRF bool
	// Reader replaces Body with a stream, for bodies a test does not want
	// to hold in memory.
	Reader        io.Reader
	ContentLength int64
}

// Text returns a pointer to s, for Request.Body.
func Text(s string) *string { return &s }

// Do sends a request through the console's pipeline. The role is signed
// in on first use, and writes carry the CSRF token unless NoCSRF is set.
func (w *World) Do(req Request) Answer {
	w.T.Helper()
	b := w.browser(req.Role)
	if req.Method == "" {
		req.Method = http.MethodGet
	}
	headers := map[string]string{"Accept": "application/json"}
	safe := req.Method == http.MethodGet || req.Method == http.MethodHead || req.Method == http.MethodOptions
	if !safe && !req.NoCSRF {
		if b.token == "" {
			var out struct {
				Token string `json:"token"`
			}
			answer := w.send(b, http.MethodGet, "/__test/token", nil, 0, nil)
			if err := json.Unmarshal([]byte(answer.Body), &out); err != nil || out.Token == "" {
				w.T.Fatalf("no CSRF token: %d %s", answer.Status, answer.Body)
			}
			b.token = out.Token
		}
		headers["X-CSRF-Token"] = b.token
	}
	var body io.Reader
	length := int64(0)
	switch {
	case req.Reader != nil:
		body, length = req.Reader, req.ContentLength
		headers["Content-Type"] = "application/json"
	case req.Body != nil:
		body, length = strings.NewReader(*req.Body), int64(len(*req.Body))
		headers["Content-Type"] = "application/json"
	}
	for name, value := range req.Headers {
		headers[name] = value
	}
	return w.send(b, req.Method, req.Path, body, length, headers)
}

func (w *World) browser(role string) *browser {
	w.T.Helper()
	if b := w.browsers[role]; b != nil {
		return b
	}
	b := &browser{cookies: map[string]string{}}
	w.browsers[role] = b
	if role != "" {
		if _, known := Users[role]; !known {
			w.T.Fatalf("no test account for role %q", role)
		}
		if answer := w.send(b, http.MethodGet, "/__test/signin/"+role, nil, 0, nil); answer.Status != http.StatusOK {
			w.T.Fatalf("sign in as %s: %d %s", role, answer.Status, answer.Body)
		}
	}
	return b
}

func (w *World) send(b *browser, method, target string, body io.Reader, length int64, headers map[string]string) Answer {
	w.T.Helper()
	req, err := newRequest(method, target, body)
	if err != nil {
		w.T.Fatalf("request target %q: %v", target, err)
	}
	if body != nil {
		req.ContentLength = length
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	var pairs []string
	for name, value := range b.cookies {
		pairs = append(pairs, name+"="+value)
	}
	if len(pairs) > 0 {
		req.Header.Set("Cookie", strings.Join(pairs, "; "))
	}
	rec := httptest.NewRecorder()
	w.Server.ServeHTTP(rec, req)
	res := rec.Result()
	for _, c := range res.Cookies() {
		if c.MaxAge < 0 || c.Value == "" {
			delete(b.cookies, c.Name)
			continue
		}
		b.cookies[c.Name] = c.Value
	}
	return Answer{Status: res.StatusCode, Header: res.Header, Body: rec.Body.String()}
}

// newRequest is the request a client sends with this target, as the
// console gets it over a connection: the target is read as net/http reads
// a request line, so dot segments and a "#" stay in the path the router
// reads.
func newRequest(method, target string, body io.Reader) (*http.Request, error) {
	parsed, err := url.ParseRequestURI(target)
	if err != nil {
		return nil, err
	}
	req := httptest.NewRequest(method, "http://console.test/", body)
	req.RequestURI, req.URL = target, parsed
	req.RemoteAddr = "203.0.113.7:40000"
	return req, nil
}

// Recording is a recorded file of a group's testdata: rows to start from,
// and requests with the Node console's answers.
type Recording struct {
	Dialect string `json:"dialect"`
	Seed    []Seed `json:"seed"`
	Steps   []Step `json:"steps"`
}

// Seed is a statement with ? placeholders and its parameters.
type Seed struct {
	SQL    string
	Params []any
}

// UnmarshalJSON reads the pair [sql, params].
func (s *Seed) UnmarshalJSON(data []byte) error {
	pair := []any{&s.SQL, &s.Params}
	return json.Unmarshal(data, &pair)
}

// Step is one request of a recording with Node's answer, or a statement
// that was run between two requests.
type Step struct {
	Name    string            `json:"name"`
	Role    string            `json:"role"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Body    *string           `json:"body"`
	Headers map[string]string `json:"headers"`
	SQL     string            `json:"sql"`
	Params  []any             `json:"params"`

	Status int    `json:"status"`
	Answer string `json:"answer"`
	// Rows is what a SELECT statement returned, as JSON.
	Rows string `json:"rows"`
	// Files is a list of file names a script recorded after its last request.
	Files []string `json:"files"`
	// Extra holds what a group's script recorded beside status and body.
	Extra map[string]json.RawMessage `json:"extra"`
}

// Load reads the recording for the database's dialect:
// testdata/<name>_sqlite.json or testdata/<name>_postgres.json.
func Load(t *testing.T, db *store.DB, name string) Recording {
	t.Helper()
	file := filepath.Join("testdata", name+"_"+db.Dialect().String()+".json")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read the recording: %v", err)
	}
	var rec Recording
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	if rec.Dialect != db.Dialect().String() {
		t.Fatalf("%s was recorded on %s", file, rec.Dialect)
	}
	if strings.HasPrefix(name, deviceRecording) {
		for i, seed := range rec.Seed {
			if strings.HasPrefix(seed.SQL, tokenSeed) && len(seed.Params) > 1 && seed.Params[1] == nil {
				if token, ok := seed.Params[0].(string); ok {
					rec.Seed[i].Params = slices.Clone(seed.Params)
					rec.Seed[i].Params[1] = store.HashAccessToken(token)
				}
			}
		}
		rec.Seed = append(rec.Seed, Seed{SQL: "UPDATE access_tokens SET token_kind = 'device'"})
	}
	return rec
}

// deviceRecording starts the name of a recording of the device routes.
// Node took every live token with a client id as that device's token, and
// such a recording's tokens stand for device tokens: Load makes every
// token its seed wrote a device token, which the console's device guards
// take (BUGS RV1). A token the seed wrote without its hash, as an older
// console wrote them, gets its hash, as the console's start gives it one:
// the console finds tokens by their hash only (BUGS RV128).
const deviceRecording = "node_device"

// tokenSeed starts a seed statement that writes a token and its hash.
const tokenSeed = "INSERT INTO access_tokens (token, token_hash,"

// args are the parameters of a statement of a recording. JSON has one
// kind of number, so a whole number is bound as an integer; the marks of
// Vars are filled in.
func (w *World) args(params []any) []any {
	args := make([]any, len(params))
	for i, p := range params {
		args[i] = p
		switch v := p.(type) {
		case float64:
			if v == float64(int64(v)) {
				args[i] = int64(v)
			}
		case string:
			for mark, text := range w.Vars {
				v = strings.ReplaceAll(v, mark, text)
			}
			args[i] = v
		}
	}
	return args
}

// Exec runs a statement of a recording.
func (w *World) Exec(query string, params []any) {
	w.T.Helper()
	args := w.args(params)
	if _, err := w.DB.Exec(context.Background(), query, args...); err != nil {
		w.T.Fatalf("%s: %v", query, err)
	}
}

// The times a database fills in itself are the time of the run. They
// are compared by their form: text of the second on SQLite, ISO with
// milliseconds on PostgreSQL. Times a recording sets itself lie before
// 2020 and are compared as they are.
var (
	nowSQLite   = regexp.MustCompile(`"20[2-9][0-9]-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}"`)
	nowPostgres = regexp.MustCompile(`"20[2-9][0-9]-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{3}Z"`)
)

// Normalize replaces the times of the run in a JSON text by a mark for
// their form.
func Normalize(body string) string {
	body = nowSQLite.ReplaceAllString(body, `"<now, SQLite form>"`)
	return nowPostgres.ReplaceAllString(body, `"<now, ISO form>"`)
}

// Check is called for every replayed request with Node's answer and the
// Go console's, both normalized. It reports whether it has compared them
// itself; Run compares status and body when it returns false or is nil.
type Check func(t *testing.T, step Step, got Answer) bool

// Run replays a recording: the seed, then every step in order. Each
// answer must have the status Node gave and, after Normalize, the same
// body, byte for byte.
func (w *World) Run(rec Recording, check Check) {
	w.T.Helper()
	for _, seed := range rec.Seed {
		w.Exec(seed.SQL, seed.Params)
	}
	failed := 0
	for i, step := range rec.Steps {
		if step.SQL != "" {
			if !strings.HasPrefix(step.SQL, "SELECT") {
				w.Exec(step.SQL, step.Params)
			} else if have := w.Rows(step.SQL, step.Params); have != step.Rows {
				w.T.Errorf("step %d %q: %s\n   node: %s\n     go: %s", i, step.Name, step.SQL, step.Rows, have)
			}
			continue
		}
		if step.Method == "" {
			if check == nil || !check(w.T, step, Answer{}) {
				w.T.Errorf("step %d %q is no request and no statement, and nothing checked it", i, step.Name)
			}
			continue
		}
		got := w.Do(Request{Role: step.Role, Method: step.Method, Path: step.Path, Body: step.Body, Headers: step.Headers})
		if check != nil && check(w.T, step, got) {
			continue
		}
		want, have := Normalize(step.Answer), Normalize(got.Body)
		if got.Status == step.Status && want == have {
			continue
		}
		failed++
		if failed <= 25 {
			body := ""
			if step.Body != nil {
				body = " " + *step.Body
			}
			w.T.Errorf("step %d %q: %s %s%s as %q\n   node: %d %s\n     go: %d %s", i, step.Name, step.Method, step.Path, body, step.Role, step.Status, want, got.Status, have)
		}
	}
	if failed > 25 {
		w.T.Errorf("%d more steps differ", failed-25)
	}
}

// Rows runs a SELECT of a recording and returns its rows as the JSON the
// Node drivers' rows had: a list of objects with the columns in order.
// It reads whole numbers, texts, flags and NULL; select no times with it.
func (w *World) Rows(query string, params []any) string {
	w.T.Helper()
	rows, err := w.DB.Query(context.Background(), query, w.args(params)...)
	if err != nil {
		w.T.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		w.T.Fatal(err)
	}
	var out bytes.Buffer
	out.WriteByte('[')
	for n := 0; rows.Next(); n++ {
		cells := make([]jsbind.Cell, len(columns))
		dest := make([]any, len(columns))
		for i := range cells {
			dest[i] = &cells[i]
		}
		if err := rows.Scan(dest...); err != nil {
			w.T.Fatalf("%s: %v", query, err)
		}
		if n > 0 {
			out.WriteByte(',')
		}
		out.WriteByte('{')
		for i, column := range columns {
			if i > 0 {
				out.WriteByte(',')
			}
			name, _ := json.Marshal(column)
			value, _ := cells[i].MarshalJSON()
			out.Write(name)
			out.WriteByte(':')
			out.Write(value)
		}
		out.WriteByte('}')
	}
	if err := rows.Err(); err != nil {
		w.T.Fatalf("%s: %v", query, err)
	}
	out.WriteByte(']')
	return out.String()
}

// SameJSON reports whether two JSON texts hold the same value with the
// same order of keys: equal after white space between tokens is removed.
func SameJSON(a, b string) bool {
	var ca, cb bytes.Buffer
	if json.Compact(&ca, []byte(a)) != nil || json.Compact(&cb, []byte(b)) != nil {
		return a == b
	}
	return ca.String() == cb.String()
}
