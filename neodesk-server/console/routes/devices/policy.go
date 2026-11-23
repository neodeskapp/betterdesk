package devices

import (
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/api"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsreq"
)

// The unattended access policy of a device and the connect password the
// web remote fills in, both kept by the server.

// accessPolicy answers GET /api/devices/{id}/access-policy with what the
// server answered, always with 200.
func (h *handlers) accessPolicy(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.foundOr(w, r, h.failWith(w, r, "Get access policy error:", "Failed to get access policy")); !ok {
		return
	}
	answer(w, r, http.StatusOK, h.api.GetAccessPolicy(r.Context(), r.PathValue("id")))
}

// saveAccessPolicy answers PUT /api/devices/{id}/access-policy: the body
// goes to the server as it is.
func (h *handlers) saveAccessPolicy(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.foundOr(w, r, h.failWith(w, r, "Save access policy error:", "Failed to save access policy")); !ok {
		return
	}
	answer(w, r, http.StatusOK, h.api.SaveAccessPolicy(r.Context(), r.PathValue("id"), jsreq.Body(r)))
}

// deleteAccessPolicy answers DELETE /api/devices/{id}/access-policy.
func (h *handlers) deleteAccessPolicy(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.foundOr(w, r, h.failWith(w, r, "Delete access policy error:", "Failed to delete access policy")); !ok {
		return
	}
	answer(w, r, http.StatusOK, h.api.DeleteAccessPolicy(r.Context(), r.PathValue("id")))
}

// connectPassword answers GET /api/devices/{id}/connect-password with the
// server's answer, whose password the web remote fills in; it is never
// logged. The call goes to the server as the signed-in account, not as the
// console's API key, so the server hands out a password stored for an
// organization only to its members and to the roles that see every
// organization. A device without a stored password is
// {"password_set":false} with 200, and the server's 403 is
// {"error":"forbidden"}.
func (h *handlers) connectPassword(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.foundOr(w, r, h.failWith(w, r, "Get connect password error:", "Failed to get connect password")); !ok {
		return
	}
	user := kit.User(r)
	if user == nil {
		answer(w, r, http.StatusForbidden, forbidden{Error: "forbidden"})
		return
	}
	account := api.Identity{Username: user.Username, Role: user.Role}
	data, err := h.api.GetPeerConnectPassword(r.Context(), r.PathValue("id"), goapi.As(account))
	if err == nil {
		answer(w, r, http.StatusOK, data)
		return
	}
	switch goapi.AsError(err).Status() {
	case http.StatusNotFound:
		answer(w, r, http.StatusOK, passwordUnset{})
	case http.StatusForbidden:
		answer(w, r, http.StatusForbidden, forbidden{Error: "forbidden"})
	default:
		h.log.Error("Get connect password error:", goapi.AsError(err).Message)
		answer(w, r, http.StatusInternalServerError, failure{Error: "Failed to get connect password"})
	}
}

type passwordUnset struct {
	PasswordSet bool `json:"password_set"`
}

type forbidden struct {
	Error string `json:"error"`
}

// failWith answers a failure with 500 and a fixed text, as the policy
// routes' catch did.
func (h *handlers) failWith(w http.ResponseWriter, r *http.Request, what, message string) func(err error) {
	return func(err error) {
		h.log.Error(what, err)
		answer(w, r, http.StatusInternalServerError, failure{Error: message})
	}
}
