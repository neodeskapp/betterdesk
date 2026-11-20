package activity

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/alerts"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsjson"
	"github.com/unitronix/betterdesk-server/console/store"
)

// maxSessions is how many sessions of one report are stored.
const maxSessions = 500

type done struct {
	Success bool `json:"success"`
}

// failReport answers what the route's catch block answered.
func (h *handlers) failReport(w http.ResponseWriter, r *http.Request, err error) {
	h.log.Error("Upload error:", jsbind.Message(err))
	kit.Fail(w, r, http.StatusInternalServerError, "Internal server error")
}

// parsed is a member of the body as JSON.parse made it, undefined when
// the body has no such member.
func parsed(v middleware.Value) (jsjson.Value, error) {
	if !v.Exists() {
		return jsjson.Value{}, nil
	}
	value, err := jsjson.Parse(string(v.Raw()))
	if err != nil {
		return jsjson.Value{}, fmt.Errorf("read a member of the body: %w", err)
	}
	return value, nil
}

// report answers POST activity with the body {device_id, sessions?,
// idle_seconds?, timestamp?}: the sessions of the report that are objects
// with a start and an end (500 at most) are stored, and the report's
// summary replaces the one of the same device and time. The summary
// counts the stored sessions and adds their durations, each a number or
// text that is one (anything else counts 0). Then the idle time is
// checked against the alert rules in the background.
//
// The device guards came first. The route compares the body's device_id
// with the token's device once more, as it is: a number that passed the
// guard as its text is refused here, with success false.
//
// The values of a session are stored as they came, and a session the
// database cannot take fails the report with no session stored.
func (h *handlers) report(w http.ResponseWriter, r *http.Request) {
	device := kit.Device(r).DeviceID
	body := kit.Body(r)
	deviceID, err := parsed(body.Get("device_id"))
	if err != nil {
		h.failReport(w, r, err)
		return
	}
	if !deviceID.Truthy() {
		kit.Fail(w, r, http.StatusBadRequest, "Missing device_id")
		return
	}
	// The guard put the token's device into the request, never empty
	if deviceID.Kind() != jsjson.String || deviceID.Str() != device {
		kit.Fail(w, r, http.StatusForbidden, "Device ID mismatch")
		return
	}

	var stored []middleware.JSONObject
	sessions, _ := body.Get("sessions").Array()
	for _, s := range sessions {
		session, isObject := s.Object()
		if isObject && session.Truthy("started_at") && session.Truthy("ended_at") && len(stored) < maxSessions {
			stored = append(stored, session)
		}
	}
	if len(stored) > 0 {
		if err := insertActivitySessions(r.Context(), h.db, device, stored); err != nil {
			h.failReport(w, r, err)
			return
		}
	}
	total := 0.0
	for _, session := range stored {
		total += seconds(session.Get("duration_secs"))
	}
	idle := jsbind.Of(body.Get("idle_seconds"))
	err = upsertActivitySummary(r.Context(), h.db, device, summary{
		IdleSeconds:     orZero(idle),
		SessionCount:    jsbind.Number(float64(len(stored))),
		TotalActiveSecs: jsbind.Number(math.Round(total)),
		ReportedAt:      jsbind.Of(body.Get("timestamp")).Or(jsbind.String(store.ISOTime(time.Now()))),
	})
	if err != nil {
		h.failReport(w, r, err)
		return
	}
	if !idle.IsUndefined() && !idle.IsNull() && h.alerts != nil {
		h.alerts.EvaluateInBackground(func(ctx context.Context) error {
			return h.alerts.EvaluateRules(ctx, jsbind.String(device), "idle_duration", idle, alerts.EvalOptions{})
		})
	}
	h.log.Info(fmt.Sprintf("Received %d sessions from %s (idle: %s)", len(stored), device, idleText(body.Get("idle_seconds"))))
	kit.JSON(w, r, http.StatusOK, done{Success: true})
}

// seconds is a duration as a number: a JSON number, or text that is a
// decimal number. Anything else, and a number that is not finite, is 0.
func seconds(v middleware.Value) float64 {
	n, isNumber := v.Float()
	if text, isText := v.Str(); isText {
		var err error
		n, err = strconv.ParseFloat(strings.TrimSpace(text), 64)
		isNumber = err == nil
	}
	if !isNumber {
		return 0
	}
	if math.IsInf(n, 0) || math.IsNaN(n) {
		return 0
	}
	return n
}

// idleText is the idle time of a report for the log: its JSON, or 0s
// when the report has none.
func idleText(v middleware.Value) string {
	if !v.Exists() || v.Kind() == middleware.KindNull {
		return "0s"
	}
	return string(v.Raw()) + "s"
}

// orZero is v ?? 0.
func orZero(v jsbind.Value) jsbind.Value {
	if v.IsUndefined() || v.IsNull() {
		return jsbind.Number(0)
	}
	return v
}
