package auth

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/webui"
)

// logout answers POST /api/auth/logout: it ends the session, signed in or
// not, and makes the browser forget the session cookie.
func (h *handlers) logout(w http.ResponseWriter, r *http.Request) {
	h.signOut(r)
	kit.Session(r).ClearCookies(w)
	kit.OK(w, r, nil)
}

// logoutGetMessage is what GET /logout tells a signed-in browser.
const logoutGetMessage = "Signing out changed to a POST request for CSRF protection. Use the sign-out button in the panel."

// logoutPage answers GET /logout. It signs nobody out: a link or an image
// on another site could send it. A signed-in browser gets 405 with the
// console's error page (plain text without a build) and keeps its session;
// any other goes to the sign-in page.
func (h *handlers) logoutPage(w http.ResponseWriter, r *http.Request) {
	var userID json.RawMessage
	if kit.Session(r).Get("userId", &userID); !jsonTruthy(userID) {
		kit.Redirect(w, r, "/login")
		return
	}
	options := webui.ErrorOptions{Status: http.StatusMethodNotAllowed, Title: "Logout requires POST", Message: logoutGetMessage}
	if h.d.WebUI != nil && h.d.WebUI.ErrorPage(w, r, http.StatusInternalServerError, options) {
		return
	}
	httpx.WriteBody(w, r, http.StatusMethodNotAllowed, "text/plain; charset=utf-8", []byte(logoutGetMessage))
}

// logoutForm answers POST /logout, which passed the CSRF check: it ends
// the session the way POST /api/auth/logout does and sends the browser to
// the sign-in page.
func (h *handlers) logoutForm(w http.ResponseWriter, r *http.Request) {
	h.signOut(r)
	kit.Session(r).ClearCookies(w)
	kit.Redirect(w, r, "/login")
}

// signOut writes the audit entry of a signed-in account, ends the
// session's row with the reason "logout" and destroys the session. Neither
// a failed entry nor a failed write to the session table stops a sign-out.
func (h *handlers) signOut(r *http.Request) {
	sess := kit.Session(r)
	if userID := sess.UserID(); userID != 0 {
		if err := h.audit(r, userID, "logout", "User logged out"); err != nil {
			h.log.Warn("Sign-out audit entry failed:", err)
		}
	}
	sess.End(session.ReasonLogout)
	if err := sess.Destroy(); err != nil {
		h.log.Error("Session destroy error:", err)
	}
}

// verified is the answer of GET /api/auth/verify.
type verified struct {
	Success bool `json:"success"`
	// User is the session's user as it is stored, left out when the
	// session has none.
	User    json.RawMessage `json:"user,omitempty"`
	Session verifiedSession `json:"session"`
}

type verifiedSession struct {
	CreatedAt         *float64        `json:"createdAt"`
	LastActivityAt    *float64        `json:"lastActivityAt"`
	LoginIP           json.RawMessage `json:"loginIp"`
	IdleTimeoutMs     int64           `json:"idleTimeoutMs"`
	MaxAgeMs          int64           `json:"maxAgeMs"`
	WarnBeforeMs      int64           `json:"warnBeforeMs"`
	IdleExpiresAt     *float64        `json:"idleExpiresAt"`
	AbsoluteExpiresAt *float64        `json:"absoluteExpiresAt"`
	ServerNow         int64           `json:"serverNow"`
}

// verify answers GET /api/auth/verify: the session's user and when the
// session ends, for the web app's idle warning.
func (h *handlers) verify(w http.ResponseWriter, r *http.Request) {
	sess := kit.Session(r)
	policy := h.sessions.Policy()
	answer := verified{Success: true, Session: verifiedSession{
		CreatedAt:      sessionTime(sess, "createdAt"),
		LastActivityAt: sessionTime(sess, "lastActivityAt"),
		IdleTimeoutMs:  policy.IdleTimeoutMs,
		MaxAgeMs:       policy.MaxAgeMs,
		WarnBeforeMs:   policy.WarnBeforeMs,
		ServerNow:      h.now().UnixMilli(),
	}}
	sess.Get("user", &answer.User)
	if sess.Get("loginIp", &answer.Session.LoginIP); !jsonTruthy(answer.Session.LoginIP) {
		answer.Session.LoginIP = nil
	}
	if at := answer.Session.LastActivityAt; at != nil {
		end := *at + float64(policy.IdleTimeoutMs)
		answer.Session.IdleExpiresAt = &end
	}
	if at := answer.Session.CreatedAt; at != nil {
		end := *at + float64(policy.MaxAgeMs)
		answer.Session.AbsoluteExpiresAt = &end
	}
	kit.JSON(w, r, http.StatusOK, answer)
}

// sessionTime is Number(session[key]) || null: a time of the session in
// milliseconds, nil when the field is missing, zero or no number.
func sessionTime(sess *session.Session, key string) *float64 {
	var raw json.RawMessage
	if !sess.Get(key, &raw) {
		return nil
	}
	text := strings.TrimSpace(string(raw))
	if text == "true" {
		text = "1"
	}
	if strings.HasPrefix(text, `"`) {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil
		}
		text = jsTrim(s)
	}
	n, err := strconv.ParseFloat(text, 64)
	if err != nil || n == 0 || math.IsNaN(n) || math.IsInf(n, 0) {
		return nil
	}
	return &n
}

type sessionListAnswer struct {
	Success bool            `json:"success"`
	Data    sessionListData `json:"data"`
}

type sessionListData struct {
	Sessions []session.Info `json:"sessions"`
	Policy   session.Policy `json:"policy"`
}

// sessionList answers GET /api/auth/sessions: the account's own sessions,
// ended ones included, and the session policy.
func (h *handlers) sessionList(w http.ResponseWriter, r *http.Request) {
	sess := kit.Session(r)
	list, err := h.sessions.List(r.Context(), sess.UserID(), sess.ID())
	if err != nil {
		h.serverError(w, r, "List sessions error:", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, sessionListAnswer{Success: true, Data: sessionListData{Sessions: list, Policy: h.sessions.Policy()}})
}
