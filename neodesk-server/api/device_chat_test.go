package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// TestComputersChatWhileTheConsoleStarts: the API port listens before the
// console is up. A computer's chat asked in that moment gets 503 with a
// short Retry-After, so the client asks again in seconds, not in ten
// minutes as after a 404 (L536). 404 is only for a server without a
// console; once the console is up, it answers.
func TestComputersChatWhileTheConsoleStarts(t *testing.T) {
	srv := New(config.DefaultConfig(), testSetupDB(t), peer.NewMap(), nil, "test")
	h := srv.ListenerHandler()
	ask := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	for _, path := range []string{"/ws/chat/900100001?v=2", "/api/chat/device/files/0123456789abcdef0123456789abcdef", "/api/chat/device/helper/picture"} {
		rec := ask(path)
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" || rec.Header().Get("X-NeoDesk-Chat") != "starting" {
			t.Errorf("%s before the console: %d %v", path, rec.Code, rec.Header())
		}
	}

	srv.SetDeviceChat(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	for _, path := range []string{"/ws/chat/900100001?v=2", "/api/chat/device/helper/picture"} {
		if rec := ask(path); rec.Code != http.StatusTeapot {
			t.Errorf("%s with the console: %d", path, rec.Code)
		}
	}

	// A console that stops (the server shuts down or restarts) comes back
	srv.SetDeviceChat(nil)
	if rec := ask("/ws/chat/900100001?v=2"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("while the console stops: %d, want 503", rec.Code)
	}

	// No console at all (CONSOLE_PORT=0, or one that could not start)
	srv.NoDeviceChat()
	if rec := ask("/ws/chat/900100001?v=2"); rec.Code != http.StatusNotFound {
		t.Errorf("without a console: %d, want 404", rec.Code)
	}
	// The chat of version 1 never was on this port
	srv.SetDeviceChat(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	if rec := ask("/ws/chat/900100001"); rec.Code != http.StatusNotFound {
		t.Errorf("version 1: %d, want 404", rec.Code)
	}
}
