package kit

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/webui"
	"github.com/unitronix/betterdesk-server/logging"
)

// The guards are the middleware of middleware/auth.js under their old
// names. Each asks package access for the decision and sends the answer
// the old console sent: JSON when the request's original address starts
// with /api/ (also below a mount), else a redirect or the web app's error
// page, which is plain text when there is no build.

// RequireAuth lets a signed-in account through that is not a pro account.
//
//	no session, API   401 {"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}
//	no session, page  302 to the sign-in page with the way back
//	pro account       the session is destroyed; 403 JSON, or 302 to /login
func (d *Deps) RequireAuth(next http.Handler) http.Handler {
	return d.guard(next, func(r *http.Request) access.Decision {
		return Access(r).RequireAuth(httpx.OriginalURL(r))
	})
}

// RequireAdmin lets super_admin, admin, global_admin and server_admin
// through.
//
//	no session   401 {"success":false,"error":"Unauthorized"}, or 302 to /login
//	other roles  403 {"success":false,"error":"Admin access required"}, or the 403 page
func (d *Deps) RequireAdmin(next http.Handler) http.Handler {
	return d.guard(next, func(r *http.Request) access.Decision {
		return Access(r).RequireAdmin(httpx.OriginalURL(r))
	})
}

// RequireRole lets the named role and the super admin roles through.
//
//	no session   401 {"success":false,"error":"Unauthorized"}, or 302 to /login
//	other roles  403 {"success":false,"error":"Forbidden"}, or the 403 page
func (d *Deps) RequireRole(role string) Middleware {
	return func(next http.Handler) http.Handler {
		return d.guard(next, func(r *http.Request) access.Decision {
			return Access(r).RequireRole(role, httpx.OriginalURL(r))
		})
	}
}

// RequirePermission lets an account through that has the permission.
//
//	no session      401 {"success":false,"error":"Unauthorized"}, or 302 to /login
//	lookup failed   503 {"success":false,"error":"Permission service unavailable","code":"permission_service_unavailable"},
//	                or the error page with status 503
//	not allowed     403 {"success":false,"error":"Permission denied: <permission>"}, or the 403 page
func (d *Deps) RequirePermission(permission string) Middleware {
	return func(next http.Handler) http.Handler {
		return d.guard(next, func(r *http.Request) access.Decision {
			return Access(r).RequirePermission(permission, httpx.OriginalURL(r))
		})
	}
}

// RequireAnyPermission lets an account through that has one of the
// permissions. It answers JSON on every path and does not look at the
// session, so RequireAuth goes in front of it.
func (d *Deps) RequireAnyPermission(permissions ...string) Middleware {
	return func(next http.Handler) http.Handler {
		return d.guard(next, func(r *http.Request) access.Decision {
			return Access(r).RequireAnyPermission(permissions...)
		})
	}
}

// RequireRdClientAuth is RequireAuth for the remote client: people
// without a session go to /remote/login with the way back, and a
// permission is checked when one is named ("" for none).
func (d *Deps) RequireRdClientAuth(permission string) Middleware {
	return func(next http.Handler) http.Handler {
		return d.guard(next, func(r *http.Request) access.Decision {
			return Access(r).RequireRdClientAuth(permission, httpx.OriginalURL(r), singleQueryValue(r.URL.RawQuery, "return"))
		})
	}
}

func (d *Deps) guard(next http.Handler, decide func(r *http.Request) access.Decision) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decision := decide(r)
		if decision.Passed() {
			next.ServeHTTP(w, r)
			return
		}
		d.Refuse(w, r, decision)
	})
}

// Refuse sends the answer of a decision that did not pass. The panel
// access guard of the pipeline answers through it too.
func (d *Deps) Refuse(w http.ResponseWriter, r *http.Request, decision access.Decision) {
	if decision.DestroySession {
		if s := session.From(r); s != nil {
			if err := s.Destroy(); err != nil {
				logging.Logf("console", "WARN: destroying the session of a refused account failed: %v", err)
			}
		}
	}
	switch decision.Answer {
	case access.AnswerJSON:
		httpx.WriteJSON(w, r, decision.Status, decision.Body)
	case access.AnswerRedirect:
		Redirect(w, r, decision.Location)
	case access.AnswerLoginRedirect:
		Redirect(w, r, httpx.LoginRedirectURL(r, nil))
	case access.AnswerErrorPage:
		options := webui.ErrorOptions{Status: decision.Status, Message: decision.Page.Message}
		if d.WebUI != nil && d.WebUI.ErrorPage(w, r, decision.Page.Kind, options) {
			return
		}
		httpx.WriteBody(w, r, decision.Status, "text/plain; charset=utf-8", []byte(decision.Page.Text))
	default:
		httpx.WriteJSON(w, r, http.StatusInternalServerError, failure{Error: "Internal Server Error"})
	}
}

// Redirect answers 302 the way res.redirect(target) did (httpx.Redirect).
func Redirect(w http.ResponseWriter, r *http.Request, target string) {
	httpx.Redirect(w, r, http.StatusFound, target)
}

// singleQueryValue is req.query[name] when the query gives name once as
// plain text, and "" when it is missing, repeated or written with
// brackets (name[]=, name[x]=), which the old console read as a list or
// an object.
func singleQueryValue(rawQuery, name string) string {
	value, seen := "", 0
	for _, pair := range strings.Split(rawQuery, "&") {
		if pair == "" {
			continue
		}
		rawKey, rawValue, _ := strings.Cut(pair, "=")
		key := queryDecode(rawKey)
		switch {
		case key == name:
			seen++
			value = queryDecode(rawValue)
		case strings.HasPrefix(key, name+"["):
			return ""
		}
	}
	if seen != 1 {
		return ""
	}
	return value
}

// queryDecode decodes one key or value of a query string: "+" is a space,
// and a text whose escapes do not decode stays as it is.
func queryDecode(s string) string {
	s = strings.ReplaceAll(s, "+", " ")
	if decoded, err := url.PathUnescape(s); err == nil {
		return decoded
	}
	return s
}
