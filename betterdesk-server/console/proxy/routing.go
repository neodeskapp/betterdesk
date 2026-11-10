package proxy

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// RoutingPath is where the routing report answers when it is switched on.
const RoutingPath = "/__bd/routing"

// Who answers a request, as the routing report names it.
const (
	HandlerGo   = "go"
	HandlerNode = "node"
	HandlerNone = "none"
)

// Routing returns the routing report: GET /__bd/routing?method=GET&path=/api/x
// answers {"handler":"go"} when the Go route table has that method and
// path, {"handler":"node"} when the request goes to the Node console, and
// {"handler":"none"} when nothing answers it because there is no Node
// console behind this one.
//
// served is the lookup the console server uses for real requests, so the
// report cannot disagree with what happens. The contract suite reads the
// report to tell ported routes from forwarded ones; the console registers
// it only when CONSOLE_DEBUG_ROUTING=1.
func Routing(served func(*http.Request) bool, hasUpstream bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")

		method := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("method")))
		target := r.URL.Query().Get("path")
		if method == "" || !strings.HasPrefix(target, "/") {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "method and path (starting with /) are required"})
			return
		}
		u, err := url.ParseRequestURI(target)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "path is not a request path"})
			return
		}

		handler := HandlerNone
		switch {
		case served(&http.Request{Method: method, URL: u, Header: http.Header{}}):
			handler = HandlerGo
		case hasUpstream:
			handler = HandlerNode
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"handler": handler})
	}
}
