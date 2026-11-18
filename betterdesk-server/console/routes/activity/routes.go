// Package activity holds the activity routes. The console mounts the
// group twice, at /api/bd and at /api/activity, so each route answers at
// both:
//
//   - The route the desktop agent reports its app usage with, POST
//     activity, behind the device guards (device.go).
//   - The routes people read app usage with: the summaries devices
//     reported, one device's sessions and summaries, and its most used
//     apps. All three need the permission audit.view, and all three step
//     aside at /api/bd for the routes of other groups there.
package activity

import (
	"math"
	"net/http"
	"strings"

	"github.com/unitronix/betterdesk-server/console/alerts"
	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/devicemount"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsjson"
	"github.com/unitronix/betterdesk-server/console/store"
)

type handlers struct {
	db     *store.DB
	alerts *alerts.Engine
	log    *kit.Logger
}

// Register adds the group's routes below the mount.
func Register(d *kit.Deps) {
	h := &handlers{db: d.Store, alerts: d.Alerts, log: d.Log.Child("Activity")}
	r := d.Router
	canRead := d.RequirePermission("audit.view")
	r.Post("/activity", h.report, d.RequireDeviceToken, d.RequireTokenDeviceMatch)
	r.Get("/", h.list, devicemount.Not, d.RequireAuth, canRead)
	r.Get("/{id}", h.detail, devicemount.Not, d.RequireAuth, canRead)
	r.Get("/{id}/top", h.top, devicemount.Not, d.RequireAuth, canRead)
}

// failure is the group's error answer: {"error":"..."}.
type failure struct {
	Error string `json:"error"`
}

// serverError answers what every route's catch block answered.
func (h *handlers) serverError(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.log.Error(what+":", err)
	kit.JSON(w, r, http.StatusInternalServerError, failure{Error: "Internal server error"})
}

// window is the from and to of a request, as the query gave them: text,
// a list or an object. The query ends at a "#".
func window(r *http.Request) (from, to jsbind.Value) {
	query, _, _ := strings.Cut(r.URL.RawQuery, "#")
	return jsbind.Query(query, "from"), jsbind.Query(query, "to")
}

// limit is "limit ? parseInt(limit, 10) : fallback" for the limit of a
// request. It fails where JavaScript could not make text of the value.
func limit(r *http.Request, fallback float64) (float64, error) {
	value := goapipath.Query(r, "limit")
	if !value.Truthy() {
		return fallback, nil
	}
	text, err := value.Text()
	if err != nil {
		return 0, err
	}
	return parseInt10(text), nil
}

// parseInt10 is parseInt(text, 10): the whole number the text starts
// with, after white space and a sign, and NaN when it starts with none.
// "0x10" is 0, where parseInt without a base reads a hexadecimal number.
func parseInt10(text string) float64 {
	digits := strings.TrimLeftFunc(text, jsbind.IsSpace)
	if digits != "" && (digits[0] == '+' || digits[0] == '-') {
		digits = digits[1:]
	}
	if len(digits) > 1 && digits[0] == '0' && (digits[1] == 'x' || digits[1] == 'X') {
		return 0
	}
	return goapipath.ParseInt(text)
}

type summaryList struct {
	Summaries []listed `json:"summaries"`
	Total     int      `json:"total"`
}

// listed is a summary in the list of all devices.
type listed struct {
	DeviceID        any `json:"device_id"`
	Hostname        any `json:"hostname"`
	IdleSeconds     any `json:"idle_seconds"`
	SessionCount    any `json:"session_count"`
	TotalActiveSecs any `json:"total_active_secs"`
	DetailCount     any `json:"detail_count"`
	ReportedAt      any `json:"reported_at"`
	ReceivedAt      any `json:"received_at"`
}

// list answers GET /api/activity?from=&to=: every summary whose report
// time is in the window, newest first, with the name of its device.
func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	from, to := window(r)
	summaries, err := allSummaries(r.Context(), h.db, from, to)
	if err != nil {
		h.serverError(w, r, "List error", err)
		return
	}
	out := make([]listed, len(summaries))
	for i, s := range summaries {
		id := s.text("device_id")
		out[i] = listed{
			DeviceID:        s.value("device_id"),
			Hostname:        h.hostname(r, id),
			IdleSeconds:     s.value("idle_seconds"),
			SessionCount:    s.value("session_count"),
			TotalActiveSecs: s.value("total_active_secs"),
			DetailCount:     s.value("detail_count"),
			ReportedAt:      s.value("reported_at"),
			ReceivedAt:      s.value("received_at"),
		}
	}
	kit.JSON(w, r, http.StatusOK, summaryList{Summaries: out, Total: len(out)})
}

// hostname is the name a device is shown under: the note the console has
// for it, else the hostname in its info, else its id. A device the
// console cannot look up, or whose info it cannot read, has its id. The
// hostname in an info can be any JSON value and goes out as it is.
func (h *handlers) hostname(r *http.Request, id string) any {
	fallback := jsjson.Quoted(id)
	p, err := h.peerByID(r.Context(), id)
	if err != nil || p == nil {
		return fallback
	}
	info, err := p.parsedInfo()
	switch {
	case err != nil:
		return fallback
	case p.note != "":
		return jsjson.Quoted(p.note)
	}
	if name := info.Get("hostname"); name.Truthy() {
		return name
	}
	return fallback
}

type deviceDetail struct {
	DeviceID     jsjson.Quoted `json:"device_id"`
	Hostname     any           `json:"hostname"`
	Sessions     []record      `json:"sessions"`
	Summaries    []record      `json:"summaries"`
	SessionCount int           `json:"session_count"`
}

// detail answers GET /api/activity/{id}?from=&to=&limit=: the device's
// sessions that lie in the window, newest first and 100 at most unless
// limit says otherwise, and its summaries reported in the window.
func (h *handlers) detail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	from, to := window(r)
	max, err := limit(r, 100)
	var sessions, summaries []record
	if err == nil {
		sessions, err = deviceSessions(r.Context(), h.db, id, from, to, max)
	}
	if err == nil {
		summaries, err = deviceSummaries(r.Context(), h.db, id, from, to)
	}
	if err != nil {
		h.serverError(w, r, "Detail error", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, deviceDetail{
		DeviceID:     jsjson.Quoted(id),
		Hostname:     h.hostname(r, id),
		Sessions:     sessions,
		Summaries:    summaries,
		SessionCount: len(sessions),
	})
}

type topApps struct {
	DeviceID jsjson.Quoted `json:"device_id"`
	TopApps  []record      `json:"top_apps"`
}

// top answers GET /api/activity/{id}/top?from=&to=&limit=: the apps of
// the device's sessions in the window with the time spent in each, the
// most used first, 10 unless limit says otherwise.
func (h *handlers) top(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	from, to := window(r)
	max, err := limit(r, 10)
	var apps []record
	if err == nil {
		apps, err = topAppsOf(r.Context(), h.db, id, from, to, max)
	}
	if err != nil {
		h.serverError(w, r, "Top apps error", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, topApps{DeviceID: jsjson.Quoted(id), TopApps: apps})
}

// counts reports whether a limit limits: "if (limit)" for a number.
func counts(limit float64) bool { return limit != 0 && !math.IsNaN(limit) }
