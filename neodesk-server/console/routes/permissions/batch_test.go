package permissions_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
)

const batchPath = "/api/panel/role-permissions/batch"

// TestBatchIsForSuperAdmins: like the single change, a server admin's
// server.config is not enough.
func TestBatchIsForSuperAdmins(t *testing.T) {
	api := routetest.NewAPI()
	w := routetest.New(t, routetest.Options{API: api})
	body := `{"role":"operator","changes":[{"permission":"device.edit","granted":true}]}`
	if got := w.SignedOut().Do("POST", batchPath, body); got.Status != 401 || got.Body != signedOut {
		t.Errorf("signed out: %d %s", got.Status, got.Body)
	}
	for _, role := range []string{"server_admin", "global_admin", "operator", "viewer"} {
		if got := w.As(role).Do("POST", batchPath, body); got.Status != 403 || got.Body != forbidden {
			t.Errorf("as %s: %d %s", role, got.Status, got.Body)
		}
	}
	if calls := api.Calls(); calls != nil {
		t.Errorf("the API server was asked for accounts that may not: %v", calls)
	}
}

// TestBatchChecksEverythingBeforeItWrites: one bad change refuses the
// whole batch, with a plain message, before anything is read or written.
func TestBatchChecksEverythingBeforeItWrites(t *testing.T) {
	api := routetest.NewAPI()
	admin := routetest.New(t, routetest.Options{API: api}).As("admin")
	for _, tc := range []struct{ name, body, want string }{
		{"no body", `{}`, "Missing required fields: role, changes"},
		{"no changes", `{"role":"operator","changes":[]}`, "Missing required fields: role, changes"},
		{"changes not a list", `{"role":"operator","changes":{"permission":"device.edit","granted":true}}`, "Missing required fields: role, changes"},
		{"a role that is no word", `{"role":"op erator","changes":[{"permission":"device.edit","granted":true}]}`, "Invalid role"},
		{"an unknown role", `{"role":"nobody","changes":[{"permission":"device.edit","granted":true}]}`, "Invalid role"},
		{"a super admin role", `{"role":"admin","changes":[{"permission":"device.edit","granted":false}]}`, "Cannot override super admin permissions"},
		{"an unknown permission after a good one", `{"role":"operator","changes":[{"permission":"device.edit","granted":true},{"permission":"device.fly","granted":true}]}`,
			"Unknown permission: device.fly"},
		{"a change that is no object", `{"role":"operator","changes":["device.edit"]}`, "Each change needs a permission and granted"},
		{"granted missing", `{"role":"operator","changes":[{"permission":"device.edit"}]}`, "granted must be true, false or null: device.edit"},
		{"granted as text", `{"role":"operator","changes":[{"permission":"device.edit","granted":"true"}]}`, "granted must be true, false or null: device.edit"},
		{"a fixed permission", `{"role":"operator","changes":[{"permission":"device.edit","granted":true},{"permission":"user.create","granted":true}]}`,
			"user.create is fixed for the role operator and cannot be changed"},
		{"a fixed permission back to the default", `{"role":"pro","changes":[{"permission":"device.view","granted":null}]}`,
			"device.view is fixed for the role pro and cannot be changed"},
		{"the same permission twice", `{"role":"operator","changes":[{"permission":"device.edit","granted":true},{"permission":"device.edit","granted":null}]}`,
			"device.edit is changed twice"},
	} {
		got := admin.Do("POST", batchPath, tc.body)
		message, _ := json.Marshal(tc.want)
		want := `{"success":false,"error":` + string(message) + `}`
		if got.Status != 400 || got.Body != want {
			t.Errorf("%s: %d %s, want 400 %s", tc.name, got.Status, got.Body, want)
		}
		if calls := api.Calls(); calls != nil {
			t.Errorf("%s: the API server was asked %v", tc.name, calls)
		}
	}
}

// TestBatchAnswersTheUndoAndWritesOneAuditEntry: the answer has, for each
// change, the override that was stored before (null for none), so that
// sending it back undoes the batch, and the permissions in effect after.
// One audit entry names the role and every change.
func TestBatchAnswersTheUndoAndWritesOneAuditEntry(t *testing.T) {
	api := routetest.NewAPI()
	api.On("GET /api/role-permissions?role=operator", 200,
		`{"overrides":[{"role":"operator","permission":"device.view","granted":true},{"role":"operator","permission":"user.view","granted":false}]}`)
	api.On("POST /api/role-permissions", 200, `{"status":"ok"}`)
	api.On("DELETE /api/role-permissions/operator/device.view", 200, `{"status":"ok"}`)
	api.On("GET /api/roles/operator/permissions", 200, `{"role":"operator","permissions":["device.connect","device.edit"]}`)
	w := routetest.New(t, routetest.Options{API: api})
	before := len(w.AuditRows())

	got := w.As("admin").Do("POST", batchPath,
		`{"role":" operator ","changes":[{"permission":"device.edit","granted":true},{"permission":"device.view","granted":null},{"permission":"user.view","granted":true},{"permission":"device.ban","granted":false}]}`)
	want := `{"success":true,"data":{"previous":[{"permission":"device.edit","granted":null},{"permission":"device.view","granted":true},` +
		`{"permission":"user.view","granted":false},{"permission":"device.ban","granted":null}],"permissions":["device.connect","device.edit"]}}`
	if got.Status != 200 || got.Body != want {
		t.Errorf("batch: %d %s\nwant 200 %s", got.Status, got.Body, want)
	}
	wantCalls := []string{
		"GET /api/role-permissions?role=operator",
		`POST /api/role-permissions {"role":"operator","permission":"device.edit","granted":true}`,
		"DELETE /api/role-permissions/operator/device.view",
		`POST /api/role-permissions {"role":"operator","permission":"user.view","granted":true}`,
		`POST /api/role-permissions {"role":"operator","permission":"device.ban","granted":false}`,
		"GET /api/roles/operator/permissions",
	}
	if calls := api.Calls(); !reflect.DeepEqual(calls, wantCalls) {
		t.Errorf("calls:\n got  %q\n want %q", calls, wantCalls)
	}
	rows := w.AuditRows()[before:]
	if len(rows) != 1 || !strings.HasPrefix(rows[0],
		"role_permissions_changed|Role operator: device.edit granted, device.view back to the default, user.view granted, device.ban taken away|") {
		t.Errorf("audit: %q", rows)
	}
}

// TestBatchUndoesWhatItWroteWhenAWriteFails: a write the API server
// refuses ends the batch; the writes before it are put back to what was
// stored, and nothing is audited.
func TestBatchUndoesWhatItWroteWhenAWriteFails(t *testing.T) {
	api := routetest.NewAPI()
	api.On("GET /api/role-permissions?role=operator", 200, `{"overrides":[{"role":"operator","permission":"device.view","granted":false}]}`)
	api.Fallback = func(c routetest.Call) routetest.Reply {
		if c.Method == "POST" && strings.Contains(c.Body, `"user.view"`) {
			return routetest.Reply{Status: 500, Body: `{"error":"Failed to set permission"}`}
		}
		return routetest.Reply{Status: 200, Body: `{"status":"ok"}`}
	}
	w := routetest.New(t, routetest.Options{API: api})
	before := len(w.AuditRows())
	got := w.As("admin").Do("POST", batchPath,
		`{"role":"operator","changes":[{"permission":"device.edit","granted":true},{"permission":"device.view","granted":null},{"permission":"user.view","granted":true}]}`)
	if got.Status != 500 || got.Body != `{"success":false,"error":"Failed to set permission"}` {
		t.Errorf("batch: %d %s", got.Status, got.Body)
	}
	wantCalls := []string{
		"GET /api/role-permissions?role=operator",
		`POST /api/role-permissions {"role":"operator","permission":"device.edit","granted":true}`,
		"DELETE /api/role-permissions/operator/device.view",
		`POST /api/role-permissions {"role":"operator","permission":"user.view","granted":true}`,
		`POST /api/role-permissions {"role":"operator","permission":"device.view","granted":false}`,
		"DELETE /api/role-permissions/operator/device.edit",
	}
	if calls := api.Calls(); !reflect.DeepEqual(calls, wantCalls) {
		t.Errorf("calls:\n got  %q\n want %q", calls, wantCalls)
	}
	if rows := w.AuditRows()[before:]; len(rows) != 0 {
		t.Errorf("audit: %q", rows)
	}
}

// TestBatchCountsAtOnce: like the single change, the next request uses
// the new permissions, not the ones kept in memory.
func TestBatchCountsAtOnce(t *testing.T) {
	overrides := routetest.Overrides{}
	api := routetest.NewAPI()
	api.On("GET /api/tokens", 200, `{"tokens":null,"count":0}`)
	api.On("GET /api/role-permissions?role=operator", 200, `{"overrides":[]}`)
	api.On("GET /api/roles/operator/permissions", 200, `{"role":"operator","permissions":[]}`)
	api.Fallback = func(c routetest.Call) routetest.Reply {
		if c.Method != "POST" || c.Target != "/api/role-permissions" {
			return routetest.Reply{Status: 404, Body: `{"error":"not found"}`}
		}
		var body struct {
			Role, Permission string
			Granted          bool
		}
		if err := json.Unmarshal([]byte(c.Body), &body); err != nil {
			t.Error(err)
		}
		_ = overrides.SetRolePermission(body.Role, body.Permission, body.Granted)
		return routetest.Reply{Status: 200, Body: `{"status":"ok"}`}
	}
	w := routetest.New(t, routetest.Options{API: api, Roles: overrides})
	operator := w.As("operator")
	if got := operator.Do("GET", "/api/panel/tokens", ""); got.Status != 403 {
		t.Fatalf("operator before: %d %s", got.Status, got.Body)
	}
	if got := w.As("admin").Do("POST", batchPath, `{"role":"operator","changes":[{"permission":"enrollment.manage","granted":true}]}`); got.Status != 200 {
		t.Fatalf("batch: %d %s", got.Status, got.Body)
	}
	if got := operator.Do("GET", "/api/panel/tokens", ""); got.Status != 200 {
		t.Errorf("operator right after the batch: %d %s", got.Status, got.Body)
	}
}
