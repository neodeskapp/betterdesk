package settings

import (
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/lifecycle"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsreq"
)

// Settings, Sign-in, "Disable inactive accounts": accounts that have not
// signed in for the chosen number of days are disabled by the job of the
// users routes, after a notice to the person a week before. 0 is off,
// which it is until an admin turns it on. The server keeps the number
// (server_config, lifecycle.InactiveDaysKey).

type inactiveData struct {
	Days       int `json:"days"`
	NoticeDays int `json:"notice_days"`
	MinDays    int `json:"min_days"`
	MaxDays    int `json:"max_days"`
}

// getInactive answers GET /api/settings/inactive-accounts.
func (g *general) getInactive(w http.ResponseWriter, r *http.Request) {
	value := jsreq.String(sessionConfig(g.d.GoAPI.GetConfig(r.Context(), lifecycle.InactiveDaysKey)))
	kit.OK(w, r, inactiveData{Days: lifecycle.ParseInactiveDays(value), NoticeDays: lifecycle.InactiveNoticeDays,
		MinDays: lifecycle.MinInactiveDays, MaxDays: lifecycle.MaxInactiveDays})
}

// setInactive answers PUT /api/settings/inactive-accounts with {days}: 0
// turns the rule off, a whole number of days from MinInactiveDays to
// MaxInactiveDays turns it on.
func (g *general) setInactive(w http.ResponseWriter, r *http.Request) {
	body := jsreq.Body(r)
	if refuseNothingToSave(w, r, body, "days") {
		return
	}
	days, ok := jsreq.Prop(body, "days").(float64)
	if !ok || days != math.Trunc(days) || (days != 0 && (days < lifecycle.MinInactiveDays || days > lifecycle.MaxInactiveDays)) {
		kit.JSON(w, r, http.StatusBadRequest, failure{Error: kit.T(r, "settings.inactive_days_invalid", kit.Vars("min", lifecycle.MinInactiveDays, "max", lifecycle.MaxInactiveDays))})
		return
	}
	n := int(days)
	if result, ok := g.setConfigs(r, []configPair{{lifecycle.InactiveDaysKey, strconv.Itoa(n)}}); !ok {
		kit.JSON(w, r, http.StatusInternalServerError, failure{Error: orText(result.Get("error"), "Failed to save the inactive accounts rule")})
		return
	}
	details := "Disable accounts that have not signed in: off"
	if n > 0 {
		details = "Disable accounts that have not signed in for " + strconv.Itoa(n) + " days"
	}
	if err := g.d.LogAction(r, "inactive_policy_changed", strings.TrimSpace(details)); err != nil {
		g.serverError(w, r, "Save inactive accounts error", err)
		return
	}
	kit.OK(w, r, nil)
}
