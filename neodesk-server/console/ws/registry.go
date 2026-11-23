package ws

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
)

// Handler takes an upgrade to a registered path. It runs the checks of
// its socket, refuses with Refuse or answers the handshake with Accept,
// and returns when the socket has ended.
type Handler func(u *Upgrade)

// Options are what the checks of a socket use.
type Options struct {
	// Session reads the session an upgrade's cookie names, without
	// renewing it (session.Manager.Load). nil: nobody is signed in.
	Session func(r *http.Request) Session
	// Permission is roleHasEffectivePermission: a role's built-in
	// permissions with the stored overrides (access.Service). err is a
	// failed lookup. nil: no role has any permission.
	Permission func(ctx context.Context, role, permission string) (bool, error)
	// Origins is the origin policy of CheckOrigin.
	Origins OriginPolicy
	// ClientIP is the client's address as the console's TRUST_PROXY
	// setting gives it (httpx.TrustProxy.ClientIP, req.ip). nil: the
	// connection's address.
	ClientIP func(r *http.Request) string
	// SectionAccess is the panel access guard of the routes for sockets:
	// whether the account's effective panel access policy reaches level in
	// section (see SectionAccessOf). err is a failed lookup. nil: no
	// account reaches any level, so a socket that asks refuses everyone.
	SectionAccess func(ctx context.Context, user access.PolicyUser, section string, level access.Level) (bool, error)
}

// Session is what a socket's checks read from the session.
type Session interface {
	// SignedIn reports a truthy req.session.userId.
	SignedIn() bool
	// UserID is req.session.userId as a number, 0 when it is none.
	UserID() int64
	// UserRole is req.session.user.role when it is text, else "".
	UserRole() string
	// Role is session.user.role, else session.role.
	Role() string
	// Username is session.user.username, else session.username.
	Username() string
	// User is the account as the session store reads req.session.user
	// (session.Session.User), for the device scope; nil when nobody is
	// signed in.
	User() *session.User
}

// SessionOf adapts the session store: it loads the session of r as the
// session middleware did on an upgrade. A session that cannot be read
// counts as none.
func SessionOf(m *session.Manager) func(r *http.Request) Session {
	return func(r *http.Request) Session {
		s, err := m.Load(r)
		if err != nil || s == nil {
			return storedSession{}
		}
		return storedSession{s: s}
	}
}

// PermissionOf adapts the permission service.
func PermissionOf(a *access.Service) func(ctx context.Context, role, permission string) (bool, error) {
	return a.RoleHasEffectivePermission
}

// SectionAccessOf adapts the permission service: it resolves the
// account's policy as the guard does for a request (ResolveForUser) and
// compares its level in the section (AccessAllows).
func SectionAccessOf(a *access.Service) func(ctx context.Context, user access.PolicyUser, section string, level access.Level) (bool, error) {
	return func(ctx context.Context, user access.PolicyUser, section string, level access.Level) (bool, error) {
		policy, err := a.ResolveForUser(ctx, user)
		if err != nil {
			return false, fmt.Errorf("resolve panel access policy: %w", err)
		}
		return access.AccessAllows(policy, section, level), nil
	}
}

// Registry holds the console's sockets: one handler per path. The front
// door asks it whether an upgrade is for a socket the Go console has
// (Serves) and hands it the request (ServeHTTP); every other upgrade
// goes on as before.
type Registry struct {
	opts   Options
	routes []route
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	stopped bool
	running sync.WaitGroup
}

// New returns a registry without sockets.
func New(opts Options) *Registry {
	ctx, cancel := context.WithCancel(context.Background())
	return &Registry{opts: opts, ctx: ctx, cancel: cancel}
}

// route is one registered path: its segments, each a literal or a
// parameter with an optional rule.
type route struct {
	pattern  string
	segments []segment
	handler  Handler
}

type segment struct {
	literal string
	param   string
	rule    *regexp.Regexp
}

// Handle registers a socket. pattern is a path of literal segments with
// at most one parameter segment, written {name} (any text without a
// slash) or {name:rule} (text the regular expression matches whole):
//
//	reg.Handle("/ws/device-status", h)
//	reg.Handle("/ws/chat/{id}", h)
//	reg.Handle("/api/cdap/devices/{id:[A-Za-z0-9_-]{1,64}}/terminal", h)
//
// A path matches as the old console's upgrade router matched it: the
// pathname of new URL(req.url), exactly, in its letter case, with no
// slash added or dropped. The first registered path that matches wins.
// Handle panics on a pattern it cannot read.
func (g *Registry) Handle(pattern string, h Handler) {
	if !strings.HasPrefix(pattern, "/") || h == nil {
		panic(fmt.Sprintf("ws: Handle(%q): a pattern starts with / and needs a handler", pattern))
	}
	r := route{pattern: pattern, handler: h}
	params := 0
	for _, part := range strings.Split(pattern[1:], "/") {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			params++
			name, rule, hasRule := strings.Cut(part[1:len(part)-1], ":")
			s := segment{param: name}
			if name == "" {
				panic(fmt.Sprintf("ws: Handle(%q): a parameter needs a name", pattern))
			}
			if hasRule {
				s.rule = regexp.MustCompile("^(?:" + rule + ")$")
			}
			r.segments = append(r.segments, s)
			continue
		}
		if strings.ContainsAny(part, "{}") {
			panic(fmt.Sprintf("ws: Handle(%q): a parameter is a whole segment", pattern))
		}
		r.segments = append(r.segments, segment{literal: part})
	}
	if params > 1 {
		panic(fmt.Sprintf("ws: Handle(%q): one parameter at most", pattern))
	}
	g.routes = append(g.routes, r)
}

// Patterns lists the registered paths in order.
func (g *Registry) Patterns() []string {
	out := make([]string, len(g.routes))
	for i, r := range g.routes {
		out[i] = r.pattern
	}
	return out
}

// match finds the route for a pathname and its parameter.
func (g *Registry) match(path string) (*route, map[string]string) {
	if !strings.HasPrefix(path, "/") {
		return nil, nil
	}
	parts := strings.Split(path[1:], "/")
	for i := range g.routes {
		r := &g.routes[i]
		if len(parts) != len(r.segments) {
			continue
		}
		var params map[string]string
		ok := true
		for j, s := range r.segments {
			switch {
			case s.param == "":
				ok = parts[j] == s.literal
			case parts[j] == "":
				ok = false
			case s.rule != nil:
				ok = s.rule.MatchString(parts[j])
			}
			if !ok {
				break
			}
			if s.param != "" {
				params = map[string]string{s.param: parts[j]}
			}
		}
		if ok {
			return r, params
		}
	}
	return nil, nil
}

// Serves reports whether r is an upgrade for a socket of the registry.
func (g *Registry) Serves(r *http.Request) bool {
	if g == nil || !IsUpgrade(r) {
		return false
	}
	t, ok := parseTarget(requestTarget(r))
	if !ok {
		return false
	}
	route, _ := g.match(t.Path)
	return route != nil
}

// ServeHTTP runs the handler of the socket an upgrade is for. Behind the
// front door it only gets upgrades Serves said yes to; anything else is
// refused with 400.
func (g *Registry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t, ok := parseTarget(requestTarget(r))
	if !ok {
		writeRefusal(w, r, statusLine(http.StatusBadRequest))
		return
	}
	route, params := g.match(t.Path)
	if route == nil {
		writeRefusal(w, r, statusLine(http.StatusBadRequest))
		return
	}
	g.mu.Lock()
	if g.stopped {
		g.mu.Unlock()
		writeRefusal(w, r, statusLine(http.StatusServiceUnavailable))
		return
	}
	g.running.Add(1)
	g.mu.Unlock()
	defer g.running.Done()
	route.handler(&Upgrade{W: w, R: r, Path: t.Path, query: t.Query, params: params, reg: g})
}

// Close ends every socket without a closing handshake and waits until
// every handler has returned. Later upgrades are refused with 503.
func (g *Registry) Close() {
	g.mu.Lock()
	g.stopped = true
	g.mu.Unlock()
	g.cancel()
	g.running.Wait()
}

// IsUpgrade reports a request that asks to switch protocols: an Upgrade
// header and "upgrade" among the tokens of Connection, which is when
// Node's server handed a request to its upgrade listener.
func IsUpgrade(r *http.Request) bool {
	if r.Header.Get("Upgrade") == "" {
		return false
	}
	for _, value := range r.Header.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
				return true
			}
		}
	}
	return false
}

// requestTarget is the request target as the client sent it.
func requestTarget(r *http.Request) string {
	if r.RequestURI != "" {
		return r.RequestURI
	}
	return r.URL.RequestURI()
}
