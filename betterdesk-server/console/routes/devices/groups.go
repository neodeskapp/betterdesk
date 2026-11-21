package devices

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/devicegroups"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
)

// Device groups: manual ones whose members are stored, tag groups whose
// members are the devices with a tag, both with an optional list of the
// accounts and user groups that may see them. The groups that carry a
// folder's access list are left to the folder routes.

var groupGUIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)

// validGroupGUID is isValidGroupGuid: text of 1 to 80 letters, digits and
// _ . : -.
func validGroupGUID(guid string) bool {
	return guid != "" && len(guid) <= 80 && groupGUIDPattern.MatchString(guid)
}

// errNoGroup is the TypeError of reading the name of a group that is
// gone.
var errNoGroup = errors.New("Cannot read properties of null (reading 'name')")

// deviceGroups answers GET /api/device-groups: the groups the account may
// see, each with the number of its devices the account may see.
func (h *handlers) deviceGroups(w http.ResponseWriter, r *http.Request) {
	devices, err := h.visibleDevices(r)
	if err != nil {
		h.serverError(w, r, "Get device groups error:", err)
		return
	}
	groups, err := h.visibleGroups(r)
	if err != nil {
		h.serverError(w, r, "Get device groups error:", err)
		return
	}
	enriched := h.groups.EnrichGroups(r.Context(), groups, devices)
	answer(w, r, http.StatusOK, withData{Success: true, Data: groupList{Groups: enriched, Total: len(enriched)}})
}

type groupList struct {
	Groups []*goapi.Object `json:"groups"`
	Total  int             `json:"total"`
}

// bodyMap is req.body as the group rules read it: the members of an
// object body as encoding/json decodes them.
func bodyMap(r *http.Request) map[string]any {
	body := kit.Body(r)
	out := make(map[string]any, body.Len())
	for _, key := range body.Keys() {
		var v any
		if err := body.Get(key).Decode(&v); err == nil {
			out[key] = v
		} else {
			out[key] = nil
		}
	}
	return out
}

// groupData is the write of a device group from its fields.
func groupData(f devicegroups.GroupFields) devicegroups.GroupData {
	of := func(p *string) jsbind.Value {
		if p == nil {
			return jsbind.Undefined
		}
		return jsbind.String(*p)
	}
	return devicegroups.GroupData{Name: of(f.Name), Note: of(f.Note), TeamID: of(f.TeamID), SourceType: of(f.SourceType), TagFilter: of(f.TagFilter)}
}

// saveDeviceGroup answers POST /api/device-groups: a group without guid
// is created, one with a guid is changed; a name, guid, note, team id or
// tag filter that is no text, and an access list of more than 100
// accounts or user groups, are refused with 400. The API server writes
// it and answers with what it saved; with the console's own writer
// (BACKEND_PANEL_GROUPING=node, or an API server that cannot take it),
// allowed_users and allowed_groups replace the access lists when the body
// has them, and the answer is the group's row.
func (h *handlers) saveDeviceGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	payload := devicegroups.NormalizeGroupPayload(bodyMap(r))
	if payload.NotText != "" {
		answer(w, r, http.StatusBadRequest, failure{Error: payload.NotText + " must be text"})
		return
	}
	if payload.TooLong != "" {
		answer(w, r, http.StatusBadRequest, failure{Error: tooLong(payload.TooLong)})
		return
	}
	if payload.Name == "" {
		fail(w, r, http.StatusBadRequest, "folders.name_required")
		return
	}
	if payload.SourceType == "tag" && payload.TagFilter == "" {
		fail(w, r, http.StatusBadRequest, "devices.group_tag_required")
		return
	}
	for _, guid := range payload.AllowedGroups {
		if !validGroupGUID(guid) {
			fail(w, r, http.StatusBadRequest, "devices.group_invalid")
			return
		}
	}
	if payload.GUID != "" {
		if !validGroupGUID(payload.GUID) {
			fail(w, r, http.StatusBadRequest, "devices.group_invalid")
			return
		}
		if _, folder := devicegroups.FolderIDFromGroupGUID(payload.GUID); folder {
			fail(w, r, http.StatusBadRequest, "devices.folder_group_readonly")
			return
		}
	}
	if ok, err := h.groupSaveInScope(r, payload); err != nil || !ok {
		if err != nil {
			h.serverError(w, r, "Save device group error:", err)
			return
		}
		fail(w, r, http.StatusForbidden, "errors.forbidden")
		return
	}
	if h.writesToGo() {
		saved, err := h.saveGroupToGo(r, groupPayload(groupFields{
			guid: payload.GUID, name: payload.Name, note: payload.Note, teamID: payload.TeamID,
			sourceType: payload.SourceType, tagFilter: payload.TagFilter,
			allowedUsers: payload.AllowedUsers, allowedGroups: payload.AllowedGroups,
		}))
		if err != nil {
			h.groupWriteFailed(w, r, "Save device group error:", err)
			return
		}
		if goapi.Truthy(saved) {
			name, err := savedName(saved)
			if err == nil {
				err = h.d.LogAction(r, "device_group_saved", "Device group "+name+" saved")
			}
			if err != nil {
				h.serverError(w, r, "Save device group error:", err)
				return
			}
			answer(w, r, http.StatusOK, withData{Success: true, Data: saved})
			return
		}
	}
	var group *goapi.Object
	var err error
	if payload.GUID != "" {
		group, err = h.groups.UpdateDeviceGroup(ctx, payload.GUID, groupData(payload.UpdateFields()))
		if err == nil && group == nil {
			fail(w, r, http.StatusNotFound, "devices.group_not_found")
			return
		}
	} else {
		group, err = h.groups.CreateDeviceGroup(ctx, groupData(payload.CreateFields()))
	}
	body := kit.Body(r)
	if err == nil && body.Has("allowed_users") {
		group, err = h.groups.SetDeviceGroupUserAccess(ctx, groupGUID(group), payload.AllowedUsers)
	}
	if err == nil && group != nil && body.Has("allowed_groups") {
		group, err = h.groups.SetDeviceGroupUserGroupAccess(ctx, groupGUID(group), payload.AllowedGroups)
	}
	if err == nil && group == nil {
		err = errNoGroup
	}
	var name string
	if err == nil {
		name, err = text(devicegroups.Plain(group.Get("name")))
	}
	if err == nil {
		err = h.d.LogAction(r, "device_group_saved", "Device group "+name+" saved")
	}
	if err != nil {
		h.serverError(w, r, "Save device group error:", err)
		return
	}
	answer(w, r, http.StatusOK, withData{Success: true, Data: group})
}

// groupRights is what the account may do with a stored device group, or
// with a new one for nil (access.GroupRightsForUser, the rule of the
// folders too).
func (h *handlers) groupRights(r *http.Request, group *goapi.Object) (access.GroupRights, error) {
	var g *access.DeviceGroup
	if group != nil {
		scoped := devicegroups.ScopeGroup(group)
		g = &scoped
	}
	return h.d.Access.GroupRightsForUser(r.Context(), devicegroups.ScopeUser(kit.User(r)), g)
}

// groupSaveInScope reports whether the account may save the group of the
// payload: it must be allowed to change the group (a new one when the
// payload names none that is stored), and to change access lists when the
// payload's lists differ from the stored ones (a new group has none). An
// account that may not change access lists, one whose device scope is
// limited, also makes no tag group: it neither turns a group into one nor
// changes the tag of one, since either would widen its own scope.
func (h *handlers) groupSaveInScope(r *http.Request, p devicegroups.GroupPayload) (bool, error) {
	var stored *goapi.Object
	if p.GUID != "" {
		var err error
		if stored, err = h.groups.DeviceGroupByGUID(r.Context(), p.GUID); err != nil {
			return false, err
		}
	}
	rights, err := h.groupRights(r, stored)
	if err != nil || !rights.Change {
		return false, err
	}
	if rights.ChangeAccess {
		return true, nil
	}
	var was access.DeviceGroup
	if stored != nil {
		was = devicegroups.ScopeGroup(stored)
	}
	if !sameEntries(p.AllowedUsers, was.AllowedUsers) || !sameEntries(p.AllowedGroups, was.AllowedGroups) {
		return false, nil
	}
	if p.SourceType == "tag" && (was.SourceType != "tag" || p.TagFilter != was.TagFilter) {
		return false, nil
	}
	return true, nil
}

// sameEntries reports whether two lists hold the same entries, in any
// order.
func sameEntries(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	in := make(map[string]bool, len(b))
	for _, s := range b {
		in[s] = true
	}
	for _, s := range a {
		if !in[s] {
			return false
		}
	}
	return true
}

// tooLong is the refusal of an access list with more entries than
// devicegroups.MaxAccessListEntries.
func tooLong(list string) string {
	return list + " can hold at most " + strconv.Itoa(devicegroups.MaxAccessListEntries) + " entries"
}

// groupWriteFailed answers a failed group write: a refusal of the API
// server with its status and message, anything else with 500.
func (h *handlers) groupWriteFailed(w http.ResponseWriter, r *http.Request, what string, err error) {
	var refused *goRefusal
	if errors.As(err, &refused) {
		answer(w, r, refused.status, failure{Error: refused.message})
		return
	}
	h.serverError(w, r, what, err)
}

// groupGUID is the guid of a group row as text.
func groupGUID(group *goapi.Object) string {
	if group == nil {
		return ""
	}
	return jsTextOf(devicegroups.Plain(group.Get("guid")))
}

// deleteDeviceGroup answers DELETE /api/device-groups/{guid}.
func (h *handlers) deleteDeviceGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	guid := r.PathValue("guid")
	if !validGroupGUID(guid) {
		fail(w, r, http.StatusBadRequest, "devices.group_invalid")
		return
	}
	group, err := h.groups.DeviceGroupByGUID(ctx, guid)
	if err != nil {
		h.serverError(w, r, "Delete device group error:", err)
		return
	}
	if group == nil {
		fail(w, r, http.StatusNotFound, "devices.group_not_found")
		return
	}
	if _, folder := devicegroups.FolderIDFromGroupGUID(guid); folder {
		fail(w, r, http.StatusBadRequest, "devices.folder_group_readonly")
		return
	}
	if rights, err := h.groupRights(r, group); err != nil || !rights.Change {
		if err != nil {
			h.serverError(w, r, "Delete device group error:", err)
			return
		}
		fail(w, r, http.StatusForbidden, "errors.forbidden")
		return
	}
	deleted := false
	if h.writesToGo() {
		if deleted, err = h.deleteGroupFromGo(r, guid); err != nil {
			h.groupWriteFailed(w, r, "Delete device group error:", err)
			return
		}
	}
	name, err := text(devicegroups.Plain(group.Get("name")))
	if err == nil && !deleted {
		err = h.groups.DeleteDeviceGroup(ctx, guid)
	}
	if err == nil {
		err = h.d.LogAction(r, "device_group_deleted", "Device group "+name+" deleted")
	}
	if err != nil {
		h.serverError(w, r, "Delete device group error:", err)
		return
	}
	answer(w, r, http.StatusOK, done{Success: true})
}

// canSee reports whether the account may see the device, with the scope
// computed over the devices it may see (userCanAccessDevice with the
// whole list).
func (h *handlers) canSee(r *http.Request, device *goapi.Object, all []*goapi.Object) (bool, error) {
	one := devicegroups.ScopeDevices([]*goapi.Object{device})[0]
	return h.d.Access.UserCanAccessDevice(r.Context(), devicegroups.ScopeUser(kit.User(r)), &one, devicegroups.ScopeDevices(all))
}

// memberships answers GET /api/devices/{id}/groups: the groups the
// account may see, and those the device is in: the tag groups of its
// tags, then the manual groups it is a stored member of.
func (h *handlers) memberships(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	device := h.backend.GetDeviceByID(ctx, id, false)
	if device == nil {
		fail(w, r, http.StatusNotFound, "devices.not_found")
		return
	}
	all, err := h.visibleDevices(r)
	if err != nil {
		h.serverError(w, r, "Get device memberships error:", err)
		return
	}
	ok, err := h.canSee(r, device, all)
	if err != nil {
		h.serverError(w, r, "Get device memberships error:", err)
		return
	}
	if !ok {
		fail(w, r, http.StatusForbidden, "errors.forbidden")
		return
	}
	visible, err := h.visibleGroups(r)
	if err != nil {
		h.serverError(w, r, "Get device memberships error:", err)
		return
	}
	groups := h.groups.EnrichGroups(ctx, visible, all)
	tags := access.NormalizeTags(devicegroups.Plain(device.Get("tags")))
	memberships := []*goapi.Object{}
	in := map[string]bool{}
	for _, group := range groups {
		if jsTextOf(devicegroups.Plain(group.Get("source_type"))) != "tag" {
			continue
		}
		filter := jsbind.ToLower(jsTextOf(or(devicegroups.Plain(group.Get("tag_filter")), "")))
		for _, tag := range tags {
			if jsbind.ToLower(tag) == filter {
				memberships = append(memberships, group)
				in[groupGUID(group)] = true
				break
			}
		}
	}
	manual, err := h.groups.DeviceGroupsForPeer(ctx, id)
	if err != nil {
		h.serverError(w, r, "Get device memberships error:", err)
		return
	}
	stored := map[string]bool{}
	for _, row := range manual {
		stored[groupGUID(row)] = true
	}
	for _, group := range groups {
		if guid := groupGUID(group); stored[guid] && !in[guid] {
			memberships = append(memberships, group)
			in[guid] = true
		}
	}
	answer(w, r, http.StatusOK, withData{Success: true, Data: membershipList{Groups: groups, Memberships: memberships}})
}

type membershipList struct {
	Groups      []*goapi.Object `json:"groups"`
	Memberships []*goapi.Object `json:"memberships"`
}

// setMemberships answers PUT /api/devices/{id}/groups with {groupGuids}:
// the device becomes a member of the one manual group of the list the
// account may see and leaves the other manual groups it may see; an empty
// list takes it out of all of them. groupGuids that is missing or no list
// is refused like an invalid guid. A device is in at most one manual
// group; tag groups are not touched.
func (h *handlers) setMemberships(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	list, isList := value(r, "groupGuids").([]any)
	if !isList {
		fail(w, r, http.StatusBadRequest, "devices.group_invalid")
		return
	}
	var guids []string
	for _, item := range list {
		s, err := text(item)
		if err != nil {
			h.serverError(w, r, "Update device memberships error:", err)
			return
		}
		guids = append(guids, s)
	}
	invalid := len(guids) > 100
	for _, guid := range guids {
		invalid = invalid || !validGroupGUID(guid)
	}
	if invalid {
		fail(w, r, http.StatusBadRequest, "devices.group_invalid")
		return
	}
	device := h.backend.GetDeviceByID(ctx, id, false)
	if device == nil {
		fail(w, r, http.StatusNotFound, "devices.not_found")
		return
	}
	all, err := h.visibleDevices(r)
	if err != nil {
		h.serverError(w, r, "Update device memberships error:", err)
		return
	}
	ok, err := h.canSee(r, device, all)
	if err != nil {
		h.serverError(w, r, "Update device memberships error:", err)
		return
	}
	if !ok {
		fail(w, r, http.StatusForbidden, "errors.forbidden")
		return
	}
	groups, err := h.visibleGroups(r)
	if err != nil {
		h.serverError(w, r, "Update device memberships error:", err)
		return
	}
	var manual []string
	isManual := map[string]bool{}
	for _, group := range groups {
		if jsTextOf(or(devicegroups.Plain(group.Get("source_type")), "manual")) == "tag" {
			continue
		}
		guid := groupGUID(group)
		manual = append(manual, guid)
		isManual[guid] = true
	}
	selected := []string{}
	picked := map[string]bool{}
	for _, guid := range guids {
		if isManual[guid] && !picked[guid] {
			picked[guid] = true
			selected = append(selected, guid)
		}
	}
	if len(selected) > 1 {
		message := kit.T(r, "devices.group_one_only", nil)
		if message == "" {
			message = "A device can belong to only one group"
		}
		answer(w, r, http.StatusBadRequest, failure{Error: message})
		return
	}
	saved := false
	if h.writesToGo() {
		if saved, err = h.membershipToGo(r, id, manual, selected); err != nil {
			h.groupWriteFailed(w, r, "Update device memberships error:", err)
			return
		}
	}
	for _, guid := range manual {
		if saved {
			break
		}
		if len(selected) == 1 && selected[0] == guid {
			err = h.groups.AddDeviceToGroup(ctx, guid, id)
		} else {
			err = h.groups.RemoveDeviceFromGroup(ctx, guid, id)
		}
		if err != nil {
			h.serverError(w, r, "Update device memberships error:", err)
			return
		}
	}
	if err := h.d.LogAction(r, "device_group_membership_updated", "Device "+id+" groups set to ["+strings.Join(selected, ", ")+"]"); err != nil {
		h.serverError(w, r, "Update device memberships error:", err)
		return
	}
	answer(w, r, http.StatusOK, withData{Success: true, Data: groupGUIDs{GroupGUIDs: selected}})
}

type groupGUIDs struct {
	GroupGUIDs []string `json:"groupGuids"`
}
