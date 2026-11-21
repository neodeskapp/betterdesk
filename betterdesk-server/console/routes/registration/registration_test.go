package registration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/routes/registration"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The intent of web-nodejs/tests/registration.routes.test.js: the pending
// count adds the LAN registrations and the API server's enrollment count,
// and a failing API server counts 0. The API server's count is read as a
// number: text as the number it holds, anything else as 0 (BUGS RG7).
func TestPendingCountAddsBothSources(t *testing.T) {
	for _, c := range []struct {
		name, pending, want string
		lan                 int
	}{
		{"enrollments only", `{"devices":[{"device_id":"Client-A"}],"count":2}`, `{"success":true,"count":2}`, 0},
		{"both", `{"devices":[],"count":1}`, `{"success":true,"count":4}`, 3},
		{"API server fails", ``, `{"success":true,"count":5}`, 5},
		{"count in text", `{"devices":[],"count":"5"}`, `{"success":true,"count":7}`, 2},
		{"count an object", `{"devices":[],"count":{}}`, `{"success":true,"count":2}`, 2},
		{"count a list of one", `{"devices":[],"count":[3]}`, `{"success":true,"count":5}`, 2},
		{"count no number", `{"devices":[],"count":"x"}`, `{"success":true,"count":2}`, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := routetest.NewAPI()
			if c.pending != "" {
				api.On("GET /api/enrollment/pending", http.StatusOK, c.pending)
			} else {
				api.On("GET /api/enrollment/pending", http.StatusBadGateway, `{"error":"connection refused"}`)
			}
			w := routetest.New(t, routetest.Options{API: api})
			for i := 0; i < c.lan; i++ {
				if _, err := w.DB.Exec(context.Background(), "INSERT INTO pending_registrations (device_id) VALUES (?)", "lan-"+string(rune('a'+i))); err != nil {
					t.Fatal(err)
				}
			}
			got := w.As("admin").Do(http.MethodGet, "/api/registrations/count", "")
			if got.Status != http.StatusOK || got.Body != c.want {
				t.Fatalf("%d %s, want %s", got.Status, got.Body, c.want)
			}
			if calls := api.Calls(); len(calls) != 1 || calls[0] != "GET /api/enrollment/pending" {
				t.Fatalf("API calls %v", calls)
			}
		})
	}
}

func helpAPI() *routetest.API {
	api := routetest.NewAPI()
	api.On("POST /api/help/requests/501/acknowledge", http.StatusOK, `{"id":501}`)
	api.On("POST /api/help/requests/"+longHelpID+"/acknowledge", http.StatusOK, `{}`)
	api.On("GET /api/help/requests?limit=200", http.StatusOK,
		`[{"id":501,"device_id":"900100001","status":"acknowledged","handled_by":"apikey:console-bridge","created_at":"2026-11-08T10:00:00Z"},`+
			`{"id":502,"device_id":"900100002","status":"acknowledged","handled_by":"apikey:console-bridge","created_at":"2026-11-08T10:00:00Z"},`+
			`{"id":503,"device_id":"900100003","status":"acknowledged","handled_by":"apikey:console-bridge","created_at":"2026-11-08T10:00:00Z"},`+
			`{"id":504,"device_id":"900100004","status":"resolved","handled_by":"demo_helpdesk","created_at":"2026-11-08T10:00:00Z"},`+
			`{"id":505,"device_id":"900100005","status":"acknowledged","handled_by":"apikey:console-bridge","created_at":"2026-11-08T10:00:00Z"},`+
			`{"id":506,"device_id":"900100006","status":"acknowledged","handled_by":"apikey:console-bridge","created_at":"2026-11-08T10:00:00Z"},`+
			`{"id":507,"device_id":"900100007","status":"acknowledged","handled_by":"apikey:console-bridge","created_at":"2026-11-08T10:00:00Z"}]`)
	return api
}

// longHelpID is a request id of 45 digits; the audit text keeps 40.
const longHelpID = "123456789012345678901234567890123456789012345"

func acceptedBy(t *testing.T, body string) map[string]string {
	t.Helper()
	var out struct {
		Requests []struct {
			ID         string `json:"id"`
			AcceptedBy string `json:"accepted_by"`
			ResolvedBy string `json:"resolved_by"`
		} `json:"requests"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	names := map[string]string{}
	for _, r := range out.Requests {
		names[r.ID] = r.AcceptedBy + "|" + r.ResolvedBy
	}
	return names
}

func audit(t *testing.T, db *store.DB, user int64, action, details, at string) {
	t.Helper()
	if _, err := db.Exec(context.Background(), "INSERT INTO audit_log (user_id, action, details, ip_address, created_at) VALUES (?, ?, ?, '10.0.0.1', ?)", user, action, details, at); err != nil {
		t.Fatal(err)
	}
}

// The intent of registration.routes.test.js: the console records who
// accepted a request and shows that name instead of the API key; it looks
// the name up in the audit log, hides an API key nobody in the console
// stands for, keeps other names, and ignores an audit entry more than a
// minute older than the request (an id the API server gave again after a
// reset).
func TestHelpRequestsShowTheConsolesNames(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db, API: helpAPI()})
		operator := w.As("operator")
		if got := operator.Do(http.MethodPost, "/api/help/requests/501/accept", "{}"); got.Status != http.StatusOK || got.Body != `{"success":true,"request":{"id":501}}` {
			t.Fatalf("accept: %d %s", got.Status, got.Body)
		}
		rows := w.AuditRows()
		if len(rows) == 0 || !strings.HasPrefix(rows[len(rows)-1], "help_request_accepted|Help request 501 accepted|") {
			t.Fatalf("audit rows %v", rows)
		}
		zone := ""
		if db.IsPostgres() {
			zone = "+00"
		}
		audit(t, db, 1, "help_request_accepted", "Help request 502 accepted", "2026-11-08 10:00:30"+zone)
		audit(t, db, 1, "help_request_accepted", "Help request 505 accepted", "2020-01-01 00:00:00"+zone)
		// 59 seconds before the request still counts, 90 seconds before does not
		audit(t, db, 1, "help_request_accepted", "Help request 506 accepted", "2026-11-08 09:59:01"+zone)
		audit(t, db, 1, "help_request_accepted", "Help request 507 accepted", "2026-11-08 09:58:30"+zone)
		viewer := w.As("viewer")
		names := acceptedBy(t, viewer.Do(http.MethodGet, "/api/help/requests", "").Body)
		want := map[string]string{"501": "e2e_operator|", "502": "e2e_admin|", "503": "|", "504": "|demo_helpdesk", "505": "|", "506": "e2e_admin|", "507": "|"}
		for id, name := range want {
			if names[id] != name {
				t.Errorf("request %s: %q, want %q", id, names[id], name)
			}
		}
		// What the console looked up once it keeps, also a name it did not find
		if _, err := db.Exec(context.Background(), "DELETE FROM audit_log"); err != nil {
			t.Fatal(err)
		}
		audit(t, db, 1, "help_request_accepted", "Help request 503 accepted", "2026-11-08 10:00:30"+zone)
		again := acceptedBy(t, viewer.Do(http.MethodGet, "/api/help/requests", "").Body)
		for id, name := range want {
			if again[id] != name {
				t.Errorf("request %s the second time: %q, want %q", id, again[id], name)
			}
		}
		if got := operator.Do(http.MethodPost, "/api/help/requests/"+longHelpID+"/accept", "{}"); got.Status != http.StatusOK {
			t.Fatalf("accept a long id: %d %s", got.Status, got.Body)
		}
		rows = w.AuditRows()
		if want := "help_request_accepted|Help request " + longHelpID[:40] + " accepted|"; !strings.HasPrefix(rows[len(rows)-1], want) {
			t.Fatalf("audit row %q, want %q...", rows[len(rows)-1], want)
		}
	})
}

// Both mounts share one memory of the names, as the old router's one map
// did: a name the root mount learnt answers at /api/bd after its audit
// entry is gone, and a console of its own does not know it.
func TestBothMountsShareTheHelpNames(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db, API: helpAPI()})
		if got := w.As("operator").Do(http.MethodPost, "/api/help/requests/501/accept", "{}"); got.Status != http.StatusOK {
			t.Fatalf("accept: %d %s", got.Status, got.Body)
		}
		if _, err := db.Exec(context.Background(), "DELETE FROM audit_log"); err != nil {
			t.Fatal(err)
		}
		names := acceptedBy(t, w.As("viewer").Do(http.MethodGet, "/api/bd/api/help/requests", "").Body)
		if names["501"] != "e2e_operator|" {
			t.Fatalf("at /api/bd: %q", names["501"])
		}
		other := routetest.New(t, routetest.Options{API: helpAPI()})
		if names := acceptedBy(t, other.As("viewer").Do(http.MethodGet, "/api/help/requests", "").Body); names["501"] != "|" {
			t.Fatalf("another console knows the name: %q", names["501"])
		}
	})
}

// The device routes take no session and, below /api/bd, no CSRF token;
// at the root mount the CSRF gate answers first.
func TestDeviceRoutesAtBothMounts(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	device := w.SignedOut()
	got := device.Do(http.MethodPost, "/api/bd/register-request", `{"device_id":"dev-1"}`, "X-CSRF-Token", "")
	if got.Status != http.StatusOK || got.Body != `{"success":true,"status":"pending","id":1}` {
		t.Fatalf("at /api/bd: %d %s", got.Status, got.Body)
	}
	got = device.Do(http.MethodPost, "/register-request", `{"device_id":"dev-2"}`, "X-CSRF-Token", "")
	if got.Status != http.StatusForbidden || !strings.Contains(got.Body, "Invalid CSRF token") {
		t.Fatalf("at the root without the token: %d %s", got.Status, got.Body)
	}
	got = device.Do(http.MethodPost, "/register-request", `{"device_id":"dev-2"}`)
	if got.Status != http.StatusOK || got.Body != `{"success":true,"status":"pending","id":2}` {
		t.Fatalf("at the root with the token: %d %s", got.Status, got.Body)
	}
	got = device.Do(http.MethodGet, "/register-status?device_id=dev-2", "")
	if got.Status != http.StatusOK || got.Body != `{"success":true,"status":"pending","id":2}` {
		t.Fatalf("status at the root: %d %s", got.Status, got.Body)
	}
}

// The checks of the request route, each on its own: the device id must
// be text that is, once trimmed, 3 to 64 letters, digits, "_" and "-"
// (BUGS RG4), a field that is truthy but no text fails, the fields are
// cut to their length.
func TestRegisterRequestChecks(t *testing.T) {
	w := routetest.New(t, routetest.Options{Env: map[string]string{"BD_REGISTER_RATE_LIMIT_MAX": "1000"}})
	device := w.SignedOut()
	row := func(id string) map[string]any {
		t.Helper()
		var hostname, platform, version, publicKey, uuid, ip, deviceID string
		err := w.DB.QueryRow(context.Background(), "SELECT device_id, hostname, platform, version, public_key, uuid, ip_address FROM pending_registrations WHERE device_id = ?", id).
			Scan(&deviceID, &hostname, &platform, &version, &publicKey, &uuid, &ip)
		if err != nil {
			t.Fatalf("row %q: %v", id, err)
		}
		return map[string]any{"hostname": hostname, "platform": platform, "version": version, "public_key": publicKey, "uuid": uuid, "ip": ip}
	}
	for _, c := range []struct {
		body, want string
	}{
		{`{}`, `400 {"success":false,"error":"Invalid device_id"}`},
		{`{"device_id":""}`, `400 {"success":false,"error":"Invalid device_id"}`},
		// Two UTF-16 units in six bytes
		{`{"device_id":"日本"}`, `400 {"success":false,"error":"Invalid device_id"}`},
		{`{"device_id":"   "}`, `400 {"success":false,"error":"Invalid device_id"}`},
		{`{"device_id":" ab "}`, `400 {"success":false,"error":"Invalid device_id"}`},
		{`{"device_id":"` + strings.Repeat("a", 65) + `"}`, `400 {"success":false,"error":"Invalid device_id"}`},
		{`{"device_id":"😀a"}`, `400 {"success":false,"error":"Invalid device_id"}`},
		{`{"device_id":"dev.1"}`, `400 {"success":false,"error":"Invalid device_id"}`},
		{`{"device_id":"` + strings.Repeat("a", 64) + `"}`, `200 {"success":true,"status":"pending","id":1}`},
		{`{"device_id":"ab"}`, `400 {"success":false,"error":"Invalid device_id"}`},
		{`{"device_id":"é"}`, `400 {"success":false,"error":"Invalid device_id"}`},
		{`{"device_id":123}`, `400 {"success":false,"error":"Invalid device_id"}`},
		{`{"device_id":["abc"]}`, `400 {"success":false,"error":"Invalid device_id"}`},
		{`["abc"]`, `400 {"success":false,"error":"Invalid device_id"}`},
		{`{"device_id":"abc","hostname":5}`, `500 {"success":false,"error":"Internal server error"}`},
		{`{"device_id":"abc","platform":true}`, `500 {"success":false,"error":"Internal server error"}`},
		{`{"device_id":"abc","version":["1"]}`, `500 {"success":false,"error":"Internal server error"}`},
		{`{"device_id":"abc","public_key":{}}`, `500 {"success":false,"error":"Internal server error"}`},
		{`{"device_id":"abc","uuid":1}`, `500 {"success":false,"error":"Internal server error"}`},
	} {
		got := device.Do(http.MethodPost, "/api/bd/register-request", c.body)
		if have := strconv.Itoa(got.Status) + " " + got.Body; have != c.want {
			t.Errorf("%s: %s, want %s", c.body, have, c.want)
		}
	}
	// A no-break space before, a byte order mark after: JavaScript white space
	if got := device.Do(http.MethodPost, "/api/bd/register-request", `{"device_id":" `+string(rune(0xa0))+`a_b-1`+string(rune(0xfeff))+` ","hostname":0,"platform":false,"version":null}`); got.Status != http.StatusOK {
		t.Errorf("white space around: %d %s", got.Status, got.Body)
	}
	if r := row("a_b-1"); r["hostname"] != "" || r["platform"] != "" || r["version"] != "" {
		t.Errorf("falsy fields: %v", r)
	}
	long := `{"device_id":"cut","hostname":"` + strings.Repeat("h", 300) + `","platform":"` + strings.Repeat("😀", 40) + `","version":"` + strings.Repeat("v", 40) + `","public_key":"` + strings.Repeat("k", 600) + `","uuid":"` + strings.Repeat("u", 70) + `"}`
	if got := device.Do(http.MethodPost, "/api/bd/register-request", long); got.Status != http.StatusOK {
		t.Fatalf("long fields: %d %s", got.Status, got.Body)
	}
	r := row("cut")
	if len(r["hostname"].(string)) != 255 || r["platform"] != strings.Repeat("😀", 32) || len(r["version"].(string)) != 32 ||
		len(r["public_key"].(string)) != 512 || len(r["uuid"].(string)) != 64 {
		t.Errorf("cut fields: %v", r)
	}
	// A pair cut in half is stored as U+FFFD
	odd := `{"device_id":"half","platform":"` + strings.Repeat("a", 63) + `😀"}`
	if got := device.Do(http.MethodPost, "/api/bd/register-request", odd); got.Status != http.StatusOK {
		t.Fatalf("half a pair: %d %s", got.Status, got.Body)
	}
	if r := row("half"); r["platform"] != strings.Repeat("a", 63)+string(utf8.RuneError) {
		t.Errorf("half a pair: %v", r)
	}
}

// The address of a request, and of the audit entries of the people who
// decide on it, is the client's as TRUST_PROXY gives it: X-Forwarded-For
// and X-Real-IP count only from a trusted proxy (BUGS RG1).
func TestRecordsTheTrustedAddress(t *testing.T) {
	for _, c := range []struct{ trust, want string }{{"", "127.0.0.1"}, {"1", "203.0.113.5"}} {
		w := routetest.New(t, routetest.Options{Config: func(cfg *config.Config) {
			cfg.TrustProxy, _ = httpx.ParseTrustProxy(c.trust)
		}})
		forwarded := []string{"X-Forwarded-For", "203.0.113.5", "X-Real-IP", "192.0.2.1"}
		if got := w.SignedOut().Do(http.MethodPost, "/api/bd/register-request", `{"device_id":"addr-1"}`, forwarded...); got.Status != http.StatusOK {
			t.Fatalf("TRUST_PROXY=%q: request %d %s", c.trust, got.Status, got.Body)
		}
		var ip string
		if err := w.DB.QueryRow(context.Background(), "SELECT ip_address FROM pending_registrations WHERE device_id = 'addr-1'").Scan(&ip); err != nil || ip != c.want {
			t.Errorf("TRUST_PROXY=%q: stored address %q (%v), want %s", c.trust, ip, err, c.want)
		}
		if got := w.As("admin").Do(http.MethodPut, "/api/registrations/1/reject", `{"reason":"no"}`, forwarded...); got.Status != http.StatusOK {
			t.Fatalf("TRUST_PROXY=%q: reject %d %s", c.trust, got.Status, got.Body)
		}
		// The session notes a changed address on its own under a trusted proxy
		rows := w.AuditRows()
		if last := rows[len(rows)-1]; !strings.HasPrefix(last, "registration_rejected|") || !strings.HasSuffix(last, "|"+c.want+"|1") {
			t.Errorf("TRUST_PROXY=%q: audit %q, want the address %s", c.trust, rows, c.want)
		}
	}
}

// The status a device polls: the configuration only once it is approved,
// the reason only once it is rejected, a missing id refused.
func TestRegisterStatus(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	ctx := context.Background()
	for _, stmt := range []string{
		"INSERT INTO pending_registrations (id, device_id, status) VALUES (1, 'pend', 'pending')",
		"INSERT INTO pending_registrations (id, device_id, status, console_url, server_address, server_key, access_token) VALUES (2, 'appr', 'approved', 'http://h:5000', 'h:21116', 'key', 'tok')",
		"INSERT INTO pending_registrations (id, device_id, status) VALUES (3, 'bare', 'approved')",
		"INSERT INTO pending_registrations (id, device_id, status, rejected_reason) VALUES (4, 'rej', 'rejected', 'not ours')",
		"INSERT INTO pending_registrations (id, device_id, status, rejected_reason) VALUES (5, 'rejn', 'rejected', NULL)",
	} {
		if _, err := w.DB.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	device := w.SignedOut()
	for query, want := range map[string]string{
		"":                  `400 {"success":false,"error":"Missing device_id"}`,
		"?device_id=":       `400 {"success":false,"error":"Missing device_id"}`,
		"?device_id=nope":   `200 {"success":true,"status":"not_found"}`,
		"?device_id=pend":   `200 {"success":true,"status":"pending","id":1}`,
		"?device_id=appr":   `200 {"success":true,"status":"approved","id":2,"config":{"console_url":"http://h:5000","server_address":"h:21116","server_key":"key","access_token":"tok"}}`,
		"?device_id=bare":   `200 {"success":true,"status":"approved","id":3,"config":{"console_url":"","server_address":"","server_key":"","access_token":""}}`,
		"?device_id=rej":    `200 {"success":true,"status":"rejected","id":4,"reason":"not ours"}`,
		"?device_id=rejn":   `200 {"success":true,"status":"rejected","id":5,"reason":""}`,
		"?device_id[]=appr": `200 {"success":true,"status":"approved","id":2,"config":{"console_url":"http://h:5000","server_address":"h:21116","server_key":"key","access_token":"tok"}}`,
		// The first of several, and an object is no id (BUGS LS9)
		"?device_id=pend&device_id=appr":    `200 {"success":true,"status":"pending","id":1}`,
		"?device_id[]=rej&device_id[]=pend": `200 {"success":true,"status":"rejected","id":4,"reason":"not ours"}`,
		"?device_id[x]=pend":                `400 {"success":false,"error":"Missing device_id"}`,
	} {
		got := device.Do(http.MethodGet, "/api/bd/register-status"+query, "")
		if have := strconv.Itoa(got.Status) + " " + got.Body; have != want {
			t.Errorf("%q: %s, want %s", query, have, want)
		}
	}
}

// clientAPI is the client API port around the group's ClientAPI routes:
// the port's own router as Early, a request beginning as the port begins
// it, and in local mode the port's JSON parser in front.
func clientAPI(t *testing.T, local bool) (http.Handler, *store.DB) {
	t.Helper()
	db := storetest.Migrated(t)
	return clientAPIHandler(db, local), db
}

func clientAPIHandler(db *store.DB, local bool) http.Handler {
	d := &kit.Deps{Early: kit.NewRouter(), Router: kit.NewRouter(), Store: db, Log: kit.NewLogger("error", true)}
	registration.ClientAPI(d)
	var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !d.Early.Serve(w, kit.Begin(r, httpx.TrustProxy{}, nil)) {
			w.WriteHeader(http.StatusNotFound)
		}
	})
	if local {
		h = middleware.RouteJSON(middleware.BodyOptions{Limit: 64 << 10})(h)
	}
	return h
}

// On the client API port the two routes answer below /api/bd only. In
// proxy mode no parser reads the body, so a request is refused whatever
// it says (BUGS I1); in local mode the port's parser gives the route the
// body.
func TestClientAPIPort(t *testing.T) {
	post := func(h http.Handler, target, body string) (int, string) {
		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", "198.51.100.4")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	proxy, _ := clientAPI(t, false)
	if status, body := post(proxy, "/api/bd/register-request", `{"device_id":"port-dev"}`); status != http.StatusBadRequest || body != `{"success":false,"error":"Invalid device_id"}` {
		t.Fatalf("proxy mode: %d %s", status, body)
	}
	local, db := clientAPI(t, true)
	if status, body := post(local, "/api/bd/register-request", `{"device_id":"port-dev"}`); status != http.StatusOK || body != `{"success":true,"status":"pending","id":1}` {
		t.Fatalf("local mode: %d %s", status, body)
	}
	// The connection's address: the port trusts no proxy here
	var ip string
	if err := db.QueryRow(context.Background(), "SELECT ip_address FROM pending_registrations WHERE id = 1").Scan(&ip); err != nil || ip != "192.0.2.1" {
		t.Fatalf("stored address %q %v", ip, err)
	}
	if status, _ := post(local, "/register-request", `{"device_id":"port-dev"}`); status != http.StatusNotFound {
		t.Fatalf("root mount on the port: %d", status)
	}
	if status, _ := post(local, "/api/bd/api/registrations", `{}`); status != http.StatusNotFound {
		t.Fatalf("an admin route on the port: %d", status)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/bd/register-status?device_id=port-dev", nil)
	rec := httptest.NewRecorder()
	local.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"success":true,"status":"pending","id":1}` || rec.Header().Get("ETag") == "" {
		t.Fatalf("status on the port: %d %s %v", rec.Code, rec.Body.String(), rec.Header())
	}
}

// The device groups of an approval: an account with a limited device scope
// (operator, given enrollment.approve) is refused for a device it may not
// see (BUGS RV6), and changes only the manual groups it may see for one it
// may; a guid
// of the wrong form counts as not named, a guid named twice once, and a
// folder of id 0 is no folder.
func TestApprovalDeviceGroups(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		api := routetest.NewAPI()
		api.Fallback = func(c routetest.Call) routetest.Reply {
			path, _, _ := strings.Cut(c.Target, "?")
			switch {
			case path == "/api/peers":
				return routetest.Reply{Status: http.StatusOK, Body: `[{"id":"DEV-HID"},{"id":"DEV-SEE"},{"id":"DEV-ADM"},{"id":"DEV-ZERO"}]`}
			case strings.HasPrefix(path, "/api/peers/"):
				return routetest.Reply{Status: http.StatusOK, Body: `{"id":"` + strings.TrimPrefix(path, "/api/peers/") + `"}`}
			case path == "/api/remote-targets":
				return routetest.Reply{Status: http.StatusOK, Body: `[]`}
			}
			return routetest.Reply{Status: http.StatusNotFound, Body: `{"error":"not found"}`}
		}
		w := routetest.New(t, routetest.Options{DB: db, API: api, Roles: routetest.Overrides{"operator": {"enrollment.approve": true}}})
		ctx := context.Background()
		for _, stmt := range []string{
			"INSERT INTO pending_registrations (id, device_id, status) VALUES (1, 'DEV-HID', 'pending'), (2, 'DEV-SEE', 'pending'), (3, 'DEV-ADM', 'pending'), (4, 'DEV-ZERO', 'pending')",
			"INSERT INTO folders (id, name) VALUES (0, 'Zero')",
			"INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('dg-open', 'Open', '', '', 'manual', ''), ('dg-mine', 'Mine', '', '', 'manual', ''), ('dg-closed', 'Closed', '', '', 'manual', ''), ('odd guid', 'Odd', '', '', 'manual', '')",
			"INSERT INTO device_group_user_access (device_group_id, user_id) SELECT id, 5 FROM device_groups WHERE guid = 'dg-mine'",
			"INSERT INTO device_group_user_access (device_group_id, user_id) SELECT id, 1 FROM device_groups WHERE guid = 'dg-closed'",
			"INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'DEV-SEE' FROM device_groups WHERE guid IN ('dg-mine', 'dg-closed')",
			"INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'DEV-HID' FROM device_groups WHERE guid = 'dg-closed'",
			"INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'DEV-ADM' FROM device_groups WHERE guid = 'odd guid'",
		} {
			if _, err := db.Exec(ctx, stmt); err != nil {
				t.Fatalf("%s: %v", stmt, err)
			}
		}
		members := func() string {
			rows, err := db.Query(ctx, "SELECT g.guid, m.peer_id FROM device_group_members m INNER JOIN device_groups g ON g.id = m.device_group_id ORDER BY g.guid, m.peer_id")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var out []string
			for rows.Next() {
				var guid, peer string
				if err := rows.Scan(&guid, &peer); err != nil {
					t.Fatal(err)
				}
				out = append(out, guid+":"+peer)
			}
			return strings.Join(out, " ")
		}
		operator := w.As("operator")
		approve := func(a *routetest.Agent, target, body string) {
			t.Helper()
			if got := a.Do(http.MethodPut, target, body); got.Status != http.StatusOK {
				t.Fatalf("%s: %d %s", target, got.Status, got.Body)
			}
		}
		if got := operator.Do(http.MethodPut, "/api/bd/api/registrations/1/approve", `{"group_guids":["dg-open"]}`); got.Status != http.StatusForbidden {
			t.Fatalf("a device the operator may not see: %d %s", got.Status, got.Body)
		}
		if got := members(); got != "dg-closed:DEV-HID dg-closed:DEV-SEE dg-mine:DEV-SEE odd guid:DEV-ADM" {
			t.Fatalf("a device the operator may not see: %s", got)
		}
		approve(operator, "/api/bd/api/registrations/2/approve", `{"group_guids":["dg-open"]}`)
		if got := members(); got != "dg-closed:DEV-HID dg-closed:DEV-SEE dg-open:DEV-SEE odd guid:DEV-ADM" {
			t.Fatalf("a device the operator may see: %s", got)
		}
		guids := []string{`"odd guid"`}
		for i := 0; i < 60; i++ {
			guids = append(guids, `"g`+strconv.Itoa(i)+`"`, `"g`+strconv.Itoa(i)+`"`)
		}
		guids = append(guids, `"dg-open"`)
		admin := w.As("admin")
		approve(admin, "/api/registrations/3/approve", `{"group_guids":[`+strings.Join(guids, ",")+`]}`)
		if got := members(); got != "dg-closed:DEV-HID dg-closed:DEV-SEE dg-open:DEV-ADM dg-open:DEV-SEE" {
			t.Fatalf("a guid of the wrong form and guids named twice: %s", got)
		}
		approve(admin, "/api/registrations/4/approve", `{"folder_id":0}`)
		var n int
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM device_folder_assignments").Scan(&n); err != nil || n != 0 {
			t.Fatalf("folder 0: %d assignments, %v", n, err)
		}
	})
}

// A reason that is not truthy is stored as no reason: null, a missing
// one, false, 0 and a body that is a list. Each device asks once: a
// rejected device stays rejected (BUGS RG2).
func TestRejectWithoutAReason(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		for i, body := range []string{`{"reason":null}`, `{}`, `{"reason":false}`, `{"reason":0}`, `[]`} {
			id := "nobody-" + strconv.Itoa(i)
			if got := w.SignedOut().Do(http.MethodPost, "/api/bd/register-request", `{"device_id":"`+id+`"}`); got.Status != http.StatusOK {
				t.Fatalf("request %s: %d %s", id, got.Status, got.Body)
			}
			got := w.As("admin").Do(http.MethodPut, "/api/registrations/"+strconv.Itoa(i+1)+"/reject", body)
			if got.Status != http.StatusOK || !strings.Contains(got.Body, `"status":"rejected","approved_by":null,"approved_at":null,"rejected_reason":""`) {
				t.Errorf("%s: %d %s", body, got.Status, got.Body)
			}
		}
	})
}

// An approval whose display name or tags are truthy but no text is
// refused before anything is stored: the request stays pending, and
// there is neither an event nor an audit entry without its approval
// (BUGS RG6).
func TestApprovalChecksTheBodyFirst(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		if got := w.SignedOut().Do(http.MethodPost, "/api/bd/register-request", `{"device_id":"odd-body"}`); got.Status != http.StatusOK {
			t.Fatalf("request: %d %s", got.Status, got.Body)
		}
		for body, want := range map[string]string{
			`{"display_name":5}`:  `400 {"success":false,"error":"Invalid display_name"}`,
			`{"tags":["a","b"]}`:  `400 {"success":false,"error":"Invalid tags"}`,
			`{"display_name":{}}`: `400 {"success":false,"error":"Invalid display_name"}`,
		} {
			got := w.As("admin").Do(http.MethodPut, "/api/registrations/1/approve", body)
			if have := strconv.Itoa(got.Status) + " " + got.Body; have != want {
				t.Errorf("%s: %s, want %s", body, have, want)
			}
		}
		var status string
		if err := db.QueryRow(context.Background(), "SELECT status FROM pending_registrations WHERE id = 1").Scan(&status); err != nil || status != "pending" {
			t.Errorf("status %q (%v), want pending", status, err)
		}
		if rows := w.AuditRows(); len(rows) != 0 {
			t.Errorf("audit %q", rows)
		}
		got := w.As("admin").Do(http.MethodPut, "/api/registrations/1/approve", `{"display_name":"Kiosk","tags":"a, b"}`)
		if got.Status != http.StatusOK || len(w.AuditRows()) != 1 {
			t.Errorf("a good body: %d %s, audit %q", got.Status, got.Body, w.AuditRows())
		}
	})
}

// An enrollment approval whose sync_mode is no text, or whose group_guids
// are no list of text, is refused before the API server is asked, so an
// approval that happens is always audited (BUGS RG13).
func TestEnrollmentApprovalChecksTheBodyFirst(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		api := routetest.NewAPI().On("POST /api/enrollment/approve/DEV1", http.StatusOK, `{"success":true,"device_id":"DEV1"}`)
		w := routetest.New(t, routetest.Options{DB: db, API: api})
		for body, want := range map[string]string{
			`{"sync_mode":{"toString":1}}`:       `400 {"success":false,"error":"Invalid sync_mode"}`,
			`{"sync_mode":5}`:                    `400 {"success":false,"error":"Invalid sync_mode"}`,
			`{"group_guids":[{"toString":"x"}]}`: `400 {"success":false,"error":"Invalid group_guids"}`,
			`{"group_guids":["dg-a",null]}`:      `400 {"success":false,"error":"Invalid group_guids"}`,
			`{"group_guids":"dg-a"}`:             `400 {"success":false,"error":"Invalid group_guids"}`,
		} {
			got := w.As("admin").Do(http.MethodPost, "/api/enrollment/approve/DEV1", body)
			if have := strconv.Itoa(got.Status) + " " + got.Body; have != want {
				t.Errorf("%s: %s, want %s", body, have, want)
			}
		}
		if calls := api.Calls(); len(calls) != 0 {
			t.Errorf("API calls %q", calls)
		}
		if rows := w.AuditRows(); len(rows) != 0 {
			t.Errorf("audit %q", rows)
		}
		got := w.As("admin").Do(http.MethodPost, "/api/enrollment/approve/DEV1", `{"sync_mode":"turbo","group_guids":[]}`)
		if rows := w.AuditRows(); got.Status != http.StatusOK || len(rows) != 1 || !strings.Contains(rows[0], "(mode: turbo)") {
			t.Errorf("a good body: %d %s, audit %q", got.Status, got.Body, rows)
		}
	})
}

// An id that no row can have (no number, below 1, above the largest
// integer of the column) is not found, on both databases, before any
// query; an id with digits first still finds its row (BUGS RG8).
func TestRegistrationIDs(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		if got := w.SignedOut().Do(http.MethodPost, "/api/bd/register-request", `{"device_id":"id-dev"}`); got.Status != http.StatusOK {
			t.Fatalf("request: %d %s", got.Status, got.Body)
		}
		admin := w.As("admin")
		notFound := `404 {"success":false,"error":"Registration not found"}`
		for _, id := range []string{"abc", "99999999999", "2147483648", "0", "-1", "%20"} {
			for _, c := range []struct{ method, suffix string }{
				{http.MethodGet, ""}, {http.MethodPut, "/approve"}, {http.MethodPut, "/reject"}, {http.MethodDelete, ""},
			} {
				got := admin.Do(c.method, "/api/registrations/"+id+c.suffix, `{}`)
				if have := strconv.Itoa(got.Status) + " " + got.Body; have != notFound {
					t.Errorf("%s %s%s: %s", c.method, id, c.suffix, have)
				}
			}
		}
		if got := admin.Do(http.MethodGet, "/api/registrations/1abc", ""); got.Status != http.StatusOK || !strings.Contains(got.Body, `"device_id":"id-dev"`) {
			t.Errorf("1abc: %d %s", got.Status, got.Body)
		}
	})
}

// An approval or rejection whose UPDATE changes no row, because another
// approver changed the request after the route read it, is answered as
// one of a request that is not pending, with the status the row has now,
// on both databases. A trigger that skips the UPDATE stands in for the
// other approver (BUGS RG9).
func TestLostRaceIsRefused(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		if got := w.SignedOut().Do(http.MethodPost, "/api/bd/register-request", `{"device_id":"race-dev"}`); got.Status != http.StatusOK {
			t.Fatalf("request: %d %s", got.Status, got.Body)
		}
		ctx := context.Background()
		statements := []string{"CREATE TRIGGER skip_update BEFORE UPDATE ON pending_registrations BEGIN SELECT RAISE(IGNORE); END"}
		if db.IsPostgres() {
			statements = []string{
				"CREATE FUNCTION skip_update() RETURNS trigger AS $$ BEGIN RETURN NULL; END $$ LANGUAGE plpgsql",
				"CREATE TRIGGER skip_update BEFORE UPDATE ON pending_registrations FOR EACH ROW EXECUTE FUNCTION skip_update()",
			}
		}
		for _, stmt := range statements {
			if _, err := db.Exec(ctx, stmt); err != nil {
				t.Fatal(err)
			}
		}
		admin := w.As("admin")
		for path, want := range map[string]string{
			"/api/registrations/1/approve": `400 {"success":false,"error":"Cannot approve: status is pending"}`,
			"/api/registrations/1/reject":  `400 {"success":false,"error":"Cannot reject: status is pending"}`,
		} {
			got := admin.Do(http.MethodPut, path, `{}`)
			if have := strconv.Itoa(got.Status) + " " + got.Body; have != want {
				t.Errorf("%s: %s, want %s", path, have, want)
			}
		}
		if rows := w.AuditRows(); len(rows) != 0 {
			t.Errorf("audit %q", rows)
		}
	})
}

// An accept or resolve of a request the API server does not have answers
// 404, and one of an id it cannot read, or that the console does not send,
// 400, each with the reason, where they answered 502. Any other failure of
// the API server is still 502. Nothing is written to the audit log
// (BUGS C5).
func TestHelpActionOfAnUnknownRequest(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		api := routetest.NewAPI().
			On("POST /api/help/requests/999999999/acknowledge", http.StatusNotFound, `{"error":"not found"}`).
			On("POST /api/help/requests/999999999/resolve", http.StatusNotFound, `{"error":"not found"}`).
			On("POST /api/help/requests/0/acknowledge", http.StatusBadRequest, `{"error":"invalid id"}`).
			On("POST /api/help/requests/7/resolve", http.StatusInternalServerError, `{"error":"internal"}`).
			On("POST /api/help/requests/8/acknowledge", http.StatusServiceUnavailable, ``)
		w := routetest.New(t, routetest.Options{DB: db, API: api})
		operator := w.As("operator")
		for _, c := range []struct {
			path, want string
			status     int
		}{
			{"/api/help/requests/999999999/accept", `{"success":false,"error":"not found"}`, 404},
			{"/api/bd/api/help/requests/999999999/resolve", `{"success":false,"error":"not found"}`, 404},
			{"/api/help/requests/0/accept", `{"success":false,"error":"invalid id"}`, 400},
			{"/api/help/requests/x%20y/resolve", `{"success":false,"error":"Invalid requestId"}`, 400},
			{"/api/help/requests/7/resolve", `{"success":false,"error":"internal"}`, 502},
			{"/api/help/requests/8/accept", `{"success":false,"error":"Request failed with status code 503"}`, 502},
		} {
			if got := operator.Do(http.MethodPost, c.path, "{}"); got.Status != c.status || got.Body != c.want {
				t.Errorf("%s: %d %s, want %d %s", c.path, got.Status, got.Body, c.status, c.want)
			}
		}
		if rows := w.AuditRows(); len(rows) != 0 {
			t.Errorf("audit %q", rows)
		}
	})
}

// An accept through another spelling of the id ("021") names the request
// the API server answered for (21), in the audit entry and in the list
// (BUGS RG11).
func TestHelpActionNamesTheServersID(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		api := routetest.NewAPI().
			On("POST /api/help/requests/021/acknowledge", http.StatusOK, `{"id":21}`).
			On("POST /api/help/requests/022/resolve", http.StatusOK, `{}`).
			On("GET /api/help/requests?limit=200", http.StatusOK,
				`[{"id":21,"device_id":"900100001","status":"acknowledged","handled_by":"apikey:console-bridge","created_at":"2026-11-08T10:00:00Z"},`+
					`{"id":22,"device_id":"900100002","status":"resolved","handled_by":"apikey:console-bridge","created_at":"2026-11-08T10:00:00Z"}]`)
		w := routetest.New(t, routetest.Options{DB: db, API: api})
		operator := w.As("operator")
		for _, path := range []string{"/api/help/requests/021/accept", "/api/help/requests/022/resolve"} {
			if got := operator.Do(http.MethodPost, path, "{}"); got.Status != http.StatusOK {
				t.Fatalf("%s: %d %s", path, got.Status, got.Body)
			}
		}
		rows := w.AuditRows()
		if len(rows) != 2 || !strings.HasPrefix(rows[0], "help_request_accepted|Help request 21 accepted|") || !strings.HasPrefix(rows[1], "help_request_resolved|Help request 22 resolved|") {
			t.Errorf("audit %q", rows)
		}
		names := acceptedBy(t, w.As("viewer").Do(http.MethodGet, "/api/help/requests", "").Body)
		if names["21"] != "e2e_operator|" || names["22"] != "|e2e_operator" {
			t.Errorf("names %v", names)
		}
	})
}
