package tenants_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
)

// tenantFixes are the steps of testdata/node_*.json that the bug fixes
// change.
var tenantFixes replay.Fixes

// tenantColumns are the columns of a tenant that a request writes.
var tenantColumns = []string{"name", "slug", "contact_name", "contact_email", "max_devices", "notes", "active"}

// nodeTenant writes the tenant Node's answer holds where Node created or
// changed one, so the steps after it see the rows Node had. Where Node
// failed it does nothing.
func nodeTenant(t *testing.T, w *replay.World, step replay.Step) {
	t.Helper()
	if step.Status != 200 && step.Status != 201 {
		return
	}
	var row map[string]json.RawMessage
	if err := json.Unmarshal([]byte(step.Answer), &row); err != nil {
		t.Fatalf("%s: Node's answer is no tenant: %v", step.Name, err)
	}
	values := make([]any, len(tenantColumns))
	for i, column := range tenantColumns {
		var v any
		if err := json.Unmarshal(row[column], &v); err != nil {
			t.Fatal(err)
		}
		if n, isNumber := v.(float64); isNumber && n == float64(int64(n)) {
			v = int64(n)
		}
		values[i] = v
	}
	if step.Status == 201 {
		w.Exec("INSERT INTO tenants ("+strings.Join(tenantColumns, ", ")+") VALUES (?, ?, ?, ?, ?, ?, ?)", values)
		return
	}
	sets := make([]string, len(tenantColumns))
	for i, column := range tenantColumns {
		sets[i] = column + " = ?"
	}
	var id int64
	if err := json.Unmarshal(row["id"], &id); err != nil {
		t.Fatal(err)
	}
	w.Exec("UPDATE tenants SET "+strings.Join(sets, ", ")+", updated_at = "+store.NowSQL(w.DB.Dialect())+" WHERE id = ?", append(values, id))
}

var sessionRequired = `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`

// TN6: someone who is not signed in gets the console's 401.
var _ = tenantFixes.Add("TN6", map[string]replay.Fix{
	" GET /api/tenants ":                               {Status: 401, Answer: sessionRequired},
	" GET /api/tenants/1 ":                             {Status: 401, Answer: sessionRequired},
	` POST /api/tenants {"name":"By nobody"}`:          {Status: 401, Answer: sessionRequired},
	` PATCH /api/tenants/999 {"notes":"x"}`:            {Status: 401, Answer: sessionRequired},
	" DELETE /api/tenants/999 ":                        {Status: 401, Answer: sessionRequired},
	" GET /api/tenants/999/devices ":                   {Status: 401, Answer: sessionRequired},
	` POST /api/tenants/999/devices {"device_id":"x"}`: {Status: 401, Answer: sessionRequired},
	" DELETE /api/tenants/999/devices/x ":              {Status: 401, Answer: sessionRequired},
	" GET /api/tenants/999/users ":                     {Status: 401, Answer: sessionRequired},
	` POST /api/tenants/999/users {"user_id":1}`:       {Status: 401, Answer: sessionRequired},
	" DELETE /api/tenants/999/users/1 ":                {Status: 401, Answer: sessionRequired},
	" GET /api/tenants/999/stats ":                     {Status: 401, Answer: sessionRequired},
})

// A pro account is turned away by the console on its first request, which
// ends its session; the requests after it are signed out.
var _ = tenantFixes.Add("TN6", map[string]replay.Fix{
	"pro GET /api/tenants ":                               {Status: 403, Answer: `{"success":false,"error":"Pro accounts can only access the RustDesk desktop client API"}`},
	`pro GET /api/tenants/1 `:                             {Status: 401, Answer: sessionRequired},
	`pro POST /api/tenants {"name":"By pro"}`:             {Status: 401, Answer: sessionRequired},
	`pro PATCH /api/tenants/999 {"notes":"x"}`:            {Status: 401, Answer: sessionRequired},
	`pro DELETE /api/tenants/999 `:                        {Status: 401, Answer: sessionRequired},
	`pro GET /api/tenants/999/devices `:                   {Status: 401, Answer: sessionRequired},
	`pro POST /api/tenants/999/devices {"device_id":"x"}`: {Status: 401, Answer: sessionRequired},
	`pro DELETE /api/tenants/999/devices/x `:              {Status: 401, Answer: sessionRequired},
	`pro GET /api/tenants/999/users `:                     {Status: 401, Answer: sessionRequired},
	`pro POST /api/tenants/999/users {"user_id":1}`:       {Status: 401, Answer: sessionRequired},
	`pro DELETE /api/tenants/999/users/1 `:                {Status: 401, Answer: sessionRequired},
	`pro GET /api/tenants/999/stats `:                     {Status: 401, Answer: sessionRequired},
})
