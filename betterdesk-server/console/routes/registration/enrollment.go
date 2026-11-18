package registration

import (
	"net/http"
	"regexp"

	"github.com/unitronix/betterdesk-server/console/devicegroups"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// The API server's enrollment queue, passed on with the console's own
// steps around an approval: the folder, the device groups, the audit log.

// enrollmentPending answers GET /api/enrollment/pending with what the
// API client returned.
func (h *handlers) enrollmentPending(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, h.d.GoAPI.GetEnrollmentPending(r.Context()))
}

// enrollmentHistory answers GET /api/enrollment/history?status=. A status
// that is no text (a list, an object) asks for every decision.
func (h *handlers) enrollmentHistory(w http.ResponseWriter, r *http.Request) {
	status := ""
	if v := jsbind.Query(r.URL.RawQuery, "status"); v.IsString() {
		status = v.Str()
	}
	kit.JSON(w, r, http.StatusOK, h.d.GoAPI.GetEnrollmentHistory(r.Context(), status))
}

// enrollmentApprove answers POST /api/enrollment/approve/{id} with the
// body {display_name, sync_mode, tags, folder_id, group_guids}: the API
// server approves, and when it says it did, the device goes into the
// folder and the device groups and the approval is audited. The answer is
// the API client's.
func (h *handlers) enrollmentApprove(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("id")
	body := jsreq.Body(r)
	field := func(name string) any { return jsreq.Prop(body, name) }
	syncMode, groupGUIDs := field("sync_mode"), field("group_guids")
	result := h.d.GoAPI.ApproveEnrollment(r.Context(), deviceID, field("display_name"), syncMode, field("tags"))
	if result.Bool("success") {
		h.assignFolder(r, deviceID, field("folder_id"), "Assign device to folder error:")
		if err := h.applyGroups(r, deviceID, groupGUIDs); err != nil {
			h.log.Error("Assign device groups on enrollment approve:", err)
		}
		if details, err := enrollmentApprovalDetails(deviceID, syncMode, groupGUIDs); err == nil {
			h.audit(r, "enrollment_approved", details)
		}
	}
	kit.JSON(w, r, http.StatusOK, result)
}

// enrollmentApprovalDetails is the audit text of an enrollment approval:
// the mode and the device groups as the body named them. It fails where
// JavaScript could not make text of them, and the entry is not written.
func enrollmentApprovalDetails(deviceID string, syncMode, groupGUIDs any) (string, error) {
	mode, err := text(orValue(syncMode, "standard"))
	if err != nil {
		return "", err
	}
	note := ""
	if list, ok := groupGUIDs.([]any); ok && len(list) > 0 {
		joined, err := join(list, ", ")
		if err != nil {
			return "", err
		}
		note = ", groups: [" + joined + "]"
	}
	return "Approved enrollment for device " + deviceID + " (mode: " + mode + note + ")", nil
}

// enrollmentReject answers POST /api/enrollment/reject/{id} with the body
// {ban}, which counts when it is truthy.
func (h *handlers) enrollmentReject(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("id")
	ban := goapi.Truthy(jsreq.Prop(jsreq.Body(r), "ban"))
	result := h.d.GoAPI.RejectEnrollment(r.Context(), deviceID, ban)
	if result.Bool("success") {
		details := "Rejected enrollment for device " + deviceID
		if ban {
			details += " (banned)"
		}
		h.audit(r, "enrollment_rejected", details)
	}
	kit.JSON(w, r, http.StatusOK, result)
}

// enrollmentClearRejection answers POST
// /api/enrollment/clear-rejection/{id}.
func (h *handlers) enrollmentClearRejection(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("id")
	result := h.d.GoAPI.ClearEnrollmentRejection(r.Context(), deviceID)
	if result.Bool("success") {
		details := "Cleared enrollment rejection for device " + deviceID
		if goapi.Truthy(goapi.Dig(result.Get("data"), "unbanned")) {
			details += " (unbanned)"
		}
		h.audit(r, "enrollment_rejection_cleared", details)
	}
	kit.JSON(w, r, http.StatusOK, result)
}

var groupGUIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)

// applyGroups is applyDeviceGroupMemberships: for a list of device group
// guids (at most 100 good ones), the device becomes a stored member of
// each manual group the account may see that the list names, and stops
// being one of the others. Nothing happens for a device the API server
// does not know or the account may not see. A guid that is not text is
// read as String() made it; one String() throws on ends it all.
func (h *handlers) applyGroups(r *http.Request, deviceID string, groupGUIDs any) error {
	list, ok := groupGUIDs.([]any)
	if !ok || len(list) == 0 {
		return nil
	}
	var guids []string
	seen := map[string]bool{}
	for _, item := range list {
		s, err := text(item)
		if err != nil {
			return err
		}
		if s == "" || length(s) > 80 || !groupGUIDPattern.MatchString(s) || seen[s] {
			continue
		}
		seen[s] = true
		guids = append(guids, s)
	}
	if len(guids) > 100 {
		return nil
	}
	ctx := r.Context()
	device := h.d.Backend.GetDeviceByID(ctx, deviceID, false)
	if device == nil {
		return nil
	}
	all := h.d.Backend.GetAllDevices(ctx, goapi.DeviceFilters{})
	user := kit.User(r)
	scoped := devicegroups.ScopeDevices([]*goapi.Object{device})
	allowed, err := h.d.Access.UserCanAccessDevice(ctx, devicegroups.ScopeUser(user), &scoped[0], devicegroups.ScopeDevices(all))
	if err != nil {
		return err
	}
	if !allowed {
		return nil
	}
	groups, err := h.d.DeviceGroups.AllDeviceGroups(ctx)
	if err != nil {
		return err
	}
	accessUser := h.d.DeviceGroups.UserAccessContext(ctx, user)
	var manual []string
	inManual := map[string]bool{}
	for _, group := range groups {
		if _, isFolder := devicegroups.FolderIDFromGroupGUID(group.Get("guid")); isFolder {
			continue
		}
		if !devicegroups.GroupAllowedForUser(group, accessUser) {
			continue
		}
		if orValue(devicegroups.Plain(group.Get("source_type")), "manual") == "tag" {
			continue
		}
		guid := jsreq.String(devicegroups.Plain(group.Get("guid")))
		manual = append(manual, guid)
		inManual[guid] = true
	}
	selected := map[string]bool{}
	for _, guid := range guids {
		if inManual[guid] {
			selected[guid] = true
		}
	}
	for _, guid := range manual {
		if selected[guid] {
			err = h.d.DeviceGroups.AddDeviceToGroup(ctx, guid, deviceID)
		} else {
			err = h.d.DeviceGroups.RemoveDeviceFromGroup(ctx, guid, deviceID)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
