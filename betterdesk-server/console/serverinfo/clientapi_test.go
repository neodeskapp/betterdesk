package serverinfo

import "testing"

// TestClientsGetTheClientAPIPort: while the console listens on the client
// API port (API_ENABLED), clients are given that port as the API address
// on every install type, with https only when the port has TLS; the
// server's own API port (GO_API_PORT, 21114) is given only without it
// (BUGS L76, DECISIONS A166).
func TestClientsGetTheClientAPIPort(t *testing.T) {
	for _, c := range []struct {
		name      string
		opts      Options
		useHTTPS  bool
		want      string
		port      string
		httpsWant bool
	}{
		{"client API on plain HTTP", Options{APIURL: "http://127.0.0.1:21114/api", GoAPIPort: 21114, APIPort: 21121, ClientAPIPort: 21121}, true, "http://desk:21121", "21121", false},
		{"client API with TLS", Options{APIURL: "http://127.0.0.1:21114/api", GoAPIPort: 21114, APIPort: 21121, ClientAPIPort: 21121, ClientAPIHTTPS: true}, false, "https://desk:21121", "21121", true},
		{"client API on another port", Options{GoAPIPort: 21114, APIPort: 31121, ClientAPIPort: 31121}, false, "http://desk:31121", "31121", false},
		{"client API off", Options{APIURL: "http://127.0.0.1:21114/api", GoAPIPort: 21114, APIPort: 21121}, false, "http://desk:21114", "21114", false},
	} {
		s := New(c.opts)
		if got := s.APIURLForHost("desk", c.useHTTPS); got != c.want {
			t.Errorf("%s: APIURLForHost = %q, want %q", c.name, got, c.want)
		}
		if got := s.DefaultAPIPort(); got != c.port {
			t.Errorf("%s: DefaultAPIPort = %q, want %q", c.name, got, c.port)
		}
		if got := s.DefaultUseHTTPS(); got != c.httpsWant {
			t.Errorf("%s: DefaultUseHTTPS = %v, want %v", c.name, got, c.httpsWant)
		}
		if got := s.ResolveRustDeskEndpoints(&RequestHost{Host: "desk:5000"}, "").API; got != c.want {
			t.Errorf("%s: endpoints API = %q, want %q", c.name, got, c.want)
		}
	}
}
