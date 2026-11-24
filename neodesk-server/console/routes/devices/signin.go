package devices

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/admintoken"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// Saved sign-in (BUGS L469): the account name and password of a device's
// operating system account, which the browser viewer types at the
// device's lock or sign-in screen.
//
//	GET    /api/devices/{id}/signin      whether details are saved, the account
//	                                     name, the form, the switch, who and when;
//	                                     never the password (Use or Manage saved sign-in)
//	PUT    /api/devices/{id}/signin      {username, password, form}: set or replace (Manage)
//	PATCH  /api/devices/{id}/signin      {auto, form}: the switch and the form (Manage)
//	DELETE /api/devices/{id}/signin      clear (Manage)
//	POST   /api/devices/{id}/signin/use  {session}: the details for the viewer's open
//	                                     session, once (Use saved sign-in and Connect;
//	                                     a person in the browser, never an API token)
//
// The API server keeps the details sealed and decides the session (the
// relay session must run, to this device, opened by this account); this
// layer refuses API tokens for the use, writes an audit entry for every
// change, every use and every refused write or use, and writes the use's
// entry before it answers. Nothing here logs a value.

const (
	signinUse    = auth.PermDeviceSigninUse
	signinManage = auth.PermDeviceSigninManage
)

// signinPath reports whether the request is a write or the use of a saved
// sign-in: their handler checks the device scope itself, so a refusal of
// a device outside the scope is in the audit log too.
func signinPath(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return false
	}
	path := strings.TrimSuffix(kit.RoutePath(r), "/")
	return strings.HasPrefix(path, "/api/devices/") && (strings.HasSuffix(path, "/signin") || strings.HasSuffix(path, "/signin/use"))
}

// signinRefused writes the audit entry of a refused write or use.
func (h *handlers) signinRefused(r *http.Request, what, reason string) {
	via := ""
	if admintoken.FromRequest(r) != nil {
		via = " (API token)"
	}
	h.audit(r, "signin.refused", fmt.Sprintf("Saved sign-in %s on %s refused%s: %s", what, r.PathValue("id"), via, reason))
}

// signinGuard checks the permissions and the device scope of a write or
// use; it answers and audits the refusal and reports whether to go on.
func (h *handlers) signinGuard(w http.ResponseWriter, r *http.Request, what string, perms ...string) bool {
	for _, p := range perms {
		if !kit.HasPermission(r, p) {
			h.signinRefused(r, what, "no permission "+p)
			fail(w, r, http.StatusForbidden, "errors.forbidden")
			return false
		}
	}
	device := h.backend.GetDeviceByID(r.Context(), r.PathValue("id"), false)
	if device == nil {
		fail(w, r, http.StatusNotFound, "devices.not_found")
		return false
	}
	ok, err := h.groups.UserCanAccessDevice(r.Context(), kit.User(r), device)
	if err != nil {
		h.serverError(w, r, "Saved sign-in scope error:", err)
		return false
	}
	if !ok {
		h.signinRefused(r, what, "device outside the account's scope")
		fail(w, r, http.StatusForbidden, "errors.forbidden")
		return false
	}
	return true
}

// signinStatus answers GET /api/devices/{id}/signin.
func (h *handlers) signinStatus(w http.ResponseWriter, r *http.Request) {
	if !kit.HasPermission(r, signinUse) && !kit.HasPermission(r, signinManage) {
		fail(w, r, http.StatusForbidden, "errors.forbidden")
		return
	}
	if _, ok := h.foundOr(w, r, h.failWith(w, r, "Saved sign-in error:", "Failed to read the saved sign-in")); !ok {
		return
	}
	a, err := h.askAsAccount(r, http.MethodGet, "/peers/"+url.PathEscape(r.PathValue("id"))+"/signin", nil)
	if err != nil {
		h.serverError(w, r, "Saved sign-in error:", err)
		return
	}
	if a.status == http.StatusOK {
		// may_use tells the viewer whether to offer Sign in: Use saved
		// sign-in and Connect to devices, as a person (never a token)
		var st map[string]any
		if err := json.Unmarshal(a.body, &st); err == nil && st != nil {
			st["may_use"] = admintoken.FromRequest(r) == nil && kit.HasPermission(r, signinUse) && kit.HasPermission(r, auth.PermDeviceConnect)
			answer(w, r, http.StatusOK, st)
			return
		}
	}
	passOn(w, r, a)
}

// signinSet answers PUT /api/devices/{id}/signin.
func (h *handlers) signinSet(w http.ResponseWriter, r *http.Request) {
	if !h.signinGuard(w, r, "set", signinManage) {
		return
	}
	var body struct {
		Username string  `json:"username"`
		Password string  `json:"password"`
		Form     *string `json:"form,omitempty"`
	}
	if err := kit.Bind(r, &body); err != nil {
		answer(w, r, http.StatusBadRequest, failure{Error: "invalid_json"})
		return
	}
	a, err := h.askAsAccount(r, http.MethodPut, "/peers/"+url.PathEscape(r.PathValue("id"))+"/signin", body)
	body.Password = ""
	if err != nil {
		h.serverError(w, r, "Saved sign-in error:", err)
		return
	}
	if a.status == http.StatusOK {
		var st struct {
			Replaced bool   `json:"replaced"`
			Username string `json:"username"`
		}
		_ = json.Unmarshal(a.body, &st)
		action, verb := "signin.set", "set"
		if st.Replaced {
			action, verb = "signin.replace", "replaced"
		}
		h.audit(r, action, fmt.Sprintf("Saved sign-in %s on %s for account %s", verb, r.PathValue("id"), st.Username))
	} else {
		h.signinRefused(r, "set", a.code())
	}
	passOn(w, r, a)
}

// signinChange answers PATCH /api/devices/{id}/signin.
func (h *handlers) signinChange(w http.ResponseWriter, r *http.Request) {
	if !h.signinGuard(w, r, "change", signinManage) {
		return
	}
	var body struct {
		Auto *bool   `json:"auto,omitempty"`
		Form *string `json:"form,omitempty"`
	}
	if err := kit.Bind(r, &body); err != nil {
		answer(w, r, http.StatusBadRequest, failure{Error: "invalid_json"})
		return
	}
	a, err := h.askAsAccount(r, http.MethodPatch, "/peers/"+url.PathEscape(r.PathValue("id"))+"/signin", body)
	if err != nil {
		h.serverError(w, r, "Saved sign-in error:", err)
		return
	}
	if a.status == http.StatusOK {
		var parts []string
		if body.Auto != nil {
			state := "off"
			if *body.Auto {
				state = "on"
			}
			parts = append(parts, "automatic sign-in "+state)
		}
		if body.Form != nil {
			parts = append(parts, "sign-in screen form "+*body.Form)
		}
		h.audit(r, "signin.change", fmt.Sprintf("Saved sign-in on %s changed: %s", r.PathValue("id"), strings.Join(parts, ", ")))
	} else {
		h.signinRefused(r, "change", a.code())
	}
	passOn(w, r, a)
}

// signinClear answers DELETE /api/devices/{id}/signin.
func (h *handlers) signinClear(w http.ResponseWriter, r *http.Request) {
	if !h.signinGuard(w, r, "clear", signinManage) {
		return
	}
	a, err := h.askAsAccount(r, http.MethodDelete, "/peers/"+url.PathEscape(r.PathValue("id"))+"/signin", nil)
	if err != nil {
		h.serverError(w, r, "Saved sign-in error:", err)
		return
	}
	if a.status == http.StatusOK {
		h.audit(r, "signin.clear", fmt.Sprintf("Saved sign-in cleared on %s", r.PathValue("id")))
	} else {
		h.signinRefused(r, "clear", a.code())
	}
	passOn(w, r, a)
}

// sessionLabel is the start of a session's id, for the audit log.
func sessionLabel(session string) string {
	if len(session) > 8 {
		return session[:8]
	}
	return session
}

// signinUseAnswer answers POST /api/devices/{id}/signin/use: the details
// for the open session of this account, once. API tokens are refused: the
// use needs a person with the device open in the browser viewer. The
// audit entry is written before the answer, and no answer goes out when
// it can't be written.
func (h *handlers) signinUseAnswer(w http.ResponseWriter, r *http.Request) {
	if admintoken.FromRequest(r) != nil {
		h.signinRefused(r, "use", "the use needs a person in the browser viewer")
		answer(w, r, http.StatusForbidden, failure{Error: "token_not_allowed"})
		return
	}
	if !h.signinGuard(w, r, "use", signinUse, auth.PermDeviceConnect) {
		return
	}
	var body struct {
		Session string `json:"session"`
	}
	if err := kit.Bind(r, &body); err != nil {
		answer(w, r, http.StatusBadRequest, failure{Error: "invalid_json"})
		return
	}
	session := strings.TrimSpace(body.Session)
	a, err := h.askAsAccount(r, http.MethodPost, "/peers/"+url.PathEscape(r.PathValue("id"))+"/signin/use",
		map[string]any{"session": session, "account": kit.UserID(r)})
	if err != nil {
		h.serverError(w, r, "Saved sign-in error:", err)
		return
	}
	if a.status != http.StatusOK {
		h.signinRefused(r, "use", a.code()+", session "+sessionLabel(session))
		passOn(w, r, a)
		return
	}
	if err := h.d.LogAction(r, "signin.use", fmt.Sprintf("Saved sign-in handed to the viewer for %s, session %s", r.PathValue("id"), sessionLabel(session))); err != nil {
		h.log.Error("Saved sign-in audit error:", err)
		answer(w, r, http.StatusInternalServerError, failure{Error: "audit_failed"})
		return
	}
	// Written as it came, without an ETag (a hash of the body) and never
	// kept by the browser or a proxy
	h2 := w.Header()
	h2.Set("Content-Type", "application/json; charset=utf-8")
	h2.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(a.body)
}
