package settings_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestSMTPSettings goes through the three SMTP routes under
// /api/settings/email: the guards, a read without settings, a refused and
// a taken save with its audit entry, the masked password, and a
// connection test against a port where nothing listens.
func TestSMTPSettings(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db)
		admin := w.As("admin")
		const path = "/api/settings/email/smtp"
		json := []string{"Content-Type", "application/json"}
		if res := w.SignedOut().Do("GET", path, ""); res.Status != http.StatusUnauthorized {
			t.Errorf("signed out: %d %s", res.Status, res.Body)
		}
		for _, role := range []string{"operator", "global_admin"} {
			for _, req := range [][2]string{{"GET", path}, {"PUT", path}, {"POST", path + "/test"}} {
				if res := w.As(role).Do(req[0], req[1], `{"host":"127.0.0.1"}`, json...); res.Status != http.StatusForbidden {
					t.Errorf("%s %s as %s: %d %s", req[0], req[1], role, res.Status, res.Body)
				}
			}
		}
		if res := admin.Do("GET", path, ""); res.Status != http.StatusOK || res.Body != `{"configured":false}` {
			t.Errorf("first read: %d %s", res.Status, res.Body)
		}
		if res := admin.Do("PUT", path, `{"port":1}`, json...); res.Status != http.StatusBadRequest || res.Body != `{"error":"SMTP host is required"}` {
			t.Errorf("save without host: %d %s", res.Status, res.Body)
		}
		save := `{"host":" 127.0.0.1 ","port":"1","user":"contract","pass":"smtp-test-pass","from":"console@example.com","alert_email":" ops@example.com ","tlsVerify":false}`
		if res := admin.Do("PUT", path, save, json...); res.Status != http.StatusOK || res.Body != `{"success":true}` {
			t.Errorf("save: %d %s", res.Status, res.Body)
		}
		res := admin.Do("GET", path, "")
		want := `{"configured":true,"host":"127.0.0.1","port":1,"secure":false,"user":"contract","pass":"********","from":"console@example.com","alert_email":"ops@example.com","tlsVerify":false}`
		if res.Body != want {
			t.Errorf("read after save:\n%s\nwant\n%s", res.Body, want)
		}
		if strings.Contains(res.Body, "smtp-test-pass") {
			t.Error("the password is in the answer")
		}
		if rows := w.AuditRows(); len(rows) != 1 || !strings.HasPrefix(rows[0], "smtp_config_updated|SMTP: 127.0.0.1:1|") {
			t.Errorf("audit %q", rows)
		}
		if res := admin.Do("POST", path+"/test", `{}`, json...); res.Status != http.StatusOK || res.Body != `{"success":false,"error":"connect ECONNREFUSED 127.0.0.1:1"}` {
			t.Errorf("test: %d %s", res.Status, res.Body)
		}
		if res := w.As("server_admin").Do("POST", path+"/test", `{}`, json...); res.Status != http.StatusOK {
			t.Errorf("test as server_admin: %d %s", res.Status, res.Body)
		}
	})
}
