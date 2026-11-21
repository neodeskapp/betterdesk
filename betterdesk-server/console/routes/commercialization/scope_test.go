package commercialization_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
)

// TestBillingKeepsToTheDeviceScope closes devices a and c to the operator
// with a device group whose access list names the viewer (BUGS.md RV91).
// The operator lists the sessions, work reports and device contracts of
// device b alone, is refused the contract state and pending session of
// another device and the work report of another device's session without
// a call reaching the API server, and gets no export. A work report's
// session is looked up by its id, so a session older than the newest 500
// is no longer refused. The admin gets the API server's answers as they
// are.
func TestBillingKeepsToTheDeviceScope(t *testing.T) {
	sessions := `{"sessions":[{"id":"s1","device_id":"a","status":"pending_report"},{"id":"s2","device_id":"b","status":"pending_report"},{"id":"s3","device_id":"c","status":"closed"}]}`
	api := routetest.NewAPI()
	api.On("GET /api/peers", 200, `[{"id":"a"},{"id":"b"},{"id":"c"}]`)
	api.On("GET /api/billing/sessions", 200, sessions)
	api.On("GET /api/billing/sessions?limit=500", 200, sessions)
	// a work report's session is looked up by its id, also one far past
	// the newest sessions (s6, s7); s9 is unknown
	for id, device := range map[string]string{"s1": "a", "s2": "b", "s4": "b", "s5": "c", "s6": "b", "s7": "a"} {
		api.On("GET /api/billing/sessions/"+id, 200, `{"session":{"id":"`+id+`","device_id":"`+device+`","status":"pending_report"},"device":{"id":"`+device+`"}}`)
	}
	api.On("GET /api/billing/sessions/s9", 404, `{"error":"billing session not found"}`)
	api.On("GET /api/billing/reports", 200, `{"reports":[{"id":1,"session_id":"s1"},{"id":2,"session_id":"s2"},{"id":3,"session_id":"s9"}]}`)
	api.On("GET /api/billing/contracts", 200,
		`{"contracts":[{"id":"k1","target_type":"device","target_key":"a"},{"id":"k2","target_type":"device","target_key":"b"},{"id":"k3","target_type":"org","target_key":"o1"}]}`)
	for _, id := range []string{"a", "b"} {
		api.On("GET /api/billing/check?device_id="+id, 200, `{"allowed":true,"device":"`+id+`"}`)
		api.On("GET /api/billing/sessions/pending?device_id="+id, 200, `{"session":{"device":"`+id+`"}}`)
	}
	for _, id := range []string{"s1", "s2", "s4", "s5", "s6", "s7"} {
		api.On("POST /api/billing/sessions/"+id+"/report", 200, `{"ok":"true"}`)
	}
	api.On("GET /api/billing/sessions/export?format=csv", 200, "id,device_id\ns1,a")
	api.On("GET /api/billing/reports/export?format=csv", 200, "id,session_id\n1,s1")
	w := routetest.New(t, routetest.Options{API: api, Roles: routetest.Overrides{"operator": {"billing.export": true}}})
	for _, statement := range []string{
		"INSERT INTO device_groups (id, guid, name) VALUES (90, 'closed', 'Closed')",
		"INSERT INTO device_group_members (device_group_id, peer_id) VALUES (90, 'a'), (90, 'c')",
		"INSERT INTO device_group_user_access (device_group_id, user_id) VALUES (90, 6)",
	} {
		if _, err := w.DB.Exec(context.Background(), statement); err != nil {
			t.Fatal(err)
		}
	}
	operator, admin := w.As("operator"), w.As("admin")
	for _, c := range []struct {
		method, path string
		status       int
		body         string
		// called is the call the API server gets, "" for none
		called string
	}{
		{"GET", "/api/panel/billing/sessions", 200, `{"sessions":[{"id":"s2","device_id":"b","status":"pending_report"}]}`, "GET /api/billing/sessions"},
		{"GET", "/api/panel/billing/reports", 200, `{"reports":[{"id":2,"session_id":"s2"}]}`, "GET /api/billing/reports"},
		{"GET", "/api/panel/billing/contracts", 200,
			`{"contracts":[{"id":"k2","target_type":"device","target_key":"b"},{"id":"k3","target_type":"org","target_key":"o1"}]}`, "GET /api/billing/contracts"},
		{"GET", "/api/panel/billing/check?device_id=b", 200, `{"allowed":true,"device":"b"}`, "GET /api/billing/check?device_id=b"},
		{"GET", "/api/panel/billing/check?device_id=a", 403, `{"success":false,"error":"errors.forbidden"}`, ""},
		{"GET", "/api/panel/billing/check?device_id=b&device_id=a", 403, `{"success":false,"error":"errors.forbidden"}`, ""},
		{"GET", "/api/panel/billing/sessions/pending?device_id=b", 200, `{"session":{"device":"b"}}`, "GET /api/billing/sessions/pending?device_id=b"},
		{"GET", "/api/panel/billing/sessions/pending?device_id=c", 403, `{"success":false,"error":"errors.forbidden"}`, ""},
		{"POST", "/api/panel/billing/sessions/s2/report", 200, `{"ok":"true"}`, "POST /api/billing/sessions/s2/report"},
		{"POST", "/api/panel/billing/sessions/s4/report", 200, `{"ok":"true"}`, "POST /api/billing/sessions/s4/report"},
		{"POST", "/api/panel/billing/sessions/s1/report", 403, `{"success":false,"error":"errors.forbidden"}`, ""},
		{"POST", "/api/panel/billing/sessions/s5/report", 403, `{"success":false,"error":"errors.forbidden"}`, ""},
		{"POST", "/api/panel/billing/sessions/s9/report", 403, `{"success":false,"error":"errors.forbidden"}`, ""},
		{"POST", "/api/panel/billing/sessions/s6/report", 200, `{"ok":"true"}`, "POST /api/billing/sessions/s6/report"},
		{"POST", "/api/panel/billing/sessions/s7/report", 403, `{"success":false,"error":"errors.forbidden"}`, ""},
		{"GET", "/api/panel/billing/sessions/export?format=csv", 403, `{"success":false,"error":"errors.forbidden"}`, ""},
		{"GET", "/api/panel/billing/reports/export?format=csv", 403, `{"success":false,"error":"errors.forbidden"}`, ""},
	} {
		body := ""
		if c.method == "POST" {
			body = `{"summary":"done"}`
		}
		api.Calls()
		got := operator.Do(c.method, c.path, body)
		if got.Status != c.status || got.Body != c.body {
			t.Errorf("%s %s as the operator: %d %s\nwant %d %s", c.method, c.path, got.Status, got.Body, c.status, c.body)
		}
		// the billing calls, without the lookups of sessions
		var passed []string
		for _, call := range api.Calls() {
			method, target, _ := strings.Cut(call, " ")
			lookup := strings.HasPrefix(target, "/api/billing/sessions?limit=500") ||
				(method == "GET" && strings.HasPrefix(target, "/api/billing/sessions/s") && !strings.Contains(target, "/report"))
			if strings.HasPrefix(target, "/api/billing/") && !lookup {
				passed = append(passed, call)
			}
		}
		if (c.called == "") != (len(passed) == 0) || (c.called != "" && !slices.ContainsFunc(passed, func(call string) bool { return strings.HasPrefix(call, c.called) })) {
			t.Errorf("%s %s as the operator: the API server got %q, want %q", c.method, c.path, passed, c.called)
		}
	}
	for path, want := range map[string]string{
		"/api/panel/billing/sessions":                   sessions,
		"/api/panel/billing/reports":                    `"session_id":"s9"`,
		"/api/panel/billing/contracts":                  `"target_key":"a"`,
		"/api/panel/billing/check?device_id=a":          `"device":"a"`,
		"/api/panel/billing/sessions/export?format=csv": "s1,a",
	} {
		if got := admin.Do("GET", path, ""); got.Status != 200 || !strings.Contains(got.Body, want) {
			t.Errorf("%s as the admin: %d %s", path, got.Status, got.Body)
		}
	}
	if got := admin.Do("POST", "/api/panel/billing/sessions/s1/report", `{"summary":"done"}`); got.Status != 200 {
		t.Errorf("the report of s1 as the admin: %d %s", got.Status, got.Body)
	}
}
