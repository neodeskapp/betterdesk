package api

import (
	"net"
	"net/http"
	"sync/atomic"
)

// Callers outside this machine on the API port. While the console's client
// API port (API_PORT, 21121) runs in this process, the API port (21114,
// and the derived client port) answers the whole API only to:
//
//   - this machine (a loopback address),
//   - the addresses in API_ALLOW_FROM, for scripts and Prometheus,
//   - the addresses in METRICS_IP_ALLOWLIST, for /metrics,
//     /api/health/detail and /api/server/stats only, and every address
//     for those with METRICS_PUBLIC,
//   - the console of another host that presents the panel trust header
//     from an address in PANEL_TRUST_ALLOWED_HOSTS.
//
// Everyone else gets what the client API port answers, through its
// handler: the same path list, rate limits and body limits. That is the
// API a stock RustDesk client calls when its API server setting is empty
// (the ID server's host at the ID server's port minus 2). A computer's
// support chat (device_chat.go) answers everyone, as it does on the API
// port today: the console checks the device token there.
//
// The caller's address is the API's (config ClientIP): behind a trusted
// proxy (TRUST_PROXY and TRUSTED_PROXIES) the forwarded client, so a proxy
// on this machine does not make every client local.
//
// Without a client API port in this process (API_ENABLED=false, as in
// Docker, or CONSOLE_PORT=0) the API port is the client API port, and
// everyone gets the whole API as before.
type outsideAPI struct {
	// limited: the console's client API port runs in this process
	limited atomic.Bool
	// h is the client API port's handler, nil until the console is up
	h atomic.Pointer[http.Handler]
	// failed: the console did not start, so there is no client API
	failed atomic.Bool
}

// outsideRetry is the Retry-After (seconds) while the console starts.
const outsideRetry = "5"

// LimitOutside says that the console's client API port will run in this
// process: callers outside this machine and API_ALLOW_FROM get its
// handler once SetOutsideHandler names it, and 503 until then. Call it
// before Start.
func (s *Server) LimitOutside() {
	s.outside.limited.Store(true)
}

// SetOutsideHandler names the client API port's handler
// (listen.ClientAPI.Handler) that outside callers get. nil means the
// console is not up (yet, or any more).
func (s *Server) SetOutsideHandler(h http.Handler) {
	s.outside.failed.Store(false)
	if h == nil {
		s.outside.h.Store(nil)
		return
	}
	s.outside.h.Store(&h)
}

// NoOutsideHandler says that the console did not start: outside callers
// get 503 and no longer "starting". The whole API stays with this
// machine and API_ALLOW_FROM.
func (s *Server) NoOutsideHandler() {
	s.outside.h.Store(nil)
	s.outside.failed.Store(true)
}

// metricsPaths are the routes behind metricsGuard.
var metricsPaths = map[string]bool{
	"/metrics":           true,
	"/api/health/detail": true,
	"/api/server/stats":  true,
}

// wholeAPICaller reports whether r gets the whole API on the API port.
func (s *Server) wholeAPICaller(r *http.Request) bool {
	if chat, _ := deviceChatRequest(r); chat {
		return true
	}
	ip := net.ParseIP(s.remoteIP(r))
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || inNets(ip, s.cfg.APIAllowFrom) {
		return true
	}
	if metricsPaths[r.URL.Path] && (s.cfg.MetricsPublic || ipInAllowlist(ip.String(), s.cfg.GetMetricsAllowlist())) {
		return true
	}
	return r.Header.Get(PanelTrustHeader) != "" && s.panelTrustPeerAllowed(r)
}

// inNets reports whether ip is in one of nets.
func inNets(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n != nil && n.Contains(ip) {
			return true
		}
	}
	return false
}

// outsideGate hands outside callers to the client API port's handler and
// everyone else to next (the API's ListenerHandler).
func (s *Server) outsideGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.outside.limited.Load() || s.wholeAPICaller(r) {
			next.ServeHTTP(w, r)
			return
		}
		if h := s.outside.h.Load(); h != nil {
			(*h).ServeHTTP(w, r)
			return
		}
		if s.outside.failed.Load() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the client API is not running"})
			return
		}
		w.Header().Set("Retry-After", outsideRetry)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "starting", "code": "starting"})
	})
}
