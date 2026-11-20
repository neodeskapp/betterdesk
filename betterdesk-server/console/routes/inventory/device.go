package inventory

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/unitronix/betterdesk-server/console/alerts"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsjson"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The routes of the desktop agent are behind the device guards of package
// kit, as the other device routes are: an access token bound to a device
// id, sent as "Authorization: Bearer <token>", and no other device named
// in the body, the query or X-Device-Id. The routes work on the token's
// device.

// mismatch is the answer for a request that names another device than
// its token's.
func mismatch(w http.ResponseWriter, r *http.Request) {
	kit.Fail(w, r, http.StatusForbidden, "Device ID mismatch")
}

// member is req.body[name] as JSON.parse made it, undefined when the body
// has no such member.
func member(r *http.Request, name string) (jsjson.Value, error) {
	raw := kit.Body(r).Get(name).Raw()
	if len(raw) == 0 {
		return jsjson.Value{}, nil
	}
	value, err := jsjson.Parse(string(raw))
	if err != nil {
		return jsjson.Value{}, fmt.Errorf("read the member %s of the body: %w", name, err)
	}
	return value, nil
}

// failUpload answers what the catch blocks of the two upload routes
// answered.
func (h *handlers) failUpload(w http.ResponseWriter, r *http.Request, log *kit.Logger, err error) {
	log.Error("Upload error:", jsbind.Message(err))
	kit.Fail(w, r, http.StatusInternalServerError, "Internal server error")
}

type done struct {
	Success bool `json:"success"`
}

// upload answers POST inventory with the body {device_id, hardware,
// software?, collected_at?}: the device's report replaces the one it sent
// before, as it came.
//
// The device guards came first. A disk whose size or name cannot be made
// a number or text (an object that has a member named toString) is 400,
// before anything is stored. After the report is stored, the device's
// info in the console's table of devices gets the host name, the system,
// the processor and the memory of the report; what goes wrong there is
// logged and changes nothing else. Then the fullest disk is checked
// against the disk rules in the background.
func (h *handlers) upload(w http.ResponseWriter, r *http.Request) {
	device := kit.Device(r).DeviceID
	deviceID, err := member(r, "device_id")
	var hardware, software jsjson.Value
	if err == nil {
		hardware, err = member(r, "hardware")
	}
	if err == nil {
		software, err = member(r, "software")
	}
	if err != nil {
		h.failUpload(w, r, h.log, err)
		return
	}
	if !deviceID.Truthy() || !hardware.Truthy() {
		kit.Fail(w, r, http.StatusBadRequest, "Missing device_id or hardware")
		return
	}

	fullest, unread := fullestDisk(hardware.Get("disks"))
	if len(unread) > 0 {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid disks")
		return
	}
	collectedAt := jsbind.Of(kit.Body(r).Get("collected_at")).Or(jsbind.String(store.ISOTime(time.Now())))
	if err := upsertInventory(r.Context(), h.db, device, hardware, software, collectedAt); err != nil {
		h.failUpload(w, r, h.log, err)
		return
	}
	if err := h.updatePeerInfo(r.Context(), device, hardware); err != nil {
		h.log.Warn("Failed to update peer info:", jsbind.Message(err))
	}
	if fullest != nil && h.alerts != nil {
		percent := jsbind.Number(round(fullest.percent*10) / 10)
		h.alerts.EvaluateInBackground(func(ctx context.Context) error {
			return h.alerts.EvaluateRules(ctx, jsbind.String(device), "disk_usage", percent, alerts.EvalOptions{})
		})
	}

	h.log.Info(fmt.Sprintf("Full inventory received from %s (HW + %d apps)", device, len(software.Get("apps").Items())))
	kit.JSON(w, r, http.StatusOK, done{Success: true})
}

// updatePeerInfo writes what a report says about the device into the info
// of the device's row in the console's table, if the console has one that
// is not marked deleted. The info gets hostname, os, os_version, cpu,
// cpu_cores and memory_mb where the report has a true value for them, and
// keeps its other members. No info (NULL) is an empty object; an info that
// is no object stays as it is.
//
// It fails where the info is no JSON or the text null, the memory is an
// object JavaScript cannot make a number of, or the database refuses the
// new info (PostgreSQL does not take the character U+0000 in a JSON
// document).
func (h *handlers) updatePeerInfo(ctx context.Context, id string, hardware jsjson.Value) error {
	p, err := readPeer(ctx, h.db, id)
	if err != nil || p == nil {
		return err
	}
	old, err := p.parsedInfo()
	if err != nil {
		return err
	}
	if !p.info.Valid {
		old, _ = jsjson.Parse("{}")
	}
	if old.Kind() != jsjson.Object {
		return nil
	}
	info := spread(old)
	changed := false
	cpu, memory := hardware.Get("cpu"), hardware.Get("memory")
	for _, m := range []struct {
		name  string
		value jsjson.Value
	}{
		{"hostname", hardware.Get("hostname")},
		{"os", hardware.Get("os_name")},
		{"os_version", hardware.Get("os_version")},
		{"cpu", cpu.Get("brand")},
		{"cpu_cores", cpu.Get("logical_cores")},
	} {
		if m.value.Truthy() {
			info.set(m.name, m.value)
			changed = true
		}
	}
	if total := memory.Get("total_bytes"); total.Truthy() {
		bytes, err := total.Num()
		if err != nil {
			return err
		}
		info.set("memory_mb", jsjson.OfNumber(round(bytes/megabyte)))
		changed = true
	}
	if !changed {
		return nil
	}
	return updatePeer(ctx, h.db, id, info.json())
}

// object is an object built in a route, its keys in the order JavaScript
// keeps them. A member that is undefined stays in its place and is left
// out of the JSON.
type object struct {
	keys    []string
	members map[string]jsjson.Value
}

// spread is {...v} for an object: its members, in its order.
func spread(v jsjson.Value) *object {
	o := &object{members: map[string]jsjson.Value{}}
	for _, key := range v.Keys() {
		o.set(key, v.Get(key))
	}
	return o
}

// set is o[key] = value. A key the object has keeps its place. The keys
// the routes add are no array indexes, so they go to the end.
func (o *object) set(key string, value jsjson.Value) {
	if _, ok := o.members[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.members[key] = value
}

// json is JSON.stringify(o).
func (o *object) json() string {
	b := []byte{'{'}
	first := true
	for _, key := range o.keys {
		value := o.members[key]
		if value.Kind() == jsjson.Undefined {
			continue
		}
		if !first {
			b = append(b, ',')
		}
		first = false
		b = jsjson.AppendQuoted(b, key)
		b = append(b, ':')
		text, _ := value.MarshalJSON()
		b = append(b, text...)
	}
	return string(append(b, '}'))
}

// telemetry answers POST telemetry with the body {device_id,
// cpu_usage_percent?, memory_used_bytes?, memory_total_bytes?,
// uptime_secs?, timestamp?}: the device's load replaces the one it sent
// before. A missing value is 0, a missing time the time of arrival, and
// every value is stored as it came. Then the CPU load and the used memory
// are checked against the alert rules in the background.
func (h *handlers) telemetry(w http.ResponseWriter, r *http.Request) {
	device := kit.Device(r).DeviceID
	deviceID, err := member(r, "device_id")
	if err != nil {
		h.failUpload(w, r, h.telemetryLog, err)
		return
	}
	if !deviceID.Truthy() {
		kit.Fail(w, r, http.StatusBadRequest, "Missing device_id")
		return
	}
	body := kit.Body(r)
	cpu, used, total := jsbind.Of(body.Get("cpu_usage_percent")), jsbind.Of(body.Get("memory_used_bytes")), jsbind.Of(body.Get("memory_total_bytes"))
	err = upsertTelemetry(r.Context(), h.db, device, deviceTelemetry{
		CPUUsagePercent:  orZero(cpu),
		MemoryUsedBytes:  orZero(used),
		MemoryTotalBytes: orZero(total),
		UptimeSecs:       orZero(jsbind.Of(body.Get("uptime_secs"))),
		Timestamp:        jsbind.Of(body.Get("timestamp")).Or(jsbind.String(store.ISOTime(time.Now()))),
	})
	if err != nil {
		h.failUpload(w, r, h.telemetryLog, err)
		return
	}
	if h.alerts != nil {
		h.alerts.EvaluateInBackground(func(ctx context.Context) error {
			return h.alerts.EvaluateTelemetry(ctx, jsbind.String(device), alerts.Telemetry{CPUUsagePercent: cpu, MemoryUsedBytes: used, MemoryTotalBytes: total})
		})
	}
	kit.JSON(w, r, http.StatusOK, done{Success: true})
}

// orZero is v ?? 0.
func orZero(v jsbind.Value) jsbind.Value {
	if v.IsUndefined() || v.IsNull() {
		return jsbind.Number(0)
	}
	return v
}

// entry is a device's last report as the agent reads it back.
type entry struct {
	DeviceID    any          `json:"device_id"`
	Hardware    jsjson.Value `json:"hardware"`
	Software    jsjson.Value `json:"software"`
	CollectedAt any          `json:"collected_at"`
	ReceivedAt  any          `json:"received_at"`
}

// ownReport answers GET inventory/{id}: the last report of the device the
// token is bound to. The id must be that device's, written the same.
func (h *handlers) ownReport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id != kit.Device(r).DeviceID {
		mismatch(w, r)
		return
	}
	inv, err := inventoryOf(r.Context(), h.db, id)
	if err != nil {
		h.serverError(w, r, "Get error", err)
		return
	}
	if inv == nil {
		fail(w, r, http.StatusNotFound, "No inventory data for this device")
		return
	}
	kit.JSON(w, r, http.StatusOK, entry{
		DeviceID:    inv.deviceID,
		Hardware:    inv.hardware,
		Software:    inv.software,
		CollectedAt: inv.collectedAt,
		ReceivedAt:  inv.receivedAt,
	})
}
