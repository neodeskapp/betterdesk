package auth

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/authn"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session/cookie"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// Single sign-on. The signal server speaks with the identity provider and
// keeps state, nonce and code verifier. The console passes the browser
// along: it asks the signal server where the provider is, hands the
// provider's callback to the signal server, and turns the one-time code
// the signal server answers with into a console session.

// The sign-in page with the reason a single sign-on did not work.
const (
	oidcFailed  = "/login?error=oidc_error"
	oidcInvalid = "/login?error=oidc_invalid"
	// oidcCollision answers a user name that belongs to a local or
	// directory account, with the text of the password sign-in's answer.
	oidcCollision = "/login?error=username_collision"
	// oidcDisabled answers a single sign-on of a disabled account.
	oidcDisabled = "/login?error=account_disabled"
)

// queryText is req.query[name] when it is a text: the parameter given
// once, without brackets. A parameter that is missing, repeated or written
// as name[] or name[key] is no text.
func queryText(r *http.Request, name string) (string, bool) {
	value, seen := "", 0
	for _, pair := range strings.Split(r.URL.RawQuery, "&") {
		if pair == "" {
			continue
		}
		rawKey, rawValue, _ := strings.Cut(pair, "=")
		switch key := queryDecode(rawKey); {
		case key == name:
			seen++
			value = queryDecode(rawValue)
		case strings.HasPrefix(key, name+"["):
			return "", false
		}
	}
	if seen != 1 {
		return "", false
	}
	return value, true
}

// queryDecode decodes one key or value of a query: "+" is a space, and a
// text whose escapes do not spell out UTF-8 stays as it is.
func queryDecode(s string) string {
	s = strings.ReplaceAll(s, "+", " ")
	decoded, err := url.PathUnescape(s)
	if err != nil || (!utf8.ValidString(decoded) && utf8.ValidString(s)) {
		return s
	}
	return decoded
}

// The cookie that ties a single sign-on to the browser that started it. It
// holds the state from authorize on, and the one-time code once the
// callback came through this console. It lives as long as the signal
// server keeps a state.
const (
	oidcCookie    = "nd.oidc"
	oidcCookieAge = 10 * time.Minute
	// oidcSessionPath is where the signal server sends the browser after
	// the callback of a console sign-in.
	oidcSessionPath = "/api/auth/oidc/session"
)

func (h *handlers) setOIDCCookie(w http.ResponseWriter, r *http.Request, value string) {
	cookie.Set(w, oidcCookie, value, cookie.Attributes{MaxAge: oidcCookieAge, Path: "/api", HTTPOnly: true, Secure: h.sessions.SecureCookie(r), SameSite: "lax"})
}

func (h *handlers) clearOIDCCookie(w http.ResponseWriter, r *http.Request) {
	cookie.Clear(w, oidcCookie, cookie.Attributes{Path: "/api", HTTPOnly: true, Secure: h.sessions.SecureCookie(r), SameSite: "lax"})
}

// sameText compares two values of the cookie in constant time; an empty
// one fits nothing.
func sameText(a, b string) bool {
	return a != "" && b != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// sessionCode is the one-time code of a Location that sends the browser to
// the session route, "" for any other Location.
func sessionCode(location string) string {
	u, err := url.Parse(location)
	if err != nil || u.Path != oidcSessionPath {
		return ""
	}
	return u.Query().Get("code")
}

type oidcOff struct {
	Enabled bool `json:"enabled"`
}

// oidcStatus answers GET /api/auth/oidc/status for the sign-in page: what
// the signal server says about single sign-on (enabled, display_name,
// prefer_sso) and local_bypass, which tells a client on a private network
// that it may still sign in with a password. Without an answer of the
// signal server it is {"enabled":false}.
func (h *handlers) oidcStatus(w http.ResponseWriter, r *http.Request) {
	result := h.d.GoAPI.GetOIDCStatus(r.Context())
	if !result.Bool("success") {
		kit.JSON(w, r, http.StatusOK, oidcOff{})
		return
	}
	answer := spread(result.Get("data"))
	answer.Set("local_bypass", authn.IsPrivateClientIP(kit.ClientIP(r)))
	kit.JSON(w, r, http.StatusOK, answer)
}

// spread is {...v}: the members of an object, the items of a list under
// their index and the characters of a text under theirs.
func spread(v any) *goapi.Object {
	out := goapi.NewObject()
	switch value := v.(type) {
	case *goapi.Object:
		if value != nil {
			return value.Clone()
		}
	case []any:
		for i, item := range value {
			out.Set(strconv.Itoa(i), item)
		}
	case string:
		for i, unit := range utf16.Encode([]rune(value)) {
			out.Set(strconv.Itoa(i), string(utf16.Decode([]uint16{unit})))
		}
	}
	return out
}

// oidcAuthorize answers GET /api/auth/oidc/authorize?return_url=<path>:
// it sends the browser to the identity provider. The address comes from
// the signal server, which also remembers the way back; the browser never
// sees the signal server's own address. The browser gets the address's
// state in its cookie.
func (h *handlers) oidcAuthorize(w http.ResponseWriter, r *http.Request) {
	returnTo := "/"
	if requested, ok := queryText(r, "return_url"); ok {
		if path, safe := httpx.SafeReturnPath(requested); safe {
			returnTo = path
		}
	}
	result := h.d.GoAPI.StartOIDCAuthorize(r.Context(), returnTo)
	target := result.Object("data").Str("auth_url")
	if !result.Bool("success") || target == "" {
		kit.Redirect(w, r, oidcFailed)
		return
	}
	provider, err := url.Parse(target)
	if err != nil || provider.Query().Get("state") == "" {
		h.log.Warn("[OIDC] The identity provider's address has no state")
		kit.Redirect(w, r, oidcFailed)
		return
	}
	h.setOIDCCookie(w, r, provider.Query().Get("state"))
	kit.Redirect(w, r, target)
}

// callbackQuery is the query of a request as the old console read it,
// for the signal server: every parameter with its values in order.
func callbackQuery(r *http.Request) url.Values {
	query := url.Values{}
	for _, pair := range strings.Split(r.URL.RawQuery, "&") {
		if pair == "" {
			continue
		}
		key, value, _ := strings.Cut(pair, "=")
		query.Add(queryDecode(key), queryDecode(value))
	}
	return query
}

// exchangeText is a value of the signal server's exchange answer as text.
// The signal server sends texts; any other value is written as its JSON.
func exchangeText(v any) string {
	if text, ok := v.(string); ok {
		return text
	}
	raw, err := httpx.Marshal(v)
	if err != nil {
		return ""
	}
	return string(raw)
}

// oidcCallback answers GET /api/auth/oidc/callback and GET
// /api/oidc/callback, where the identity provider sends the browser back
// to: the signal server gets the query and its answer goes to the browser
// with status, Content-Type, Location and Cache-Control.
//
// An answer that leads to a console session goes only to the browser whose
// cookie holds the callback's state, and the cookie then holds the
// one-time code; any other browser goes back to the sign-in page. Other
// answers, such as the desktop client's result page, go to any browser.
func (h *handlers) oidcCallback(w http.ResponseWriter, r *http.Request) {
	// The browser's address goes with the call, so the server's audit
	// entry of the sign-in names the browser, not 127.0.0.1 (L357)
	answer := h.d.GoAPI.ProxyOIDCCallback(authn.WithClientAddress(r.Context(), kit.ClientIP(r)), callbackQuery(r))
	if code := sessionCode(answer.Location); code != "" {
		held, _ := cookie.Get(r, oidcCookie)
		state, _ := queryText(r, "state")
		if !sameText(held, state) {
			h.log.Warn("[OIDC] Callback of a sign-in this browser did not start")
			kit.Redirect(w, r, oidcInvalid)
			return
		}
		h.setOIDCCookie(w, r, code)
	}
	header := w.Header()
	if answer.Location != "" {
		header.Set("Location", answer.Location)
	}
	if answer.CacheControl != "" {
		header.Set("Cache-Control", answer.CacheControl)
	}
	// A body without a type of its own goes out as plain bytes
	contentType := answer.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	httpx.WriteBody(w, r, answer.Status, contentType, answer.Body)
}

// oidcSession answers GET /api/auth/oidc/session?code=<code>, the last hop
// of a single sign-on. The code is good once and for a minute; the signal
// server exchanges it for the user it signed in. The browser's cookie must
// hold the code, or the state the exchange names (when the identity
// provider sent the browser to the signal server's own port); a browser
// without it goes back to the sign-in page and the code stays unused. That user gets a local
// account if it has none, and a console session. A name that belongs to a
// local or directory account goes back to the sign-in page with
// username_collision, a disabled account with account_disabled. A pro
// account gets 403 with the text of the password sign-in and no session.
//
// No second factor is asked for here.
func (h *handlers) oidcSession(w http.ResponseWriter, r *http.Request) {
	code, _ := queryText(r, "code")
	if code == "" {
		kit.Redirect(w, r, oidcInvalid)
		return
	}
	held, _ := cookie.Get(r, oidcCookie)
	if held == "" {
		h.log.Warn("[OIDC] Session code in a browser that started no sign-in")
		kit.Redirect(w, r, oidcInvalid)
		return
	}
	h.clearOIDCCookie(w, r)
	exchange := h.d.GoAPI.ExchangeOIDCCode(r.Context(), code)
	data := exchange.Object("data")
	if !exchange.Bool("success") || !data.Bool("token") || !data.Bool("username") {
		h.log.Warn("[OIDC] Exchange rejected by Go server")
		kit.Redirect(w, r, oidcInvalid)
		return
	}
	if state, _ := data.Get("state").(string); !sameText(held, code) && !sameText(held, state) {
		h.log.Warn("[OIDC] Session code of a sign-in this browser did not start")
		kit.Redirect(w, r, oidcInvalid)
		return
	}
	returnTo := "/"
	if raw, ok := data.Get("return_url").(string); ok {
		if path, safe := httpx.SafeReturnPath(raw); safe {
			returnTo = path
		}
	}

	role, _ := data.Get("role").(string)
	user, err := h.authn.OIDCUser(r.Context(), exchangeText(data.Get("username")), role)
	if errors.Is(err, authn.ErrUsernameCollision) {
		kit.Redirect(w, r, oidcCollision)
		return
	}
	if errors.Is(err, authn.ErrAccountDisabled) {
		kit.Redirect(w, r, oidcDisabled)
		return
	}
	if err != nil {
		h.log.Error("OIDC session creation error:", err)
		kit.Redirect(w, r, oidcFailed)
		return
	}
	if user == nil {
		kit.Redirect(w, r, oidcFailed)
		return
	}
	if h.syncPicture != nil {
		h.syncPicture(user.ID.V, data)
	}

	// The role is the signal server's, which checked the sign-in; the
	// stored one only when the signal server named none
	sessionRole := store.NullString{String: "viewer", Valid: true}
	switch {
	case goapi.Truthy(data.Get("role")):
		sessionRole.String = exchangeText(data.Get("role"))
	case user.Role.String != "":
		sessionRole.String = user.Role.String
	}
	// Accounts for the desktop client only get no console session, as at
	// the password sign-in
	if sessionRole.String == "pro" {
		kit.Fail(w, r, http.StatusForbidden, kit.T(r, "auth.pro_only_account", nil))
		return
	}
	who := newAccount(user.ID, user.Username, sessionRole, user.PreferredLanguage)
	meta := h.sessions.ClientMeta(r, "")
	est, err := h.establish(r, who, meta)
	replaced := len(est.Ended)
	if err != nil {
		h.log.Error("OIDC session establish error:", err)
		kit.Redirect(w, r, oidcFailed)
		return
	}
	// What only a session from single sign-on carries
	sess := kit.Session(r)
	sess.Set("username", who.Username)
	sess.Set("role", who.Role)
	sess.Set("goToken", data.Get("token"))
	sess.Set("authMethod", "oidc")
	h.clearGuestCookie(w, r)

	err = h.authn.TouchLastLogin(r.Context(), who.id)
	if err == nil {
		details := "User logged in via OIDC; ip=" + meta.IP + "; ua=" + cutUTF16(meta.UserAgent, 160) + replacedNote(replaced)
		err = h.audit(r, who.id, "login", details)
	}
	if err == nil {
		err = h.auditEnded(r, who.id, est, meta, "OIDC login")
	}
	if err != nil {
		h.log.Error("OIDC post-auth bookkeeping error:", err)
	}
	kit.Redirect(w, r, returnTo)
}
