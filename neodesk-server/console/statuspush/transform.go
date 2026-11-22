package statuspush

import (
	"strconv"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
)

// output is what one event becomes: a message for one kind of socket,
// and for a changed device id the ids to carry into the console's tables.
type output struct {
	panel   bool
	message *goapi.Object
	cascade *[2]string
}

// undefined stands for a property JavaScript reads as undefined.
type undefinedValue struct{}

var undefined = undefinedValue{}

// prop reads v[key] as JavaScript does for the values JSON.parse makes.
func prop(v any, key string) any {
	switch t := v.(type) {
	case *goapi.Object:
		if t.Has(key) {
			return t.Get(key)
		}
	case []any:
		if key == "length" {
			return float64(len(t))
		}
	case string:
		if key == "length" {
			return float64(len(t))
		}
	}
	return undefined
}

func truthy(v any) bool {
	if v == undefined {
		return false
	}
	return goapi.Truthy(v)
}

// or is a || b || ...: the first truthy value, else the last.
func or(values ...any) any {
	for _, v := range values {
		if truthy(v) {
			return v
		}
	}
	return values[len(values)-1]
}

// set stores value under key unless it is undefined, which
// JSON.stringify leaves out.
func set(o *goapi.Object, key string, value any) {
	if value != undefined {
		o.Set(key, value)
	}
}

// transform is the message handler of the old console's event socket
// client. event is one frame as JSON.parse read it. ok is false for an
// event the browsers do not get.
func transform(event any, now func() time.Time) (output, bool) {
	if event == nil {
		// event.data on null throws; the frame was dropped
		return output{}, false
	}
	payload := prop(event, "data")
	if !truthy(payload) {
		payload = goapi.NewObject()
	}
	typ, _ := prop(event, "type").(string)
	timestamp := func() any {
		return or(prop(event, "timestamp"), float64(now().UnixMilli()))
	}
	switch typ {
	case "peer_id_changed":
		oldID, newID := prop(payload, "old_id"), prop(payload, "new_id")
		var out output
		if truthy(oldID) && truthy(newID) {
			oldText, oldOK := oldID.(string)
			newText, newOK := newID.(string)
			if oldOK && newOK {
				out.cascade = &[2]string{oldText, newText}
			}
		}
		msg := goapi.NewObject("type", "device_id_changed")
		set(msg, "old_id", oldID)
		set(msg, "new_id", newID)
		msg.Set("source", or(prop(payload, "source"), ""))
		msg.Set("timestamp", timestamp())
		out.message = msg
		return out, true
	case "device_pending", "enrollment_pending":
		msg := goapi.NewObject("type", "registration_pending")
		msg.Set("registration", payload)
		msg.Set("timestamp", timestamp())
		return output{panel: true, message: msg}, true
	case "device_approved", "device_rejected", "enrollment_rejection_cleared":
		msg := goapi.NewObject("type", "registration_changed")
		msg.Set("registration", payload)
		msg.Set("timestamp", timestamp())
		return output{panel: true, message: msg}, true
	case "help_request":
		// The data's fields stay at the top, where the web app reads them,
		// but a type in them does not change the message's.
		msg := goapi.NewObject("type", "help_request")
		spread(msg, payload)
		msg.Set("type", "help_request")
		msg.Set("timestamp", timestamp())
		return output{panel: true, message: msg}, true
	case "peer_online", "peer_offline", "peer_status_changed", "peer_registered":
		msg := goapi.NewObject("type", "device_status")
		set(msg, "device_id", or(prop(payload, "peer_id"), prop(payload, "id"), prop(event, "peer_id"), prop(event, "id"), prop(event, "device_id")))
		fallback := "offline"
		if typ == "peer_online" {
			fallback = "online"
		}
		msg.Set("status", or(prop(payload, "status"), prop(event, "status"), fallback))
		msg.Set("timestamp", timestamp())
		msg.Set("details", event)
		return output{message: msg}, true
	}
	return output{}, false
}

// spread copies the own properties of v into o, as {...v} in an object
// literal does: an object's keys, an array's or a string's indexes.
func spread(o *goapi.Object, v any) {
	switch t := v.(type) {
	case *goapi.Object:
		for _, k := range t.Keys() {
			o.Set(k, t.Get(k))
		}
	case []any:
		for i, el := range t {
			o.Set(strconv.Itoa(i), el)
		}
	case string:
		i := 0
		for _, r := range t {
			o.Set(strconv.Itoa(i), string(r))
			i++
		}
	}
}

// stringify is JSON.stringify for the values JSON.parse makes and the
// messages built from them.
func stringify(v any) (string, error) {
	b, err := httpx.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
