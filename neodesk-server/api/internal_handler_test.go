package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/audit"
	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	eventsModule "github.com/neodeskapp/neodesk/neodesk-server/events"
	"github.com/neodeskapp/neodesk/neodesk-server/metrics"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
	"github.com/neodeskapp/neodesk/neodesk-server/security"
)

const (
	testViewerKey   = "viewer-api-key-12345"
	testOperatorKey = "operator-api-key-12345"
)

// internalFixture is an API server with its listener up, so that the same
// request can go over the listener and through InternalHandler.
type internalFixture struct {
	srv      *Server
	db       db.Database
	host     string
	internal http.Handler
}

func addTestAPIKey(t *testing.T, database db.Database, key, name, role string) {
	t.Helper()
	hash := sha256.Sum256([]byte(key))
	if err := database.CreateAPIKey(&db.APIKey{
		KeyHash:   hex.EncodeToString(hash[:]),
		KeyPrefix: key[:8],
		Name:      name,
		Role:      role,
	}); err != nil {
		t.Fatal(err)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// newInternalServer builds a server the way the tests of this package do,
// with two peers and API keys for the roles admin, operator and viewer. It
// does not start the listener.
func newInternalServer(t *testing.T, tweak func(*config.Config)) (*Server, db.Database) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.APIPort = freePort(t)
	if tweak != nil {
		tweak(cfg)
	}
	database := testSetupDB(t)
	t.Cleanup(func() { database.Close() })
	addTestAPIKey(t, database, testViewerKey, "viewer-key", auth.RoleViewer)
	addTestAPIKey(t, database, testOperatorKey, "operator-key", auth.RoleOperator)
	for _, p := range []*db.Peer{
		{ID: "PEERAAA1", Hostname: "host-a", OS: "Windows", Status: "OFFLINE"},
		{ID: "PEERBBB2", Hostname: "host-b", OS: "Linux", Status: "OFFLINE"},
	} {
		if err := database.UpsertPeer(p); err != nil {
			t.Fatal(err)
		}
	}

	srv := New(cfg, database, peer.NewMap(), nil, "1.0.0-test")
	srv.SetBlocklist(security.NewBlocklist())
	auditLog := audit.NewLogger("")
	t.Cleanup(func() { auditLog.Close() })
	srv.SetAuditLogger(auditLog)
	srv.SetEventBus(eventsModule.NewBus())
	srv.SetMetrics(metrics.NewCollector())
	return srv, database
}

func newInternalFixture(t *testing.T) *internalFixture {
	t.Helper()
	srv, database := newInternalServer(t, nil)
	f := &internalFixture{
		srv:  srv,
		db:   database,
		host: fmt.Sprintf("127.0.0.1:%d", srv.cfg.APIPort),
	}
	f.internal = srv.InternalHandler()
	if err := srv.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Stop)
	// Stop waits for idle connections, so drop the test client's first.
	t.Cleanup(http.DefaultClient.CloseIdleConnections)
	for deadline := time.Now().Add(5 * time.Second); ; {
		resp, err := http.Get("http://" + f.host + "/api/health")
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("listener did not come up: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return f
}

type answer struct {
	status      int
	contentType string
	body        []byte
}

// overListener sends the request to the listener, with the API key when one
// is given.
func (f *internalFixture) overListener(t *testing.T, method, path, body, key string) answer {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+f.host+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s over the listener: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return answer{resp.StatusCode, resp.Header.Get("Content-Type"), data}
}

// internalRequest builds the request an in-process caller makes: the
// listener's Host, a loopback caller address, and the identity when one is
// given.
func (f *internalFixture) internalRequest(method, path, body string, id *Identity) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Host = f.host
	req.RemoteAddr = "127.0.0.1:50000"
	if id != nil {
		req = req.WithContext(WithIdentity(req.Context(), *id))
	}
	return req
}

func (f *internalFixture) inProcess(method, path, body string, id *Identity) answer {
	rec := httptest.NewRecorder()
	f.internal.ServeHTTP(rec, f.internalRequest(method, path, body, id))
	return answer{rec.Code, rec.Header().Get("Content-Type"), rec.Body.Bytes()}
}

// volatileKeys are the fields whose values change from one call to the
// next: clocks and counters of the runtime.
var volatileKeys = map[string]bool{
	"uptime": true, "uptime_seconds": true, "goroutines": true,
	"memory_alloc_mb": true, "memory_sys_mb": true,
}

// maskVolatile hides the masked fields and sorts the permission lists,
// which the role handlers build in the order of a map.
func maskVolatile(v any, masked map[string]bool) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if list, ok := val.([]any); ok && k == "permissions" {
				sort.Slice(list, func(i, j int) bool { return fmt.Sprint(list[i]) < fmt.Sprint(list[j]) })
			}
			if masked[k] {
				t[k] = "<masked>"
			} else {
				t[k] = maskVolatile(val, masked)
			}
		}
	case []any:
		for i := range t {
			t[i] = maskVolatile(t[i], masked)
		}
	}
	return v
}

// comparableBody returns the body in a form two answers can be compared in:
// JSON with the volatile fields masked, or the text as it is.
func comparableBody(body []byte, masked map[string]bool) string {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return string(body)
	}
	out, _ := json.Marshal(maskVolatile(v, masked))
	return string(out)
}

type sampleCall struct {
	method string
	path   string
	body   string
	// wantStatus is the status both sides must give; it keeps a sample from
	// passing because both sides fail the same wrong way.
	wantStatus int
	// anyBody skips the body comparison, for answers made of counters.
	anyBody bool
	// mask names fields to mask besides the volatile ones.
	mask []string
}

func (c sampleCall) name() string { return c.method + " " + c.path }

func assertSameAnswer(t *testing.T, c sampleCall, listener, internal answer) {
	t.Helper()
	if listener.status != c.wantStatus {
		t.Errorf("listener status = %d, want %d (body %s)", listener.status, c.wantStatus, listener.body)
	}
	if internal.status != listener.status {
		t.Errorf("in-process status = %d, listener %d (in-process body %s)", internal.status, listener.status, internal.body)
	}
	if internal.contentType != listener.contentType {
		t.Errorf("in-process Content-Type = %q, listener %q", internal.contentType, listener.contentType)
	}
	if c.anyBody {
		return
	}
	masked := map[string]bool{}
	for k := range volatileKeys {
		masked[k] = true
	}
	for _, k := range c.mask {
		masked[k] = true
	}
	want, got := comparableBody(listener.body, masked), comparableBody(internal.body, masked)
	if got != want {
		t.Errorf("in-process body differs\n listener:   %s\n in-process: %s", want, got)
	}
}

// adminSample is a spread of routes of every kind: without credential,
// guarded by a permission, by a role, by the metrics guard, unknown paths
// and wrong methods, reads and writes. The writes give the same answer when
// they run twice.
var adminSample = []sampleCall{
	{method: "GET", path: "/api/health", wantStatus: 200},
	{method: "GET", path: "/api/server/pubkey", wantStatus: 500},
	{method: "GET", path: "/api/login-options", wantStatus: 200},
	{method: "GET", path: "/api/branding", wantStatus: 200},
	{method: "GET", path: "/api/auth/sso/status", wantStatus: 200},
	{method: "GET", path: "/api/auth/oidc/status", wantStatus: 200},
	{method: "GET", path: "/api/guest/access-links/validate?token=nope", wantStatus: 200},
	{method: "POST", path: "/api/auth/login", body: `{"username":"nobody","password":"wrong"}`, wantStatus: 401},
	{method: "GET", path: "/api/peers", wantStatus: 200},
	{method: "GET", path: "/api/peers?limit=1", wantStatus: 200},
	{method: "GET", path: "/api/peers/PEERAAA1", wantStatus: 200},
	{method: "GET", path: "/api/peers/NOSUCH99", wantStatus: 404},
	{method: "PATCH", path: "/api/peers/PEERAAA1", body: `{"note":"n1"}`, wantStatus: 200},
	{method: "PATCH", path: "/api/peers/PEERAAA1", body: `{}`, wantStatus: 400},
	{method: "PUT", path: "/api/peers/PEERAAA1/tags", body: `{"tags":["a","b"]}`, wantStatus: 200},
	{method: "POST", path: "/api/peers/PEERBBB2/ban", body: `{"reason":"r"}`, wantStatus: 200},
	{method: "POST", path: "/api/peers/PEERBBB2/unban", wantStatus: 200},
	{method: "GET", path: "/api/peers/status/summary", wantStatus: 200},
	{method: "GET", path: "/api/peers/online", wantStatus: 200},
	{method: "GET", path: "/api/peers/PEERAAA1/status", wantStatus: 200},
	{method: "GET", path: "/api/peers/PEERAAA1/access-policy", wantStatus: 200},
	{method: "PUT", path: "/api/peers/PEERAAA1/access-policy", body: `{"unattended_enabled":true}`, wantStatus: 200},
	{method: "GET", path: "/api/blocklist", wantStatus: 200},
	{method: "GET", path: "/api/audit/events?limit=3", wantStatus: 200},
	{method: "PUT", path: "/api/config/sample_key", body: `{"value":"v"}`, wantStatus: 200},
	{method: "GET", path: "/api/config/sample_key", wantStatus: 200},
	{method: "GET", path: "/api/config/bad%20key", wantStatus: 400},
	{method: "GET", path: "/api/tokens", wantStatus: 200},
	{method: "GET", path: "/api/tokens/abc", wantStatus: 400},
	{method: "GET", path: "/api/enrollment/mode", wantStatus: 200},
	{method: "GET", path: "/api/enrollment/pending", wantStatus: 200},
	{method: "GET", path: "/api/roles", wantStatus: 200},
	{method: "GET", path: "/api/roles/viewer/permissions", wantStatus: 200},
	{method: "GET", path: "/api/users", wantStatus: 200},
	{method: "GET", path: "/api/org", wantStatus: 200},
	{method: "POST", path: "/api/org", body: `{"name":"","slug":"x"}`, wantStatus: 400},
	{method: "GET", path: "/api/help/requests", wantStatus: 200},
	{method: "POST", path: "/api/help/requests/999/acknowledge", wantStatus: 404},
	{method: "POST", path: "/api/help/requests", body: `{}`, wantStatus: 400},
	{method: "POST", path: "/api/pairing/codes", body: `{"label":"desk"}`, wantStatus: 201, mask: []string{"id", "code", "expires_at"}},
	{method: "GET", path: "/api/pairing/codes", wantStatus: 200},
	{method: "GET", path: "/api/auth/me", wantStatus: 401},
	{method: "GET", path: "/api/server/stats", wantStatus: 200},
	{method: "GET", path: "/metrics", wantStatus: 200, anyBody: true},
	{method: "GET", path: "/api/health/detail", wantStatus: 200, anyBody: true},
	{method: "GET", path: "/api/no/such/route", wantStatus: 404},
	{method: "GET", path: "/no-api-prefix", wantStatus: 404},
	{method: "DELETE", path: "/api/health", wantStatus: 404},
	{method: "PUT", path: "/api/peers", body: `{}`, wantStatus: 404},
}

// TestInternalHandler runs the checks that need the listener next to the
// internal handler on one server: opening a database takes seconds.
func TestInternalHandler(t *testing.T) {
	f := newInternalFixture(t)
	for _, c := range []struct {
		name string
		run  func(*testing.T, *internalFixture)
	}{
		{"answers like the listener", answersLikeListener},
		{"answers like the listener for lower roles", answersLikeListenerForLowerRoles},
		{"refuses a request without identity", refusesRequestWithoutIdentity},
		{"listener still asks for credentials", listenerStillAsksForCredentials},
		{"puts the caller into the context", putsCallerIntoContext},
		{"refuses the device role", refusesDeviceRole},
		{"caps the body", capsBody},
		{"leaves the caller's request alone", leavesCallersRequestAlone},
		{"metrics guard", metricsGuardThroughHandlers},
	} {
		t.Run(c.name, func(t *testing.T) { c.run(t, f) })
	}
}

func answersLikeListener(t *testing.T, f *internalFixture) {
	id := &Identity{Username: "apikey:test-key", Role: auth.RoleAdmin}
	for _, c := range adminSample {
		t.Run(c.name(), func(t *testing.T) {
			listener := f.overListener(t, c.method, c.path, c.body, testAPIKey)
			internal := f.inProcess(c.method, c.path, c.body, id)
			assertSameAnswer(t, c, listener, internal)
		})
	}
}

func answersLikeListenerForLowerRoles(t *testing.T, f *internalFixture) {
	roles := []struct {
		key  string
		id   Identity
		data []sampleCall
	}{
		{testViewerKey, Identity{Username: "apikey:viewer-key", Role: auth.RoleViewer}, []sampleCall{
			{method: "GET", path: "/api/peers", wantStatus: 200},
			{method: "DELETE", path: "/api/peers/PEERAAA1", wantStatus: 403},
			{method: "POST", path: "/api/peers/PEERAAA1/ban", body: `{}`, wantStatus: 403},
			{method: "GET", path: "/api/tokens", wantStatus: 403},
			{method: "GET", path: "/api/enrollment/pending", wantStatus: 403},
			{method: "GET", path: "/api/blocklist", wantStatus: 403},
			{method: "GET", path: "/api/audit/events", wantStatus: 200},
			{method: "GET", path: "/api/org", wantStatus: 200},
			{method: "GET", path: "/api/server/stats", wantStatus: 200},
			{method: "GET", path: "/api/health", wantStatus: 200},
		}},
		{testOperatorKey, Identity{Username: "apikey:operator-key", Role: auth.RoleOperator}, []sampleCall{
			{method: "GET", path: "/api/enrollment/pending", wantStatus: 403},
			{method: "GET", path: "/api/tokens", wantStatus: 403},
			{method: "POST", path: "/api/peers/PEERBBB2/ban", body: `{"reason":"r"}`, wantStatus: 403},
			{method: "PATCH", path: "/api/peers/PEERBBB2", body: `{"note":"n2"}`, wantStatus: 200},
			{method: "GET", path: "/api/config/k", wantStatus: 403},
			{method: "GET", path: "/api/pairing/codes", wantStatus: 403},
		}},
	}
	for _, role := range roles {
		for _, c := range role.data {
			t.Run(role.id.Role+" "+c.name(), func(t *testing.T) {
				listener := f.overListener(t, c.method, c.path, c.body, role.key)
				internal := f.inProcess(c.method, c.path, c.body, &role.id)
				assertSameAnswer(t, c, listener, internal)
			})
		}
	}
}

func refusesRequestWithoutIdentity(t *testing.T, f *internalFixture) {
	cases := []struct {
		name string
		id   *Identity
	}{
		{"no identity", nil},
		{"no username", &Identity{Role: auth.RoleAdmin}},
		{"no role", &Identity{Username: "apikey:test-key"}},
	}
	// A path with a permission, one that needs no credential on the
	// listener, the metrics paths and an unknown path.
	paths := []string{"/api/peers", "/api/health", "/api/server/stats", "/metrics", "/api/no/such/route"}
	for _, c := range cases {
		for _, path := range paths {
			got := f.inProcess("GET", path, "", c.id)
			if got.status != http.StatusUnauthorized {
				t.Errorf("%s, GET %s: status = %d, want 401", c.name, path, got.status)
			}
			if want := `{"error":"Invalid or missing credentials"}`; strings.TrimSpace(string(got.body)) != want {
				t.Errorf("%s, GET %s: body = %s, want %s", c.name, path, got.body, want)
			}
		}
	}
}

func listenerStillAsksForCredentials(t *testing.T, f *internalFixture) {
	for _, path := range []string{"/api/peers", "/api/server/stats", "/api/tokens", "/api/no/such/route"} {
		if got := f.overListener(t, "GET", path, "", ""); got.status != http.StatusUnauthorized {
			t.Errorf("GET %s without a key: status = %d, want 401", path, got.status)
		}
		if got := f.overListener(t, "GET", path, "", "wrong-key"); got.status != http.StatusUnauthorized {
			t.Errorf("GET %s with a wrong key: status = %d, want 401", path, got.status)
		}
	}
	if got := f.overListener(t, "GET", "/api/health", "", ""); got.status != http.StatusOK {
		t.Errorf("GET /api/health without a key: status = %d, want 200", got.status)
	}
	// The fallback of the metrics guard asks for a credential like any
	// other path: /metrics is not among the paths authMiddleware serves
	// without one.
	for _, path := range []string{"/metrics", "/api/health/detail"} {
		if got := f.overListener(t, "GET", path, "", ""); got.status != http.StatusUnauthorized {
			t.Errorf("GET %s without a key: status = %d, want 401", path, got.status)
		}
	}
}

func putsCallerIntoContext(t *testing.T, f *internalFixture) {
	hash, err := auth.HashPassword("Made-Up-Pass-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.CreateUser(&db.User{Username: "alice", PasswordHash: hash, Role: auth.RoleAdmin}); err != nil {
		t.Fatal(err)
	}
	alice := &Identity{Username: "alice", Role: auth.RoleAdmin}

	t.Run("username", func(t *testing.T) {
		got := f.inProcess("GET", "/api/auth/me", "", alice)
		var me struct {
			Username string `json:"username"`
		}
		if err := json.Unmarshal(got.body, &me); err != nil || got.status != 200 || me.Username != "alice" {
			t.Fatalf("GET /api/auth/me as alice: status %d, body %s", got.status, got.body)
		}
	})

	t.Run("user row", func(t *testing.T) {
		// The token handler takes created_by from the user row in the
		// context, which exists for alice and not for an API key.
		for _, c := range []struct {
			id   *Identity
			want string
		}{
			{alice, "alice"},
			{&Identity{Username: "apikey:test-key", Role: auth.RoleAdmin}, ""},
		} {
			got := f.inProcess("POST", "/api/tokens", `{"name":"t"}`, c.id)
			var created struct {
				ID int64 `json:"id"`
			}
			if err := json.Unmarshal(got.body, &created); err != nil || got.status != 201 {
				t.Fatalf("POST /api/tokens as %s: status %d, body %s", c.id.Username, got.status, got.body)
			}
			token, err := f.db.GetDeviceToken(created.ID)
			if err != nil || token == nil {
				t.Fatalf("token %d not stored: %v", created.ID, err)
			}
			if token.CreatedBy != c.want {
				t.Errorf("created_by as %s = %q, want %q", c.id.Username, token.CreatedBy, c.want)
			}
		}
	})

	t.Run("role", func(t *testing.T) {
		viewer := &Identity{Username: "alice", Role: auth.RoleViewer}
		if got := f.inProcess("GET", "/api/tokens", "", viewer); got.status != http.StatusForbidden {
			t.Errorf("GET /api/tokens as a viewer: status = %d, want 403", got.status)
		}
		if got := f.inProcess("GET", "/api/tokens", "", alice); got.status != http.StatusOK {
			t.Errorf("GET /api/tokens as an admin: status = %d, want 200", got.status)
		}
	})

	t.Run("organization", func(t *testing.T) {
		created := f.inProcess("POST", "/api/org", `{"name":"Org One","slug":"org-one"}`, alice)
		var org struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(created.body, &org); err != nil || org.ID == "" {
			t.Fatalf("POST /api/org: status %d, body %s", created.status, created.body)
		}
		member := &Identity{Username: "bob", Role: auth.RoleViewer, OrgID: org.ID}
		if got := f.inProcess("GET", "/api/org/"+org.ID, "", member); got.status != http.StatusOK {
			t.Errorf("GET the organization with its id in the identity: status = %d, want 200 (%s)", got.status, got.body)
		}
		for _, other := range []*Identity{
			{Username: "bob", Role: auth.RoleViewer, OrgID: "another-org"},
			{Username: "bob", Role: auth.RoleViewer},
		} {
			if got := f.inProcess("GET", "/api/org/"+org.ID, "", other); got.status != http.StatusForbidden {
				t.Errorf("GET the organization with org %q in the identity: status = %d, want 403", other.OrgID, got.status)
			}
		}
	})
}

func TestInternalHandlerSetsNoCallerOnPathsWithoutCredential(t *testing.T) {
	srv, _ := newInternalServer(t, nil)
	var sawRole, sawUsername string
	var internal bool
	probe := func(w http.ResponseWriter, r *http.Request) {
		sawRole, sawUsername, internal = getRoleFromCtx(r), getUsernameFromCtx(r), arrivedInternally(r)
	}
	// Register the probe on a mux of its own behind the same middleware.
	mux := http.NewServeMux()
	mux.HandleFunc("/", probe)
	srv.mux.once.Do(func() { srv.mux.mux = mux })
	h := srv.InternalHandler()

	id := Identity{Username: "apikey:test-key", Role: auth.RoleAdmin}
	call := func(method, path string) {
		req := httptest.NewRequest(method, path, nil)
		h.ServeHTTP(httptest.NewRecorder(), req.WithContext(WithIdentity(req.Context(), id)))
	}

	call("GET", "/api/peers")
	if sawRole != auth.RoleAdmin || sawUsername != "apikey:test-key" || !internal {
		t.Errorf("guarded path: role %q, username %q, internal %v; want the identity", sawRole, sawUsername, internal)
	}
	for _, c := range [][2]string{{"GET", "/api/health"}, {"POST", "/api/auth/login"}, {"GET", "/api/branding"}, {"GET", "/ws/nd-mgmt/DEV1"}, {"GET", "/agent.ashx"}} {
		sawRole, sawUsername, internal = "unset", "unset", false
		call(c[0], c[1])
		if sawRole != "" || sawUsername != "" || !internal {
			t.Errorf("%s %s: role %q, username %q, internal %v; want no caller and the internal mark", c[0], c[1], sawRole, sawUsername, internal)
		}
	}
	// The same path with a method that needs a credential.
	call("POST", "/api/branding")
	if sawRole != auth.RoleAdmin {
		t.Errorf("POST /api/branding: role %q, want the identity's", sawRole)
	}
}

func refusesDeviceRole(t *testing.T, f *internalFixture) {
	device := &Identity{Username: "device:PEERAAA1", Role: auth.RoleDevice}
	// The status summary has no guard of its own: only the middleware keeps
	// a device out, as authMiddleware does on the listener.
	for _, path := range []string{"/api/peers/status/summary", "/api/peers"} {
		got := f.inProcess("GET", path, "", device)
		if got.status != http.StatusForbidden || !strings.Contains(string(got.body), "Device credentials cannot access this endpoint") {
			t.Errorf("GET %s as a device: status %d, body %s", path, got.status, got.body)
		}
	}
	if got := f.inProcess("GET", "/api/health", "", device); got.status != http.StatusOK {
		t.Errorf("GET /api/health as a device: status = %d, want 200", got.status)
	}
}

func capsBody(t *testing.T, f *internalFixture) {
	id := &Identity{Username: "apikey:test-key", Role: auth.RoleAdmin}
	big := `{"note":"` + strings.Repeat("x", 1<<20) + `"}`
	c := sampleCall{method: "PATCH", path: "/api/peers/PEERAAA1", body: big, wantStatus: 400}
	assertSameAnswer(t, c, f.overListener(t, c.method, c.path, c.body, testAPIKey), f.inProcess(c.method, c.path, c.body, id))

	small := `{"note":"` + strings.Repeat("x", 1000) + `"}`
	if got := f.inProcess("PATCH", "/api/peers/PEERAAA1", small, id); got.status != http.StatusOK {
		t.Errorf("PATCH with a small body: status = %d, want 200 (%s)", got.status, got.body)
	}
}

func leavesCallersRequestAlone(t *testing.T, f *internalFixture) {
	id := &Identity{Username: "apikey:test-key", Role: auth.RoleAdmin}
	req := f.internalRequest("PATCH", "/api/peers/PEERAAA1", `{"note":"n"}`, id)
	body, ctx := req.Body, req.Context()
	f.internal.ServeHTTP(httptest.NewRecorder(), req)
	if req.Body != body || req.Context() != ctx {
		t.Error("InternalHandler changed the request it was given")
	}
}

// TestInternalHandlerSkipsHTTPSRequirement uses the chain Start builds for
// the listener: a server with ForceHTTPS would need certificates to listen.
func TestInternalHandlerSkipsHTTPSRequirement(t *testing.T) {
	srv, _ := newInternalServer(t, func(cfg *config.Config) { cfg.ForceHTTPS = true })
	listener := srv.secureEnvelopeMiddleware(srv.authMiddleware(srv.mux.get(srv)))

	req := httptest.NewRequest("GET", "/api/peers", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rec := httptest.NewRecorder()
	listener.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "HTTPS required") {
		t.Fatalf("listener chain over plain HTTP: status %d, body %s; want 403 HTTPS required", rec.Code, rec.Body)
	}

	req = httptest.NewRequest("GET", "/api/peers", nil)
	req = req.WithContext(WithIdentity(req.Context(), Identity{Username: "apikey:test-key", Role: auth.RoleAdmin}))
	rec = httptest.NewRecorder()
	srv.InternalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("in process with ForceHTTPS: status = %d, want 200 (%s)", rec.Code, rec.Body)
	}
}

func TestInternalHandlerSharesRouteTableWithStart(t *testing.T) {
	srv, _ := newInternalServer(t, nil)
	if srv.mux.get(srv) != srv.mux.get(srv) {
		t.Error("the route table was built twice")
	}
	// It works before Start, without a listener.
	req := httptest.NewRequest("GET", "/api/peers/PEERAAA1", nil)
	req = req.WithContext(WithIdentity(req.Context(), Identity{Username: "apikey:test-key", Role: auth.RoleAdmin}))
	rec := httptest.NewRecorder()
	srv.InternalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"PEERAAA1"`) {
		t.Errorf("GET a peer before Start: status %d, body %s", rec.Code, rec.Body)
	}

	// Start serves the same table: a route added to it answers on the
	// listener.
	srv.mux.get(srv).HandleFunc("GET /api/shared-table-probe", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "shared")
	})
	if err := srv.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Stop)
	t.Cleanup(http.DefaultClient.CloseIdleConnections)
	url := fmt.Sprintf("http://127.0.0.1:%d/api/shared-table-probe", srv.cfg.APIPort)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		resp, err := testAuthGet(url)
		if err != nil {
			if time.Now().After(deadline) {
				t.Fatalf("listener did not come up: %v", err)
			}
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || string(body) != "shared" {
			t.Errorf("the listener does not serve the shared route table: status %d, body %s", resp.StatusCode, body)
		}
		break
	}
}

func TestMetricsGuard(t *testing.T) {
	admin := Identity{Username: "apikey:test-key", Role: auth.RoleAdmin}
	served := func(h http.Handler, req *http.Request) (int, bool) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String() == "stats"
	}
	stats := func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "stats") }

	t.Run("guard alone", func(t *testing.T) {
		srv, _ := newInternalServer(t, nil)
		guard := srv.metricsGuard(stats)
		newReq := func() *http.Request {
			req := httptest.NewRequest("GET", "/api/server/stats", nil)
			req.RemoteAddr = "203.0.113.7:4000"
			return req
		}

		if status, ok := served(guard, newReq()); ok || status != http.StatusUnauthorized {
			t.Errorf("no credential: status %d, served %v; want 401", status, ok)
		}
		// An identity in the context is not enough: the request has to come
		// through InternalHandler.
		req := newReq()
		req = req.WithContext(WithIdentity(req.Context(), admin))
		if status, ok := served(guard, req); ok || status != http.StatusUnauthorized {
			t.Errorf("identity without the internal mark: status %d, served %v; want 401", status, ok)
		}
		req = newReq()
		req.Header.Set("X-API-Key", testAPIKey)
		if status, ok := served(guard, req); !ok {
			t.Errorf("valid API key: status %d, not served", status)
		}
		req = newReq()
		req = req.WithContext(context.WithValue(req.Context(), internalCtxKey{}, true))
		if status, ok := served(guard, req); !ok {
			t.Errorf("internal mark: status %d, not served", status)
		}
	})

	t.Run("allowlist", func(t *testing.T) {
		srv, _ := newInternalServer(t, func(cfg *config.Config) { cfg.MetricsAllowlist = "203.0.113.7" })
		guard := srv.metricsGuard(stats)
		req := httptest.NewRequest("GET", "/api/server/stats", nil)
		req.RemoteAddr = "203.0.113.7:4000"
		if status, ok := served(guard, req); !ok {
			t.Errorf("listed address: status %d, not served", status)
		}
		req = httptest.NewRequest("GET", "/api/server/stats", nil)
		req.RemoteAddr = "203.0.113.8:4000"
		if status, ok := served(guard, req); ok || status != http.StatusUnauthorized {
			t.Errorf("unlisted address without a credential: status %d, served %v; want 401", status, ok)
		}
	})

}

func metricsGuardThroughHandlers(t *testing.T, f *internalFixture) {
	for _, role := range []string{auth.RoleAdmin, auth.RoleViewer} {
		id := &Identity{Username: "apikey:some-key", Role: role}
		for _, path := range []string{"/api/server/stats", "/metrics"} {
			if got := f.inProcess("GET", path, "", id); got.status != http.StatusOK {
				t.Errorf("in process as %s, GET %s: status = %d, want 200 (%s)", role, path, got.status, got.body)
			}
		}
	}
	if got := f.overListener(t, "GET", "/api/server/stats", "", ""); got.status != http.StatusUnauthorized {
		t.Errorf("listener, GET /api/server/stats without a key: status = %d, want 401", got.status)
	}
	if got := f.overListener(t, "GET", "/api/server/stats", "", testViewerKey); got.status != http.StatusOK {
		t.Errorf("listener, GET /api/server/stats with a key: status = %d, want 200", got.status)
	}
}

// TestInternalHandlerKeepsAddressLimits checks that a limit that counts per
// caller address counts in-process calls under the address they carry,
// together with the calls that reach the listener from that address.
func TestInternalHandlerKeepsAddressLimits(t *testing.T) {
	f := newInternalFixture(t)
	id := &Identity{Username: "apikey:test-key", Role: auth.RoleAdmin}
	// The branding limit is 60 a minute per address.
	for i := 0; i < 30; i++ {
		if got := f.overListener(t, "GET", "/api/branding", "", ""); got.status != http.StatusOK {
			t.Fatalf("listener call %d: status %d", i+1, got.status)
		}
		if got := f.inProcess("GET", "/api/branding", "", id); got.status != http.StatusOK {
			t.Fatalf("in-process call %d: status %d", i+1, got.status)
		}
	}
	if got := f.inProcess("GET", "/api/branding", "", id); got.status != http.StatusTooManyRequests {
		t.Errorf("in-process call 61 from loopback: status = %d, want 429", got.status)
	}
	// Another address has its own budget.
	req := f.internalRequest("GET", "/api/branding", "", id)
	req.RemoteAddr = "198.51.100.9:50000"
	rec := httptest.NewRecorder()
	f.internal.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("in-process call from another address: status = %d, want 200", rec.Code)
	}
}

var routePattern = regexp.MustCompile(`(?m)^\s*mux\.HandleFunc\("(?:([A-Z]+) )?(/[^"]*)"`)

// TestNoCredentialPathsMatchAuthMiddleware holds needsNoCredential to
// authMiddleware: for every registered route and every method, the path
// needs no credential in process exactly when authMiddleware serves it
// without one.
func TestNoCredentialPathsMatchAuthMiddleware(t *testing.T) {
	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	wildcard := regexp.MustCompile(`\{[^}]*\}`)
	paths := map[string]bool{}
	for _, m := range routePattern.FindAllStringSubmatch(string(source), -1) {
		path := wildcard.ReplaceAllString(m[2], "x1")
		paths[path] = true
		if strings.HasSuffix(path, "/") && path != "/" {
			paths[path+"x1"] = true
		}
	}
	if len(paths) < 200 {
		t.Fatalf("found %d route paths in server.go, expected over 200: the pattern no longer matches", len(paths))
	}
	for _, extra := range []string{"/agent.ashx", "/meshrelay.ashx", "/control.ashx", "/neocore.js", "/meshcore.js", "/ws/nd-mgmt/DEV1/extra", "/api/unknown"} {
		paths[extra] = true
	}
	for path := range noCredentialPaths {
		paths[path] = true
	}
	sorted := make([]string, 0, len(paths))
	for path := range paths {
		sorted = append(sorted, path)
	}
	sort.Strings(sorted)

	srv, _ := newInternalServer(t, nil)
	var reached bool
	chain := srv.authMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	for _, path := range sorted {
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
			reached = false
			chain.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, path, bytes.NewReader(nil)))
			if got := needsNoCredential(method, path); got != reached {
				t.Errorf("%s %s: needsNoCredential = %v, authMiddleware serves it without a credential = %v", method, path, got, reached)
			}
		}
	}
}
