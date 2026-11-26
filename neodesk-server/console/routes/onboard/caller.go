package onboard

import (
	"net/http"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/onboard"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
)

// callerAnswer is POST /api/support/session/caller when the server
// vouches for the caller.
type callerAnswer struct {
	Success  bool   `json:"success"`
	Vouched  bool   `json:"vouched"`
	DeviceID string `json:"device_id"`
	Session  string `json:"session"`
	Helper   string `json:"helper"`
}

// sessionLabel is the start of a relay session's id, for the audit log.
func sessionLabel(session string) string {
	if len(session) > 8 {
		return session[:8]
	}
	return session
}

// appCaller answers POST /api/support/session/caller with the body
// {session}: the quick support app got a connection without the session
// password through the relay session it names, and asks whether a
// signed-in helper of the console opened it (A271). The server vouches
// only when the person allowed quick support, the relay session runs to
// the session's device from the browser viewer of a signed-in console
// session and met on the relay less than a minute ago, and the account
// may connect to the device. It vouches once per relay session, names the
// helper as the console knows them and writes the audit entry before it
// answers. Anything else is 409 not_vouched with the reason, and the app
// asks for the password as before. Wire format: PROTOCOL.md.
func (h *handlers) appCaller(w http.ResponseWriter, r *http.Request) {
	sess := appSessionOf(r)
	var body struct {
		Session string `json:"session"`
	}
	if err := kit.Bind(r, &body); err != nil {
		appFail(w, r, http.StatusBadRequest, "invalid_body", "")
		return
	}
	relaySession := strings.TrimSpace(body.Session)
	if relaySession == "" || len(relaySession) > 128 {
		appFail(w, r, http.StatusBadRequest, "invalid_body", "")
		return
	}
	notVouched := func(reason string) {
		kit.JSON(w, r, http.StatusConflict, refusal{Error: "not_vouched", Reason: reason})
	}
	if sess.Status != onboard.StatusAllowed || sess.DeviceID == "" {
		notVouched("session_state")
		return
	}
	ctx := r.Context()
	account, refused, err := h.sessions.CallerAccount(ctx, sess.DeviceID, relaySession)
	if err != nil {
		h.log.Error("Quick support caller:", err)
		appFail(w, r, http.StatusInternalServerError, "server_error", "")
		return
	}
	if refused != "" {
		notVouched(refused)
		return
	}
	// The entries are about the helper; the quick support app asked, no
	// account did it (L341)
	refuseAccount := func(why string) {
		if h.d.Audit != nil {
			details := "Quick support session " + sess.ID + " of " + sess.CreatedBy + ": the server did not vouch for the caller of relay session " +
				sessionLabel(relaySession) + " to device " + sess.DeviceID + ": " + why
			if err := h.d.Audit.WriteAbout(ctx, 0, account, "quick_support_caller_refused", details, kit.ClientIP(r)); err != nil {
				h.log.Warn("Audit entry quick_support_caller_refused failed:", err)
			}
		}
		notVouched("no_permission")
	}
	if h.d.Authn == nil || h.d.Access == nil {
		notVouched("no_permission")
		return
	}
	acct, err := h.d.Authn.UserByID(ctx, account)
	if err != nil {
		h.log.Error("Quick support caller account:", err)
		appFail(w, r, http.StatusInternalServerError, "server_error", "")
		return
	}
	if acct == nil || acct.Disabled.True() {
		refuseAccount("the account is gone or disabled")
		return
	}
	role := acct.Role.String
	may, err := h.d.Access.RoleHasEffectivePermission(ctx, role, auth.PermDeviceConnect)
	if err != nil {
		h.log.Error("Quick support caller permission:", err)
		appFail(w, r, http.StatusInternalServerError, "server_error", "")
		return
	}
	if !may {
		refuseAccount(acct.Username + " may not connect to devices")
		return
	}
	if h.d.DeviceGroups != nil && h.d.Backend != nil {
		device := h.d.Backend.GetDeviceByID(ctx, sess.DeviceID, false)
		inScope, err := h.d.DeviceGroups.UserCanAccessDevice(ctx, &session.User{ID: account, Username: acct.Username, Role: role}, device)
		if err != nil {
			h.log.Error("Quick support caller scope:", err)
			appFail(w, r, http.StatusInternalServerError, "server_error", "")
			return
		}
		if !inScope {
			refuseAccount("device " + sess.DeviceID + " is outside the scope of " + acct.Username)
			return
		}
	}
	name := h.personName(ctx, account, acct.Username)
	if h.d.Audit != nil {
		details := "Quick support session " + sess.ID + " of " + sess.CreatedBy + ": the server vouched to device " + sess.DeviceID +
			" that " + name + " (" + acct.Username + ") is the caller of relay session " + sessionLabel(relaySession) +
			", so the person may accept by click"
		if err := h.d.Audit.WriteAbout(ctx, 0, account, "quick_support_caller", details, kit.ClientIP(r)); err != nil {
			// No proof goes out that the log does not hold
			h.log.Error("Quick support caller audit:", err)
			appFail(w, r, http.StatusInternalServerError, "audit_failed", "")
			return
		}
	}
	kit.JSON(w, r, http.StatusOK, callerAnswer{Success: true, Vouched: true, DeviceID: sess.DeviceID, Session: relaySession, Helper: name})
}
