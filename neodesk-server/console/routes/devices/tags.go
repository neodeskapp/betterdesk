package devices

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/devicegroups"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// Tags are free text on devices, kept by the server. Renaming or deleting
// a tag works on the devices the account may see and on the filter of the
// tag groups it may see that select by the tag: a rename gives them the
// new name, a delete empties their filter.

// tags answers GET /api/tags: every tag of the devices the account may
// see, once, sorted.
func (h *handlers) tags(w http.ResponseWriter, r *http.Request) {
	devices, err := h.visibleDevices(r)
	if err != nil {
		h.serverError(w, r, "Get tags error:", err)
		return
	}
	list := make([]any, len(devices))
	for i, device := range devices {
		list[i] = device
	}
	tags := devicegroups.CollectVisibleTags(list)
	answer(w, r, http.StatusOK, withData{Success: true, Data: tagList{Tags: tags}})
}

type tagList struct {
	Tags []string `json:"tags"`
}

// setPeerTags stores a device's tags on the server; ok is false when the
// server refused.
func (h *handlers) setPeerTags(ctx context.Context, deviceID any, tags []string) (bool, error) {
	result := h.backend.SetPeerTags(ctx, jsTextOf(deviceID), tags)
	return result.Get("success") != false, nil
}

// jsTextOf is String(v) for a device id, which is text.
func jsTextOf(v any) string {
	s, _ := text(v)
	return s
}

// visibleGroups are the device groups the account may see, without the
// groups that carry a folder's access list
// (getVisibleDeviceGroupsForRequest).
func (h *handlers) visibleGroups(r *http.Request) ([]*goapi.Object, error) {
	rows, err := h.groups.AllDeviceGroups(r.Context())
	if err != nil {
		return nil, err
	}
	user := h.d.Access.UserAccessContext(r.Context(), devicegroups.ScopeUser(kit.User(r)))
	out := make([]*goapi.Object, 0, len(rows))
	for _, row := range rows {
		if _, folder := devicegroups.FolderIDFromGroupGUID(row.Get("guid")); folder {
			continue
		}
		if access.GroupAllowedForUser(devicegroups.ScopeGroup(row), user) {
			out = append(out, row)
		}
	}
	return out, nil
}

// scopeGroups are device group rows as the tag rules read them.
func scopeGroups(rows []*goapi.Object) []*access.DeviceGroup {
	out := make([]*access.DeviceGroup, len(rows))
	for i, row := range rows {
		g := devicegroups.ScopeGroup(row)
		out[i] = &g
	}
	return out
}

// tagName reads a tag name of a rename or delete: bodyString of the
// member, cleaned.
func tagName(r *http.Request, name string) string {
	return devicegroups.SanitizeTagName(kit.Body(r).Text(name, ""))
}

// setTagFilter stores the tag filter of a device group.
func (h *handlers) setTagFilter(ctx context.Context, guid, tagFilter string) error {
	_, err := h.groups.UpdateDeviceGroup(ctx, guid, devicegroups.GroupData{TagFilter: jsbind.String(tagFilter)})
	return err
}

// renameTag answers POST /api/tags/rename with {from, to}.
func (h *handlers) renameTag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	from, to := tagName(r, "from"), tagName(r, "to")
	if from == "" || to == "" {
		fail(w, r, http.StatusBadRequest, "devices.tag_name_required")
		return
	}
	if from == to {
		fail(w, r, http.StatusBadRequest, "devices.tag_rename_identical")
		return
	}
	devices, err := h.visibleDevices(r)
	if err != nil {
		h.serverError(w, r, "Rename tag error:", err)
		return
	}
	groups, err := h.visibleGroups(r)
	if err != nil {
		h.serverError(w, r, "Rename tag error:", err)
		return
	}
	result, err := devicegroups.RenameTagAcrossScope(ctx, devices, scopeGroups(groups), from, to, h.setPeerTags, h.setTagFilter)
	if err != nil {
		h.serverError(w, r, "Rename tag error:", err)
		return
	}
	for _, e := range result.Errors {
		if e == "identical" {
			fail(w, r, http.StatusBadRequest, "devices.tag_rename_identical")
			return
		}
	}
	details := fmt.Sprintf(`Tag "%s" renamed to "%s" on %d device(s), %d group(s)`, from, to, result.UpdatedDevices, result.UpdatedGroups)
	if err := h.d.LogAction(r, "device_tag_renamed", details); err != nil {
		h.serverError(w, r, "Rename tag error:", err)
		return
	}
	answer(w, r, http.StatusOK, withData{Success: true, Data: renamed{
		From: result.From, To: result.To, UpdatedDevices: result.UpdatedDevices, UpdatedGroups: result.UpdatedGroups,
		Skipped: result.Skipped, FailedDevices: result.Errors,
	}})
}

type renamed struct {
	From           string   `json:"from"`
	To             string   `json:"to"`
	UpdatedDevices int      `json:"updatedDevices"`
	UpdatedGroups  int      `json:"updatedGroups"`
	Skipped        int      `json:"skipped"`
	FailedDevices  []string `json:"failedDevices"`
}

// deleteTag answers POST /api/tags/delete with {tag}.
func (h *handlers) deleteTag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tag := tagName(r, "tag")
	if tag == "" {
		fail(w, r, http.StatusBadRequest, "devices.tag_name_required")
		return
	}
	devices, err := h.visibleDevices(r)
	if err != nil {
		h.serverError(w, r, "Delete tag error:", err)
		return
	}
	groups, err := h.visibleGroups(r)
	if err != nil {
		h.serverError(w, r, "Delete tag error:", err)
		return
	}
	result, err := devicegroups.DeleteTagAcrossScope(ctx, devices, scopeGroups(groups), tag, h.setPeerTags, h.setTagFilter)
	if err != nil {
		h.serverError(w, r, "Delete tag error:", err)
		return
	}
	details := fmt.Sprintf(`Tag "%s" removed from %d device(s) (%d dynamic group(s) reference it)`, tag, result.UpdatedDevices, result.AffectedGroups)
	if err := h.d.LogAction(r, "device_tag_deleted", details); err != nil {
		h.serverError(w, r, "Delete tag error:", err)
		return
	}
	answer(w, r, http.StatusOK, withData{Success: true, Data: tagDeleted{
		Tag: result.Tag, UpdatedDevices: result.UpdatedDevices, Skipped: result.Skipped,
		AffectedGroups: result.AffectedGroups, FailedDevices: result.Errors,
	}})
}

type tagDeleted struct {
	Tag            string   `json:"tag"`
	UpdatedDevices int      `json:"updatedDevices"`
	Skipped        int      `json:"skipped"`
	AffectedGroups int      `json:"affectedGroups"`
	FailedDevices  []string `json:"failedDevices"`
}

// setTags answers PUT /api/devices/{id}/tags with {tags: [...]}: the
// device's tags become the texts of the list, trimmed, without empty ones,
// each cut to 50 UTF-16 units before a character that does not fit whole
// and trimmed again, at most 20.
func (h *handlers) setTags(w http.ResponseWriter, r *http.Request) {
	list, isList := value(r, "tags").([]any)
	if !isList {
		answer(w, r, http.StatusBadRequest, failure{Error: "Tags must be an array"})
		return
	}
	cleaned := []string{}
	for _, item := range list {
		if s, ok := item.(string); ok && jsTrim(s) != "" {
			cleaned = append(cleaned, jsTrim(slice(jsTrim(s), 50)))
		}
	}
	if len(cleaned) > 20 {
		answer(w, r, http.StatusBadRequest, failure{Error: "Maximum 20 tags allowed"})
		return
	}
	if _, ok := h.found(w, r, "Set tags error:"); !ok {
		return
	}
	id := r.PathValue("id")
	result := h.backend.SetPeerTags(r.Context(), id, cleaned)
	if !result.Bool("success") {
		answer(w, r, http.StatusBadRequest, failure{Error: or(result.Get("error"), "Failed to set tags")})
		return
	}
	if err := h.d.LogAction(r, "device_tags_updated", "Device "+id+" tags set to ["+strings.Join(cleaned, ", ")+"]"); err != nil {
		h.serverError(w, r, "Set tags error:", err)
		return
	}
	answer(w, r, http.StatusOK, withData{Success: true, Data: tagSet{Tags: cleaned}})
}

type tagSet struct {
	Tags []string `json:"tags"`
}
