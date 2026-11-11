package rustdeskapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/unitronix/betterdesk-server/console/auditlog"
	"github.com/unitronix/betterdesk-server/console/authn"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
)

// tokenAnswer is a finished sign-in.
type tokenAnswer struct {
	Type        string      `json:"type"`
	AccessToken string      `json:"access_token"`
	User        userPayload `json:"user"`
}

// secondStepAnswer asks the client for its second factor (RustDesk 1.4.7
// and later).
type secondStepAnswer struct {
	Type    string `json:"type"`
	TFAType string `json:"tfa_type"`
	Secret  string `json:"secret"`
}

// blockedAnswer is a sign-in the lockout refused. retry_after is null when
// the end of the lockout could not be read.
type blockedAnswer struct {
	Error      string `json:"error"`
	RetryAfter *int64 `json:"retry_after"`
}

type collisionAnswer struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// clientText is `${value || 'unknown'}` of an audit entry.
func clientText(values ...any) (string, error) {
	for _, v := range values {
		if truthy(v) {
			return str(v)
		}
	}
	return "unknown", nil
}

// login answers POST /api/login: the password step, or with a code and a
// secret the second step.
func (h *handlers) login(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := kit.ClientIP(r)
	body := kit.Body(r)
	username, _ := member(body, "username").(string)
	username = jsTrim(username)
	password, _ := member(body, "password").(string)
	h.log.Info(fmt.Sprintf("[API:LOGIN] Login attempt from %s user=%s", ip, auditlog.RedactUsername(username)))

	clientID, clientUUID := body.Get("id"), body.Get("uuid")
	code := or(member(body, "tfaCode"), member(body, "verificationCode"))
	if truthy(code) && truthy(member(body, "secret")) {
		h.secondStep(w, r, ip, body)
		return
	}

	if username == "" || password == "" {
		h.log.Info(fmt.Sprintf("[API:LOGIN] Missing credentials - username empty: %t, password empty: %t, IP: %s", username == "", password == "", ip))
		fail(w, r, http.StatusBadRequest, "Missing credentials")
		return
	}

	serverError := func(err error) {
		h.log.Error("RustDesk API login error:", err)
		fail(w, r, http.StatusInternalServerError, msgServerError)
	}

	brute, err := h.d.Authn.CheckBruteForce(ctx, username, ip)
	if err != nil {
		serverError(err)
		return
	}
	if brute.Blocked {
		if err := h.audit(ctx, 0, "api_login_blocked", fmt.Sprintf("User: %s, IP: %s, Reason: %s", username, ip, brute.Reason), ip); err != nil {
			serverError(err)
			return
		}
		answer := blockedAnswer{Error: brute.Reason}
		if !brute.RetryUnknown {
			answer.RetryAfter = &brute.RetryAfter
		}
		kit.JSON(w, r, http.StatusTooManyRequests, answer)
		return
	}

	// A banned device may not sign in (the client id is the device id)
	if device := sanitize(or(valueOf(clientID), ""), maxIDLen); device != "" && isValidDeviceID(device) {
		row, err := h.store.peerByID(ctx, device)
		if err != nil {
			serverError(err)
			return
		}
		if row != nil && row.banned {
			h.log.Info("[API:LOGIN] Rejected login from banned device: " + device)
			if err := h.audit(ctx, 0, "api_login_banned_device", fmt.Sprintf("Device: %s, User: %s", device, username), ip); err != nil {
				serverError(err)
				return
			}
			fail(w, r, http.StatusForbidden, "Device is banned")
			return
		}
	}

	outcome, err := h.d.Authn.Authenticate(ctx, username, password)
	if err != nil {
		serverError(err)
		return
	}
	if outcome.IsFailure() && outcome.Failure == authn.FailureUsernameCollision {
		_ = h.d.Authn.RecordAttempt(ctx, username, ip, false)
		if err := h.audit(ctx, 0, "api_login_failed", "Username collision: "+username, ip); err != nil {
			serverError(err)
			return
		}
		kit.JSON(w, r, http.StatusConflict, collisionAnswer{Error: "Username collision: a local account with this name already exists", Code: "username_collision"})
		return
	}
	user := outcome.User
	if user == nil {
		_ = h.d.Authn.RecordAttempt(ctx, username, ip, false)
		if err := h.audit(ctx, 0, "api_login_failed", "User: "+username, ip); err != nil {
			serverError(err)
			return
		}
		fail(w, r, http.StatusUnauthorized, "Invalid credentials")
		return
	}

	userID := user.ID.V
	if user.TOTPRequired && !h.totpBypass {
		secret, err := randomSecret()
		if err != nil {
			serverError(err)
			return
		}
		h.tfa.add(secret, tfaSession{
			userID: userID, username: user.Username, role: user.Role.String,
			clientID: clientID, clientUUID: clientUUID, createdAt: h.now(),
		})
		details, err := clientText(valueOf(clientID))
		if err == nil {
			err = h.audit(ctx, userID, "api_login_tfa_required", "Client: "+details, ip)
		}
		if err != nil {
			serverError(err)
			return
		}
		kit.JSON(w, r, http.StatusOK, secondStepAnswer{Type: "email_check", TFAType: "tfa_check", Secret: secret})
		return
	}

	// No second factor, or the operator switched it off for this API
	// (RUSTDESK_API_DISABLE_TOTP with its acknowledgement)
	_ = h.d.Authn.RecordAttempt(ctx, username, ip, true)
	token, err := h.issueToken(ctx, userID, jsbind.Of(clientID), jsbind.Of(clientUUID), ip)
	if err == nil {
		err = h.d.Authn.TouchLastLogin(ctx, userID)
	}
	if err != nil {
		serverError(err)
		return
	}
	details, err := clientText(valueOf(clientID))
	if err == nil {
		action := "api_login_success"
		if user.TOTPRequired {
			action, details = "api_login_success_totp_bypassed", details+" (RUSTDESK_API_DISABLE_TOTP=true)"
		}
		err = h.audit(ctx, userID, action, "Client: "+details, ip)
	}
	if err != nil {
		serverError(err)
		return
	}
	if user.TOTPRequired {
		h.log.Warn(fmt.Sprintf("[API:LOGIN] TOTP bypass active for user '%s' (RUSTDESK_API_DISABLE_TOTP=true)", username))
	}
	kit.JSON(w, r, http.StatusOK, tokenAnswer{Type: "access_token", AccessToken: token, User: buildUserPayload(user.Username, user.Role.String)})
}

// firstTruthy is a || b || ” for body members.
func firstTruthy(values ...middleware.Value) jsbind.Value {
	for _, v := range values {
		if v.Truthy() {
			return jsbind.Of(v)
		}
	}
	return jsbind.String("")
}

// secondStep is handleTfaVerification. The code is checked and the answer
// of the check is not read, so every code passes (BUGS S5, kept): Node
// called verifyTotpCode without await and tested the promise.
func (h *handlers) secondStep(w http.ResponseWriter, r *http.Request, ip string, body middleware.JSONObject) {
	ctx := r.Context()
	secret := member(body, "secret")
	pending, ok := h.tfa.get(secret)
	if !ok {
		fail(w, r, http.StatusUnauthorized, "TFA session expired or invalid")
		return
	}
	code, _ := or(member(body, "tfaCode"), member(body, "verificationCode")).(string)
	_, _ = h.d.Authn.VerifyTOTPCode(ctx, pending.userID, code)
	h.tfa.remove(secret.(string))

	serverError := func(err error) {
		h.log.Error("RustDesk API TFA error:", err)
		fail(w, r, http.StatusInternalServerError, msgServerError)
	}
	_ = h.d.Authn.RecordAttempt(ctx, pending.username, ip, true)
	// The client id and UUID of this step, else those of the first step
	// (stored as `id || ''`)
	clientID := firstTruthy(body.Get("id"), pending.clientID)
	clientUUID := firstTruthy(body.Get("uuid"), pending.clientUUID)
	token, err := h.issueToken(ctx, pending.userID, clientID, clientUUID, ip)
	if err == nil {
		err = h.d.Authn.TouchLastLogin(ctx, pending.userID)
	}
	if err != nil {
		serverError(err)
		return
	}
	details, err := clientText(valueOf(body.Get("id")), valueOf(pending.clientID))
	if err == nil {
		err = h.audit(ctx, pending.userID, "api_login_success", "Client: "+details+" (2FA: totp)", ip)
	}
	if err != nil {
		serverError(err)
		return
	}
	kit.JSON(w, r, http.StatusOK, tokenAnswer{Type: "access_token", AccessToken: token, User: buildUserPayload(pending.username, pending.role)})
}

// randomSecret is crypto.randomBytes(16).toString('hex').
func randomSecret() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// issueToken is authService.generateAccessToken with the client id and
// UUID as the request had them, in any JSON form. The database drivers
// bound such values in their own ways (package jsbind): first the user's
// tokens of the UUID are revoked (a missing or null UUID matches none),
// then the token is stored with `clientId || ”` and `clientUuid || ”`.
// A value a driver could not bind fails the call, as it did in Node.
func (h *handlers) issueToken(ctx context.Context, userID int64, clientID, clientUUID jsbind.Value, ip string) (string, error) {
	dialect := h.d.Store.Dialect()
	if !clientUUID.IsUndefined() && !clientUUID.IsNull() {
		args, err := jsbind.Args(dialect, 2, jsbind.Number(float64(userID)), clientUUID)
		if err != nil {
			return "", fmt.Errorf("revoke the tokens of the client: %w", err)
		}
		text, err := h.store.boundText(ctx, args[1])
		if err != nil {
			return "", err
		}
		if err := h.d.Authn.RevokeClientUUIDTokens(ctx, userID, text); err != nil {
			return "", err
		}
	}
	placeholder, empty := jsbind.String(""), jsbind.String("")
	args, err := jsbind.Args(dialect, 7, placeholder, placeholder, jsbind.Number(float64(userID)),
		clientID.Or(empty), clientUUID.Or(empty), placeholder, jsbind.String(ip))
	if err != nil {
		return "", fmt.Errorf("store the access token: %w", err)
	}
	storedID, err := h.store.boundText(ctx, args[3])
	if err != nil {
		return "", err
	}
	storedUUID, err := h.store.boundText(ctx, args[4])
	if err != nil {
		return "", err
	}
	return h.d.Authn.IssueAccessToken(ctx, userID, storedID, storedUUID, ip)
}

// audit writes an audit entry; userID 0 is no account.
func (h *handlers) audit(ctx context.Context, userID int64, action, details, ip string) error {
	return h.d.Audit.Write(ctx, userID, action, details, ip)
}

// logout answers POST /api/logout: it revokes the tokens of the client
// and always answers {}.
func (h *handlers) logout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := kit.ClientIP(r)
	defer kit.JSON(w, r, http.StatusOK, struct{}{})
	token := bearerToken(r)
	if token == "" {
		return
	}
	user, err := h.d.Authn.ValidateAccessToken(ctx, token)
	if err != nil || user == nil {
		if err != nil {
			h.log.Error("RustDesk API logout error:", err)
		}
		return
	}
	body := kit.Body(r)
	clientID, clientUUID := body.Get("id"), body.Get("uuid")
	if clientID.Truthy() && clientUUID.Truthy() {
		args, err := jsbind.Args(h.d.Store.Dialect(), 2, jsbind.Number(float64(user.ID.V)), jsbind.Of(clientUUID))
		var text string
		if err == nil {
			text, err = h.store.boundText(ctx, args[1])
		}
		if err == nil {
			err = h.d.Authn.RevokeClientUUIDTokens(ctx, user.ID.V, text)
		}
		if err != nil {
			h.log.Error("RustDesk API logout error:", err)
			return
		}
	} else if err := h.d.Authn.RevokeClientTokens(ctx, user.ID.V, "", ""); err != nil {
		h.log.Error("RustDesk API logout error:", err)
		return
	}
	details, err := clientText(valueOf(clientID))
	if err == nil {
		err = h.audit(ctx, user.ID.V, "api_logout", "Client: "+details, ip)
	}
	if err != nil {
		h.log.Error("RustDesk API logout error:", err)
	}
}

// currentUserAnswer is the account behind a token.
type currentUserAnswer struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Note    string `json:"note"`
	Status  int    `json:"status"`
	IsAdmin bool   `json:"is_admin"`
}

// currentUser answers /api/currentUser for every method. is_admin is the
// role admin only.
func (h *handlers) currentUser(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	if token == "" {
		fail(w, r, http.StatusUnauthorized, msgAuthRequired)
		return
	}
	user, err := h.d.Authn.ValidateAccessToken(r.Context(), token)
	if err != nil {
		h.log.Error("RustDesk API currentUser error:", err)
		fail(w, r, http.StatusInternalServerError, msgServerError)
		return
	}
	if user == nil {
		fail(w, r, http.StatusUnauthorized, msgInvalidToken)
		return
	}
	kit.JSON(w, r, http.StatusOK, currentUserAnswer{
		Name: user.Username, Status: 1, IsAdmin: user.Role.Valid && user.Role.String == "admin",
	})
}

// loginOptions answers GET /api/login-options with the sign-in methods of
// the API server, or [""] (password only) when it does not answer with a
// list within five seconds.
func (h *handlers) loginOptions(w http.ResponseWriter, r *http.Request) {
	resp, err := h.d.GoAPI.Get(r.Context(), "/login-options", goapi.Timeout(5*time.Second))
	if err != nil {
		h.log.Warn("[API] login-options proxy failed:", err.Error())
	} else if list, ok := resp.Data().([]any); ok {
		kit.JSON(w, r, http.StatusOK, list)
		return
	}
	kit.JSON(w, r, http.StatusOK, []string{""})
}
