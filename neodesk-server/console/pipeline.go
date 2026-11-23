package console

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session/cookie"
)

// step is one step of the request pipeline.
type step struct {
	// n is the step's number in the Node console's pipeline (server.js).
	n    int
	name string
	// use wraps what follows the step.
	use kit.Middleware
}

// steps lists the pipeline in the order a request passes it. The numbers
// are those of the Node console's pipeline; what is missing here has no
// step of its own in Go:
//
//	 2  error pages   every step answers errors through webui directly
//	 5  cookies       each package reads the cookies it needs
//	 7  idle check    part of the session step
//	13  device API    shares the early stage with the client API
//	20  CSRF, 404     the CSRF error is answered by step 16, the 404 by end
func (s *Server) steps() []step {
	d := s.deps
	cfg := s.cfg
	onError := func(w http.ResponseWriter, r *http.Request, status int, err error) {
		d.WebUI.ServerError(w, r, err, status)
	}
	return []step{
		{1, "security headers", s.securityHeaders()},
		{3, "CORS", middleware.CORS()},
		{4, "bodies", middleware.Bodies(middleware.BodyOptions{RouteBodies: true, OnError: onError})},
		{6, "session", d.Sessions.Middleware},
		{8, "CSRF token", d.CSRF.Provider},
		{9, "static folders", middleware.StaticFolders(middleware.StaticFoldersOptions{PublicDir: cfg.PublicDir, FontsDir: cfg.FontsDir, ProtosDir: cfg.ProtosDir, UploadsDir: cfg.UploadsDir, WallpapersDir: cfg.WallpapersDir, Static: cfg.Static, Production: cfg.Production, OnError: onError})},
		{10, "permissions and panel access", s.loadAccess},
		{11, "rate limiters", d.Limits.AppLevel()},
		{12, "client and device API routes", stage(s.early)},
		{14, "request language", s.language},
		{15, "cache header", middleware.NoStoreForNonFiles()},
		{16, "CSRF gate", s.csrfGate},
		{17, "compression mark", middleware.MarkAPICompression(nil)},
		{18, "JSON content type", middleware.RequireJSON()},
		{18, "routes", stage(s.router)},
		{19, "web app", s.webApp},
	}
}

// securityHeaders is step 1.
func (s *Server) securityHeaders() kit.Middleware {
	cfg := s.cfg
	return middleware.SecurityHeaders(middleware.SecurityOptions{HTTPS: cfg.HTTPS, HSTS: middleware.HSTSEnabled(cfg.HTTPS, cfg.HSTSEnabled, cfg.AllowSelfSignedCerts)})
}

// brokenEscape answers a request whose path has a broken percent escape
// (/api/devices/%zz). net/http refuses such a request before any handler,
// and the console's listeners hand it here (listen.BrokenEscapes). The
// answer is the old console's: the security headers of step 1 and 400
// from the error handler, as its router refused a parameter it could not
// decode (JSON under /api/), for the path segment that holds the escape.
// The connection closes after it.
func (s *Server) brokenEscape() http.Handler {
	return s.securityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		segment := ""
		for _, part := range strings.Split(httpx.RawPath(r), "/") {
			if _, err := url.PathUnescape(part); err != nil {
				segment = part
				break
			}
		}
		s.deps.WebUI.ServerError(w, r, &kit.ParamError{Raw: segment}, http.StatusBadRequest)
	}))
}

// buildPipeline chains the steps in front of the end of the pipeline.
func (s *Server) buildPipeline() http.Handler {
	var handler http.Handler = http.HandlerFunc(s.end)
	steps := s.steps()
	for i := len(steps) - 1; i >= 0; i-- {
		handler = steps[i].use(handler)
	}
	return s.begin(handler)
}

// begin prepares a request for the pipeline: the accessors of package kit.
func (s *Server) begin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, kit.Begin(r, s.cfg.TrustProxy, s.deps.I18n.Bundle))
	})
}

// stage answers from a route table and goes on when no route of the table
// takes the request.
func stage(routes *kit.Router) kit.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !routes.Serve(w, r) {
				next.ServeHTTP(w, r)
			}
		})
	}
}

// loadAccess is step 10: it loads the role's effective permissions and
// the account's panel access policy, then lets the panel access guard
// refuse a request to a section the account may not use. The section is
// the one the request's route declares, or the one the guard's path rules
// give the path when the route declares nothing.
func (s *Server) loadAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who := access.Session{UserID: kit.UserID(r)}
		if user := kit.User(r); user != nil {
			who.HasUser, who.Role = true, user.Role
		}
		loaded := s.deps.Access.Load(r.Context(), who)
		kit.SetAccess(r, loaded)
		if verdict := loaded.GuardRoute(r.Method, httpx.RawPath(r), s.routeSection(r)); !verdict.Decision.Passed() {
			s.deps.Refuse(w, r, verdict.Decision)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// routeSection is what the route that answers the request declares about
// its panel section, nil when it declares nothing or no route fits. The
// early stage is asked first, as it answers first.
func (s *Server) routeSection(r *http.Request) *access.RouteSection {
	for _, table := range []*kit.Router{s.early, s.router} {
		if route, ok := table.Find(r); ok {
			return route.Section
		}
	}
	return nil
}

// language is step 14: it picks the request language and remembers a
// ?lang= choice in a cookie.
func (s *Server) language(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang, err := s.deps.I18n.Language(w, r)
		if err != nil {
			s.deps.WebUI.ServerError(w, r, err, http.StatusInternalServerError)
			return
		}
		kit.SetLanguage(r, lang)
		next.ServeHTTP(w, r)
	})
}

// webApp is step 19: the files and pages of the web app.
func (s *Server) webApp(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.deps.WebUI.Handle(w, r) {
			next.ServeHTTP(w, r)
		}
	})
}

// end answers what no step answered: the 404 of step 20.
func (s *Server) end(w http.ResponseWriter, r *http.Request) {
	s.deps.WebUI.NotFound(w, r)
}

// csrfGate is step 16. The gate takes the token from the X-CSRF-Token
// header or, without one, from the _csrf field of the body, which it gets
// from csrfBodyToken.
func (s *Server) csrfGate(next http.Handler) http.Handler {
	return s.deps.CSRF.Gate(next)
}

// cookieAuth reports whether a cookie authenticates a request, for the
// CSRF gate: the session cookie named a stored session (signed in or
// waiting for its second factor), or a guest link's cookie came along.
// The gate checks such a request whatever else it carries; only a request
// no cookie speaks for may pass on a bearer header or the device prefix.
func (s *Server) cookieAuth(r *http.Request) bool {
	if sess := session.From(r); sess != nil && sess.FromCookie() {
		return true
	}
	if guests := s.deps.GuestAccess; guests != nil {
		if _, found := cookie.Get(r, guests.CookieName()); found {
			return true
		}
	}
	return false
}

// csrfBodyToken is the _csrf field of the body the body step has read, for
// the CSRF protector.
func csrfBodyToken(r *http.Request) string {
	token, _ := middleware.Body(r).String("_csrf")
	return token
}
