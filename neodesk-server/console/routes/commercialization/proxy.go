package commercialization

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/routes/internal/reqquery"
)

// The routes that pass a call on to the API server. A path parameter
// goes into the API path only as goapipath.SafeSegment takes it, named
// after the parameter (packageId, contractId, sessionId, currencyCode)
// in the 400 answer of a value it refuses.

// pass is a route that calls the API path built from pattern, without a
// body.
func (h *handlers) pass(method, pattern string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		goapipath.ProxyToGo(h.api, w, r, method, goapipath.PathOf(r, pattern))
	}
}

// passBody is pass with the request's body sent along, {} without one.
func (h *handlers) passBody(method, pattern string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		goapipath.ProxyToGo(h.api, w, r, method, goapipath.PathOf(r, pattern), goapipath.Body(goapipath.RequestBody(r)))
	}
}

// withQuery is a GET route that passes the query on (see search).
func (h *handlers) withQuery(pattern string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		goapipath.ProxyToGo(h.api, w, r, http.MethodGet, withSearch(goapipath.PathOf(r, pattern), search(r)))
	}
}

// export is withQuery for a file: the API server's answer goes back as
// it is, with its type and file name.
func (h *handlers) export(pattern string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		goapipath.ProxyBinaryToGo(h.api, w, r, http.MethodGet, withSearch(goapipath.PathOf(r, pattern), search(r)))
	}
}

// queryParts is how many parts of a query the query parser reads.
const queryParts = 1000

// search is the request's query as it goes to the API server, with a "?"
// in front when it is not empty: the parameters written without brackets,
// written out again as URLSearchParams writes req.query (every parameter
// once, a repeated one as its values joined by commas). A parameter
// written with brackets (a[]=1, a[x]=1) would be a list or an object,
// which reaches the API server as "[object Object]" or cannot be written
// at all; it is left out.
func search(r *http.Request) string {
	raw, _, _ := strings.Cut(r.URL.RawQuery, "#")
	parts := strings.Split(raw, "&")
	if len(parts) > queryParts {
		parts = parts[:queryParts]
	}
	plain := make([]string, 0, len(parts))
	for _, part := range parts {
		if !bracketed(part) {
			plain = append(plain, part)
		}
	}
	text, _ := reqquery.Of(&http.Request{URL: &url.URL{RawQuery: strings.Join(plain, "&")}}).Search()
	if text == "" {
		return ""
	}
	return "?" + text
}

// brackets are the escapes the query parser reads as brackets.
var brackets = strings.NewReplacer("%5B", "[", "%5b", "[", "%5D", "]", "%5d", "]")

// bracketed reports a part of a query whose name has a bracket, the name
// cut as the query parser cuts it: up to "]=" when the part has one, else
// up to the first "=".
func bracketed(part string) bool {
	clean := brackets.Replace(part)
	name := clean
	if end := strings.Index(clean, "]="); end >= 0 {
		name = clean[:end+1]
	} else if end := strings.IndexByte(clean, '='); end >= 0 {
		name = clean[:end]
	}
	return strings.Contains(name, "[")
}

// withSearch adds the written query to a path.
func withSearch(path goapipath.Path, query string) goapipath.Path {
	return func() (string, error) {
		p, err := path()
		if err != nil {
			return "", err
		}
		return p + query, nil
	}
}
