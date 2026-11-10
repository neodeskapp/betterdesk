package network

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/netmonitor"
)

const (
	targetNotFound   = "Target not found"
	invalidCheckType = "check_type must be one of: ping, tcp, http, https"
)

// validCheckType reports whether v is one of the four check types, as
// text.
func validCheckType(v middleware.Value) bool {
	s, isString := v.Str()
	if !isString {
		return false
	}
	switch s {
	case "ping", "tcp", "http", "https":
		return true
	}
	return false
}

// pathID is req.params.id as a statement parameter.
func pathID(r *http.Request) netmonitor.Param {
	return netmonitor.Text(r.PathValue("id"))
}

// listTargets answers GET /targets: the targets by name. ?enabled=true
// lists the enabled ones, any other value of enabled the disabled ones;
// ?check_type= lists one type.
func (h *handlers) listTargets(w http.ResponseWriter, r *http.Request) {
	var filter netmonitor.TargetFilter
	if enabled := query(r.URL.RawQuery, "enabled"); enabled.kind != queryUndefined {
		on := enabled.kind == queryString && enabled.text == "true"
		filter.Enabled = &on
	}
	if checkType := query(r.URL.RawQuery, "check_type"); checkType.truthy() {
		p := checkType.param()
		filter.CheckType = &p
	}
	targets, err := netmonitor.ListTargets(r.Context(), h.db, filter)
	if err != nil {
		fail(w, r, http.StatusInternalServerError, netmonitor.DriverMessage(err))
		return
	}
	kit.JSON(w, r, http.StatusOK, targets)
}

// createTarget answers POST /targets with the body {name, host, port,
// url, check_type, timeout_ms, interval_ms, enabled}.
func (h *handlers) createTarget(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	name, host, url, checkType := body.Get("name"), body.Get("host"), body.Get("url"), body.Get("check_type")
	if !name.Truthy() || !checkType.Truthy() {
		fail(w, r, http.StatusBadRequest, "name and check_type are required")
		return
	}
	if !validCheckType(checkType) {
		fail(w, r, http.StatusBadRequest, invalidCheckType)
		return
	}
	if !host.Truthy() && !url.Truthy() {
		fail(w, r, http.StatusBadRequest, "host or url is required")
		return
	}

	data := netmonitor.NewTarget{
		Name:       param(name),
		Host:       netmonitor.Text(""),
		Port:       netmonitor.Null(),
		URL:        netmonitor.Null(),
		CheckType:  param(checkType),
		TimeoutMs:  parseInt(body.Get("timeout_ms")).Or(netmonitor.DefaultTimeoutMs),
		IntervalMs: parseInt(body.Get("interval_ms")).Or(netmonitor.DefaultPollIntervalMs),
		Enabled:    true,
	}
	if host.Truthy() {
		data.Host = param(host)
	}
	if port := parseInt(body.Get("port")); port.Truthy() {
		data.Port = netmonitor.Num(port)
	}
	if url.Truthy() {
		data.URL = param(url)
	}
	if enabled, isBool := body.Get("enabled").Bool(); isBool && !enabled {
		data.Enabled = false
	}
	target, err := netmonitor.CreateTarget(detached(r), h.db, data)
	if err != nil {
		fail(w, r, http.StatusBadRequest, netmonitor.DriverMessage(err))
		return
	}
	kit.JSON(w, r, http.StatusCreated, target)
}

// getTarget answers GET /targets/{id}.
func (h *handlers) getTarget(w http.ResponseWriter, r *http.Request) {
	target, err := netmonitor.GetTarget(r.Context(), h.db, pathID(r))
	if err != nil {
		fail(w, r, http.StatusInternalServerError, netmonitor.DriverMessage(err))
		return
	}
	if target == nil {
		fail(w, r, http.StatusNotFound, targetNotFound)
		return
	}
	kit.JSON(w, r, http.StatusOK, target)
}

// updateTarget answers PATCH /targets/{id}: it changes the members the
// body names. A body that names none answers 404, like an id no target
// has (BUGS.md NM2).
func (h *handlers) updateTarget(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	if checkType := body.Get("check_type"); checkType.Truthy() && !validCheckType(checkType) {
		fail(w, r, http.StatusBadRequest, invalidCheckType)
		return
	}

	var changes netmonitor.TargetChanges
	given := func(key string) *netmonitor.Param {
		if !body.Has(key) {
			return nil
		}
		p := param(body.Get(key))
		return &p
	}
	number := func(key string, orNull bool) *netmonitor.Param {
		if !body.Has(key) {
			return nil
		}
		n := parseInt(body.Get(key))
		p := netmonitor.Num(n)
		if orNull && !n.Truthy() {
			p = netmonitor.Null()
		}
		return &p
	}
	changes.Name = given("name")
	changes.Host = given("host")
	changes.Port = number("port", true)
	changes.URL = given("url")
	changes.CheckType = given("check_type")
	changes.TimeoutMs = number("timeout_ms", false)
	changes.IntervalMs = number("interval_ms", false)
	if body.Has("enabled") {
		enabled := body.Get("enabled").Truthy()
		changes.Enabled = &enabled
	}

	target, err := netmonitor.UpdateTarget(detached(r), h.db, pathID(r), changes)
	if err != nil {
		fail(w, r, http.StatusBadRequest, netmonitor.DriverMessage(err))
		return
	}
	if target == nil {
		fail(w, r, http.StatusNotFound, targetNotFound)
		return
	}
	kit.JSON(w, r, http.StatusOK, target)
}

type deleted struct {
	Status string `json:"status"`
}

// deleteTarget answers DELETE /targets/{id}.
func (h *handlers) deleteTarget(w http.ResponseWriter, r *http.Request) {
	ok, err := netmonitor.DeleteTarget(detached(r), h.db, pathID(r))
	if err != nil {
		fail(w, r, http.StatusInternalServerError, netmonitor.DriverMessage(err))
		return
	}
	if !ok {
		fail(w, r, http.StatusNotFound, targetNotFound)
		return
	}
	kit.JSON(w, r, http.StatusOK, deleted{Status: "deleted"})
}

type statsAnswer struct {
	Total   int `json:"total"`
	Up      int `json:"up"`
	Down    int `json:"down"`
	Unknown int `json:"unknown"`
	// AvgLatency is always null: the old route averaged a member the
	// targets do not have (BUGS.md NM3).
	AvgLatency *float64 `json:"avg_latency"`
}

// stats answers GET /stats: how many targets are up, down and unknown.
func (h *handlers) stats(w http.ResponseWriter, r *http.Request) {
	targets, err := netmonitor.ListTargets(r.Context(), h.db, netmonitor.TargetFilter{})
	if err != nil {
		fail(w, r, http.StatusInternalServerError, netmonitor.DriverMessage(err))
		return
	}
	answer := statsAnswer{Total: len(targets)}
	for i := range targets {
		switch targets[i].LastStatus.String {
		case "up":
			answer.Up++
		case "down":
			answer.Down++
		}
	}
	answer.Unknown = answer.Total - answer.Up - answer.Down
	kit.JSON(w, r, http.StatusOK, answer)
}
