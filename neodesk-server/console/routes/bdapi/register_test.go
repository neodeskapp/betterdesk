package bdapi

import (
	"context"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// An enrollment token admits the one device its template is bound to: a
// device whose binding finds the template bound to another one already (two
// devices redeeming a fresh token at once) is refused, and so is a token no
// template has; a table without the binding columns admits nobody by token
// (BUGS BD9).
func TestBindTemplateAdmitsOnlyTheBoundDevice(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		if _, err := db.Exec(ctx, "INSERT INTO agent_templates (name, enrollment_token) VALUES ('kiosk', 'bd9-token')"); err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			token, device string
			want          bool
		}{
			{"bd9-token", "DEV1", true},
			{"bd9-token", "DEV2", false},
			{"bd9-token", "DEV1", true},
			{"no-such-token", "DEV3", false},
		} {
			if got := bindTemplate(ctx, db, c.token, c.device); got != c.want {
				t.Errorf("bind %s for %s: %v, want %v", c.token, c.device, got, c.want)
			}
		}
		if _, err := db.Exec(ctx, "ALTER TABLE agent_templates DROP COLUMN enrolled_device_id"); err != nil {
			t.Fatal(err)
		}
		if bindTemplate(ctx, db, "bd9-token", "DEV1") {
			t.Error("a table without the binding columns admitted a device by token")
		}
	})
}
