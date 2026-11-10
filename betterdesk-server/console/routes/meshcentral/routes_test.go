package meshcentral

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/limits"
	"github.com/unitronix/betterdesk-server/console/middleware"
)

// apiCall is one request the API server got.
type apiCall struct {
	method, target, body string
}

func (c apiCall) String() string {
	if c.body == "" {
		return c.method + " " + c.target
	}
	return c.method + " " + c.target + " " + c.body
}

// answer is what the stand-in for the API server sends.
type answer struct {
	status      int
	contentType string
	body        string
}

func jsonAnswer(status int, body string) answer {
	return answer{status: status, contentType: "application/json", body: body}
}

// world is the group on a router with a stand-in for the API server.
type world struct {
	t       *testing.T
	mu      sync.Mutex
	calls   []apiCall
	answers func(method, path string) answer
	handler http.Handler
}

// newWorld builds the group with the default limits. answers decides what
// the API server says to a request; its path is the path below /api,
// without the query.
func newWorld(t *testing.T, answers func(method, path string) answer) *world {
	t.Helper()
	return newLimitedWorld(t, answers, "")
}

// newLimitedWorld is newWorld with a limit for the link routes ("" for the
// default of 120 a minute).
func newLimitedWorld(t *testing.T, answers func(method, path string) answer, limit string) *world {
	t.Helper()
	w := &world{t: t, answers: answers}
	api := goapi.New(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.mu.Lock()
		w.calls = append(w.calls, apiCall{r.Method, strings.TrimPrefix(r.RequestURI, "/api"), string(body)})
		w.mu.Unlock()
		a := w.answers(r.Method, strings.TrimPrefix(r.URL.Path, "/api"))
		if a.status == 0 {
			panic(http.ErrAbortHandler)
		}
		if a.contentType != "" {
			rw.Header().Set("Content-Type", a.contentType)
		}
		rw.WriteHeader(a.status)
		_, _ = io.WriteString(rw, a.body)
	}), goapi.WithTimeout(2*time.Second))
	d := &kit.Deps{
		Router: kit.NewRouter(),
		GoAPI:  api,
		Limits: limits.NewSet(limits.Options{Getenv: func(name string) string {
			if name == "RDCLIENT_PAGE_RATE_LIMIT_MAX" {
				return limit
			}
			return ""
		}}),
	}
	Register(d)
	w.handler = middleware.Bodies(middleware.BodyOptions{})(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		r = kit.Begin(r, httpx.TrustProxy{}, nil)
		who := &access.Context{}
		if role := r.Header.Get("X-Test-Role"); role != "" {
			who = &access.Context{UserID: 7, HasUser: true, Role: role}
			if list, custom := strings.CutPrefix(role, "only:"); custom {
				who.Role, who.Loaded, who.Permissions = "viewer", true, access.PermissionSet{}
				for _, permission := range strings.Split(list, ",") {
					who.Permissions[permission] = struct{}{}
				}
			}
		}
		kit.SetAccess(r, who)
		if !d.Router.Serve(rw, r) {
			http.NotFound(rw, r)
		}
	}))
	return w
}

// do sends a request as an account of the role: "" for signed out,
// "only:a,b" for an account whose role has just these permissions. A
// body is sent as JSON.
func (w *world) do(role, method, target, body string) *httptest.ResponseRecorder {
	w.t.Helper()
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if role != "" {
		r.Header.Set("X-Test-Role", role)
	}
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, r)
	return rec
}

// sent returns the calls the API server got since the last look.
func (w *world) sent() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	lines := make([]string, len(w.calls))
	for i, c := range w.calls {
		lines[i] = c.String()
	}
	w.calls = nil
	return strings.Join(lines, "\n")
}

func expect(t *testing.T, what string, rec *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	if rec.Code != status || rec.Body.String() != body {
		t.Errorf("%s: %d %s, want %d %s", what, rec.Code, rec.Body.String(), status, body)
	}
}

func notConnected(string, string) answer {
	return jsonAnswer(400, `{"error":"mesh agent not connected"}`)
}

func TestRoutesInTheOrderOfTheOldFile(t *testing.T) {
	d := &kit.Deps{Router: kit.NewRouter(), GoAPI: goapi.New(http.NotFoundHandler()), Limits: limits.NewSet(limits.Options{})}
	Register(d)
	var got []string
	for _, route := range d.Router.Routes() {
		got = append(got, route.Method+" "+route.Path)
	}
	want := []string{
		"GET /api/mesh/status",
		"GET /api/mesh/server-id",
		"GET /api/mesh/groups",
		"POST /api/mesh/groups",
		"GET /api/mesh/download.msh",
		"POST /api/mesh/devices/{id}/desktop",
		"POST /api/mesh/devices/{id}/terminal",
		"POST /api/mesh/devices/{id}/files",
		"POST /api/mesh/devices/{id}/share",
		"GET /api/mesh/share/validate",
		"POST /api/mesh/devices/{id}/tcp",
		"POST /api/mesh/devices/{id}/udp",
		"POST /api/mesh/devices/{id}/power",
		"POST /api/mesh/devices/{id}/group",
		"GET /api/mesh/recordings",
		"GET /api/mesh/recordings/{id}",
		"GET /api/session/recordings",
		"POST /api/mesh/devices/{id}/exec",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Who may call what: the role that has the permission gets through to the
// API server, the role one step below is refused, and nobody gets through
// signed out.
func TestGuards(t *testing.T) {
	w := newWorld(t, notConnected)
	const (
		signedOut = `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`
		denied    = `{"success":false,"error":"Permission denied: `
	)
	cases := []struct {
		method, target      string
		permission          string
		allowed, notAllowed string
		call                string
	}{
		{"GET", "/api/mesh/status", "", "guest_issuer", "", "GET /mesh/status"},
		{"GET", "/api/mesh/server-id", "server.config", "server_admin", "global_admin", "GET /mesh/server-id"},
		{"GET", "/api/mesh/groups", "server.config", "server_admin", "global_admin", "GET /mesh/groups"},
		{"POST", "/api/mesh/groups", "server.config", "server_admin", "global_admin", "POST /mesh/groups {}"},
		{"GET", "/api/mesh/download.msh", "server.config", "server_admin", "global_admin", "GET /mesh/download.msh"},
		{"POST", "/api/mesh/devices/d1/desktop", "device.connect", "operator", "viewer", "POST /mesh/devices/d1/desktop"},
		{"POST", "/api/mesh/devices/d1/terminal", "mesh.terminal", "global_admin", "operator", "POST /mesh/devices/d1/terminal"},
		{"POST", "/api/mesh/devices/d1/files", "mesh.files", "global_admin", "operator", "POST /mesh/devices/d1/files"},
		{"POST", "/api/mesh/devices/d1/share", "device.connect", "operator", "viewer", "POST /mesh/devices/d1/share {}"},
		{"POST", "/api/mesh/devices/d1/tcp", "device.connect", "operator", "viewer", "POST /mesh/devices/d1/tcp {}"},
		{"POST", "/api/mesh/devices/d1/udp", "device.connect", "operator", "viewer", "POST /mesh/devices/d1/udp {}"},
		{"POST", "/api/mesh/devices/d1/power", "mesh.power", "operator", "viewer", "POST /mesh/devices/d1/power {}"},
		{"POST", "/api/mesh/devices/d1/group", "server.config", "server_admin", "global_admin", "POST /mesh/devices/d1/group {}"},
		{"GET", "/api/mesh/recordings", "device.view", "guest_issuer", "only:device.connect,chat.access", "GET /mesh/recordings"},
		{"GET", "/api/mesh/recordings/r1", "device.connect", "operator", "viewer", "GET /mesh/recordings/r1"},
		{"GET", "/api/session/recordings", "device.view", "guest_issuer", "only:device.connect,chat.access", "GET /session/recordings"},
		{"POST", "/api/mesh/devices/d1/exec", "device.connect", "operator", "viewer", "POST /mesh/devices/d1/exec {}"},
	}
	for _, c := range cases {
		name := c.method + " " + c.target
		body := ""
		if c.method == "POST" {
			body = "{}"
		}
		expect(t, name+" signed out", w.do("", c.method, c.target, body), 401, signedOut)
		if c.notAllowed != "" {
			expect(t, name+" as "+c.notAllowed, w.do(c.notAllowed, c.method, c.target, body), 403, denied+c.permission+`"}`)
		}
		if got := w.sent(); got != "" {
			t.Errorf("%s: a refused request reached the API server: %s", name, got)
		}
		rec := w.do(c.allowed, c.method, c.target, body)
		if rec.Code == 401 || rec.Code == 403 {
			t.Errorf("%s as %s: %d %s", name, c.allowed, rec.Code, rec.Body.String())
		}
		if got := w.sent(); got != c.call {
			t.Errorf("%s as %s: the API server got %q, want %q", name, c.allowed, got, c.call)
		}
	}
}

func TestAnswersArePassedOn(t *testing.T) {
	var next answer
	w := newWorld(t, func(string, string) answer { return next })
	cases := []struct {
		name   string
		api    answer
		status int
		body   string
	}{
		{"an object keeps its keys in order", jsonAnswer(200, `{"status":"ok","group_id":"x","n":1.50,"list":[1,null,{"b":1,"a":2}]}`), 200, `{"status":"ok","group_id":"x","n":1.5,"list":[1,null,{"b":1,"a":2}]}`},
		{"a status other than 200", jsonAnswer(201, `{"id":"1"}`), 201, `{"id":"1"}`},
		{"an error with a body", jsonAnswer(404, `{"error":"mesh disabled"}`), 404, `{"error":"mesh disabled"}`},
		{"an error with a body of another type", jsonAnswer(403, `{"error":"invalid or expired share link","valid":"false"}`), 403, `{"error":"invalid or expired share link","valid":"false"}`},
		{"an error without a body", answer{status: 502}, 502, `{"error":"Go server unreachable"}`},
		{"an error whose body is null", jsonAnswer(500, `null`), 500, `{"error":"Go server unreachable"}`},
		{"an error whose body is zero", jsonAnswer(500, `0`), 500, `{"error":"Go server unreachable"}`},
		{"an error in plain text", answer{status: 404, contentType: "text/plain", body: "404 page not found\n"}, 404, `"404 page not found\n"`},
		{"text that is not JSON", answer{status: 200, contentType: "application/octet-stream", body: "RIFF"}, 200, `"RIFF"`},
		{"an empty answer", answer{status: 200}, 200, `""`},
		{"a list", jsonAnswer(200, `[1,2]`), 200, `[1,2]`},
		{"null", jsonAnswer(200, `null`), 200, `null`},
		{"no answer at all", answer{}, 500, `{"error":"Go server unreachable"}`},
	}
	for _, c := range cases {
		next = c.api
		expect(t, c.name, w.do("admin", "GET", "/api/mesh/groups", ""), c.status, c.body)
		expect(t, c.name+" (write)", w.do("admin", "POST", "/api/mesh/devices/d1/power", `{"action":"off"}`), c.status, c.body)
	}
	if ct := w.do("admin", "GET", "/api/mesh/groups", "").Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content type %q", ct)
	}
}

func TestBodyIsSentAsTheParsedValue(t *testing.T) {
	w := newWorld(t, notConnected)
	cases := []struct{ body, sent string }{
		{`{"action":"off","forced":true}`, `{"action":"off","forced":true}`},
		{` { "b" : 1.0 , "a" : [ 1e2 , "x" ] , "b" : 2 } `, `{"b":2,"a":[100,"x"]}`},
		{`{"2":"b","x":1,"1":"a"}`, `{"1":"a","2":"b","x":1}`},
		{`[1, {"a": null}]`, `[1,{"a":null}]`},
		{`{}`, `{}`},
	}
	for _, c := range cases {
		w.do("admin", "POST", "/api/mesh/devices/d1/tcp", c.body)
		if got, want := w.sent(), "POST /mesh/devices/d1/tcp "+c.sent; got != want {
			t.Errorf("body %s: the API server got %q, want %q", c.body, got, want)
		}
	}
	// The routes that take no body send none, whatever the request has
	for _, action := range []string{"terminal", "files", "desktop"} {
		w.do("admin", "POST", "/api/mesh/devices/d1/"+action, `{"x":1}`)
		if got, want := w.sent(), "POST /mesh/devices/d1/"+action; got != want {
			t.Errorf("%s: the API server got %q, want %q", action, got, want)
		}
	}
}

func TestDeviceIDIsOneEscapedSegment(t *testing.T) {
	w := newWorld(t, notConnected)
	cases := []struct {
		id     string
		status int
		body   string
		call   string
	}{
		{"900100001", 400, `{"error":"mesh agent not connected"}`, "POST /mesh/devices/900100001/terminal"},
		{"a%2Fb", 400, `{"error":"mesh agent not connected"}`, "POST /mesh/devices/a%2Fb/terminal"},
		{"a%20b%3Fc%23d", 400, `{"error":"mesh agent not connected"}`, "POST /mesh/devices/a%20b%3Fc%23d/terminal"},
		{"%C3%A9~!*'()", 400, `{"error":"mesh agent not connected"}`, "POST /mesh/devices/%C3%A9~!*'()/terminal"},
		{"a%5Cb", 400, `{"error":"mesh agent not connected"}`, "POST /mesh/devices/a%5Cb/terminal"},
		{"x..y", 500, `{"error":"Go server unreachable"}`, ""},
		{"%2e%2e", 500, `{"error":"Go server unreachable"}`, ""},
		{"%2e", 400, `{"error":"Invalid API path segment"}`, ""},
	}
	for _, c := range cases {
		expect(t, "device "+c.id, w.do("admin", "POST", "/api/mesh/devices/"+c.id+"/terminal", "{}"), c.status, c.body)
		if got := w.sent(); got != c.call {
			t.Errorf("device %s: the API server got %q, want %q", c.id, got, c.call)
		}
	}
	expect(t, "recording .", w.do("admin", "GET", "/api/mesh/recordings/%2e", ""), 400, `{"error":"Invalid API path segment"}`)
	expect(t, "recording ..", w.do("admin", "GET", "/api/mesh/recordings/%2e%2e", ""), 500, `{"error":"Go server unreachable"}`)
	w.do("admin", "GET", "/api/mesh/recordings/a%2Fb.webm", "")
	if got, want := w.sent(), "GET /mesh/recordings/a%2Fb.webm"; got != want {
		t.Errorf("recording: the API server got %q, want %q", got, want)
	}
}

func TestStatus(t *testing.T) {
	var next answer
	w := newWorld(t, func(string, string) answer { return next })
	cases := []struct {
		name string
		api  answer
		body string
	}{
		{"the status", jsonAnswer(200, `{"enabled":true,"agents_online":0}`), `{"enabled":true,"agents_online":0}`},
		{"the API server is down", answer{}, `{"enabled":false}`},
		{"the API server refuses", jsonAnswer(403, `{"error":"forbidden"}`), `{"enabled":false}`},
		{"an answer with an error in it", jsonAnswer(200, `{"error":"broken","enabled":true}`), `{"success":false,"error":"broken"}`},
		{"an empty answer", answer{status: 200}, `{"success":true,"data":""}`},
		{"a list", jsonAnswer(200, `[]`), `[]`},
	}
	for _, c := range cases {
		next = c.api
		expect(t, c.name, w.do("viewer", "GET", "/api/mesh/status", ""), 200, c.body)
	}
}

func TestDownload(t *testing.T) {
	file := "MeshName=BetterDesk Mesh\nMeshType=2\n"
	next := answer{status: 200, contentType: "application/octet-stream", body: file}
	w := newWorld(t, func(string, string) answer { return next })

	rec := w.do("admin", "GET", "/api/mesh/download.msh", "")
	expect(t, "download", rec, 200, file)
	h := rec.Header()
	if h.Get("Content-Type") != "application/octet-stream" || h.Get("Content-Disposition") != `attachment; filename="meshagents.msh"` || h.Get("Content-Length") != "36" || !strings.HasPrefix(h.Get("ETag"), `W/"24-`) {
		t.Errorf("download headers: %v", h)
	}
	if got := w.sent(); got != "GET /mesh/download.msh" {
		t.Errorf("the API server got %q", got)
	}

	// The query goes on as the old console wrote it out
	queries := []struct{ query, sent string }{
		{"name=Contract%20Mesh&mesh_server=wss://example.test/agent.ashx", "?name=Contract+Mesh&mesh_server=wss%3A%2F%2Fexample.test%2Fagent.ashx"},
		{"name=a&name=b&mesh_id[x]=1", "?name=a%2Cb&mesh_id=%5Bobject+Object%5D"},
		{"name[]=a~*", "?name=a%7E*"},
		{"name=", "?name="},
		{"=x&&", ""},
	}
	for _, c := range queries {
		expect(t, "download ?"+c.query, w.do("admin", "GET", "/api/mesh/download.msh?"+c.query, ""), 200, file)
		if got, want := w.sent(), "GET /mesh/download.msh"+c.sent; got != want {
			t.Errorf("query %q: the API server got %q, want %q", c.query, got, want)
		}
	}
	expect(t, "a query without a text", w.do("admin", "GET", "/api/mesh/download.msh?name[toString]=x", ""), 500, `{"error":"Cannot convert object to primitive value"}`)
	if got := w.sent(); got != "" {
		t.Errorf("a query without a text reached the API server: %s", got)
	}

	// The file's type and name are set here, whatever the API server says
	next = answer{status: 200, contentType: "text/plain", body: "x"}
	rec = w.do("admin", "GET", "/api/mesh/download.msh", "")
	if rec.Header().Get("Content-Type") != "application/octet-stream" || rec.Body.String() != "x" {
		t.Errorf("another type: %v %q", rec.Header(), rec.Body.String())
	}
	etag := rec.Header().Get("ETag")
	r := httptest.NewRequest("GET", "/api/mesh/download.msh", nil)
	r.Header.Set("X-Test-Role", "admin")
	r.Header.Set("If-None-Match", etag)
	cached := httptest.NewRecorder()
	w.handler.ServeHTTP(cached, r)
	if cached.Code != 304 || cached.Body.Len() != 0 {
		t.Errorf("If-None-Match: %d %q", cached.Code, cached.Body.String())
	}
	head := w.do("admin", "HEAD", "/api/mesh/download.msh", "")
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != "1" {
		t.Errorf("HEAD: %d %q %v", head.Code, head.Body.String(), head.Header())
	}

	// A failure is answered 500 with the client's message, not the body
	next = jsonAnswer(404, `{"error":"mesh disabled"}`)
	expect(t, "mesh disabled", w.do("admin", "GET", "/api/mesh/download.msh", ""), 500, `{"error":"Request failed with status code 404"}`)
	next = answer{}
	expect(t, "no answer", w.do("admin", "GET", "/api/mesh/download.msh", ""), 500, `{"error":"socket hang up"}`)
}

func TestValidateShare(t *testing.T) {
	w := newWorld(t, func(string, string) answer {
		return jsonAnswer(403, `{"error":"invalid or expired share link","valid":"false"}`)
	})
	const refused = `{"error":"invalid or expired share link","valid":"false"}`
	cases := []struct{ query, sent string }{
		{"", "GET /mesh/share/validate"},
		{"?token=abc&peer_id=900100001", "GET /mesh/share/validate?token=abc&peer_id=900100001"},
		{"?token[]=abc&peer_id[x]=1", "GET /mesh/share/validate?token=abc&peer_id=%5Bobject+Object%5D"},
		{"?peer_id=1&token=a&token=b", "GET /mesh/share/validate?peer_id=1&token=a%2Cb"},
	}
	for _, role := range []string{"", "viewer"} {
		for _, c := range cases {
			expect(t, "validate "+c.query, w.do(role, "GET", "/api/mesh/share/validate"+c.query, ""), 403, refused)
			if got := w.sent(); got != c.sent {
				t.Errorf("validate %q: the API server got %q, want %q", c.query, got, c.sent)
			}
		}
	}
	expect(t, "a query without a text", w.do("", "GET", "/api/mesh/share/validate?token[toString]=x", ""), 500, `{"error":"Go server unreachable"}`)
	if got := w.sent(); got != "" {
		t.Errorf("a query without a text reached the API server: %s", got)
	}
}

func TestDesktopWithALink(t *testing.T) {
	share := jsonAnswer(403, `{"error":"invalid or expired share link","valid":"false"}`)
	guest := jsonAnswer(200, `{"valid":false,"error":"expired"}`)
	w := newWorld(t, func(method, path string) answer {
		switch path {
		case "/mesh/share/validate":
			return share
		case "/guest/access-links/validate":
			return guest
		}
		return jsonAnswer(200, `{"relay_id":"r1","browser_url":"/meshrelay.ashx?id=r1"}`)
	})
	const (
		badShare = `{"error":"Invalid or expired share link"}`
		badGuest = `{"error":"Invalid or expired guest link"}`
		tunnel   = `{"relay_id":"r1","browser_url":"/meshrelay.ashx?id=r1"}`
		device   = "/api/mesh/devices/d%201/desktop"
	)
	steps := []struct {
		name         string
		share, guest string
		query        string
		status       int
		body         string
		sent         string
	}{
		{"an unknown share link", "", "", "?mesh_share=abc", 403, badShare,
			"GET /mesh/share/validate?token=abc&peer_id=d+1"},
		{"a share link the API server knows", `{"valid":true,"peer_id":"d 1","view_only":true}`, "", "?mesh_share=%20abc%20&record=1", 200, tunnel,
			"GET /mesh/share/validate?token=abc&peer_id=d+1\nPOST /mesh/devices/d%201/desktop?mesh_share=+abc+&record=1"},
		{"valid has to be true-like", `{"valid":0}`, "", "?mesh_share=abc", 403, badShare,
			"GET /mesh/share/validate?token=abc&peer_id=d+1"},
		{"an answer that is no object", `"yes"`, "", "?mesh_share=abc", 403, badShare,
			"GET /mesh/share/validate?token=abc&peer_id=d+1"},
		{"a share token given twice", `{"valid":true}`, "", "?mesh_share=a&mesh_share=b", 200, tunnel,
			"GET /mesh/share/validate?token=a%2Cb&peer_id=d+1\nPOST /mesh/devices/d%201/desktop?mesh_share=a%2Cb"},
		{"the share link goes first", `{"valid":true}`, `{"valid":false}`, "?guest=g&mesh_share=s", 200, tunnel,
			"GET /mesh/share/validate?token=s&peer_id=d+1\nPOST /mesh/devices/d%201/desktop?guest=g&mesh_share=s"},

		{"an unknown guest link", "", `{"valid":false,"error":"expired"}`, "?guest=abc", 403, badGuest,
			"GET /guest/access-links/validate?token=abc&peer_id=d+1"},
		{"a guest link", "", `{"valid":true,"view_only":false}`, "?guest=abc", 200, tunnel,
			"GET /guest/access-links/validate?token=abc&peer_id=d+1\nPOST /mesh/devices/d%201/desktop?guest=abc"},
		{"a guest link as t", "", `{"valid":true}`, "?t=abc&relay_base=/x", 200, tunnel,
			"GET /guest/access-links/validate?token=abc&peer_id=d+1\nPOST /mesh/devices/d%201/desktop?t=abc&relay_base=%2Fx"},
		{"guest goes before t", "", `{"valid":true}`, "?t=tt&guest=gg", 200, tunnel,
			"GET /guest/access-links/validate?token=gg&peer_id=d+1\nPOST /mesh/devices/d%201/desktop?t=tt&guest=gg"},
		{"an empty guest leaves t", "", `{"valid":true}`, "?guest=&t=tt", 200, tunnel,
			"GET /guest/access-links/validate?token=tt&peer_id=d+1\nPOST /mesh/devices/d%201/desktop?guest=&t=tt"},
		{"a link for watching adds view_only", "", `{"valid":true,"view_only":true}`, "?guest=abc", 200, tunnel,
			"GET /guest/access-links/validate?token=abc&peer_id=d+1\nPOST /mesh/devices/d%201/desktop?guest=abc&view_only=1"},
		{"an empty view_only is replaced where it stands", "", `{"valid":true,"view_only":true}`, "?view_only=&guest=abc", 200, tunnel,
			"GET /guest/access-links/validate?token=abc&peer_id=d+1\nPOST /mesh/devices/d%201/desktop?view_only=1&guest=abc"},
		{"a view_only of the caller stays", "", `{"valid":true,"view_only":true}`, "?guest=abc&view_only=0", 200, tunnel,
			"GET /guest/access-links/validate?token=abc&peer_id=d+1\nPOST /mesh/devices/d%201/desktop?guest=abc&view_only=0"},
		{"a guest link the API server refuses outright", "", `!403`, "?guest=abc", 403, badShare,
			"GET /guest/access-links/validate?token=abc&peer_id=d+1"},
		{"another parameter that has no text", "", `{"valid":true}`, "?guest=abc&x[toString]=1", 500, `{"error":"Go server unreachable"}`,
			"GET /guest/access-links/validate?token=abc&peer_id=d+1"},
	}
	for _, role := range []string{"", "viewer", "admin"} {
		for _, s := range steps {
			share = jsonAnswer(403, `{"error":"invalid or expired share link","valid":"false"}`)
			if s.share != "" {
				share = jsonAnswer(200, s.share)
			}
			guest = jsonAnswer(200, s.guest)
			if s.guest == "!403" {
				guest = jsonAnswer(403, `{"error":"forbidden"}`)
			}
			expect(t, s.name+" as "+role, w.do(role, "POST", device+s.query, "{}"), s.status, s.body)
			if got := w.sent(); got != s.sent {
				t.Errorf("%s as %q: the API server got\n%s\nwant\n%s", s.name, role, got, s.sent)
			}
		}
	}
}

func TestDesktopWithoutALinkNeedsTheSession(t *testing.T) {
	w := newWorld(t, func(string, string) answer { return jsonAnswer(200, `{"relay_id":"r1"}`) })
	for _, query := range []string{"", "?mesh_share=&guest=", "?mesh_share=%20&t=%09", "?mesh_share[]=&guest[]=%20", "?record=1&view_only=1"} {
		target := "/api/mesh/devices/d1/desktop" + query
		expect(t, "signed out "+query, w.do("", "POST", target, "{}"), 401, `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`)
		expect(t, "viewer "+query, w.do("viewer", "POST", target, "{}"), 403, `{"success":false,"error":"Permission denied: device.connect"}`)
		expect(t, "pro "+query, w.do("pro", "POST", target, "{}"), 403, `{"success":false,"error":"Pro accounts can only access the RustDesk desktop client API"}`)
		if got := w.sent(); got != "" {
			t.Errorf("%q: a refused request reached the API server: %s", query, got)
		}
		expect(t, "operator "+query, w.do("operator", "POST", target, "{}"), 200, `{"relay_id":"r1"}`)
		// The query of a signed-in caller is not passed on
		if got := w.sent(); got != "POST /mesh/devices/d1/desktop" {
			t.Errorf("%q: the API server got %q", query, got)
		}
	}
}

// A token that cannot be turned into text ended the old console's process
// (BUGS.md MC1). Here the connection closes without an answer and the
// server goes on.
func TestDesktopTokenWithoutATextClosesTheConnection(t *testing.T) {
	w := newWorld(t, notConnected)
	server := httptest.NewServer(w.handler)
	defer server.Close()
	for _, query := range []string{"mesh_share[toString]=x", "guest[toString]=x", "t[toString]=x", "guest[]=a&guest[][toString]=x", "mesh_share=abc&t[toString]=x"} {
		req, err := http.NewRequest("POST", server.URL+"/api/mesh/devices/d1/desktop?"+query, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Test-Role", "admin")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			t.Errorf("%s: got the answer %d, want a closed connection", query, resp.StatusCode)
			continue
		}
		var netErr net.Error
		if !errors.Is(err, io.EOF) && !errors.As(err, &netErr) {
			t.Errorf("%s: %v", query, err)
		}
	}
	if got := w.sent(); got != "" {
		t.Errorf("the API server got %s", got)
	}
	// A token of t is not looked at behind a guest text: the link is checked
	expect(t, "t behind a guest text", w.do("", "POST", "/api/mesh/devices/d1/desktop?guest=abc&t[toString]=x", "{}"), 403, `{"error":"Invalid or expired share link"}`)
	if got := w.sent(); got != "GET /guest/access-links/validate?token=abc&peer_id=d1" {
		t.Errorf("t behind a guest text: the API server got %q", got)
	}
	resp, err := http.Get(server.URL + "/api/mesh/share/validate?token=a&peer_id=b")
	if err != nil {
		t.Fatalf("the server stopped: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("after the closed connections: %d", resp.StatusCode)
	}
}

func TestLinkRoutesAreRateLimitedBeforeAnythingElse(t *testing.T) {
	w := newLimitedWorld(t, notConnected, "2")
	for i := 0; i < 2; i++ {
		if rec := w.do("", "GET", "/api/mesh/share/validate", ""); rec.Code != 400 || rec.Header().Get("RateLimit-Limit") != "2" {
			t.Fatalf("request %d: %d %v", i+1, rec.Code, rec.Header())
		}
	}
	w.sent()
	const tooMany = `{"success":false,"error":"Too many remote page requests. Please slow down."}`
	expect(t, "third validate", w.do("", "GET", "/api/mesh/share/validate", ""), 429, tooMany)
	// The desktop route shares the budget, and the limit answers before
	// the session is looked at
	expect(t, "desktop signed out", w.do("", "POST", "/api/mesh/devices/d1/desktop", "{}"), 429, tooMany)
	expect(t, "desktop with a link", w.do("", "POST", "/api/mesh/devices/d1/desktop?mesh_share=abc", "{}"), 429, tooMany)
	if got := w.sent(); got != "" {
		t.Errorf("a limited request reached the API server: %s", got)
	}
	// Other routes of the group are not counted
	if rec := w.do("admin", "POST", "/api/mesh/devices/d1/exec", "{}"); rec.Code != 400 || rec.Header().Get("RateLimit-Limit") != "" {
		t.Errorf("exec: %d %v", rec.Code, rec.Header())
	}
}
