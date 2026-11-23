package kit

import (
	"context"
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/csrf"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/i18n"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
)

type requestKey struct{}

// requestInfo is what the pipeline has worked out for one request. The
// steps fill it in as the request moves on, so a handler sees what the
// steps before it left.
type requestInfo struct {
	trust  httpx.TrustProxy
	bundle *i18n.Bundle
	access *access.Context
	lang   string
}

func infoOf(r *http.Request) *requestInfo {
	info, _ := r.Context().Value(requestKey{}).(*requestInfo)
	return info
}

// Begin prepares a request for the accessors of this package. The
// pipeline calls it once, before its first step; tests of a route group
// call it to stand in for the pipeline.
func Begin(r *http.Request, trust httpx.TrustProxy, bundle *i18n.Bundle) *http.Request {
	info := &requestInfo{trust: trust, bundle: bundle}
	return r.WithContext(context.WithValue(r.Context(), requestKey{}, info))
}

// SetAccess stores the permissions and panel access loaded for the
// request (step 10 of the pipeline).
func SetAccess(r *http.Request, c *access.Context) {
	if info := infoOf(r); info != nil {
		info.access = c
	}
}

// SetLanguage stores the request language (step 14 of the pipeline).
func SetLanguage(r *http.Request, lang string) {
	if info := infoOf(r); info != nil {
		info.lang = lang
	}
}

// Session is the request's session (req.session). It is never nil behind
// the pipeline; someone signed out has an empty one.
func Session(r *http.Request) *session.Session { return session.From(r) }

// User is the signed-in account as the session holds it
// (req.session.user), nil when nobody is signed in.
func User(r *http.Request) *session.User {
	if s := session.From(r); s != nil {
		return s.User()
	}
	return nil
}

// UserID is the id of the signed-in account (req.session.userId), 0 when
// nobody is signed in.
func UserID(r *http.Request) int64 {
	if s := session.From(r); s != nil {
		return s.UserID()
	}
	return 0
}

// Access is the request's permission context: the role's effective
// permissions, the account's panel access and the section of the path.
// HasPermission on it is requestHasPermission(req, permission). Before
// step 10 of the pipeline it is an empty context, which decides by the
// built-in role map.
func Access(r *http.Request) *access.Context {
	if info := infoOf(r); info != nil && info.access != nil {
		return info.access
	}
	c := &access.Context{}
	if user := User(r); user != nil {
		c.UserID, c.HasUser, c.Role = user.ID, true, user.Role
	} else {
		c.UserID = UserID(r)
	}
	return c
}

// HasPermission reports whether the request's account has the permission.
func HasPermission(r *http.Request, permission string) bool {
	return Access(r).HasPermission(permission)
}

// Lang is the request language (req.lang). It is "" in the early stage,
// which runs before the language is picked.
func Lang(r *http.Request) string {
	if info := infoOf(r); info != nil {
		return info.lang
	}
	return ""
}

// T is req.t(key, vars): the text for key in the request language, with
// the default language and then the key itself as fallbacks. In the early
// stage it uses the default language.
func T(r *http.Request, key string, vars i18n.Vars) string {
	info := infoOf(r)
	if info == nil || info.bundle == nil {
		return key
	}
	lang := info.lang
	if lang == "" {
		lang = info.bundle.DefaultLang
	}
	return info.bundle.T(lang, key, vars)
}

// Vars builds the placeholder values for T from name, value pairs:
// T(r, "devices.count", Vars("count", 3)).
func Vars(pairs ...any) i18n.Vars { return i18n.V(pairs...) }

// ClientIP is the client's address under TRUST_PROXY (req.ip).
func ClientIP(r *http.Request) string {
	if info := infoOf(r); info != nil {
		return info.trust.ClientIP(r)
	}
	return httpx.SocketIP(r)
}

// Secure reports whether the request came over HTTPS, counting a trusted
// proxy (req.secure).
func Secure(r *http.Request) bool {
	if info := infoOf(r); info != nil {
		return info.trust.Scheme(r) == "https"
	}
	return r.TLS != nil
}

// OriginalURL is the request target as the client sent it, path and
// query (req.originalUrl).
func OriginalURL(r *http.Request) string { return httpx.OriginalURL(r) }

// CSRFToken is the token the web app sends back with its writes
// (res.locals.csrfToken). It is set on GET, HEAD and OPTIONS requests.
func CSRFToken(r *http.Request) string { return csrf.Token(r) }

// Body is req.body as an object: the members of a JSON object or the
// fields of a form, empty when the request had no body the console reads.
func Body(r *http.Request) middleware.JSONObject { return middleware.Body(r) }

// Bind decodes req.body into v, for bodies with a fixed shape.
func Bind(r *http.Request, v any) error { return middleware.Bind(r, v) }

// JSON answers with v as JSON (res.status(status).json(v)), with the
// ETag, 304 and compression rules of every JSON answer of the console.
func JSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	httpx.WriteJSON(w, r, status, v)
}

type failure struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
}

// Fail answers {"success":false,"error":message} with the status.
func Fail(w http.ResponseWriter, r *http.Request, status int, message string) {
	httpx.WriteJSON(w, r, status, failure{Error: message})
}

// FailCode answers {"success":false,"error":message,"code":code}.
func FailCode(w http.ResponseWriter, r *http.Request, status int, message, code string) {
	httpx.WriteJSON(w, r, status, failure{Error: message, Code: code})
}

type success struct {
	Success bool `json:"success"`
	Data    any  `json:"data,omitempty"`
}

// OK answers 200 {"success":true,"data":data}, and {"success":true}
// when data is nil.
func OK(w http.ResponseWriter, r *http.Request, data any) {
	httpx.WriteJSON(w, r, http.StatusOK, success{Success: true, Data: data})
}
