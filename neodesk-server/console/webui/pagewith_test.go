package webui

import (
	"net/http"
	"strings"
	"testing"
)

// TestPageWithPutsMarkupAfterTheBodyTag: a public page the server can say
// without JavaScript (the invite page) gets the markup right after <body>,
// the rest of the page as it is; other pages and a missing build answer
// false and write nothing.
func TestPageWithPutsMarkupAfterTheBodyTag(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, dir string) {
		extra := `<noscript><p>Download</p></noscript>`
		ok := false
		a := record(func(w http.ResponseWriter) { ok = h.PageWith(w, rawRequest("GET", base+"/join/ABCD2345", nil), extra) })
		if !ok || a.status != http.StatusOK {
			t.Fatalf("answered %v with %d", ok, a.status)
		}
		want := strings.Replace(readSite(t, dir, "index.html"), "<body>", "<body>"+extra, 1)
		if a.body != want {
			t.Errorf("body %q", clip(a.body))
		}
		if a.get("Content-Security-Policy") != sitePolicy || a.get("Cache-Control") != "no-store" {
			t.Errorf("headers %v", a.header)
		}
		for _, target := range []string{base + "/settings", "/elsewhere/join/ABCD2345"} {
			if target == "/elsewhere/join/ABCD2345" && base == "" {
				continue
			}
			ok := true
			a := record(func(w http.ResponseWriter) { ok = h.PageWith(w, rawRequest("GET", target, nil), extra) })
			if ok || a.body != "" {
				t.Errorf("%s: answered %v %q", target, ok, clip(a.body))
			}
		}
	})
	if noBuild(t, true).PageWith(nil, rawRequest("GET", "/join/ABCD2345", nil), "x") {
		t.Error("a host without a build answered")
	}
}
