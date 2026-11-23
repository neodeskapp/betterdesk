package devices

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsreq"
)

// Changes to one device. The server keeps the device; the console's own
// copy (peer) and its side tables follow where the old console wrote
// them.

// trimmedField is the member as String(v).trim().slice(0, n), or not set
// when the body does not have it.
func trimmedField(r *http.Request, name string, n int) (string, bool, error) {
	v, ok := field(r, name)
	if !ok {
		return "", false, nil
	}
	s, err := text(v)
	if err != nil {
		return "", false, err
	}
	return slice(jsTrim(s), n), true, nil
}

// update answers PATCH /api/devices/{id} with {user, note, display_name}:
// each that the body has is written, trimmed and cut (user and display
// name to 128 characters, the note to 512).
func (h *handlers) update(w http.ResponseWriter, r *http.Request) {
	fields := goapi.NewObject()
	for _, f := range []struct {
		name string
		max  int
	}{{"user", 128}, {"note", 512}, {"display_name", 128}} {
		s, ok, err := trimmedField(r, f.name, f.max)
		if err != nil {
			h.serverError(w, r, "Update device error:", err)
			return
		}
		if ok {
			fields.Set(f.name, s)
		}
	}
	if _, ok := h.found(w, r, "Update device error:"); !ok {
		return
	}
	id := r.PathValue("id")
	result := h.backend.UpdateDevice(r.Context(), id, fields)
	if goapi.Truthy(result.Get("error")) {
		answer(w, r, http.StatusBadGateway, failure{Error: result.Get("error")})
		return
	}
	if err := h.d.LogAction(r, "device_updated", "Device "+id+" updated"); err != nil {
		h.log.Warn("Device update audit log failed:", err)
	}
	answer(w, r, http.StatusOK, withData{Success: true, Data: changes{Changes: changesOf(result)}})
}

type changes struct {
	Changes any `json:"changes"`
}

// changesOf is result?.changes ?? 1.
func changesOf(result *goapi.Object) any {
	if v := result.Get("changes"); v != nil {
		return v
	}
	return float64(1)
}

// remove answers DELETE /api/devices/{id}?revoke=&cascade=&hard=: a soft
// delete; revoke also blocks the device and drops its connections,
// cascade deletes the devices linked to it (refused with 403 when the
// account may not see one of them; one linked after that check stays),
// hard deletes it for good
// (also one that is soft-deleted already) and clears what the console
// keeps of it. Every device the server deleted loses its device tokens.
func (h *handlers) remove(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	opts := goapi.DeleteOptions{
		Revoke:  jsreq.Query(r, "revoke") == "true",
		Cascade: jsreq.Query(r, "cascade") == "true",
		Hard:    jsreq.Query(r, "hard") == "true",
	}
	device := h.backend.GetDeviceByID(ctx, id, false)
	if device == nil && opts.Hard {
		device = h.backend.GetDeviceByID(ctx, id, true)
	}
	if device == nil {
		fail(w, r, http.StatusNotFound, "devices.not_found")
		return
	}
	if ok, err := h.inScope(w, r, device); err != nil || !ok {
		if err != nil {
			h.serverError(w, r, "Delete device error:", err)
		}
		return
	}
	if opts.Cascade {
		linked, ok, err := h.linkedInScope(r, id)
		if err != nil {
			h.serverError(w, r, "Delete device error:", err)
			return
		}
		if !ok {
			fail(w, r, http.StatusForbidden, "errors.forbidden")
			return
		}
		opts.CascadeOnly = linked
	}
	result := h.backend.DeleteDevice(ctx, id, opts)
	if !result.Bool("success") {
		answer(w, r, http.StatusInternalServerError, failure{Error: or(result.Get("error"), kit.T(r, "devices.delete_failed", nil))})
		return
	}
	// The server names the devices it deleted with this one
	cascaded := or(dig(result.Get("data"), "cascaded"), []any{})
	gone := []string{id}
	if list, ok := cascaded.([]any); ok {
		for _, item := range list {
			if s, isText := item.(string); isText && s != "" {
				gone = append(gone, s)
			}
		}
	}
	// A deleted device keeps no device token, also when it is only in the
	// bin: a restored one registers again
	h.revokeDeviceTokens(r, gone...)
	// A soft-deleted device can come back and keeps what the console holds
	// of it; after a hard delete there is nothing to come back to, also for
	// the devices deleted with it
	if opts.Hard {
		for _, deviceID := range gone {
			if err := clearDeletedDevice(ctx, h.d.Store, deviceID); err != nil {
				h.log.Warn("Clean-up after a device delete failed:", err)
			}
		}
	}
	action, details := "device_deleted", "Device "+id+" deleted"
	switch {
	case opts.Revoke:
		action, details = "device_revoked", "Device "+id+" revoked (blocklist + disconnect)"
		if opts.Cascade {
			details += " + cascade"
		}
	case opts.Hard:
		details += " permanently"
	}
	if err := h.d.LogAction(r, action, details); err != nil {
		h.serverError(w, r, "Delete device error:", err)
		return
	}
	answer(w, r, http.StatusOK, deleted{Success: true, Revoked: opts.Revoke, Hard: opts.Hard, Cascaded: cascaded})
}

// linkedInScope reports whether the account may see every device linked
// to the device id, which a delete with cascade deletes with it, and
// returns the ids of those devices: the server's cascade deletes none
// other, also when a device is linked after this lookup. The roles that
// see every device pass without a lookup and get nil, which lets the
// cascade delete every linked device; a list of linked devices that
// cannot be read is an error.
func (h *handlers) linkedInScope(r *http.Request, id string) ([]string, bool, error) {
	if user := kit.User(r); user == nil || access.IsAdminRole(user.Role) {
		return nil, true, nil
	}
	result := h.api.GetLinkedPeers(r.Context(), id)
	linked, isList := dig(result.Get("data"), "linked").([]any)
	if !result.Bool("success") || !isList {
		return nil, false, fmt.Errorf("read the devices linked to %s: %v", id, result.Get("error"))
	}
	ids := make([]string, len(linked))
	for i, item := range linked {
		device, _ := item.(*goapi.Object)
		if ids[i] = idText(device); ids[i] == "" {
			return nil, false, fmt.Errorf("a device linked to %s has no id", id)
		}
	}
	ok, err := h.allInScope(r, ids)
	return ids, ok, err
}

// revokeDeviceTokens revokes the device tokens of devices the server
// deleted or banned, so that none of them keeps the device routes and
// sockets. A revocation that fails is logged; the server's change stands.
func (h *handlers) revokeDeviceTokens(r *http.Request, ids ...string) {
	for _, id := range ids {
		if err := h.d.Authn.RevokeDeviceTokens(r.Context(), id); err != nil {
			h.log.Error("Revoke the device tokens of a deleted or banned device:", err)
		}
	}
}

type deleted struct {
	Success  bool `json:"success"`
	Revoked  bool `json:"revoked"`
	Hard     bool `json:"hard"`
	Cascaded any  `json:"cascaded"`
}

// restore answers POST /api/devices/{id}/restore: a soft-deleted device
// comes back. A deleted device keeps its groups and folder, so the scope
// is checked on it first.
func (h *handlers) restore(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if existing := h.backend.GetDeviceByID(ctx, id, true); existing != nil {
		if ok, err := h.inScope(w, r, existing); err != nil || !ok {
			if err != nil {
				h.serverError(w, r, "Restore device error:", err)
			}
			return
		}
	}
	result := h.backend.RestoreDevice(ctx, id)
	if !result.Bool("success") {
		status := http.StatusInternalServerError
		if result.Get("error") == "peer not found" {
			status = http.StatusNotFound
		}
		answer(w, r, status, failure{Error: or(result.Get("error"), kit.T(r, "devices.restore_failed", nil))})
		return
	}
	if err := h.d.LogAction(r, "device_restored", "Device "+id+" restored"); err != nil {
		h.serverError(w, r, "Restore device error:", err)
		return
	}
	answer(w, r, http.StatusOK, withData{Success: true, Data: or(result.Get("data"), result)})
}

// connectionMode answers POST /api/devices/{id}/connection-mode with
// {mode, reason}: the server queues the change of the desktop's
// connection mode ("normal" or "incoming-only") for its support agent.
func (h *handlers) connectionMode(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.found(w, r, "Set connection mode error:"); !ok {
		return
	}
	id := r.PathValue("id")
	mode, _ := value(r, "mode").(string)
	mode = jsTrim(mode)
	reason, _ := value(r, "reason").(string)
	reason = slice(jsTrim(reason), 500)
	if mode != "normal" && mode != "incoming-only" {
		fail(w, r, http.StatusBadRequest, "device_detail.connection_mode_failed")
		return
	}
	if reason == "" {
		fail(w, r, http.StatusBadRequest, "device_detail.connection_mode_reason")
		return
	}
	operatorID := ""
	if userID := kit.UserID(r); userID != 0 {
		operatorID = strconv.FormatInt(userID, 10)
	}
	operatorName := ""
	if user := kit.User(r); user != nil {
		operatorName = user.Username
	}
	result := h.api.SetConnectionMode(r.Context(), id, goapi.NewObject(
		"mode", mode, "reason", reason, "operator_id", operatorID, "operator_name", operatorName,
	))
	if !result.Bool("success") {
		status := http.StatusBadGateway
		if n, ok := result.Number("status"); ok && n != 0 {
			status = int(n)
		}
		answer(w, r, status, failure{Error: or(result.Get("error"), kit.T(r, "device_detail.connection_mode_failed", nil))})
		return
	}
	data := result.Get("data")
	previous := or(dig(data, "command", "previous_mode"), or(dig(data, "policy", "effective_mode"), ""))
	parts := []any{previous, mode, reason, or(dig(data, "command", "revision"), ""), or(dig(data, "command", "command_id"), "")}
	texts := make([]string, len(parts))
	for i, p := range parts {
		s, err := text(p)
		if err != nil {
			h.serverError(w, r, "Set connection mode error:", err)
			return
		}
		texts[i] = s
	}
	details := "Device " + id + " connection mode " + texts[0] + " -> " + texts[1] + "; reason: " + texts[2] +
		"; revision: " + texts[3] + "; command: " + texts[4]
	if err := h.d.LogAction(r, "device_connection_mode", details); err != nil {
		h.serverError(w, r, "Set connection mode error:", err)
		return
	}
	answer(w, r, http.StatusOK, withData{Success: true, Data: data})
}

// banRefused answers that the server did not change the ban.
func banRefused(w http.ResponseWriter, r *http.Request, result *goapi.Object) {
	answer(w, r, http.StatusBadGateway, failure{Error: or(result.Get("error"), "The server did not change the ban")})
}

// ban answers POST /api/devices/{id}/ban with {reason}. The audit entry
// is where the device page reads who banned it from. A banned device
// loses its device tokens; an unban gives none back.
func (h *handlers) ban(w http.ResponseWriter, r *http.Request) {
	reason := slice(jsTrim(kit.Body(r).Text("reason", "")), 500)
	if _, ok := h.found(w, r, "Ban device error:"); !ok {
		return
	}
	id := r.PathValue("id")
	result := h.backend.SetBanStatus(r.Context(), id, true, reason)
	if !result.Bool("success") {
		banRefused(w, r, result)
		return
	}
	h.revokeDeviceTokens(r, id)
	details := "Device " + id + " banned"
	if reason != "" {
		details += ": " + reason
	}
	if err := h.d.LogAction(r, "device_banned", details); err != nil {
		h.serverError(w, r, "Ban device error:", err)
		return
	}
	answer(w, r, http.StatusOK, done{Success: true})
}

// unban answers POST /api/devices/{id}/unban.
func (h *handlers) unban(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.found(w, r, "Unban device error:"); !ok {
		return
	}
	id := r.PathValue("id")
	result := h.backend.SetBanStatus(r.Context(), id, false, "")
	if !result.Bool("success") {
		banRefused(w, r, result)
		return
	}
	if err := h.d.LogAction(r, "device_unbanned", "Device "+id+" unbanned"); err != nil {
		h.serverError(w, r, "Unban device error:", err)
		return
	}
	answer(w, r, http.StatusOK, done{Success: true})
}

// goIDReservedDeleted is the server's refusal of an id that a deleted
// device still holds.
const goIDReservedDeleted = "This ID belongs to a deleted device. Restore or permanently delete that device before reusing the ID."

var newIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// changeID answers POST /api/devices/{id}/change-id with {newId}: text of
// 6 to 16 letters, digits, - and _, not taken by a device, also not by a
// deleted one. Anything but text is refused like an id of the wrong
// length. The console's tables follow the new id.
func (h *handlers) changeID(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	oldID := r.PathValue("id")
	newID, isText := value(r, "newId").(string)
	if n := length(newID); !isText || n < 6 || n > 16 {
		fail(w, r, http.StatusBadRequest, "devices.invalid_id")
		return
	}
	if !newIDPattern.MatchString(newID) {
		fail(w, r, http.StatusBadRequest, "devices.invalid_id_format")
		return
	}
	if _, ok := h.found(w, r, "Change ID error:"); !ok {
		return
	}
	if h.backend.GetDeviceByID(ctx, newID, false) != nil {
		fail(w, r, http.StatusBadRequest, "devices.id_exists")
		return
	}
	if existing := h.backend.GetDeviceByID(ctx, newID, true); existing != nil && existing.Bool("soft_deleted") {
		fail(w, r, http.StatusBadRequest, "devices.id_reserved_deleted")
		return
	}
	result := h.backend.ChangePeerID(ctx, oldID, newID)
	if !result.Bool("success") {
		message, err := changeIDError(r, result.Get("error"))
		if err != nil {
			h.serverError(w, r, "Change ID error:", err)
			return
		}
		answer(w, r, http.StatusBadRequest, failure{Error: message})
		return
	}
	h.groups.CascadePeerIDChange(ctx, oldID, newID)
	if err := h.d.LogAction(r, "device_id_changed", "Device ID changed from "+oldID+" to "+newID); err != nil {
		h.serverError(w, r, "Change ID error:", err)
		return
	}
	answer(w, r, http.StatusOK, done{Success: true})
}

// changeIDError is mapChangeIdError: the server's refusals in the request
// language, any other error as the server gave it.
func changeIDError(r *http.Request, refusal any) (any, error) {
	if !goapi.Truthy(refusal) {
		return kit.T(r, "devices.change_id_failed", nil), nil
	}
	s, err := text(refusal)
	if err != nil {
		return nil, err
	}
	if s == goIDReservedDeleted || strings.Contains(s, "deleted device") {
		return kit.T(r, "devices.id_reserved_deleted", nil), nil
	}
	if s == "Device ID already exists" {
		return kit.T(r, "devices.id_exists", nil), nil
	}
	return refusal, nil
}

// bulkDelete answers POST /api/devices/bulk-delete with {ids}: every
// device must exist and be one the account may see, else nothing is
// deleted; then each is soft-deleted, and the answer counts those the
// server deleted, which lose their device tokens.
func (h *handlers) bulkDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ids, isList := value(r, "ids").([]any)
	if !isList || len(ids) == 0 {
		fail(w, r, http.StatusBadRequest, "devices.no_selection")
		return
	}
	var targets []string
	for _, id := range ids {
		key, err := text(id)
		if err != nil {
			h.serverError(w, r, "Bulk delete error:", err)
			return
		}
		device := h.backend.GetDeviceByID(ctx, key, false)
		if device == nil {
			answer(w, r, http.StatusNotFound, notFoundDevice{Error: kit.T(r, "devices.not_found", nil), ID: key})
			return
		}
		if ok, err := h.inScope(w, r, device); err != nil || !ok {
			if err != nil {
				h.serverError(w, r, "Bulk delete error:", err)
			}
			return
		}
		targets = append(targets, key)
	}
	count := 0
	for _, id := range targets {
		result := h.backend.DeleteDevice(ctx, id, goapi.DeleteOptions{})
		if result.Bool("success") || result.Bool("changes") {
			count++
			h.revokeDeviceTokens(r, id)
		}
	}
	if err := h.d.LogAction(r, "devices_bulk_deleted", strconv.Itoa(count)+" devices deleted"); err != nil {
		h.serverError(w, r, "Bulk delete error:", err)
		return
	}
	answer(w, r, http.StatusOK, withData{Success: true, Data: deletedCount{Deleted: count}})
}

type deletedCount struct {
	Deleted int `json:"deleted"`
}

// notFoundDevice is the answer for a device of a list that does not
// exist.
type notFoundDevice struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	ID      string `json:"id"`
}

// rename answers POST /api/devices/{id}/rename with {display_name}: the
// display name, trimmed and cut to 200 characters. A refusal of the
// server is not checked: the answer counts its changes.
func (h *handlers) rename(w http.ResponseWriter, r *http.Request) {
	raw, err := textOr(value(r, "display_name"), "")
	if err != nil {
		h.serverError(w, r, "Rename device error:", err)
		return
	}
	name := slice(jsTrim(raw), 200)
	if name == "" {
		answer(w, r, http.StatusBadRequest, failure{Error: "display_name_required"})
		return
	}
	if _, ok := h.found(w, r, "Rename device error:"); !ok {
		return
	}
	id := r.PathValue("id")
	result := h.backend.UpdateDevice(r.Context(), id, goapi.NewObject("display_name", name))
	if err := h.d.LogAction(r, "device.rename", "Device "+id+" renamed to \""+name+"\""); err != nil {
		h.serverError(w, r, "Rename device error:", err)
		return
	}
	answer(w, r, http.StatusOK, withData{Success: true, Data: renamedDevice{Changes: changesOf(result), DisplayName: name}})
}

type renamedDevice struct {
	Changes     any    `json:"changes"`
	DisplayName string `json:"display_name"`
}
