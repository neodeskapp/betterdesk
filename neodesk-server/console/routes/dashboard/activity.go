package dashboard

import (
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// feedLength is how many events the activity feed shows.
const feedLength = 10

type activityAnswer struct {
	Success bool            `json:"success"`
	Events  []*goapi.Object `json:"events"`
}

// activity answers GET /api/dashboard/activity: the ten newest entries of
// the server's audit log, and when it has none, of the console's own. An
// account limited to some devices gets none of the server's entries about
// a device outside its scope; entries about something that is no device,
// such as a setting, stay.
func (h *handlers) activity(w http.ResponseWriter, r *http.Request) {
	scope, err := h.scope(r)
	if err != nil {
		h.log.Error("Activity feed: device scope error:", err)
		serverError(w, r)
		return
	}
	all := serverEvents(h.d.GoAPI.GetAuditEvents(r.Context(), float64(feedLength)))
	hidden := h.devicesOutside(r, scope)
	events := make([]*goapi.Object, 0, len(all))
	for _, event := range all {
		if !hidden[event.Str("device_id")] {
			events = append(events, event)
		}
	}
	if len(all) == 0 {
		rows, err := recentAuditRows(r.Context(), h.d.Store, feedLength)
		if err != nil {
			h.log.Warn("Activity feed: the console's audit log could not be read:", err)
		}
		for _, row := range rows {
			events = append(events, consoleEvent(row))
		}
	}
	kit.JSON(w, r, http.StatusOK, activityAnswer{Success: true, Events: events})
}

// devicesOutside is the set of ids of the devices, deleted ones included,
// that are outside a limited scope; nil for a scope without a limit.
func (h *handlers) devicesOutside(r *http.Request, scope *access.DeviceScope) map[string]bool {
	if scope == nil || h.d.Backend == nil {
		return nil
	}
	out := map[string]bool{}
	for _, device := range h.d.Backend.GetAllDevices(r.Context(), goapi.DeviceFilters{IncludeDeleted: true}) {
		if id := device.Str("id"); id != "" && !scope.Has(id) {
			out[id] = true
		}
	}
	return out
}

// or is JavaScript's a || b || ...: the first value that is truthy, else
// the last one.
func or(values ...any) any {
	for _, v := range values[:len(values)-1] {
		if goapi.Truthy(v) {
			return v
		}
	}
	return values[len(values)-1]
}

// serverEvents turns the server's answer to the audit request into
// events: the first ten entries that are not null of the list under
// data.events, or data.entries in the older form, or of the answer's
// data when that is a list itself.
func serverEvents(audit *goapi.Object) []*goapi.Object {
	events := []*goapi.Object{}
	var picked any
	switch data := audit.Get("data").(type) {
	case *goapi.Object:
		picked = or(data.Get("events"), data.Get("entries"))
	case []any:
		picked = data
	}
	entries, _ := or(picked, audit.Get("events"), audit.Get("entries")).([]any)
	for _, entry := range entries {
		if len(events) == feedLength {
			break
		}
		if entry == nil {
			continue
		}
		details := goapi.Dig(entry, "details")
		event := goapi.NewObject(
			"action", or(goapi.Dig(entry, "action"), "info"),
			"action_label", or(goapi.Dig(entry, "action"), "Event"),
			"device_id", or(goapi.Dig(entry, "peer_id"), goapi.Dig(details, "peer_id"), goapi.Dig(entry, "target"), ""),
			"details", or(goapi.Dig(details, "message"), goapi.Dig(details, "reason"), ""),
		)
		// The time is left out when the entry has neither field
		if timestamp := goapi.Dig(entry, "timestamp"); goapi.Truthy(timestamp) {
			event.Set("timestamp", timestamp)
		} else if object, ok := entry.(*goapi.Object); ok && object.Has("created_at") {
			event.Set("timestamp", object.Get("created_at"))
		}
		events = append(events, event)
	}
	return events
}

// consoleEvent is an entry of the console's own audit log as an event.
// An entry without a time has no timestamp.
func consoleEvent(row auditRow) *goapi.Object {
	details := row.Details.String
	if details == "" {
		details = row.IPAddress.String
	}
	event := goapi.NewObject(
		"action", or(row.Action, "info"),
		"action_label", or(row.Action, "Event"),
		"device_id", "",
		"details", details,
	)
	if row.CreatedAt.String() != "" {
		event.Set("timestamp", row.CreatedAt)
	}
	return event
}
