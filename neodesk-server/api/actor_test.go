package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/billing"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// TestActorIsRecordedAsCreatorAndHandler: an in-process call that names
// the console's signed-in account (Identity.Actor) has that account
// recorded as the creator of a pairing code and a device token, the
// handler of a help request and the operator of a billing work report
// (BUGS G7). Role and permissions still go by the caller's role, and a
// call without an actor records the caller's username.
func TestActorIsRecordedAsCreatorAndHandler(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	srv.SetBillingService(billing.NewService(database, nil, 1, true))
	h := srv.InternalHandler()
	bridge := Identity{Username: "apikey:console-bridge", Role: auth.RoleAdmin, Actor: "alice"}
	call := func(id Identity, method, path, body string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "127.0.0.1:50000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req.WithContext(WithIdentity(req.Context(), id)))
		out := map[string]any{}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	creators := func(path, list string) []string {
		t.Helper()
		_, out := call(bridge, http.MethodGet, path, "")
		items, _ := out[list].([]any)
		var names []string
		for _, item := range items {
			names = append(names, fmt.Sprint(item.(map[string]any)["created_by"]))
		}
		return names
	}

	if status, _ := call(bridge, http.MethodPost, "/api/pairing/codes", `{"label":"x"}`); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("pairing code: %d", status)
	}
	noActor := bridge
	noActor.Actor = ""
	call(noActor, http.MethodPost, "/api/pairing/codes", `{"label":"y"}`)
	if got := strings.Join(creators("/api/pairing/codes", "codes"), ","); got != "alice,apikey:console-bridge" {
		t.Errorf("pairing codes created by %q", got)
	}

	if status, _ := call(bridge, http.MethodPost, "/api/tokens", `{"name":"t","max_uses":1}`); status >= 300 {
		t.Fatalf("device token: %d", status)
	}
	if got := strings.Join(creators("/api/tokens", "tokens"), ","); got != "alice" {
		t.Errorf("device tokens created by %q", got)
	}

	helpID, err := database.CreateHelpRequest(&db.HelpRequest{DeviceID: "PEERAAA1", Message: "help", Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := call(bridge, http.MethodPost, fmt.Sprintf("/api/help/requests/%d/acknowledge", helpID), ""); status >= 300 {
		t.Fatalf("acknowledge: %d", status)
	}
	if req, err := database.GetHelpRequest(helpID); err != nil || req.HandledBy != "alice" {
		t.Errorf("help request handled by %+v, %v", req, err)
	}

	session := &db.BillingSession{ID: "bs-actor", DeviceID: "PEERAAA1", OperatorID: "apikey:console-bridge", Transport: "relay",
		Status: billing.StatusPendingReport, BillingPhase: "included", StartedAt: time.Now().Add(-time.Hour), Currency: "EUR"}
	if err := database.CreateBillingSession(session); err != nil {
		t.Fatal(err)
	}
	if status, out := call(bridge, http.MethodPost, "/api/billing/sessions/bs-actor/report", `{"summary":"done"}`); status >= 300 {
		t.Fatalf("work report: %d %v", status, out)
	}
	if report, err := database.GetBillingWorkReportBySession("bs-actor"); err != nil || report.OperatorID != "alice" {
		t.Errorf("work report by %+v, %v", report, err)
	}

	// The actor decides nothing: a viewer's call that names an admin as
	// its actor is still a viewer's
	viewer := Identity{Username: "apikey:viewer", Role: auth.RoleViewer, Actor: "admin"}
	if status, _ := call(viewer, http.MethodPost, "/api/tokens", `{"name":"v"}`); status != http.StatusForbidden {
		t.Errorf("a viewer naming an admin as actor creates a token: %d", status)
	}
}
