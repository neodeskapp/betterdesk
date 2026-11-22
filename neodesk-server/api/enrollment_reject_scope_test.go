package api

import (
	"net/http"
	"strings"
	"testing"
)

// TestEnrollmentRejectTouchesOnlyPendingRequests: rejecting an id that is
// not pending answers 404 and leaves the device alone, and clearing a
// rejection removes only a device row the rejection created; a row that
// existed before keeps its row and loses the rejection's ban (RV171).
func TestEnrollmentRejectTouchesOnlyPendingRequests(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	admin := map[string]string{"X-API-Key": testAPIKey}
	call := func(method, path, body string) (int, string) {
		t.Helper()
		return listenerCall(t, srv, method, path, body, admin)
	}

	if status, body := call(http.MethodPost, "/api/enrollment/reject/PEERAAA1", `{"ban":true}`); status != http.StatusNotFound {
		t.Errorf("reject an enrolled device: %d %s, want 404", status, body)
	}
	if status, body := call(http.MethodPost, "/api/enrollment/clear-rejection/PEERAAA1", `{}`); status != http.StatusNotFound {
		t.Errorf("clear a rejection that was refused: %d %s, want 404", status, body)
	}
	p, _ := database.GetPeer("PEERAAA1")
	if p == nil || p.Banned {
		t.Fatalf("the enrolled device was banned or removed: %+v", p)
	}
	if marker, _ := database.GetConfig(rejectedDevicePrefix + "PEERAAA1"); marker != "" {
		t.Errorf("a rejection marker was written for a device that was not pending: %s", marker)
	}

	// A pending request for an id that has a row already.
	seedPendingEnrollment(t, database, "PEERBBB2")
	if status, body := call(http.MethodPost, "/api/enrollment/reject/PEERBBB2", `{"ban":true}`); status != http.StatusOK {
		t.Fatalf("reject a pending request: %d %s", status, body)
	}
	status, body := call(http.MethodPost, "/api/enrollment/clear-rejection/PEERBBB2", `{}`)
	if status != http.StatusOK || !strings.Contains(body, `"peer_removed":false`) {
		t.Errorf("clear the rejection of a device with an older row: %d %s, want the row kept", status, body)
	}
	if p, _ := database.GetPeer("PEERBBB2"); p == nil || p.Banned {
		t.Errorf("the older row was removed or stays banned: %+v", p)
	}

	// A pending request without a row: the ban creates one, the clear
	// removes it.
	seedPendingEnrollment(t, database, "PENDNEW1")
	if status, body := call(http.MethodPost, "/api/enrollment/reject/PENDNEW1", `{"ban":true}`); status != http.StatusOK {
		t.Fatalf("reject a new pending request: %d %s", status, body)
	}
	if p, _ := database.GetPeer("PENDNEW1"); p == nil || !p.Banned {
		t.Fatalf("reject with ban made no banned row: %+v", p)
	}
	status, body = call(http.MethodPost, "/api/enrollment/clear-rejection/PENDNEW1", `{}`)
	if status != http.StatusOK || !strings.Contains(body, `"peer_removed":true`) {
		t.Errorf("clear the rejection of a new request: %d %s, want the row removed", status, body)
	}
	if p, _ := database.GetPeer("PENDNEW1"); p != nil {
		t.Errorf("the row the rejection created is still there: %+v", p)
	}
}

// TestPendingEnrollmentIsRejectedAndCleared: a pending request rejected
// without a ban leaves the pending list and is marked rejected, without a
// device row; clearing the rejection removes the mark and its decision, and
// a second clear finds nothing to clear.
func TestPendingEnrollmentIsRejectedAndCleared(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	admin := map[string]string{"X-API-Key": testAPIKey}
	call := func(method, path, body string) (int, string) {
		t.Helper()
		return listenerCall(t, srv, method, path, body, admin)
	}
	seedPendingEnrollment(t, database, "PENDPLN1")
	if _, body := call(http.MethodGet, "/api/enrollment/pending", ""); !strings.Contains(body, `"PENDPLN1"`) {
		t.Fatalf("pending list without the seeded request: %s", body)
	}

	if status, body := call(http.MethodPost, "/api/enrollment/reject/PENDPLN1", `{}`); status != http.StatusOK || body != `{"banned":false,"success":true}` {
		t.Fatalf("reject: %d %s", status, body)
	}
	if _, body := call(http.MethodGet, "/api/enrollment/pending", ""); strings.Contains(body, `"PENDPLN1"`) {
		t.Errorf("the rejected request is still pending: %s", body)
	}
	if marker, _ := database.GetConfig(rejectedDevicePrefix + "PENDPLN1"); !strings.Contains(marker, `"rejected":true`) {
		t.Errorf("rejection marker: %q", marker)
	}
	if p, _ := database.GetPeer("PENDPLN1"); p != nil {
		t.Errorf("a reject without ban made a device row: %+v", p)
	}

	status, body := call(http.MethodPost, "/api/enrollment/clear-rejection/PENDPLN1", `{}`)
	if want := `{"device_id":"PENDPLN1","peer_removed":false,"success":true,"unbanned":false}`; status != http.StatusOK || body != want {
		t.Errorf("clear: %d %s, want 200 %s", status, body, want)
	}
	if marker, _ := database.GetConfig(rejectedDevicePrefix + "PENDPLN1"); marker != "" {
		t.Errorf("the rejection marker stays after the clear: %s", marker)
	}
	if decision, _ := database.GetConfig(enrollmentDecisionPrefix + "PENDPLN1"); decision != "" {
		t.Errorf("the rejected decision stays after the clear: %s", decision)
	}
	if status, body := call(http.MethodPost, "/api/enrollment/clear-rejection/PENDPLN1", `{}`); status != http.StatusNotFound {
		t.Errorf("second clear: %d %s, want 404", status, body)
	}
}
