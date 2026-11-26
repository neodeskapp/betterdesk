package users

import (
	"crypto/rand"
	"encoding/json"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/authn"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsreq"
	"github.com/neodeskapp/neodesk/neodesk-server/console/securitymail"
)

// The sign-in security of other accounts (permission user.security): a
// temporary password, a new password required at the next sign-in, the
// end of a lockout, two-factor sign-in required of one account, reset or
// turned off, and recovery codes made unusable. Each is audited about the
// account, and the person gets a mail when their credentials changed
// (securitymail). An admin's own account is changed on Your account.

// denial is a refusal of a change of an account's sign-in security: the
// status and the text of the answer.
type denial struct {
	status int
	text   string
}

// deny answers a denial.
func deny(w http.ResponseWriter, r *http.Request, d *denial) {
	fail(w, r, d.status, d.text)
}

// securityTarget reads the account of the path for a change of its
// sign-in security: it must exist, be one the caller may change
// (mayChange) and, unless own is allowed, not be the caller's own.
func (h *handlers) securityTarget(w http.ResponseWriter, r *http.Request, own bool) (*account, bool) {
	userID, ok := pathID(r)
	if !ok {
		invalidID(w, r)
		return nil, false
	}
	user, err := accountByID(r.Context(), h.d.Store, userID)
	if err != nil {
		h.serverError(w, r, "Account security error:", err)
		return nil, false
	}
	if user == nil {
		failKey(w, r, http.StatusNotFound, "users.not_found")
		return nil, false
	}
	if d, err := h.securityDenial(r, user, own); err != nil {
		h.serverError(w, r, "Account security error:", err)
		return nil, false
	} else if d != nil {
		deny(w, r, d)
		return nil, false
	}
	return user, true
}

// securityDenial refuses a change of the account's sign-in security, nil
// when the caller may make it.
func (h *handlers) securityDenial(r *http.Request, user *account, own bool) (*denial, error) {
	if !own && isSessionAccount(r, user.ID.V) {
		return &denial{http.StatusBadRequest, kit.T(r, "users.security_own", nil)}, nil
	}
	ok, err := h.mayChange(r, user)
	if err != nil {
		return nil, err
	}
	if !ok {
		return &denial{http.StatusForbidden, "Cannot assign a role higher than your own"}, nil
	}
	return nil, nil
}

// externalPassword refuses a password feature for an account that signs
// in with a directory or single sign-on, naming it; nil for a local
// account.
func externalPassword(r *http.Request, user *account) *denial {
	if isLocalAccount(user) {
		return nil
	}
	return &denial{http.StatusBadRequest, kit.T(r, "users.external_password", kit.Vars("provider", providerName(r, user)))}
}

// providerName is how the console names the account's provider.
func providerName(r *http.Request, user *account) string {
	provider, _ := providerOf(user)
	switch provider {
	case "ldap":
		return kit.T(r, "users.provider_ldap", nil)
	case "oidc":
		return kit.T(r, "users.provider_oidc_name", nil)
	}
	return provider
}

// ---- Temporary password ----

// temporaryAlphabet is what a made-up temporary password is written with:
// no letters or digits that look alike.
const temporaryAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"

// newTemporaryPassword is four groups of four random characters joined
// by dashes, about 92 bits.
func newTemporaryPassword() (string, error) {
	var b strings.Builder
	max := big.NewInt(int64(len(temporaryAlphabet)))
	for i := 0; i < 16; i++ {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b.WriteByte(temporaryAlphabet[n.Int64()])
	}
	return b.String(), nil
}

type temporaryAnswer struct {
	Success bool `json:"success"`
	Data    struct {
		Password string `json:"password"`
	} `json:"data"`
}

// temporaryPassword answers POST /api/users/{id}/temporary-password with
// the body {password?}: the account gets the password, or one made up
// when none is given, and must choose a new one at its next sign-in. The
// answer holds the password, to be shown this once. Every session and
// access token of the account ends, and a reset link sent before stops
// working. Not for the caller's own account, nor for a directory or
// single sign-on account.
func (h *handlers) temporaryPassword(w http.ResponseWriter, r *http.Request) {
	user, ok := h.securityTarget(w, r, false)
	if !ok {
		return
	}
	if d := externalPassword(r, user); d != nil {
		deny(w, r, d)
		return
	}
	ctx := r.Context()
	given := member(kit.Body(r), "password")
	password := ""
	switch {
	case given.IsUndefined() || given.IsNull() || given.IsString() && given.Str() == "":
		var err error
		if password, err = newTemporaryPassword(); err != nil {
			h.serverError(w, r, "Temporary password error:", err)
			return
		}
	case !given.IsString():
		failKey(w, r, http.StatusBadRequest, "users.password_required")
		return
	default:
		if refusal := passwordRefusal(r, given); refusal != nil {
			sent(w, r, refusal)
			return
		}
		password = given.Str()
	}
	hash, err := h.d.Authn.HashPassword(ctx, password)
	if err == nil {
		err = updatePasswordHash(ctx, h.d.Store, user.ID.V, hash)
	}
	if err == nil {
		h.d.Authn.FirstPasswordReset(user.username())
		err = h.d.Authn.SetMustChangePassword(ctx, user.ID.V, true)
	}
	if err == nil {
		err = h.endSessions(r, user.ID.V, false)
	}
	if err == nil {
		err = h.links.Revoke(ctx, user.ID.V)
	}
	if err == nil {
		err = h.d.LogAccountAction(r, user.ID.V, "temporary_password_set", "Set a temporary password for user: "+user.username())
	}
	if err != nil {
		h.serverError(w, r, "Temporary password error:", err)
		return
	}
	h.notices.PasswordChanged(ctx, user.ID.V, securitymail.PasswordTemporary)
	out := temporaryAnswer{Success: true}
	out.Data.Password = password
	answer(w, r, http.StatusOK, out)
}

// ---- New password at the next sign-in, for several accounts ----

// maxBulkAccounts is the most accounts one request names.
const maxBulkAccounts = 500

// bulkResult is what happened to one account of a request for several.
type bulkResult struct {
	ID       any    `json:"id"`
	Username string `json:"username,omitempty"`
	// Outcome is "changed", "unchanged" (it was so already) or "refused",
	// with the reason in Error.
	Outcome string `json:"outcome"`
	Error   string `json:"error,omitempty"`
}

// bulkIDs reads the body's "ids": a list of account ids, as numbers or as
// text of digits, 1 to maxBulkAccounts of them.
func bulkIDs(r *http.Request) ([]jsbind.Value, error) {
	given := member(kit.Body(r), "ids")
	list := given.Items()
	if !given.IsArray() || len(list) == 0 || len(list) > maxBulkAccounts {
		return nil, sayKey(r, http.StatusBadRequest, "users.bulk_ids_required")
	}
	return list, nil
}

// bulkID is an id of the list as a number, false for one that is none.
func bulkID(v jsbind.Value) (float64, bool) {
	switch {
	case v.IsNumber():
		n := v.ToNumber()
		return n, n > 0 && n == float64(int64(n))
	case v.IsString():
		n, ok := jsreq.ParseInt(v.Str(), 10)
		return n, ok && n > 0 && jsreq.NumberString(n) == strings.TrimSpace(v.Str())
	}
	return 0, false
}

// requirePasswordChange answers POST /api/users/require-password-change
// with the body {ids, required?}: each local account of the list must
// choose a new password at its next sign-in (required true, the default),
// or no longer (false). Its sessions go on until then. The answer says
// for each id what happened, in the order given: changed, unchanged, or
// refused with the reason (an unknown account, one the caller may not
// change, the caller's own, a directory or single sign-on account).
func (h *handlers) requirePasswordChange(w http.ResponseWriter, r *http.Request) {
	ids, err := bulkIDs(r)
	if err != nil {
		sent(w, r, err)
		return
	}
	required := true
	if v := member(kit.Body(r), "required"); !v.IsUndefined() {
		switch v.JSON() {
		case "true":
		case "false":
			required = false
		default:
			fail(w, r, http.StatusBadRequest, "Invalid value for required")
			return
		}
	}
	ctx := r.Context()
	policies, err := h.d.Authn.AccountPolicies(ctx)
	if err != nil {
		h.serverError(w, r, "Require password change error:", err)
		return
	}
	results := make([]bulkResult, 0, len(ids))
	for _, raw := range ids {
		res := bulkResult{ID: json.RawMessage(raw.JSON()), Outcome: "refused"}
		results = append(results, res)
		last := &results[len(results)-1]
		id, ok := bulkID(raw)
		if !ok {
			last.Error = "Invalid user ID"
			continue
		}
		user, err := accountByID(ctx, h.d.Store, id)
		if err != nil {
			h.serverError(w, r, "Require password change error:", err)
			return
		}
		if user == nil {
			last.Error = kit.T(r, "users.not_found", nil)
			continue
		}
		last.ID, last.Username = user.ID, user.username()
		d, err := h.securityDenial(r, user, false)
		if err != nil {
			h.serverError(w, r, "Require password change error:", err)
			return
		}
		if d == nil {
			d = externalPassword(r, user)
		}
		if d != nil {
			last.Error = d.text
			continue
		}
		if policies[user.ID.V].MustChangePassword == required {
			last.Outcome = "unchanged"
			continue
		}
		if err := h.d.Authn.SetMustChangePassword(ctx, user.ID.V, required); err != nil {
			h.serverError(w, r, "Require password change error:", err)
			return
		}
		action, details := "password_change_required", "Required a new password at the next sign-in of user: "
		if !required {
			action, details = "password_change_requirement_removed", "No longer requires a new password of user: "
		}
		if err := h.d.LogAccountAction(r, user.ID.V, action, details+user.username()); err != nil {
			h.serverError(w, r, "Require password change error:", err)
			return
		}
		last.Outcome = "changed"
	}
	answer(w, r, http.StatusOK, struct {
		Success bool `json:"success"`
		Data    any  `json:"data"`
	}{true, map[string]any{"results": results}})
}

// ---- Unlock ----

// unlock answers POST /api/users/{id}/unlock: the account's lockouts
// after failed sign-ins end, at every address. The answer says in
// was_locked whether it was locked.
func (h *handlers) unlock(w http.ResponseWriter, r *http.Request) {
	user, ok := h.securityTarget(w, r, true)
	if !ok {
		return
	}
	ctx := r.Context()
	locks, err := h.d.Authn.AccountLocks(ctx)
	if err != nil {
		h.serverError(w, r, "Unlock error:", err)
		return
	}
	wasLocked := len(locks[user.username()]) > 0
	cleared, err := h.d.Authn.Unlock(ctx, user.username())
	if err == nil {
		details := "Unlocked user: " + user.username()
		if cleared > 0 {
			details += "; cleared " + jsbind.NumberString(float64(cleared)) + " failed sign-in(s)"
		}
		err = h.d.LogAccountAction(r, user.ID.V, "user_unlocked", details)
	}
	if err != nil {
		h.serverError(w, r, "Unlock error:", err)
		return
	}
	answer(w, r, http.StatusOK, struct {
		Success bool `json:"success"`
		Data    any  `json:"data"`
	}{true, map[string]bool{"was_locked": wasLocked}})
}

// ---- Two-factor sign-in ----

// setTwoFactorRequired answers PUT /api/users/{id}/two-factor-required
// with the body {required}: true makes the local account set up two-factor
// sign-in at its next sign-in when it has none, false leaves that to the
// server-wide switch. Its sessions go on.
func (h *handlers) setTwoFactorRequired(w http.ResponseWriter, r *http.Request) {
	user, ok := h.securityTarget(w, r, false)
	if !ok {
		return
	}
	var required bool
	switch member(kit.Body(r), "required").JSON() {
	case "true":
		required = true
	case "false":
	default:
		fail(w, r, http.StatusBadRequest, "Invalid value for required")
		return
	}
	if !isLocalAccount(user) {
		fail(w, r, http.StatusBadRequest, kit.T(r, "users.external_two_factor", kit.Vars("provider", providerName(r, user))))
		return
	}
	if err := h.d.Authn.SetTwoFactorRequiredFor(r.Context(), user.ID.V, required); err != nil {
		h.serverError(w, r, "Two-factor requirement error:", err)
		return
	}
	action, details := "two_factor_required", "Requires two-factor sign-in of user: "
	if !required {
		action, details = "two_factor_requirement_removed", "No longer requires two-factor sign-in of user: "
	}
	if err := h.d.LogAccountAction(r, user.ID.V, action, details+user.username()); err != nil {
		h.serverError(w, r, "Two-factor requirement error:", err)
		return
	}
	answer(w, r, http.StatusOK, done{Success: true})
}

// resetTwoFactor answers POST /api/users/{id}/two-factor/reset: the
// account's secret and recovery codes go, its sessions end and its access
// tokens are revoked. Where two-factor sign-in is required of the account
// (server-wide or for it), it sets it up again at its next sign-in
// (set_up_again in the answer); else it signs in with its password alone
// until it turns it on again, which is turning it off.
func (h *handlers) resetTwoFactor(w http.ResponseWriter, r *http.Request) {
	user, ok := h.securityTarget(w, r, false)
	if !ok {
		return
	}
	if !totpOn(user.TOTPEnabled) {
		failKey(w, r, http.StatusBadRequest, "users.two_factor_none")
		return
	}
	again, err := h.d.Authn.TwoFactorDue(r.Context(), user.ID.V, isLocalAccount(user))
	if err == nil {
		err = h.clearTwoFactor(r, user, again)
	}
	if err != nil {
		h.serverError(w, r, "Two-factor reset error:", err)
		return
	}
	answer(w, r, http.StatusOK, struct {
		Success bool `json:"success"`
		Data    any  `json:"data"`
	}{true, map[string]bool{"set_up_again": again}})
}

// clearTwoFactor deletes the account's secret and recovery codes, ends its
// sessions and access tokens, audits it about the account and mails the
// person: a reset when the account must set it up again, else turning it
// off.
func (h *handlers) clearTwoFactor(r *http.Request, user *account, again bool) error {
	if err := h.d.Authn.DisableTOTP(r.Context(), user.ID.V); err != nil {
		return err
	}
	if err := h.endSessions(r, user.ID.V, false); err != nil {
		return err
	}
	action, details, change := "totp_disabled_by_admin", "Turned off two-factor sign-in for user: ", securitymail.TwoFactorOffByAdmin
	if again {
		action, details, change = "two_factor_reset", "Reset two-factor sign-in for user: ", securitymail.TwoFactorReset
	}
	if err := h.d.LogAccountAction(r, user.ID.V, action, details+user.username()); err != nil {
		return err
	}
	h.notices.TwoFactorChanged(r.Context(), user.ID.V, change)
	return nil
}

// invalidateRecoveryCodes answers POST
// /api/users/{id}/recovery-codes/invalidate: the account's recovery codes
// stop working, its sessions end and its access tokens are revoked. The
// person makes new codes on Your account with their authenticator; nobody
// else ever sees them.
func (h *handlers) invalidateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	user, ok := h.securityTarget(w, r, false)
	if !ok {
		return
	}
	if !totpOn(user.TOTPEnabled) {
		failKey(w, r, http.StatusBadRequest, "users.two_factor_none")
		return
	}
	err := h.d.Authn.InvalidateRecoveryCodes(r.Context(), user.ID.V)
	if err == nil {
		err = h.endSessions(r, user.ID.V, false)
	}
	if err == nil {
		err = h.d.LogAccountAction(r, user.ID.V, "recovery_codes_invalidated", "Made the recovery codes of user "+user.username()+" stop working")
	}
	if err != nil {
		h.serverError(w, r, "Recovery codes error:", err)
		return
	}
	h.notices.TwoFactorChanged(r.Context(), user.ID.V, securitymail.RecoveryCodesInvalidate)
	answer(w, r, http.StatusOK, done{Success: true})
}

// ---- What the list says about sign-in security ----

// lockInfo is a lockout of an account at one address.
type lockInfo struct {
	IP       string `json:"ip"`
	Until    string `json:"until"`
	Failures int    `json:"failures"`
}

// signInState is what the users list says about an account's sign-in.
type signInState struct {
	// Locked says failed sign-ins locked the account (at one address or
	// more) until LockedUntil; Locks has each.
	Locked      bool       `json:"locked"`
	LockedUntil *string    `json:"locked_until"`
	Locks       []lockInfo `json:"locks"`
	// MustChangePassword: the next sign-in asks for a new password.
	MustChangePassword bool `json:"must_change_password"`
	// TwoFactorRequired is the account's own rule; TwoFactorState is
	// "on", "off" or "required" (required, by the account's rule or the
	// server's switch, and not set up yet).
	TwoFactorRequired bool   `json:"two_factor_required"`
	TwoFactorState    string `json:"two_factor_state"`
	// TwoFactorDue says a policy requires two-factor sign-in of the
	// account (its rule, or the switch for a local account): a reset makes
	// it set it up again rather than turning it off.
	TwoFactorDue bool `json:"two_factor_due"`
	// PasswordResetUntil is until when a reset link sent by mail works.
	PasswordResetUntil *string `json:"password_reset_until"`
}

// signInFacts is what signInStateOf needs, read once for the list.
type signInFacts struct {
	locks    map[string][]authn.Lock
	policies map[int64]authn.AccountPolicy
	resets   map[int64]time.Time
	// everyone says the server-wide switch requires two-factor sign-in
	// of local accounts
	everyone bool
}

func (h *handlers) readSignInFacts(r *http.Request) (signInFacts, error) {
	ctx := r.Context()
	var f signInFacts
	var err error
	if f.locks, err = h.d.Authn.AccountLocks(ctx); err != nil {
		return f, err
	}
	if f.policies, err = h.d.Authn.AccountPolicies(ctx); err != nil {
		return f, err
	}
	if f.resets, err = h.links.Resets(ctx); err != nil {
		return f, err
	}
	f.everyone, err = h.d.Authn.TwoFactorRequired(ctx)
	return f, err
}

func (f signInFacts) of(a *account) signInState {
	out := signInState{Locks: []lockInfo{}, TwoFactorState: "off"}
	for _, l := range f.locks[a.username()] {
		out.Locks = append(out.Locks, lockInfo{IP: l.IP, Until: l.Until.Format(time.RFC3339), Failures: l.Failures})
	}
	if len(out.Locks) > 0 {
		out.Locked = true
		out.LockedUntil = &out.Locks[0].Until
	}
	policy := f.policies[a.ID.V]
	local := isLocalAccount(a)
	out.MustChangePassword = local && policy.MustChangePassword
	out.TwoFactorRequired = local && policy.TwoFactorRequired
	out.TwoFactorDue = local && (policy.TwoFactorRequired || f.everyone)
	switch {
	case totpOn(a.TOTPEnabled):
		out.TwoFactorState = "on"
	case local && (policy.TwoFactorRequired || f.everyone):
		out.TwoFactorState = "required"
	}
	if until, ok := f.resets[a.ID.V]; ok {
		text := until.Format(time.RFC3339)
		out.PasswordResetUntil = &text
	}
	return out
}
