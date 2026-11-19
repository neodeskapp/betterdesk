package keys_test

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/i18n"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/keys"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
)

// validKey is a public key a client accepts: 32 bytes in base64.
var validKey = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

const apiKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// bullets is what stands in for the masked API key when there is none.
const bullets = "\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022"

// world is the keys group on a folder of key files, with a fake API
// server behind it.
type world struct {
	t      *testing.T
	dir    string
	router *kit.Router
	bundle *i18n.Bundle
	// serverKey is what the API server answers for its key; asked counts
	// the requests.
	serverKey string
	asked     atomic.Int32
}

// languages loads the console's language files once for all tests.
var languages = sync.OnceValue(func() *i18n.Bundle {
	bundle := i18n.New(filepath.Join("..", "..", "static", "files", "lang"), "en")
	bundle.Logf = func(string, ...any) {}
	bundle.Load()
	return bundle
})

// newWorld sets the group up. files are written into the keys folder
// (id_ed25519.pub, .api_key); env is the process environment.
func newWorld(t *testing.T, files, env map[string]string) *world {
	t.Helper()
	w := &world{t: t, dir: t.TempDir(), router: kit.NewRouter()}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(w.dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	w.bundle = languages()
	api := goapi.New(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/server-key" {
			http.NotFound(rw, r)
			return
		}
		w.asked.Add(1)
		rw.Header().Set("Content-Type", "application/json")
		_, _ = rw.Write([]byte(`{"key":"` + w.serverKey + `"}`))
	}))
	info := serverinfo.New(serverinfo.Options{
		PubKeyPath: filepath.Join(w.dir, "id_ed25519.pub"),
		APIKeyPath: filepath.Join(w.dir, ".api_key"),
		APIURL:     "http://127.0.0.1:21114/api",
		GoAPIPort:  21114,
		APIPort:    21121,
		DataDir:    w.dir,
		EnvPath:    filepath.Join(w.dir, ".env"),
		Getenv:     func(key string) string { return env[key] },
		GoAPI:      api,
		QR:         func(text string) (string, error) { return "qr:" + text, nil },
		Logger:     kit.NewLogger("error", true),
	})
	keys.Register(&kit.Deps{Router: w.router, ServerInfo: info})
	return w
}

// get sends a GET as an account of the role, "" for nobody signed in.
// header is a list of name, value pairs.
func (w *world) get(role, target string, header ...string) *httptest.ResponseRecorder {
	w.t.Helper()
	return w.do(http.MethodGet, role, target, header...)
}

func (w *world) do(method, role, target string, header ...string) *httptest.ResponseRecorder {
	w.t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req.Host = "console.test:5000"
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	req = kit.Begin(req, httpx.TrustProxy{}, w.bundle)
	kit.SetLanguage(req, "en")
	if role != "" {
		kit.SetAccess(req, &access.Context{UserID: 7, HasUser: true, Role: role})
	}
	rec := httptest.NewRecorder()
	if !w.router.Serve(rec, req) {
		w.t.Fatalf("%s %s: no route", method, target)
	}
	return rec
}

// want fails unless the answer has the status and exactly the body.
func want(t *testing.T, name string, rec *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	if rec.Code != status || rec.Body.String() != body {
		t.Errorf("%s: %d %s\nwant %d %s", name, rec.Code, rec.Body.String(), status, body)
	}
}

const signedOut = `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`

var routes = []string{
	"/api/keys/public", "/api/keys/public/qr", "/api/keys/public/download",
	"/api/keys/api", "/api/keys/server-info", "/api/keys/qr",
}

func TestRoutesInTheOrderOfTheOldFile(t *testing.T) {
	w := newWorld(t, nil, nil)
	var got []string
	for _, route := range w.router.Routes() {
		got = append(got, route.Method+" "+route.Path)
	}
	want := "GET " + strings.Join(routes, ",GET ")
	if strings.Join(got, ",") != want {
		t.Errorf("routes = %v\nwant %s", got, want)
	}
}

func TestEveryRouteNeedsASession(t *testing.T) {
	w := newWorld(t, map[string]string{"id_ed25519.pub": validKey, ".api_key": apiKey}, nil)
	for _, route := range routes {
		want(t, route, w.get("", route), 401, signedOut)
		if rec := w.get("viewer", route); rec.Code != 200 {
			t.Errorf("%s as viewer: %d %s", route, rec.Code, rec.Body.String())
		}
	}
	// Nobody signed in gets as far as the permission check of the full key
	want(t, "full key signed out", w.get("", "/api/keys/api?show=true"), 401, signedOut)
}

func TestPublicKey(t *testing.T) {
	w := newWorld(t, map[string]string{"id_ed25519.pub": "  " + validKey + "\n"}, nil)
	want(t, "from the file", w.get("viewer", "/api/keys/public"), 200, `{"success":true,"data":{"key":"`+validKey+`"}}`)
	if w.asked.Load() != 0 {
		t.Error("the server was asked although the key file is valid")
	}

	// Without a valid key file the server's own key is the answer
	w = newWorld(t, map[string]string{"id_ed25519.pub": "YOUR_PUBLIC_KEY"}, nil)
	w.serverKey = validKey
	want(t, "from the server", w.get("viewer", "/api/keys/public"), 200, `{"success":true,"data":{"key":"`+validKey+`"}}`)
	if w.asked.Load() != 1 {
		t.Errorf("the server was asked %d times, want once", w.asked.Load())
	}

	w = newWorld(t, nil, nil)
	want(t, "no key anywhere", w.get("viewer", "/api/keys/public"), 404, `{"success":false,"error":"Key not found"}`)
	w.serverKey = "not a key"
	want(t, "the server has no valid key", w.get("viewer", "/api/keys/public"), 404, `{"success":false,"error":"Key not found"}`)
}

func TestNotFoundSpeaksTheRequestLanguage(t *testing.T) {
	w := newWorld(t, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/keys/api", nil)
	req = kit.Begin(req, httpx.TrustProxy{}, w.bundle)
	kit.SetLanguage(req, "de")
	kit.SetAccess(req, &access.Context{UserID: 7, HasUser: true, Role: "viewer"})
	rec := httptest.NewRecorder()
	w.router.Serve(rec, req)
	want(t, "German", rec, 404, `{"success":false,"error":"`+w.bundle.T("de", "keys.api_not_found", nil)+`"}`)
	if w.bundle.T("de", "keys.api_not_found", nil) == "API key not found" {
		t.Error("the German text is the English one: the test proves nothing")
	}
}

func TestDownload(t *testing.T) {
	w := newWorld(t, map[string]string{"id_ed25519.pub": validKey + "\n"}, nil)
	rec := w.get("viewer", "/api/keys/public/download")
	want(t, "download", rec, 200, validKey)
	h := rec.Header()
	if h.Get("Content-Type") != "text/plain; charset=utf-8" || h.Get("Content-Disposition") != `attachment; filename="id_ed25519.pub"` {
		t.Errorf("headers = %v", h)
	}
	if h.Get("ETag") != httpx.ETag([]byte(validKey)) {
		t.Errorf("ETag = %q", h.Get("ETag"))
	}
	if rec := w.get("viewer", "/api/keys/public/download", "If-None-Match", h.Get("ETag")); rec.Code != 304 || rec.Body.Len() != 0 {
		t.Errorf("same tag again: %d %q", rec.Code, rec.Body.String())
	}
	if rec := w.do(http.MethodHead, "viewer", "/api/keys/public/download"); rec.Code != 200 || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "44" {
		t.Errorf("HEAD: %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}

	w = newWorld(t, nil, nil)
	rec = w.get("viewer", "/api/keys/public/download")
	want(t, "no key", rec, 404, `{"success":false,"error":"Key not found"}`)
	if rec.Header().Get("Content-Disposition") != "" {
		t.Error("a refusal is sent as a download")
	}
}

func TestAPIKey(t *testing.T) {
	w := newWorld(t, map[string]string{".api_key": apiKey + "\n"}, nil)
	masked := `{"success":true,"data":{"key":"0123****cdef","masked":true}}`
	full := `{"success":true,"data":{"key":"` + apiKey + `","masked":false}}`
	denied := `{"success":false,"error":"Permission denied: server.keys"}`

	for _, role := range []string{"guest_issuer", "viewer", "operator", "global_admin", "server_admin", "super_admin", "admin"} {
		want(t, role+" masked", w.get(role, "/api/keys/api"), 200, masked)
	}
	// server.keys: the roles that run the server
	for _, role := range []string{"server_admin", "super_admin", "admin"} {
		want(t, role+" full", w.get(role, "/api/keys/api?show=true"), 200, full)
	}
	for _, role := range []string{"guest_issuer", "viewer", "operator", "global_admin"} {
		want(t, role+" full", w.get(role, "/api/keys/api?show=true"), 403, denied)
	}

	// Only the text "true", given once, asks for the full key
	for _, query := range []string{"show=false", "show=TRUE", "show=1", "show=", "show", "show=true&show=true", "show[]=true", "show%5Bx%5D=true", "other=true"} {
		want(t, query, w.get("viewer", "/api/keys/api?"+query), 200, masked)
		want(t, query+" as admin", w.get("admin", "/api/keys/api?"+query), 200, masked)
	}
	want(t, "encoded", w.get("viewer", "/api/keys/api?%73how=%74rue"), 403, denied)
	want(t, "among others", w.get("admin", "/api/keys/api?a=1&show=true&b=2"), 200, full)
}

func TestAPIKeyThatIsMissingOrEmpty(t *testing.T) {
	notFound := `{"success":false,"error":"API key not found"}`
	w := newWorld(t, nil, nil)
	want(t, "no file", w.get("admin", "/api/keys/api"), 404, notFound)
	want(t, "no file, full", w.get("admin", "/api/keys/api?show=true"), 404, notFound)
	// The permission is checked before the file is read
	want(t, "no file, full, viewer", w.get("viewer", "/api/keys/api?show=true"), 403, `{"success":false,"error":"Permission denied: server.keys"}`)

	w = newWorld(t, map[string]string{".api_key": " \n"}, nil)
	want(t, "empty file", w.get("admin", "/api/keys/api"), 404, notFound)

	// A short key shows at most a quarter at each end (BUGS SI4)
	w = newWorld(t, map[string]string{".api_key": "12345678"}, nil)
	want(t, "short key", w.get("viewer", "/api/keys/api"), 200, `{"success":true,"data":{"key":"12****78","masked":true}}`)
}

func TestServerInfo(t *testing.T) {
	w := newWorld(t, map[string]string{".api_key": apiKey}, nil)
	// The host of the configured API address wins over the request's own
	// host (BUGS SI2). A host asked for keeps its letter case and loses
	// its port.
	want(t, "default", w.get("viewer", "/api/keys/server-info"), 200,
		`{"success":true,"data":{"server_id":"127.0.0.1","relay_server":"127.0.0.1:21117","api_url":"http://127.0.0.1:21114","api_key_masked":"0123****cdef",`+
			`"endpoint_sources":{"host":"config","relay":"config","api":"config"},"env_override_active":false}}`)
	want(t, "a host asked for", w.get("viewer", "/api/keys/server-info?host=Console.Example.org:5000"), 200,
		`{"success":true,"data":{"server_id":"Console.Example.org","relay_server":"Console.Example.org:21117","api_url":"http://Console.Example.org:21114","api_key_masked":"0123****cdef",`+
			`"endpoint_sources":{"host":"query","relay":"query","api":"query"},"env_override_active":false}}`)
	for _, query := range []string{"host=a.example&host=b.example", "host[]=a.example", "host%5Bx%5D=a.example", "host=not%20a%20host", "host="} {
		rec := w.get("viewer", "/api/keys/server-info?"+query)
		if !strings.Contains(rec.Body.String(), `"server_id":"127.0.0.1"`) || !strings.Contains(rec.Body.String(), `"host":"config"`) {
			t.Errorf("%s: %s", query, rec.Body.String())
		}
	}

	w = newWorld(t, nil, map[string]string{"PUBLIC_SERVER_ID": "id.example.org", "PUBLIC_RELAY_SERVER": "relay.example.org"})
	want(t, "public endpoints and no key", w.get("viewer", "/api/keys/server-info?host=other.example.org"), 200,
		`{"success":true,"data":{"server_id":"id.example.org","relay_server":"relay.example.org:21117","api_url":"http://id.example.org:21114","api_key_masked":"`+bullets+`",`+
			`"endpoint_sources":{"host":"env","relay":"env","api":"derived"},"env_override_active":true}}`)

	w = newWorld(t, map[string]string{".api_key": ""}, nil)
	if rec := w.get("viewer", "/api/keys/server-info"); !strings.Contains(rec.Body.String(), `"api_key_masked":"`+bullets+`"`) {
		t.Errorf("empty key file: %s", rec.Body.String())
	}
}

func TestConfigQR(t *testing.T) {
	for _, route := range []string{"/api/keys/public/qr", "/api/keys/qr"} {
		w := newWorld(t, map[string]string{"id_ed25519.pub": validKey}, nil)
		payload := serverinfo.ConfigPayload{Host: "127.0.0.1", Relay: "127.0.0.1:21117", API: "http://127.0.0.1:21114", Key: validKey}
		want(t, route, w.get("viewer", route), 200,
			`{"success":true,"data":{"qr":"qr:`+serverinfo.EncodeRustDeskConfigURI(payload)+`","server_id":"127.0.0.1","relay_server":"127.0.0.1:21117",`+
				`"api_url":"http://127.0.0.1:21114","phone_unreachable_host":true}}`)

		payload = serverinfo.ConfigPayload{Host: "desk.example.org", Relay: "desk.example.org:21117", API: "http://desk.example.org:21114", Key: validKey}
		want(t, route+" for a host", w.get("viewer", route+"?host=desk.example.org"), 200,
			`{"success":true,"data":{"qr":"qr:`+serverinfo.EncodeRustDeskConfigURI(payload)+`","server_id":"desk.example.org","relay_server":"desk.example.org:21117",`+
				`"api_url":"http://desk.example.org:21114","phone_unreachable_host":false}}`)
		if rec := w.get("viewer", route+"?host=Printer.LOCAL"); !strings.Contains(rec.Body.String(), `"server_id":"Printer.LOCAL"`) || !strings.Contains(rec.Body.String(), `"phone_unreachable_host":true`) {
			t.Errorf("%s for a .local name: %s", route, rec.Body.String())
		}

		// A relay of its own is named beside the host
		w = newWorld(t, map[string]string{"id_ed25519.pub": validKey}, map[string]string{"PUBLIC_RELAY_SERVER": "relay.example.org"})
		if rec := w.get("viewer", route); !strings.Contains(rec.Body.String(), `"server_id":"127.0.0.1","relay_server":"relay.example.org:21117","api_url":"http://127.0.0.1:21114"`) {
			t.Errorf("%s with a public relay: %s", route, rec.Body.String())
		}

		w = newWorld(t, nil, nil)
		want(t, route+" without a key", w.get("viewer", route), 404, `{"success":false,"error":"Key not found"}`)
	}
}
