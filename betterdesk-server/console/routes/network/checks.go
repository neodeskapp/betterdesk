package network

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/netmonitor"
	"github.com/unitronix/betterdesk-server/console/store"
)

// checkTarget answers POST /targets/{id}/check: it runs the target's
// check now, stores the result and answers {target_id, success, rtt_ms,
// error, status}, with status_code for an HTTP check.
func (h *handlers) checkTarget(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		fail(w, r, http.StatusNotFound, targetNotFound)
		return
	}
	target, err := netmonitor.GetTarget(r.Context(), h.db, id)
	if err != nil {
		fail(w, r, http.StatusInternalServerError, netmonitor.DriverMessage(err))
		return
	}
	if target == nil {
		fail(w, r, http.StatusNotFound, targetNotFound)
		return
	}
	checked := h.monitor.CheckTarget(detached(r), target)
	answer := object{{"target_id", checked.TargetID}}.result(checked.Result)
	kit.JSON(w, r, http.StatusOK, append(answer, member{"status", checked.Status}))
}

// history answers GET /targets/{id}/history: the target's checks, the
// newest first, at most ?limit= of them (historyLimit), from ?from= to
// ?to= (historyBound). An id that names no row has none.
func (h *handlers) history(w http.ResponseWriter, r *http.Request) {
	n, ok := historyLimit(r.URL.RawQuery)
	if !ok {
		fail(w, r, http.StatusBadRequest, "Invalid limit")
		return
	}
	limit := netmonitor.Num(netmonitor.Number(n))
	filter := netmonitor.HistoryFilter{Limit: &limit}
	for _, end := range []struct {
		name  string
		into  **netmonitor.Param
		isEnd bool
	}{{"from", &filter.From, false}, {"to", &filter.To, true}} {
		if *end.into, ok = historyBound(h.db, r.URL.RawQuery, end.name, end.isEnd); !ok {
			fail(w, r, http.StatusBadRequest, "Invalid "+end.name)
			return
		}
	}
	id, ok := pathID(r)
	if !ok {
		kit.JSON(w, r, http.StatusOK, []netmonitor.Check{})
		return
	}
	checks, err := netmonitor.CheckHistory(r.Context(), h.db, id, filter)
	if err != nil {
		fail(w, r, http.StatusInternalServerError, netmonitor.DriverMessage(err))
		return
	}
	kit.JSON(w, r, http.StatusOK, checks)
}

// The history's limit: 100 checks when the query names none, at most
// maxHistory.
const (
	defaultHistory = 100
	maxHistory     = 500
)

// historyLimit reads ?limit= of the history: defaultHistory without one or
// with an empty one, else a whole number from 1 written in digits, given
// once, held at maxHistory. ok is false for anything else, which the two
// databases would take apart: a negative limit is no limit on SQLite and
// an error on PostgreSQL.
func historyLimit(rawQuery string) (int, bool) {
	text, _, err := jsbind.QueryText(rawQuery, "limit")
	if err != nil {
		return 0, false
	}
	if text == "" {
		return defaultHistory, true
	}
	if strings.Trim(text, "0123456789") != "" || strings.Trim(text, "0") == "" {
		return 0, false
	}
	n, err := strconv.Atoi(text)
	if err != nil || n > maxHistory {
		return maxHistory, true
	}
	return n, true
}

// historyBound reads ?from= (isEnd false) or ?to= (isEnd true) of the
// history: nil without one or with an empty one, else the time as db
// compares it with checked_at. The time is a date, or a date and time as
// the console writes them, with or without a zone (none is UTC), in the
// years 1 to 9999 in UTC, given once; ok is false for anything else. A
// date alone as to takes in that whole day.
func historyBound(db *store.DB, rawQuery, name string, isEnd bool) (*netmonitor.Param, bool) {
	text, _, err := jsbind.QueryText(rawQuery, name)
	if err != nil {
		return nil, false
	}
	if text == "" {
		return nil, true
	}
	at, err := time.Parse(time.DateOnly, text)
	switch {
	case err == nil && isEnd:
		at = at.AddDate(0, 0, 1).Add(-time.Microsecond)
	case err != nil:
		var ok bool
		if at, ok = store.ParseTime(text); !ok {
			return nil, false
		}
	}
	// Both databases hold checked_at to the microsecond at most: from
	// rounds up to one and to down, so a time between two of them takes
	// in the same checks on both
	rounded := at.UTC().Truncate(time.Microsecond)
	if !isEnd && rounded.Before(at) {
		rounded = rounded.Add(time.Microsecond)
	}
	if year := rounded.Year(); year < 1 || year > 9999 {
		return nil, false
	}
	// PostgreSQL reads the instant from the text with its zone. SQLite
	// compares texts, which in the form datetime('now') writes, in UTC,
	// sort as the times do
	p := netmonitor.Text(rounded.Format(db.Pick("2006-01-02 15:04:05.999999", "2006-01-02T15:04:05.999999Z07:00")))
	return &p, true
}

// toolTimeout is the timeout of a tool's body in milliseconds, the default
// when it is no number or 0. problem is the refusal of a number below 1 or
// above netmonitor.MaxTimeoutMs, "" for a timeout the tools take.
func toolTimeout(body middleware.JSONObject) (timeout netmonitor.Number, problem string) {
	timeout = parseInt(body.Get("timeout"))
	switch {
	case timeout.Truthy() && timeout < 1:
		return 0, timeoutBelowOne
	case timeout > netmonitor.MaxTimeoutMs:
		return 0, timeoutAboveMax
	}
	return timeout.Or(netmonitor.DefaultTimeoutMs), ""
}

const (
	timeoutBelowOne = "timeout must be at least 1"
	timeoutAboveMax = "timeout must be at most 60000"
)

// ping answers POST /ping with the body {host, timeout}.
func (h *handlers) ping(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	host := body.Get("host")
	if !host.Truthy() {
		fail(w, r, http.StatusBadRequest, "host is required")
		return
	}
	timeout, problem := toolTimeout(body)
	if problem != "" {
		fail(w, r, http.StatusBadRequest, problem)
		return
	}
	result := netmonitor.InvalidHost()
	if name, isString := host.Str(); isString {
		result = h.monitor.PingHost(detached(r), name, timeout)
	}
	kit.JSON(w, r, http.StatusOK, object{{"host", stringify(host)}}.result(result))
}

// tcp answers POST /tcp with the body {host, port, timeout}.
func (h *handlers) tcp(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	host := body.Get("host")
	if !host.Truthy() || !body.Truthy("port") {
		fail(w, r, http.StatusBadRequest, "host and port are required")
		return
	}
	timeout, problem := toolTimeout(body)
	if problem != "" {
		fail(w, r, http.StatusBadRequest, problem)
		return
	}
	port := parseInt(body.Get("port"))
	result := netmonitor.InvalidHost()
	if name, isString := host.Str(); isString {
		result = h.monitor.CheckTCPPort(detached(r), name, port, timeout)
	}
	kit.JSON(w, r, http.StatusOK, object{{"host", stringify(host)}, {"port", port}}.result(result))
}

// httpCheck answers POST /http with the body {url, timeout}.
func (h *handlers) httpCheck(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	target := body.Get("url")
	if !target.Truthy() {
		fail(w, r, http.StatusBadRequest, "url is required")
		return
	}
	timeout, problem := toolTimeout(body)
	if problem != "" {
		fail(w, r, http.StatusBadRequest, problem)
		return
	}
	result := netmonitor.URLRequired()
	if rawURL, isString := target.Str(); isString {
		result = h.monitor.CheckHTTP(detached(r), rawURL, timeout)
	}
	kit.JSON(w, r, http.StatusOK, object{{"url", stringify(target)}}.result(result))
}

type resolved struct {
	Host string `json:"host"`
	IP   string `json:"ip"`
}

// resolve answers POST /resolve with the body {host}: the first address
// of the host.
func (h *handlers) resolve(w http.ResponseWriter, r *http.Request) {
	host := kit.Body(r).Get("host")
	if !host.Truthy() {
		fail(w, r, http.StatusBadRequest, "host is required")
		return
	}
	name, isString := host.Str()
	if !isString {
		fail(w, r, http.StatusInternalServerError, "Invalid host format")
		return
	}
	ip, err := h.monitor.ResolveHost(detached(r), name)
	if err != nil {
		fail(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	kit.JSON(w, r, http.StatusOK, resolved{Host: name, IP: ip})
}

type started struct {
	Status     string            `json:"status"`
	IntervalMs netmonitor.Number `json:"interval_ms"`
}

type stopped struct {
	Status string `json:"status"`
}

type monitorState struct {
	Running    bool              `json:"running"`
	IntervalMs netmonitor.Number `json:"interval_ms"`
}

// minPollMs is the shortest interval of the monitor.
const minPollMs = 1000

// startMonitor answers POST /monitor/start with the body {interval_ms}:
// from a second to the longest a timer waits, the default when it is no
// number or 0. A monitor that runs keeps its interval.
func (h *handlers) startMonitor(w http.ResponseWriter, r *http.Request) {
	interval := parseInt(kit.Body(r).Get("interval_ms"))
	if interval.Truthy() && (interval < minPollMs || interval > maxMs) {
		fail(w, r, http.StatusBadRequest, "interval_ms must be from 1000 to 2147483647")
		return
	}
	h.monitor.Start(interval)
	h.saveMonitor(detached(r))
	kit.JSON(w, r, http.StatusOK, started{Status: "started", IntervalMs: h.monitor.PollInterval()})
}

// stopMonitor answers POST /monitor/stop.
func (h *handlers) stopMonitor(w http.ResponseWriter, r *http.Request) {
	h.monitor.Stop()
	h.saveMonitor(detached(r))
	kit.JSON(w, r, http.StatusOK, stopped{Status: "stopped"})
}

// monitorStatus answers GET /monitor/status.
func (h *handlers) monitorStatus(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, monitorState{Running: h.monitor.Running(), IntervalMs: h.monitor.PollInterval()})
}
