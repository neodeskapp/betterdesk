package settings_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// A profile from its making to its deletion, with what each step writes
// into the audit log.
func TestProfileChain(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db)
		admin := w.As("admin")
		admin.Do("POST", "/api/settings/branding", `{"appName":"Current"}`)
		res := admin.Do("POST", "/api/settings/branding/profiles", `{"name":"  First  ","description":"  about  "}`)
		if res.Status != http.StatusOK || !strings.HasPrefix(res.Body, `{"success":true,"data":{"id":`) {
			t.Fatalf("create: %d %s", res.Status, res.Body)
		}
		first := strconv.Itoa(int(decode(t, res.Body)["data"].(map[string]any)["id"].(float64)))
		if got := lastAudit(t, w); got != "branding_profile_create|Created profile: First|127.0.0.1|1" {
			t.Errorf("create audit: %s", got)
		}
		var name, description, data string
		if err := db.QueryRow(t.Context(), "SELECT name, description, data FROM branding_profiles WHERE id = ?", first).Scan(&name, &description, &data); err != nil {
			t.Fatal(err)
		}
		if name != "First" || description != "about" || !strings.Contains(data, `"appName":"Current"`) || !strings.Contains(data, `"version":"2.0"`) {
			t.Errorf("stored: %q %q %.120s", name, description, data)
		}
		// with settings in the body, a list counts as settings too
		res = admin.Do("POST", "/api/settings/branding/profiles", `{"name":"Second","branding":{"appName":"From the body"}}`)
		second := strconv.Itoa(int(decode(t, res.Body)["data"].(map[string]any)["id"].(float64)))
		if err := db.QueryRow(t.Context(), "SELECT data FROM branding_profiles WHERE id = ?", second).Scan(&data); err != nil {
			t.Fatal(err)
		}
		if data != `{"version":"1.0","type":"neodesk-theme","branding":{"appName":"From the body"}}` {
			t.Errorf("stored with settings: %s", data)
		}
		for body, want := range map[string]string{
			`{"name":"   "}`:          "Profile name is required",
			`{"name":0}`:              "Profile name is required",
			`{}`:                      "Profile name is required",
			`["First"]`:               "Profile name is required",
			`{"name":{"toString":1}}`: "Invalid profile name",
			`{"name":"x","description":{"toString":1}}`: "Invalid profile description",
		} {
			if res := admin.Do("POST", "/api/settings/branding/profiles", body); res.Status != http.StatusBadRequest || res.Body != `{"success":false,"error":"`+want+`"}` {
				t.Errorf("create %s: %d %s", body, res.Status, res.Body)
			}
		}
		// a name that is taken is 409 on both databases (BUGS BR11)
		taken := admin.Do("POST", "/api/settings/branding/profiles", `{"name":"First"}`)
		if taken.Status != http.StatusConflict || taken.Body != `{"success":false,"error":"Profile name already exists"}` {
			t.Errorf("taken: %d %s", taken.Status, taken.Body)
		}

		if res := admin.Do("PUT", "/api/settings/branding/profiles/"+first+"abc", `{"name":"  Renamed ","description":"new"}`); res.Body != `{"success":true}` {
			t.Errorf("update: %d %s", res.Status, res.Body)
		}
		if got := lastAudit(t, w); got != "branding_profile_update|Updated profile #"+first+"|127.0.0.1|1" {
			t.Errorf("update audit: %s", got)
		}
		if err := db.QueryRow(t.Context(), "SELECT name, description FROM branding_profiles WHERE id = ?", first).Scan(&name, &description); err != nil {
			t.Fatal(err)
		}
		if name != "Renamed" || description != "new" {
			t.Errorf("after the update: %q %q", name, description)
		}
		for path, want := range map[string]string{
			"abc":                    `400 {"success":false,"error":"Invalid profile id"}`,
			"":                       `404`,
			"999999":                 `400 {"success":false,"error":"Profile not found"}`,
			strings.Repeat("9", 400): `400 {"success":false,"error":"Invalid profile id"}`,
			"3000000000":             `400 {"success":false,"error":"Invalid profile id"}`,
			"-2147483649":            `400 {"success":false,"error":"Invalid profile id"}`,
		} {
			res := admin.Do("PUT", "/api/settings/branding/profiles/"+path, `{"name":"x"}`)
			got := strconv.Itoa(res.Status)
			if res.Status != http.StatusNotFound {
				got += " " + res.Body
			}
			if got != want {
				t.Errorf("update %q: %s", path, got)
			}
		}
		if res := admin.Do("PUT", "/api/settings/branding/profiles/"+first, `{"name":{"toString":1}}`); res.Body != `{"success":false,"error":"Invalid profile name"}` {
			t.Errorf("update with a name that is no text: %s", res.Body)
		}
		if res := admin.Do("PUT", "/api/settings/branding/profiles/"+first, `{"name":"Second"}`); res.Status != http.StatusConflict || res.Body != `{"success":false,"error":"Profile name already exists"}` {
			t.Errorf("update to a taken name: %d %s", res.Status, res.Body)
		}

		res = admin.Do("POST", "/api/settings/branding/profiles/"+second+"/apply", "{}")
		if res.Status != http.StatusOK || keyOrder(t, res.Body) != "success,data,revision" || !strings.Contains(res.Body, `"appName":"From the body"`) {
			t.Errorf("apply: %d %.200s", res.Status, res.Body)
		}
		if got := lastAudit(t, w); got != "branding_profile_apply|Applied profile #"+second+"|127.0.0.1|1" {
			t.Errorf("apply audit: %s", got)
		}
		if res := admin.Do("POST", "/api/settings/branding/profiles/999999/apply", "{}"); res.Status != http.StatusNotFound || res.Body != `{"success":false,"error":"Profile not found or invalid"}` {
			t.Errorf("apply a missing one: %d %s", res.Status, res.Body)
		}
		if res := admin.Do("POST", "/api/settings/branding/profiles/x/apply", "{}"); res.Status != http.StatusBadRequest {
			t.Errorf("apply x: %d %s", res.Status, res.Body)
		}

		before := len(w.AuditRows())
		res = admin.Do("POST", "/api/settings/branding/profiles/"+first+"/duplicate", `{"name":""}`)
		if res.Status != http.StatusOK {
			t.Fatalf("duplicate: %d %s", res.Status, res.Body)
		}
		copied := strconv.Itoa(int(decode(t, res.Body)["data"].(map[string]any)["id"].(float64)))
		if err := db.QueryRow(t.Context(), "SELECT name FROM branding_profiles WHERE id = ?", copied).Scan(&name); err != nil {
			t.Fatal(err)
		}
		if name != "Renamed (copy)" {
			t.Errorf("copy named %q", name)
		}
		res = admin.Do("POST", "/api/settings/branding/profiles/"+first+"/duplicate", `{"name":"Second"}`)
		if err := db.QueryRow(t.Context(), "SELECT name FROM branding_profiles WHERE id = ?", int(decode(t, res.Body)["data"].(map[string]any)["id"].(float64))).Scan(&name); err != nil {
			t.Fatal(err)
		}
		if name != "Second 2" {
			t.Errorf("copy with a taken name named %q", name)
		}
		for body, want := range map[string]string{`{"name":5}`: "Invalid profile name", `{"name":["x"]}`: "Invalid profile name", `{"name":null}`: ""} {
			res := admin.Do("POST", "/api/settings/branding/profiles/"+first+"/duplicate", body)
			if want == "" {
				if res.Status != http.StatusOK {
					t.Errorf("duplicate %s: %d %s", body, res.Status, res.Body)
				}
				continue
			}
			if res.Status != http.StatusBadRequest || res.Body != `{"success":false,"error":"`+want+`"}` {
				t.Errorf("duplicate %s: %d %s", body, res.Status, res.Body)
			}
		}
		if rows := w.AuditRows(); len(rows) != before {
			t.Errorf("a duplicate was audited: %v", rows[before:])
		}

		if res := admin.Do("DELETE", "/api/settings/branding/profiles/"+second, ""); res.Status != http.StatusBadRequest || res.Body != `{"success":false,"error":"Cannot delete the active profile"}` {
			t.Errorf("delete the active one: %d %s", res.Status, res.Body)
		}
		if res := admin.Do("DELETE", "/api/settings/branding/profiles/"+copied, ""); res.Body != `{"success":true}` {
			t.Errorf("delete: %d %s", res.Status, res.Body)
		}
		if got := lastAudit(t, w); got != "branding_profile_delete|Deleted profile #"+copied+"|127.0.0.1|1" {
			t.Errorf("delete audit: %s", got)
		}
		// a profile that is not there: 404, and nothing audited (BUGS BR11)
		before = len(w.AuditRows())
		if res := admin.Do("DELETE", "/api/settings/branding/profiles/-0", ""); res.Status != http.StatusNotFound || res.Body != `{"success":false,"error":"Profile not found"}` {
			t.Errorf("delete -0: %d %s", res.Status, res.Body)
		}
		if rows := w.AuditRows(); len(rows) != before {
			t.Errorf("a delete of nothing was audited: %v", rows[before:])
		}
		var count int
		if err := db.QueryRow(t.Context(), "SELECT COUNT(*) FROM branding_profiles WHERE id = ?", copied).Scan(&count); err != nil || count != 0 {
			t.Errorf("the deleted profile is still there: %d %v", count, err)
		}
	})
}
