//go:build i18ndiff

package i18n

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestAgainstRunningNode sends the same requests to a running Node console
// (ND_URL) and to the Go handlers on the same language folder (ND_LANG_DIR,
// the repository's by default), and compares status, the headers that
// matter and the decoded body byte for byte.
//
//	go test -tags i18ndiff ./console/i18n/ -run TestAgainstRunningNode -count=1 -v
//
// Where Node answers with brotli, Go answers with gzip or, when the client
// offered nothing else, uncompressed. Go has no brotli decoder either, so
// for those answers only status and headers are compared.
func TestAgainstRunningNode(t *testing.T) {
	node := strings.TrimRight(os.Getenv("ND_URL"), "/")
	if node == "" {
		t.Skip("ND_URL is not set")
	}
	langDir := realLangDir
	b, _ := loaded(t, langDir, "en")
	h := &Handlers{Bundle: b, Logf: func(string, ...any) {}, Now: time.Now}
	// server.js marks every answer that is not a static file as not
	// cacheable before the routes run; the strings endpoint replaces that.
	app := routes(h)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		app.ServeHTTP(w, r)
	}))
	defer srv.Close()

	codes := b.Available().Keys()
	sort.Strings(codes)
	if len(codes) < 20 {
		t.Fatalf("only %d languages in %s", len(codes), langDir)
	}
	unknown := []string{"xx", "EN", "zh-tw", "constructor", "__proto__", "e", "abcd", "..%2Fconfig", "he", "de.json", "toString"}

	type request struct {
		method, path string
		headers      map[string]string
		cacheControl bool
	}
	var requests []request
	add := func(method, path string, cacheControl bool, headers map[string]string) {
		requests = append(requests, request{method, path, headers, cacheControl})
	}
	for _, code := range append(append([]string{}, codes...), unknown...) {
		p := "/api/console/strings/" + code
		first := do(t, node, "GET", p, nil, nil, 0)
		etag := first.header.Get("ETag")
		revision := strings.Trim(etag, `"`)
		add("GET", p, true, nil)
		add("GET", p, true, map[string]string{"Accept-Encoding": "gzip"})
		add("GET", p, true, map[string]string{"Accept-Encoding": "identity"})
		add("GET", p, true, map[string]string{"Accept-Encoding": "gzip, deflate, br"})
		add("GET", p, true, map[string]string{"Accept-Encoding": "br"})
		add("GET", p+"?v="+revision, true, nil)
		add("GET", p+"?v=old", true, map[string]string{"Accept-Encoding": "gzip"})
		add("GET", p+"?v="+revision+"&v="+revision, true, nil)
		add("HEAD", p, true, map[string]string{"Accept-Encoding": "gzip"})
		if first.status == 200 {
			add("GET", p, true, map[string]string{"If-None-Match": etag})
			add("GET", p, true, map[string]string{"If-None-Match": etag, "Accept-Encoding": "gzip"})
			add("GET", p, true, map[string]string{"If-None-Match": "W/" + etag, "Accept-Encoding": "gzip"})
			add("GET", p, true, map[string]string{"If-None-Match": `"nope", ` + etag})
			add("GET", p, true, map[string]string{"If-None-Match": `"nope"`})
		}
		t2 := "/api/i18n/translations/" + code
		add("GET", t2, false, nil)
		add("GET", t2, false, map[string]string{"Accept-Encoding": "gzip"})
	}
	add("GET", "/api/i18n/languages", false, nil)
	add("GET", "/api/i18n/languages", false, map[string]string{"Accept-Encoding": "gzip"})
	add("HEAD", "/api/i18n/languages", false, nil)

	compared, bodies, brotli, keptType := 0, 0, 0, 0
	var differences []string
	for _, rq := range requests {
		want := do(t, node, rq.method, rq.path, rq.headers, nil, 0)
		got := do(t, srv.URL, rq.method, rq.path, rq.headers, nil, 0)
		compared++
		label := rq.method + " " + rq.path
		for k, v := range rq.headers {
			label += " [" + k + ": " + v + "]"
		}
		differ := func(what, g, w string) {
			differences = append(differences, label+": "+what+" Go "+short(g)+", Node "+short(w))
		}
		if got.status != want.status {
			differ("status", numberString(float64(got.status)), numberString(float64(want.status)))
			continue
		}
		names := []string{"ETag", "Vary", "Cache-Control"}
		if got.status != 304 {
			// Go's HTTP server drops Content-Type from every 304. Node kept
			// it when the If-None-Match header was exactly the tag.
			names = append(names, "Content-Type")
		} else if want.header.Get("Content-Type") != "" {
			keptType++
		}
		for _, name := range names {
			if g, w := got.header.Get(name), want.header.Get(name); g != w {
				differ(name, g, w)
			}
		}
		nodeEncoding, goEncoding := want.header.Get("Content-Encoding"), got.header.Get("Content-Encoding")
		if nodeEncoding == "br" {
			brotli++
			offersGzip := strings.Contains(rq.headers["Accept-Encoding"], "gzip")
			if (offersGzip && goEncoding != "gzip") || (!offersGzip && goEncoding != "") {
				differ("Content-Encoding", goEncoding, nodeEncoding)
			}
			continue
		}
		if goEncoding != nodeEncoding {
			differ("Content-Encoding", goEncoding, nodeEncoding)
		}
		bodies++
		if got.body != want.body {
			differ("body", firstDifference(got.body, want.body), "")
		}
	}
	t.Logf("%d requests compared, %d bodies byte for byte, %d answers where Node sent brotli (headers only), %d answers 304 where Node kept Content-Type", compared, bodies, brotli, keptType)
	for _, d := range differences {
		t.Error(d)
	}
}
