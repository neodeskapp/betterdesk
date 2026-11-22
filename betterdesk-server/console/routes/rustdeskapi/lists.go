package rustdeskapi

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strings"

	"github.com/unitronix/betterdesk-server/console/devicegroups"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/routes/internal/reqquery"
)

// ---- Users ----

type listAnswer struct {
	Data  any `json:"data"`
	Total int `json:"total"`
}

type listMsgAnswer struct {
	Data  any    `json:"data"`
	Total int    `json:"total"`
	Msg   string `json:"msg"`
}

type rustdeskUser struct {
	Name      string `json:"name"`
	Email     string `json:"email"`
	Note      string `json:"note"`
	Status    int    `json:"status"`
	IsAdmin   bool   `json:"is_admin"`
	GroupName string `json:"group_name"`
}

// users answers GET /api/users with a bearer token: the account itself.
func (h *handlers) users(w http.ResponseWriter, r *http.Request) {
	user, ok := h.validate(w, r, bearerToken(r))
	if !ok {
		return
	}
	if user == nil {
		fail(w, r, http.StatusUnauthorized, msgInvalidToken)
		return
	}
	if user.isPro() {
		kit.JSON(w, r, http.StatusOK, listAnswer{Data: []any{}})
		return
	}
	kit.JSON(w, r, http.StatusOK, listAnswer{Data: []rustdeskUser{{
		Name: user.Username, Status: 1, IsAdmin: adminRoles[user.role()], GroupName: "Default",
	}}, Total: 1})
}

// ---- Peers ----

// filterParams is requestFilterParams: the members of the body, then the
// query's over them. A body member __proto__ that is an object became the
// prototype of the merged object (Object.assign), so its members fill in
// what neither has.
type filterParams struct {
	body  middleware.JSONObject
	query *reqquery.Query
}

func (h *handlers) filterParams(r *http.Request) filterParams {
	return filterParams{body: kit.Body(r), query: reqquery.Of(r)}
}

func (p filterParams) get(name string) any {
	if p.query.Has(name) {
		return p.query.Get(name).JSON()
	}
	if p.body.Has(name) {
		return member(p.body, name)
	}
	if proto, ok := valueOf(p.body.Get("__proto__")).(*goapi.Object); ok && proto.Has(name) {
		return proto.Get(name)
	}
	return undefined{}
}

// groupFieldValue reads a group out of a parameter: the first item of a
// list, the first of guid, id, ... of an object, else the trimmed text.
func groupFieldValue(v any) (string, error) {
	switch x := v.(type) {
	case []any:
		if len(x) == 0 {
			return groupFieldValue(undefined{})
		}
		return groupFieldValue(x[0])
	case *goapi.Object:
		var next any = ""
		for _, key := range []string{"guid", "id", "device_group_guid", "device_group_id", "group_guid", "group_id", "name", "group_name", "tag", "tag_filter"} {
			if value := x.Get(key); truthy(value) {
				next = value
				break
			}
		}
		return groupFieldValue(next)
	}
	text, err := str(or(v, ""))
	return jsTrim(text), err
}

var groupKeys = []string{
	"folder_id", "folderId", "folder", "folder_name", "folderName",
	"device_group_guid", "deviceGroupGuid",
	"device_group_id", "deviceGroupId",
	"device_group", "deviceGroup",
	"device_group_name", "deviceGroupName",
	"group_guid", "groupGuid",
	"group_id", "groupId",
	"group_name", "groupName",
	"group",
}

// requestedGroupValue is the first group any of the parameters names.
func requestedGroupValue(p filterParams) (string, error) {
	for _, key := range groupKeys {
		value, err := groupFieldValue(p.get(key))
		if err != nil {
			return "", err
		}
		if value != "" {
			return value, nil
		}
	}
	return "", nil
}

// requestedTagValues is the tags the parameters ask for: a list, or text
// split at commas; trimmed, cut to 50 characters, without repeats.
func requestedTagValues(p filterParams) ([]string, error) {
	raw := any("")
	for _, key := range []string{"tag", "tags", "tag_name", "tagName", "tag_filter", "tagFilter"} {
		if value := p.get(key); truthy(value) {
			raw = value
			break
		}
	}
	var items []any
	if list, ok := raw.([]any); ok {
		items = list
	} else {
		text, err := str(or(raw, ""))
		if err != nil {
			return nil, err
		}
		for _, part := range strings.Split(text, ",") {
			items = append(items, part)
		}
	}
	out := []string{}
	seen := map[string]bool{}
	for _, item := range items {
		text, err := str(or(item, ""))
		if err != nil {
			return nil, err
		}
		tag := sanitize(jsTrim(text), 50)
		if tag != "" && !seen[tag] {
			seen[tag] = true
			out = append(out, tag)
		}
	}
	return out, nil
}

var (
	folderGUIDPattern = regexp.MustCompile(`(?i)^folder_(\d+)$`)
	digitsPattern     = regexp.MustCompile(`^(\d+)$`)
)

// folderIDFromGroupGUID is the route file's own folderIdFromGroupGuid: the
// number of folder_<n>, or of a value that is all digits.
func folderIDFromGroupGUID(value any) (float64, bool) {
	if isUndefined(value) || value == nil || value == "" {
		return 0, false
	}
	text := jsTrim(strOr(value))
	m := folderGUIDPattern.FindStringSubmatch(text)
	if m == nil {
		m = digitsPattern.FindStringSubmatch(text)
	}
	if m == nil {
		return 0, false
	}
	n := parseIntText(m[1])
	return n, isFinite(n)
}

// deviceFolderID is getDeviceFolderId.
func deviceFolderID(d *goapi.Object) (float64, bool) {
	raw := d.Get("folder_id")
	if raw == nil || raw == "" || !d.Has("folder_id") {
		return 0, false
	}
	n := parseInt10(raw)
	return n, isFinite(n)
}

// deviceHasAllTags reports whether the device carries every tag, in any
// letter case.
func deviceHasAllTags(d *goapi.Object, expected []string) bool {
	if len(expected) == 0 {
		return true
	}
	tags := map[string]bool{}
	for _, tag := range devicegroups.NormalizeTags(d.Get("tags")) {
		tags[lowerJS(tag)] = true
	}
	for _, tag := range expected {
		if !tags[lowerJS(tag)] {
			return false
		}
	}
	return true
}

// isReachable is isReachableRustDeskDevice.
func isReachable(d *goapi.Object) bool {
	if d == nil || truthy(d.Get("banned")) || truthy(d.Get("disabled")) {
		return false
	}
	if d.Get("online") == true || d.Get("live_online") == true || d.Get("cdap_connected") == true {
		return true
	}
	status := lowerJS(jsTrim(strOr(or(or(or(d.Get("live_status"), d.Get("status_tier")), d.Get("status")), ""))))
	return status == "online" || status == "degraded" || status == "critical"
}

// rustdeskGroup is a device group or folder as the client API lists it.
type rustdeskGroup struct {
	guid, name, note any
	peerIDs          []string
}

// deviceGroups is getRustDeskDeviceGroups: the device groups open to the
// account (not those that mirror folders), then the folders open to it,
// each with its devices among the ones the account may see.
func (h *handlers) deviceGroups(ctx context.Context, user *tokenUser) []rustdeskGroup {
	groups := []rustdeskGroup{}
	accessUser := h.d.DeviceGroups.UserAccessContext(ctx, user.sessionUser())
	all := h.d.Backend.GetAllDevices(ctx, goapi.DeviceFilters{})
	limit := h.deviceLimit(ctx, user, all)
	devices := make([]*goapi.Object, 0, len(all))
	for _, d := range all {
		if allowedDevice(limit, deviceID(d)) {
			devices = append(devices, d)
		}
	}
	assignments, err := h.store.folderAssignments(ctx)
	if err != nil {
		assignments = nil
	}
	if rows, err := h.d.DeviceGroups.AllDeviceGroups(ctx); err != nil {
		h.groupLog.Warn("Failed to read device groups:", err.Error())
	} else {
		for _, group := range rows {
			if _, isFolder := folderIDFromGroupGUID(col(group, "guid")); isFolder {
				continue
			}
			if !devicegroups.GroupAllowedForUser(group, accessUser) {
				continue
			}
			ids := []string{}
			for _, id := range h.d.DeviceGroups.GroupPeerIDList(ctx, group, devices) {
				if allowedDevice(limit, id) {
					ids = append(ids, id)
				}
			}
			groups = append(groups, rustdeskGroup{guid: col(group, "guid"), name: col(group, "name"), note: or(col(group, "note"), ""), peerIDs: ids})
		}
	}
	folders, err := h.d.DeviceGroups.AllFolders(ctx)
	if err != nil {
		h.groupLog.Warn("Failed to read folders:", err.Error())
		return groups
	}
	for _, folder := range folders {
		folderID := plain(folder.Get("id"))
		guid := "folder_" + strOr(folderID)
		mirror, _ := h.d.DeviceGroups.DeviceGroupByGUID(ctx, guid)
		lists := goapi.NewObject("allowed_users", []any{}, "allowed_groups", []any{})
		if mirror != nil {
			if users, ok := plain(mirror.Get("allowed_users")).([]any); ok {
				lists.Set("allowed_users", users)
			}
			if groupsList, ok := plain(mirror.Get("allowed_groups")).([]any); ok {
				lists.Set("allowed_groups", groupsList)
			}
		}
		if !devicegroups.GroupAllowedForUser(lists, accessUser) {
			continue
		}
		ids := []string{}
		want := parseInt10(folderID)
		for _, a := range assignments {
			if parseInt10(a.folderID) == want && allowedDevice(limit, a.deviceID) {
				ids = append(ids, a.deviceID)
			}
		}
		groups = append(groups, rustdeskGroup{guid: guid, name: col(folder, "name"), note: "", peerIDs: ids})
	}
	return groups
}

type peerRef struct {
	ID string `json:"id"`
}

type team struct {
	Peers []peerRef `json:"peers"`
}

type groupPayload struct {
	GUID       string `json:"guid"`
	Name       any    `json:"name"`
	Team       team   `json:"team"`
	AccessPerm int    `json:"access_perm"`
	Note       any    `json:"note"`
	CreatedAt  string `json:"created_at"`
	Sort       int    `json:"sort"`
}

type accessibleGroupPayload struct {
	Name any    `json:"name"`
	GUID string `json:"guid"`
	Note any    `json:"note"`
	Sort int    `json:"sort"`
}

// sendDeviceGroups is sendRustDeskDeviceGroups for a route: accessible
// gives the short form of the list (the accessible route), else the long
// one.
func (h *handlers) sendDeviceGroups(accessible bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.deviceGroupList(w, r, accessible)
	}
}

func (h *handlers) deviceGroupList(w http.ResponseWriter, r *http.Request, accessible bool) {
	user := authUser(r)
	if user.isPro() {
		kit.JSON(w, r, http.StatusOK, listMsgAnswer{Data: []any{}, Msg: "success"})
		return
	}
	groups := h.deviceGroups(r.Context(), user)
	var data []any
	for i, g := range groups {
		guid := jsTrim(strOr(or(g.guid, "")))
		if accessible {
			data = append(data, accessibleGroupPayload{Name: or(g.name, ""), GUID: guid, Note: or(g.note, ""), Sort: i})
			continue
		}
		refs := make([]peerRef, len(g.peerIDs))
		for j, id := range g.peerIDs {
			refs[j] = peerRef{ID: id}
		}
		data = append(data, groupPayload{GUID: guid, Name: or(g.name, ""), Team: team{Peers: refs}, AccessPerm: 1, Note: or(g.note, ""), Sort: i})
	}
	if data == nil {
		data = []any{}
	}
	kit.JSON(w, r, http.StatusOK, listMsgAnswer{Data: data, Total: len(groups), Msg: "success"})
}

// peerList is getRustDeskPeerList.
func (h *handlers) peerList(ctx context.Context, user *tokenUser, p filterParams) (listAnswer, error) {
	folders, err := h.d.DeviceGroups.AllFolders(ctx)
	if err != nil {
		folders = nil
	}
	requestedFolder, hasFolder, err := resolveRequestedFolderID(p, folders)
	if err != nil {
		return listAnswer{}, err
	}
	requestedGroup, err := requestedGroupValue(p)
	if err != nil {
		return listAnswer{}, err
	}
	requestedTags, err := requestedTagValues(p)
	if err != nil {
		return listAnswer{}, err
	}
	search := or(p.get("search"), "")
	searchText, isText := search.(string)
	if !isText {
		// filters.search.toLowerCase() of the device layer threw
		return listAnswer{}, errors.New("filters.search.toLowerCase is not a function")
	}
	devices := h.d.Backend.GetAllDevices(ctx, goapi.DeviceFilters{Search: searchText})
	devices = h.filterDevices(ctx, user, devices)
	accessUser := h.d.DeviceGroups.UserAccessContext(ctx, user.sessionUser())

	kept := make([]*goapi.Object, 0, len(devices))
	for _, d := range devices {
		if d != nil && !truthy(d.Get("banned")) && !truthy(d.Get("disabled")) {
			kept = append(kept, d)
		}
	}
	devices = kept

	keep := func(match func(d *goapi.Object) bool) {
		out := make([]*goapi.Object, 0, len(devices))
		for _, d := range devices {
			if match(d) {
				out = append(out, d)
			}
		}
		devices = out
	}
	switch {
	case hasFolder:
		keep(func(d *goapi.Object) bool {
			id, ok := deviceFolderID(d)
			return ok && id == requestedFolder
		})
	case requestedGroup != "":
		normalized := lowerJS(jsTrim(requestedGroup))
		group, err := h.d.DeviceGroups.DeviceGroupByGUID(ctx, jsTrim(requestedGroup))
		if err == nil && group == nil {
			var all []*goapi.Object
			if all, err = h.d.DeviceGroups.AllDeviceGroups(ctx); err == nil {
				for _, g := range all {
					if lowerJS(jsTrim(strOr(or(col(g, "name"), "")))) == normalized {
						group = g
						break
					}
				}
			}
		}
		if err != nil {
			group = nil
		}
		switch {
		case group != nil && devicegroups.GroupAllowedForUser(group, accessUser):
			members := map[string]bool{}
			for _, id := range h.d.DeviceGroups.GroupPeerIDList(ctx, group, devices) {
				members[id] = true
			}
			keep(func(d *goapi.Object) bool { return members[deviceID(d)] })
		case group == nil && normalized != "":
			keep(func(d *goapi.Object) bool { return deviceHasAllTags(d, []string{normalized}) })
		default:
			devices = []*goapi.Object{}
		}
	}
	if len(requestedTags) > 0 {
		keep(func(d *goapi.Object) bool { return deviceHasAllTags(d, requestedTags) })
	}

	folderNames := map[float64]any{}
	for _, f := range folders {
		folderNames[parseInt10(plain(f.Get("id")))] = col(f, "name")
	}
	manualGroup := map[string]any{}
	for _, g := range h.deviceGroups(ctx, user) {
		if _, isFolder := folderIDFromGroupGUID(g.guid); isFolder {
			continue
		}
		for _, id := range g.peerIDs {
			if _, ok := manualGroup[id]; !ok {
				manualGroup[id] = or(g.name, "")
			}
		}
	}
	sysinfo, err := h.store.allSysinfo(ctx)
	if err != nil {
		return listAnswer{}, err
	}

	peers := make([]any, 0, len(devices))
	for _, d := range devices {
		si, hasSysinfo := sysinfo[strOr(d.Get("id"))]
		get := func(field string) any {
			if !hasSysinfo {
				return nil
			}
			switch field {
			case "hostname":
				return si.hostname
			case "username":
				return si.username
			case "platform":
				return si.platform
			}
			return si.version
		}
		groupName := any("")
		if folder, ok := deviceFolderID(d); ok && folder != 0 {
			groupName = or(folderNames[folder], "")
		} else if name, ok := manualGroup[deviceID(d)]; ok {
			groupName = or(name, "")
		}
		hostname := or(or(get("hostname"), d.Get("hostname")), "")
		username := or(or(or(get("username"), d.Get("username")), d.Get("user")), "")
		platform := or(or(or(get("platform"), d.Get("platform")), d.Get("os")), "")
		alias := or(or(or(d.Get("display_name"), ""), d.Get("note")), hostname)
		if !truthy(alias) {
			alias = strOr(or(d.Get("id"), ""))
		}
		peers = append(peers, goapi.NewObject(
			"id", d.Get("id"),
			"info", goapi.NewObject("device_name", hostname, "os", platform, "username", username, "version", or(get("version"), "")),
			"status", float64(1),
			"user", username,
			"user_name", username,
			"note", or(d.Get("note"), ""),
			"device_group_name", groupName,
			"tags", tagValues(devicegroups.NormalizeTags(d.Get("tags"))),
			"online", isReachable(d),
			"alias", alias,
			"hash", or(d.Get("hash"), ""),
		))
	}

	page := math.Max(1, orNaN(parseInt10(p.get("page")), 1))
	pageSize := math.Min(200, math.Max(1, orNaN(parseInt10(or(p.get("page_size"), p.get("pageSize"))), 100)))
	start := (page - 1) * pageSize
	paged := []any{}
	for i := start; i < start+pageSize && i < float64(len(peers)); i++ {
		paged = append(paged, peers[int(i)])
	}
	return listAnswer{Data: paged, Total: len(peers)}, nil
}

// orNaN is n || fallback for a number.
func orNaN(n, fallback float64) float64 {
	if n == 0 || math.IsNaN(n) {
		return fallback
	}
	return n
}

func tagValues(tags []string) []any {
	out := make([]any, len(tags))
	for i, t := range tags {
		out[i] = t
	}
	return out
}

// resolveRequestedFolderID is resolveRequestedFolderId: the folder the
// group parameters name, by number, by folder_<n>, or by name in any
// letter case.
func resolveRequestedFolderID(p filterParams, folders []*goapi.Object) (float64, bool, error) {
	raw, err := requestedGroupValue(p)
	if err != nil {
		return 0, false, err
	}
	if id, ok := folderIDFromGroupGUID(raw); ok {
		return id, true, nil
	}
	normalized := lowerJS(jsTrim(raw))
	if normalized == "" {
		return 0, false, nil
	}
	for _, f := range folders {
		if lowerJS(jsTrim(strOr(or(col(f, "name"), "")))) == normalized {
			id := parseInt10(plain(f.Get("id")))
			return id, isFinite(id), nil
		}
	}
	return 0, false, nil
}

// peers answers GET /api/peers with a bearer token.
func (h *handlers) peers(w http.ResponseWriter, r *http.Request) {
	user, ok := h.validate(w, r, bearerToken(r))
	if !ok {
		return
	}
	if user == nil {
		fail(w, r, http.StatusUnauthorized, msgInvalidToken)
		return
	}
	if user.isPro() {
		kit.JSON(w, r, http.StatusOK, listAnswer{Data: []any{}})
		return
	}
	result, err := h.peerList(r.Context(), user, h.filterParams(r))
	if err != nil {
		h.peersLog.Error("Error:", err.Error())
		result = listAnswer{Data: []any{}}
	}
	kit.JSON(w, r, http.StatusOK, result)
}

// peersList answers /api/peers/list for every method.
func (h *handlers) peersList(w http.ResponseWriter, r *http.Request) {
	user := authUser(r)
	if user.isPro() {
		kit.JSON(w, r, http.StatusOK, listMsgAnswer{Data: []any{}, Msg: "success"})
		return
	}
	result, err := h.peerList(r.Context(), user, h.filterParams(r))
	if err != nil {
		h.peersLog.Error("[API:PEERS/LIST] Error:", err.Error())
		result = listAnswer{Data: []any{}}
	}
	kit.JSON(w, r, http.StatusOK, listMsgAnswer{Data: result.Data, Total: result.Total, Msg: "success"})
}

// ---- Device groups ----

// bodyMap is req.body || {} as decoded JSON, {} for a list.
func bodyMap(r *http.Request) map[string]any {
	var m map[string]any
	if err := json.Unmarshal(middleware.BodyValue(r).Raw(), &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

// saveDeviceGroup answers POST /api/device-group: with a guid an update,
// else a new group; the answer is the group's row. A name, guid, note,
// team id or tag filter that is no text is refused with 400.
func (h *handlers) saveDeviceGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	payload := devicegroups.NormalizeGroupPayload(bodyMap(r))
	if payload.NotText != "" {
		fail(w, r, http.StatusBadRequest, payload.NotText+" must be text")
		return
	}
	if payload.Name == "" {
		fail(w, r, http.StatusBadRequest, "Group name is required")
		return
	}
	if payload.SourceType == "tag" && payload.TagFilter == "" {
		fail(w, r, http.StatusBadRequest, "Tag filter is required for dynamic groups")
		return
	}
	text := func(s *string, max int) jsbind.Value {
		if s == nil {
			return jsbind.Undefined
		}
		return jsbind.String(sanitize(*s, max))
	}
	var row *goapi.Object
	var err error
	if payload.GUID != "" {
		fields := payload.UpdateFields()
		row, err = h.d.DeviceGroups.UpdateDeviceGroup(ctx, payload.GUID, devicegroups.GroupData{
			Name: text(fields.Name, maxHostnameLen), SourceType: jsbind.String(*fields.SourceType), TagFilter: jsbind.String(*fields.TagFilter),
			Note: text(fields.Note, maxStringLen), TeamID: text(fields.TeamID, 64),
		})
		if err == nil && row == nil {
			fail(w, r, http.StatusNotFound, "Group not found")
			return
		}
	} else {
		fields := payload.CreateFields()
		row, err = h.d.DeviceGroups.CreateDeviceGroup(ctx, devicegroups.GroupData{
			Name: text(fields.Name, maxHostnameLen), Note: text(fields.Note, maxStringLen), TeamID: text(fields.TeamID, 64),
			SourceType: jsbind.String(*fields.SourceType), TagFilter: jsbind.String(*fields.TagFilter),
		})
	}
	if err != nil {
		h.groupLog.Error("Create/update error:", err.Error())
		fail(w, r, http.StatusInternalServerError, msgServerError)
		return
	}
	kit.JSON(w, r, http.StatusOK, row)
}

// ---- User groups ----

type userGroupItem struct {
	Name   any `json:"name"`
	GUID   any `json:"guid"`
	Note   any `json:"note"`
	TeamID any `json:"team_id"`
}

type userGroupListItem struct {
	GUID   any `json:"guid"`
	Name   any `json:"name"`
	Note   any `json:"note"`
	TeamID any `json:"team_id"`
}

// userGroup answers GET /api/user/group: the account's first user group
// with the list of all of them, or the group Default.
func (h *handlers) userGroup(w http.ResponseWriter, r *http.Request) {
	user := authUser(r)
	groups, err := h.d.DeviceGroups.UserGroupsForUser(r.Context(), user.id())
	if err != nil || len(groups) == 0 {
		kit.JSON(w, r, http.StatusOK, map[string]any{"data": goapi.NewObject("name", "Default", "guid", "default")})
		return
	}
	list := make([]any, len(groups))
	for i, g := range groups {
		list[i] = userGroupItem{Name: col(g, "name"), GUID: col(g, "guid"), Note: or(col(g, "note"), ""), TeamID: or(col(g, "team_id"), "")}
	}
	kit.JSON(w, r, http.StatusOK, map[string]any{"data": goapi.NewObject("name", col(groups[0], "name"), "guid", col(groups[0], "guid"), "groups", list)})
}

// userGroups answers GET /api/user-groups: every user group for an
// account whose role has user.view (with its overrides), the empty list
// for any other and for a pro account.
func (h *handlers) userGroups(w http.ResponseWriter, r *http.Request) {
	user := authUser(r)
	allowed, err := h.d.Access.RoleHasEffectivePermission(r.Context(), user.role(), "user.view")
	if err != nil {
		h.serverFailure(w, r, "Permission lookup failed:", err)
		return
	}
	if !allowed || user.isPro() {
		kit.JSON(w, r, http.StatusOK, listAnswer{Data: []any{}})
		return
	}
	groups, err := h.d.DeviceGroups.AllUserGroups(r.Context())
	if err != nil {
		h.log.Error("[API:USER-GROUPS] Error:", err.Error())
		kit.JSON(w, r, http.StatusOK, listAnswer{Data: []any{}})
		return
	}
	list := make([]any, len(groups))
	for i, g := range groups {
		list[i] = userGroupListItem{GUID: col(g, "guid"), Name: col(g, "name"), Note: or(col(g, "note"), ""), TeamID: or(col(g, "team_id"), "")}
	}
	kit.JSON(w, r, http.StatusOK, listAnswer{Data: list, Total: len(groups)})
}

// boundGUID is a guid from the body as the statement bound it: text as it
// is, anything else in the driver's form; err where the driver refused it.
func (h *handlers) boundGUID(ctx context.Context, v middleware.Value) (string, error) {
	args, err := jsbind.Args(h.d.Store.Dialect(), 1, jsbind.Of(v))
	if err != nil {
		return "", err
	}
	return h.store.boundText(ctx, args[0])
}

// saveUserGroup answers POST /api/user-groups: with a guid an update,
// else a new group.
func (h *handlers) saveUserGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	body := kit.Body(r)
	name, isText := member(body, "name").(string)
	if !isText || utf16Len(jsTrim(name)) == 0 {
		fail(w, r, http.StatusBadRequest, "Group name is required")
		return
	}
	note := sanitize(or(member(body, "note"), ""), maxStringLen)
	teamID := sanitize(or(member(body, "team_id"), ""), 64)
	group := devicegroups.UserGroupData{
		Name:   jsbind.String(sanitize(name, maxHostnameLen)),
		Note:   jsbind.String(note),
		TeamID: jsbind.String(teamID),
	}
	var row *goapi.Object
	var err error
	if guid := body.Get("guid"); guid.Truthy() {
		var key string
		if key, err = h.boundGUID(ctx, guid); err == nil {
			row, err = h.d.DeviceGroups.UpdateUserGroup(ctx, key, group)
		}
		if err == nil && row == nil {
			fail(w, r, http.StatusNotFound, "Group not found")
			return
		}
	} else {
		row, err = h.d.DeviceGroups.CreateUserGroup(ctx, group)
	}
	if err != nil {
		h.log.Error("[API:USER-GROUPS] Create/update error:", err.Error())
		fail(w, r, http.StatusInternalServerError, msgServerError)
		return
	}
	kit.JSON(w, r, http.StatusOK, row)
}

// ---- Devices ----

type deviceRef struct {
	ID   any `json:"id"`
	GUID any `json:"guid"`
}

type devicesAnswer struct {
	Total int         `json:"total"`
	Data  []deviceRef `json:"data"`
}

// devices answers GET /api/devices with a bearer token: one page of the
// devices of the console's table that the account may see, with their
// guids; none for a pro account. A page holds pageSize devices (50 when it
// is not a number, at most 1000) and current counts pages from 1; total
// counts the devices of every page.
func (h *handlers) devices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := authUser(r)
	if user.isPro() {
		kit.JSON(w, r, http.StatusOK, devicesAnswer{Data: []deviceRef{}})
		return
	}
	q := reqquery.Of(r)
	id := sanitize(or(q.Get("id").JSON(), ""), 64)
	size := 50.0
	if value := q.Get("pageSize"); value.Defined() {
		text, ok := value.Text()
		if !ok {
			kit.JSON(w, r, http.StatusOK, devicesAnswer{Data: []deviceRef{}})
			return
		}
		if n := parseIntText(text); !math.IsNaN(n) && n != 0 {
			size = math.Min(math.Max(n, 1), 1000)
		}
	}
	current := parseInt10(q.Get("current").JSON())
	if math.IsNaN(current) || current < 1 {
		current = 1
	}
	peers, err := h.store.allPeers(ctx)
	if err != nil {
		h.log.Error("[API:DEVICES] List error:", err.Error())
		kit.JSON(w, r, http.StatusOK, devicesAnswer{Data: []deviceRef{}})
		return
	}
	limit := h.deviceLimit(ctx, user, h.d.Backend.GetAllDevices(ctx, goapi.DeviceFilters{}))
	rows := []deviceRef{}
	for _, p := range peers {
		ref := deviceRef{ID: plain(driverValue(p.id)), GUID: orDriver(plain(driverValue(p.uuid)), "")}
		if id != "" && ref.ID != id {
			continue
		}
		if !allowedDevice(limit, strOr(or(ref.ID, ""))) {
			continue
		}
		rows = append(rows, ref)
	}
	total := len(rows)
	if start := (current - 1) * size; start >= float64(total) {
		rows = rows[:0]
	} else {
		rows = rows[int(start):int(math.Min(start+size, float64(total)))]
	}
	for i := range rows {
		if truthy(rows[i].GUID) {
			continue
		}
		if guid, err := h.store.resolvePeerAssignmentKey(ctx, strOr(or(rows[i].ID, ""))); err == nil {
			rows[i].GUID = guid
		}
	}
	kit.JSON(w, r, http.StatusOK, devicesAnswer{Total: total, Data: rows})
}

type peerKeyAnswer struct {
	ID any `json:"id"`
	PK any `json:"pk"`
}

// buffer is a Buffer as JSON.stringify wrote it.
type buffer []byte

func (b buffer) MarshalJSON() ([]byte, error) {
	data := make([]int, len(b))
	for i, c := range b {
		data[i] = int(c)
	}
	return json.Marshal(struct {
		Type string `json:"type"`
		Data []int  `json:"data"`
	}{"Buffer", data})
}

// peerKey answers GET /api/peer-key/:id: the device's public key, "" when
// it has none or is unknown; nothing for a pro account.
func (h *handlers) peerKey(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("id")
	id := sanitize(raw, maxIDLen)
	if authUser(r).isPro() {
		kit.JSON(w, r, http.StatusOK, peerKeyAnswer{ID: or(id, raw), PK: ""})
		return
	}
	if id == "" {
		fail(w, r, http.StatusBadRequest, "Invalid peer ID")
		return
	}
	device, err := h.store.peerByID(r.Context(), id)
	if err != nil {
		h.log.Error("[API:PEER-KEY] Error:", err.Error())
		kit.JSON(w, r, http.StatusOK, peerKeyAnswer{ID: raw, PK: ""})
		return
	}
	if device == nil {
		kit.JSON(w, r, http.StatusOK, peerKeyAnswer{ID: id, PK: ""})
		return
	}
	var pk any = ""
	switch key := device.pk.(type) {
	case []byte:
		pk = buffer(key)
	case string:
		if key != "" {
			pk = key
		}
	case nil:
	default:
		pk = key
	}
	kit.JSON(w, r, http.StatusOK, peerKeyAnswer{ID: plain(driverValue(device.id)), PK: pk})
}

// lowerJS is String.prototype.toLowerCase.
func lowerJS(s string) string { return jsbind.ToLower(s) }
