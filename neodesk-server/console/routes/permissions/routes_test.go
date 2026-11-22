package permissions_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
)

const (
	signedOut = `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`
	forbidden = `{"success":false,"error":"Forbidden"}`
	setBody   = `{"role":"operator","permission":"device.delete","granted":true}`
)

func TestGuards(t *testing.T) {
	api := routetest.NewAPI()
	api.On("GET /api/roles", 200, `{"roles":[],"all_permissions":[]}`)
	api.On("GET /api/roles/operator/permissions", 200, `{"role":"operator","permissions":[]}`)
	api.On("GET /api/role-permissions", 200, `{"overrides":[]}`)
	api.On("POST /api/role-permissions", 200, `{"status":"ok"}`)
	api.On("DELETE /api/role-permissions/operator/device.view", 200, `{"status":"ok"}`)
	w := routetest.New(t, routetest.Options{API: api})

	for _, tc := range []struct {
		method, target, body string
		refused              map[string]string
		allowed              []string
	}{
		{"GET", "/api/panel/roles", "",
			map[string]string{"guest_issuer": `{"success":false,"error":"Permission denied: user.view"}`, "viewer": `{"success":false,"error":"Permission denied: user.view"}`},
			[]string{"operator", "global_admin", "server_admin", "super_admin", "admin"}},
		{"GET", "/api/panel/roles/operator/permissions", "",
			map[string]string{"viewer": `{"success":false,"error":"Permission denied: user.view"}`},
			[]string{"operator", "admin"}},
		{"GET", "/api/panel/role-permissions", "",
			map[string]string{"operator": `{"success":false,"error":"Permission denied: server.config"}`, "global_admin": `{"success":false,"error":"Permission denied: server.config"}`},
			[]string{"server_admin", "super_admin", "admin"}},
		// Only super admins change overrides: server.config, which a
		// server admin has, is not enough
		{"POST", "/api/panel/role-permissions", setBody,
			map[string]string{"server_admin": forbidden, "global_admin": forbidden, "operator": forbidden, "viewer": forbidden},
			[]string{"super_admin", "admin"}},
		{"DELETE", "/api/panel/role-permissions/operator/device.view", "",
			map[string]string{"server_admin": forbidden, "global_admin": forbidden, "operator": forbidden},
			[]string{"super_admin", "admin"}},
	} {
		name := tc.method + " " + tc.target
		if got := w.SignedOut().Do(tc.method, tc.target, tc.body); got.Status != 401 || got.Body != signedOut {
			t.Errorf("%s signed out: %d %s", name, got.Status, got.Body)
		}
		for role, want := range tc.refused {
			if got := w.As(role).Do(tc.method, tc.target, tc.body); got.Status != 403 || got.Body != want {
				t.Errorf("%s as %s: %d %s", name, role, got.Status, got.Body)
			}
		}
		if calls := api.Calls(); calls != nil {
			t.Errorf("%s reached the API server for accounts that may not: %v", name, calls)
		}
		for _, role := range tc.allowed {
			if got := w.As(role).Do(tc.method, tc.target, tc.body); got.Status != 200 {
				t.Errorf("%s as %s: %d %s", name, role, got.Status, got.Body)
			}
		}
		if calls := api.Calls(); len(calls) != len(tc.allowed) {
			t.Errorf("%s: %d calls for %d accounts that may: %v", name, len(calls), len(tc.allowed), calls)
		}
	}
}

func TestCallsAndAnswers(t *testing.T) {
	api := routetest.NewAPI()
	api.On("GET /api/roles", 200, `{"roles":[{"name":"viewer","level":1,"permissions":["device.view"]}],"all_permissions":["device.view"]}`)
	api.On("GET /api/roles/operator/permissions", 200, `{"role":"operator","permissions":["device.view"]}`)
	api.On("GET /api/roles/nobody/permissions", 400, `{"error":"Invalid role"}`)
	api.On("GET /api/roles/told/permissions", 200, `{"error":"told in the body","role":"told"}`)
	api.On("GET /api/roles/quiet/permissions", 200, `{"error":null}`)
	api.On("GET /api/roles/zero/permissions", 200, `{"error":0}`)
	api.On("GET /api/role-permissions", 200, `{"overrides":[]}`)
	api.On("GET /api/role-permissions?role=operator", 200, `{"overrides":[{"role":"operator","permission":"device.delete","granted":true}]}`)
	api.On("GET /api/role-permissions?role=a%2Cb", 200, `{"overrides":[]}`)
	api.On("GET /api/role-permissions?role=%5Bobject%20Object%5D", 200, `{"overrides":[]}`)
	api.On("GET /api/role-permissions?role=a%20b%2Fc%3Fd", 500, `{"error":"boom"}`)
	api.On("POST /api/role-permissions", 200, `{"status":"ok"}`)
	api.On("DELETE /api/role-permissions/operator/device.delete", 200, `{"status":"ok"}`)
	api.On("DELETE /api/role-permissions/admin/device.view", 400, `{"error":"Cannot override super admin permissions"}`)
	api.On("DELETE /api/role-permissions/quiet/device.view", 200, `{"error":""}`)
	admin := routetest.New(t, routetest.Options{API: api}).As("admin")
	missing := `{"success":false,"error":"Missing required fields: role, permission, granted"}`

	for _, tc := range []struct {
		name                 string
		method, target, body string
		status               int
		want                 string
		calls                []string
	}{
		{"roles", "GET", "/api/panel/roles", "", 200,
			`{"success":true,"data":{"roles":[{"name":"viewer","level":1,"permissions":["device.view"],"fixed":{"user.create":"assigns_no_role","user.delete":"assigns_no_role"}}],"all_permissions":["device.view"]}}`, []string{"GET /api/roles"}},
		{"permissions of a role", "GET", "/api/panel/roles/operator/permissions", "", 200,
			`{"success":true,"data":{"role":"operator","permissions":["device.view"]}}`, []string{"GET /api/roles/operator/permissions"}},
		{"the role is trimmed", "GET", "/api/panel/roles/%20operator%09/permissions", "", 200,
			`{"success":true,"data":{"role":"operator","permissions":["device.view"]}}`, []string{"GET /api/roles/operator/permissions"}},
		{"a role the API server refuses has its status and reason", "GET", "/api/panel/roles/nobody/permissions", "", 400,
			`{"success":false,"error":"Invalid role"}`, []string{"GET /api/roles/nobody/permissions"}},
		{"an error told in a good answer is a 500", "GET", "/api/panel/roles/told/permissions", "", 500,
			`{"success":false,"error":"told in the body"}`, []string{"GET /api/roles/told/permissions"}},
		{"an error that is null is sent as null", "GET", "/api/panel/roles/quiet/permissions", "", 500,
			`{"success":false,"error":null}`, []string{"GET /api/roles/quiet/permissions"}},
		{"an error that is zero is sent as zero", "GET", "/api/panel/roles/zero/permissions", "", 500,
			`{"success":false,"error":0}`, []string{"GET /api/roles/zero/permissions"}},
		{"a role with a space never reaches the API server", "GET", "/api/panel/roles/oper%20ator/permissions", "", 400,
			`{"success":false,"error":"Invalid role"}`, nil},
		{"a role with a slash", "GET", "/api/panel/roles/a%2Fb/permissions", "", 400,
			`{"success":false,"error":"Invalid role"}`, nil},
		{"a role of dots", "GET", "/api/panel/roles/%2E%2E/permissions", "", 400,
			`{"success":false,"error":"Invalid role"}`, nil},

		{"overrides", "GET", "/api/panel/role-permissions", "", 200,
			`{"success":true,"data":{"overrides":[]}}`, []string{"GET /api/role-permissions"}},
		{"overrides of a role", "GET", "/api/panel/role-permissions?role=operator", "", 200,
			`{"success":true,"data":{"overrides":[{"role":"operator","permission":"device.delete","granted":true}]}}`, []string{"GET /api/role-permissions?role=operator"}},
		{"an empty role asks for all", "GET", "/api/panel/role-permissions?role=", "", 200,
			`{"success":true,"data":{"overrides":[]}}`, []string{"GET /api/role-permissions"}},
		{"a role sent twice is joined", "GET", "/api/panel/role-permissions?role=a&role=b", "", 200,
			`{"success":true,"data":{"overrides":[]}}`, []string{"GET /api/role-permissions?role=a%2Cb"}},
		{"a role as an object", "GET", "/api/panel/role-permissions?role[x]=1", "", 200,
			`{"success":true,"data":{"overrides":[]}}`, []string{"GET /api/role-permissions?role=%5Bobject%20Object%5D"}},
		{"the role is escaped, and a refusal has its status and reason", "GET", "/api/panel/role-permissions?role=a%20b%2Fc%3Fd", "", 500,
			`{"success":false,"error":"boom"}`, []string{"GET /api/role-permissions?role=a%20b%2Fc%3Fd"}},

		{"set", "POST", "/api/panel/role-permissions", setBody, 200,
			`{"success":true,"data":{"status":"ok"}}`, []string{`POST /api/role-permissions {"role":"operator","permission":"device.delete","granted":true}`}},
		{"set to denied, names trimmed, other fields dropped", "POST", "/api/panel/role-permissions", `{"granted":false,"permission":" device.view ","role":"\tviewer","more":1}`, 200,
			`{"success":true,"data":{"status":"ok"}}`, []string{`POST /api/role-permissions {"role":"viewer","permission":"device.view","granted":false}`}},
		{"names in lists of one", "POST", "/api/panel/role-permissions", `{"role":["viewer"],"permission":["device.view"],"granted":true}`, 200,
			`{"success":true,"data":{"status":"ok"}}`, []string{`POST /api/role-permissions {"role":"viewer","permission":"device.view","granted":true}`}},
		{"a number as role is sent as text", "POST", "/api/panel/role-permissions", `{"role":5,"permission":"device.view","granted":true}`, 200,
			`{"success":true,"data":{"status":"ok"}}`, []string{`POST /api/role-permissions {"role":"5","permission":"device.view","granted":true}`}},
		{"without fields", "POST", "/api/panel/role-permissions", `{}`, 400, missing, nil},
		{"without a role", "POST", "/api/panel/role-permissions", `{"permission":"device.view","granted":true}`, 400, missing, nil},
		{"with an empty role", "POST", "/api/panel/role-permissions", `{"role":"","permission":"device.view","granted":true}`, 400, missing, nil},
		{"with role zero", "POST", "/api/panel/role-permissions", `{"role":0,"permission":"device.view","granted":true}`, 400, missing, nil},
		{"without a permission", "POST", "/api/panel/role-permissions", `{"role":"viewer","granted":true}`, 400, missing, nil},
		{"with permission null", "POST", "/api/panel/role-permissions", `{"role":"viewer","permission":null,"granted":true}`, 400, missing, nil},
		{"without granted", "POST", "/api/panel/role-permissions", `{"role":"viewer","permission":"device.view"}`, 400, missing, nil},
		{"granted as text", "POST", "/api/panel/role-permissions", `{"role":"viewer","permission":"device.view","granted":"true"}`, 400, missing, nil},
		{"granted as a number", "POST", "/api/panel/role-permissions", `{"role":"viewer","permission":"device.view","granted":1}`, 400, missing, nil},
		{"a list as body", "POST", "/api/panel/role-permissions", `[` + setBody + `]`, 400, missing, nil},
		{"a role with a space", "POST", "/api/panel/role-permissions", `{"role":"vie wer","permission":"device.view","granted":true}`, 400,
			`{"success":false,"error":"Invalid role"}`, nil},
		{"a role that is an object", "POST", "/api/panel/role-permissions", `{"role":{"name":"viewer"},"permission":"device.view","granted":true}`, 400,
			`{"success":false,"error":"Invalid role"}`, nil},
		{"a permission with a slash", "POST", "/api/panel/role-permissions", `{"role":"viewer","permission":"device/view","granted":true}`, 400,
			`{"success":false,"error":"Invalid permission"}`, nil},
		{"the role is checked first", "POST", "/api/panel/role-permissions", `{"role":"vie wer","permission":"device view","granted":true}`, 400,
			`{"success":false,"error":"Invalid role"}`, nil},

		{"delete", "DELETE", "/api/panel/role-permissions/operator/device.delete", "", 200,
			`{"success":true,"data":{"status":"ok"}}`, []string{"DELETE /api/role-permissions/operator/device.delete"}},
		{"delete trims the names", "DELETE", "/api/panel/role-permissions/%20operator%20/%20device.delete%20", "", 200,
			`{"success":true,"data":{"status":"ok"}}`, []string{"DELETE /api/role-permissions/operator/device.delete"}},
		{"a refused delete has the API server's status and reason", "DELETE", "/api/panel/role-permissions/admin/device.view", "", 400,
			`{"success":false,"error":"Cannot override super admin permissions"}`, []string{"DELETE /api/role-permissions/admin/device.view"}},
		{"a failure without a reason gets the general one", "DELETE", "/api/panel/role-permissions/quiet/device.view", "", 400,
			`{"success":false,"error":"Failed to delete override"}`, []string{"DELETE /api/role-permissions/quiet/device.view"}},
		{"delete with a role that is no plain word", "DELETE", "/api/panel/role-permissions/a%20b/device.view", "", 400,
			`{"success":false,"error":"Invalid role"}`, nil},
		{"delete with a permission that is no plain word", "DELETE", "/api/panel/role-permissions/viewer/a%2Fb", "", 400,
			`{"success":false,"error":"Invalid permission"}`, nil},
	} {
		got := admin.Do(tc.method, tc.target, tc.body)
		if got.Status != tc.status || got.Body != tc.want {
			t.Errorf("%s: %d %s\nwant %d %s", tc.name, got.Status, got.Body, tc.status, tc.want)
		}
		if calls := api.Calls(); !reflect.DeepEqual(calls, tc.calls) {
			t.Errorf("%s: the API server was asked %q, want %q", tc.name, calls, tc.calls)
		}
	}
}

func TestSetFallsBackToTheGeneralReason(t *testing.T) {
	api := routetest.NewAPI()
	api.On("POST /api/role-permissions", 200, `{"error":""}`)
	admin := routetest.New(t, routetest.Options{API: api}).As("admin")
	if got := admin.Do("POST", "/api/panel/role-permissions", setBody); got.Status != 400 || got.Body != `{"success":false,"error":"Failed to set permission"}` {
		t.Errorf("a set that failed without a reason: %d %s", got.Status, got.Body)
	}
}

// TestAChangeCountsAtOnce checks that a changed override is used by the
// next request, not when the role's permissions kept in memory run out.
func TestAChangeCountsAtOnce(t *testing.T) {
	overrides := routetest.Overrides{}
	api := routetest.NewAPI()
	api.On("GET /api/tokens", 200, `{"tokens":null,"count":0}`)
	// The made-up API server writes the override table like the real one
	api.Fallback = func(c routetest.Call) routetest.Reply {
		switch {
		case c.Method == "POST" && c.Target == "/api/role-permissions":
			var body struct {
				Role, Permission string
				Granted          bool
			}
			if err := json.Unmarshal([]byte(c.Body), &body); err != nil {
				t.Error(err)
			}
			_ = overrides.SetRolePermission(body.Role, body.Permission, body.Granted)
		case c.Method == "DELETE" && c.Target == "/api/role-permissions/operator/enrollment.manage":
			_ = overrides.DeleteRolePermission("operator", "enrollment.manage")
		default:
			return routetest.Reply{Status: 404, Body: `{"error":"not found"}`}
		}
		return routetest.Reply{Status: 200, Body: `{"status":"ok"}`}
	}
	w := routetest.New(t, routetest.Options{API: api, Roles: overrides})
	operator, admin := w.As("operator"), w.As("admin")

	// Operators may not manage enrollment tokens; asking loads their permissions
	if got := operator.Do("GET", "/api/panel/tokens", ""); got.Status != 403 {
		t.Fatalf("operator before the override: %d %s", got.Status, got.Body)
	}
	if got := admin.Do("POST", "/api/panel/role-permissions", `{"role":"operator","permission":"enrollment.manage","granted":true}`); got.Status != 200 {
		t.Fatalf("set: %d %s", got.Status, got.Body)
	}
	if got := operator.Do("GET", "/api/panel/tokens", ""); got.Status != 200 {
		t.Errorf("operator right after the override was set: %d %s", got.Status, got.Body)
	}
	if got := admin.Do("DELETE", "/api/panel/role-permissions/operator/enrollment.manage", ""); got.Status != 200 {
		t.Fatalf("delete: %d %s", got.Status, got.Body)
	}
	if got := operator.Do("GET", "/api/panel/tokens", ""); got.Status != 403 {
		t.Errorf("operator right after the override was deleted: %d %s", got.Status, got.Body)
	}
}

// TestAnUnknownRoleIsTheAPIServersRefusal: what the API server refuses
// comes back with its status and its reason, also when it answers text or
// gives no answer.
func TestAnUnknownRoleIsTheAPIServersRefusal(t *testing.T) {
	api := routetest.NewAPI()
	api.On("GET /api/roles/ghost/permissions", 400, `{"error":"Invalid role"}`)
	api.On("POST /api/role-permissions", 400, `{"error":"Cannot override super admin permissions"}`)
	api.OnText("GET /api/roles", 503, "Service Unavailable\n")
	api.On("DELETE /api/role-permissions/viewer/device.view", 0, "")
	api.On("GET /api/role-permissions", 403, `{}`)
	admin := routetest.New(t, routetest.Options{API: api}).As("admin")
	for _, tc := range []struct {
		method, target, body string
		status               int
		want                 string
	}{
		{"GET", "/api/panel/roles/ghost/permissions", "", 400, `{"success":false,"error":"Invalid role"}`},
		{"POST", "/api/panel/role-permissions", `{"role":"super_admin","permission":"device.view","granted":false}`, 400,
			`{"success":false,"error":"Cannot override super admin permissions"}`},
		{"GET", "/api/panel/roles", "", 503, `{"success":false,"error":"Service Unavailable"}`},
		{"DELETE", "/api/panel/role-permissions/viewer/device.view", "", 502, `{"success":false,"error":"socket hang up"}`},
		{"GET", "/api/panel/role-permissions", "", 403, `{"success":false,"error":"Request failed with status code 403"}`},
	} {
		if got := admin.Do(tc.method, tc.target, tc.body); got.Status != tc.status || got.Body != tc.want {
			t.Errorf("%s %s: %d %s, want %d %s", tc.method, tc.target, got.Status, got.Body, tc.status, tc.want)
		}
	}
}
