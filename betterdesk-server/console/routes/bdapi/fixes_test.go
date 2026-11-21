package bdapi_test

import (
	"net/http"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
)

// The chat history is the conversation of the device the token is bound
// to: a body that names the own device does not open another device's
// conversation through the query (BUGS RV5).
func TestChatHistoryIsTheTokensDevice(t *testing.T) {
	api := routetest.NewAPI().
		On("GET /api/chat/history/DEV1?limit=100", http.StatusOK, `{"conversation_id":"DEV1","messages":[{"id":1,"conversation_id":"DEV1","from_id":"DEV1","text":"own","created_at":"2026-11-18T10:00:00Z"}]}`).
		On("GET /api/chat/history/DEV2?limit=100", http.StatusOK, `{"conversation_id":"DEV2","messages":[{"id":2,"conversation_id":"DEV2","from_id":"DEV2","text":"secret","created_at":"2026-11-18T10:00:00Z"}]}`)
	w := routetest.New(t, routetest.Options{API: api})
	auth := []string{"Authorization", "Bearer " + deviceToken(t, w, "tok-chat-rv5-0123456789", "DEV1")}

	got := w.SignedOut().Do("GET", "/api/bd/chat/history?device_id=DEV2", `{"device_id":"DEV1"}`, auth...)
	if got.Status == 200 && got.Body != `[{"id":"1","device_id":"DEV1","sender":"DEV1","content":"own","timestamp":"2026-11-18T10:00:00Z"}]` {
		t.Errorf("history with the own id in the body and another in the query: %d %s", got.Status, got.Body)
	}
	for _, call := range api.Calls() {
		if call != "GET /api/chat/history/DEV1?limit=100" {
			t.Errorf("asked the API server %s", call)
		}
	}
	if got := w.SignedOut().Do("GET", "/api/bd/chat/history?device_id=DEV2", "", auth...); got.Status != 403 {
		t.Errorf("another device in the query: %d %s", got.Status, got.Body)
	}
}
