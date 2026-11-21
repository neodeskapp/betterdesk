package registration

import (
	"encoding/hex"
	"io"
	"math"
	"net"
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

// registrationID is parseInt(req.params.id, 10). ok is false when that is
// no id a row can have: no number, below 1, or above the largest integer
// the column holds. Such an id finds no registration on either database.
func registrationID(r *http.Request) (id jsbind.Value, ok bool) {
	n, ok := jsreq.ParseInt(r.PathValue("id"), 10)
	if !ok || n < 1 || n > math.MaxInt32 {
		return jsbind.Value{}, false
	}
	return jsbind.Number(n), true
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

// listPageSize is the most registrations one page of the list holds, and
// the page size when the request names none.
const listPageSize = 500

// list answers GET /api/registrations?status=&search=&limit=&offset=: one
// page of the registrations the filters keep, newest first, at most
// listPageSize of them, after offset skipped ones; total counts the
// registrations of every page.
func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	filters := registrations.Filters{
		Status: jsbind.Query(r.URL.RawQuery, "status").Or(jsbind.String("")),
		Search: jsbind.Query(r.URL.RawQuery, "search").Or(jsbind.String("")),
		Limit:  listPageSize,
	}
	if n, ok := jsreq.ParseInt(jsbind.Query(r.URL.RawQuery, "limit").ToString(), 10); ok && n >= 1 && n < listPageSize {
		filters.Limit = int(n)
	}
	if n, ok := jsreq.ParseInt(jsbind.Query(r.URL.RawQuery, "offset").ToString(), 10); ok && n >= 1 && n <= math.MaxInt32 {
		filters.Offset = int(n)
	}
	rows, total, err := h.regs.List(r.Context(), filters)
	if err != nil {
		h.serverError(w, r, "Get registrations error:", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "data", rows, "total", total))
}

// count answers GET /api/registrations/count: the pending LAN
// registrations plus the count the API server gives for its enrollment
// queue, read as a number (Number(count) || 0). A source that fails
// counts 0.
func (h *handlers) count(w http.ResponseWriter, r *http.Request) {
	lan, err := h.regs.PendingCount(r.Context())
	if err != nil {
		lan = 0
	}
	pending := h.d.GoAPI.GetEnrollmentPending(r.Context())
	kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "count", lan+countOf(pending.Get("count"))))
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
	id, ok := registrationID(r)
	if !ok {
		failure(w, r, http.StatusNotFound, "Registration not found")
		return
	}
	reg, err := h.regs.ByID(r.Context(), id)
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
// becomes approved with the server configuration its device polls for
// (serverConfig). Then the device goes into the folder and the device groups, and
// gets the display name and the tags when the API server knows it. A
// display name or tags that are truthy but no text answer 400 before
// anything is stored, so an approval always has its event and its audit
// entry.
func (h *handlers) approve(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := registrationID(r)
	if !ok {
		failure(w, r, http.StatusNotFound, "Registration not found")
		return
	}
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
	body := jsreq.Body(r)
	displayName, err := textOr(jsreq.Prop(body, "display_name"), "")
	if err != nil {
		failure(w, r, http.StatusBadRequest, "Invalid display_name")
		return
	}
	tags, err := textOr(jsreq.Prop(body, "tags"), "")
	if err != nil {
		failure(w, r, http.StatusBadRequest, "Invalid tags")
		return
	}
	allowed, err := h.mayApproveFor(r, reg.Get("device_id"))
	if err != nil {
		h.serverError(w, r, "Approve registration error:", err)
		return
	}
	if !allowed {
		failure(w, r, http.StatusForbidden, kit.T(r, "errors.forbidden", nil))
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
	updated, changed, err := h.regs.Approve(ctx, id, approvedBy, cfg)
	if err != nil {
		h.serverError(w, r, "Approve registration error:", err)
		return
	}
	if !changed {
		h.lostRace(w, r, updated, "Cannot approve: status is ")
		return
	}

	var unassigned []string
	if deviceID := reg.Get("device_id"); goapi.Truthy(deviceID) {
		device := jsreq.String(deviceID)
		if err := h.assignFolder(r, device, jsreq.Prop(body, "folder_id")); err != nil {
			h.log.Error("Assign folder on LAN approve:", err)
			unassigned = append(unassigned, "folder")
		}
		if err := h.applyGroups(r, device, jsreq.Prop(body, "group_guids")); err != nil {
			h.log.Error("Assign groups on LAN approve:", err)
			unassigned = append(unassigned, "device groups")
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
	if len(unassigned) > 0 {
		kit.JSON(w, r, http.StatusInternalServerError, goapi.NewObject("success", false, "error", unassignedMessage(unassigned), "data", nullable(updated)))
		return
	}
	kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "data", nullable(updated)))
}

// unassignedMessage is the answer to an approval that was stored while
// the folder or the device groups (parts) could not be assigned.
func unassignedMessage(parts []string) string {
	return "Approved, but the " + strings.Join(parts, " and ") + " could not be assigned"
}

// mayApproveFor reports whether the approver may approve a registration
// for the device id: always for a device the API server does not know (a
// new machine), and for a device it knows only when the device is in the
// approver's device scope. An approval puts that device into a folder and
// device groups and gives it a name and tags, so an approver must not
// reach a device outside its scope this way.
func (h *handlers) mayApproveFor(r *http.Request, deviceID any) (bool, error) {
	if !goapi.Truthy(deviceID) {
		return true, nil
	}
	device := h.d.Backend.GetDeviceByID(r.Context(), jsreq.String(deviceID), false)
	if device == nil {
		return true, nil
	}
	return h.d.DeviceGroups.UserCanAccessDevice(r.Context(), kit.User(r), device)
}

// nullable is a row for JSON: null when there is none.
func nullable(row *goapi.Object) any {
	if row == nil {
		return nil
	}
	return row
}

// serverConfig is the configuration an approved device polls for: the
// console's address and the signal server's (approvalAddresses), the
// server's key and a new access token.
func (h *handlers) serverConfig(r *http.Request) (registrations.ServerConfig, error) {
	protocol := "http"
	port := 0
	if h.d.Config != nil {
		if h.d.Config.HTTPS {
			protocol = "https"
		}
		port = h.d.Config.Port
	}
	var key, publicID string
	if h.d.ServerInfo != nil {
		key = string(h.d.ServerInfo.ResolvePublicKey(r.Context()))
		publicID = h.d.ServerInfo.GetPublicEndpointSettings().PublicServerID
	}
	token := make([]byte, 32)
	if _, err := io.ReadFull(h.random, token); err != nil {
		return registrations.ServerConfig{}, err
	}
	consoleURL, serverAddress := approvalAddresses(protocol, port, r.Host, publicID)
	return registrations.ServerConfig{
		ConsoleURL:    consoleURL,
		ServerAddress: serverAddress,
		ServerKey:     key,
		AccessToken:   hex.EncodeToString(token),
	}, nil
}

// approvalAddresses are the console's address and the signal server's an
// approved device is given. The host is the public ID server setting's
// when there is one, else the one of the Host header the approver's
// request had, else localhost; an IPv6 address keeps its brackets. The
// console's address has the console's port; the signal server's has the
// setting's port, else 21116.
func approvalAddresses(protocol string, port int, requestHost, publicID string) (consoleURL, serverAddress string) {
	host, signalPort := splitHost(publicID)
	if host == "" {
		host, _ = splitHost(requestHost)
		signalPort = ""
	}
	if host == "" {
		host = "localhost"
	}
	if signalPort == "" {
		signalPort = "21116"
	}
	return protocol + "://" + net.JoinHostPort(host, strconv.Itoa(port)), net.JoinHostPort(host, signalPort)
}

// splitHost takes "host:port", "[v6]:port", "[v6]" or "host" apart.
func splitHost(s string) (host, port string) {
	s = jsbind.Trim(s)
	if h, p, err := net.SplitHostPort(s); err == nil {
		return h, p
	}
	return strings.TrimSuffix(strings.TrimPrefix(s, "["), "]"), ""
}

// assignFolder puts the device into the folder parseInt(folderID, 10)
// names when that is a number above 0, and returns why it could not.
func (h *handlers) assignFolder(r *http.Request, deviceID string, folderID any) error {
	n, ok := jsreq.ParseInt(folderID, 10)
	if !ok || !(n > 0) {
		return nil
	}
	return h.d.DeviceGroups.AssignDeviceToFolder(r.Context(), jsbind.String(deviceID), jsbind.Number(n))
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
	reason, err := textOr(jsreq.Prop(jsreq.Body(r), "reason"), "")
	if err != nil {
		h.serverError(w, r, "Reject registration error:", err)
		return
	}
	reason = cut(reason, 500)
	id, ok := registrationID(r)
	if !ok {
		failure(w, r, http.StatusNotFound, "Registration not found")
		return
	}
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
	updated, changed, err := h.regs.Reject(ctx, id, reason)
	if err != nil {
		h.serverError(w, r, "Reject registration error:", err)
		return
	}
	if !changed {
		h.lostRace(w, r, updated, "Cannot reject: status is ")
		return
	}
	h.audit(r, "registration_rejected", "Rejected registration for device "+jsreq.String(reg.Get("device_id"))+" ("+jsreq.String(reg.Get("hostname"))+"): "+reason)
	kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "data", nullable(updated)))
}

// lostRace answers an approval or a rejection whose registration stopped
// being pending after the route read it (another approver was first) as
// one that was not pending: 400 with its status now, 404 when it is gone.
func (h *handlers) lostRace(w http.ResponseWriter, r *http.Request, row *goapi.Object, message string) {
	if row == nil {
		failure(w, r, http.StatusNotFound, "Registration not found")
		return
	}
	failure(w, r, http.StatusBadRequest, message+jsreq.String(row.Get("status")))
}

// remove answers DELETE /api/registrations/{id}.
func (h *handlers) remove(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := registrationID(r)
	if !ok {
		failure(w, r, http.StatusNotFound, "Registration not found")
		return
	}
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
