package activity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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

// errNullSession is the TypeError of reading a member of null: a session
// of the report was null.
var errNullSession = errors.New("Cannot read properties of null (reading 'started_at')")

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
// idle_seconds?, timestamp?}: the sessions of the report that have a
// start and an end (500 at most) are stored, and the report's summary
// replaces the one of the same device and time. Then the idle time is
// checked against the alert rules in the background.
//
// The device guards came first. The route compares the body's device_id
// with the token's device once more, as it is: a number that passed the
// guard as its text is refused here, with success false.
//
// What the route does with the sessions is JavaScript's: a session that
// is null fails the request, the count is that of every session sent, and
// the total adds the durations with +, so a duration that is text joins
// the total as text ("0" + "60" is "060"). The values are stored as they
// came, and a session the database cannot take fails the report with no
// session stored.
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

	sessions, isList := body.Get("sessions").Array()
	if isList && len(sessions) > 0 {
		var valid []middleware.JSONObject
		for _, s := range sessions {
			if s.Kind() == middleware.KindNull {
				h.failReport(w, r, errNullSession)
				return
			}
			session, _ := s.Object()
			if session.Truthy("started_at") && session.Truthy("ended_at") {
				valid = append(valid, session)
			}
		}
		if len(valid) > maxSessions {
			valid = valid[:maxSessions]
		}
		if len(valid) > 0 {
			if err := insertActivitySessions(r.Context(), h.db, device, valid); err != nil {
				h.failReport(w, r, err)
				return
			}
		}
	}

	total := jsjson.OfNumber(0)
	for _, s := range sessions {
		session, _ := s.Object()
		duration, err := parsed(session.Get("duration_secs"))
		if err == nil {
			total, err = add(total, duration.Or(jsjson.OfNumber(0)))
		}
		if err != nil {
			h.failReport(w, r, err)
			return
		}
	}
	idle := jsbind.Of(body.Get("idle_seconds"))
	err = upsertActivitySummary(r.Context(), h.db, device, summary{
		IdleSeconds:     orZero(idle),
		SessionCount:    jsbind.Number(float64(len(sessions))),
		TotalActiveSecs: bindable(total),
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

	// The log line makes text of what the report holds, which can fail
	count, err := lengthOf(body.Get("sessions"))
	var idleText string
	if err == nil {
		idleText, err = textOf(body.Get("idle_seconds"))
	}
	if err != nil {
		h.failReport(w, r, err)
		return
	}
	h.log.Info(fmt.Sprintf("Received %s sessions from %s (idle: %ss)", count, device, idleText))
	kit.JSON(w, r, http.StatusOK, done{Success: true})
}

// lengthOf is `${v?.length || 0}`.
func lengthOf(v middleware.Value) (string, error) {
	value, err := parsed(v)
	if err != nil {
		return "", err
	}
	return value.Get("length").Or(jsjson.OfNumber(0)).Text()
}

// textOf is `${v ?? 0}`.
func textOf(v middleware.Value) (string, error) {
	value, err := parsed(v)
	if err != nil {
		return "", err
	}
	if value.IsNullish() {
		return "0", nil
	}
	return value.Text()
}

// orZero is v ?? 0.
func orZero(v jsbind.Value) jsbind.Value {
	if v.IsUndefined() || v.IsNull() {
		return jsbind.Number(0)
	}
	return v
}

// add is a + b in JavaScript for two JSON values: each side is made a
// primitive first (a list or an object its text), then a text on either
// side joins both as text, and otherwise their numbers are added. It
// fails where JavaScript could not make a primitive of a side (an object
// with a member named toString).
func add(a, b jsjson.Value) (jsjson.Value, error) {
	a, err := primitive(a)
	if err != nil {
		return jsjson.Value{}, err
	}
	b, err = primitive(b)
	if err != nil {
		return jsjson.Value{}, err
	}
	if a.Kind() == jsjson.String || b.Kind() == jsjson.String {
		left, _ := a.Text()
		right, _ := b.Text()
		return jsjson.OfText(left + right), nil
	}
	x, _ := a.Num()
	y, _ := b.Num()
	return jsjson.OfNumber(x + y), nil
}

// primitive is what JavaScript's + makes of a value before it adds: a
// list or an object becomes its text.
func primitive(v jsjson.Value) (jsjson.Value, error) {
	if !v.IsObjectLike() {
		return v, nil
	}
	text, err := v.Text()
	if err != nil {
		return jsjson.Value{}, err
	}
	return jsjson.OfText(text), nil
}

// bindable is a total, a number or a text, as a value for a statement.
func bindable(v jsjson.Value) jsbind.Value {
	if v.Kind() == jsjson.String {
		return jsbind.String(jsjson.WellFormed(v.Str()))
	}
	return jsbind.Number(v.Float())
}
