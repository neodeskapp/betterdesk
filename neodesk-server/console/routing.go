package console

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// routingPath is where the routing report answers when it is switched on
// (CONSOLE_DEBUG_ROUTING=1).
const routingPath = "/__nd/routing"

// routingReport answers GET /__nd/routing?method=GET&path=/api/x, the
// contract suite's question who answers a request (console/contract
// client.go). The console answers every request itself, so a question
// with a method and a request path gets {"handler":"go"}, with
// upgrade=websocket too. A question without them gets 400 and an error.
func routingReport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	method := strings.TrimSpace(r.URL.Query().Get("method"))
	target := r.URL.Query().Get("path")
	if method == "" || !strings.HasPrefix(target, "/") {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "method and path (starting with /) are required"})
		return
	}
	if _, err := url.ParseRequestURI(target); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "path is not a request path"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"handler": "go"})
}
