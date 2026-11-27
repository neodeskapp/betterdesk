package onboard_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// redeemOffer opens a quick support session and returns the redeem
// answer's receiver_offer.
func redeemOffer(t *testing.T, w *world) map[string]any {
	t.Helper()
	made := w.browser("operator").do(http.MethodPost, "/api/support/sessions", `{"name":"Ana"}`).json(t)
	res := w.app().do(http.MethodPost, "/api/support/redeem", `{"code":"`+str(made, "code")+`"}`)
	if res.status != http.StatusOK {
		t.Fatalf("redeem: %d %s", res.status, res.body)
	}
	offer, ok := res.json(t)["receiver_offer"].(map[string]any)
	if !ok {
		t.Fatalf("the redeem answer has no receiver_offer: %s", res.body)
	}
	return offer
}

// registerWith registers a new computer with the API server the way the
// installed app does, and returns the status of the answer.
func registerWith(t *testing.T, w *world, deviceID, admission string) string {
	t.Helper()
	body := `{"device_id":"` + deviceID + `","uuid":"machine-` + deviceID + `","hostname":"kept","platform":"windows","admission":"` + admission + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/devices/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.10:40000"
	rec := httptest.NewRecorder()
	w.api.ServeHTTP(rec, req)
	return str(answer{status: rec.Code, body: rec.Body.String()}.json(t), "status")
}

// Quick support offers to keep the app by default: the redeem answer
// carries an admission that admits one computer on this managed server,
// and the second one waits. Switched off, the answer says so and carries
// none. Only server.config changes the setting, and the change is audited.
func TestQuickSupportOffersTheReceiverApp(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, sdb *store.DB) {
		w := newWorld(t, sdb)
		if res := w.browser("admin").do(http.MethodGet, "/api/support/receiver-offer", ""); res.body != `{"success":true,"enabled":true}` {
			t.Fatalf("the setting never saved: %d %s", res.status, res.body)
		}

		offer := redeemOffer(t, w)
		admission, _ := offer["admission"].(string)
		if offer["allowed"] != true || len(admission) != 48 || !strings.HasSuffix(str(offer, "expires_at"), "Z") {
			t.Fatalf("receiver_offer = %v, want allowed with an admission", offer)
		}
		if got := registerWith(t, w, "KEEP00001", admission); got != "approved" {
			t.Fatalf("first computer with the admission: %q, want approved", got)
		}
		if got := registerWith(t, w, "KEEP00002", admission); got != "rejected" {
			t.Fatalf("second computer with the same admission: %q, want rejected", got)
		}

		for _, role := range []string{"operator", "viewer"} {
			if res := w.browser(role).do(http.MethodPut, "/api/support/receiver-offer", `{"enabled":false}`); res.status != http.StatusForbidden {
				t.Errorf("%s switches the offer: %d", role, res.status)
			}
		}
		if res := w.browser("admin").do(http.MethodPut, "/api/support/receiver-offer", `{"enabled":"no"}`); res.status != http.StatusBadRequest {
			t.Errorf("a body without a switch: %d", res.status)
		}
		if res := w.browser("admin").do(http.MethodPut, "/api/support/receiver-offer", `{"enabled":false}`); res.body != `{"success":true,"enabled":false}` {
			t.Fatalf("switch off: %d %s", res.status, res.body)
		}
		off := redeemOffer(t, w)
		if off["allowed"] != false || off["admission"] != nil || off["expires_at"] != nil {
			t.Fatalf("receiver_offer with the setting off = %v, want not allowed without an admission", off)
		}
		var audited int
		if err := sdb.QueryRow(t.Context(), `SELECT COUNT(*) FROM audit_log WHERE action = 'quick_support_receiver_offer'`).Scan(&audited); err != nil || audited != 1 {
			t.Errorf("audit entries of the switch: %d %v, want 1", audited, err)
		}
	})
}
