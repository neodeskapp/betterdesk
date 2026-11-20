package webui

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const browser = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"

const sitePolicy = "default-src 'self'; script-src 'self' 'sha256-abc='; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; object-src 'none'; frame-ancestors 'none'"

func isAppPage(a answer) bool { return strings.Contains(a.body, "console app") }

func wantStatus(t *testing.T, a answer, what string, status int) {
	t.Helper()
	if a.status != status {
		t.Errorf("%s: status %d, want %d (body %q)", what, a.status, status, clip(a.body))
	}
}

func wantRedirect(t *testing.T, a answer, what string, status int, location string) {
	t.Helper()
	if a.status != status || a.get("Location") != location {
		t.Errorf("%s: %d to %q, want %d to %q", what, a.status, a.get("Location"), status, location)
	}
}

func wantPage(t *testing.T, a answer, what string, status int) {
	t.Helper()
	if a.status != status || !isAppPage(a) {
		t.Errorf("%s: status %d, app page %v; want the app page with %d", what, a.status, isAppPage(a), status)
	}
}

// wantDeclined checks that Handle left the request to the server.
func wantDeclined(t *testing.T, a answer, what string) {
	t.Helper()
	if a.handled {
		t.Errorf("%s: Handle answered %d %q, want it left to the server", what, a.status, clip(a.body))
	}
}

// encodedBase is the base as it appears inside a ?return= value.
func encodedBase(base string) string { return strings.ReplaceAll(base, "/", "%2F") }

func TestSignedOutGoesToSignInWithTheWayBack(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, _ string) {
		a := serve(h, "GET", base+"/devices?open=123", nil)
		wantRedirect(t, a, "a console page", 302, base+"/login?return="+encodedBase(base)+"%2Fdevices%3Fopen%3D123")
		if a.get("Vary") != "Accept" || a.get("Content-Type") != "text/plain; charset=utf-8" ||
			a.body != "Found. Redirecting to "+a.get("Location") {
			t.Errorf("redirect without Accept: Vary %q, type %q, body %q", a.get("Vary"), a.get("Content-Type"), a.body)
		}
		a = serve(h, "GET", base+"/devices", map[string]string{"Accept": browser})
		if a.get("Content-Type") != "text/html; charset=utf-8" || a.body != "<p>Found. Redirecting to "+a.get("Location")+"</p>" {
			t.Errorf("redirect for a browser: type %q, body %q", a.get("Content-Type"), a.body)
		}
		a = serve(h, "GET", base+"/devices", map[string]string{"Accept": "image/png"})
		if a.status != 302 || a.body != "" || a.get("Content-Type") != "" || a.get("Content-Length") != "0" {
			t.Errorf("redirect for a client that takes neither text nor HTML: %d, type %q, body %q", a.status, a.get("Content-Type"), a.body)
		}
		a = serve(h, "HEAD", base+"/devices", map[string]string{"Accept": browser})
		if a.status != 302 || a.body != "" || a.get("Content-Length") == "0" || a.get("Location") == "" {
			t.Errorf("HEAD of a console page: %d, body %q, length %q", a.status, a.body, a.get("Content-Length"))
		}
		// A path the sign-in page would not lead back to: the start
		home := base
		if home == "" {
			home = "/"
		}
		a = serve(h, "GET", base+"/organizations/a%5Cb", nil)
		wantRedirect(t, a, "a page whose path holds an encoded backslash", 302, base+"/login?return="+strings.ReplaceAll(home, "/", "%2F"))
	})
}

// A build for another base has its sign-in page under that base: someone
// signed out is sent there, not to /login, which no page answers (W5).
func TestSignedOutUnderAnotherBaseGoesToItsSignIn(t *testing.T) {
	h, _ := testHost(t, "/app2")
	for target, want := range map[string]string{
		"/app2/devices?open=1": "/app2/login?return=%2Fapp2%2Fdevices%3Fopen%3D1",
		"/app2":                "/app2/login?return=%2Fapp2",
		"/app2/":               "/app2/login?return=%2Fapp2%2F",
	} {
		a := serve(h, "GET", target, nil)
		wantRedirect(t, a, target, 302, want)
		// The address it leads to is the sign-in page
		wantPage(t, serve(h, "GET", a.get("Location"), nil), "the sign-in page of "+target, 200)
	}
	a := serve(h, "GET", "/app2/devices", map[string]string{"Cookie": "bd.sid=s%3Agone.sig"})
	wantRedirect(t, a, "after a session ended", 302, "/app2/login?expired=1&return=%2Fapp2%2Fdevices")
	// At the root nothing changes
	root, _ := testHost(t, "")
	wantRedirect(t, serve(root, "GET", "/devices", nil), "at the root", 302, "/login?return=%2Fdevices")
}

func TestProAccountsGetNoConsole(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, _ string) {
		pro := map[string]string{"X-Test-User": "pro"}
		a := serve(h, "GET", base+"/devices", pro)
		wantRedirect(t, a, "a console page for a pro account", 302, base+"/login?return="+encodedBase(base)+"%2Fdevices")
		wantStatus(t, serve(h, "GET", base+"/remote", pro), "the remote dashboard for a pro account", 302)
		wantPage(t, serve(h, "GET", base+"/devices", map[string]string{"X-Test-User": "viewer"}), "a console page for a viewer", 200)
		wantPage(t, serve(h, "GET", base+"/devices", map[string]string{"X-Test-User": "norole"}), "a console page for a session without a role", 200)
	})
}

func TestPublicPagesOpenWithoutSession(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, _ string) {
		for _, p := range []string{"/login", "/login?desktop=1", "/login?autoLaunch=0", "/login/", "/portal", "/d/abc", "/remote/login", "/remote/guest?t=x"} {
			wantPage(t, serve(h, "GET", base+p, nil), p, 200)
		}
	})
}

func TestRemoteViewerOpensWithoutSession(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, _ string) {
		for _, p := range []string{"/remote/900100012", "/remote/900100012?guest=tok", "/remote/rt_abc?mesh_share=x", "/remote/" + strings.Repeat("a", 64)} {
			a := serve(h, "GET", base+p, nil)
			wantPage(t, a, p, 200)
			if !strings.Contains(a.get("Content-Security-Policy"), "frame-ancestors 'none'") {
				t.Errorf("%s: policy %q", p, a.get("Content-Security-Policy"))
			}
		}
	})
}

func TestRemotePagesGoToRemoteSignIn(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, _ string) {
		b := encodedBase(base)
		wantRedirect(t, serve(h, "GET", base+"/remote", nil), "the remote dashboard", 302, base+"/remote/login?return="+b+"%2Fremote")
		wantRedirect(t, serve(h, "GET", base+"/remote/?a=b'c", nil), "with a query", 302, base+"/remote/login?return="+b+"%2Fremote%2F%3Fa%3Db'c")
		// Not a device id: no public page
		for _, p := range []string{"/remote/a", "/remote/ab", "/remote/%3Cb%3E", "/remote/" + strings.Repeat("a", 65)} {
			a := serve(h, "GET", base+p, nil)
			wantStatus(t, a, p, 302)
			if !strings.HasPrefix(a.get("Location"), base+"/remote/login?return=") {
				t.Errorf("%s leads to %q", p, a.get("Location"))
			}
		}
		// No page at all: the not-found page, also signed out
		wantPage(t, serve(h, "GET", base+"/remote/x/y", nil), "/remote/x/y", 404)
		wantStatus(t, serve(h, "GET", base+"/remote", map[string]string{"X-Test-User": "pro"}), "a pro account", 302)
		wantPage(t, serve(h, "GET", base+"/remote", admin), "signed in", 200)
	})
}

func TestPageCarriesThePolicyOfItsBuild(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, dir string) {
		a := serve(h, "GET", base+"/settings?tab=auth", admin)
		wantPage(t, a, "a console page", 200)
		if a.body != readSite(t, dir, "index.html") {
			t.Error("the page is not the build's index.html")
		}
		if got := a.get("Content-Security-Policy"); got != sitePolicy {
			t.Errorf("policy\n got %s\nwant %s", got, sitePolicy)
		}
		if a.get("Cache-Control") != "no-store" || a.get("Content-Type") != "text/html; charset=utf-8" {
			t.Errorf("Cache-Control %q, Content-Type %q", a.get("Cache-Control"), a.get("Content-Type"))
		}
		if a.get("ETag") == "" || a.get("Content-Length") == "" {
			t.Errorf("ETag %q, Content-Length %q", a.get("ETag"), a.get("Content-Length"))
		}
		head := serve(h, "HEAD", base+"/settings", admin)
		if head.status != 200 || head.body != "" || head.get("Content-Length") != a.get("Content-Length") || head.get("ETag") != a.get("ETag") {
			t.Errorf("HEAD: %d, body %q, length %q", head.status, head.body, head.get("Content-Length"))
		}
	})
}

// A page is not stored (no-store), but a browser that asks whether its copy
// still holds gets 304, as from the old console.
func TestPageAnswersNotModified(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, _ string) {
		tag := serve(h, "GET", base+"/login", nil).get("ETag")
		a := serve(h, "GET", base+"/login", map[string]string{"If-None-Match": tag})
		if a.status != 304 || a.body != "" || a.get("Content-Type") != "" || a.get("Content-Length") != "" ||
			a.get("Cache-Control") != "no-store" || a.get("ETag") != tag || a.get("Content-Security-Policy") != sitePolicy {
			t.Errorf("matching tag: %d, headers %v", a.status, a.header)
		}
		wantStatus(t, serve(h, "GET", base+"/login", map[string]string{"If-None-Match": `"other"`}), "another tag", 200)
		wantStatus(t, serve(h, "GET", base+"/login", map[string]string{"If-None-Match": tag, "Cache-Control": "no-cache"}), "a forced reload", 200)
		// The not-found page keeps its status
		wantStatus(t, serve(h, "GET", base+"/no-such-page", map[string]string{"If-None-Match": "*"}), "a 404 page", 404)
	})
}

func TestHTTPSAddsUpgradeInsecureRequests(t *testing.T) {
	dir := copySite(t, "")
	h := New(Options{Dir: dir, Production: true, HTTPS: true})
	a := serve(h, "GET", "/login", nil)
	if got := a.get("Content-Security-Policy"); got != sitePolicy+"; upgrade-insecure-requests" {
		t.Errorf("policy with HTTPS on: %s", got)
	}
}

func TestHeaderPolicy(t *testing.T) {
	cases := []struct {
		meta  string
		https bool
		want  string
	}{
		{"default-src 'self'; frame-ancestors *; script-src 'self'", false, "default-src 'self'; script-src 'self'; frame-ancestors 'none'"},
		{"default-src 'self'; report-uri /x; sandbox allow-scripts;; ", false, "default-src 'self'; frame-ancestors 'none'"},
		{"", false, "default-src 'self'; script-src 'self'; frame-ancestors 'none'"},
		{"", true, "default-src 'self'; script-src 'self'; frame-ancestors 'none'; upgrade-insecure-requests"},
		{"  a b ;c  ", true, "a b; c; frame-ancestors 'none'; upgrade-insecure-requests"},
		{"sandboxed x; report-to g", false, "sandboxed x; report-to g; frame-ancestors 'none'"},
	}
	for _, c := range cases {
		if got := HeaderPolicy(c.meta, c.https); got != c.want {
			t.Errorf("HeaderPolicy(%q, %v) = %q, want %q", c.meta, c.https, got, c.want)
		}
	}
}

func TestReadBase(t *testing.T) {
	cases := []struct {
		html string
		base string
		ok   bool
	}{
		{`{ __sveltekit_x = { base: "/console", assets: "/console" } }`, "/console", true},
		{`{ __sveltekit_x = { base: "", assets: "" } }`, "", true},
		{`{ __sveltekit_x = { base: "https://evil.example" } }`, "", false},
		{`<html>no init script</html>`, "", false},
		{"__sveltekit_1a2b = {\n\t\t\tbase: \"/a/b\"", "/a/b", true},
		{`__sveltekit_x = { base: "/a/" }`, "", false},
	}
	for _, c := range cases {
		if base, ok := readBase(c.html); base != c.base || ok != c.ok {
			t.Errorf("readBase(%q) = %q, %v; want %q, %v", c.html, base, ok, c.base, c.ok)
		}
	}
}

func TestBuildForAnotherBase(t *testing.T) {
	h, _ := testHost(t, "/console")
	if h.Base() != "/console" {
		t.Fatalf("base %q", h.Base())
	}
	wantPage(t, serve(h, "GET", "/console/devices", admin), "/console/devices", 200)
	wantPage(t, serve(h, "GET", "/console", admin), "/console", 200)
	wantPage(t, serve(h, "GET", "/console/", admin), "/console/", 200)
	wantStatus(t, serve(h, "GET", "/console/_ui/immutable/entry/start.t3st.js", nil), "a hashed file", 200)
	wantStatus(t, serve(h, "GET", "/console/licenses.txt", nil), "a build file", 200)
	wantDeclined(t, serve(h, "GET", "/devices", admin), "/devices")
	wantDeclined(t, serve(h, "GET", "/licenses.txt", admin), "/licenses.txt")
	wantDeclined(t, serve(h, "GET", "/_ui/immutable/entry/start.t3st.js", admin), "a hashed file at the root")
	wantDeclined(t, serve(h, "GET", "/consolex", admin), "/consolex")
	// The move from /app to the root only applies to a build for the root
	wantDeclined(t, serve(h, "GET", "/app/devices", admin), "/app/devices")
	// The prefix matches in any letter case, as the old router did
	wantStatus(t, serve(h, "GET", "/CONSOLE/licenses.txt", nil), "the base in capitals", 200)
}

func TestBaseOption(t *testing.T) {
	dir := copySite(t, "")
	base := "/panel"
	h := New(Options{Dir: dir, Production: true, Base: &base, Session: sessionFromHeader})
	wantPage(t, serve(h, "GET", "/panel/devices", admin), "under the given base", 200)
	wantDeclined(t, serve(h, "GET", "/devices", admin), "at the root")
	root := ""
	h = New(Options{Dir: copySite(t, "/app2"), Production: true, Base: &root, Session: sessionFromHeader})
	wantPage(t, serve(h, "GET", "/devices", admin), "a build for /app2 forced to the root", 200)
}

func TestOldAppAddressesMoveToTheRoot(t *testing.T) {
	h, _ := testHost(t, "")
	for from, to := range map[string]string{
		"/app":                         "/",
		"/app/":                        "/",
		"/app?activateAgent=1":         "/?activateAgent=1",
		"/app/devices?open=900100001":  "/devices?open=900100001",
		"/app/_ui/immutable/start.js":  "/_ui/immutable/start.js",
		"/app//evil.example/x":         "/evil.example/x",
		`/app/\evil.example`:           "/evil.example",
		`/app/\/\evil.example`:         "/evil.example",
		"/APP/devices":                 "/devices",
		"/app/a%20b?c=%7B":             "/a%20b?c=%7B",
		`/app/"x"<y>`:                  "/%22x%22%3Cy%3E",
		"/app/x?return=//evil.example": "/x?return=//evil.example",
	} {
		a := serve(h, "GET", from, nil)
		wantRedirect(t, a, from, 301, to)
		if a.body != "Moved Permanently. Redirecting to "+to {
			t.Errorf("%s: body %q", from, a.body)
		}
		wantRedirect(t, serve(h, "HEAD", from, nil), "HEAD "+from, 301, to)
	}
	// Only /app itself and what is under it
	wantPage(t, serve(h, "GET", "/application", admin), "/application", 404)
	wantDeclined(t, serve(h, "POST", "/app/devices", admin), "POST /app/devices")
}

func TestBuildWithoutBaseIsAtTheRoot(t *testing.T) {
	dir := copySite(t, "/app")
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html><head></head><body>console app, no base</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := New(Options{Dir: dir, Production: true, Session: sessionFromHeader})
	if h.Base() != "" {
		t.Fatalf("base %q", h.Base())
	}
	a := serve(h, "GET", "/devices", admin)
	wantPage(t, a, "/devices", 200)
	// A build without a policy gets one that allows the console's own scripts only
	if got := a.get("Content-Security-Policy"); got != "default-src 'self'; script-src 'self'; frame-ancestors 'none'" {
		t.Errorf("policy of a build without one: %s", got)
	}
}

func TestSignedInLeavesTheSignInPage(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, _ string) {
		wantRedirect(t, serve(h, "GET", base+"/login?return=%2Fdevices%3Fopen%3D7", admin), "a return path", 302, "/devices?open=7")
		wantRedirect(t, serve(h, "GET", base+"/login", admin), "no return path", 302, "/")
		wantRedirect(t, serve(h, "GET", base+"/login/?return=/a%20b", map[string]string{"X-Test-User": "viewer"}), "a viewer", 302, "/a%20b")
		wantRedirect(t, serve(h, "GET", base+"/login?return=/a&x=1", map[string]string{"X-Test-User": "norole"}), "a session without a role", 302, "/a")
		for _, bad := range []string{"%2F%2Fevil.example", "https%3A%2F%2Fevil.example", "%2F%255Cevil", "%2Flogin", "/api/x", "devices", "", "/a&return=/b", "%2F%09%2Fevil"} {
			wantRedirect(t, serve(h, "GET", base+"/login?return="+bad, admin), "return="+bad, 302, "/")
		}
		wantRedirect(t, serve(h, "GET", base+"/login?return[]=/devices", admin), "a list", 302, "/")
		wantRedirect(t, serve(h, "GET", base+"/login?return%5Bx%5D=/devices", admin), "an object", 302, "/")
		// The remote client's own sign-in page stays
		wantPage(t, serve(h, "GET", base+"/remote/login?return=/devices", admin), "/remote/login signed in", 200)
	})
}

func TestProAccountKeepsTheSignInPage(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, _ string) {
		wantPage(t, serve(h, "GET", base+"/login?return=/devices", map[string]string{"X-Test-User": "pro"}), "/login for a pro account", 200)
	})
}

func TestExpiredNoticeOnlyAfterASessionEnded(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, _ string) {
		b := encodedBase(base)
		wantRedirect(t, serve(h, "GET", base+"/devices?open=1", nil), "a first visit", 302, base+"/login?return="+b+"%2Fdevices%3Fopen%3D1")
		wantRedirect(t, serve(h, "GET", base+"/devices", map[string]string{"Cookie": "bd.sid=s%3Agone.sig"}), "after a session ended", 302,
			base+"/login?expired=1&return="+b+"%2Fdevices")
		wantRedirect(t, serve(h, "GET", base+"/devices", map[string]string{"Cookie": "theme=dark"}), "another cookie", 302, base+"/login?return="+b+"%2Fdevices")
		// The remote sign-in has no such notice
		wantRedirect(t, serve(h, "GET", base+"/remote", map[string]string{"Cookie": "bd.sid=x"}), "a remote page", 302, base+"/remote/login?return="+b+"%2Fremote")
	})
}

func TestUnknownAddressGetsThePageWith404(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, _ string) {
		for _, p := range []string{"/no-such-page", "/devices/7", "/policies/7/x", "/organizations", "/remote/a/b", "/DEVICES", "/login/x"} {
			for _, user := range []map[string]string{admin, nil, {"X-Test-User": "pro"}} {
				a := serve(h, "GET", base+p, user)
				wantPage(t, a, p, 404)
				if a.get("Content-Security-Policy") != sitePolicy || a.get("Cache-Control") != "no-store" {
					t.Errorf("%s: policy %q, Cache-Control %q", p, a.get("Content-Security-Policy"), a.get("Cache-Control"))
				}
			}
		}
	})
}

func TestKnownPagesAnswer200(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, _ string) {
		for _, p := range []string{"/", "", "/devices", "/devices/", "/devices?open=7", "/policies", "/policies/7", "/organizations/7", "/organizations/a%20b",
			"/organizations/a%2Fb", "/organizations/100%25", "/organizations/caf%C3%A9", "/%64evices", "/files/a/b/c", "/files"} {
			wantPage(t, serve(h, "GET", base+p, admin), p, 200)
		}
		// A known page still sends people to sign in
		wantStatus(t, serve(h, "GET", base+"/devices", nil), "/devices signed out", 302)
	})
}

func TestPathThatDoesNotDecodeIsABadRequest(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, _ string) {
		for _, p := range []string{"/organizations/%E0%A4%A", "/%zz", "/x%", "/%ff", "/%C3%28"} {
			a := serve(h, "GET", base+p, admin)
			if a.status != 400 || a.body != "" || a.get("Content-Length") != "0" || !a.handled {
				t.Errorf("%s: %d %q (handled %v), want an empty 400", p, a.status, a.body, a.handled)
			}
			wantStatus(t, serve(h, "HEAD", base+p, admin), "HEAD "+p, 400)
		}
	})
}

// Under the routes that take the rest of the path as a parameter, and for
// other methods, a path that does not decode is a failed request: the
// answer of ServerError with status 400.
func TestParameterThatDoesNotDecodeIsAFailedRequest(t *testing.T) {
	h, _ := testHost(t, "")
	for _, c := range []struct{ method, target, param string }{
		{"GET", "/app/%E0%A4%A", "%E0%A4%A"}, {"POST", "/app/%zz", "%zz"}, {"GET", "/_ui/immutable/%zz", "%zz"}, {"DELETE", "/_ui/immutable/x%", "x%"},
		{"POST", "/organizations/%E0%A4%A", "/organizations/%E0%A4%A"},
	} {
		// The message says which parameter, also in production (S17)
		a := serve(h, c.method, c.target, map[string]string{"Accept": "application/json"})
		if a.status != 400 || a.body != `{"success":false,"error":"Failed to decode param '`+c.param+`'"}` || !a.handled {
			t.Errorf("%s %s: %d %q (handled %v)", c.method, c.target, a.status, a.body, a.handled)
		}
	}
	dev := New(Options{Dir: copySite(t, ""), Session: sessionFromHeader})
	a := serve(dev, "GET", "/app/x%zz", map[string]string{"Accept": "application/json"})
	if a.status != 400 || a.body != `{"success":false,"error":"Failed to decode param 'x%zz'"}` {
		t.Errorf("outside production: %d %q", a.status, a.body)
	}
	a = serve(dev, "GET", "/app/%zz", map[string]string{"Accept": browser})
	if a.status != 400 || !strings.Contains(a.body, `<meta name="bd-error" content="500" data-message="Failed to decode param &#39;%zz&#39;">`) {
		t.Errorf("for a browser: %d %q", a.status, clip(a.body))
	}

	app2, _ := testHost(t, "/app2")
	wantStatus(t, serve(app2, "GET", "/app2/_ui/immutable/%zz", nil), "under the base", 400)
	wantDeclined(t, serve(app2, "GET", "/other/%zz", nil), "outside the base")
	wantDeclined(t, serve(app2, "GET", "/app/%zz", nil), "/app with a build for another base")
}

func TestMissingFilesAreLeftToTheServer(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, _ string) {
		for _, p := range []string{"/no-such-file.js", "/missing.txt", "/a/b.c", "/_ui/immutable/nope.js", "/favicon.ico", "/x.htm", "/devices.json", "/x.HTML"} {
			wantDeclined(t, serve(h, "GET", base+p, admin), p)
		}
		// Only the last name of the path says whether it is a file
		wantPage(t, serve(h, "GET", base+"/a.b/c", admin), "/a.b/c", 404)
		// An .html address is a page address
		wantPage(t, serve(h, "GET", base+"/x.html", admin), "/x.html", 404)
	})
}

func TestWithoutRouteListEveryAddressIsAPage(t *testing.T) {
	for name, routes := range map[string]*string{
		"no file":       nil,
		"not JSON":      ptr("{not json"),
		"empty list":    ptr("[]"),
		"not a list":    ptr(`{"pattern":"^\\/$"}`),
		"look-ahead":    ptr(`[{"id":"/","pattern":"^\\/$"},{"id":"/x","pattern":"^\\/x(?=y)"}]`),
		"bad pattern":   ptr(`[{"id":"/","pattern":"^\\/("}]`),
		"backreference": ptr(`[{"id":"/","pattern":"^\\/(a)\\1$"}]`),
	} {
		t.Run(name, func(t *testing.T) {
			dir := copySite(t, "")
			file := filepath.Join(dir, "routes.json")
			if routes == nil {
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(file, []byte(*routes), 0o644); err != nil {
				t.Fatal(err)
			}
			logged := captureLog(t)
			h := New(Options{Dir: dir, Production: true, Session: sessionFromHeader})
			wantPage(t, serve(h, "GET", "/no-such-page", admin), "/no-such-page", 200)
			wantStatus(t, serve(h, "GET", "/no-such-page", nil), "signed out", 302)
			unusable := name == "look-ahead" || name == "bad pattern" || name == "backreference"
			if said := strings.Contains(logged.String(), "routes.json is not usable"); said != unusable {
				t.Errorf("logged a pattern that is not usable: %v, want %v (%s)", said, unusable, logged.String())
			}
		})
	}
}

func ptr(s string) *string { return &s }

func TestAPIAndSocketsNeverGetThePage(t *testing.T) {
	h, _ := testHost(t, "")
	for _, p := range []string{"/api/no-such-endpoint", "/api", "/ws/nothing", "/ws", "/API/x", "/Ws"} {
		a := serve(h, "GET", p, with(admin, "Accept", browser))
		wantDeclined(t, a, p)
		if a.status != 404 || isAppPage(a) || a.body != `{"success":false,"error":"Not Found"}` {
			t.Errorf("%s: %d %q", p, a.status, clip(a.body))
		}
	}
	// Only whole path parts
	wantPage(t, serve(h, "GET", "/apix", admin), "/apix", 404)
}

func TestWritesAreNotPages(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, _ string) {
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
			for _, p := range []string{"/devices", "/login", "/licenses.txt", "/_ui/immutable/entry/start.t3st.js"} {
				a := serve(h, method, base+p, with(admin, "Accept", "application/json"))
				wantDeclined(t, a, method+" "+p)
				if a.status != 404 || isAppPage(a) {
					t.Errorf("%s %s: %d %q", method, p, a.status, clip(a.body))
				}
			}
		}
	})
}

func TestWithoutABuildNothingIsAnswered(t *testing.T) {
	h := New(Options{Dir: filepath.Join(t.TempDir(), "missing"), Production: true, Session: sessionFromHeader})
	if h.Base() != "" {
		t.Errorf("base %q", h.Base())
	}
	for _, p := range []string{"/devices", "/login", "/", "/licenses.txt", "/_ui/immutable/entry/start.t3st.js"} {
		a := serve(h, "GET", p, with(admin, "Accept", browser))
		wantDeclined(t, a, p)
		if a.status != 404 || a.body != "Not Found" || a.get("Content-Type") != "text/plain; charset=utf-8" {
			t.Errorf("%s: %d %q (%s)", p, a.status, a.body, a.get("Content-Type"))
		}
	}
	// The old addresses still move: that needs no build
	wantRedirect(t, serve(h, "GET", "/app/devices", nil), "/app/devices", 301, "/devices")
	if h.ErrorPage(nil, rawRequest("GET", "/x", nil), 404, ErrorOptions{}) {
		t.Error("an error page without a build")
	}
}

// Options without a directory mean no build. The directory the server runs
// in is not taken for one, even when it looks like one.
func TestNoDirectoryIsNoBuild(t *testing.T) {
	t.Chdir(copySite(t, ""))
	for _, production := range []bool{true, false} {
		h := New(Options{Production: production, Session: sessionFromHeader})
		for _, p := range []string{"/devices", "/login", "/licenses.txt", startJS} {
			wantDeclined(t, serve(h, "GET", p, admin), p)
		}
	}
}

// Outside production the host follows the build: a changed page or route
// list is read again, a build that appears is served, one that goes away is
// not. In production what was read at the start stays.
func TestBuildIsReadAgainWhenItChanges(t *testing.T) {
	dir := copySite(t, "")
	dev := New(Options{Dir: dir, Session: sessionFromHeader})
	prod := New(Options{Dir: dir, Production: true, Session: sessionFromHeader})
	index := filepath.Join(dir, "index.html")
	routes := filepath.Join(dir, "routes.json")
	write := func(name, content string, at time.Time) {
		t.Helper()
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(name, at, at); err != nil {
			t.Fatal(err)
		}
	}

	wantPage(t, serve(dev, "GET", "/extra", admin), "/extra before", 404)
	write(routes, `[{"id":"/extra","pattern":"^\\/extra\\/?$"}]`, siteTime.Add(time.Minute))
	wantPage(t, serve(dev, "GET", "/extra", admin), "/extra after the route list changed", 200)
	wantPage(t, serve(dev, "GET", "/devices", admin), "/devices after the route list changed", 404)
	wantPage(t, serve(prod, "GET", "/extra", admin), "/extra in production", 404)

	write(index, `<html><head><meta http-equiv="content-security-policy" content="default-src 'none'"></head><body>console app v2</body></html>`, siteTime.Add(time.Minute))
	a := serve(dev, "GET", "/extra", admin)
	if !strings.Contains(a.body, "console app v2") || a.get("Content-Security-Policy") != "default-src 'none'; frame-ancestors 'none'" {
		t.Errorf("after the page changed: %q with %q", clip(a.body), a.get("Content-Security-Policy"))
	}
	if b := serve(prod, "GET", "/devices", admin); strings.Contains(b.body, "v2") || b.get("Content-Security-Policy") != sitePolicy {
		t.Errorf("production follows the build: %q", clip(b.body))
	}

	// Same content written again with the same time: nothing is read
	watched := &watchedFiles{}
	quiet := newHost(Options{Dir: dir, Session: sessionFromHeader}, dir, watched)
	watched.take()
	serve(quiet, "GET", "/extra", admin)
	looked := 0
	for _, access := range watched.take() {
		if access.op != "stat" {
			t.Errorf("an unchanged build made the host %s %s", access.op, access.name)
		}
		looked++
	}
	if looked == 0 {
		t.Error("the host did not look at the build's files at all")
	}

	if err := os.Remove(routes); err != nil {
		t.Fatal(err)
	}
	wantPage(t, serve(dev, "GET", "/anything", admin), "without a route list", 200)

	if err := os.Remove(index); err != nil {
		t.Fatal(err)
	}
	wantDeclined(t, serve(dev, "GET", "/extra", admin), "after the build went away")
	wantPage(t, serve(prod, "GET", "/devices", admin), "production after the build went away", 200)

	write(index, `<html><head></head><body>console app v3</body></html>`, siteTime.Add(2*time.Minute))
	if a := serve(dev, "GET", "/extra", admin); !strings.Contains(a.body, "v3") {
		t.Errorf("after the build came back: %d %q", a.status, clip(a.body))
	}

	late := New(Options{Dir: filepath.Join(dir, "later"), Session: sessionFromHeader})
	wantDeclined(t, serve(late, "GET", "/devices", admin), "before the build exists")
	if err := os.MkdirAll(filepath.Join(dir, "later"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(dir, "later", "index.html"), `<html><head></head><body>console app late</body></html>`, siteTime)
	wantPage(t, serve(late, "GET", "/devices", admin), "once the build exists", 200)
}

// Requests in parallel while the build changes (run with -race).
func TestParallelRequestsWhileTheBuildChanges(t *testing.T) {
	dir := copySite(t, "")
	h := New(Options{Dir: dir, Session: sessionFromHeader})
	index := filepath.Join(dir, "index.html")
	html := readSite(t, dir, "index.html")
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 60; i++ {
				switch (worker + i) % 4 {
				case 0:
					if a := serve(h, "GET", "/devices", admin); a.status != 200 {
						t.Errorf("page: %d", a.status)
					}
				case 1:
					if a := serve(h, "GET", "/_ui/immutable/entry/start.t3st.js", map[string]string{"Accept-Encoding": "br"}); a.status != 200 {
						t.Errorf("hashed file: %d", a.status)
					}
				case 2:
					if a := serve(h, "POST", "/x", map[string]string{"Accept": browser}); a.status != 404 {
						t.Errorf("not found: %d", a.status)
					}
				case 3:
					at := siteTime.Add(time.Duration(i) * time.Second)
					_ = os.WriteFile(index, []byte(html), 0o644)
					_ = os.Chtimes(index, at, at)
				}
			}
		}(worker)
	}
	wg.Wait()
}

func TestRequestLineForms(t *testing.T) {
	h, _ := testHost(t, "")
	// A client-side request (no request line): the URL counts
	r, err := http.NewRequest("GET", "http://console.test/devices?open=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	a := record(func(w http.ResponseWriter) { h.Handle(w, r) })
	wantRedirect(t, a, "a request without RequestURI", 302, "/login?return=%2Fdevices%3Fopen%3D1")

	// A proxy-style request line: the path of the URL is the page
	abs := rawRequest("GET", "http://console.test/login", nil)
	wantPage(t, record(func(w http.ResponseWriter) { h.Handle(w, abs) }), "an absolute request target", 200)
	abs = rawRequest("GET", "http://console.test/devices", nil)
	wantRedirect(t, record(func(w http.ResponseWriter) { h.Handle(w, abs) }), "an absolute target for a console page", 302, "/login?return=%2F")

	// Bytes above 0x7F count as Latin-1 characters
	a = serve(h, "GET", "/app/café", nil)
	wantRedirect(t, a, "a raw byte above 0x7F", 301, "/caf%C3%A9")

	// A request built by hand whose request line is shorter than its path
	odd := rawRequest("GET", "/app", nil)
	odd.RequestURI = "x"
	wantRedirect(t, record(func(w http.ResponseWriter) { h.Handle(w, odd) }), "a request line that is not the path", 301, "/")
}

// An escaped percent sign stays escaped when a path is matched against the
// pages, as in the app: a page whose name holds a percent sign is listed
// with %25 in its pattern.
func TestPagePathKeepsEncodedPercent(t *testing.T) {
	dir := copySite(t, "")
	writeFile(t, filepath.Join(dir, "routes.json"), `[{"id":"/100%","pattern":"^\/100%25\/?$"},{"id":"/[id]","pattern":"^\/item\/([^/]+?)\/?$"}]`)
	h := New(Options{Dir: dir, Production: true, Session: sessionFromHeader})
	wantPage(t, serve(h, "GET", "/100%25", admin), "/100%25", 200)
	wantPage(t, serve(h, "GET", "/100%25/", admin), "/100%25/", 200)
	wantPage(t, serve(h, "GET", "/100%2525", admin), "/100%2525", 404)
	wantPage(t, serve(h, "GET", "/1%300%25", admin), "an escaped digit in the name", 200)
	wantPage(t, serve(h, "GET", "/item/50%25", admin), "a parameter with a percent sign", 200)
}
