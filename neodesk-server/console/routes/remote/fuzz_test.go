package remote

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/guestaccess"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/i18n"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/limits"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/webui"
)

// fuzzAPI answers a guest link as good for dev-1 only and a share link
// as good for no device; everything else is not found.
func fuzzAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasPrefix(r.RequestURI, "/api/guest/access-links/validate"):
		_, _ = w.Write([]byte(`{"valid":true,"id":"g1","peer_ids":["dev-1"],"expires_at":"2099-01-01T00:00:00Z"}`))
	case strings.HasPrefix(r.RequestURI, "/api/guest/access-links/peers"):
		_, _ = w.Write([]byte(`{"valid":true,"devices":[]}`))
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	}
}

// FuzzRequests sends any device id, query and cookie to the routes that
// read them, signed out: each answers or drops the connection where the
// old console stopped (http.ErrAbortHandler), nothing else panics, and the
// viewer opens only for dev-1, the one device the scripted guest link
// reaches.
func FuzzRequests(f *testing.F) {
	for _, seed := range [][3]string{
		{"dev-1", "guest=tok", ""}, {"rt_abcdefgh", "transport=mesh", ""}, {"dev-1", "mesh_share=s&t=x", "bd.guest=tok"},
		{"900100001", "mock=1&scenario=SLOW", ""}, {"a%20b", "guest[toString]=1", ""}, {"dev-2", "return=/remote", "bd.guest=j:5"},
		{"dev-1", "t[a][b]=1&t=2", "bd.guest=%zz"}, {"login", "return=%2F%2Fevil", ""},
	} {
		f.Add(seed[0], seed[1], seed[2])
	}
	bundle := i18n.New(f.TempDir(), "en")
	bundle.Logf = func(string, ...any) {}
	bundle.Load()
	api := goapi.New(http.HandlerFunc(fuzzAPI))
	d := &kit.Deps{
		Router: kit.NewRouter(), Early: kit.NewRouter(), GoAPI: api, Log: kit.NewLogger("silent", true),
		Limits:      limits.NewSet(limits.Options{ClientIP: httpx.SocketIP, UserKey: func(*http.Request) string { return "" }, Getenv: func(string) string { return "100000000" }}),
		WebUI:       webui.New(webui.Options{}),
		GuestAccess: guestaccess.New(guestaccess.Options{GoAPI: api}),
		I18n:        &i18n.Handlers{Bundle: bundle},
	}
	w := &world{deps: d}
	src := w.sources("1.0.0")
	src.languages = bundle.AvailableList
	src.mockOn = true
	register(d, src)
	steps := middleware.Bodies(middleware.BodyOptions{})(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		r = kit.Begin(r, httpx.TrustProxy{}, bundle)
		if !d.Router.Serve(rw, r) {
			rw.WriteHeader(http.StatusNotFound)
		}
	}))
	f.Fuzz(func(t *testing.T, id, query, cookie string) {
		for _, path := range []string{"/api/remote/viewer/", "/api/remote/guest-info", "/api/remote/login-info", "/remote-cdap/", "/api/remote/session/"} {
			target := path
			if strings.HasSuffix(path, "/") {
				target += id
			}
			target += "?" + query
			r, err := http.NewRequest(http.MethodGet, "http://console.test"+target, nil)
			if err != nil || strings.ContainsAny(target, " \t\r\n#") {
				continue
			}
			r.RequestURI = target
			if cookie != "" && !strings.ContainsAny(cookie, "\r\n") {
				r.Header.Set("Cookie", cookie)
			}
			rec := httptest.NewRecorder()
			aborted := false
			func() {
				defer func() {
					if p := recover(); p != nil {
						if err, ok := p.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
							t.Fatalf("%s: panic %v", target, p)
						}
						aborted = true
					}
				}()
				steps.ServeHTTP(rec, r)
			}()
			opened, _ := url.PathUnescape(strings.TrimSuffix(strings.TrimPrefix(r.URL.EscapedPath(), path), "/"))
			if !aborted && path == "/api/remote/viewer/" && rec.Code == http.StatusOK && opened != "dev-1" {
				t.Fatalf("%s: the viewer opened for a device the guest link does not reach", target)
			}
		}
	})
}
