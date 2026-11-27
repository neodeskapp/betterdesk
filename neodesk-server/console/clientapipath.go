package console

import (
	"net/http"
	"net/url"
	"strings"
)

// ClientAPIPath is where the console's own port answers the client API
// as well: a request to ClientAPIPath + "/api/login" gets what the client
// API port (API_PORT, 21121) answers to "/api/login", through the same
// path list, limits and routes. The console's pages use /api/ for
// themselves, so a reverse proxy cannot tell the two apart by path; with
// this, one host name in front of the console carries both, and clients
// get "https://<host>/client-api" as their API address.
const ClientAPIPath = "/client-api"

// serveClientAPIPath answers a request under ClientAPIPath with the
// client API port's handler, the prefix cut from its path. It does
// nothing and returns false for any other request, and when the console
// has no client API (API_ENABLED off).
func (s *Server) serveClientAPIPath(w http.ResponseWriter, r *http.Request) bool {
	if s.net.clientAPI == nil {
		return false
	}
	target := r.RequestURI
	if target == "" {
		target = r.URL.RequestURI()
	}
	rest, ok := strings.CutPrefix(target, ClientAPIPath)
	if !ok || !strings.HasPrefix(rest, "/") {
		return false
	}
	u, err := url.ParseRequestURI(rest)
	if err != nil {
		return false
	}
	inner := r.WithContext(r.Context())
	clean := *r.URL
	clean.Path, clean.RawPath, clean.RawQuery, clean.ForceQuery = u.Path, u.RawPath, u.RawQuery, u.ForceQuery
	inner.URL, inner.RequestURI = &clean, rest
	s.net.clientAPI.Handler().ServeHTTP(w, inner)
	return true
}
