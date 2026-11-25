package auth

import (
	"errors"
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/authn"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
)

// refusalKeys are the texts of the sign-in service's refusals in the
// language files; their English is the refusal's own text.
var refusalKeys = map[authn.Refusal]string{
	authn.ErrUserNotFound:     "users.not_found",
	authn.ErrWrongPassword:    "auth.current_password_wrong",
	authn.ErrPasswordTooShort: "auth.password_min_length",
	authn.ErrPasswordTooLong:  "auth.password_max_length",
	authn.ErrTOTPNotSetUp:     "auth.totp_not_set_up",
	authn.ErrInvalidTOTPCode:  "auth.totp_code_invalid",
}

// refuse answers 400 with the text of a refusal of the sign-in service in
// the request's language.
func refuse(w http.ResponseWriter, r *http.Request, refusal authn.Refusal) {
	if key, ok := refusalKeys[refusal]; ok {
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, key, nil))
		return
	}
	kit.Fail(w, r, http.StatusBadRequest, string(refusal))
}

// strictEqual is a === b for two values of a JSON body. Two lists or two
// objects are never the same value.
func strictEqual(a, b middleware.Value) bool {
	if a.Kind() != b.Kind() {
		return false
	}
	switch a.Kind() {
	case middleware.KindUndefined, middleware.KindNull:
		return true
	case middleware.KindBool:
		return a.Truthy() == b.Truthy()
	case middleware.KindNumber:
		x, _ := a.Float()
		y, _ := b.Float()
		return x == y
	case middleware.KindString:
		x, _ := a.Str()
		y, _ := b.Str()
		return x == y
	}
	return false
}

// changePassword answers POST /api/auth/password with the body
// {currentPassword, newPassword, confirmPassword}: the signed-in account
// changes its own password. Its other sessions end and its access tokens
// are revoked; the session that changed it goes on.
func (h *handlers) changePassword(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	current, next := body.Get("currentPassword"), body.Get("newPassword")
	if !current.Truthy() || !next.Truthy() {
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "auth.password_required", nil))
		return
	}
	if !strictEqual(next, body.Get("confirmPassword")) {
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "auth.passwords_mismatch", nil))
		return
	}
	// A directory or single sign-on account signs in with the password of
	// its directory or provider; one stored here would never be asked for
	user, err := h.authn.UserByID(r.Context(), kit.UserID(r))
	if err != nil {
		h.serverError(w, r, "Password change error:", err)
		return
	}
	if authn.IsExternalAccount(user) {
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "auth.password_external_account", nil))
		return
	}

	err = h.replacePassword(r, current, next)
	var refusal authn.Refusal
	switch {
	case errors.As(err, &refusal):
		refuse(w, r, refusal)
		return
	case err == nil:
		err = h.endOtherSessions(r)
	}
	if err == nil {
		// A link sent before would replace the new password
		err = h.links.Revoke(r.Context(), kit.UserID(r))
	}
	if err == nil {
		err = h.audit(r, kit.UserID(r), "password_changed", "Password changed")
	}
	if err != nil {
		h.serverError(w, r, "Password change error:", err)
		return
	}
	kit.OK(w, r, nil)
}

// replacePassword changes the password of the signed-in account. A current
// password that is not text is a wrong one, and a new one that is not text
// is refused, once the current one was accepted, as one too short.
func (h *handlers) replacePassword(r *http.Request, current, next middleware.Value) error {
	userID := kit.UserID(r)
	currentText, currentIsText := current.Str()
	nextText, nextIsText := next.Str()
	if currentIsText && nextIsText {
		return h.authn.ChangePassword(r.Context(), userID, currentText, nextText)
	}
	user, err := h.authn.UserByID(r.Context(), userID)
	if err != nil {
		return err
	}
	if user == nil {
		return authn.ErrUserNotFound
	}
	if !currentIsText {
		return authn.ErrWrongPassword
	}
	valid, err := h.authn.VerifyPassword(r.Context(), currentText, user.PasswordHash.String)
	if err != nil {
		return err
	}
	if !valid {
		return authn.ErrWrongPassword
	}
	return authn.ErrPasswordTooShort
}

// endOtherSessions ends the signed-in account's sessions but the one of
// the request, and revokes the account's access tokens.
func (h *handlers) endOtherSessions(r *http.Request) error {
	userID := kit.UserID(r)
	if _, err := h.sessions.EndAccount(r.Context(), userID, kit.Session(r).ID()); err != nil {
		return err
	}
	return h.authn.RevokeClientTokens(r.Context(), userID, "", "")
}
