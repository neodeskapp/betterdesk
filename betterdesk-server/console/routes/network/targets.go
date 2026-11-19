package network

import (
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/netmonitor"
	"github.com/unitronix/betterdesk-server/console/store"
)

const (
	targetNotFound   = "Target not found"
	invalidCheckType = "check_type must be one of: ping, tcp, http, https"
	notSaved         = "The target could not be saved"
)

// The members of a target are checked before anything is stored, and a
// refusal names the member (BUGS.md NM1).
const (
	nameNotText     = "name must be text"
	hostNotText     = "host must be text"
	urlNotText      = "url must be text"
	portNotInRange  = "port must be a whole number from 1 to 65535"
	timeoutNotRange = "timeout_ms must be a whole number from 1 to 2147483647"
	intervalNoRange = "interval_ms must be a whole number from 1 to 2147483647"
	enabledNotFlag  = "enabled must be true or false"
	maxPort         = 65535
	maxMs           = 1<<31 - 1
)

// wholeNumber reads a member that holds a whole number from 1 to max: a
// number, or text of digits. 0 is a member that is not there, null, 0 or
// empty text, which all stand for no value. ok is false for anything else.
func wholeNumber(v middleware.Value, max float64) (n netmonitor.Number, ok bool) {
	var f float64
	switch v.Kind() {
	case middleware.KindUndefined, middleware.KindNull:
		return 0, true
	case middleware.KindNumber:
		f, _ = v.Float()
	case middleware.KindString:
		s, _ := v.Str()
		if s == "" {
			return 0, true
		}
		if strings.Trim(s, "0123456789") != "" {
			return 0, false
		}
		f, _ = strconv.ParseFloat(s, 64)
	default:
		return 0, false
	}
	if f == 0 {
		return 0, true
	}
	if f != math.Trunc(f) || f < 1 || f > max {
		return 0, false
	}
	return netmonitor.Number(f), true
}

// flag reads enabled: true or false, or 1 or 0.
func flag(v middleware.Value) (on, ok bool) {
	if b, isBool := v.Bool(); isBool {
		return b, true
	}
	if f, isNumber := v.Float(); isNumber && (f == 0 || f == 1) {
		return f == 1, true
	}
	return false, false
}

// textOrNull is a member that must be text, or null where null may be
// stored.
func textOrNull(v middleware.Value) (netmonitor.Param, bool) {
	if v.Kind() == middleware.KindNull {
		return netmonitor.Null(), true
	}
	s, isString := v.Str()
	return netmonitor.Text(s), isString
}

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

	data, problem := newTarget(body)
	if problem != "" {
		fail(w, r, http.StatusBadRequest, problem)
		return
	}
	target, err := netmonitor.CreateTarget(detached(r), h.db, data)
	if err != nil {
		h.logError("create a network target:", err)
		fail(w, r, http.StatusBadRequest, notSaved)
		return
	}
	kit.JSON(w, r, http.StatusCreated, target)
}

// newTarget reads the members of a create body that passed the checks for
// the members it must have, and the first member it refuses.
func newTarget(body middleware.JSONObject) (netmonitor.NewTarget, string) {
	kind, _ := body.Get("check_type").Str()
	data := netmonitor.NewTarget{
		Host:      netmonitor.Text(""),
		Port:      netmonitor.Null(),
		URL:       netmonitor.Null(),
		CheckType: netmonitor.Text(kind),
		Enabled:   true,
	}
	name, isString := body.Get("name").Str()
	if !isString {
		return data, nameNotText
	}
	data.Name = netmonitor.Text(name)
	if host := body.Get("host"); host.Truthy() {
		text, isString := host.Str()
		if !isString {
			return data, hostNotText
		}
		data.Host = netmonitor.Text(text)
	}
	if url := body.Get("url"); url.Truthy() {
		text, isString := url.Str()
		if !isString {
			return data, urlNotText
		}
		data.URL = netmonitor.Text(text)
	}
	port, ok := wholeNumber(body.Get("port"), maxPort)
	if !ok {
		return data, portNotInRange
	}
	if port != 0 {
		data.Port = netmonitor.Num(port)
	}
	if data.TimeoutMs, ok = wholeNumber(body.Get("timeout_ms"), maxMs); !ok {
		return data, timeoutNotRange
	}
	if data.IntervalMs, ok = wholeNumber(body.Get("interval_ms"), maxMs); !ok {
		return data, intervalNoRange
	}
	if body.Has("enabled") {
		if data.Enabled, ok = flag(body.Get("enabled")); !ok {
			return data, enabledNotFlag
		}
	}
	return data, ""
}

// targetChanges reads the members an update body names, and the first
// member it refuses. Host and url may be null; port, timeout_ms and
// interval_ms may be null or 0 for no value.
func targetChanges(body middleware.JSONObject) (netmonitor.TargetChanges, string) {
	var changes netmonitor.TargetChanges
	if body.Has("check_type") {
		v := body.Get("check_type")
		if !validCheckType(v) {
			return changes, invalidCheckType
		}
		kind, _ := v.Str()
		p := netmonitor.Text(kind)
		changes.CheckType = &p
	}
	if body.Has("name") {
		name, isString := body.Get("name").Str()
		if !isString {
			return changes, nameNotText
		}
		p := netmonitor.Text(name)
		changes.Name = &p
	}
	for _, m := range []struct {
		key, problem string
		into         **netmonitor.Param
	}{{"host", hostNotText, &changes.Host}, {"url", urlNotText, &changes.URL}} {
		if !body.Has(m.key) {
			continue
		}
		p, ok := textOrNull(body.Get(m.key))
		if !ok {
			return changes, m.problem
		}
		*m.into = &p
	}
	if body.Has("port") {
		port, ok := wholeNumber(body.Get("port"), maxPort)
		if !ok {
			return changes, portNotInRange
		}
		p := netmonitor.Null()
		if port != 0 {
			p = netmonitor.Num(port)
		}
		changes.Port = &p
	}
	for _, m := range []struct {
		key, problem string
		into         **netmonitor.Param
	}{{"timeout_ms", timeoutNotRange, &changes.TimeoutMs}, {"interval_ms", intervalNoRange, &changes.IntervalMs}} {
		if !body.Has(m.key) {
			continue
		}
		v := body.Get(m.key)
		n, ok := wholeNumber(v, maxMs)
		if !ok {
			return changes, m.problem
		}
		p := netmonitor.Num(n)
		if v.Kind() == middleware.KindNull {
			p = netmonitor.Null()
		}
		*m.into = &p
	}
	if body.Has("enabled") {
		on, ok := flag(body.Get("enabled"))
		if !ok {
			return changes, enabledNotFlag
		}
		changes.Enabled = &on
	}
	return changes, ""
}

// logError writes a failure the client is not told the details of.
func (h *handlers) logError(what string, err error) {
	if h.log != nil {
		h.log.Error(what, netmonitor.DriverMessage(err))
	}
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
// body names and answers the target. A body that names none answers the
// target as it is.
func (h *handlers) updateTarget(w http.ResponseWriter, r *http.Request) {
	changes, problem := targetChanges(kit.Body(r))
	if problem != "" {
		fail(w, r, http.StatusBadRequest, problem)
		return
	}
	target, err := netmonitor.UpdateTarget(detached(r), h.db, pathID(r), changes)
	if err != nil {
		h.logError("update a network target:", err)
		fail(w, r, http.StatusBadRequest, notSaved)
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
	// AvgLatency is the mean of the last round trip of the targets that
	// have one, null when none has.
	AvgLatency *netmonitor.Number `json:"avg_latency"`
}

// stats answers GET /stats: how many targets are up, down and unknown, and
// their mean round trip.
func (h *handlers) stats(w http.ResponseWriter, r *http.Request) {
	targets, err := netmonitor.ListTargets(r.Context(), h.db, netmonitor.TargetFilter{})
	if err != nil {
		fail(w, r, http.StatusInternalServerError, netmonitor.DriverMessage(err))
		return
	}
	answer := statsAnswer{Total: len(targets)}
	var sum float64
	var timed int
	for i := range targets {
		switch targets[i].LastStatus.String {
		case "up":
			answer.Up++
		case "down":
			answer.Down++
		}
		if rtt, ok := asSent(targets[i].LastRTT); ok {
			sum += rtt
			timed++
		}
	}
	answer.Unknown = answer.Total - answer.Up - answer.Down
	if timed > 0 {
		mean := netmonitor.Number(sum / float64(timed))
		answer.AvgLatency = &mean
	}
	kit.JSON(w, r, http.StatusOK, answer)
}

// asSent is a stored round trip as the answers write it: a PostgreSQL real
// in its short form (12.3, not 12.300000190734863).
func asSent(rtt store.Float) (float64, bool) {
	text, err := rtt.MarshalJSON()
	if err != nil || string(text) == "null" {
		return 0, false
	}
	f, err := strconv.ParseFloat(string(text), 64)
	return f, err == nil
}
