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

	refused, err := h.registerPolicy(r, effective, uuid, publicKey)
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
	kit.JSON(w, r, http.StatusOK, registered{Success: true, DeviceID: effective, ServerTime: h.now().UnixMilli(), HeartbeatInterval: 15})
}

// registerPolicy is evaluateRegisterPolicy: nil when the registration may
// go on, the refusal otherwise. uuid and publicKey are the request's
// values (uuid || ”, public_key || ”).
func (h *handlers) registerPolicy(r *http.Request, id string, uuid, publicKey any) (*refusal, error) {
	ctx := r.Context()
	existing, err := registrationState(ctx, h.d.Store, id)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.banned {
			return &refusal{status: http.StatusForbidden, Error: "device_banned", Message: "This device is banned"}, nil
		}
		if existing.deleted {
			return &refusal{status: http.StatusForbidden, Error: "device_removed", Message: "This device has been removed"}, nil
		}
		storedKey, hasStored := canonicalKey(orValue(existing.pk, ""))
		incomingKey, hasIncoming := canonicalKey(orValue(publicKey, ""))
		keyChanged := hasIncoming && hasStored && incomingKey != storedKey
		storedUUID := trim(existing.uuid.ToString())
		incoming, err := textOr(uuid, "")
		if err != nil {
			return nil, err
		}
		incoming = trim(incoming)
		uuidChanged := storedUUID != "" && incoming != "" && incoming != storedUUID
		if keyChanged || uuidChanged {
			if !hasStored {
				return &refusal{status: http.StatusConflict, Error: "identity_change_not_allowed",
					Message: "Device identity is already registered; an operator must change it"}, nil
			}
			if reason := h.verifyProof(r, id, storedKey); reason != "" {
				return &refusal{status: http.StatusForbidden, Error: "identity_proof_required",
					Message: "Changing device identity requires proof of possession (" + reason + ")"}, nil
			}
		}
		return nil, nil
	}

	admitted, err := h.admitted(r, id, publicKey)
	if err != nil || admitted {
		return nil, err
	}
	if truthyValue(publicKey) && h.verifyProof(r, id, publicKey) == "" {
		return nil, nil
	}
	if !h.allowOpenRegister() {
		return &refusal{status: http.StatusForbidden, Error: "enrollment_proof_required",
			Message: "Registration requires operator approval, an enrollment token, or an Ed25519 proof of possession"}, nil
	}
	return nil, nil
}

// admitted is resolveAdmissionGrant: an approval of the operator for the
// id, with the key the approver reviewed when the request carried one,
// or an enrollment token in the body that is, or now becomes, bound to
// the id. A failed lookup of the approval counts as none.
func (h *handlers) admitted(r *http.Request, id string, publicKey any) (bool, error) {
	ctx := r.Context()
	if reg, err := h.regs.ByDeviceID(ctx, id); err == nil && reg != nil && reg.Get("status") == "approved" {
		reviewed, hasReviewed := canonicalKey(orValue(reg.Get("public_key"), ""))
		incoming, hasIncoming := canonicalKey(orValue(publicKey, ""))
		if !hasReviewed || hasIncoming && reviewed == incoming {
			return true, nil
		}
	}

	raw, err := textOr(jsreq.Prop(jsreq.Body(r), "enrollment_token"), "")
	if err != nil {
		return false, err
	}
	token := cut(trim(raw), 64)
	if token == "" {
		return false, nil
	}
	return h.redeemToken(r, token, id), nil
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
