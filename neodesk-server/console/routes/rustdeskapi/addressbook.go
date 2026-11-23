package rustdeskapi

import (
	"context"
	"fmt"
	"net/http"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/devicegroups"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// RustDesk clients keep their address book on the server: the data is a
// JSON text inside the JSON of the request and of the answer, which is
// stored as it came. What a client reads back is merged with the devices
// of the console (devicegroups.MergeAddressBookData) and cut to the
// caller's device scope.

// addressBookPeerIDs is getAddressBookPeerIds: the ids of the peers of the
// account's legacy and personal address books. A book that cannot be read
// adds the ids read before the failure.
func (h *handlers) addressBookPeerIDs(ctx context.Context, user *tokenUser) map[string]bool {
	ids := map[string]bool{}
	if user == nil || user.id() == 0 {
		return ids
	}
	for _, abType := range []string{"legacy", "personal"} {
		data, _, err := h.d.DeviceGroups.AddressBook(ctx, user.id(), abType)
		if err != nil {
			h.ab.Warn(fmt.Sprintf("Failed to read %s address book for user %s:", abType, user.Username), err.Error())
			continue
		}
		parsed := devicegroups.ParseAddressBookData(data)
		peers, _ := parsed.Get("peers").([]any)
		for _, p := range peers {
			var raw any = undefined{}
			if peer, ok := p.(*goapi.Object); ok {
				raw = or(peer.Get("id"), "")
				if !peer.Has("id") {
					raw = ""
				}
			} else if p != nil {
				raw = ""
			}
			if isUndefined(raw) {
				raw = ""
			}
			text, err := str(raw)
			if err != nil {
				h.ab.Warn(fmt.Sprintf("Failed to read %s address book for user %s:", abType, user.Username), err.Error())
				break
			}
			if id := jsTrim(text); id != "" {
				ids[id] = true
			}
		}
	}
	return ids
}

// filterDevices is filterDevicesForRustDeskUser: the devices of the
// account's device scope, and for an account that may not browse the
// inventory only those in its address books. A failure of the scope keeps
// every device.
func (h *handlers) filterDevices(ctx context.Context, user *tokenUser, devices []*goapi.Object) []*goapi.Object {
	scoped := devices
	if scope, err := h.d.DeviceGroups.DeviceScopeForUser(ctx, user.sessionUser(), devices); err != nil {
		h.peersLog.Warn(fmt.Sprintf("Failed to apply device group scope for %s:", user.Username), err.Error())
	} else {
		scoped = access.FilterByScope(devices, scope, deviceID)
	}
	return h.inAddressBooks(ctx, user, scoped)
}

// inAddressBooks keeps every device for an account that may browse the
// inventory, and for any other only the devices of its address books.
func (h *handlers) inAddressBooks(ctx context.Context, user *tokenUser, devices []*goapi.Object) []*goapi.Object {
	if canBrowseDeviceInventory(user) {
		return devices
	}
	allowed := h.addressBookPeerIDs(ctx, user)
	if len(allowed) == 0 {
		return []*goapi.Object{}
	}
	out := []*goapi.Object{}
	for _, d := range devices {
		if allowed[deviceID(d)] {
			out = append(out, d)
		}
	}
	return out
}

// deviceLimit is the set of device ids among devices that the account may
// see through the client API, nil when it may see every device: its device
// scope, and for an account that may not browse the inventory the devices
// of its address books. Where filterDevices keeps every device when the
// scope cannot be read, deviceLimit keeps none.
func (h *handlers) deviceLimit(ctx context.Context, user *tokenUser, devices []*goapi.Object) map[string]bool {
	scope, err := h.d.DeviceGroups.DeviceScopeForUser(ctx, user.sessionUser(), devices)
	if err != nil {
		h.peersLog.Warn(fmt.Sprintf("Failed to apply device group scope for %s:", user.Username), err.Error())
		return map[string]bool{}
	}
	if scope.Unlimited() && canBrowseDeviceInventory(user) {
		return nil
	}
	keep := map[string]bool{}
	for _, d := range h.inAddressBooks(ctx, user, access.FilterByScope(devices, scope, deviceID)) {
		keep[deviceID(d)] = true
	}
	return keep
}

// allowedDevice reports whether a device id is in a deviceLimit.
func allowedDevice(limit map[string]bool, id string) bool {
	return limit == nil || limit[id]
}

// deviceID is String(device.id).
func deviceID(d *goapi.Object) string {
	if d == nil {
		return "undefined"
	}
	if !d.Has("id") {
		return "undefined"
	}
	return strOr(d.Get("id"))
}

// visibleDevices is the devices part of getConsoleDeviceContext: every
// device of the device layer, cut to the account's scope. The folders and
// assignments it also read played no part in what its callers answered.
func (h *handlers) visibleDevices(ctx context.Context, user *tokenUser) []*goapi.Object {
	return h.filterDevices(ctx, user, h.d.Backend.GetAllDevices(ctx, goapi.DeviceFilters{}))
}

func anyList(devices []*goapi.Object) []any {
	out := make([]any, len(devices))
	for i, d := range devices {
		out[i] = d
	}
	return out
}

// syncedAddressBook is buildSyncedAddressBook: the stored data as it is
// for a pro account; for the others the data merged with the visible
// devices (no device is added, banned and deleted ones leave) and without
// the server's devices outside the account's scope.
func (h *handlers) syncedAddressBook(ctx context.Context, user *tokenUser, abType string) (string, error) {
	stored, _, err := h.d.DeviceGroups.AddressBook(ctx, user.id(), abType)
	if err != nil {
		return "", err
	}
	data := "{}"
	if truthy(stored) {
		data = strOr(stored)
	}
	if user.isPro() {
		return data, nil
	}
	devices := h.visibleDevices(ctx, user)
	merged := jsJSON(devicegroups.MergeAddressBookData(data, devicegroups.MergeOptions{Devices: anyList(devices), KnownOnly: true}))

	all := h.d.Backend.GetAllDevices(ctx, goapi.DeviceFilters{})
	scope, err := h.d.DeviceGroups.DeviceScopeForUser(ctx, user.sessionUser(), all)
	if err != nil {
		h.ab.Warn(fmt.Sprintf("Failed to apply device scope to address book for %s:", user.Username), err.Error())
		return merged, nil
	}
	if scope != nil {
		known := make([]any, len(all))
		for i, d := range all {
			if d != nil {
				known[i] = d.Get("id")
			}
		}
		merged = jsJSON(devicegroups.FilterAddressBookPeersByScope(merged, scope.Has, known))
	}
	return merged, nil
}

// syncedTags is getSyncedAddressBookTags: for a pro account the tags of
// its legacy address book, else the tags of the visible devices with the
// address book's own added, in either case trimmed, without empty ones
// and sorted.
func (h *handlers) syncedTags(ctx context.Context, user *tokenUser) []string {
	if user.isPro() {
		raw, err := h.d.DeviceGroups.AddressBookTags(ctx, user.id())
		list, isList := raw.([]any)
		if err != nil || !isList {
			return []string{}
		}
		tags := []string{}
		for _, tag := range list {
			text, err := str(or(tag, ""))
			if err != nil {
				return []string{}
			}
			if text = jsTrim(text); text != "" {
				tags = append(tags, text)
			}
		}
		devicegroups.SortLikeLocaleCompare(tags)
		return tags
	}
	tags := devicegroups.CollectVisibleTags(anyList(h.visibleDevices(ctx, user)))
	seen := map[string]bool{}
	for _, tag := range tags {
		if key := lowerJS(jsTrim(tag)); key != "" {
			seen[key] = true
		}
	}
	raw, err := h.d.DeviceGroups.AddressBookTags(ctx, user.id())
	if err == nil {
		for _, tag := range iterate(raw) {
			text, err := str(or(tag, ""))
			if err != nil {
				break
			}
			value := jsTrim(text)
			key := lowerJS(value)
			if key != "" && !seen[key] {
				seen[key] = true
				tags = append(tags, value)
			}
		}
	}
	devicegroups.SortLikeLocaleCompare(tags)
	return tags
}

// iterate is what for...of visits of a value: the items of a list, the
// characters of a text; a value that cannot be iterated visits nothing
// (the loop threw, and the caller ignored that).
func iterate(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case string:
		out := make([]any, 0, utf8.RuneCountInString(x))
		for _, r := range x {
			out = append(out, string(r))
		}
		return out
	}
	return nil
}

// syncTags is syncAddressBookTagsToConsole: for an account that may
// change devices (by the built-in role map), the tags of every peer of the
// book with a valid device id go to the device on the server.
func (h *handlers) syncTags(ctx context.Context, user *tokenUser, data, abType string) {
	if !canSyncDeviceTags(user) {
		return
	}
	synced := 0
	for _, update := range devicegroups.CollectPeerTagUpdates(data) {
		if !isValidDeviceID(update.ID) {
			continue
		}
		result := h.d.Backend.SetPeerTags(ctx, update.ID, update.Tags)
		if result != nil && result.Get("success") != false {
			synced++
		}
	}
	if synced > 0 {
		h.ab.Info(fmt.Sprintf("Synced %d peer tag set(s) from %s address book for user %s", synced, abType, user.Username))
	}
}

type legacyBook struct {
	Data            string `json:"data"`
	LicensedDevices int    `json:"licensed_devices"`
}

type personalBook struct {
	Data string `json:"data"`
}

// readAddressBook answers GET /api/ab and GET /api/ab/personal.
func (h *handlers) readAddressBook(abType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := h.tokenOrFail(w, r)
		if user == nil {
			return
		}
		data, err := h.syncedAddressBook(r.Context(), user, abType)
		if err != nil {
			h.ab.Error(fmt.Sprintf("Error reading %s address book:", abType), err.Error())
			data = "{}"
		}
		if abType == "personal" {
			kit.JSON(w, r, http.StatusOK, personalBook{Data: data})
			return
		}
		kit.JSON(w, r, http.StatusOK, legacyBook{Data: data})
	}
}

// saveAddressBook answers POST /api/ab and POST /api/ab/personal. The data
// is stored as the client sent it: a text as it is, anything else as its
// JSON. The legacy book takes any data but none; the personal book
// answers an empty probe (no data, null or "") with 404 and no body, as
// RustDesk 1.4.7 expects of a server without one.
func (h *handlers) saveAddressBook(abType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := h.tokenOrFail(w, r)
		if user == nil {
			return
		}
		data := member(kit.Body(r), "data")
		if abType == "personal" && (isUndefined(data) || data == nil || data == "") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if !isUndefined(data) {
			text, isText := data.(string)
			if !isText {
				text = stringify(data)
			}
			ctx := r.Context()
			if err := h.d.DeviceGroups.SaveAddressBook(ctx, user.id(), abType, text); err != nil {
				h.ab.Error(fmt.Sprintf("Error saving %s address book:", abType), err.Error())
			} else {
				h.syncTags(ctx, user, text, abType)
				h.ab.Info(fmt.Sprintf("Saved %s address book for user %s (%d bytes)", abType, user.Username, utf16Len(text)))
			}
		}
		kit.JSON(w, r, http.StatusOK, struct{}{})
	}
}

type tagsAnswer struct {
	Data []string `json:"data"`
}

// addressBookTags answers GET /api/ab/tags.
func (h *handlers) addressBookTags(w http.ResponseWriter, r *http.Request) {
	user := h.tokenOrFail(w, r)
	if user == nil {
		return
	}
	kit.JSON(w, r, http.StatusOK, tagsAnswer{Data: h.syncedTags(r.Context(), user)})
}

// jsJSON writes JSON text again the way JSON.stringify writes it. The
// address book functions of devicegroups encode with encoding/json, which
// writes -0 as "-0" and escapes U+2028 and U+2029 where JSON.stringify does
// not; a client gets back the bytes Node sent.
func jsJSON(text string) string {
	v, err := goapi.Parse([]byte(text))
	if err != nil {
		return text
	}
	return stringify(v)
}
