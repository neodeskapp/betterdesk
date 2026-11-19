package servermanagement

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/auditlog"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/limits"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/servermgmt"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

var roleIDs = map[string]int64{"admin": 1, "super_admin": 2, "server_admin": 3, "global_admin": 4, "operator": 5, "viewer": 6, "guest_issuer": 7, "pro": 8}

// site is the group behind the pipeline steps its routes need (the body
// parsers, the session, the request accessors), with the server
// management of a folder of the test and a stand-in for the commands.
type site struct {
	t     *testing.T
	d     *kit.Deps
	db    *store.DB
	steps http.Handler
	root  string
	run   *fakeRun
	// env is the environment the server management reads
	env map[string]string
}

// fakeRun answers every command with a fixed result and notes it.
type fakeRun struct {
	stdout string
	code   int
	err    error
	calls  []string
}

func (f *fakeRun) run(_ context.Context, name string, args ...string) ([]byte, []byte, int, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	return []byte(f.stdout), nil, f.code, f.err
}

func newSite(t *testing.T, db *store.DB) *site {
	t.Helper()
	sessions, err := session.New(context.Background(), session.Options{DB: db, Secret: "srvmgmt-secret-x-0123456789abcdef0123456789"})
	if err != nil {
		t.Fatal(err)
	}
	// The accounts exist, so audit entries can name them (PostgreSQL
	// checks the reference)
	for role, id := range roleIDs {
		if _, err := db.Exec(context.Background(), "INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, 'x', ?)", id, "e2e_"+role, role); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	for _, dir := range []string{"server", "console", "home", "log", "tmp"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o777); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{
		"BETTERDESK_PATH": root + "/server", "BETTERDESK_CONSOLE_PATH": root + "/console", "HOME": root + "/home", "TMPDIR": root + "/tmp",
	}
	run := &fakeRun{}
	health := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"uptime":"1h2m3s","version":"3.5.264"}`))
	})
	api := goapi.New(health)
	d := &kit.Deps{
		Router:   kit.NewRouter(),
		Store:    db,
		Sessions: sessions,
		Log:      kit.NewLogger("error", true),
		Limits:   limits.NewSet(limits.Options{ClientIP: httpx.SocketIP, UserKey: func(*http.Request) string { return "" }}),
		Audit:    auditlog.New(db),
		Backend:  goapi.NewBackend(api, nil),
		ServerMgmt: servermgmt.New(servermgmt.Options{
			Getenv: func(k string) string { return env[k] }, WorkDir: root + "/console", GOOS: "linux",
			LogDir: root + "/log", TempDirs: []string{root + "/tmp"}, Run: run.run,
		}),
	}
	Register(d)
	d.Router.Get("/signin/{role}", func(w http.ResponseWriter, r *http.Request) {
		s := kit.Session(r)
		if err := s.Regenerate(); err != nil {
			t.Error(err)
		}
		role := r.PathValue("role")
		if _, err := s.Establish(session.User{ID: roleIDs[role], Username: "e2e_" + role, Role: role}, session.Meta{}); err != nil {
			t.Error(err)
		}
	})
	steps := middleware.Bodies(middleware.BodyOptions{})(sessions.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !d.Router.Serve(w, kit.Begin(r, httpx.TrustProxy{}, nil)) {
			http.NotFound(w, r)
		}
	})))
	return &site{t: t, d: d, db: db, steps: steps, root: root, run: run, env: env}
}

func (s *site) call(cookie, method, target, body string, header ...string) *httptest.ResponseRecorder {
	s.t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.RemoteAddr = "203.0.113.7:5555"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Add(header[i], header[i+1])
	}
	s.steps.ServeHTTP(rec, req)
	return rec
}

func (s *site) signIn(role string) string {
	s.t.Helper()
	pair, _, _ := strings.Cut(s.call("", "GET", "/signin/"+role, "").Header().Get("Set-Cookie"), ";")
	if !strings.HasPrefix(pair, "bd.sid=") {
		s.t.Fatalf("sign in as %s set the cookie %q", role, pair)
	}
	return pair
}

func decode(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("not JSON: %q", body)
	}
	return m
}

// audits are the audit log's entries: user, action, details, address.
func audits(t *testing.T, db *store.DB) [][4]string {
	t.Helper()
	rows, err := db.Query(context.Background(), "SELECT COALESCE(CAST(user_id AS TEXT), ''), action, COALESCE(details, ''), COALESCE(ip_address, '') FROM audit_log ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][4]string
	for rows.Next() {
		var e [4]string
		if err := rows.Scan(&e[0], &e[1], &e[2], &e[3]); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

// Every route needs a session (401) and server.config (403): viewer and
// global_admin lack it, server_admin and admin have it.
func TestRoutesNeedServerConfig(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newSite(t, db)
		routes := [][2]string{
			{"GET", "/api/server-management/info"}, {"GET", "/api/server-management/resources"},
			{"GET", "/api/server-management/files?path=/"}, {"GET", "/api/server-management/files/read?path=x"},
			{"POST", "/api/server-management/files/write"}, {"POST", "/api/server-management/files/mkdir"},
			{"POST", "/api/server-management/files/rename"}, {"POST", "/api/server-management/files/delete"},
			{"GET", "/api/server-management/services"}, {"POST", "/api/server-management/services/hbbs/status"},
		}
		cookies := map[string]string{"": ""}
		for _, role := range []string{"viewer", "global_admin", "server_admin", "admin"} {
			cookies[role] = s.signIn(role)
		}
		for _, rt := range routes {
			for role, want := range map[string]int{"": 401, "viewer": 403, "global_admin": 403} {
				if rec := s.call(cookies[role], rt[0], rt[1], "{}"); rec.Code != want {
					t.Errorf("%s %s as %q: %d, want %d", rt[0], rt[1], role, rec.Code, want)
				}
			}
			for _, role := range []string{"server_admin", "admin"} {
				if rec := s.call(cookies[role], rt[0], rt[1], "{}"); rec.Code == 401 || rec.Code == 403 {
					t.Errorf("%s %s as %s: %d", rt[0], rt[1], role, rec.Code)
				}
			}
		}
		if strings.Count(strings.Join(s.run.calls, ","), "systemctl status hbbs") != 2 {
			t.Errorf("commands run by the allowed roles only: %q", s.run.calls)
		}
		if got := audits(t, db); len(got) != 2 {
			t.Errorf("audit entries for the allowed service actions only: %v", got)
		}
	})
}

// A file round trip as admin: every change is in the audit log with the
// user, the details and the client's address as TRUST_PROXY gives it:
// with no proxy trusted, the connection's address, whatever
// X-Forwarded-For says.
func TestFileRoutes(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newSite(t, db)
		admin := s.signIn("admin")
		base := s.root + "/server"
		post := func(path, body string) map[string]any {
			t.Helper()
			rec := s.call(admin, "POST", "/api/server-management/files/"+path, body, "X-Forwarded-For", " 198.51.100.9 , 10.0.0.1")
			return decode(t, rec.Body.String())
		}
		if got := post("mkdir", `{"path":"`+base+`/d"}`); got["success"] != true || got["path"] != base+"/d" {
			t.Errorf("mkdir %v", got)
		}
		if got := post("write", `{"path":"`+base+`/d/a.txt","content":"héllo"}`); got["size"] != 6.0 {
			t.Errorf("write %v", got)
		}
		rec := s.call(admin, "GET", "/api/server-management/files/read?path="+base+"/d/a.txt", "")
		if got := decode(t, rec.Body.String()); got["content"] != "héllo" || got["isBinary"] != false || got["truncated"] != false || got["mode"] != 420.0 && runtime.GOOS != "windows" {
			t.Errorf("read %v", got)
		}
		rec = s.call(admin, "GET", "/api/server-management/files?path="+base+"/d", "")
		list := decode(t, rec.Body.String())
		if list["cwd"] != base+"/d" || list["path"] != base+"/d" || list["parent"] != base || len(list["entries"].([]any)) != 1 || !reflect.DeepEqual(list["entries"], list["items"]) {
			t.Errorf("list %v", list)
		}
		if got := post("rename", `{"from":"`+base+`/d/a.txt","to":"`+base+`/b.txt"}`); got["from"] != base+"/d/a.txt" || got["to"] != base+"/b.txt" {
			t.Errorf("rename %v", got)
		}
		if got := post("delete", `{"path":"`+base+`/d"}`); got["path"] != base+"/d" {
			t.Errorf("delete %v", got)
		}
		want := [][4]string{
			{"1", "server_file_mkdir", "path=" + base + "/d", "203.0.113.7"},
			{"1", "server_file_write", "path=" + base + "/d/a.txt bytes=6", "203.0.113.7"},
			{"1", "server_file_rename", "from=" + base + "/d/a.txt to=" + base + "/b.txt", "203.0.113.7"},
			{"1", "server_file_delete", "path=" + base + "/d", "203.0.113.7"},
		}
		if got := audits(t, db); !reflect.DeepEqual(got, want) {
			t.Errorf("audits\n got %v\nwant %v", got, want)
		}
		if _, err := os.Stat(base + "/b.txt"); err != nil {
			t.Errorf("renamed file: %v", err)
		}
	})
}

// The body checks of the old routes, before anything touches a file, and
// the refusals of the service answered as 400 with their message.
func TestBodyChecks(t *testing.T) {
	db := storetest.Migrated(t)
	s := newSite(t, db)
	admin := s.signIn("admin")
	cases := []struct{ method, path, body, want string }{
		{"POST", "/api/server-management/files/write", `{"path":"x"}`, `{"success":false,"error":"path_and_content_required"}`},
		{"POST", "/api/server-management/files/write", `{"path":5,"content":"x"}`, `{"success":false,"error":"path_and_content_required"}`},
		{"POST", "/api/server-management/files/write", `{"path":"","content":"x"}`, `{"success":false,"error":"Path is required"}`},
		{"POST", "/api/server-management/files/write", `{"path":"/etc/x","content":"x"}`, `{"success":false,"error":"Path outside allowed directory roots"}`},
		{"POST", "/api/server-management/files/write", `{"path":"` + s.root + `/home/x","content":"x"}`, `{"success":false,"error":"Path outside allowed directory roots"}`},
		{"POST", "/api/server-management/files/mkdir", `{"path":""}`, `{"success":false,"error":"path_required"}`},
		{"POST", "/api/server-management/files/mkdir", `{"path":true}`, `{"success":false,"error":"path_required"}`},
		{"POST", "/api/server-management/files/delete", `{}`, `{"success":false,"error":"path_required"}`},
		{"POST", "/api/server-management/files/delete", `{"path":["x"]}`, `{"success":false,"error":"path_required"}`},
		{"POST", "/api/server-management/files/rename", `{"from":"a"}`, `{"success":false,"error":"from_and_to_required"}`},
		{"POST", "/api/server-management/files/rename", `{"from":"a","to":{}}`, `{"success":false,"error":"from_and_to_required"}`},
		{"POST", "/api/server-management/files/rename", `{"from":1,"to":"b"}`, `{"success":false,"error":"from_and_to_required"}`},
		{"GET", "/api/server-management/files/read", "", `{"success":false,"error":"Path is required"}`},
		{"GET", "/api/server-management/files", "", `{"success":false,"error":"Path outside allowed directory roots"}`},
		{"GET", "/api/server-management/files?path[x]=1", "", `{"success":false,"error":"ENOENT: no such file or directory, stat '` + s.root + `/console/[object Object]'"}`},
		{"POST", "/api/server-management/services/bad%20name/status", "{}", `{"success":false,"error":"invalid_service_name"}`},
		{"POST", "/api/server-management/services/sshd/status", "{}", `{"success":false,"error":"Service is not allowlisted"}`},
		{"POST", "/api/server-management/services/hbbs/kill", "{}", `{"success":false,"error":"Action not allowed"}`},
	}
	for _, tc := range cases {
		rec := s.call(admin, tc.method, tc.path, tc.body)
		if rec.Code != 400 || strings.TrimSpace(rec.Body.String()) != tc.want {
			t.Errorf("%s %s %s: %d %s, want 400 %s", tc.method, tc.path, tc.body, rec.Code, rec.Body.String(), tc.want)
		}
	}
	if len(s.run.calls) != 0 || len(audits(t, db)) != 0 {
		t.Errorf("a refused request ran %q or wrote %v", s.run.calls, audits(t, db))
	}
	if entries, _ := os.ReadDir(s.root + "/home"); len(entries) != 0 {
		t.Errorf("a refused write left %v", entries)
	}
}

// A service action runs the command, answers its result and writes the
// audit entry with the exit code; a command that cannot start answers 500
// with a fixed text and writes no audit entry.
func TestServiceRoutes(t *testing.T) {
	db := storetest.Migrated(t)
	s := newSite(t, db)
	admin := s.signIn("admin")
	s.run.stdout, s.run.code = "active\n", 3
	rec := s.call(admin, "POST", "/api/server-management/services/hbbs.service/status", "{}")
	if strings.TrimSpace(rec.Body.String()) != `{"success":true,"name":"hbbs.service","action":"status","exitCode":3,"stdout":"active\n","stderr":""}` {
		t.Errorf("status %d %s", rec.Code, rec.Body.String())
	}
	if got := audits(t, db); len(got) != 1 || got[0] != [4]string{"1", "server_service_control", "name=hbbs.service action=status exit=3", "203.0.113.7"} {
		t.Errorf("audits %v", got)
	}
	s.run.err = errors.New("exec: systemctl not found")
	rec = s.call(admin, "POST", "/api/server-management/services/hbbs/restart", "{}")
	if rec.Code != 500 || strings.TrimSpace(rec.Body.String()) != `{"success":false,"error":"Service command could not be started"}` {
		t.Errorf("a command that cannot start: %d %s", rec.Code, rec.Body.String())
	}
	if len(audits(t, db)) != 1 {
		t.Error("a command that could not start wrote an audit entry")
	}

	s.run.stdout, s.run.code, s.run.err = "betterdesk-server.service loaded active running BetterDesk\nsshd.service loaded active running SSH\nhbbs.service loaded inactive dead\n", 0, nil
	rec = s.call(admin, "GET", "/api/server-management/services", "")
	want := `{"success":true,"platform":"linux","services":[{"name":"betterdesk-server.service","load":"loaded","active":"active","sub":"running","description":"BetterDesk","state":"active","status":"running"},{"name":"hbbs.service","load":"loaded","active":"inactive","sub":"dead","description":"","state":"inactive","status":"dead"}]}`
	if strings.TrimSpace(rec.Body.String()) != want {
		t.Errorf("services %s", rec.Body.String())
	}
}

// The console's own service restarts by the process's exit when a
// supervisor runs it: nothing is run, and the answer is a clean result.
func TestRestartSelf(t *testing.T) {
	db := storetest.Migrated(t)
	s := newSite(t, db)
	exits := 0
	svc := servermgmt.New(servermgmt.Options{GOOS: "linux", Run: s.run.run, RestartSelf: func() bool { exits++; return true }})
	for _, name := range []string{"betterdesk-server", "betterdesk-server.service"} {
		res, err := svc.ControlService(context.Background(), name, "restart")
		if err != nil || res.ExitCode != 0 || res.Stdout != "" {
			t.Errorf("%s: %+v %v", name, res, err)
		}
	}
	for _, other := range [][2]string{{"betterdesk-server", "stop"}, {"betterdesk-console", "restart"}, {"hbbs", "restart"}} {
		if _, err := svc.ControlService(context.Background(), other[0], other[1]); err != nil {
			t.Error(err)
		}
	}
	windows := servermgmt.New(servermgmt.Options{GOOS: "windows", Run: s.run.run, RestartSelf: func() bool { exits++; return true }})
	if _, err := windows.ControlService(context.Background(), "BetterDeskServer", "restart"); err != nil {
		t.Error(err)
	}
	if exits != 3 || len(s.run.calls) != 3 {
		t.Errorf("exits %d, commands %q", exits, s.run.calls)
	}
	none := servermgmt.New(servermgmt.Options{GOOS: "linux", Run: s.run.run, RestartSelf: func() bool { return false }})
	if _, err := none.ControlService(context.Background(), "betterdesk-server", "restart"); err != nil || len(s.run.calls) != 4 {
		t.Errorf("without a supervisor the restart runs systemctl: %v %q", err, s.run.calls)
	}
}

// The info route: Go's runtime, this process, the folder lists and the
// API server's health.
func TestInfo(t *testing.T) {
	db := storetest.Migrated(t)
	s := newSite(t, db)
	rec := s.call(s.signIn("admin"), "GET", "/api/server-management/info", "")
	got := decode(t, rec.Body.String())
	if got["nodeVersion"] != runtime.Version() || got["pid"] != float64(os.Getpid()) || got["beta"] != true || got["terminalSessions"] != 0.0 ||
		got["platform"] != "linux" || got["arch"] != servermgmt.Arch() {
		t.Errorf("info %v", got)
	}
	if !reflect.DeepEqual(got["server"], map[string]any{"running": true, "uptimeSeconds": 3723.0, "version": "3.5.264"}) {
		t.Errorf("server %v", got["server"])
	}
	want := []any{s.root + "/server", s.root + "/console", s.root + "/home", s.root + "/log", s.root + "/tmp"}
	if !reflect.DeepEqual(got["fileRoots"], want) || !reflect.DeepEqual(got["mutableFileRoots"], want[:2]) {
		t.Errorf("roots %v %v", got["fileRoots"], got["mutableFileRoots"])
	}

	// The server's folder as the update routes name it: RUSTDESK_PATH, as
	// the Docker image sets it, else /opt/betterdesk.
	delete(s.env, "BETTERDESK_PATH")
	for value, folder := range map[string]string{s.root + "/rd": s.root + "/rd", "": "/opt/betterdesk"} {
		s.env["RUSTDESK_PATH"] = value
		got := decode(t, s.call(s.signIn("admin"), "GET", "/api/server-management/info", "").Body.String())
		if read, write := got["fileRoots"].([]any), got["mutableFileRoots"].([]any); read[0] != folder || write[0] != folder {
			t.Errorf("RUSTDESK_PATH %q: roots %v %v", value, read, write)
		}
	}
}

// The server part of the info route for the health answers the oracle
// gave the old route (servermgmt/testdata/node_commands.json): running,
// the uptime from a Go duration, the version when it is truthy.
func TestNodeHealthVectors(t *testing.T) {
	raw, err := os.ReadFile("../../servermgmt/testdata/node_commands.json")
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		Health []struct {
			Health any `json:"health"`
			Server any `json:"server"`
		} `json:"health"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	for i, v := range rec.Health {
		if v.Health == "throws" {
			continue
		}
		var health *goapi.Object
		if o, ok := v.Health.(map[string]any); ok {
			b, _ := json.Marshal(o)
			health = &goapi.Object{}
			if err := json.Unmarshal(b, health); err != nil {
				t.Fatal(err)
			}
		} else {
			health = goapi.NewObject()
			if v.Health == nil {
				health = nil
			}
		}
		b, _ := json.Marshal(serverFromHealth(health))
		var got any
		_ = json.Unmarshal(b, &got)
		if !reflect.DeepEqual(got, v.Server) {
			t.Errorf("health %d %v: got %v, want %v", i, v.Health, got, v.Server)
		}
	}
	if len(rec.Health) < 40 {
		t.Errorf("only %d vectors", len(rec.Health))
	}
}
