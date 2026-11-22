package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/auth"
	eventsModule "github.com/unitronix/betterdesk-server/events"
)

// TestCreateHelpRequestIsStored: POST /api/help/requests, which the
// console calls for a device's help request, stores the request, answers
// with its id, lists it and publishes it; it needs chat.access (BUGS G1,
// RG12).
func TestCreateHelpRequestIsStored(t *testing.T) {
	srv, _ := newInternalServer(t, nil)
	bus := eventsModule.NewBus()
	srv.SetEventBus(bus)
	sub := bus.Subscribe("help_request")
	defer bus.Unsubscribe(sub)

	status, body := asConsole(t, srv, http.MethodPost, "/api/help/requests",
		`{"device_id":"PEERAAA1","hostname":"host-a","message":"The printer is gone"}`)
	var created struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(body), &created); status != http.StatusOK || err != nil || created.ID <= 0 || created.Status != "pending" {
		t.Fatalf("create: %d %s", status, body)
	}
	status, body = asConsole(t, srv, http.MethodGet, "/api/help/requests", "")
	if status != http.StatusOK || !strings.Contains(body, `"message":"The printer is gone"`) || !strings.Contains(body, `"device_id":"PEERAAA1"`) {
		t.Errorf("list after the create: %d %s", status, body)
	}
	select {
	case ev := <-sub.Ch:
		if ev.Type != "help_request" || ev.Data["device_id"] != "PEERAAA1" {
			t.Errorf("event: %+v", ev)
		}
	default:
		t.Error("no help_request event")
	}

	issuer := Identity{Username: "issuer", Role: auth.RoleGuestIssuer}
	if status, body := callAs(t, srv, issuer, http.MethodPost, "/api/help/requests", `{"device_id":"PEERAAA1"}`); status != http.StatusForbidden {
		t.Errorf("create without chat.access: %d %s", status, body)
	}
}
