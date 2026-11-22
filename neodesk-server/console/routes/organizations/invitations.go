package organizations

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// Invitations. An invitation makes a sign-in for the BetterDesk client in
// its organization (an organization account): the person it is sent to
// opens its link on the web app's /invite page and picks the username and
// password. The API server keeps the invitations and only a hash of each
// code; the code is in the answer that creates the invitation, once.
//
// The page's two routes take a session or none: lookup says which
// organization a code is for (and which console account the browser is
// signed in with), redeem makes the sign-in. Both count against the
// invitation limit per address, and a code that cannot be used (unknown,
// used, expired, revoked) gets the same 404 from both, so they tell
// nothing about other invitations or accounts. Creating, revoking and
// redeeming are written to the audit log.

// invitationCode is the form of an invitation's code; anything else is
// answered as an unknown code without a call.
var invitationCode = regexp.MustCompile(`^[0-9a-f]{64}$`)

// codeOf is the body's token in lower case, "" when it is no code.
func codeOf(body any) string {
	code, _ := jsreq.Prop(body, "token").(string)
	code = strings.ToLower(strings.TrimSpace(code))
	if !invitationCode.MatchString(code) {
		return ""
	}
	return code
}

// textOf is a member of the body as text, "" when it is none.
func textOf(body any, name string) string {
	text, _ := jsreq.Prop(body, name).(string)
	return text
}

// unusable answers a code that cannot be used.
func unusable(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusNotFound, goapi.NewObject("success", false, "error", kit.T(r, "organizations.invite_invalid", nil)))
}

// apiFailure answers a failed call of the invitation routes: 404 as an
// unusable code, 400 and 409 with what the API server said, anything else
// as a server error.
func (h *handlers) apiFailure(w http.ResponseWriter, r *http.Request, what string, err error) {
	status, data := goapipath.FailureOf(err, unreachable)
	message := ""
	if body, ok := data.(*goapi.Object); ok {
		message, _ = body.Get("error").(string)
	}
	switch status {
	case http.StatusNotFound:
		unusable(w, r)
	case http.StatusConflict:
		kit.JSON(w, r, status, goapi.NewObject("success", false, "error", kit.T(r, "organizations.invite_username_taken", nil)))
	case http.StatusBadRequest:
		kit.JSON(w, r, status, goapi.NewObject("success", false, "error", message))
	default:
		h.log.Error(what, err)
		kit.JSON(w, r, http.StatusInternalServerError, goapi.NewObject("success", false, "error", kit.T(r, "errors.server_error", nil)))
	}
}

// lookupInvitation answers POST /api/invitations/lookup {token}: the
// organization's name and slug and the invitation's end, and the console
// account of the request's session (null without one).
func (h *handlers) lookupInvitation(w http.ResponseWriter, r *http.Request) {
	code := codeOf(jsreq.Body(r))
	if code == "" {
		unusable(w, r)
		return
	}
	resp, err := h.api.Request(r.Context(), http.MethodPost, "/org-invitations/lookup", goapi.Body(goapi.NewObject("token", code)))
	if err != nil {
		h.apiFailure(w, r, "Invitation lookup error:", err)
		return
	}
	data, _ := resp.Data().(*goapi.Object)
	if data == nil {
		h.apiFailure(w, r, "Invitation lookup error:", &goapi.Error{Message: "no answer"})
		return
	}
	var account any
	if user := kit.User(r); user != nil {
		account = goapi.NewObject("username", user.Username)
	}
	kit.JSON(w, r, http.StatusOK, goapi.NewObject(
		"success", true,
		"organization", goapi.NewObject("name", data.Get("org_name"), "slug", data.Get("org_slug")),
		"expires_at", data.Get("expires_at"),
		"account", account,
	))
}

// redeemInvitation answers POST /api/invitations/redeem {token, username,
// password}: 201 with the organization and the username of the new
// sign-in; 404 for a code that cannot be used, 409 for a username the
// organization has, 400 for a username or password against the rules.
func (h *handlers) redeemInvitation(w http.ResponseWriter, r *http.Request) {
	body := jsreq.Body(r)
	code := codeOf(body)
	if code == "" {
		unusable(w, r)
		return
	}
	resp, err := h.api.Request(r.Context(), http.MethodPost, "/org-invitations/redeem", goapi.Body(goapi.NewObject(
		"token", code,
		"username", textOf(body, "username"),
		"password", textOf(body, "password"),
	)))
	if err != nil {
		h.apiFailure(w, r, "Invitation redeem error:", err)
		return
	}
	data, _ := resp.Data().(*goapi.Object)
	if data == nil {
		h.apiFailure(w, r, "Invitation redeem error:", &goapi.Error{Message: "no answer"})
		return
	}
	org, _ := data.Get("org").(*goapi.Object)
	user, _ := data.Get("user").(*goapi.Object)
	if org == nil || user == nil {
		h.apiFailure(w, r, "Invitation redeem error:", &goapi.Error{Message: "no organization in the answer"})
		return
	}
	h.audit(r, "org_invitation_redeemed", "Organization "+jsreq.String(org.Get("id"))+": sign-in "+jsreq.String(user.Get("username"))+" made with invitation "+jsreq.String(data.Get("invitation_id")))
	kit.JSON(w, r, http.StatusCreated, goapi.NewObject(
		"success", true,
		"organization", goapi.NewObject("name", org.Get("name"), "slug", org.Get("slug")),
		"username", user.Get("username"),
	))
}

// invite answers POST /api/panel/org/{id}/invite with the API server's
// answer, which carries the invitation's code this once.
func (h *handlers) invite(w http.ResponseWriter, r *http.Request) {
	target, err := orgPath("/invite")(r)
	if err != nil {
		invalid(w, r, err)
		return
	}
	resp, err := h.api.Request(r.Context(), http.MethodPost, target, goapipath.Body(goapipath.RequestBody(r)))
	if err != nil {
		status, data := goapipath.FailureOf(err, unreachable)
		goapipath.WriteJSON(w, r, status, data)
		return
	}
	if inv, ok := resp.Data().(*goapi.Object); ok {
		to := jsreq.String(inv.Get("email"))
		if to == "" {
			to = "no address"
		}
		h.audit(r, "org_invitation_created", "Organization "+r.PathValue("id")+": invitation "+jsreq.String(inv.Get("id"))+" for "+to+", valid until "+jsreq.String(inv.Get("expires_at")))
	}
	goapipath.WriteJSON(w, r, resp.Status, resp.Data())
}

// revokeInvitation answers DELETE /api/panel/org/{id}/invitations/{invId}
// with the API server's answer: 204, 404 for an invitation the
// organization does not have, 409 for a used one.
func (h *handlers) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	target, err := orgItemPath("/invitations/", "invId", "invitationId")(r)
	if err != nil {
		invalid(w, r, err)
		return
	}
	resp, err := h.api.Request(r.Context(), http.MethodDelete, target)
	if err != nil {
		status, data := goapipath.FailureOf(err, unreachable)
		goapipath.WriteJSON(w, r, status, data)
		return
	}
	h.audit(r, "org_invitation_revoked", "Organization "+r.PathValue("id")+": invitation "+r.PathValue("invId")+" revoked")
	if resp.Status == http.StatusNoContent {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	goapipath.WriteJSON(w, r, resp.Status, resp.Data())
}

// audit writes an entry of the invitation routes; a failure is logged.
func (h *handlers) audit(r *http.Request, action, details string) {
	if h.logAction == nil {
		return
	}
	if err := h.logAction(r, action, details); err != nil {
		h.log.Error("Audit entry not written:", err)
	}
}
