package console

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
)

// TestClientAPIPathOnTheConsolePort: the console's port hands a request
// under /client-api to the client API with the prefix cut off, so one
// host name in front of the console carries the client API too. Paths
// without the prefix stay the console's.
func TestClientAPIPathOnTheConsolePort(t *testing.T) {
	seen := make(chan string, 4)
	w := newWorld(t, setup{
		listener: http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			seen <- r.Method + " " + r.URL.RequestURI()
			rw.WriteHeader(http.StatusTeapot)
		}),
		configure: func(cfg *config.Config) {
			cfg.APIEnabled, cfg.ClientAPIProxy, cfg.APIHost = true, true, "127.0.0.1:1"
		},
	})
	reached := func() string {
		select {
		case got := <-seen:
			return got
		default:
			return ""
		}
	}

	for _, c := range []struct{ method, target, want string }{
		{http.MethodGet, "/client-api/api/health?probe=1", "GET /api/health?probe=1"},
		{http.MethodGet, "/client-api/api/peer-key/ABC123", "GET /api/peer-key/ABC123"},
	} {
		rec := httptest.NewRecorder()
		w.s.ServeHTTP(rec, httptest.NewRequest(c.method, c.target, nil))
		if got := reached(); got != c.want {
			t.Errorf("%s %s reached the API as %q, want %q", c.method, c.target, got, c.want)
		}
		if rec.Code != http.StatusTeapot {
			t.Errorf("%s %s answered %d, want the API's 418", c.method, c.target, rec.Code)
		}
	}

	// The client API's path list still applies under the prefix
	rec := httptest.NewRecorder()
	w.s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/client-api/api/settings", nil))
	if got := reached(); got != "" || rec.Code != http.StatusNotFound {
		t.Errorf("a path off the client API's list: %d, reached the API as %q", rec.Code, got)
	}

	for _, target := range []string{"/api/health", "/client-apix/api/health", "/client-api"} {
		w.s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil))
		if got := reached(); got != "" {
			t.Errorf("%s reached the client API as %q", target, got)
		}
	}
}

// TestClientAPIPathWithoutClientAPI: with API_ENABLED off there is no
// client API, and /client-api is an ordinary console path.
func TestClientAPIPathWithoutClientAPI(t *testing.T) {
	seen := make(chan string, 1)
	w := newWorld(t, setup{
		listener:  http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) { seen <- r.URL.Path }),
		configure: func(cfg *config.Config) { cfg.APIEnabled = false },
	})
	w.s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/client-api/api/health", nil))
	select {
	case got := <-seen:
		t.Errorf("the request reached the API as %q", got)
	default:
	}
}
