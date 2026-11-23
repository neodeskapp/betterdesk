package cdapstudio

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// The accounts of the tests, as in the recording testdata/flows.json.
var accounts = map[string]session.User{
	"alice": {ID: 2, Username: "alice", Role: "operator"},
	"bob":   {ID: 3, Username: "bob", Role: "operator"},
	"vera":  {ID: 4, Username: "vera", Role: "viewer"},
	"root":  {ID: 1, Username: "root", Role: "super_admin"},
	"adm":   {ID: 5, Username: "adm", Role: "admin"},
	"gabi":  {ID: 6, Username: "gabi", Role: "global_admin"},
}

// counter gives the ids of new flows in a fixed order: a0a1a2a3a4a50000,
// a0a1a2a3a4a50001 and so on.
type counter struct{ next uint64 }

func (c *counter) Read(p []byte) (int, error) {
	id := make([]byte, 8)
	binary.BigEndian.PutUint64(id, 0xa0a1a2a3a4a50000+c.next)
	c.next++
	return copy(p, id), nil
}

// world is the group on a router with sessions on a test database. Its
// clock moves one second with every reading.
type world struct {
	t       *testing.T
	db      *store.DB
	handler http.Handler
	cookies map[string]string
	clock   time.Time
}

func newWorld(t *testing.T, db *store.DB) *world {
	t.Helper()
	w := &world{t: t, db: db, cookies: map[string]string{}, clock: time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)}
	sessions, err := session.New(context.Background(), session.Options{DB: db, Secret: "studio-test-secret-0123456789abcdef0123456789"})
	if err != nil {
		t.Fatal(err)
	}
	d := &kit.Deps{Router: kit.NewRouter(), Store: db, Sessions: sessions, Log: kit.NewLogger("error", true)}
	register(d, func() time.Time {
		w.clock = w.clock.Add(time.Second)
		return w.clock
	}, &counter{})
	d.Router.Get("/signin/{name}", func(rw http.ResponseWriter, r *http.Request) {
		s := kit.Session(r)
		if err := s.Regenerate(); err != nil {
			t.Error(err)
		}
		if _, err := s.Establish(accounts[r.PathValue("name")], session.Meta{}); err != nil {
			t.Error(err)
		}
	})
	w.handler = middleware.Bodies(middleware.BodyOptions{})(sessions.Middleware(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if !d.Router.Serve(rw, kit.Begin(r, httpx.TrustProxy{}, nil)) {
			http.NotFound(rw, r)
		}
	})))
	return w
}

// do sends a request as the account ("" for signed out). contentType ""
// with a body means JSON.
func (w *world) do(as, method, target, body, contentType string) *httptest.ResponseRecorder {
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
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		if contentType == "" {
			contentType = "application/json"
		}
		r.Header.Set("Content-Type", contentType)
	}
	if as != "" {
		r.Header.Set("Cookie", w.cookies[as])
	}
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, r)
	return rec
}

func expect(t *testing.T, what string, rec *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	if rec.Code != status || rec.Body.String() != body {
		t.Errorf("%s: %d %s, want %d %s", what, rec.Code, rec.Body.String(), status, body)
	}
}

// step is one line of testdata/flows.json: a request to the Node
// console's routes and what they answered (testdata/README.md).
type step struct {
	Name        string  `json:"name"`
	As          *string `json:"as"`
	Method      string  `json:"method"`
	Path        string  `json:"path"`
	Save        string  `json:"save"`
	ID          string  `json:"id"`
	Body        *string `json:"body"`
	BodyType    string  `json:"bodyType"`
	Status      int     `json:"status"`
	Type        string  `json:"type"`
	Disposition string  `json:"disposition"`
	Text        *string `json:"text"`
	Length      int     `json:"length"`
}

var (
	isoTime     = regexp.MustCompile(`\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z`)
	placeholder = regexp.MustCompile(`\{(\w+)\}`)
)

// stepDifferences are the steps of testdata/flows.json a bug fix answers
// differently on purpose, with what Go answers now. A step that saved a
// flow in Node and is refused now leaves its name unsaved, so the steps
// after it ask for a flow that does not exist.
var stepDifferences = map[string]struct {
	status int
	text   string
}{
	// ST4: the flow of somebody else is not found, also when it is changed
	"update by another operator": {404, `{"success":false,"error":"Flow not found"}`},
	"deploy by another operator": {404, `{"success":false,"error":"Flow not found"}`},
	"delete by another operator": {404, `{"success":false,"error":"Flow not found"}`},
	// ST2: nodes and wires count only as lists, and a node that is no
	// object or whose type is no text gets no result
	"test: nodes of every kind":                   {200, `{"success":true,"results":[{"nodeId":1,"nodeType":2,"label":3,"status":"ok","output":{"_simulated":true,"value":"Mock data from 2"}},{"nodeId":null,"nodeType":null,"label":null,"status":"ok","output":{"_simulated":true,"value":"Mock data from null"}},{"nodeId":"a","nodeType":"b","label":"b","status":"ok","output":{"_simulated":true,"value":"Mock data from b"}},{"nodeId":"a","nodeType":"b","label":"b","status":"ok","output":{"_simulated":true,"value":"Mock data from b"}},{"nodeId":"a","nodeType":"b","label":[],"status":"ok","output":{"_simulated":true,"value":"Mock data from b"}},{"nodeId":{"x":1},"nodeType":["t",1,null,[2,[3]],{}],"label":{},"status":"ok","output":{"_simulated":true,"value":"Mock data from t,1,,2,3,[object Object]"}},{"status":"ok","output":{"_simulated":true,"value":"Mock data from undefined"}},{"nodeType":true,"label":true,"status":"ok","output":{"_simulated":true,"value":"Mock data from true"}},{"nodeType":1e+21,"label":1e+21,"status":"ok","output":{"_simulated":true,"value":"Mock data from 1e+21"}},{"nodeType":0,"label":0,"status":"ok","output":{"_simulated":true,"value":"Mock data from 0"}},{"nodeType":0.1,"label":0.1,"status":"ok","output":{"_simulated":true,"value":"Mock data from 0.1"}},{"nodeType":1e-7,"label":1e-7,"status":"ok","output":{"_simulated":true,"value":"Mock data from 1e-7"}},{"nodeType":null,"label":null,"status":"ok","output":{"_simulated":true,"value":"Mock data from null"}},{"nodeType":null,"label":null,"status":"ok","output":{"_simulated":true,"value":"Mock data from null"}},{"nodeId":"last","status":"ok","output":{"_simulated":true,"value":"Mock data from undefined"}}],"nodeCount":21,"wireCount":3}`},
	"test: a null node":                           {200, `{"success":true,"results":[{"nodeId":"n1","status":"ok","output":{"_simulated":true,"value":"Mock data from undefined"}}],"nodeCount":2,"wireCount":0}`},
	"test: a node type with toString":             {200, `{"success":true,"results":[],"nodeCount":1,"wireCount":0}`},
	"test: a node type list with toString inside": {200, `{"success":true,"results":[],"nodeCount":1,"wireCount":0}`},
	"test: nodes as text":                         {200, `{"success":true,"results":[],"nodeCount":0,"wireCount":0}`},
	"test: nodes as a number":                     {200, `{"success":true,"results":[],"nodeCount":0,"wireCount":0}`},
	"test: nodes as true":                         {200, `{"success":true,"results":[],"nodeCount":0,"wireCount":0}`},
	"test: nodes as an object":                    {200, `{"success":true,"results":[],"nodeCount":0,"wireCount":0}`},
	"test: wires as a number":                     {200, `{"success":true,"results":[],"nodeCount":0,"wireCount":0}`},
	"test: wires as true":                         {200, `{"success":true,"results":[],"nodeCount":0,"wireCount":0}`},
	"test: wires as an object with length":        {200, `{"success":true,"results":[],"nodeCount":0,"wireCount":0}`},
	"test: wires as an object with an odd length": {200, `{"success":true,"results":[],"nodeCount":0,"wireCount":0}`},
	"test: wires as an object without length":     {200, `{"success":true,"results":[],"nodeCount":0,"wireCount":0}`},
	"test: wires with a null length":              {200, `{"success":true,"results":[],"nodeCount":0,"wireCount":0}`},
	// ST1: a flow nested deeper than 200 levels is not JSON
	"flow text nested 1000 deep": {400, `{"success":false,"error":"Invalid flow JSON"}`},
	"export nested 1000 deep":    {404, `{"success":false,"error":"Flow not found"}`},
	"delete nested":              {404, `{"success":false,"error":"Flow not found"}`},
}

// TestNodeSteps replays the steps the Node console answered, in order, on
// both databases, but for the steps in stepDifferences. Ids and times
// differ from run to run: an id is compared by the step that made it, a
// time by its form.
func TestNodeSteps(t *testing.T) {
	file, err := os.Open("testdata/flows.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var steps []step
	lines := bufio.NewScanner(file)
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
	if len(steps) < 250 {
		t.Fatalf("read %d steps, the file has more than 250", len(steps))
	}

	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db)
		mine, theirs := map[string]string{}, map[string]string{}
		// mask replaces the ids of saved flows by the names they were
		// saved under, and every time by a word
		mask := func(text string, ids map[string]string) string {
			for name, id := range ids {
				text = strings.ReplaceAll(text, id, "<"+name+">")
			}
			return isoTime.ReplaceAllString(text, "<time>")
		}
		for _, s := range steps {
			target := placeholder.ReplaceAllStringFunc(s.Path, func(match string) string {
				key := match[1 : len(match)-1]
				if key == "A1" {
					return strings.ToUpper(mine["a1"])
				}
				if id, ok := mine[key]; ok {
					return id
				}
				return "unsaved"
			})
			as, body := "", ""
			if s.As != nil {
				as = *s.As
			}
			if s.Body != nil {
				body = *s.Body
			}
			rec := w.do(as, s.Method, target, body, s.BodyType)
			if db.IsPostgres() && s.Method != "GET" {
				// The database's clock orders the list
				time.Sleep(2 * time.Millisecond)
			}
			if fixed, ok := stepDifferences[s.Name]; ok {
				if got := mask(rec.Body.String(), mine); rec.Code != fixed.status || got != fixed.text {
					t.Errorf("%s: %d %s, the fix answers %d %s", s.Name, rec.Code, got, fixed.status, fixed.text)
				}
				if s.Save != "" && rec.Code == http.StatusOK {
					t.Errorf("%s: the fix saves the flow; the step belongs in the replay", s.Name)
				}
				continue
			}
			if s.Save != "" {
				var answer struct {
					ID string `json:"id"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil || answer.ID == "" {
					t.Fatalf("%s: no id in %d %s", s.Name, rec.Code, rec.Body.String())
				}
				mine[s.Save], theirs[s.Save] = answer.ID, s.ID
			}
			if rec.Code != s.Status {
				t.Errorf("%s: status %d, Node has %d (%s)", s.Name, rec.Code, s.Status, rec.Body.String())
				continue
			}
			if got := rec.Header().Get("Content-Type"); got != s.Type {
				t.Errorf("%s: Content-Type %q, Node has %q", s.Name, got, s.Type)
			}
			if got := rec.Header().Get("Content-Disposition"); got != s.Disposition {
				t.Errorf("%s: Content-Disposition %q, Node has %q", s.Name, got, s.Disposition)
			}
			got := rec.Body.String()
			if s.Text == nil {
				if n := units(got); n != s.Length {
					t.Errorf("%s: %d characters, Node has %d", s.Name, n, s.Length)
				}
				continue
			}
			if got, want := mask(got, mine), mask(*s.Text, theirs); got != want {
				t.Errorf("%s:\n got %s\nNode %s", s.Name, got, want)
			}
		}
	})
}

func TestRoutesInTheOrderOfTheOldFile(t *testing.T) {
	d := &kit.Deps{Router: kit.NewRouter(), Log: kit.NewLogger("error", true)}
	Register(d)
	var got []string
	for _, route := range d.Router.Routes() {
		got = append(got, route.Method+" "+route.Path)
	}
	want := []string{
		"GET /api/cdap-studio/flows",
		"GET /api/cdap-studio/flows/{id}",
		"POST /api/cdap-studio/flows",
		"PUT /api/cdap-studio/flows/{id}",
		"DELETE /api/cdap-studio/flows/{id}",
		"POST /api/cdap-studio/flows/{id}/deploy",
		"POST /api/cdap-studio/flows/{id}/test",
		"GET /api/cdap-studio/flows/{id}/export",
		"POST /api/cdap-studio/flows/import",
		"GET /api/cdap-studio/templates",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Reading needs a session; writing, the dry run included, needs the role
// operator or a super admin role. The role is checked before the flow is
// looked up, so a viewer learns nothing about an id.
func TestGuards(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db)
		expect(t, "create", w.do("alice", "POST", "/api/cdap-studio/flows", `{"name":"Mine","flow_json":"{}"}`, ""), 200, `{"success":true,"id":"a0a1a2a3a4a50000"}`)
		const (
			id        = "a0a1a2a3a4a50000"
			signedOut = `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`
			forbidden = `{"success":false,"error":"Forbidden"}`
		)
		reads := [][2]string{
			{"GET", "/api/cdap-studio/flows"},
			{"GET", "/api/cdap-studio/flows/" + id},
			{"GET", "/api/cdap-studio/flows/" + id + "/export"},
			{"GET", "/api/cdap-studio/templates"},
		}
		writes := [][2]string{
			{"POST", "/api/cdap-studio/flows"},
			{"PUT", "/api/cdap-studio/flows/" + id},
			{"DELETE", "/api/cdap-studio/flows/" + id},
			{"POST", "/api/cdap-studio/flows/" + id + "/deploy"},
			{"POST", "/api/cdap-studio/flows/" + id + "/test"},
			{"POST", "/api/cdap-studio/flows/import"},
			{"PUT", "/api/cdap-studio/flows/unknown"},
			{"DELETE", "/api/cdap-studio/flows/unknown"},
		}
		for _, c := range append(reads, writes...) {
			body := ""
			if c[0] != "GET" && c[0] != "DELETE" {
				body = `{"name":"x","flow":{}}`
			}
			expect(t, c[0]+" "+c[1]+" signed out", w.do("", c[0], c[1], body, ""), 401, signedOut)
		}
		for _, c := range writes {
			body := ""
			if c[0] != "DELETE" {
				body = `{"name":"x","flow":{}}`
			}
			for _, as := range []string{"vera", "gabi"} {
				expect(t, c[0]+" "+c[1]+" as "+as, w.do(as, c[0], c[1], body, ""), 403, forbidden)
			}
		}
		for _, c := range reads {
			if rec := w.do("vera", c[0], c[1], "", ""); rec.Code == 401 || rec.Code == 403 {
				t.Errorf("%s %s as a viewer: %d %s", c[0], c[1], rec.Code, rec.Body.String())
			}
		}
		// Nothing was written or removed by the refused requests
		rec := w.do("root", "GET", "/api/cdap-studio/flows", "", "")
		if n := strings.Count(rec.Body.String(), `"id":`); n != 1 || !strings.Contains(rec.Body.String(), `"name":"Mine"`) || !strings.Contains(rec.Body.String(), `"status":"draft"`) {
			t.Errorf("after the refused requests: %s", rec.Body.String())
		}
	})
}

// A row is stored the way the old console stored it: times as ISO text
// on SQLite, the version counted up by every change, the status only
// changed by deploy.
func TestStoredRow(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db)
		ctx := context.Background()
		w.do("alice", "POST", "/api/cdap-studio/flows", `{"name":" A ","description":" d ","flow_json":{"b":1,"a":[]}}`, "")
		const id = "a0a1a2a3a4a50000"
		read := func() (name, description, text, status, owner, created, updated string, version int64) {
			t.Helper()
			query := "SELECT name, description, flow_json, status, version, created_by, created_at, updated_at FROM studio_flows WHERE id = ?"
			if db.IsPostgres() {
				query = "SELECT name, description, flow_json, status, version, created_by, created_at::text, updated_at::text FROM studio_flows WHERE id = ?"
			}
			if err := db.QueryRow(ctx, query, id).Scan(&name, &description, &text, &status, &version, &owner, &created, &updated); err != nil {
				t.Fatal(err)
			}
			return
		}
		name, description, text, status, owner, created, updated, version := read()
		if name != "A" || description != "d" || text != `{"b":1,"a":[]}` || status != "draft" || owner != "alice" || version != 1 {
			t.Errorf("new row: %q %q %q %q %q %d", name, description, text, status, owner, version)
		}
		if created != updated {
			t.Errorf("new row: created %q, updated %q", created, updated)
		}
		if !db.IsPostgres() && created != "2026-11-10T08:00:01.000Z" {
			t.Errorf("new row on SQLite: created %q, want the clock's time as ISO text", created)
		}

		if db.IsPostgres() {
			time.Sleep(5 * time.Millisecond)
		}
		expect(t, "update", w.do("alice", "PUT", "/api/cdap-studio/flows/"+id, `{"name":"B"}`, ""), 200, `{"success":true}`)
		name, description, text, status, _, created2, changed, version := read()
		if name != "B" || description != "d" || text != `{"b":1,"a":[]}` || status != "draft" || version != 2 {
			t.Errorf("changed row: %q %q %q %q %d", name, description, text, status, version)
		}
		if created2 != created || changed == created {
			t.Errorf("changed row: created %q (was %q), updated %q", created2, created, changed)
		}
		if !db.IsPostgres() && changed != "2026-11-10T08:00:02.000Z" {
			t.Errorf("changed row on SQLite: updated %q", changed)
		}

		if db.IsPostgres() {
			time.Sleep(5 * time.Millisecond)
		}
		expect(t, "deploy", w.do("alice", "POST", "/api/cdap-studio/flows/"+id+"/deploy", `{}`, ""), 200, `{"success":true,"status":"deployed"}`)
		name, _, text, status, _, _, updated, version = read()
		if name != "B" || text != `{"b":1,"a":[]}` || status != "deployed" || version != 2 {
			t.Errorf("deployed row: %q %q %q %d", name, text, status, version)
		}
		if updated == changed {
			t.Errorf("deployed row: updated %q, as before", updated)
		}
		if !db.IsPostgres() && updated != "2026-11-10T08:00:03.000Z" {
			t.Errorf("deployed row on SQLite: updated %q", updated)
		}

		expect(t, "delete", w.do("alice", "DELETE", "/api/cdap-studio/flows/"+id, "", ""), 200, `{"success":true}`)
		var n int
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM studio_flows").Scan(&n); err != nil || n != 0 {
			t.Errorf("after delete: %d rows, %v", n, err)
		}
	})
}

func TestExportFile(t *testing.T) {
	w := newWorld(t, storetest.Migrated(t))
	w.do("alice", "POST", "/api/cdap-studio/flows", `{"name":"Füll-level №1_a","description":"d","flow_json":{"nodes":[{"id":"n1"}],"wires":[]}}`, "")
	rec := w.do("alice", "GET", "/api/cdap-studio/flows/a0a1a2a3a4a50000/export", "", "")
	want := `{
  "version": "1.0",
  "format": "bdflow",
  "name": "Füll-level №1_a",
  "description": "d",
  "flow": {
    "nodes": [
      {
        "id": "n1"
      }
    ],
    "wires": []
  },
  "exportedAt": "2026-11-10T08:00:02.000Z",
  "exportedBy": "alice"
}`
	expect(t, "export", rec, 200, want)
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="F_ll-level__1_a.bdflow"` {
		t.Errorf("file name: %q", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("type: %q", got)
	}
	// A flow whose stored text is not JSON cannot be exported or run
	if _, err := w.db.Exec(context.Background(), "UPDATE studio_flows SET flow_json = ? WHERE id = ?", "{nope", "a0a1a2a3a4a50000"); err != nil {
		t.Fatal(err)
	}
	expect(t, "export of broken JSON", w.do("alice", "GET", "/api/cdap-studio/flows/a0a1a2a3a4a50000/export", "", ""), 400, `{"success":false,"error":"Invalid flow JSON"}`)
	expect(t, "test of broken JSON", w.do("alice", "POST", "/api/cdap-studio/flows/a0a1a2a3a4a50000/test", "{}", ""), 400, `{"success":false,"error":"Invalid flow JSON"}`)
	expect(t, "get of broken JSON", w.do("alice", "GET", "/api/cdap-studio/flows/a0a1a2a3a4a50000", "", ""), 200,
		`{"success":true,"flow":{"id":"a0a1a2a3a4a50000","name":"Füll-level №1_a","description":"d","flow_json":"{nope","status":"draft","version":1,"created_by":"alice","created_at":"2026-11-10T08:00:01.000Z","updated_at":"2026-11-10T08:00:01.000Z"}}`)
}

// A flow whose stored text has more bytes than a request body may have
// (2 MiB) is refused. A request body ends there, so only numbers that grow
// when they are written out get past it. Each 1e20 is stored as 21
// digits: n of them in a list are 22n+1 bytes.
func TestFlowTooLarge(t *testing.T) {
	w := newWorld(t, storetest.Migrated(t))
	tooLarge := `{"success":false,"error":"Flow data too large (max 2MB)"}`
	numbers := func(n int) string { return "[" + strings.Repeat("1e20,", n-1) + "1e20]" }
	expect(t, "2097151 bytes", w.do("alice", "POST", "/api/cdap-studio/flows", `{"name":"Fits","flow_json":`+numbers(95325)+`}`, ""), 200, `{"success":true,"id":"a0a1a2a3a4a50000"}`)
	expect(t, "2097173 bytes", w.do("alice", "POST", "/api/cdap-studio/flows", `{"name":"Big","flow_json":`+numbers(95326)+`}`, ""), 400, tooLarge)
	expect(t, "an import of 2097173 bytes", w.do("alice", "POST", "/api/cdap-studio/flows/import", `{"flow":`+numbers(95326)+`}`, ""), 400, tooLarge)
	expect(t, "an update to 2097173 bytes", w.do("alice", "PUT", "/api/cdap-studio/flows/a0a1a2a3a4a50000", `{"flow_json":`+numbers(95326)+`}`, ""), 400, tooLarge)
	// Bytes count, not characters: 100000 euro signs are 300000 bytes
	euros := `["` + strings.Repeat("€", 100000) + `",` + strings.TrimPrefix(numbers(81689), "[")
	expect(t, "2097162 bytes in 1897162 characters", w.do("alice", "POST", "/api/cdap-studio/flows", `{"name":"Euro","flow_json":`+euros+`}`, ""), 400, tooLarge)
}

func TestDatabaseFailureAnswers500(t *testing.T) {
	db := storetest.Migrated(t)
	w := newWorld(t, db)
	w.do("alice", "POST", "/api/cdap-studio/flows", `{"name":"x"}`, "")
	if _, err := db.Exec(context.Background(), "ALTER TABLE studio_flows RENAME TO studio_flows_gone"); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ method, target, message string }{
		{"GET", "/api/cdap-studio/flows", "Failed to list flows"},
		{"GET", "/api/cdap-studio/flows/a0a1a2a3a4a50000", "Failed to get flow"},
		{"POST", "/api/cdap-studio/flows", "Failed to create flow"},
		{"PUT", "/api/cdap-studio/flows/a0a1a2a3a4a50000", "Failed to update flow"},
		{"DELETE", "/api/cdap-studio/flows/a0a1a2a3a4a50000", "Failed to delete flow"},
		{"POST", "/api/cdap-studio/flows/a0a1a2a3a4a50000/deploy", "Failed to deploy flow"},
		{"POST", "/api/cdap-studio/flows/a0a1a2a3a4a50000/test", "Failed to test flow"},
		{"GET", "/api/cdap-studio/flows/a0a1a2a3a4a50000/export", "Failed to export flow"},
		{"POST", "/api/cdap-studio/flows/import", "Failed to import flow"},
	}
	for _, c := range cases {
		body := ""
		if c.method == "POST" || c.method == "PUT" {
			body = `{"name":"x","flow":{}}`
		}
		expect(t, c.method+" "+c.target, w.do("alice", c.method, c.target, body, ""), 500, `{"success":false,"error":"`+c.message+`"}`)
	}
}

// A row whose flow text is empty gets {} with the next change that
// leaves the flow out.
func TestUpdateOfAnEmptyStoredFlow(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db)
		ctx := context.Background()
		w.do("alice", "POST", "/api/cdap-studio/flows", `{"name":"x","flow_json":{"a":1}}`, "")
		if _, err := db.Exec(ctx, "UPDATE studio_flows SET flow_json = ''"); err != nil {
			t.Fatal(err)
		}
		expect(t, "update", w.do("alice", "PUT", "/api/cdap-studio/flows/a0a1a2a3a4a50000", `{"name":"y"}`, ""), 200, `{"success":true}`)
		var text string
		if err := db.QueryRow(ctx, "SELECT flow_json FROM studio_flows").Scan(&text); err != nil || text != "{}" {
			t.Errorf("flow_json = %q, %v", text, err)
		}
	})
}

// TestFlowNestedTooDeepIsRefused: a flow nested deeper than 200 levels is
// refused as text and as a value, when it is made, changed and imported;
// 200 levels are stored.
func TestFlowNestedTooDeepIsRefused(t *testing.T) {
	w := newWorld(t, storetest.Migrated(t))
	deep := func(n int) string { return strings.Repeat("[", n) + strings.Repeat("]", n) }
	refused := `{"success":false,"error":"Invalid flow JSON"}`
	expect(t, "200 levels as text", w.do("alice", "POST", "/api/cdap-studio/flows", `{"name":"Deep","flow_json":"`+deep(200)+`"}`, ""), 200, `{"success":true,"id":"a0a1a2a3a4a50000"}`)
	expect(t, "201 levels as text", w.do("alice", "POST", "/api/cdap-studio/flows", `{"name":"Deep","flow_json":"`+deep(201)+`"}`, ""), 400, refused)
	expect(t, "201 levels as a value", w.do("alice", "POST", "/api/cdap-studio/flows", `{"name":"Deep","flow_json":`+deep(201)+`}`, ""), 400, refused)
	expect(t, "an update to 201 levels", w.do("alice", "PUT", "/api/cdap-studio/flows/a0a1a2a3a4a50000", `{"flow_json":"`+deep(201)+`"}`, ""), 400, refused)
	expect(t, "an import of 201 levels", w.do("alice", "POST", "/api/cdap-studio/flows/import", `{"name":"Deep","flow":`+deep(201)+`}`, ""), 400, refused)
	objects := strings.Repeat(`{"a":`, 201) + "1" + strings.Repeat("}", 201)
	expect(t, "201 levels of objects", w.do("alice", "POST", "/api/cdap-studio/flows", `{"name":"Deep","flow_json":`+objects+`}`, ""), 400, refused)
}

// TestDryRunOfOddFlows: nodes and wires count only as lists, and a node
// that is no object or whose type is no text gets no result, where the
// dry run answered 500 or walked a text letter by letter.
func TestDryRunOfOddFlows(t *testing.T) {
	w := newWorld(t, storetest.Migrated(t))
	mock := func(id, kind string) string {
		return `{"nodeId":"` + id + `","nodeType":"` + kind + `","label":"` + kind + `","status":"ok","output":{"_simulated":true,"value":"Mock data from ` + kind + `"}}`
	}
	for i, c := range []struct{ flow, want string }{
		{`{"nodes":5,"wires":3}`, `{"success":true,"results":[],"nodeCount":0,"wireCount":0}`},
		{`{"nodes":true,"wires":true}`, `{"success":true,"results":[],"nodeCount":0,"wireCount":0}`},
		{`{"nodes":{"0":{"id":"n","type":"t"}},"wires":{"length":7}}`, `{"success":true,"results":[],"nodeCount":0,"wireCount":0}`},
		{`{"nodes":"abc","wires":"xy"}`, `{"success":true,"results":[],"nodeCount":0,"wireCount":0}`},
		{`{"nodes":[null,1,"x",["y"],{"id":"n1","type":"inject"},{"id":"n2","type":{"toString":1}},{"id":"n3","type":["a",{"toString":1}]}],"wires":[1,2]}`,
			`{"success":true,"results":[` + mock("n1", "inject") + `],"nodeCount":7,"wireCount":2}`},
	} {
		created := w.do("alice", "POST", "/api/cdap-studio/flows", `{"name":"Odd","flow_json":`+c.flow+`}`, "")
		var answer struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(created.Body.Bytes(), &answer); err != nil || answer.ID == "" {
			t.Fatalf("flow %d: %d %s", i, created.Code, created.Body.String())
		}
		expect(t, c.flow, w.do("alice", "POST", "/api/cdap-studio/flows/"+answer.ID+"/test", `{}`, ""), 200, c.want)
	}
}

// TestFlowWhoseExportIsTooLargeIsRefused: a flow whose export, with two
// spaces per level, would be over 8 MiB is refused when it is made,
// changed or imported, and a stored one is not exported: 30000 zeros 199
// levels deep are 60 KB stored and 12 MB exported (BUGS RV165).
func TestFlowWhoseExportIsTooLargeIsRefused(t *testing.T) {
	w := newWorld(t, storetest.Migrated(t))
	tooLarge := `{"success":false,"error":"Flow data too large (max 2MB)"}`
	deep := strings.Repeat("[", 199) + strings.Repeat("0,", 29999) + "0" + strings.Repeat("]", 199)
	shallow := "[" + strings.Repeat("0,", 29999) + "0]"
	expect(t, "a shallow flow", w.do("alice", "POST", "/api/cdap-studio/flows", `{"name":"Wide","flow_json":"`+shallow+`"}`, ""), 200, `{"success":true,"id":"a0a1a2a3a4a50000"}`)
	expect(t, "as text", w.do("alice", "POST", "/api/cdap-studio/flows", `{"name":"Deep","flow_json":"`+deep+`"}`, ""), 400, tooLarge)
	expect(t, "as a value", w.do("alice", "POST", "/api/cdap-studio/flows", `{"name":"Deep","flow_json":`+deep+`}`, ""), 400, tooLarge)
	expect(t, "an update", w.do("alice", "PUT", "/api/cdap-studio/flows/a0a1a2a3a4a50000", `{"flow_json":"`+deep+`"}`, ""), 400, tooLarge)
	expect(t, "an import", w.do("alice", "POST", "/api/cdap-studio/flows/import", `{"name":"Deep","flow":`+deep+`}`, ""), 400, tooLarge)
	if _, err := w.db.Exec(context.Background(), "UPDATE studio_flows SET flow_json = ? WHERE id = ?", deep, "a0a1a2a3a4a50000"); err != nil {
		t.Fatal(err)
	}
	expect(t, "the export of a stored one", w.do("alice", "GET", "/api/cdap-studio/flows/a0a1a2a3a4a50000/export", "", ""), 400, tooLarge)
}
