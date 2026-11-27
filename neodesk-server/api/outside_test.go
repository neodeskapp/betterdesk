package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// gateResult is who answered a request through outsideGate: "api" for
// the whole API, "client" for the client API port's handler, or the
// status of the gate's own answer.
func gateResult(t *testing.T, s *Server, method, target, remote string, header http.Header) string {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("X-Answer", "api") })
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = remote
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	s.outsideGate(next).ServeHTTP(rec, req)
	if who := rec.Header().Get("X-Answer"); who != "" {
		return who
	}
	return http.StatusText(rec.Code)
}

func outsideServer(t *testing.T, cfg *config.Config) *Server {
	t.Helper()
	s := New(cfg, nil, peer.NewMap(), nil, "1.0.0-test")
	s.LimitOutside()
	s.SetOutsideHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("X-Answer", "client") }))
	return s
}

// While the console's client API port runs, a caller from another host
// gets that port's handler on the API port; this machine and
// API_ALLOW_FROM get the whole API.
func TestOutsideCallersGetTheClientAPI(t *testing.T) {
	cfg := config.DefaultConfig()
	nets, err := config.ParseTrustedProxies("192.0.2.7,198.51.100.0/24")
	if err != nil {
		t.Fatal(err)
	}
	cfg.APIAllowFrom = nets
	s := outsideServer(t, cfg)

	for _, c := range []struct {
		name, method, target, remote, want string
	}{
		{"a host on the internet", "GET", "/api/peers", "203.0.113.9:40000", "client"},
		{"a heartbeat from the internet", "POST", "/api/heartbeat", "203.0.113.9:40000", "client"},
		{"an admin route from the internet", "GET", "/api/keys", "203.0.113.9:40000", "client"},
		{"metrics from the internet", "GET", "/metrics", "203.0.113.9:40000", "client"},
		{"IPv6 from the internet", "GET", "/api/peers", "[2001:db8::5]:40000", "client"},
		{"this machine", "GET", "/api/keys", "127.0.0.1:40000", "api"},
		{"this machine over IPv6", "GET", "/api/keys", "[::1]:40000", "api"},
		{"this machine, IPv4 in IPv6", "GET", "/api/keys", "[::ffff:127.0.0.1]:40000", "api"},
		{"an address of API_ALLOW_FROM", "GET", "/api/keys", "192.0.2.7:40000", "api"},
		{"a network of API_ALLOW_FROM", "GET", "/metrics", "198.51.100.200:40000", "api"},
		{"next to an address of API_ALLOW_FROM", "GET", "/api/keys", "192.0.2.8:40000", "client"},
		{"a computer's chat socket", "GET", "/ws/chat/123456789?v=2", "203.0.113.9:40000", "api"},
		{"a computer's chat files", "POST", "/api/chat/device/files", "203.0.113.9:40000", "api"},
	} {
		if got := gateResult(t, s, c.method, c.target, c.remote, nil); got != c.want {
			t.Errorf("%s: %s %s from %s answered by %q, want %q", c.name, c.method, c.target, c.remote, got, c.want)
		}
	}
}

// Prometheus on another host keeps /metrics through METRICS_IP_ALLOWLIST,
// and only the metrics routes.
func TestMetricsAllowlistKeepsTheMetricsRoutes(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.MetricsAllowlist = "192.0.2.50, 198.51.100.0/24"
	s := outsideServer(t, cfg)
	for _, c := range []struct{ target, remote, want string }{
		{"/metrics", "192.0.2.50:9000", "api"},
		{"/api/health/detail", "198.51.100.3:9000", "api"},
		{"/api/server/stats", "198.51.100.3:9000", "api"},
		{"/api/peers", "192.0.2.50:9000", "client"},
		{"/metrics", "203.0.113.9:9000", "client"},
	} {
		if got := gateResult(t, s, "GET", c.target, c.remote, nil); got != c.want {
			t.Errorf("GET %s from %s answered by %q, want %q", c.target, c.remote, got, c.want)
		}
	}

	cfg.MetricsAllowlist = ""
	cfg.MetricsPublic = true
	if got := gateResult(t, s, "GET", "/metrics", "203.0.113.9:9000", nil); got != "api" {
		t.Errorf("METRICS_PUBLIC: /metrics from the internet answered by %q", got)
	}
	if got := gateResult(t, s, "GET", "/api/keys", "203.0.113.9:9000", nil); got != "client" {
		t.Errorf("METRICS_PUBLIC: /api/keys from the internet answered by %q", got)
	}
}

// A proxy on this machine does not make its clients local: behind a
// trusted proxy the forwarded client decides.
func TestOutsideCallerBehindATrustedProxy(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.TrustProxy = true
	nets, err := config.ParseTrustedProxies("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	cfg.TrustedProxies = nets
	s := outsideServer(t, cfg)
	forwarded := http.Header{"X-Forwarded-For": {"203.0.113.9"}}
	if got := gateResult(t, s, "GET", "/api/keys", "127.0.0.1:5000", forwarded); got != "client" {
		t.Errorf("a client forwarded by the local proxy answered by %q, want the client API", got)
	}
	if got := gateResult(t, s, "GET", "/api/keys", "127.0.0.1:5000", nil); got != "api" {
		t.Errorf("this machine without a forwarded address answered by %q, want the whole API", got)
	}
	// A host that is no trusted proxy cannot claim to be local
	spoofed := http.Header{"X-Forwarded-For": {"127.0.0.1"}}
	if got := gateResult(t, s, "GET", "/api/keys", "203.0.113.9:5000", spoofed); got != "client" {
		t.Errorf("a forwarded loopback address from the internet answered by %q, want the client API", got)
	}
}

// Until the console is up outside callers get 503 with Retry-After, and
// after a console that did not start 503 without it; never the whole API.
func TestOutsideCallersWhileTheConsoleIsNotUp(t *testing.T) {
	s := New(config.DefaultConfig(), nil, peer.NewMap(), nil, "1.0.0-test")
	s.LimitOutside()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Errorf("the whole API answered an outside caller") })

	req := httptest.NewRequest("GET", "/api/keys", nil)
	req.RemoteAddr = "203.0.113.9:40000"
	rec := httptest.NewRecorder()
	s.outsideGate(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("while starting: %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}

	s.NoOutsideHandler()
	rec = httptest.NewRecorder()
	s.outsideGate(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "" {
		t.Fatalf("without a console: %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}

	if got := gateResult(t, s, "GET", "/api/keys", "127.0.0.1:40000", nil); got != "api" {
		t.Errorf("this machine without a console answered by %q, want the whole API", got)
	}
}

// Without a client API port in this process (Docker, API_ENABLED=false)
// the API port is the client API port: everyone gets the whole API.
func TestNoLimitWithoutAClientAPIPort(t *testing.T) {
	s := New(config.DefaultConfig(), nil, peer.NewMap(), nil, "1.0.0-test")
	if got := gateResult(t, s, "GET", "/api/keys", "203.0.113.9:40000", nil); got != "api" {
		t.Errorf("an outside caller without LimitOutside answered by %q, want the whole API", got)
	}
}

// The API's listeners serve through the gate.
func TestAPIListenerServesThroughTheGate(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.APIPort = freeTCPPort(t)
	database := testSetupDB(t)
	defer database.Close()
	s := New(cfg, database, peer.NewMap(), nil, "1.0.0-test")
	s.LimitOutside()
	s.SetOutsideHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	if err := s.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	for _, c := range []struct {
		remote string
		want   int
	}{
		{"203.0.113.9:40000", http.StatusTeapot},
		{"127.0.0.1:40000", http.StatusUnauthorized},
	} {
		req := httptest.NewRequest("GET", "/api/keys", nil)
		req.RemoteAddr = c.remote
		rec := httptest.NewRecorder()
		s.httpSrv.Handler.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("GET /api/keys from %s: %d, want %d", c.remote, rec.Code, c.want)
		}
	}
}
