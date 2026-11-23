package onboard_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestTheDeviceAndTheAppAreWhoActed: the audit entries of what a joining
// device and the quick support app did name no account under Who; the
// supporter and the invite's maker did not do them (L341).
func TestTheDeviceAndTheAppAreWhoActed(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, sdb *store.DB) {
		w := newWorld(t, sdb)
		invite := w.browser("admin").do(http.MethodPost, "/api/onboard/invites", `{"name":"Reception PC","lifetime_hours":24,"uses":1}`).json(t)
		if res := w.app().do(http.MethodPost, "/api/onboard/join", `{"code":"`+str(invite, "code")+`","device_id":"623456789","hostname":"reception"}`); res.status != http.StatusOK {
			t.Fatalf("join: %d %s", res.status, res.body)
		}

		made := w.browser("operator").do(http.MethodPost, "/api/support/sessions", `{"name":"Ana"}`).json(t)
		app := w.app()
		token := str(app.do(http.MethodPost, "/api/support/redeem", `{"code":"`+str(made, "code")+`"}`).json(t), "session_token")
		for _, step := range []struct{ path, body string }{
			{"/api/support/session/device", `{"device_id":"723456789","hostname":"ana-laptop"}`},
			{"/api/support/session/consent", `{"allowed":true}`},
			{"/api/support/session/end", `{}`},
		} {
			if res := app.do(http.MethodPost, step.path, step.body, "Authorization", "Bearer "+token); res.status != http.StatusOK {
				t.Fatalf("%s: %d %s", step.path, res.status, res.body)
			}
		}

		rows, err := sdb.Query(context.Background(), `SELECT action, COALESCE(user_id, 0), details FROM audit_log
			WHERE action IN ('invite_redeemed', 'quick_support_opened', 'quick_support_device', 'quick_support_allowed', 'quick_support_ended')`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		seen := 0
		for rows.Next() {
			var action, details string
			var userID int64
			if err := rows.Scan(&action, &userID, &details); err != nil {
				t.Fatal(err)
			}
			seen++
			if userID != 0 {
				t.Errorf("%s names account %d as who did it: %s", action, userID, details)
			}
		}
		if seen != 5 {
			t.Errorf("%d entries, want 5", seen)
		}
	})
}
