package users

import (
	"context"
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/devicegroups"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// userGroup is a user group as the routes send it.
type userGroup struct {
	GUID        any `json:"guid"`
	Name        any `json:"name"`
	Note        any `json:"note"`
	TeamID      any `json:"team_id"`
	MemberCount any `json:"member_count"`
}

// orValue is v || fallback for a value of a row.
func orValue(v any, fallback any) any {
	if truthy(v) {
		return v
	}
	return fallback
}

// serializeUserGroup picks the fields of a user group row.
func serializeUserGroup(row *goapi.Object) userGroup {
	return userGroup{
		GUID:        row.Get("guid"),
		Name:        row.Get("name"),
		Note:        orValue(row.Get("note"), ""),
		TeamID:      orValue(row.Get("team_id"), ""),
		MemberCount: orValue(row.Get("member_count"), float64(0)),
	}
}

// groupPayload is normalizeUserGroupPayload: name, note and team id as
// trimmed text, all three read before any is checked.
func groupPayload(r *http.Request) (devicegroups.UserGroupData, error) {
	body := kit.Body(r)
	name, err := trimmedOr(member(body, "name"), "")
	if err != nil {
		return devicegroups.UserGroupData{}, err
	}
	note, err := trimmedOr(member(body, "note"), "")
	if err != nil {
		return devicegroups.UserGroupData{}, err
	}
	teamID, err := trimmedOr(member(body, "team_id"), "")
	if err != nil {
		return devicegroups.UserGroupData{}, err
	}
	switch {
	case name == "":
		return devicegroups.UserGroupData{}, badRequest("Group name is required")
	case jsLength(name) > 80:
		return devicegroups.UserGroupData{}, badRequest("Group name is too long")
	case jsLength(note) > 500:
		return devicegroups.UserGroupData{}, badRequest("Group note is too long")
	case teamID != "" && !validGroupGUID(teamID):
		return devicegroups.UserGroupData{}, badRequest("Invalid team identifier")
	}
	return devicegroups.UserGroupData{Name: jsbind.String(name), Note: jsbind.String(note), TeamID: jsbind.String(teamID)}, nil
}

// groupFailure answers what a user group write threw: its text with
// status 400, the server error otherwise.
func (h *handlers) groupFailure(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.log.Error(what, err)
	status := statusOf(err)
	if status == 0 {
		status = http.StatusInternalServerError
	}
	if status == http.StatusBadRequest {
		fail(w, r, status, err.Error())
		return
	}
	failKey(w, r, status, "errors.server_error")
}

// userGroups answers GET /api/panel/user-groups: every user group with its
// number of members, ordered by name.
func (h *handlers) userGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.d.DeviceGroups.AllUserGroups(r.Context())
	if err != nil {
		h.serverError(w, r, "Get user groups error:", err)
		return
	}
	out := make([]userGroup, len(groups))
	for i, group := range groups {
		out[i] = serializeUserGroup(group)
	}
	answer(w, r, http.StatusOK, groupList{Success: true, Data: groupListData{Groups: out, Total: len(out)}})
}

type groupList struct {
	Success bool          `json:"success"`
	Data    groupListData `json:"data"`
}

type groupListData struct {
	Groups []userGroup `json:"groups"`
	Total  int         `json:"total"`
}

type groupAnswer struct {
	Success bool            `json:"success"`
	Data    groupAnswerData `json:"data"`
}

type groupAnswerData struct {
	Group userGroup `json:"group"`
}

// createUserGroup answers POST /api/panel/user-groups with the body
// {"name", "note", "team_id"}. The group gets a random guid.
func (h *handlers) createUserGroup(w http.ResponseWriter, r *http.Request) {
	payload, err := groupPayload(r)
	if err != nil {
		h.groupFailure(w, r, "Create user group error:", err)
		return
	}
	group, err := h.d.DeviceGroups.CreateUserGroup(r.Context(), payload)
	if err == nil && group == nil {
		err = errNoRow
	}
	if err == nil {
		err = h.d.LogAction(r, "user_group_created", "Created user group: "+rowText(group.Get("name")))
	}
	if err != nil {
		h.groupFailure(w, r, "Create user group error:", err)
		return
	}
	answer(w, r, http.StatusOK, groupAnswer{Success: true, Data: groupAnswerData{Group: serializeUserGroup(group)}})
}

// groupGUID is String(req.params.guid || "").trim(); ok is false for a
// guid isValidGroupGuid refuses.
func groupGUID(r *http.Request) (string, bool) {
	guid := jsbind.Trim(r.PathValue("guid"))
	return guid, validGroupGUID(guid)
}

// updateUserGroup answers PATCH /api/panel/user-groups/{guid}: the guid is
// checked, then that the group exists, then the body.
func (h *handlers) updateUserGroup(w http.ResponseWriter, r *http.Request) {
	guid, ok := groupGUID(r)
	if !ok {
		fail(w, r, http.StatusBadRequest, "Invalid user group identifier")
		return
	}
	ctx := r.Context()
	existing, err := h.d.DeviceGroups.UserGroupByGUID(ctx, guid)
	if err != nil {
		h.groupFailure(w, r, "Update user group error:", err)
		return
	}
	if existing == nil {
		failKey(w, r, http.StatusNotFound, "users.user_group_not_found")
		return
	}
	payload, err := groupPayload(r)
	if err != nil {
		h.groupFailure(w, r, "Update user group error:", err)
		return
	}
	// A note or team the body leaves out stays as it is
	body := kit.Body(r)
	if !body.Has("note") {
		payload.Note = jsbind.Undefined
	}
	if !body.Has("team_id") {
		payload.TeamID = jsbind.Undefined
	}
	group, err := h.d.DeviceGroups.UpdateUserGroup(ctx, guid, payload)
	if err == nil && group == nil {
		err = errNoRow
	}
	if err == nil {
		err = h.countMembers(ctx, group)
	}
	if err == nil {
		err = h.d.LogAction(r, "user_group_updated", "Updated user group: "+rowText(group.Get("name")))
	}
	if err != nil {
		h.groupFailure(w, r, "Update user group error:", err)
		return
	}
	answer(w, r, http.StatusOK, groupAnswer{Success: true, Data: groupAnswerData{Group: serializeUserGroup(group)}})
}

// countMembers sets a group row's member_count, which a row read after a
// write doesn't have.
func (h *handlers) countMembers(ctx context.Context, group *goapi.Object) error {
	all, err := h.d.DeviceGroups.AllUserGroups(ctx)
	if err != nil {
		return err
	}
	for _, g := range all {
		if rowText(g.Get("guid")) == rowText(group.Get("guid")) {
			group.Set("member_count", g.Get("member_count"))
		}
	}
	return nil
}

// deleteUserGroup answers DELETE /api/panel/user-groups/{guid}, for a
// caller who may change every member of the group, as saving its panel
// access needs (L391). The limits the group puts on its members stay with
// them (access.Service.KeepGroupLimits), then the group's panel access
// policy is emptied.
func (h *handlers) deleteUserGroup(w http.ResponseWriter, r *http.Request) {
	guid, ok := groupGUID(r)
	if !ok {
		fail(w, r, http.StatusBadRequest, "Invalid user group identifier")
		return
	}
	ctx := r.Context()
	group, err := h.d.DeviceGroups.UserGroupByGUID(ctx, guid)
	if err != nil {
		h.serverError(w, r, "Delete user group error:", err)
		return
	}
	if group == nil {
		failKey(w, r, http.StatusNotFound, "users.user_group_not_found")
		return
	}
	if ok, err := h.mayChangeMembers(r, guid); err != nil {
		h.serverError(w, r, "Delete user group error:", err)
		return
	} else if !ok {
		sent(w, r, errRoleAboveOwn)
		return
	}
	if err := h.d.Access.KeepGroupLimits(ctx, guid); err != nil {
		h.serverError(w, r, "Delete user group error:", err)
		return
	}
	if _, err := h.d.Access.SavePolicy(ctx, access.SubjectGroup, guid, nil, nil); err != nil {
		h.serverError(w, r, "Delete user group error:", err)
		return
	}
	if err := h.d.DeviceGroups.DeleteUserGroup(ctx, guid); err != nil {
		h.serverError(w, r, "Delete user group error:", err)
		return
	}
	if err := h.d.LogAction(r, "user_group_deleted", "Deleted user group: "+rowText(group.Get("name"))); err != nil {
		h.serverError(w, r, "Delete user group error:", err)
		return
	}
	answer(w, r, http.StatusOK, done{Success: true})
}

// mayChangeMembers reports whether the caller may give every member of the
// user group its role (access.Service.MayGiveRole), its own account
// included: no account lifts limits of an account it may not change, nor
// its own.
func (h *handlers) mayChangeMembers(r *http.Request, guid string) (bool, error) {
	group, err := h.d.Access.UserGroupByGUID(r.Context(), guid)
	if err != nil || group == nil {
		return group == nil && err == nil, err
	}
	members, err := h.d.Access.UserGroupMembers(r.Context(), group.ID)
	if err != nil {
		return false, err
	}
	for _, member := range members {
		ok, err := h.d.Access.MayGiveRole(r.Context(), callerRole(r), member.Role)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}
