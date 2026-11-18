package commercialization

import (
	"net/http"

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

// withQuery is a GET route that passes the whole query on, written out
// again as URLSearchParams writes an object: every parameter once, a
// list as its values joined by commas. The query is written before the
// path is built, so a query that cannot be written (a value with a
// member named toString) is a server error, also where the path would be
// refused.
func (h *handlers) withQuery(pattern string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query, ok := search(r)
		if !ok {
			h.web.ServerError(w, r, goapipath.ErrNotPrimitive, 0)
			return
		}
		goapipath.ProxyToGo(h.api, w, r, http.MethodGet, withSearch(goapipath.PathOf(r, pattern), query))
	}
}

// export is withQuery for a file: the API server's answer goes back as
// it is, with its type and file name.
func (h *handlers) export(pattern string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query, ok := search(r)
		if !ok {
			h.web.ServerError(w, r, goapipath.ErrNotPrimitive, 0)
			return
		}
		goapipath.ProxyBinaryToGo(h.api, w, r, http.MethodGet, withSearch(goapipath.PathOf(r, pattern), query))
	}
}

// search is the request's query written out again, with a "?" in front
// when it is not empty. ok is false where JavaScript throws.
func search(r *http.Request) (string, bool) {
	text, ok := reqquery.Of(r).Search()
	if !ok || text == "" {
		return "", ok
	}
	return "?" + text, true
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
