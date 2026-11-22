package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/billing"
	"github.com/unitronix/betterdesk-server/db"
)

// TestGetBillingSessionByID: GET /api/billing/sessions/{id} answers one
// session with its device, 404 for an unknown id, and asks billing.view
// like the list.
func TestGetBillingSessionByID(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	if err := database.CreateBillingSession(&db.BillingSession{ID: "bs-one", DeviceID: "PEERAAA1", OperatorID: "alice", Transport: "relay",
		Status: billing.StatusPendingReport, BillingPhase: "included", StartedAt: time.Now().Add(-time.Hour), Currency: "EUR"}); err != nil {
		t.Fatal(err)
	}

	status, body := asConsole(t, srv, http.MethodGet, "/api/billing/sessions/bs-one", "")
	var got struct {
		Session struct {
			ID       string `json:"id"`
			DeviceID string `json:"device_id"`
		} `json:"session"`
		Device *struct {
			ID       string `json:"id"`
			Hostname string `json:"hostname"`
		} `json:"device"`
	}
	if err := json.Unmarshal([]byte(body), &got); status != http.StatusOK || err != nil {
		t.Fatalf("GET bs-one: %d %s", status, body)
	}
	if got.Session.ID != "bs-one" || got.Device == nil || got.Device.ID != "PEERAAA1" || got.Device.Hostname != "host-a" {
		t.Errorf("GET bs-one: %s, want the session with device PEERAAA1", body)
	}
	if status, body := asConsole(t, srv, http.MethodGet, "/api/billing/sessions/nosuch", ""); status != http.StatusNotFound {
		t.Errorf("GET an unknown session: %d %s, want 404", status, body)
	}
	if status, body := asConsole(t, srv, http.MethodGet, "/api/billing/sessions/pending?device_id=PEERAAA1", ""); status != http.StatusOK || !json.Valid([]byte(body)) {
		t.Errorf("the pending route still answers: %d %s", status, body)
	}
	if status, body := listenerCall(t, srv, http.MethodGet, "/api/billing/sessions/bs-one", "", map[string]string{"X-API-Key": testViewerKey}); status != http.StatusForbidden {
		t.Errorf("GET bs-one as a viewer (no billing.view): %d %s, want 403", status, body)
	}
}
