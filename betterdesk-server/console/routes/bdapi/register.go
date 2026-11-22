package bdapi

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// Who may register (the audit of 2026-11-13, finding C1). A device id the
// console does not know yet is admitted by one of three proofs:
//
//  1. the operator approved a LAN registration request of the id
//     (pending_registrations, status approved) and, when that request
//     carried a key, the registration brings the same key;
//  2. an enrollment token of an agent template (POST /api/bd/enroll), which
//     admits one device: the first one that redeems it;
//  3. an Ed25519 proof of possession of the key it registers (proof.go).
//
// Without one the registration is refused, unless BD_REGISTER_ALLOW_OPEN
// is true, 1 or yes. A banned or removed device is always refused. A known
// device registers again freely while its key and uuid stay; changing
// either needs a proof made with the key it has.
//
// A device admitted by a template or by the proof of its key gets its
// device token in the answer (authn.IssueServerDeviceToken), and so does a
// known device that proves its key and brings no device token of its own,
// for example after "Issue a new token" ended its token. A device admitted
// by an approval gets its token from the registration's status
// (register-status); an open registration gives none.

// refusal is the answer of a refused registration: {"error": code,
// "message": text}.
type refusal struct {
	status  int
	Error   string `json:"error"`
	Message string `json:"message"`
}

// registered is the answer of a registration.
type registered struct {
	Success           bool   `json:"success"`
	DeviceID          string `json:"device_id"`
	ServerTime        int64  `json:"server_time"`
	HeartbeatInterval int    `json:"heartbeat_interval"`
	DeviceToken       string `json:"device_token,omitempty"`
}

// renamed is the refusal of an id the signal server renamed.
type renamed struct {
	Error   string `json:"error"`
	NewID   string `json:"new_id"`
	Message string `json:"message"`
}

// register answers POST /api/bd/register with the body {device_id, uuid,
// hostname, platform, version, public_key, enrollment_token}: the device
// is written into the console's device table, online, with its info.
func (h *handlers) register(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	body := jsreq.Body(r)
	field := func(name string) any { return jsreq.Prop(body, name) }
	identified := callerOf(r).deviceID

	claimed := ""
	if s, ok := field("device_id").(string); ok {
		claimed = trim(s)
	}
	if claimed != "" && identified != claimed {
		fail(w, r, http.StatusForbidden, "device_id mismatch")
		return
	}
	// identifyDevice always names a device id of the right form, so the
	// two checks of the old route on the id itself (device_id is required,
	// invalid_device_id) never answer
	id := identified
	if claimed != "" {
		id = claimed
	}

	ctx := r.Context()
	uuid, publicKey := orValue(field("uuid"), ""), orValue(field("public_key"), "")
	check, err := checkRename(ctx, h.d.Store, id, uuid, publicKey, ip)
	if err != nil {
		h.serverError(w, r, "Register error:", err, "Registration failed")
		return
	}
	if check.reject && check.newID != "" {
		kit.JSON(w, r, http.StatusConflict, renamed{
			Error: "Device ID has been changed", NewID: check.newID,
			Message: "This device ID was renamed to " + check.newID + ". Please update your configuration.",
		})
		return
	}
	effective := id
	if check.redirectID != "" {
		effective = check.redirectID
	}

	refused, issue, err := h.registerPolicy(r, effective, uuid, publicKey)
	if err != nil {
		h.serverError(w, r, "Register error:", err, "Registration failed")
		return
	}
	if refused != nil {
		kit.JSON(w, r, refused.status, refused)
		return
	}

	info := jsreq.Stringify(goapi.NewObject(
		"hostname", orValue(field("hostname"), ""),
		"os", orValue(field("platform"), ""),
		"version", orValue(field("version"), ""),
		"ip", ip,
	))
	raw := kit.Body(r)
	err = upsertPeer(ctx, h.d.Store, effective, jsbind.Of(raw.Get("uuid")), jsbind.Of(raw.Get("public_key")), info, ip)
	if err != nil {
		h.serverError(w, r, "Register error:", err, "Registration failed")
		return
	}
	_ = updateOnlineStatus(ctx, h.d.Store, jsbind.String(effective))
	answer := registered{Success: true, DeviceID: effective, ServerTime: h.now().UnixMilli(), HeartbeatInterval: 15}
	if issue && !callerOf(r).byDeviceToken {
		token, err := h.d.Authn.IssueServerDeviceToken(ctx, effective, "Issued at the device registration")
		if err != nil {
			h.serverError(w, r, "Register error:", err, "Registration failed")
			return
		}
		answer.DeviceToken = token
	}
	kit.JSON(w, r, http.StatusOK, answer)
}

// registerPolicy is evaluateRegisterPolicy: nil when the registration may
// go on, the refusal otherwise, and whether the device's proof earns it a
// device token (a template, or the proof of its key). uuid and publicKey
// are the request's values (uuid || ”, public_key || ”).
func (h *handlers) registerPolicy(r *http.Request, id string, uuid, publicKey any) (*refusal, bool, error) {
	ctx := r.Context()
	existing, err := registrationState(ctx, h.d.Store, id)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		if existing.banned {
			return &refusal{status: http.StatusForbidden, Error: "device_banned", Message: "This device is banned"}, false, nil
		}
		if existing.deleted {
			return &refusal{status: http.StatusForbidden, Error: "device_removed", Message: "This device has been removed"}, false, nil
		}
		storedKey, hasStored := canonicalKey(orValue(existing.pk, ""))
		incomingKey, hasIncoming := canonicalKey(orValue(publicKey, ""))
		keyChanged := hasIncoming && hasStored && incomingKey != storedKey
		storedUUID := trim(existing.uuid.ToString())
		incoming, err := textOr(uuid, "")
		if err != nil {
			return nil, false, err
		}
		incoming = trim(incoming)
		// Compared as the rename guard compares them: without dashes and
		// letter case
		sameUUID, err := uuidsEqual(incoming, storedUUID)
		if err != nil {
			return nil, false, err
		}
		uuidChanged := storedUUID != "" && incoming != "" && !sameUUID
		if keyChanged || uuidChanged {
			if !hasStored {
				return &refusal{status: http.StatusConflict, Error: "identity_change_not_allowed",
					Message: "Device identity is already registered; an operator must change it"}, false, nil
			}
			if reason := h.verifyProof(r, id, storedKey); reason != "" {
				return &refusal{status: http.StatusForbidden, Error: "identity_proof_required",
					Message: "Changing device identity requires proof of possession (" + reason + ")"}, false, nil
			}
			return nil, true, nil
		}
		// A known device that signs the request with its key proves it is
		// that device.
		return nil, hasStored && hasProofHeaders(r) && h.verifyProof(r, id, storedKey) == "", nil
	}

	admission, err := h.admitted(r, id, publicKey)
	if err != nil {
		return nil, false, err
	}
	if admission != admittedNot {
		return nil, admission == admittedByTemplate, nil
	}
	if truthyValue(publicKey) && h.verifyProof(r, id, publicKey) == "" {
		return nil, true, nil
	}
	if !h.allowOpenRegister() {
		return &refusal{status: http.StatusForbidden, Error: "enrollment_proof_required",
			Message: "Registration requires operator approval, an enrollment token, or an Ed25519 proof of possession"}, false, nil
	}
	return nil, false, nil
}

// hasProofHeaders reports whether the request carries the three headers
// of a proof of possession.
func hasProofHeaders(r *http.Request) bool {
	timestamp, nonce, signature := proofHeaders(r)
	return timestamp != "" && nonce != "" && signature != ""
}

// How admitted admitted a device id the console does not know.
const (
	admittedNot = iota
	admittedByApproval
	admittedByTemplate
)

// admitted is resolveAdmissionGrant: an approval of the operator for the
// id, with the key the approver reviewed when the request carried one,
// or an enrollment token in the body that is, or now becomes, bound to
// the id. A failed lookup of the approval counts as none.
func (h *handlers) admitted(r *http.Request, id string, publicKey any) (int, error) {
	ctx := r.Context()
	if reg, err := h.regs.ByDeviceID(ctx, id); err == nil && reg != nil && reg.Get("status") == "approved" {
		reviewed, hasReviewed := canonicalKey(orValue(reg.Get("public_key"), ""))
		incoming, hasIncoming := canonicalKey(orValue(publicKey, ""))
		if !hasReviewed || hasIncoming && reviewed == incoming {
			return admittedByApproval, nil
		}
	}

	raw, err := textOr(jsreq.Prop(jsreq.Body(r), "enrollment_token"), "")
	if err != nil {
		return admittedNot, err
	}
	token := cut(trim(raw), 64)
	if token == "" || !h.redeemToken(r, token, id) {
		return admittedNot, nil
	}
	return admittedByTemplate, nil
}

// redeemToken is findByEnrollmentToken followed by redeemEnrollmentToken:
// the template of the token admits the device it was redeemed for, and a
// template not yet redeemed is redeemed for this one.
func (h *handlers) redeemToken(r *http.Request, token, deviceID string) bool {
	ctx := r.Context()
	clean := trim(token)
	if clean == "" || length(clean) > 64 {
		return false
	}
	if found, _ := enrollmentTemplate(ctx, h.d.Store, clean); !found {
		return false
	}
	// redeemEnrollmentToken checks the device and looks the template up
	// again
	device := trim(deviceID)
	if device == "" {
		return false
	}
	found, bound := enrollmentTemplate(ctx, h.d.Store, clean)
	if !found {
		return false
	}
	if bound.Truthy() {
		if boundTo := trim(bound.ToString()); boundTo != "" {
			return boundTo == device
		}
	}
	return bindTemplate(ctx, h.d.Store, clean, device)
}

// allowOpenRegister is the old escape hatch BD_REGISTER_ALLOW_OPEN, read
// on each registration.
func (h *handlers) allowOpenRegister() bool {
	raw := ""
	if h.d.Config != nil && h.d.Config.Getenv != nil {
		raw = h.d.Config.Getenv("BD_REGISTER_ALLOW_OPEN")
	}
	switch jsbind.ToLower(trim(raw)) {
	case "true", "1", "yes":
		return true
	}
	return false
}

// truthyValue is the truth of a value of the request.
func truthyValue(v any) bool { return goapi.Truthy(v) }
