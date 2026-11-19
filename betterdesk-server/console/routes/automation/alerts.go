package automation

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
)

type alertList struct {
	Alerts []record `json:"alerts"`
	Total  int      `json:"total"`
}

// alerts answers GET alerts?device_id=&severity=&acknowledged=&limit=:
// the alert history with the name of each alert's rule, the alert
// triggered last first.
//
// Each filter is one text, and an empty one does not filter. acknowledged
// is "true" for the acknowledged alerts and "false" for the rest. The
// limit is limitOf's. Anything else is refused with 400.
func (h *handlers) alerts(w http.ResponseWriter, r *http.Request) {
	var filter alertFilter
	if !readFilters(w, r, filterInto{"device_id", &filter.DeviceID}, filterInto{"severity", &filter.Severity}) {
		return
	}
	if text, given, err := jsbind.QueryText(r.URL.RawQuery, "acknowledged"); given {
		if err != nil || text != "true" && text != "false" {
			fail(w, r, http.StatusBadRequest, "Invalid acknowledged")
			return
		}
		filter.Acknowledged = flag(h.db, text == "true")
	}
	limit, ok := limitOf(r)
	if !ok {
		fail(w, r, http.StatusBadRequest, "Invalid limit")
		return
	}
	filter.Limit = limit
	alerts, err := listAlerts(r.Context(), h.db, filter)
	if err != nil {
		h.serverError(w, r, "Alerts list error", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, alertList{Alerts: alerts, Total: len(alerts)})
}

// acknowledge answers POST alerts/{id}/ack: the alert is marked as
// acknowledged by the account, now. An alert that does not exist is no
// error, and an alert can be acknowledged again by someone else.
func (h *handlers) acknowledge(w http.ResponseWriter, r *http.Request) {
	username := kit.User(r).Username
	if err := acknowledgeAlert(r.Context(), h.db, number(r, "id"), username); err != nil {
		h.serverError(w, r, "Ack error", err)
		return
	}
	h.log.Info("Alert #" + r.PathValue("id") + " acknowledged by " + username)
	kit.JSON(w, r, http.StatusOK, done{Success: true})
}
