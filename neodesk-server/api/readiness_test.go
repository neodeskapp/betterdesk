package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHealthSaysNotReadyWithTheReason: a pod that may not take traffic
// answers 503 with why, and 200 again once it may.
func TestHealthSaysNotReadyWithTheReason(t *testing.T) {
	s := &Server{version: "test"}
	rec := httptest.NewRecorder()
	s.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("without readiness: %d", rec.Code)
	}

	ready, reason := false, "Valkey does not answer"
	s.SetReadiness(func() (bool, string) { return ready, reason })
	rec = httptest.NewRecorder()
	s.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), reason) {
		t.Fatalf("not ready: %d %s", rec.Code, rec.Body.String())
	}

	ready = true
	rec = httptest.NewRecorder()
	s.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("ready again: %d", rec.Code)
	}
}
