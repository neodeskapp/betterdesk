package users

import (
	"net/http"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/lifecycle"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
)

// An account's end date (for contractors): PATCH /api/users/{id} with
// "expires_at", a time (RFC 3339) or a date (YYYY-MM-DD, midnight UTC),
// or null or "" for none. At that time the background job (sweep.go)
// disables the account as Disable does; the sign-in refuses it from then
// on even before the job ran. Not for the caller's own account, not in
// the past, and never the last active Super admin's.

// expiryOf reads the body's "expires_at": change says it is there.
func (h *handlers) expiryOf(r *http.Request, body middleware.JSONObject, user *account) (at time.Time, change bool, err error) {
	given := member(body, "expires_at")
	if given.IsUndefined() {
		return time.Time{}, false, nil
	}
	if given.IsNull() || (given.IsString() && strings.TrimSpace(given.Str()) == "") {
		return time.Time{}, true, nil
	}
	if !given.IsString() {
		return time.Time{}, false, sayKey(r, http.StatusBadRequest, "users.expiry_invalid")
	}
	text := strings.TrimSpace(given.Str())
	at, err = time.Parse(time.RFC3339, text)
	if err != nil {
		if at, err = time.Parse("2006-01-02", text); err != nil {
			return time.Time{}, false, sayKey(r, http.StatusBadRequest, "users.expiry_invalid")
		}
	}
	at = at.UTC()
	switch {
	case isSessionAccount(r, user.ID.V):
		return time.Time{}, false, sayKey(r, http.StatusBadRequest, "users.expiry_own")
	case !at.After(h.now()):
		return time.Time{}, false, sayKey(r, http.StatusBadRequest, "users.expiry_past")
	}
	if last, err := isLastSuperAdmin(r.Context(), h.d.Store, user.ID.V, user.role(), h.now()); err != nil {
		return time.Time{}, false, err
	} else if last {
		return time.Time{}, false, sayKey(r, http.StatusBadRequest, "users.last_super_admin")
	}
	return at, true, nil
}

// setExpiry writes the end date and notes it in the audit log.
func (h *handlers) setExpiry(r *http.Request, user *account, at time.Time) error {
	before, err := h.life.Get(r.Context(), user.ID.V)
	if err != nil {
		return err
	}
	if before.ExpiresAt.Equal(at) {
		return nil
	}
	if err := h.life.SetExpiry(r.Context(), user.ID.V, at); err != nil {
		return err
	}
	details := "Removed the end date of user " + user.username()
	if !at.IsZero() {
		details = "Set the end date of user " + user.username() + " to " + at.Format("2006-01-02 15:04") + " UTC"
	}
	return h.d.LogAccountAction(r, user.ID.V, "user_expiry_set", details)
}

// withLife adds the end date and the inactivity exception to a listed
// account.
func withLife(out *listedAccount, st lifecycle.State, now time.Time) {
	if !st.ExpiresAt.IsZero() {
		out.ExpiresAt = st.ExpiresAt.Format(time.RFC3339)
		out.Expired = !st.ExpiredAt.IsZero() || !now.Before(st.ExpiresAt)
	}
	out.InactiveExempt = st.InactiveExempt
}
