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
// acknowledged filters as soon as the query names it: "true" asks for the
// acknowledged alerts and every other value for the rest. A limit that is
// 0 or no number does not limit.
func (h *handlers) alerts(w http.ResponseWriter, r *http.Request) {
	filter := alertFilter{DeviceID: queryValue(r, "device_id"), Severity: queryValue(r, "severity")}
	if acknowledged := queryValue(r, "acknowledged"); !acknowledged.IsUndefined() {
		filter.Acknowledged = flag(h.db, acknowledged.IsString() && acknowledged.Str() == "true")
	}
	max, limited, err := limit(r)
	var alerts []record
	if err == nil {
		if limited {
			filter.Limit = jsbind.Number(max)
		}
		alerts, err = listAlerts(r.Context(), h.db, filter)
	}
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
