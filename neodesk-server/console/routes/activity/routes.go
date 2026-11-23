// Package activity holds the activity routes. The console mounts the
// group twice, at /api/nd and at /api/activity, so each route answers at
// both:
//
//   - The route the desktop agent reports its app usage with, POST
//     activity, behind the device guards (device.go).
//   - The routes people read app usage with: the summaries devices
//     reported, one device's sessions and summaries, and its most used
//     apps. All three need the permission audit.view and keep to the
//     account's device scope, and all three step aside at /api/nd for the
//     routes of other groups there.
package activity

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/alerts"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/devicemount"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsjson"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

type handlers struct {
	db     *store.DB
	alerts *alerts.Engine
	log    *kit.Logger
	// scope is the device scope of the account of a request, nil when it
	// sees every device
	scope func(r *http.Request) (*access.DeviceScope, error)
}

// Register adds the group's routes below the mount.
func Register(d *kit.Deps) {
	h := &handlers{db: d.Store, alerts: d.Alerts, log: d.Log.Child("Activity")}
	h.scope = func(r *http.Request) (*access.DeviceScope, error) {
		ctx := r.Context()
		return d.DeviceGroups.LimitedScope(ctx, kit.User(r), func() []*goapi.Object {
			return d.Backend.GetAllDevices(ctx, goapi.DeviceFilters{})
		})
	}
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

// inScope answers 403 and reports false when the device is outside the
// account's scope; a failed check is answered as the route's what.
func (h *handlers) inScope(w http.ResponseWriter, r *http.Request, what, id string) bool {
	scope, err := h.scope(r)
	if err != nil {
		h.serverError(w, r, what, err)
		return false
	}
	if !scope.Has(id) {
		kit.JSON(w, r, http.StatusForbidden, failure{Error: kit.T(r, "errors.forbidden", nil)})
		return false
	}
	return true
}

// badQuery answers 400 for a parameter of the query the route cannot use.
func badQuery(w http.ResponseWriter, r *http.Request, name string) {
	kit.JSON(w, r, http.StatusBadRequest, failure{Error: "Invalid " + name})
}

// queryText is the text of a parameter of the query, "" when the query
// has none. ok is false when the query gives it more than once, or as a
// list or an object.
func queryText(r *http.Request, name string) (text string, ok bool) {
	text, _, err := jsbind.QueryText(r.URL.RawQuery, name)
	return text, err == nil
}

// maxRows is the largest limit of the device routes (the web app asks
// for 500 top apps), and the most summaries the list gives.
const maxRows = 500

// limitOf reads the limit of a request: fallback when the query has none
// or an empty one, ok false for anything but a whole number from 1 to
// maxRows written in digits.
func limitOf(r *http.Request, fallback int) (int, bool) {
	text, ok := queryText(r, "limit")
	switch {
	case !ok:
		return 0, false
	case text == "":
		return fallback, true
	case strings.Trim(text, "0123456789") != "" || len(text) > 4:
		return 0, false
	}
	n, err := strconv.Atoi(text)
	return n, err == nil && n >= 1 && n <= maxRows
}

// span is the time window of a request, both ends in it. An end that is
// not set does not narrow the window.
type span struct {
	from, to       time.Time
	hasFrom, hasTo bool
}

// spanOf reads from and to of a request: a date, or a date and time as
// the console and the web app write them, with or without a zone (none is
// UTC). A date alone as to stands for the last millisecond of that day.
// bad names the parameter that is no time, "" when both are fine.
func spanOf(r *http.Request) (s span, bad string) {
	for _, end := range []struct {
		name  string
		at    *time.Time
		has   *bool
		isEnd bool
	}{{"from", &s.from, &s.hasFrom, false}, {"to", &s.to, &s.hasTo, true}} {
		text, ok := queryText(r, end.name)
		if !ok {
			return span{}, end.name
		}
		if text == "" {
			continue
		}
		at, day, ok := timeOf(text)
		if !ok {
			return span{}, end.name
		}
		if day && end.isEnd {
			at = at.Add(24*time.Hour - time.Millisecond)
		}
		*end.at, *end.has = at, true
	}
	return s, ""
}

// timeOf reads a date (day true) or a date and time, in the years 1 to
// 9999 in UTC, which both databases read.
func timeOf(text string) (at time.Time, day, ok bool) {
	if at, err := time.Parse(time.DateOnly, text); err == nil {
		return at, true, at.Year() >= 1
	}
	at, ok = store.ParseTime(text)
	year := at.UTC().Year()
	return at, false, ok && year >= 1 && year <= 9999
}

type summaryList struct {
	Summaries []listed `json:"summaries"`
	Total     int      `json:"total"`
}

// listed is a summary in the list of all devices.
type listed struct {
	DeviceID        any           `json:"device_id"`
	Hostname        jsjson.Quoted `json:"hostname"`
	IdleSeconds     any           `json:"idle_seconds"`
	SessionCount    any           `json:"session_count"`
	TotalActiveSecs any           `json:"total_active_secs"`
	DetailCount     any           `json:"detail_count"`
	ReportedAt      any           `json:"reported_at"`
	ReceivedAt      any           `json:"received_at"`
}

// list answers GET /api/activity?from=&to=: the summaries whose report
// time is in the window, the maxRows received last, with the name of
// their device. An account limited to some devices gets only theirs.
func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	window, bad := spanOf(r)
	if bad != "" {
		badQuery(w, r, bad)
		return
	}
	scope, err := h.scope(r)
	if err != nil {
		h.serverError(w, r, "List error", err)
		return
	}
	summaries, err := allSummaries(r.Context(), h.db, window, scope, maxRows)
	if err != nil {
		h.serverError(w, r, "List error", err)
		return
	}
	out := make([]listed, 0, len(summaries))
	for _, s := range summaries {
		id := s.text("device_id")
		out = append(out, listed{
			DeviceID:        s.value("device_id"),
			Hostname:        h.hostname(r, id),
			IdleSeconds:     s.value("idle_seconds"),
			SessionCount:    s.value("session_count"),
			TotalActiveSecs: s.value("total_active_secs"),
			DetailCount:     s.value("detail_count"),
			ReportedAt:      s.value("reported_at"),
			ReceivedAt:      s.value("received_at"),
		})
	}
	kit.JSON(w, r, http.StatusOK, summaryList{Summaries: out, Total: len(out)})
}

// hostname is the name a device is shown under: the note the console has
// for it, else the hostname in its info when that is text, else its id.
// A device the console cannot look up has its id, and so has one without
// a note whose info cannot be read.
func (h *handlers) hostname(r *http.Request, id string) jsjson.Quoted {
	fallback := jsjson.Quoted(id)
	p, err := h.peerByID(r.Context(), id)
	switch {
	case err != nil || p == nil:
		return fallback
	case p.note != "":
		return jsjson.Quoted(p.note)
	}
	info, err := p.parsedInfo()
	if err != nil {
		return fallback
	}
	if name := info.Get("hostname"); name.Kind() == jsjson.String && name.Str() != "" {
		return jsjson.Quoted(name.Str())
	}
	return fallback
}

type deviceDetail struct {
	DeviceID     jsjson.Quoted `json:"device_id"`
	Hostname     jsjson.Quoted `json:"hostname"`
	Sessions     []record      `json:"sessions"`
	Summaries    []record      `json:"summaries"`
	SessionCount int           `json:"session_count"`
}

// detail answers GET /api/activity/{id}?from=&to=&limit=: the device's
// sessions that lie in the window, newest first and 100 at most unless
// limit says otherwise, and its summaries reported in the window. A
// device outside the account's scope, or one no device has, is refused
// with 403.
func (h *handlers) detail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	window, bad := spanOf(r)
	max, ok := limitOf(r, 100)
	switch {
	case bad != "":
		badQuery(w, r, bad)
		return
	case !ok:
		badQuery(w, r, "limit")
		return
	case !h.inScope(w, r, "Detail error", id):
		return
	}
	sessions, err := deviceSessions(r.Context(), h.db, id, window, max)
	var summaries []record
	if err == nil {
		summaries, err = deviceSummaries(r.Context(), h.db, id, window)
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
// most used first, 10 unless limit says otherwise. A device outside the
// account's scope is refused with 403.
func (h *handlers) top(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	window, bad := spanOf(r)
	max, ok := limitOf(r, 10)
	switch {
	case bad != "":
		badQuery(w, r, bad)
		return
	case !ok:
		badQuery(w, r, "limit")
		return
	case !h.inScope(w, r, "Top apps error", id):
		return
	}
	apps, err := topAppsOf(r.Context(), h.db, id, window, max)
	if err != nil {
		h.serverError(w, r, "Top apps error", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, topApps{DeviceID: jsjson.Quoted(id), TopApps: apps})
}
