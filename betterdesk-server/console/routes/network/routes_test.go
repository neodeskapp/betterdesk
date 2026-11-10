package network

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/netmonitor"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The accounts of the tests, as in testdata/nodeoracle/routes.js.
var accounts = map[string]session.User{
	"root": {ID: 1, Username: "root", Role: "super_admin"},
	"adm":  {ID: 2, Username: "adm", Role: "admin"},
	"sara": {ID: 3, Username: "sara", Role: "server_admin"},
	"gabi": {ID: 4, Username: "gabi", Role: "global_admin"},
	"olga": {ID: 5, Username: "olga", Role: "operator"},
	"vera": {ID: 6, Username: "vera", Role: "viewer"},
	"gus":  {ID: 7, Username: "gus", Role: "guest_issuer"},
	"paul": {ID: 8, Username: "paul", Role: "pro"},
}

// names answers lookups from the table of the oracle scripts: a list of
// addresses for a name, or an object for a name whose lookup fails.
type names map[string]json.RawMessage

func (n names) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	var addresses []string
	raw, ok := n[host]
	if ok && json.Unmarshal(raw, &addresses) != nil {
		again := strings.Contains(string(raw), "EAI_AGAIN")
		return nil, &net.DNSError{Err: "lookup failed", Name: host, IsTemporary: again, IsNotFound: !again}
	}
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	out := make([]net.IPAddr, len(addresses))
	for i, a := range addresses {
		out[i] = net.IPAddr{IP: net.ParseIP(a)}
	}
	return out, nil
}

// nothingListens is a dialer on a machine where no port is open.
type nothingListens struct{}

func (nothingListens) DialContext(_ context.Context, network, _ string) (net.Conn, error) {
	return nil, &net.OpError{Op: "dial", Net: network, Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
}

// noPing is the ping command of a machine that has none.
func noPing(context.Context, string, ...string) ([]byte, int, error) {
	return nil, -1, errors.New("spawn ping ENOENT")
}

// runNow starts a job at once and stops it with the test.
type runNow struct {
	t *testing.T
}

func (j runNow) Add(_ string, run func(ctx context.Context)) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		run(ctx)
	}()
	j.t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			j.t.Error("the monitor's job did not end with its context")
		}
	})
}

// world is the group on a router with sessions on a test database, on a
// machine without open ports and without a ping command.
type world struct {
	t       *testing.T
	db      *store.DB
	handler http.Handler
	monitor *netmonitor.Monitor
	mu      sync.Mutex
	cookies map[string]string
	// pinged are the arguments the ping command was called with.
	pinged []string
}

func newWorld(t *testing.T, db *store.DB, table names) *world {
	t.Helper()
	w := &world{t: t, db: db, cookies: map[string]string{}}
	sessions, err := session.New(context.Background(), session.Options{DB: db, Secret: "network-test-secret-0123456789abcdef0123456789"})
	if err != nil {
		t.Fatal(err)
	}
	no := false
	w.monitor = netmonitor.New(netmonitor.Options{Store: db, Resolver: table, Dialer: nothingListens{}, Windows: &no,
		Ping: func(ctx context.Context, name string, args ...string) ([]byte, int, error) {
			w.mu.Lock()
			w.pinged = append(w.pinged, strings.Join(args, " "))
			w.mu.Unlock()
			return noPing(ctx, name, args...)
		},
	})
	d := &kit.Deps{Router: kit.NewRouter(), Store: db, Sessions: sessions, Log: kit.NewLogger("error", true), Jobs: runNow{t: t}}
	register(d.At("/api/network"), w.monitor)
	d.Router.Get("/signin/{name}", func(rw http.ResponseWriter, r *http.Request) {
		s := kit.Session(r)
		if err := s.Regenerate(); err != nil {
			t.Error(err)
		}
		if _, err := s.Establish(accounts[r.PathValue("name")], session.Meta{}); err != nil {
			t.Error(err)
		}
	})
	routes := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if !d.Router.Serve(rw, kit.Begin(r, httpx.TrustProxy{}, nil)) {
			http.NotFound(rw, r)
		}
	})
	w.handler = middleware.Bodies(middleware.BodyOptions{})(sessions.Middleware(middleware.RequireJSON()(routes)))
	return w
}

// do sends a request as the account ("" for signed out). hasBody is false
// for a request without a body; contentType "" means JSON.
func (w *world) do(as, method, target string, body *string, contentType string) *httptest.ResponseRecorder {
	w.t.Helper()
	if as != "" && w.cookies[as] == "" {
		rec := httptest.NewRecorder()
		w.handler.ServeHTTP(rec, httptest.NewRequest("GET", "/signin/"+as, nil))
		pair, _, _ := strings.Cut(rec.Header().Get("Set-Cookie"), ";")
		if !strings.Contains(pair, "=") {
			w.t.Fatalf("sign in as %s set the cookie %q", as, pair)
		}
		w.cookies[as] = pair
	}
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, target, strings.NewReader(*body))
		if contentType == "" {
			contentType = "application/json"
		}
		r.Header.Set("Content-Type", contentType)
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	if as != "" {
		r.Header.Set("Cookie", w.cookies[as])
	}
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, r)
	return rec
}

// checks counts the stored checks.
func (w *world) checks() int {
	w.t.Helper()
	var n int
	if err := w.db.QueryRow(context.Background(), "SELECT COUNT(*) FROM network_checks").Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

// settle waits until the monitor's poll has stored its last check.
func (w *world) settle() {
	w.t.Helper()
	last, since := w.checks(), time.Now()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
		if n := w.checks(); n != last {
			last, since = n, time.Now()
		} else if time.Since(since) > 400*time.Millisecond && !w.monitor.PollInFlight() {
			return
		}
	}
	w.t.Fatal("the monitor's poll does not come to an end")
}

// step is one line of testdata/routes.<database>.json: a request to the
// Node console's routes and what they answered (testdata/nodeoracle).
type step struct {
	DNS      names   `json:"dns"`
	Name     string  `json:"name"`
	As       *string `json:"as"`
	Method   string  `json:"method"`
	Path     string  `json:"path"`
	Body     *string `json:"body"`
	BodyType string  `json:"bodyType"`
	Status   int     `json:"status"`
	Type     string  `json:"type"`
	Text     string  `json:"text"`
}

func readSteps(t *testing.T, file string) (names, []step) {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var steps []step
	lines := bufio.NewScanner(f)
	lines.Buffer(make([]byte, 1<<20), 1<<26)
	for lines.Scan() {
		var s step
		if err := json.Unmarshal(lines.Bytes(), &s); err != nil {
			t.Fatal(err)
		}
		steps = append(steps, s)
	}
	if err := lines.Err(); err != nil {
		t.Fatal(err)
	}
	if len(steps) < 700 || steps[0].DNS == nil {
		t.Fatalf("%s: %d lines, want the table of names and more than 700 steps", file, len(steps))
	}
	return steps[0].DNS, steps[1:]
}

var times = regexp.MustCompile(`\d{4}-\d\d-\d\d[ T]\d\d:\d\d:\d\d(\.\d{3}Z)?`)

// maskTimes replaces every time by a word that keeps its form.
func maskTimes(text string) string {
	return times.ReplaceAllStringFunc(text, func(m string) string {
		if strings.HasSuffix(m, "Z") {
			return "<iso time>"
		}
		return "<time>"
	})
}

// orderSameNames puts neighbours with the same name in a list of targets
// into the order of their ids, and leaves every other byte as it is. The
// list is ordered by name only, so PostgreSQL returns targets of one name
// in the order it finds them, which differs from run to run (NM12).
func orderSameNames(text string) string {
	var list []json.RawMessage
	if !strings.HasPrefix(text, "[{") || json.Unmarshal([]byte(text), &list) != nil {
		return text
	}
	type key struct {
		ID   int64   `json:"id"`
		Name *string `json:"name"`
	}
	keys := make([]key, len(list))
	for i, raw := range list {
		if json.Unmarshal(raw, &keys[i]) != nil || keys[i].Name == nil {
			return text
		}
	}
	join := func(from, to int) string {
		parts := make([]string, 0, to-from)
		for _, raw := range list[from:to] {
			parts = append(parts, string(raw))
		}
		return strings.Join(parts, ",")
	}
	for from := 0; from < len(list); {
		to := from + 1
		for to < len(list) && *keys[to].Name == *keys[from].Name {
			to++
		}
		if to-from > 1 {
			before := join(from, to)
			run, runKeys := list[from:to], keys[from:to]
			for i := 1; i < len(run); i++ {
				for j := i; j > 0 && runKeys[j].ID < runKeys[j-1].ID; j-- {
					run[j], run[j-1] = run[j-1], run[j]
					runKeys[j], runKeys[j-1] = runKeys[j-1], runKeys[j]
				}
			}
			text = strings.Replace(text, before, join(from, to), 1)
		}
		from = to
	}
	return text
}

func TestOrderSameNames(t *testing.T) {
	a, b, c := `{"id":23,"name":"{}","x":1}`, `{"id":15,"name":"{}","x":2}`, `{"id":4,"name":"z"}`
	if got, want := orderSameNames("["+a+","+b+","+c+"]"), "["+b+","+a+","+c+"]"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	// Another order of names stays a difference
	if in := "[" + c + "," + a + "]"; orderSameNames(in) != in {
		t.Errorf("%s was changed", in)
	}
	if in := `{"error":"x"}`; orderSameNames(in) != in {
		t.Errorf("%s was changed", in)
	}
}

// TestNodeSteps replays the steps the Node console answered, in order, on
// the database they were recorded on. Times differ from run to run and
// are compared by their form; ids are the same, since both start from an
// empty database.
func TestNodeSteps(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		file := "testdata/routes.sqlite.json"
		if db.IsPostgres() {
			file = "testdata/routes.postgres.json"
		}
		table, steps := readSteps(t, file)
		w := newWorld(t, db, table)
		for _, s := range steps {
			switch s.Method {
			case "WAIT":
				w.settle()
				continue
			case "SPREAD":
				// The checks so far get times a minute apart, in the order
				// they were made, as the script gave them: a check's time
				// has seconds only on SQLite, and checks of the same second
				// have no order of their own.
				spread := "UPDATE network_checks SET checked_at = datetime('2026-01-01 00:00:00', '+' || id || ' minutes')"
				if db.IsPostgres() {
					spread = "UPDATE network_checks SET checked_at = TIMESTAMPTZ '2026-01-01 00:00:00+00' + id * INTERVAL '1 minute'"
				}
				if _, err := db.Exec(context.Background(), spread); err != nil {
					t.Fatal(err)
				}
				continue
			}
			as := ""
			if s.As != nil {
				as = *s.As
			}
			rec := w.do(as, s.Method, s.Path, s.Body, s.BodyType)
			if strings.HasPrefix(s.Type, "text/html") {
				// Not a route of the group: the old console answered with
				// its page for that
				if rec.Code != http.StatusNotFound {
					t.Errorf("%s: status %d, want no route", s.Name, rec.Code)
				}
				continue
			}
			if rec.Code != s.Status {
				t.Errorf("%s: status %d, Node has %d: %s", s.Name, rec.Code, s.Status, firstDifference(rec.Body.String(), s.Text))
				continue
			}
			if got := rec.Header().Get("Content-Type"); got != s.Type {
				t.Errorf("%s: Content-Type %q, Node has %q", s.Name, got, s.Type)
			}
			if s.Method == "HEAD" {
				continue
			}
			if got, want := orderSameNames(maskTimes(rec.Body.String())), orderSameNames(maskTimes(s.Text)); got != want {
				t.Errorf("%s: %s", s.Name, firstDifference(got, want))
			}
		}
	})
}

func TestRoutesInTheOrderOfTheOldFile(t *testing.T) {
	d := &kit.Deps{Router: kit.NewRouter(), Log: kit.NewLogger("error", true)}
	Register(d.At("/api/network"))
	var got []string
	for _, route := range d.Router.Routes() {
		got = append(got, route.Method+" "+route.Path)
	}
	want := []string{
		"GET /api/network/targets",
		"POST /api/network/targets",
		"GET /api/network/targets/{id}",
		"PATCH /api/network/targets/{id}",
		"DELETE /api/network/targets/{id}",
		"POST /api/network/targets/{id}/check",
		"GET /api/network/targets/{id}/history",
		"GET /api/network/stats",
		"POST /api/network/ping",
		"POST /api/network/tcp",
		"POST /api/network/http",
		"POST /api/network/resolve",
		"POST /api/network/monitor/start",
		"POST /api/network/monitor/stop",
		"GET /api/network/monitor/status",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The monitor's loop is one background job. It is added with the routes
// and checks nothing until somebody starts the monitor.
func TestMonitorJobWaitsForStart(t *testing.T) {
	db := storetest.Migrated(t)
	w := newWorld(t, db, names{})
	ctx := context.Background()
	if _, err := db.Exec(ctx, "INSERT INTO network_targets (name, host, check_type, interval_ms) VALUES ('t', '0.0.0.0', 'ping', 1)"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if n := w.checks(); n != 0 {
		t.Fatalf("%d checks before the monitor was started", n)
	}
	body := `{"interval_ms":3600000}`
	if rec := w.do("adm", "POST", "/api/network/monitor/start", &body, ""); rec.Body.String() != `{"status":"started","interval_ms":3600000}` {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	w.settle()
	if n := w.checks(); n != 1 {
		t.Errorf("%d checks after the start, want the one of the first poll", n)
	}
	empty := "{}"
	if rec := w.do("adm", "POST", "/api/network/monitor/stop", &empty, ""); rec.Body.String() != `{"status":"stopped"}` {
		t.Fatalf("stop: %d %s", rec.Code, rec.Body.String())
	}
	if rec := w.do("vera", "GET", "/api/network/monitor/status", nil, ""); rec.Body.String() != `{"running":false,"interval_ms":3600000}` {
		t.Errorf("status: %d %s", rec.Code, rec.Body.String())
	}
}

// A job registry that is not there (a test of another package that builds
// the routes without one) does not stop the routes from registering.
func TestRegisterWithoutJobs(t *testing.T) {
	d := &kit.Deps{Router: kit.NewRouter(), Log: kit.NewLogger("error", true)}
	Register(d.At("/api/network"))
	if len(d.Router.Routes()) != 15 {
		t.Errorf("%d routes, want 15", len(d.Router.Routes()))
	}
}

// firstDifference shows where two texts part, with some text around it.
func firstDifference(got, want string) string {
	i := 0
	for i < len(got) && i < len(want) && got[i] == want[i] {
		i++
	}
	window := func(s string) string {
		from, to := max(i-160, 0), min(i+120, len(s))
		return s[from:to]
	}
	return "at byte " + strconv.Itoa(i) + "\n got ..." + window(got) + "\nNode ..." + window(want)
}

// The ping tool waits five seconds unless the body names a timeout, and
// hands the host to the command as it came.
func TestPingToolArguments(t *testing.T) {
	w := newWorld(t, storetest.Migrated(t), names{"internal.corp": json.RawMessage(`["10.1.2.3"]`)})
	for body, want := range map[string]string{
		`{"host":"10.1.2.3"}`:                       "-c 1 -W 5 10.1.2.3",
		`{"host":"10.1.2.3","timeout":"2500ms"}`:    "-c 1 -W 3 10.1.2.3",
		`{"host":"10.1.2.3","timeout":0}`:           "-c 1 -W 5 10.1.2.3",
		`{"host":"Internal.Corp","timeout":[1000]}`: "-c 1 -W 1 Internal.Corp",
		`{"host":"10.1.2.3","timeout":"soon"}`:      "-c 1 -W 5 10.1.2.3",
	} {
		w.pinged = nil
		rec := w.do("olga", "POST", "/api/network/ping", &body, "")
		if rec.Code != http.StatusOK || len(w.pinged) != 1 || w.pinged[0] != want {
			t.Errorf("%s: status %d, ping got %q, want %q", body, rec.Code, w.pinged, want)
		}
	}
	// A host the guard refuses never reaches the command
	for _, body := range []string{`{"host":"127.0.0.1"}`, `{"host":"localhost"}`, `{"host":"-c5"}`, `{"host":"a;id"}`, `{"host":5}`, `{"host":["10.1.2.3"]}`} {
		w.pinged = nil
		if rec := w.do("olga", "POST", "/api/network/ping", &body, ""); rec.Code != http.StatusOK || len(w.pinged) != 0 {
			t.Errorf("%s: status %d, ping got %q, want no call", body, rec.Code, w.pinged)
		}
	}
}

// deadlineDialer notes how long a connection may take.
type deadlineDialer struct {
	mu    sync.Mutex
	limit []time.Duration
}

func (d *deadlineDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	deadline, ok := ctx.Deadline()
	d.mu.Lock()
	if ok {
		d.limit = append(d.limit, time.Until(deadline))
	} else {
		d.limit = append(d.limit, 0)
	}
	d.mu.Unlock()
	return nothingListens{}.DialContext(ctx, network, address)
}

// The TCP tool gives a connection five seconds unless the body names a
// timeout.
func TestTCPToolTimeout(t *testing.T) {
	db := storetest.Migrated(t)
	dialer := &deadlineDialer{}
	no := false
	h := &handlers{db: db, monitor: netmonitor.New(netmonitor.Options{Store: db, Resolver: names{}, Dialer: dialer, Ping: noPing, Windows: &no})}
	call := func(body string) time.Duration {
		r := httptest.NewRequest("POST", "/api/network/tcp", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		dialer.limit = nil
		middleware.Bodies(middleware.BodyOptions{})(http.HandlerFunc(h.tcp)).ServeHTTP(httptest.NewRecorder(), r)
		if len(dialer.limit) != 1 {
			t.Fatalf("%s: %d connections", body, len(dialer.limit))
		}
		return dialer.limit[0]
	}
	for body, want := range map[string]time.Duration{
		`{"host":"10.1.2.3","port":80}`:                  5 * time.Second,
		`{"host":"10.1.2.3","port":80,"timeout":"1500"}`: 1500 * time.Millisecond,
		`{"host":"10.1.2.3","port":80,"timeout":0}`:      5 * time.Second,
		`{"host":"10.1.2.3","port":80,"timeout":"x"}`:    5 * time.Second,
	} {
		if got := call(body); got > want || got < want-time.Second {
			t.Errorf("%s: the connection may take %s, want %s", body, got, want)
		}
	}
}
