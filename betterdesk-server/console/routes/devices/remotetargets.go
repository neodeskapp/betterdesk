package devices

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// RDP and VNC endpoints the server reaches for the console. The server
// keeps them and their credentials (encrypted); the routes pass the
// requests on after the device checks. A remote target is a device of
// the list too, with an id that starts with rt_, and has the device
// list's scope.

// remoteTargets answers GET /api/remote-targets?include_disabled=: the
// targets the account may see, each with its device groups.
func (h *handlers) remoteTargets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	targets, err := h.visible(r, h.api.GetAllRemoteTargets(ctx, "", jsreq.Query(r, "include_disabled") == "true"))
	if err != nil {
		h.serverError(w, r, "Get remote targets error:", err)
		return
	}
	for _, target := range targets {
		groups, err := h.groups.DeviceGroupsForPeer(ctx, idText(target))
		if err != nil {
			groups = nil
		}
		target.Set("groups", rowsOf(groups))
	}
	answer(w, r, http.StatusOK, withData{Success: true, Data: targetList{Targets: targets, Total: len(targets)}})
}

type targetList struct {
	Targets []*goapi.Object `json:"targets"`
	Total   int             `json:"total"`
}

// byResult answers with what the server said: ok when it did it, else
// refused.
func byResult(w http.ResponseWriter, r *http.Request, result *goapi.Object, ok, refused int) {
	status := refused
	if result.Bool("success") {
		status = ok
	}
	answer(w, r, status, result)
}

// createRemoteTarget answers POST /api/remote-targets: 201, or 400 with
// the server's refusal.
func (h *handlers) createRemoteTarget(w http.ResponseWriter, r *http.Request) {
	byResult(w, r, h.api.CreateRemoteTarget(r.Context(), plainBody(r)), http.StatusCreated, http.StatusBadRequest)
}

// testRemoteTarget answers POST /api/remote-targets/test: the server
// tries to reach the endpoint of the body (its guard refuses loopback,
// link-local and metadata addresses).
func (h *handlers) testRemoteTarget(w http.ResponseWriter, r *http.Request) {
	byResult(w, r, h.api.TestRemoteTarget(r.Context(), plainBody(r)), http.StatusOK, http.StatusBadRequest)
}

// remoteTargetReadiness answers GET /api/remote-targets/readiness with what
// the server says about RDP and VNC targets: {vault, guacd}, whether a
// password can be saved and whether a session can start. The target form
// warns while either is false. 502 when the server did not answer.
func (h *handlers) remoteTargetReadiness(w http.ResponseWriter, r *http.Request) {
	byResult(w, r, h.api.GetRemoteTargetReadiness(r.Context()), http.StatusOK, http.StatusBadGateway)
}

// target looks the remote target of the path up and answers 404 or 403
// when it is none or one the account may not see.
func (h *handlers) target(w http.ResponseWriter, r *http.Request, what string) (*goapi.Object, bool) {
	target := h.api.GetRemoteTarget(r.Context(), r.PathValue("id"))
	if target == nil {
		fail(w, r, http.StatusNotFound, "devices.not_found")
		return nil, false
	}
	ok, err := h.inScope(w, r, target)
	if err != nil {
		h.serverError(w, r, what, err)
		return nil, false
	}
	return target, ok
}

// remoteTarget answers GET /api/remote-targets/{id}: the target with its
// device groups.
func (h *handlers) remoteTarget(w http.ResponseWriter, r *http.Request) {
	target, ok := h.target(w, r, "Get remote target error:")
	if !ok {
		return
	}
	groups, err := h.groups.DeviceGroupsForPeer(r.Context(), idText(target))
	if err != nil {
		groups = nil
	}
	target.Set("groups", rowsOf(groups))
	answer(w, r, http.StatusOK, withData{Success: true, Data: target})
}

// updateRemoteTarget answers PATCH /api/remote-targets/{id}.
func (h *handlers) updateRemoteTarget(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.target(w, r, "Update remote target error:"); !ok {
		return
	}
	byResult(w, r, h.api.UpdateRemoteTarget(r.Context(), r.PathValue("id"), plainBody(r)), http.StatusOK, http.StatusBadRequest)
}

// remoteTargetCredentials answers GET /api/remote-targets/{id}/credentials:
// whether a password is stored, never the password. 502 when the server
// did not answer.
func (h *handlers) remoteTargetCredentials(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.target(w, r, "Get remote target credential status error:"); !ok {
		return
	}
	byResult(w, r, h.api.GetRemoteTargetCredentialStatus(r.Context(), r.PathValue("id")), http.StatusOK, http.StatusBadGateway)
}

// setRemoteTargetCredentials answers PUT /api/remote-targets/{id}/credentials
// with {username, password}: the server encrypts and stores them.
func (h *handlers) setRemoteTargetCredentials(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.target(w, r, "Set remote target credentials error:"); !ok {
		return
	}
	byResult(w, r, h.api.SetRemoteTargetCredentials(r.Context(), r.PathValue("id"), plainBody(r)), http.StatusOK, http.StatusBadRequest)
}

// clearRemoteTargetCredentials answers DELETE /api/remote-targets/{id}/credentials.
func (h *handlers) clearRemoteTargetCredentials(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.target(w, r, "Clear remote target credentials error:"); !ok {
		return
	}
	byResult(w, r, h.api.ClearRemoteTargetCredentials(r.Context(), r.PathValue("id")), http.StatusOK, http.StatusBadRequest)
}

// acceptRemoteTargetCertificate answers POST
// /api/remote-targets/{id}/certificate/accept with {fingerprint}.
func (h *handlers) acceptRemoteTargetCertificate(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.target(w, r, "Accept remote target certificate error:"); !ok {
		return
	}
	fingerprint, err := textOr(value(r, "fingerprint"), "")
	if err != nil {
		h.serverError(w, r, "Accept remote target certificate error:", err)
		return
	}
	byResult(w, r, h.api.AcceptRemoteTargetCertificate(r.Context(), r.PathValue("id"), fingerprint), http.StatusOK, http.StatusBadRequest)
}

// deleteRemoteTarget answers DELETE /api/remote-targets/{id}.
func (h *handlers) deleteRemoteTarget(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.target(w, r, "Delete remote target error:"); !ok {
		return
	}
	byResult(w, r, h.api.DeleteRemoteTarget(r.Context(), r.PathValue("id")), http.StatusOK, http.StatusBadRequest)
}
