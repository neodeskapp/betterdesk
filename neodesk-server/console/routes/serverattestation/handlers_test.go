package serverattestation

import (
	"net/http"
	"testing"
)

// A second start while a test runs is refused with 409, and the state
// shows the test from its start; an abort then reaches it. No test runs
// here: nothing serves the service, so the started test waits.
func TestRunIsRefusedWhileOneRuns(t *testing.T) {
	var logs []logged
	h := testHandlers(newFakeSettings(), "BetterDesk", nil, false, &logs)
	if got := call(h.run, http.MethodPost, "{}"); got.Status != http.StatusOK || got.Body != `{"success":true,"started":true}` {
		t.Fatalf("first run = %d %s", got.Status, got.Body)
	}
	if got := call(h.run, http.MethodPost, "{}"); got.Status != http.StatusConflict || got.Body != `{"success":false,"error":"Benchmark already running"}` {
		t.Errorf("second run = %d %s", got.Status, got.Body)
	}
	if got := call(h.status, http.MethodGet, ""); got.Body != `{"success":true,"running":true,"phase":"baseline","progress":null,"lastResult":null}` {
		t.Errorf("status = %s", got.Body)
	}
	if got := call(h.abort, http.MethodPost, "{}"); got.Body != `{"success":true,"aborted":true}` {
		t.Errorf("abort = %s", got.Body)
	}
	if got := call(h.abort, http.MethodPost, "{}"); got.Body != `{"success":true,"aborted":true}` {
		t.Errorf("second abort = %s", got.Body)
	}
}
