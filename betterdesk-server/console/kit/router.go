// Package kit is what every console route group builds on: the route
// table, the middleware that guards routes, and the request helpers.
package kit

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// Route is one entry of the route table.
type Route struct {
	// Method is the HTTP method, or "" for every method.
	Method string
	// Path is the pattern in http.ServeMux syntax: /api/devices/{id},
	// /files/{rest...}.
	Path string

	segments []string
	wildcard bool
	literals int
}

// Router is the console's route table. It matches the way the console's
// addresses have always matched: static parts of a path in any letter case,
// and with or without one trailing slash. A path that matches no route is
// reported by Lookup, so the caller decides what answers it.
type Router struct {
	mux    *http.ServeMux
	routes []*Route
}

// NewRouter returns an empty route table.
func NewRouter() *Router {
	return &Router{mux: http.NewServeMux()}
}

// Handle adds a route. The middleware runs in the order given, before h.
// method is "" for every method. It panics on a pattern the mux refuses,
// such as a duplicate, like http.ServeMux does.
func (rt *Router) Handle(method, pattern string, h http.Handler, mw ...Middleware) {
	if !strings.HasPrefix(pattern, "/") {
		panic(fmt.Sprintf("kit: route %q must start with /", pattern))
	}
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	route := &Route{Method: method, Path: pattern}
	if pattern != "/" {
		route.segments = strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	}
	for _, seg := range route.segments {
		switch {
		case strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "...}"):
			route.wildcard = true
		case !isParam(seg):
			route.literals++
		}
	}
	muxPattern := pattern
	if pattern == "/" {
		// "/" alone would match every path
		muxPattern = "/{$}"
	}
	if method != "" {
		muxPattern = method + " " + muxPattern
	}
	rt.mux.Handle(muxPattern, h)
	rt.routes = append(rt.routes, route)
}

// Get adds a GET route (HEAD requests reach it too).
func (rt *Router) Get(pattern string, h http.HandlerFunc, mw ...Middleware) {
	rt.Handle(http.MethodGet, pattern, h, mw...)
}

// Post adds a POST route.
func (rt *Router) Post(pattern string, h http.HandlerFunc, mw ...Middleware) {
	rt.Handle(http.MethodPost, pattern, h, mw...)
}

// Put adds a PUT route.
func (rt *Router) Put(pattern string, h http.HandlerFunc, mw ...Middleware) {
	rt.Handle(http.MethodPut, pattern, h, mw...)
}

// Patch adds a PATCH route.
func (rt *Router) Patch(pattern string, h http.HandlerFunc, mw ...Middleware) {
	rt.Handle(http.MethodPatch, pattern, h, mw...)
}

// Delete adds a DELETE route.
func (rt *Router) Delete(pattern string, h http.HandlerFunc, mw ...Middleware) {
	rt.Handle(http.MethodDelete, pattern, h, mw...)
}

// Routes lists what was registered, in order.
func (rt *Router) Routes() []Route {
	out := make([]Route, len(rt.routes))
	for i, r := range rt.routes {
		out[i] = Route{Method: r.Method, Path: r.Path}
	}
	return out
}

// Lookup finds the route for a request. ok is false when no route has this
// method and path; then nothing was written and the caller answers.
func (rt *Router) Lookup(r *http.Request) (h http.Handler, ok bool) {
	escaped := r.URL.EscapedPath()
	trailing := len(escaped) > 1 && strings.HasSuffix(escaped, "/")
	if trailing {
		escaped = escaped[:len(escaped)-1]
	}
	if escaped == "" || path.Clean(escaped) != escaped {
		return nil, false
	}
	var parts []string
	if escaped != "/" {
		parts = strings.Split(strings.TrimPrefix(escaped, "/"), "/")
	}
	route := rt.best(r.Method, parts, trailing)
	if route == nil {
		return nil, false
	}
	canonical := route.canonical(parts)
	if canonical == r.URL.EscapedPath() {
		return rt.mux, true
	}
	unescaped, err := url.PathUnescape(canonical)
	if err != nil {
		return nil, false
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		clone := req.Clone(req.Context())
		clone.URL.Path = unescaped
		clone.URL.RawPath = canonical
		rt.mux.ServeHTTP(w, clone)
	}), true
}

// best picks the matching route with the most static parts, the way the mux
// prefers the most specific pattern.
func (rt *Router) best(method string, parts []string, trailing bool) *Route {
	var found *Route
	for _, route := range rt.routes {
		if !route.allows(method) || !route.matches(parts, trailing) {
			continue
		}
		if found == nil || route.literals > found.literals || (route.literals == found.literals && found.wildcard && !route.wildcard) {
			found = route
		}
	}
	return found
}

func (route *Route) allows(method string) bool {
	return route.Method == "" || route.Method == method ||
		(route.Method == http.MethodGet && method == http.MethodHead)
}

// matches reports whether the path parts fit the route. trailing says the
// path ended with a slash: "/files/" fits "/files/{rest...}" with an empty
// rest, "/files" does not.
func (route *Route) matches(parts []string, trailing bool) bool {
	if route.wildcard {
		fixed := len(route.segments) - 1
		if len(parts) < fixed || (len(parts) == fixed && !trailing) {
			return false
		}
		parts = parts[:fixed]
	} else if len(parts) != len(route.segments) {
		return false
	}
	for i, part := range parts {
		seg := route.segments[i]
		if isParam(seg) {
			if part == "" {
				return false
			}
			continue
		}
		if !strings.EqualFold(seg, part) {
			return false
		}
	}
	return true
}

// canonical is the request path as the mux expects it: static parts spelled
// as registered, parameters as sent.
func (route *Route) canonical(parts []string) string {
	if len(route.segments) == 0 {
		return "/"
	}
	var b strings.Builder
	for i, part := range parts {
		b.WriteByte('/')
		if i < len(route.segments) && !isParam(route.segments[i]) {
			b.WriteString(route.segments[i])
		} else {
			b.WriteString(part)
		}
	}
	if route.wildcard && len(parts) == len(route.segments)-1 {
		// "/files" for "/files/{rest...}": the mux wants the slash
		b.WriteByte('/')
	}
	return b.String()
}

func isParam(seg string) bool {
	return strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}")
}
