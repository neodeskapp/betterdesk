package registration

import (
	"net/http"
	"regexp"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/registrations"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// The two routes devices call without signing in.

// deviceIDPattern is the form of a device id, as /api/bd/register checks
// it.
var deviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,64}$`)

// clientIP is the address the group records: the client's address as
// TRUST_PROXY gives it, so X-Forwarded-For and X-Real-IP count only from a
// trusted proxy; "unknown" without one.
func clientIP(r *http.Request) string {
	if ip := kit.ClientIP(r); ip != "" {
		return ip
	}
	return "unknown"
}

// registerRequest answers POST /register-request with the body
// {device_id, hostname, platform, version, public_key, uuid}: a new
// pending registration, or the device's registration as it is, so asking
// again changes neither the key an approver reviews nor a rejection. No
// credential is needed. The device id, trimmed, has to be an id
// /api/bd/register takes: 3 to 64 letters, digits, "_" and "-". The other
// fields are cut to their length, and a field that is truthy but no text
// is the 500 of a TypeError.
func (h *handlers) registerRequest(w http.ResponseWriter, r *http.Request) {
	body := jsreq.Body(r)
	field := func(name string) any { return jsreq.Prop(body, name) }
	deviceID, _ := field("device_id").(string)
	deviceID = jsbind.Trim(deviceID)
	if !deviceIDPattern.MatchString(deviceID) {
		failure(w, r, http.StatusBadRequest, "Invalid device_id")
		return
	}
	req := registrations.Request{DeviceID: deviceID, IPAddress: clientIP(r)}
	for _, f := range []struct {
		name string
		max  int
		dest *string
	}{
		{"hostname", 255, &req.Hostname},
		{"platform", 64, &req.Platform},
		{"version", 32, &req.Version},
		{"public_key", 512, &req.PublicKey},
		{"uuid", 64, &req.UUID},
	} {
		s, err := textOr(field(f.name), "")
		if err != nil {
			h.log.Error("Register request error:", err)
			failure(w, r, http.StatusInternalServerError, "Internal server error")
			return
		}
		*f.dest = cut(s, f.max)
	}
	row, err := h.regs.Create(r.Context(), req)
	if err != nil || row == nil {
		h.log.Error("Register request error:", err)
		failure(w, r, http.StatusInternalServerError, "Internal server error")
		return
	}
	if row.Get("status") == "pending" {
		h.publish(goapi.NewObject(
			"type", "registration_pending",
			"registration", goapi.NewObject(
				"source", "lan",
				"id", row.Get("id"),
				"device_id", row.Get("device_id"),
				"hostname", row.Get("hostname"),
				"platform", row.Get("platform"),
				"ip", row.Get("ip_address"),
				"created_at", orValue(row.Get("created_at"), row.Get("updated_at")),
			),
			"timestamp", float64(h.now().UnixMilli()),
		))
	}
	kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "status", row.Get("status"), "id", row.Get("id")))
}

// publish sends an event to the panel sockets and forgets about it, as
// the old console did through its bridge to this one.
func (h *handlers) publish(event *goapi.Object) {
	if h.d.StatusPush == nil {
		return
	}
	if err := h.d.StatusPush.PublishPanelEvent(event); err != nil {
		h.log.Warn("Panel event not published:", err.Error())
	}
}

// registerStatus answers GET /register-status?device_id=: the status of
// the device's registration, with the server configuration once it is
// approved and the reason once it is rejected. The device id is bound as
// the query parser made it (a list or an object fails on SQLite, BUGS
// LS9).
func (h *handlers) registerStatus(w http.ResponseWriter, r *http.Request) {
	deviceID := jsbind.Query(r.URL.RawQuery, "device_id")
	if !deviceID.Truthy() {
		failure(w, r, http.StatusBadRequest, "Missing device_id")
		return
	}
	reg, err := h.regs.ByDeviceIDValue(r.Context(), deviceID)
	if err != nil {
		h.log.Error("Register status error:", err)
		failure(w, r, http.StatusInternalServerError, "Internal server error")
		return
	}
	if reg == nil {
		kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "status", "not_found"))
		return
	}
	answer := goapi.NewObject("success", true, "status", reg.Get("status"), "id", reg.Get("id"))
	switch reg.Get("status") {
	case "approved":
		answer.Set("config", goapi.NewObject(
			"console_url", orValue(reg.Get("console_url"), ""),
			"server_address", orValue(reg.Get("server_address"), ""),
			"server_key", orValue(reg.Get("server_key"), ""),
			"access_token", orValue(reg.Get("access_token"), ""),
		))
	case "rejected":
		answer.Set("reason", orValue(reg.Get("rejected_reason"), ""))
	}
	kit.JSON(w, r, http.StatusOK, answer)
}
