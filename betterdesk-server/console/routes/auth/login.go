package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/auditlog"
	"github.com/unitronix/betterdesk-server/console/authn"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/session/cookie"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The fields a session carries between the password and the second factor.
const (
	pendingUserID = "pendingTotpUserId"
	pendingUser   = "pendingTotpUser"
	pendingMeta   = "pendingClientMeta"
	// pendingSince is when the password step was passed, in milliseconds
	// since 1970.
	pendingSince = "pendingTotpAt"
	// pendingFailures counts the wrong codes given since the password
	// step.
	pendingFailures = "pendingTotpFailures"
	partialToken    = "goPartialToken"
)

// secondFactorWindow is how long the code may take after the password
// step, and maxWrongCodes how many wrong codes end the wait.
const (
	secondFactorWindow = 5 * time.Minute
	maxWrongCodes      = 5
)

// errNotText stands for the fault the old console ran into when a password
// that is not text reached the hash compare.
var errNotText = errors.New("the password is not text")

// clientMeta is what a sign-in records about the client. The fingerprint
// is the body's clientFingerprint, else its fingerprint, else the header
// X-Client-Fingerprint.
func (h *handlers) clientMeta(r *http.Request, body middleware.JSONObject) session.Meta {
	for _, key := range []string{"clientFingerprint", "fingerprint"} {
		if v := body.Get(key); v.Truthy() {
			text := jsString(v)
			if text == "" {
				// A value that counts but has no text (an empty list) is
				// no fingerprint, and the header is not asked either
				text = " "
			}
			return h.sessions.ClientMeta(r, text)
		}
	}
	return h.sessions.ClientMeta(r, "")
}

// returnPath is where the page goes after sign-in: the path the body's
// "return" names when it is a path of this console, else the start page.
func returnPath(body middleware.JSONObject) string {
	if raw, ok := body.Get("return").Str(); ok {
		if path, safe := httpx.SafeReturnPath(raw); safe {
			return path
		}
	}
	return "/"
}

// account is the account a sign-in establishes a session for, with every
// value in the JSON form the session stores.
type account struct {
	// id is the users row id as a number.
	id int64
	// ID, Username, Role and PreferredLanguage are the JSON of the
	// session's user. A nil one is left out.
	ID                json.RawMessage
	Username          json.RawMessage
	Role              json.RawMessage
	PreferredLanguage json.RawMessage
	username          string
	role              string
	language          string
}

// sessionUser is the "user" field of a session.
type sessionUser struct {
	ID                json.RawMessage `json:"id,omitempty"`
	Username          json.RawMessage `json:"username,omitempty"`
	Role              json.RawMessage `json:"role,omitempty"`
	PreferredLanguage json.RawMessage `json:"preferred_language"`
}

func mustJSON(v any) json.RawMessage {
	raw, err := httpx.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("auth: value cannot be written as JSON: %v", err))
	}
	return raw
}

// accountOf is the account as a password sign-in answered it.
func accountOf(user *authn.User) account {
	return newAccount(user.ID, user.Username, user.Role, user.PreferredLanguage)
}

func newAccount(id store.Int64String, username string, role, language store.NullString) account {
	language = language.OrNull()
	return account{
		id: id.V, username: username, role: role.String, language: language.String,
		ID: mustJSON(id), Username: mustJSON(username), Role: mustJSON(role), PreferredLanguage: mustJSON(language),
	}
}

// establish gives the request a new session and signs the account in on
// it. It answers how many of the account's other sessions that ended.
//
// The session's userId and user are written in the JSON form the account's
// values have: an id is text where the users table has 64-bit ids, and a
// role can be null.
func (h *handlers) establish(r *http.Request, who account, meta session.Meta) (replaced int, err error) {
	sess := kit.Session(r)
	if err := sess.Regenerate(); err != nil {
		return 0, fmt.Errorf("regenerate the session: %w", err)
	}
	user := session.User{ID: who.id, Username: who.username, Role: who.role, PreferredLanguage: who.language}
	replaced, err = sess.Establish(user, meta)
	if err != nil {
		return 0, fmt.Errorf("establish the session: %w", err)
	}
	if who.ID != nil {
		sess.Set("userId", who.ID)
	}
	sess.Set("user", sessionUser{ID: who.ID, Username: who.Username, Role: who.Role, PreferredLanguage: who.PreferredLanguage})
	return replaced, nil
}

// clearGuestCookie makes the browser forget the cookie of a guest link.
func (h *handlers) clearGuestCookie(w http.ResponseWriter) {
	cookie.Clear(w, h.guestCookie, cookie.Attributes{Path: "/", HTTPOnly: true, Secure: h.d.Config.HTTPS, SameSite: "lax"})
}

// replacedNote is the end of a sign-in's audit text when the sign-in ended
// other sessions of the account.
func replacedNote(replaced int) string {
	if replaced == 0 {
		return ""
	}
	return fmt.Sprintf("; replaced=%d prior session(s)", replaced)
}

// signedIn is the answer to a finished sign-in.
type signedIn struct {
	Success  bool           `json:"success"`
	User     signedInUser   `json:"user"`
	Redirect string         `json:"redirect"`
	Session  signedInPolicy `json:"session"`
}

type signedInUser struct {
	Username json.RawMessage `json:"username,omitempty"`
	Role     json.RawMessage `json:"role,omitempty"`
}

type signedInPolicy struct {
	ReplacedPriorSessions int   `json:"replacedPriorSessions"`
	IdleTimeoutMs         int64 `json:"idleTimeoutMs"`
	MaxAgeMs              int64 `json:"maxAgeMs"`
}

func (h *handlers) signedInAnswer(who account, replaced int, body middleware.JSONObject) signedIn {
	policy := h.sessions.Policy()
	return signedIn{
		Success:  true,
		User:     signedInUser{Username: who.Username, Role: who.Role},
		Redirect: returnPath(body),
		Session:  signedInPolicy{ReplacedPriorSessions: replaced, IdleTimeoutMs: policy.IdleTimeoutMs, MaxAgeMs: policy.MaxAgeMs},
	}
}

// sessionFailure answers a sign-in whose session could not be set up or
// whose bookkeeping failed: always in English.
func (h *handlers) sessionFailure(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.log.Error(what, err)
	kit.Fail(w, r, http.StatusInternalServerError, "Server error")
}

// login answers POST /api/auth/login with the body {username, password,
// return?, clientFingerprint? or fingerprint?}.
//
// Before the password is looked at, a lockout of the account and too many
// failed attempts from the address refuse the sign-in (429). Every
// attempt that reaches the password check is recorded.
func (h *handlers) login(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	username, password := body.Get("username"), body.Get("password")
	meta := h.clientMeta(r, body)

	if !username.Truthy() || !password.Truthy() || tooLong(username) || tooLong(password) {
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "auth.invalid_credentials", nil))
		return
	}

	name, ok := h.boundName(username)
	if !ok {
		h.serverError(w, r, "Login error:", errUnboundName)
		return
	}
	lockout, err := h.authn.CheckBruteForceFor(r.Context(), name, kit.ClientIP(r))
	if err != nil {
		h.serverError(w, r, "Login error:", err)
		return
	}
	if lockout.Blocked {
		h.refuseLockedOut(w, r, lockout, meta)
		return
	}

	outcome, err := h.authenticate(r, username, password)
	if err != nil {
		h.serverError(w, r, "Login error:", err)
		return
	}
	client := "; ip=" + meta.IP + "; ua=" + cutUTF16(meta.UserAgent, 160)

	// Accounts for the desktop client only: the password was right, so the
	// attempt counts as a success, but the console is not theirs.
	if outcome.Failure == authn.FailureProOnly {
		h.recordAttempt(r, username, true)
		kit.Fail(w, r, http.StatusForbidden, kit.T(r, "auth.pro_only_account", nil))
		return
	}

	if outcome.IsFailure() || outcome.User == nil {
		h.recordAttempt(r, username, false)
		details := "Username: " + jsString(username) + client
		if outcome.IsFailure() {
			details = "Username collision: " + auditlog.RedactUsername(jsString(username)) + client
		}
		if err := h.audit(r, 0, "login_failed", details); err != nil {
			h.serverError(w, r, "Login error:", err)
			return
		}
		if outcome.Failure == authn.FailureUsernameCollision {
			kit.FailCode(w, r, http.StatusConflict, kit.T(r, "auth.username_collision", nil), "username_collision")
			return
		}
		kit.Fail(w, r, http.StatusUnauthorized, kit.T(r, "auth.invalid_credentials", nil))
		return
	}

	h.recordAttempt(r, username, true)
	user := outcome.User

	if user.TOTPRequired {
		// The pending account goes into a new session: a session id that
		// someone planted before the sign-in must not carry it.
		sess := kit.Session(r)
		if err := sess.Regenerate(); err != nil {
			h.sessionFailure(w, r, "Session regeneration error (2FA):", err)
			return
		}
		sess.Set(pendingUserID, user.ID)
		sess.Set(pendingUser, user)
		sess.Set(pendingMeta, meta)
		sess.Set(pendingSince, h.now().UnixMilli())
		kit.JSON(w, r, http.StatusOK, secondFactorDue{Success: true, TOTPRequired: true})
		return
	}

	who := accountOf(user)
	replaced, err := h.establish(r, who, meta)
	if err != nil {
		h.sessionFailure(w, r, "Session establish error:", err)
		return
	}
	h.clearGuestCookie(w)

	fingerprint := meta.Fingerprint
	if fingerprint == "" {
		fingerprint = "none"
	}
	err = h.audit(r, who.id, "login", "User logged in"+client+"; fingerprint="+fingerprint+replacedNote(replaced))
	if err == nil && replaced > 0 {
		err = h.audit(r, who.id, "session_replaced", fmt.Sprintf("New login replaced %d prior session(s); newIp=%s", replaced, meta.IP))
	}
	if err != nil {
		h.sessionFailure(w, r, "Session establish error:", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, h.signedInAnswer(who, replaced, body))
}

type secondFactorDue struct {
	Success      bool `json:"success"`
	TOTPRequired bool `json:"totpRequired"`
}

// authenticate checks the credentials of a sign-in, whatever their JSON
// type.
//
// A user name that is not text is refused. A password that is not text is
// a server error, unless the account is refused before its password is
// looked at: a blank name, an account bound to single sign-on, and a
// directory account, whose password the signal server would refuse.
func (h *handlers) authenticate(r *http.Request, username, password middleware.Value) (authn.Outcome, error) {
	name, isText := username.Str()
	if !isText {
		return authn.Outcome{}, nil
	}
	if pass, isText := password.Str(); isText {
		return h.authn.Authenticate(r.Context(), name, pass)
	}
	if jsTrim(name) == "" {
		return authn.Outcome{}, nil
	}
	user, err := h.authn.UserByUsername(r.Context(), name)
	if err != nil {
		return authn.Outcome{}, err
	}
	if user != nil {
		switch strings.ToLower(jsTrim(user.AuthProvider.String)) {
		case "oidc", "ldap":
			return authn.Outcome{}, nil
		}
	}
	return authn.Outcome{}, errNotText
}

// recordAttempt notes a sign-in attempt for the lockout counters. A
// failure to note it changes no answer.
//
// The name is recorded as the database took the value from the old
// console (boundName). Where SQLite refused it, nothing is recorded.
func (h *handlers) recordAttempt(r *http.Request, username middleware.Value, success bool) {
	name, ok := h.boundName(username)
	if !ok {
		return
	}
	if err := h.authn.RecordAttemptFor(r.Context(), name, kit.ClientIP(r), success); err != nil {
		h.log.Warn("Recording the sign-in attempt failed:", err)
	}
}

// errUnboundName stands for the error the old console's SQLite driver
// threw for a user name it could not bind: true or false, an empty list or
// one of several values, a list that holds a list or an object, an object.
var errUnboundName = errors.New("the database cannot take the user name as a parameter")

// boundName is the statement parameter the old console's database driver
// made of a user name of any JSON type (console/jsbind), and false where
// it refused the value. SQLite took a text as it is, a number as a double
// (stored in a text column as "12345.0") and a list of one value as that
// value; PostgreSQL took the text form of anything.
func (h *handlers) boundName(username middleware.Value) (any, bool) {
	args, err := jsbind.Args(h.d.Store.Dialect(), 1, jsbind.Of(username))
	if err != nil {
		return nil, false
	}
	return args[0], true
}

// accountLocked is the answer to a sign-in that a lockout refused.
type accountLocked struct {
	Success    bool   `json:"success"`
	Error      string `json:"error"`
	Code       string `json:"code"`
	RetryAfter int64  `json:"retry_after"`
}

// lockoutRetryFallback is the wait in seconds for a lockout whose end
// could not be read as a time.
const lockoutRetryFallback = 60

// refuseLockedOut answers a sign-in that a lockout of the account or of
// the address refused: an audit entry, the seconds to wait in Retry-After
// and in the body, and 429.
func (h *handlers) refuseLockedOut(w http.ResponseWriter, r *http.Request, lockout authn.BruteForce, meta session.Meta) {
	if err := h.audit(r, 0, "login_blocked", "Account/IP locked; ip="+meta.IP); err != nil {
		h.log.Warn("Audit entry of a blocked sign-in failed:", err)
	}
	retry := int64(lockoutRetryFallback)
	if !lockout.RetryUnknown {
		retry = lockout.RetryAfter
	}
	w.Header().Set("Retry-After", strconv.FormatInt(retry, 10))
	kit.JSON(w, r, http.StatusTooManyRequests, accountLocked{
		Error: kit.T(r, "auth.too_many_attempts", nil), Code: "account_locked", RetryAfter: retry,
	})
}

// totpVerify answers POST /api/auth/totp/verify with the body {code?,
// recoveryCode?, return?}: the second step of a sign-in, on the session
// the password step left the account in.
func (h *handlers) totpVerify(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	code, recoveryCode := body.Get("code"), body.Get("recoveryCode")
	sess := kit.Session(r)

	var rawID, rawUser json.RawMessage
	sess.Get(pendingUserID, &rawID)
	sess.Get(pendingUser, &rawUser)
	if !jsonTruthy(rawID) || !jsonTruthy(rawUser) {
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "auth.totp_session_expired", nil))
		return
	}
	if !h.pendingInTime(sess) {
		endPending(sess)
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "auth.totp_session_expired", nil))
		return
	}
	userID := jsonInt(rawID)
	who := pendingAccount(rawUser)

	// A session that carries a partial token of the signal server lets the
	// signal server check the code. No sign-in stores one.
	var token json.RawMessage
	if sess.Get(partialToken, &token); jsonTruthy(token) {
		given := ""
		for _, v := range []middleware.Value{code, recoveryCode} {
			if v.Truthy() {
				given = jsString(v)
				break
			}
		}
		var partial string
		_ = json.Unmarshal(token, &partial)
		answer, _ := h.d.GoAPI.VerifyTOTPViaGo(r.Context(), partial, given).(*goapi.Object)
		switch {
		case answer == nil:
			// No local check instead: it would skip the signal server's
			// two-factor policy and its limits whenever the server is down.
			h.log.Error("Go server unreachable during delegated 2FA, refusing the local check")
			if err := h.audit(r, userID, "totp_unavailable", "Delegated 2FA unavailable (Go unreachable)"); err != nil {
				h.log.Warn("Audit entry of an unavailable 2FA check failed:", err)
			}
			kit.FailCode(w, r, http.StatusServiceUnavailable, kit.T(r, "auth.totp_temporarily_unavailable", nil), "totp_backend_unavailable")
			return
		case answer.Bool("rejected") && answer.Bool("rateLimited"):
			kit.Fail(w, r, http.StatusTooManyRequests, kit.T(r, "auth.totp_rate_limited", nil))
			return
		case answer.Bool("rejected"):
			countWrongCode(sess)
			if err := h.audit(r, userID, "totp_failed", "Go server rejected TOTP"); err != nil {
				h.serverError(w, r, "TOTP verify error:", err)
				return
			}
			kit.Fail(w, r, http.StatusUnauthorized, kit.T(r, "auth.totp_invalid_code", nil))
			return
		case answer.Bool("token"):
			h.finishSecondFactor(w, r, who, "totp (Go delegated)")
			return
		}
	}

	verified, method := false, "totp"
	var err error
	switch {
	case recoveryCode.Truthy():
		method = "recovery"
		verified, err = h.verifyRecoveryCode(r, userID, recoveryCode)
	case code.Truthy():
		// A code that is not text is never the right one: the empty text
		// stands in for it
		text, _ := code.Str()
		verified, err = h.authn.VerifyTOTPCode(r.Context(), userID, text)
	}
	if err != nil {
		h.serverError(w, r, "TOTP verify error:", err)
		return
	}
	if !verified {
		countWrongCode(sess)
		if err := h.audit(r, userID, "totp_failed", "Method: "+method); err != nil {
			h.serverError(w, r, "TOTP verify error:", err)
			return
		}
		kit.Fail(w, r, http.StatusUnauthorized, kit.T(r, "auth.totp_invalid_code", nil))
		return
	}
	h.finishSecondFactor(w, r, who, method+" (local)")
}

// verifyRecoveryCode checks a recovery code of any JSON type. A code that
// is not text is a server error once the account has codes to compare it
// with, as it was in the old console.
func (h *handlers) verifyRecoveryCode(r *http.Request, userID int64, code middleware.Value) (bool, error) {
	if text, ok := code.Str(); ok {
		return h.authn.VerifyRecoveryCode(r.Context(), userID, text)
	}
	user, err := h.authn.UserByID(r.Context(), userID)
	if err != nil {
		return false, err
	}
	if user == nil || !user.TOTPEnabled.True() || !json.Valid([]byte(user.TOTPRecoveryCodes.String)) {
		return false, nil
	}
	return false, errors.New("the recovery code is not text")
}

// pendingAccount reads the account a session waits for the second factor
// of, from the user the password step stored.
func pendingAccount(rawUser json.RawMessage) account {
	var fields struct {
		ID                json.RawMessage `json:"id"`
		Username          json.RawMessage `json:"username"`
		Role              json.RawMessage `json:"role"`
		PreferredLanguage json.RawMessage `json:"preferred_language"`
	}
	// A stored user that is no object has no fields
	_ = json.Unmarshal(rawUser, &fields)
	who := account{
		id: jsonInt(fields.ID),
		ID: fields.ID, Username: fields.Username, Role: fields.Role,
		PreferredLanguage: json.RawMessage("null"),
	}
	_ = json.Unmarshal(fields.Username, &who.username)
	_ = json.Unmarshal(fields.Role, &who.role)
	if jsonTruthy(fields.PreferredLanguage) {
		who.PreferredLanguage = fields.PreferredLanguage
		_ = json.Unmarshal(fields.PreferredLanguage, &who.language)
	}
	return who
}

// jsonTruthy is JavaScript's truth of a stored JSON value.
func jsonTruthy(raw json.RawMessage) bool {
	text := strings.TrimSpace(string(raw))
	switch text {
	case "", "null", "false", `""`:
		return false
	}
	if text[0] == '-' || (text[0] >= '0' && text[0] <= '9') {
		var f float64
		return json.Unmarshal(raw, &f) != nil || f != 0
	}
	return true
}

// jsonInt reads a users row id from JSON: a number, or a text of digits
// where the table has 64-bit ids. Anything else is 0, which no account has.
func jsonInt(raw json.RawMessage) int64 {
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(number.String()), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// finishSecondFactor signs the pending account in once its second factor
// was accepted: a new session, the sign-in noted on the account, the audit
// entries and the answer of a finished sign-in.
func (h *handlers) finishSecondFactor(w http.ResponseWriter, r *http.Request, who account, method string) {
	body := kit.Body(r)
	// The client's details are those of the password step
	var meta session.Meta
	var rawMeta json.RawMessage
	if kit.Session(r).Get(pendingMeta, &rawMeta); !jsonTruthy(rawMeta) || json.Unmarshal(rawMeta, &meta) != nil {
		meta = h.clientMeta(r, body)
	}

	replaced, err := h.establish(r, who, meta)
	if err != nil {
		h.sessionFailure(w, r, "TOTP session regeneration error:", err)
		return
	}
	h.clearGuestCookie(w)

	err = h.authn.TouchLastLogin(r.Context(), who.id)
	if err == nil {
		details := "User logged in (2FA: " + method + "); ip=" + meta.IP + "; ua=" + cutUTF16(meta.UserAgent, 160) + replacedNote(replaced)
		err = h.audit(r, who.id, "login", details)
	}
	if err == nil && replaced > 0 {
		err = h.audit(r, who.id, "session_replaced", fmt.Sprintf("2FA login replaced %d prior session(s); newIp=%s", replaced, meta.IP))
	}
	if err != nil {
		h.sessionFailure(w, r, "TOTP post-auth bookkeeping error:", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, h.signedInAnswer(who, replaced, body))
}

// pendingInTime reports whether the password step of the waiting sign-in
// was passed less than secondFactorWindow ago. A wait without that time
// is over.
func (h *handlers) pendingInTime(sess *session.Session) bool {
	var since int64
	if !sess.Get(pendingSince, &since) || since == 0 {
		return false
	}
	return h.now().UnixMilli()-since <= secondFactorWindow.Milliseconds()
}

// countWrongCode notes a refused code of the waiting sign-in. The
// maxWrongCodes-th ends the wait, so the next code needs the password
// again.
func countWrongCode(sess *session.Session) {
	var failures int
	sess.Get(pendingFailures, &failures)
	if failures++; failures >= maxWrongCodes {
		endPending(sess)
		return
	}
	sess.Set(pendingFailures, failures)
}

// endPending drops the waiting sign-in from the session.
func endPending(sess *session.Session) {
	for _, key := range []string{pendingUserID, pendingUser, pendingMeta, pendingSince, pendingFailures, partialToken} {
		sess.Delete(key)
	}
}
