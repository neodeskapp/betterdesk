package organizations

import (
	"math"
	"net/http"
	"strconv"

	"github.com/unitronix/betterdesk-server/console/devicegroups"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// defaultGroupNote is the note of the device group made with an
// organization. Billing packages attach to that group.
const defaultGroupNote = "Default group created with organization (commercialization)"

// create answers POST /api/panel/org: the API server creates the
// organization, and the console makes a device group for it named after
// it with the organization's id as team. A group that cannot be made is
// logged; the answer is the API server's either way.
func (h *handlers) create(w http.ResponseWriter, r *http.Request) {
	resp, err := h.api.Request(r.Context(), http.MethodPost, "/org", goapipath.Body(goapipath.RequestBody(r)))
	if err != nil {
		status, data := goapipath.FailureOf(err, unreachable)
		goapipath.WriteJSON(w, r, status, data)
		return
	}
	org := resp.Data()
	if o, ok := org.(*goapi.Object); ok && o != nil && goapi.Truthy(o.Get("id")) {
		name := jsbind.Trim(jsreq.StringOr(o.Get("name"), "Organization"))
		if name == "" {
			name = "Organization"
		}
		_, err := h.groups.CreateDeviceGroup(r.Context(), devicegroups.GroupData{
			Name: jsbind.String(name), Note: jsbind.String(defaultGroupNote), TeamID: requestValue(o.Get("id")),
			SourceType: jsbind.String("manual"),
		})
		if err != nil {
			h.log.Warn("[org] default device group create failed:", jsbind.Message(err))
		}
	}
	goapipath.WriteJSON(w, r, resp.Status, org)
}

// requestValue is a value of the API server's answer as a value to bind.
func requestValue(v any) jsbind.Value {
	switch x := v.(type) {
	case nil:
		return jsbind.Null
	case bool:
		return jsbind.Bool(x)
	case float64:
		return jsbind.Number(x)
	case string:
		return jsbind.String(x)
	}
	return jsbind.String(jsreq.String(v))
}

// removeMember answers DELETE /api/panel/org/{id}/members/{userId}. The
// API server keys members by its account id, which is the console's: the
// console and the server share one user table. A user id that names an
// account goes on as that account's id, anything else as it came.
func (h *handlers) removeMember(w http.ResponseWriter, r *http.Request) {
	member := r.PathValue("userId")
	if n := jsbind.ParseNumber(member); n > 0 && n == math.Trunc(n) && !math.IsInf(n, 0) {
		if id, ok, err := h.groups.UserIDByID(r.Context(), n); err == nil && ok {
			member = strconv.FormatInt(id, 10)
		}
	}
	org, err := orgPath("")(r)
	if err != nil {
		invalid(w, r, err)
		return
	}
	segment, err := goapipath.SafeSegment(member, "userId")
	if err != nil {
		invalid(w, r, err)
		return
	}
	h.proxy(w, r, http.MethodDelete, org+"/members/"+segment, false)
}

// linked is the answer of GET /api/panel/org/{id}/device-groups.
type linked struct {
	OrgID        string `json:"org_id"`
	DeviceGroups []any  `json:"device_groups"`
	UserGroups   []any  `json:"user_groups"`
}

// deviceGroups answers GET /api/panel/org/{id}/device-groups: the device
// groups and user groups whose team is the organization, without the
// groups of folders. Each device group has member_count, the number of
// devices in it.
func (h *handlers) deviceGroups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orgID, err := goapipath.AssertSafeAPIID(r.PathValue("id"), "orgId")
	if err != nil {
		invalid(w, r, err)
		return
	}
	devices := h.backend.GetAllDevices(ctx, goapi.DeviceFilters{})
	all, err := h.groups.AllDeviceGroups(ctx)
	if err != nil {
		h.loadFailed(w, r, err)
		return
	}
	var mine []*goapi.Object
	for _, group := range all {
		if _, isFolder := devicegroups.FolderIDFromGroupGUID(devicegroups.Plain(group.Get("guid"))); isFolder {
			continue
		}
		if teamOf(group) == orgID {
			mine = append(mine, group)
		}
	}
	answer := linked{OrgID: orgID, DeviceGroups: []any{}, UserGroups: []any{}}
	for _, group := range h.groups.EnrichGroups(ctx, mine, devices) {
		answer.DeviceGroups = append(answer.DeviceGroups, group)
	}
	userGroups, err := h.groups.AllUserGroups(ctx)
	if err != nil {
		h.loadFailed(w, r, err)
		return
	}
	for _, group := range userGroups {
		if teamOf(group) != orgID {
			continue
		}
		note := devicegroups.Plain(group.Get("note"))
		if !goapi.Truthy(note) {
			note = ""
		}
		count := devicegroups.Plain(group.Get("member_count"))
		if !goapi.Truthy(count) {
			count = float64(0)
		}
		answer.UserGroups = append(answer.UserGroups, goapi.NewObject(
			"guid", group.Get("guid"), "name", group.Get("name"), "note", note, "member_count", count))
	}
	goapipath.WriteJSON(w, r, http.StatusOK, answer)
}

// teamOf is the team of a group as trimmed text, empty without one.
func teamOf(group *goapi.Object) string {
	return jsbind.Trim(jsreq.StringOr(devicegroups.Plain(group.Get("team_id")), ""))
}

// loadFailed answers 500 for groups that could not be read.
func (h *handlers) loadFailed(w http.ResponseWriter, r *http.Request, err error) {
	h.log.Error("[org] List org device groups error:", err)
	goapipath.WriteJSON(w, r, http.StatusInternalServerError, goapipath.ErrorBody{Error: "Failed to load organization groups"})
}
