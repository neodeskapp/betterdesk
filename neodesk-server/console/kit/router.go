package kit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
)

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// Route is one entry of the route table.
type Route struct {
	// Method is the HTTP method, or "" for every method.
	Method string
	// Path is the whole pattern, mount included: /api/devices/{id},
	// /files/{rest...}.
	Path string
	// Mount is the prefix the route's group was mounted at, "" for the
	// root.
	Mount string
	// Section is what the route declares about its panel section
	// (Router.Section, NoSection), nil for a route that leaves it to the
	// panel access guard's path rules.
	Section *access.RouteSection
}

type route struct {
	Route
	segments []string
	wildcard bool
	// prefix marks a middleware added with Use: it fits every path that
	// starts with its segments.
	prefix  bool
	handler http.Handler
}

// table is the list of routes a Router and its mounts share.
type table struct {
	routes   []*route
	badParam func(w http.ResponseWriter, r *http.Request, err error)
}

// Router is a route table of the console. It matches the way the
// console's addresses have always matched:
//
//   - Routes are tried in the order they were added, and the first one
//     whose method and path fit answers. A more specific pattern added
//     later does not win.
//   - Static parts of a path match in any letter case, and a path matches
//     with or without one slash at its end.
//   - A {name} part matches one path segment that is not empty and is
//     handed on decoded (r.PathValue). {name...} at the end matches the
//     rest of the path, which may be empty.
//   - A GET route also answers HEAD.
//   - An OPTIONS request to a path that only has routes for other methods
//     is answered with the list of those methods.
//   - A middleware added with Use runs at its place in the order for every
//     method and every path below its pattern.
//
// A handler, or a middleware in front of it, can decline a request with
// PassOn. The router then tries the next route that fits, and reports the
// request as unanswered when there is none.
type Router struct {
	table  *table
	prefix string
	// section is what the routes added through this router declare.
	section *access.RouteSection
}

// NewRouter returns an empty route table.
func NewRouter() *Router {
	return &Router{table: &table{}}
}

// OnBadParam sets what answers a request whose path parameter does not
// decode to text (percent-encoded bytes that are not UTF-8). Without it
// the answer is a plain 400.
func (rt *Router) OnBadParam(fn func(w http.ResponseWriter, r *http.Request, err error)) {
	rt.table.badParam = fn
}

// Mount returns a router that adds its routes to this table below prefix,
// the way app.use(prefix, router) did: Mount("/api/nd").Get("/commands")
// answers /api/nd/commands, and a route "/" answers the prefix itself.
// Handlers read the prefix with MountPath and the rest with RoutePath.
func (rt *Router) Mount(prefix string) *Router {
	if !strings.HasPrefix(prefix, "/") || strings.HasSuffix(prefix, "/") {
		panic(fmt.Sprintf("kit: mount %q must start with / and not end with one", prefix))
	}
	return &Router{table: rt.table, prefix: rt.prefix + prefix, section: rt.section}
}

// Section returns a router whose routes declare key as their panel
// section, for a route the panel access guard's path rules cannot name:
//
//	r.Section("toolkit").Post("/targets/{id}/check", h.check, ...)
//
// The guard then asks the account's level in that section, whatever the
// rules say about the path: write for a method that changes something,
// read for GET and HEAD. A route that can pass a request on (PassOn)
// declares what the route behind it declares, since the guard asks the
// first route that fits.
func (rt *Router) Section(key string) *Router {
	if key == "" {
		panic("kit: Section needs a section; NoSection declares that a route has none")
	}
	return rt.declare(access.RouteSection{Section: key})
}

// SectionLevel is Section for a route that needs another level than its
// method's: a POST that only reads, or a GET that needs write.
func (rt *Router) SectionLevel(key string, level access.Level) *Router {
	if key == "" || level == "" {
		panic(fmt.Sprintf("kit: SectionLevel needs a section and a level, got %q and %q", key, level))
	}
	return rt.declare(access.RouteSection{Section: key, Level: level})
}

// NoSection returns a router whose routes declare that they belong to no
// panel section, for a route every account may use below a prefix the
// guard's rules give to a section:
//
//	r.NoSection().Get("/api/users/{id}/avatar", h.picture, ...)
func (rt *Router) NoSection() *Router {
	return rt.declare(access.RouteSection{})
}

func (rt *Router) declare(section access.RouteSection) *Router {
	if rt.section != nil {
		panic(fmt.Sprintf("kit: the router declares %+v already", *rt.section))
	}
	if err := section.Valid(); err != nil {
		panic(fmt.Sprintf("kit: %v", err))
	}
	return &Router{table: rt.table, prefix: rt.prefix, section: &section}
}

// Prefix is the mount the router adds its routes below, "" for the table
// itself.
func (rt *Router) Prefix() string { return rt.prefix }

// Handle adds a route. The middleware runs in the order given, before h.
// method is "" for every method.
func (rt *Router) Handle(method, pattern string, h http.Handler, mw ...Middleware) {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	rt.add(method, pattern, h, false)
}

// Use adds mw at this place of the table, for every method and every path
// that starts with pattern, the way router.use(path, fn) did:
// Use("/api/devices/{id}", mw) runs for /api/devices/A1001 and for
// /api/devices/A1001/groups, in front of the routes added after it, also
// when none of them answers the path. mw lets the request go on to those
// routes by calling the handler it wraps, or answers it. A Use entry is no
// route: Match and Routes leave it out, so a path that only it fits is
// still one the table does not have.
func (rt *Router) Use(pattern string, mw Middleware) {
	if strings.HasSuffix(pattern, "...}") {
		panic(fmt.Sprintf("kit: Use %q covers the rest of the path already; leave out {name...}", pattern))
	}
	if rt.section != nil {
		panic(fmt.Sprintf("kit: Use %q is no route and has no panel section", pattern))
	}
	rt.add("", pattern, mw(http.HandlerFunc(PassOn)), true)
}

func (rt *Router) add(method, pattern string, h http.Handler, prefix bool) {
	if !strings.HasPrefix(pattern, "/") {
		panic(fmt.Sprintf("kit: route %q must start with /", pattern))
	}
	full := rt.prefix + pattern
	if pattern == "/" && rt.prefix != "" {
		full = rt.prefix
	}
	entry := &route{Route: Route{Method: method, Path: full, Mount: rt.prefix, Section: rt.section}, handler: h, prefix: prefix}
	if full != "/" {
		entry.segments = strings.Split(strings.TrimPrefix(full, "/"), "/")
	}
	for i, seg := range entry.segments {
		rest := strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "...}")
		if rest && i != len(entry.segments)-1 {
			panic(fmt.Sprintf("kit: route %q has {name...} before its end", full))
		}
		entry.wildcard = entry.wildcard || rest
	}
	rt.table.routes = append(rt.table.routes, entry)
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

// All adds a route for every method, like router.all.
func (rt *Router) All(pattern string, h http.HandlerFunc, mw ...Middleware) {
	rt.Handle("", pattern, h, mw...)
}

// Routes lists the routes of the table, in order, without what Use added.
func (rt *Router) Routes() []Route {
	out := make([]Route, 0, len(rt.table.routes))
	for _, r := range rt.table.routes {
		if !r.prefix {
			out = append(out, r.Route)
		}
	}
	return out
}

// Match reports whether the table has a route for the request's method
// and path. An OPTIONS request matches every path that has a route.
func (rt *Router) Match(r *http.Request) bool {
	parts, trailing, ok := splitPath(r)
	if !ok {
		return false
	}
	for _, entry := range rt.table.routes {
		if !entry.prefix && entry.matches(parts, trailing) && (entry.allows(r.Method) || r.Method == http.MethodOptions) {
			return true
		}
	}
	return false
}

// Find returns the first route of the table that fits the request's
// method and path: the route Serve tries first. It runs nothing. A route
// for digits only (Digits) does not fit a parameter written otherwise,
// as in Serve; any other reason a route has to pass the request on shows
// only when it runs.
func (rt *Router) Find(r *http.Request) (Route, bool) {
	parts, trailing, ok := splitPath(r)
	if !ok {
		return Route{}, false
	}
	for _, entry := range rt.table.routes {
		if entry.prefix || !entry.matches(parts, trailing) || !entry.allows(r.Method) || !entry.digitsFit(parts) {
			continue
		}
		return entry.Route, true
	}
	return Route{}, false
}

// Serve answers the request from the table and reports whether it did. On
// false nothing was written: no route fits, or every route that fits
// passed the request on.
func (rt *Router) Serve(w http.ResponseWriter, r *http.Request) bool {
	parts, trailing, ok := splitPath(r)
	if !ok {
		return false
	}
	var allow []string
	for _, entry := range rt.table.routes {
		if !entry.matches(parts, trailing) {
			continue
		}
		if !entry.allows(r.Method) {
			if r.Method == http.MethodOptions {
				allow = entry.addMethods(allow)
			}
			continue
		}
		values, written, err := entry.values(parts)
		var bad *ParamError
		if errors.As(err, &bad) && entry.digitsGuard(bad.Name) {
			// The pattern of digits does not fit a parameter that is not even text
			continue
		}
		if err != nil {
			rt.refuseParam(w, r, err)
			return true
		}
		state := &routeState{mount: entry.Mount, written: written}
		attempt := r.WithContext(context.WithValue(r.Context(), routeKey{}, state))
		for name, value := range values {
			attempt.SetPathValue(name, value)
		}
		entry.handler.ServeHTTP(w, attempt)
		if !state.passed {
			return true
		}
	}
	if len(allow) > 0 {
		list := strings.Join(allow, ",")
		w.Header().Set("Allow", list)
		httpx.WriteBody(w, r, http.StatusOK, "text/html; charset=utf-8", []byte(list))
		return true
	}
	return false
}

func (rt *Router) refuseParam(w http.ResponseWriter, r *http.Request, err error) {
	if rt.table.badParam != nil {
		rt.table.badParam(w, r, err)
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}

type routeKey struct{}

// routeState is what the router keeps about one try of a route.
type routeState struct {
	mount  string
	passed bool
	// written are the path parameters as the client wrote them, escapes
	// and all.
	written map[string]string
}

// PassOn declines the request: the router goes on to the next route that
// fits it, as next('route') and a router's next() did. Call it before
// anything is written, and return right after. Outside a route it does
// nothing.
//
// In the early stage this is how a client API route leaves a request
// without a bearer token to the console route with the same address. In
// the normal stage it is how a route steps aside for a later one.
func PassOn(_ http.ResponseWriter, r *http.Request) {
	if state, ok := r.Context().Value(routeKey{}).(*routeState); ok {
		state.passed = true
	}
}

// MountPath is the prefix the route answering r was mounted at
// (req.baseUrl in the old console), "" for a route at the root.
func MountPath(r *http.Request) string {
	if state, ok := r.Context().Value(routeKey{}).(*routeState); ok {
		return state.mount
	}
	return ""
}

// RoutePath is the request path below the mount, as the client wrote it
// (req.path inside a mounted router in the old console). It always starts
// with a slash.
func RoutePath(r *http.Request) string {
	raw := httpx.RawPath(r)
	mount := MountPath(r)
	if mount == "" {
		return raw
	}
	if rest, _, ok := httpx.StripMount(raw, mount); ok {
		return rest
	}
	return raw
}

// OnMount lets a route answer only when its group is mounted at prefix
// and passes the request on otherwise. A group that is mounted twice uses
// it for the routes that belong to one of the two addresses.
func OnMount(prefix string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if MountPath(r) != prefix {
				PassOn(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Digits lets a route answer only when the path parameter name is made of
// digits as the client wrote it, and passes the request on otherwise: the
// pattern :id(\d+), which the old console matched before it decoded the
// address, so an escaped digit (%31 for 1) is no digit. Given first among
// a route's middleware it stands for the pattern: a parameter that does
// not decode to text then passes the request on too, where the route
// table refuses it with 400 for every other route.
func Digits(name string) Middleware {
	return func(next http.Handler) http.Handler {
		return &digitsGuard{name: name, next: next}
	}
}

// digitsGuard is the handler of Digits.
type digitsGuard struct {
	name string
	next http.Handler
}

func (g *digitsGuard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	value := r.PathValue(g.name)
	if state, ok := r.Context().Value(routeKey{}).(*routeState); ok {
		value = state.written[g.name]
	}
	if !allDigits(value) {
		PassOn(w, r)
		return
	}
	g.next.ServeHTTP(w, r)
}

// digitsGuard reports whether the route's middleware starts with Digits
// for the parameter name.
func (entry *route) digitsGuard(name string) bool {
	for h := entry.handler; ; {
		g, ok := h.(*digitsGuard)
		if !ok {
			return false
		}
		if g.name == name {
			return true
		}
		h = g.next
	}
}

// digitsFit reports whether every parameter the route's middleware
// starts with Digits for is made of digits in the path parts.
func (entry *route) digitsFit(parts []string) bool {
	for h := entry.handler; ; {
		g, ok := h.(*digitsGuard)
		if !ok {
			return true
		}
		for i, seg := range entry.segments {
			if seg == "{"+g.name+"}" && i < len(parts) && !allDigits(parts[i]) {
				return false
			}
		}
		h = g.next
	}
}

// allDigits reports whether value is one or more ASCII digits.
func allDigits(value string) bool {
	ok := value != ""
	for i := 0; i < len(value) && ok; i++ {
		ok = value[i] >= '0' && value[i] <= '9'
	}
	return ok
}

// splitPath cuts the request path into its parts. It reads the path as the
// client sent it (httpx.RawPath), the text every step in front of the
// router reads: Go's EscapedPath is written anew from the decoded path as
// soon as the path holds a byte Go would have escaped, which would make an
// escaped fixed segment match. ok is false for a path no route can match:
// one with an empty part or a dot segment.
func splitPath(r *http.Request) (parts []string, trailing, ok bool) {
	escaped := httpx.RawPath(r)
	trailing = len(escaped) > 1 && strings.HasSuffix(escaped, "/")
	if trailing {
		escaped = escaped[:len(escaped)-1]
	}
	if escaped == "" || path.Clean(escaped) != escaped {
		return nil, false, false
	}
	if escaped != "/" {
		parts = strings.Split(strings.TrimPrefix(escaped, "/"), "/")
	}
	return parts, trailing, true
}

func (entry *route) allows(method string) bool {
	return entry.Method == "" || entry.Method == method ||
		(entry.Method == http.MethodGet && method == http.MethodHead)
}

// addMethods adds the route's methods to the list an OPTIONS request is
// answered with.
func (entry *route) addMethods(list []string) []string {
	add := func(method string) {
		for _, have := range list {
			if have == method {
				return
			}
		}
		list = append(list, method)
	}
	add(entry.Method)
	if entry.Method == http.MethodGet {
		add(http.MethodHead)
	}
	return list
}

// matches reports whether the path parts fit the route. trailing says the
// path ended with a slash: "/files/" fits "/files/{rest...}" with an empty
// rest, "/files" does not.
func (entry *route) matches(parts []string, trailing bool) bool {
	switch {
	case entry.prefix:
		if len(parts) < len(entry.segments) {
			return false
		}
		parts = parts[:len(entry.segments)]
	case entry.wildcard:
		fixed := len(entry.segments) - 1
		if len(parts) < fixed || (len(parts) == fixed && !trailing) {
			return false
		}
		parts = parts[:fixed]
	case len(parts) != len(entry.segments):
		return false
	}
	for i, part := range parts {
		seg := entry.segments[i]
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

// values decodes the path parameters of a path that fits the route, and
// returns them also as the client wrote them.
func (entry *route) values(parts []string) (out, written map[string]string, err error) {
	set := func(name, raw string) error {
		if written == nil {
			written = map[string]string{}
		}
		written[name] = raw
		value, err := url.PathUnescape(raw)
		if err != nil || !utf8.ValidString(value) {
			return &ParamError{Name: name, Raw: raw}
		}
		if out == nil {
			out = map[string]string{}
		}
		out[name] = value
		return nil
	}
	for i, seg := range entry.segments {
		if !isParam(seg) {
			continue
		}
		if strings.HasSuffix(seg, "...}") {
			rest := ""
			if i < len(parts) {
				rest = strings.Join(parts[i:], "/")
			}
			if err := set(seg[1:len(seg)-4], rest); err != nil {
				return nil, nil, err
			}
			continue
		}
		if err := set(seg[1:len(seg)-1], parts[i]); err != nil {
			return nil, nil, err
		}
	}
	return out, written, nil
}

// ParamError is a path parameter that does not decode to text. The old
// console answered it with status 400 through its error handler.
type ParamError struct {
	// Name is the name of the parameter in the route.
	Name string
	// Raw is the parameter as the client sent it.
	Raw string
}

func (e *ParamError) Error() string { return "Failed to decode param '" + e.Raw + "'" }

func isParam(seg string) bool {
	return strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}")
}
