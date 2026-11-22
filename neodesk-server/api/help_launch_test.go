package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// TestResolvingAgainKeepsTheResolveTime: a help request's updated_at is
// the time of its last change of status, which the trends count as the
// day it was resolved. Resolving it again changes nothing, so it keeps the
// time of the first resolve; acknowledging it afterwards is a change and
// writes a new time (BUGS L103).
func TestResolvingAgainKeepsTheResolveTime(t *testing.T) {
	srv, _ := newInternalServer(t, nil)
	_, body := asConsole(t, srv, http.MethodPost, "/api/help/requests", `{"device_id":"PEERAAA1","message":"help"}`)
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.ID <= 0 {
		t.Fatalf("create: %s", body)
	}
	path := "/api/help/requests/" + strconv.FormatInt(created.ID, 10)
	updated := func() (string, string) {
		t.Helper()
		status, body := asConsole(t, srv, http.MethodGet, path, "")
		var r struct {
			Status    string `json:"status"`
			UpdatedAt string `json:"updated_at"`
		}
		if err := json.Unmarshal([]byte(body), &r); status != http.StatusOK || err != nil {
			t.Fatalf("read the request: %d %s", status, body)
		}
		return r.Status, r.UpdatedAt
	}
	if status, body := asConsole(t, srv, http.MethodPost, path+"/resolve", ""); status != http.StatusOK {
		t.Fatalf("resolve: %d %s", status, body)
	}
	_, resolvedAt := updated()
	// The times are stored to the second
	time.Sleep(1100 * time.Millisecond)
	if status, body := asConsole(t, srv, http.MethodPost, path+"/resolve", ""); status != http.StatusOK {
		t.Fatalf("resolve again: %d %s", status, body)
	}
	if status, at := updated(); status != "resolved" || at != resolvedAt {
		t.Errorf("after a second resolve: %s at %s, want resolved at %s", status, at, resolvedAt)
	}
	if status, body := asConsole(t, srv, http.MethodPost, path+"/acknowledge", ""); status != http.StatusOK {
		t.Fatalf("acknowledge: %d %s", status, body)
	}
	if status, at := updated(); status != "acknowledged" || at == resolvedAt {
		t.Errorf("after an acknowledge: %s at %s", status, at)
	}
}
