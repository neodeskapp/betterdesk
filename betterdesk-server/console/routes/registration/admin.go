package registration

import (
	"encoding/hex"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/unitronix/betterdesk-server/console/auditlog"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/registrations"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// The registration list for people with enrollment.approve.

// serverError is the 500 of the people's routes, in the request's
// language.
func (h *handlers) serverError(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.log.Error(what, err)
	kit.JSON(w, r, http.StatusInternalServerError, goapi.NewObject("success", false, "error", kit.T(r, "errors.server_error", nil)))
}

// failure is the group's error answer: {"success":false,"error":message}.
func failure(w http.ResponseWriter, r *http.Request, status int, message any) {
	kit.JSON(w, r, status, goapi.NewObject("success", false, "error", message))
}

// registrationID is parseInt(req.params.id, 10): NaN for an id without
// leading digits.
func registrationID(r *http.Request) jsbind.Value {
	n, ok := jsreq.ParseInt(r.PathValue("id"), 10)
	if !ok {
		return jsbind.Number(math.NaN())
	}
	return jsbind.Number(n)
}

// audit writes an entry as these routes did: the account of the session
// (0 without one, BUGS AL7), the address getClientIp found, and errors
// ignored.
func (h *handlers) audit(r *http.Request, action, details string) {
	var id int64
	if user := kit.User(r); user != nil {
		id = user.ID
	}
	ip := clientIP(r)
	if err := h.d.Audit.WriteEntry(r.Context(), auditlog.Entry{UserID: &id, Action: action, Details: &details, IP: &ip}); err != nil {
		h.log.Debug("Audit entry not written:", err.Error())
	}
}

// list answers GET /api/registrations?status=&search=.
func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	filters := registrations.Filters{
		Status: jsbind.Query(r.URL.RawQuery, "status").Or(jsbind.String("")),
		Search: jsbind.Query(r.URL.RawQuery, "search").Or(jsbind.String("")),
	}
	rows, err := h.regs.List(r.Context(), filters)
	if err != nil {
		h.serverError(w, r, "Get registrations error:", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "data", rows, "total", float64(len(rows))))
}

// count answers GET /api/registrations/count: the pending LAN
// registrations plus the count the API server gives for its enrollment
// queue, added as JavaScript adds (a count in text makes text, BUGS RG7).
// A source that fails counts 0; a sum that throws answers 0.
func (h *handlers) count(w http.ResponseWriter, r *http.Request) {
	lan, err := h.regs.PendingCount(r.Context())
	if err != nil {
		lan = 0
	}
	pending := h.d.GoAPI.GetEnrollmentPending(r.Context())
	sum, err := add(lan, orValue(pending.Get("count"), float64(0)))
	if err != nil {
		sum = float64(0)
	}
	kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "count", sum))
}

const (
	settingRichApprove = "enrollment_rich_approve"
	settingTagPicker   = "enrollment_tag_picker"
)

// enrollmentUI answers GET /api/registrations/enrollment-ui: the API
// server's enrollment mode ("open" when it does not say) and the two
// approval settings, on unless they say otherwise.
func (h *handlers) enrollmentUI(w http.ResponseWriter, r *http.Request) {
	result := h.d.GoAPI.GetEnrollmentMode(r.Context())
	var mode any = "open"
	if data, ok := result.Get("data").(*goapi.Object); ok && result.Bool("success") && goapi.Truthy(data.Get("mode")) {
		mode = data.Get("mode")
	}
	rich, err := getSetting(r.Context(), h.d.Store, settingRichApprove)
	if err != nil {
		h.serverError(w, r, "Get enrollment UI settings error:", err)
		return
	}
	tags, err := getSetting(r.Context(), h.d.Store, settingTagPicker)
	if err != nil {
		h.serverError(w, r, "Get enrollment UI settings error:", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "data", goapi.NewObject(
		"mode", mode,
		"rich_approve", boolSetting(rich, true),
		"tag_picker", boolSetting(tags, true),
	)))
}

// boolSetting is parseBoolSetting: the default for a setting that is
// missing or empty, else whether its text in lower case is 1, true or
// yes.
func boolSetting(v jsbind.Value, fallback bool) bool {
	if v.IsUndefined() || v.IsNull() || (v.IsString() && v.Str() == "") {
		return fallback
	}
	switch jsbind.ToLower(v.ToString()) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// one answers GET /api/registrations/{id}.
func (h *handlers) one(w http.ResponseWriter, r *http.Request) {
	reg, err := h.regs.ByID(r.Context(), registrationID(r))
	if err != nil {
		h.serverError(w, r, "Get registration error:", err)
		return
	}
	if reg == nil {
		failure(w, r, http.StatusNotFound, "Registration not found")
		return
	}
	kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "data", reg))
}

// approve answers PUT /api/registrations/{id}/approve with the body
// {folder_id, group_guids, display_name, tags}. The pending registration
// becomes approved with the server configuration its device polls for:
// the console's address with the host the approver used, the signal
// server on port 21116 of that host, the server's key and a new access
// token. Then the device goes into the folder and the device groups, and
// gets the display name and the tags when the API server knows it. A
// display name or tags that are truthy but no text answer 500 after the
// approval is stored, without the event and the audit entry (BUGS RG6).
func (h *handlers) approve(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := registrationID(r)
	reg, err := h.regs.ByID(ctx, id)
	if err != nil {
		h.serverError(w, r, "Approve registration error:", err)
		return
	}
	if reg == nil {
		failure(w, r, http.StatusNotFound, "Registration not found")
		return
	}
	if status := reg.Get("status"); status != "pending" {
		failure(w, r, http.StatusBadRequest, "Cannot approve: status is "+jsreq.String(status))
		return
	}
	cfg, err := h.serverConfig(r)
	if err != nil {
		h.serverError(w, r, "Approve registration error:", err)
		return
	}
	approvedBy := "admin"
	if user := kit.User(r); user != nil && user.Username != "" {
		approvedBy = user.Username
	}
	updated, err := h.regs.Approve(ctx, id, approvedBy, cfg)
	if err != nil {
		h.serverError(w, r, "Approve registration error:", err)
		return
	}

	body := jsreq.Body(r)
	if deviceID := reg.Get("device_id"); goapi.Truthy(deviceID) {
		device := jsreq.String(deviceID)
		h.assignFolder(r, device, jsreq.Prop(body, "folder_id"), "Assign folder on LAN approve:")
		if err := h.applyGroups(r, device, jsreq.Prop(body, "group_guids")); err != nil {
			h.log.Error("Assign groups on LAN approve:", err)
		}
		displayName, err := textOr(jsreq.Prop(body, "display_name"), "")
		if err != nil {
			h.serverError(w, r, "Approve registration error:", err)
			return
		}
		tags, err := textOr(jsreq.Prop(body, "tags"), "")
		if err != nil {
			h.serverError(w, r, "Approve registration error:", err)
			return
		}
		h.applyPeerFields(r, device, jsbind.Trim(displayName), jsbind.Trim(tags))
	}

	h.publish(goapi.NewObject(
		"type", "registration_changed",
		"registration", goapi.NewObject(
			"source", "lan",
			"id", id.ToNumber(),
			"device_id", reg.Get("device_id"),
			"hostname", reg.Get("hostname"),
			"platform", reg.Get("platform"),
			"status", "approved",
		),
		"timestamp", float64(h.now().UnixMilli()),
	))
	h.audit(r, "registration_approved", "Approved registration for device "+jsreq.String(reg.Get("device_id"))+" ("+jsreq.String(reg.Get("hostname"))+")")
	kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "data", nullable(updated)))
}

// nullable is a row for JSON: null when there is none.
func nullable(row *goapi.Object) any {
	if row == nil {
		return nil
	}
	return row
}

// serverConfig is buildServerConfig with 0.0.0.0 replaced by the host of
// the request: the part of the Host header before its first colon, else
// the request's host name, else localhost.
func (h *handlers) serverConfig(r *http.Request) (registrations.ServerConfig, error) {
	protocol := "http"
	port := 0
	if h.d.Config != nil {
		if h.d.Config.HTTPS {
			protocol = "https"
		}
		port = h.d.Config.Port
	}
	var key string
	if h.d.ServerInfo != nil {
		key = string(h.d.ServerInfo.ResolvePublicKey(r.Context()))
	}
	token := make([]byte, 32)
	if _, err := io.ReadFull(h.random, token); err != nil {
		return registrations.ServerConfig{}, err
	}
	host := h.actualHost(r)
	return registrations.ServerConfig{
		ConsoleURL:    replaceFirst(protocol+"://0.0.0.0:"+strconv.Itoa(port), "0.0.0.0", host),
		ServerAddress: replaceFirst("0.0.0.0:21116", "0.0.0.0", host),
		ServerKey:     key,
		AccessToken:   hex.EncodeToString(token),
	}, nil
}

// actualHost is req.headers.host?.split(':')[0] || req.hostname ||
// 'localhost'. An IPv6 address in the Host header gives "[" (BUGS RG5).
func (h *handlers) actualHost(r *http.Request) string {
	if first, _, _ := strings.Cut(r.Host, ":"); first != "" {
		return first
	}
	host := r.Host
	if h.d.Config != nil {
		host = h.d.Config.TrustProxy.Host(r)
	}
	if host == "" {
		return "localhost"
	}
	offset := 0
	if host[0] == '[' {
		offset = strings.IndexByte(host, ']') + 1
	}
	if i := strings.IndexByte(host[offset:], ':'); i >= 0 {
		host = host[:offset+i]
	}
	if host == "" {
		return "localhost"
	}
	return host
}

// assignFolder puts the device into the folder parseInt(folderID, 10)
// names when that is a number above 0; a failure is logged.
func (h *handlers) assignFolder(r *http.Request, deviceID string, folderID any, what string) {
	n, ok := jsreq.ParseInt(folderID, 10)
	if !ok || !(n > 0) {
		return
	}
	if err := h.d.DeviceGroups.AssignDeviceToFolder(r.Context(), jsbind.String(deviceID), jsbind.Number(n)); err != nil {
		h.log.Error(what, err)
	}
}

// applyPeerFields gives a device the API server knows the display name
// (also as its note) and the tags of a text list split at commas.
func (h *handlers) applyPeerFields(r *http.Request, deviceID, displayName, tags string) {
	ctx := r.Context()
	if h.d.Backend.GetDeviceByID(ctx, deviceID, false) == nil {
		return
	}
	if displayName != "" {
		h.d.GoAPI.UpdatePeer(ctx, deviceID, goapi.NewObject("note", displayName, "display_name", displayName))
	}
	if tags != "" {
		var list []any
		for _, tag := range strings.Split(tags, ",") {
			if tag = jsbind.Trim(tag); tag != "" {
				list = append(list, tag)
			}
		}
		if len(list) > 0 {
			h.d.GoAPI.SetPeerTags(ctx, deviceID, list)
		}
	}
}

// reject answers PUT /api/registrations/{id}/reject with the body
// {reason}, cut to 500 UTF-16 units. A reason that is truthy but no text
// is the 500 of a TypeError, before the registration is looked up.
func (h *handlers) reject(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := registrationID(r)
	reason, err := textOr(jsreq.Prop(jsreq.Body(r), "reason"), "")
	if err != nil {
		h.serverError(w, r, "Reject registration error:", err)
		return
	}
	reason = cut(reason, 500)
	reg, err := h.regs.ByID(ctx, id)
	if err != nil {
		h.serverError(w, r, "Reject registration error:", err)
		return
	}
	if reg == nil {
		failure(w, r, http.StatusNotFound, "Registration not found")
		return
	}
	if status := reg.Get("status"); status != "pending" {
		failure(w, r, http.StatusBadRequest, "Cannot reject: status is "+jsreq.String(status))
		return
	}
	updated, err := h.regs.Reject(ctx, id, reason)
	if err != nil {
		h.serverError(w, r, "Reject registration error:", err)
		return
	}
	h.audit(r, "registration_rejected", "Rejected registration for device "+jsreq.String(reg.Get("device_id"))+" ("+jsreq.String(reg.Get("hostname"))+"): "+reason)
	kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "data", nullable(updated)))
}

// remove answers DELETE /api/registrations/{id}.
func (h *handlers) remove(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := registrationID(r)
	reg, err := h.regs.ByID(ctx, id)
	if err != nil {
		h.serverError(w, r, "Delete registration error:", err)
		return
	}
	if reg == nil {
		failure(w, r, http.StatusNotFound, "Registration not found")
		return
	}
	if _, err := h.regs.Delete(ctx, id); err != nil {
		h.serverError(w, r, "Delete registration error:", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true))
}
