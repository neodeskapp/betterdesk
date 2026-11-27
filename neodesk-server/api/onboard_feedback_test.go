package api

import (
	"net/http"
	"strings"
	"testing"
)

// TestSupportFeedbackIsStoredOncePerSession: the console stores one
// answer per quick support session; a second one is 409 already_given and
// leaves the first as it was.
func TestSupportFeedbackIsStoredOncePerSession(t *testing.T) {
	srv, _ := newInternalServer(t, nil)
	const path = "/api/onboard/feedback/aaaaaaaaaaaaaaaa"
	if status, _ := asConsole(t, srv, http.MethodGet, path, ""); status != http.StatusNotFound {
		t.Fatalf("no answer yet: %d", status)
	}
	if status, body := asConsole(t, srv, http.MethodPut, path, `{"session_id":"aaaaaaaaaaaaaaaa","answers":[{"id":"q1","value":5}]}`); status != http.StatusOK {
		t.Fatalf("store: %d %s", status, body)
	}
	if status, body := asConsole(t, srv, http.MethodPut, path, `{"session_id":"aaaaaaaaaaaaaaaa","answers":[{"id":"q1","value":1}]}`); status != http.StatusConflict || !strings.Contains(body, "already_given") {
		t.Fatalf("a second answer: %d %s", status, body)
	}
	if status, body := asConsole(t, srv, http.MethodGet, path, ""); status != http.StatusOK || !strings.Contains(body, `"value":5`) {
		t.Fatalf("the first answer: %d %s", status, body)
	}
	if status, body := asConsole(t, srv, http.MethodGet, "/api/onboard/feedback", ""); status != http.StatusOK || strings.Count(body, `"session_id"`) != 1 {
		t.Fatalf("list: %d %s", status, body)
	}
	for _, bad := range []struct{ path, body string }{
		{"/api/onboard/feedback/not-an-id", `{}`},
		{path + "b", `{}`},
		{"/api/onboard/feedback/bbbbbbbbbbbbbbbb", `{"x":`},
		{"/api/onboard/feedback/bbbbbbbbbbbbbbbb", `"` + strings.Repeat("x", supportFeedbackMaxBytes) + `"`},
	} {
		if status, _ := asConsole(t, srv, http.MethodPut, bad.path, bad.body); status != http.StatusBadRequest {
			t.Errorf("PUT %s with %.20s: %d, want 400", bad.path, bad.body, status)
		}
	}
}
