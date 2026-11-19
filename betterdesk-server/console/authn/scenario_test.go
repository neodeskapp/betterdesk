package authn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// TestScenarios replays testdata/vectors/db-<dialect>.json: the scenarios
// testdata/nodeoracle/db.js ran through the Node console's auth service on
// a real database. Every step must return what Node returned, log what
// Node logged, send the Go server what Node sent, and leave the same rows
// and files.

type dialectSQL struct {
	SQLite   string `json:"sqlite"`
	Postgres string `json:"postgres"`
}

func (d dialectSQL) pick(db *store.DB) string { return db.Pick(d.SQLite, d.Postgres) }

type seedUser struct {
	Username          string          `json:"username"`
	PasswordHash      string          `json:"password_hash"`
	Role              json.RawMessage `json:"role"`
	AuthProvider      json.RawMessage `json:"auth_provider"`
	PreferredLanguage *string         `json:"preferred_language"`
	TOTPSecret        *string         `json:"totp_secret"`
	TOTPEnabled       bool            `json:"totp_enabled"`
	TOTPRecoveryCodes *string         `json:"totp_recovery_codes"`
}

type fileVector struct {
	Path    string       `json:"path"`
	Content string       `json:"content"`
	Mode    *fs.FileMode `json:"mode"`
	Dir     bool         `json:"dir"`
}

type sleepHook struct {
	File  *fileVector `json:"file"`
	SQL   *dialectSQL `json:"sql"`
	Users []seedUser  `json:"users"`
}

type goCall struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body,omitempty"`
	Text   string          `json:"text,omitempty"`
}

type goScript struct {
	SSO   json.RawMessage   `json:"sso"`
	Login []json.RawMessage `json:"login"`
	LDAP  []json.RawMessage `json:"ldap"`
}

type stepVector struct {
	SQL  *dialectSQL       `json:"sql"`
	Fn   string            `json:"fn"`
	Args []json.RawMessage `json:"args"`
	At   struct {
		Abs *int64 `json:"abs"`
		Rel int64  `json:"rel"`
	} `json:"at"`
	Seed    uint32                 `json:"seed"`
	Go      *goScript              `json:"go"`
	OnSleep map[string][]sleepHook `json:"onSleep"`

	Result  json.RawMessage       `json:"result"`
	Thrown  *string               `json:"thrown"`
	GoCalls []goCall              `json:"goCalls"`
	Logs    [][]string            `json:"logs"`
	Sleeps  []int64               `json:"sleeps"`
	Files   map[string]fileExpect `json:"files"`
}

type fileExpect struct {
	Dir     bool        `json:"dir,omitempty"`
	Content string      `json:"content,omitempty"`
	Mode    fs.FileMode `json:"mode,omitempty"`
}

type scenarioVector struct {
	Name string             `json:"name"`
	Env  map[string]*string `json:"env"`
	Seed struct {
		Users []seedUser   `json:"users"`
		SQL   []dialectSQL `json:"sql"`
		After []dialectSQL `json:"after"`
	} `json:"seed"`
	Files []fileVector `json:"files"`
	Dirs  *struct {
		KeysPath   string `json:"keysPath"`
		DataDir    string `json:"dataDir"`
		AuthDBPath string `json:"authDbPath"`
	} `json:"dirs"`
	Platform string                      `json:"platform"`
	Steps    []stepVector                `json:"steps"`
	Tables   map[string][]map[string]any `json:"tables"`
	// skipTables leaves the rows at the end uncompared: a fix changed a
	// step whose effect on them the recording cannot tell.
	skipTables bool
}

// ---- the random stream of a step

// mulberry is the generator the oracle fed crypto.randomBytes from.
type mulberry struct{ a uint32 }

func (m *mulberry) Read(p []byte) (int, error) {
	for i := range p {
		m.a += 0x6D2B79F5
		t := m.a
		t = (t ^ t>>15) * (t | 1)
		t ^= t + (t^t>>7)*(t|61)
		p[i] = byte((t ^ t>>14) >> 24)
	}
	return len(p), nil
}

// ---- files in memory

type memFile struct {
	content []byte
	mode    fs.FileMode
	dir     bool
}

type memFS struct {
	mu    sync.Mutex
	files map[string]*memFile
}

func newMemFS() *memFS { return &memFS{files: map[string]*memFile{}} }

// isDir reports whether name is a folder: one that was made, or the parent
// of something.
func (m *memFS) isDir(name string) bool {
	if f, ok := m.files[name]; ok && f.dir {
		return true
	}
	for path := range m.files {
		if strings.HasPrefix(path, name+"/") {
			return true
		}
	}
	return false
}

func (m *memFS) Exists(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.files[name]
	return ok || m.isDir(name)
}

func (m *memFS) ReadFile(name string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.isDir(name) {
		return nil, &fs.PathError{Op: "read", Path: name, Err: errors.New("is a directory")}
	}
	f, ok := m.files[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return f.content, nil
}

func (m *memFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.isDir(name) {
		return &fs.PathError{Op: "open", Path: name, Err: errors.New("is a directory")}
	}
	if parent := filepath.Dir(name); parent != "." && !m.isDir(parent) {
		return &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	if f, ok := m.files[name]; ok {
		f.content = data
		return nil
	}
	m.files[name] = &memFile{content: data, mode: perm}
	return nil
}

func (m *memFS) Chmod(name string, mode fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[name]
	if !ok || f.dir {
		return &fs.PathError{Op: "chmod", Path: name, Err: fs.ErrNotExist}
	}
	f.mode = mode
	return nil
}

func (m *memFS) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.isDir(name) {
		return &fs.PathError{Op: "remove", Path: name, Err: errors.New("is a directory")}
	}
	if _, ok := m.files[name]; !ok {
		return &fs.PathError{Op: "remove", Path: name, Err: fs.ErrNotExist}
	}
	delete(m.files, name)
	return nil
}

func (m *memFS) place(f fileVector) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if f.Dir {
		m.files[f.Path] = &memFile{dir: true}
		return
	}
	mode := fs.FileMode(0o644)
	if f.Mode != nil {
		mode = *f.Mode
	}
	m.files[f.Path] = &memFile{content: []byte(f.Content), mode: mode}
}

const adminBase = "/tmp/authn-admin"

var (
	watchedDirs = []string{adminBase + "/keys", adminBase + "/keys/data", adminBase + "/data", adminBase + "/other", "/opt/rustdesk", "/opt/rustdesk/data",
		"/opt/betterdesk", "/opt/betterdesk/data", "/app/data", `C:\BetterDesk`, `C:\RustDesk`}
	watchedFiles = []string{".admin_credentials", ".force_password_update", "auth.db"}
)

var credentialsTime = regexp.MustCompile(`Timestamp: (\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z)`)

// listing is the state of the watched files. The time in a credentials
// file is written as its distance from the scenario's start, {{+25000}}.
func (m *memFS) listing(base time.Time) map[string]fileExpect {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]fileExpect{}
	look := func(path string) {
		if m.isDir(path) {
			out[path] = fileExpect{Dir: true}
			return
		}
		if f, ok := m.files[path]; ok {
			content := credentialsTime.ReplaceAllStringFunc(string(f.content), func(line string) string {
				at, err := time.Parse("2006-01-02T15:04:05.000Z", strings.TrimPrefix(line, "Timestamp: "))
				if err != nil {
					return line
				}
				return fmt.Sprintf("Timestamp: {{+%d}}", at.Sub(base).Milliseconds())
			})
			out[path] = fileExpect{Content: content, Mode: f.mode}
		}
	}
	for _, dir := range watchedDirs {
		for _, name := range watchedFiles {
			look(dir + "/" + name)
		}
	}
	look("/.dockerenv")
	look(".admin_credentials")
	return out
}

// ---- the Go server

type goStub struct {
	mu     sync.Mutex
	script goScript
	calls  []goCall
	// ssoAnswered is set when the single sign-on status was answered.
	ssoAnswered bool
}

var (
	defaultSSO = json.RawMessage(`{"status":200,"body":{"ldap_enabled":false,"oidc_enabled":false,"any_enabled":false}}`)
	refusedGo  = json.RawMessage(`{"status":401,"body":{"error":"Invalid credentials"}}`)
)

func (g *goStub) load(script *goScript) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.script, g.calls, g.ssoAnswered = goScript{}, nil, false
	if script != nil {
		g.script = goScript{SSO: script.SSO, Login: append([]json.RawMessage(nil), script.Login...), LDAP: append([]json.RawMessage(nil), script.LDAP...)}
	}
}

func shift(queue *[]json.RawMessage) json.RawMessage {
	if len(*queue) == 0 {
		return refusedGo
	}
	first := (*queue)[0]
	*queue = (*queue)[1:]
	return first
}

func (g *goStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	call := goCall{Method: r.Method, Path: r.URL.RequestURI()}
	if len(body) > 0 {
		if json.Valid(body) {
			call.Body = body
		} else {
			call.Text = string(body)
		}
	}
	g.mu.Lock()
	g.calls = append(g.calls, call)
	var answer json.RawMessage
	switch {
	case r.Method == http.MethodGet && call.Path == "/api/auth/sso/status":
		answer = g.script.SSO
		if answer == nil {
			answer = defaultSSO
		}
	case r.Method == http.MethodPost && call.Path == "/api/auth/login":
		answer = shift(&g.script.Login)
	case r.Method == http.MethodPost && call.Path == "/api/auth/ldap/verify":
		answer = shift(&g.script.LDAP)
	default:
		answer = json.RawMessage(`{"status":404,"body":{"error":"not found"}}`)
	}
	g.mu.Unlock()

	var word string
	if json.Unmarshal(answer, &word) == nil {
		if word == "timeout" {
			<-r.Context().Done()
			return
		}
		panic(http.ErrAbortHandler)
	}
	var shaped struct {
		Status int             `json:"status"`
		Body   json.RawMessage `json:"body"`
		Text   *string         `json:"text"`
	}
	if err := json.Unmarshal(answer, &shaped); err != nil {
		panic(err)
	}
	if call.Path == "/api/auth/sso/status" {
		g.mu.Lock()
		g.ssoAnswered = true
		g.mu.Unlock()
	}
	if shaped.Text != nil {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(shaped.Status)
		_, _ = io.WriteString(w, *shaped.Text)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(shaped.Status)
	_, _ = w.Write(shaped.Body)
}

// ---- log lines

type logCapture struct {
	mu    sync.Mutex
	lines [][]string
}

func (l *logCapture) add(level string, args []any) {
	line := []string{level}
	for _, arg := range args {
		line = append(line, fmt.Sprint(arg))
	}
	l.mu.Lock()
	l.lines = append(l.lines, line)
	l.mu.Unlock()
}

func (l *logCapture) Info(args ...any)  { l.add("info", args) }
func (l *logCapture) Warn(args ...any)  { l.add("warn", args) }
func (l *logCapture) Error(args ...any) { l.add("error", args) }

func (l *logCapture) take() [][]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	lines := l.lines
	l.lines = nil
	return lines
}

// ---- the database between scenarios

func resetTables(t *testing.T, db *store.DB) {
	t.Helper()
	statements := []string{"DELETE FROM access_tokens", "DELETE FROM login_attempts", "DELETE FROM account_lockouts", "DELETE FROM audit_log", "DELETE FROM users",
		"DELETE FROM sqlite_sequence WHERE name IN ('access_tokens', 'login_attempts', 'account_lockouts', 'audit_log', 'users')", "DROP TABLE IF EXISTS device_tokens"}
	if db.IsPostgres() {
		statements = []string{"DELETE FROM access_tokens", "DELETE FROM login_attempts", "DELETE FROM account_lockouts", "DELETE FROM audit_log", "DELETE FROM users",
			"SELECT setval(pg_get_serial_sequence('access_tokens', 'id'), 1, false), setval(pg_get_serial_sequence('login_attempts', 'id'), 1, false), " +
				"setval(pg_get_serial_sequence('account_lockouts', 'id'), 1, false), setval(pg_get_serial_sequence('users', 'id'), 1, false)",
			"DROP TABLE IF EXISTS device_tokens"}
	}
	for _, statement := range statements {
		mustExec(t, db, statement)
	}
}

func mustExec(t *testing.T, db *store.DB, statement string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), statement, args...); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}

// textOrDefault is a seeded column that is a text, NULL when the vector
// says null, and the default when the vector leaves it out.
func textOrDefault(raw json.RawMessage, fallback string) any {
	if raw == nil {
		return fallback
	}
	var text *string
	if err := json.Unmarshal(raw, &text); err != nil {
		panic(err)
	}
	if text == nil {
		return nil
	}
	return *text
}

func seedUsers(t *testing.T, db *store.DB, users []seedUser) {
	t.Helper()
	for _, u := range users {
		var enabled any = u.TOTPEnabled
		if !db.IsPostgres() {
			enabled = 0
			if u.TOTPEnabled {
				enabled = 1
			}
		}
		mustExec(t, db, `INSERT INTO users (username, password_hash, role, auth_provider, preferred_language, totp_secret, totp_enabled, totp_recovery_codes)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			u.Username, u.PasswordHash, textOrDefault(u.Role, "admin"), textOrDefault(u.AuthProvider, "local"),
			u.PreferredLanguage, u.TOTPSecret, enabled, u.TOTPRecoveryCodes)
	}
}

// dumpTables reads the rows as the oracle wrote them down: flags as
// booleans, times as seconds from the database's clock, rounded to ten.
func dumpTables(t *testing.T, db *store.DB) map[string][]map[string]any {
	t.Helper()
	seconds := func(column string) string {
		if db.IsPostgres() {
			return "CAST(ROUND(EXTRACT(EPOCH FROM (" + column + " - NOW())) / 10) AS INTEGER) * 10"
		}
		return "CAST(ROUND((julianday(" + column + ") - julianday('now')) * 8640) AS INTEGER) * 10"
	}
	truth := func(column string) string {
		if db.IsPostgres() {
			return "COALESCE(" + column + ", FALSE)"
		}
		return "(" + column + " IS NOT NULL AND " + column + " <> 0)"
	}
	queries := []struct{ table, query string }{
		{"users", `SELECT id, username, password_hash, role, auth_provider, ` + seconds("last_login") + ` AS last_login, preferred_language, totp_secret, ` +
			truth("totp_enabled") + ` AS totp_enabled, totp_recovery_codes FROM users ORDER BY id`},
		{"login_attempts", `SELECT id, username, ip_address, ` + truth("success") + ` AS success, ` + seconds("created_at") + ` AS created_at FROM login_attempts ORDER BY id`},
		{"account_lockouts", `SELECT username, ` + seconds("locked_until") + ` AS locked_until, attempt_count FROM account_lockouts ORDER BY username`},
		{"access_tokens", `SELECT id, token, token_hash, user_id, client_id, client_uuid, ` + seconds("expires_at") + ` AS expires_at, ` + seconds("last_used") + ` AS last_used,
			ip_address, ` + truth("revoked") + ` AS revoked FROM access_tokens ORDER BY id`},
	}
	flags := map[string]bool{"totp_enabled": true, "success": true, "revoked": true}
	out := map[string][]map[string]any{}
	for _, q := range queries {
		rows, err := db.Query(context.Background(), q.query)
		if err != nil {
			t.Fatalf("dump %s: %v", q.table, err)
		}
		columns, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(columns))
			targets := make([]any, len(columns))
			for i := range values {
				targets[i] = &values[i]
			}
			if err := rows.Scan(targets...); err != nil {
				t.Fatalf("dump %s: %v", q.table, err)
			}
			row := map[string]any{}
			for i, column := range columns {
				switch v := values[i].(type) {
				case int64:
					if flags[column] {
						row[column] = v != 0
					} else {
						row[column] = float64(v)
					}
				case int32:
					row[column] = float64(v)
				case []byte:
					row[column] = string(v)
				default:
					row[column] = v
				}
			}
			out[q.table] = append(out[q.table], row)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// ---- one scenario

type scenarioRun struct {
	t      *testing.T
	db     *store.DB
	sc     *scenarioVector
	svc    *Service
	stub   *goStub
	logs   *logCapture
	files  *memFS
	base   time.Time
	now    time.Time
	sleeps []int64
	hooks  map[string][]sleepHook
	// ssoAt is the time on the scenario's clock at which the status of
	// single sign-on was last answered.
	ssoAt   time.Time
	ssoKept bool
}

// testDummyHash is a bcrypt hash of cost 4 for the compare an unknown user
// costs.
const testDummyHash = "$2b$04$KiXeOj5vHpJRJHGMhWzadeGcZXEL6ZJOxJHQmO7oKUwgLGDMaRJ3O"

func (r *scenarioRun) newClient() *goapi.Client {
	// Long enough that a busy machine does not turn an answer into a
	// timeout; every scripted timeout waits this long.
	return goapi.New(r.stub, goapi.WithTimeout(200*time.Millisecond))
}

func (r *scenarioRun) settings() Settings {
	env := map[string]string{"DB_TYPE": r.db.Dialect().String()}
	for key, value := range r.sc.Env {
		if value == nil {
			delete(env, key)
		} else {
			env[key] = *value
		}
	}
	getenv := func(key string) string {
		if key == "DATABASE_URL" && r.db.IsPostgres() {
			return "postgres://betterdesk@db/betterdesk"
		}
		return env[key]
	}
	dataDir, keysPath := "/tmp/authn-oracle/data", "/tmp/authn-oracle/keys"
	if r.sc.Dirs != nil {
		dataDir, keysPath = r.sc.Dirs.DataDir, r.sc.Dirs.KeysPath
	}
	set := SettingsFromEnv(getenv, dataDir, keysPath)
	if r.sc.Dirs != nil {
		set.AuthDBPath = r.sc.Dirs.AuthDBPath
	}
	return set
}

func (r *scenarioRun) sleep(ctx context.Context, d time.Duration) error {
	r.sleeps = append(r.sleeps, d.Milliseconds())
	r.now = r.now.Add(d)
	for _, hook := range r.hooks[strconv.Itoa(len(r.sleeps))] {
		if hook.File != nil {
			r.files.place(*hook.File)
		}
		if hook.SQL != nil {
			mustExec(r.t, r.db, hook.SQL.pick(r.db))
		}
		seedUsers(r.t, r.db, hook.Users)
	}
	return ctx.Err()
}

func runScenario(t *testing.T, db *store.DB, sc *scenarioVector, changed map[string]int) {
	fixScenario(sc, changed)
	r := &scenarioRun{t: t, db: db, sc: sc, stub: &goStub{}, logs: &logCapture{}, files: newMemFS(), base: time.Now().Truncate(time.Second)}
	resetTables(t, db)
	for _, f := range sc.Files {
		r.files.place(f)
	}
	for _, statement := range sc.Seed.SQL {
		mustExec(t, db, statement.pick(db))
	}
	seedUsers(t, db, sc.Seed.Users)
	for _, statement := range sc.Seed.After {
		mustExec(t, db, statement.pick(db))
	}

	svc, err := New(Options{
		DB:       db,
		GoAPI:    r.newClient(),
		Settings: r.settings(),
		QR:       func(text string) (string, error) { return "QR:" + text, nil },
		Log:      r.logs,
		Now:      func() time.Time { return r.now },
		FS:       r.files,
		Sleep:    r.sleep,
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.bcryptCost, svc.dummy, svc.windows = bcryptMinCost, testDummyHash, sc.Platform == "win32"
	r.svc = svc

	for i := range sc.Steps {
		step := &sc.Steps[i]
		if step.SQL != nil {
			mustExec(t, db, step.SQL.pick(db))
			continue
		}
		r.runStep(i, step)
		if t.Failed() {
			return
		}
	}
	got := dumpTables(t, db)
	want := sc.Tables
	alignTimes(got, want)
	if !sc.skipTables && (len(got) != 0 || len(want) != 0) {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("rows at the end:\n got %s\nwant %s", toJSON(got), toJSON(want))
		}
	}
}

// timeColumns are the columns dumpTables writes as seconds from the
// database's clock.
var timeColumns = []string{"last_login", "created_at", "locked_until", "expires_at", "last_used"}

// alignTimes takes the recorded time where the run's own is one rounding
// step (ten seconds) away from it: a scenario on a busy machine can take
// long enough between writing a row and reading it back to round to the
// next step, which is no difference in what the code wrote.
func alignTimes(got, want map[string][]map[string]any) {
	for table, rows := range got {
		for i, row := range rows {
			if i >= len(want[table]) {
				break
			}
			for _, column := range timeColumns {
				g, gok := row[column].(float64)
				w, wok := want[table][i][column].(float64)
				if gok && wok && math.Abs(g-w) <= 10 {
					row[column] = w
				}
			}
		}
	}
}

func toJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return err.Error()
	}
	return string(raw)
}

func (r *scenarioRun) runStep(index int, step *stepVector) {
	t := r.t
	r.now = r.base.Add(time.Duration(step.At.Rel) * time.Millisecond)
	if step.At.Abs != nil {
		r.now = time.UnixMilli(*step.At.Abs)
	}
	// The service asks for the status of single sign-on once a minute by
	// the scenario's clock; the client keeps it by the real clock.
	if r.ssoKept && r.now.Sub(r.ssoAt) >= time.Minute {
		r.svc.goAPI = r.newClient()
		r.ssoKept = false
	}
	r.stub.load(step.Go)
	r.svc.rand = &mulberry{a: step.Seed}
	if step.Fn == "generateTotpSetup" {
		// AN1: Node's secret was the one of the random bytes with their top
		// bit cleared. Fed those bytes the console makes Node's secret, so
		// the steps after the set-up compare with Node's; TestTOTPVectors
		// holds the difference itself.
		r.svc.rand = topBitCleared{r.svc.rand}
	}
	r.sleeps, r.hooks = nil, step.OnSleep
	asked := r.now

	result, err := r.call(step)

	r.stub.mu.Lock()
	calls, answered := r.stub.calls, r.stub.ssoAnswered
	r.stub.mu.Unlock()
	if answered {
		r.ssoAt, r.ssoKept = asked, true
	}
	label := fmt.Sprintf("step %d %s(%s)", index, step.Fn, joinArgs(step.Args))
	var refusal Refusal
	switch {
	case step.Thrown != nil:
		if err == nil || errors.As(err, &refusal) {
			t.Errorf("%s: got %s, %v; Node throws %q", label, toJSON(result), err, *step.Thrown)
		}
	case err != nil:
		t.Errorf("%s: error %v; Node returns %s", label, err, step.Result)
	default:
		if !sameJSON(result, step.Result) {
			t.Errorf("%s:\n got %s\nNode %s", label, toJSON(result), step.Result)
		}
	}

	if !sameJSON(calls, mustJSON(step.GoCalls)) && !(len(calls) == 0 && len(step.GoCalls) == 0) {
		t.Errorf("%s: calls to the Go server:\n got %s\nNode %s", label, toJSON(calls), toJSON(step.GoCalls))
	}
	if got := r.logs.take(); !sameLogs(got, step.Logs) {
		t.Errorf("%s: log lines:\n got %s\nNode %s", label, toJSON(got), toJSON(step.Logs))
	}
	if !reflect.DeepEqual(r.sleeps, step.Sleeps) && !(len(r.sleeps) == 0 && len(step.Sleeps) == 0) {
		t.Errorf("%s: waits %v, Node waits %v", label, r.sleeps, step.Sleeps)
	}
	if step.Files != nil {
		if got := r.files.listing(r.base); !reflect.DeepEqual(got, step.Files) {
			t.Errorf("%s: files:\n got %s\nNode %s", label, toJSON(got), toJSON(step.Files))
		}
	}
}

func joinArgs(args []json.RawMessage) string {
	parts := make([]string, len(args))
	for i, arg := range args {
		parts[i] = string(arg)
	}
	return strings.Join(parts, ", ")
}

func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

// sameJSON compares a Go value with JSON text as parsed values, so the
// order of keys does not count.
func sameJSON(got any, want json.RawMessage) bool {
	var a, b any
	if err := json.Unmarshal(mustJSON(got), &a); err != nil {
		return false
	}
	if len(want) == 0 {
		want = json.RawMessage("null")
	}
	if err := json.Unmarshal(want, &b); err != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}

// sameLogs compares log lines. One line ends in the operating system's
// words for a failed write, which Node and Go phrase differently: it is
// compared up to there.
func sameLogs(got, want [][]string) bool {
	if len(got) != len(want) {
		return false
	}
	const failedWrite = "[AUTH] Could not write .admin_credentials to "
	for i := range got {
		if len(got[i]) == 2 && len(want[i]) == 2 && got[i][0] == want[i][0] &&
			strings.HasPrefix(got[i][1], failedWrite) && strings.HasPrefix(want[i][1], failedWrite) {
			cut := func(line string) string {
				rest := line[len(failedWrite):]
				return rest[:strings.Index(rest, ": ")+1]
			}
			if cut(got[i][1]) == cut(want[i][1]) {
				continue
			}
		}
		if !reflect.DeepEqual(got[i], want[i]) {
			return false
		}
	}
	return true
}

func argString(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("argument %s is not text", raw)
	}
	return s
}

func argInt(t *testing.T, raw json.RawMessage) int64 {
	t.Helper()
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		t.Fatalf("argument %s is not a whole number", raw)
	}
	return n
}

// refusalOr is the object Node returned for a call that can refuse:
// {success:true, ...} or {success:false, error}.
func refusalOr(err error, success map[string]any) (any, error) {
	var refusal Refusal
	if errors.As(err, &refusal) {
		return map[string]any{"success": false, "error": string(refusal)}, nil
	}
	if err != nil {
		return nil, err
	}
	success["success"] = true
	return success, nil
}

func (r *scenarioRun) call(step *stepVector) (any, error) {
	t, s, ctx := r.t, r.svc, context.Background()
	args := step.Args
	switch step.Fn {
	case "authenticate":
		outcome, err := s.Authenticate(ctx, argString(t, args[0]), argString(t, args[1]))
		switch {
		case err != nil:
			return nil, err
		case outcome.Failure == FailureProOnly:
			// Node's authenticate returned null for a pro account; the
			// route tells it apart now (BUGS.md S11)
			return nil, nil
		case outcome.IsFailure():
			return map[string]any{"__authFailure": outcome.Failure}, nil
		case outcome.User == nil:
			return nil, nil
		}
		return outcome.User, nil
	case "changePassword":
		return refusalOr(s.ChangePassword(ctx, argInt(t, args[0]), argString(t, args[1]), argString(t, args[2])), map[string]any{})
	case "generateTotpSetup":
		setup, err := s.GenerateTOTPSetup(ctx, argInt(t, args[0]))
		if setup == nil {
			return refusalOr(err, nil)
		}
		return refusalOr(err, map[string]any{"secret": setup.Secret, "qrCode": setup.QRCode, "otpauthUrl": setup.OtpauthURL})
	case "verifyAndEnableTotp":
		codes, err := s.VerifyAndEnableTOTP(ctx, argInt(t, args[0]), argString(t, args[1]))
		return refusalOr(err, map[string]any{"recoveryCodes": codes})
	case "verifyTotpCode":
		return s.VerifyTOTPCode(ctx, argInt(t, args[0]), argString(t, args[1]))
	case "verifyRecoveryCode":
		return s.VerifyRecoveryCode(ctx, argInt(t, args[0]), argString(t, args[1]))
	case "disableTotp":
		return refusalOr(s.DisableTOTP(ctx, argInt(t, args[0])), map[string]any{})
	case "isTotpEnabled":
		return s.IsTOTPEnabled(ctx, argInt(t, args[0]))
	case "generateAccessToken":
		return s.GenerateAccessToken(ctx, argInt(t, args[0]), argString(t, args[1]), argString(t, args[2]), argString(t, args[3]))
	case "validateAccessToken":
		user, err := s.ValidateAccessToken(ctx, argString(t, args[0]))
		if user == nil {
			return nil, err
		}
		return user, err
	case "revokeClientTokens":
		return nil, s.RevokeClientTokens(ctx, argInt(t, args[0]), argString(t, args[1]), argString(t, args[2]))
	case "checkBruteForce":
		return s.CheckBruteForce(ctx, argString(t, args[0]), argString(t, args[1]))
	case "recordAttempt":
		var success bool
		if err := json.Unmarshal(args[2], &success); err != nil {
			t.Fatal(err)
		}
		return nil, s.RecordAttempt(ctx, argString(t, args[0]), argString(t, args[1]), success)
	case "cleanupHousekeeping":
		s.CleanupHousekeeping(ctx)
		return nil, nil
	case "ensureDefaultAdmin":
		return s.EnsureDefaultAdmin(ctx)
	case "requireDeviceToken":
		header := ""
		if string(args[0]) != "null" {
			header = argString(t, args[0])
		}
		auth, err := s.RequireDeviceToken(ctx, header)
		var refused *DeviceAuthError
		if errors.As(err, &refused) {
			return map[string]any{"status": refused.Status, "body": map[string]any{"error": refused.Text}}, nil
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"next": true, "deviceId": auth.DeviceID, "tokenUserId": auth.Token.UserID.V}, nil
	case "verifyDeviceWsAuth":
		return s.VerifyDeviceWSAuth(ctx, argString(t, args[0]), argString(t, args[1]))
	case "verifyEnrollmentToken":
		return s.VerifyEnrollmentToken(ctx, argString(t, args[0]), argString(t, args[1]))
	}
	t.Fatalf("unknown function %q in the vectors", step.Fn)
	return nil, nil
}

func TestScenarios(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		name := "db-" + db.Dialect().String() + ".json"
		if !db.IsPostgres() {
			// The scenarios make tens of thousands of small writes. Nothing
			// here needs them to survive a power cut.
			mustExec(t, db, "PRAGMA synchronous = OFF")
		}
		raw, err := os.ReadFile(filepath.Join("testdata", "vectors", name))
		if err != nil {
			t.Fatalf("read vectors: %v", err)
		}
		var file struct {
			Dialect   string           `json:"dialect"`
			Scenarios []scenarioVector `json:"scenarios"`
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		if err := decoder.Decode(&file); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if len(file.Scenarios) < 1500 {
			t.Fatalf("only %d scenarios in %s", len(file.Scenarios), name)
		}
		groups, changed := map[string]int{}, map[string]int{}
		steps := 0
		for i := range file.Scenarios {
			sc := &file.Scenarios[i]
			group, _, _ := strings.Cut(sc.Name, "/")
			if testing.Short() && groups[group] >= 40 {
				continue
			}
			groups[group]++
			steps += len(sc.Steps)
			t.Run(sc.Name, func(t *testing.T) { runScenario(t, db, sc, changed) })
		}
		names := make([]string, 0, len(groups))
		for group, n := range groups {
			names = append(names, fmt.Sprintf("%s %d", group, n))
		}
		sort.Strings(names)
		t.Logf("%d steps in scenarios: %s", steps, strings.Join(names, ", "))
		for _, group := range []string{"auth", "totp", "recovery", "lockout", "housekeeping", "tokens", "device", "password", "admin"} {
			if groups[group] == 0 {
				t.Errorf("no scenario of group %s", group)
			}
		}
		for _, fix := range scenarioFixes {
			if changed[fix.bug] == 0 && !testing.Short() {
				t.Errorf("the fix of %s changes no scenario of %s", fix.bug, name)
			}
		}
	})
}
