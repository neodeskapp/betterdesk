package network

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/netmonitor"
)

// checkTarget answers POST /targets/{id}/check: it runs the target's
// check now, stores the result and answers {target_id, success, rtt_ms,
// error, status}, with status_code for an HTTP check.
func (h *handlers) checkTarget(w http.ResponseWriter, r *http.Request) {
	target, err := netmonitor.GetTarget(r.Context(), h.db, pathID(r))
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
// newest first, at most ?limit= of them (100 when it names none), from
// ?from= to ?to=.
func (h *handlers) history(w http.ResponseWriter, r *http.Request) {
	limit := netmonitor.Num(netmonitor.ParseInt(query(r.URL.RawQuery, "limit").string()).Or(100))
	filter := netmonitor.HistoryFilter{Limit: &limit}
	if from := query(r.URL.RawQuery, "from"); from.truthy() {
		p := from.param()
		filter.From = &p
	}
	if to := query(r.URL.RawQuery, "to"); to.truthy() {
		p := to.param()
		filter.To = &p
	}
	checks, err := netmonitor.CheckHistory(r.Context(), h.db, pathID(r), filter)
	if err != nil {
		fail(w, r, http.StatusInternalServerError, netmonitor.DriverMessage(err))
		return
	}
	kit.JSON(w, r, http.StatusOK, checks)
}

// toolTimeout is the timeout of a tool's body in milliseconds, the default
// when it is no number or 0. ok is false for a number below 1, which the
// tools refuse.
func toolTimeout(body middleware.JSONObject) (timeout netmonitor.Number, ok bool) {
	timeout = parseInt(body.Get("timeout"))
	if timeout.Truthy() && timeout < 1 {
		return 0, false
	}
	return timeout.Or(netmonitor.DefaultTimeoutMs), true
}

const timeoutBelowOne = "timeout must be at least 1"

// ping answers POST /ping with the body {host, timeout}.
func (h *handlers) ping(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	host := body.Get("host")
	if !host.Truthy() {
		fail(w, r, http.StatusBadRequest, "host is required")
		return
	}
	timeout, ok := toolTimeout(body)
	if !ok {
		fail(w, r, http.StatusBadRequest, timeoutBelowOne)
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
	timeout, ok := toolTimeout(body)
	if !ok {
		fail(w, r, http.StatusBadRequest, timeoutBelowOne)
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
	timeout, ok := toolTimeout(body)
	if !ok {
		fail(w, r, http.StatusBadRequest, timeoutBelowOne)
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
