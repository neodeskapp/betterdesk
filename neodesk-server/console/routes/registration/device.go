package registration

import (
	"errors"
	"net/http"
	"regexp"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/registrations"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsreq"
)

// The two routes devices call without signing in.

// deviceIDPattern is the form of a device id, as /api/nd/register checks
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
// credential is needed. A request for a device without a registration
// counts against the limit of device registrations (d.Limits.Register,
// ND_REGISTER_RATE_LIMIT_MAX a minute per address, shared with
// /api/nd/register), which answers 429 past it; a request for a device
// that has its registration, or one refused for its fields, counts not.
// A new registration is also refused with 429 while
// registrations.MaxPending wait, or registrations.MaxPendingPerAddress
// from the client's address. The device id, trimmed, has to be an id
// /api/nd/register takes: 3 to 64 letters, digits, "_" and "-". The other
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
	if !h.allowNew(w, r, deviceID) {
		return
	}
	row, err := h.regs.Create(r.Context(), req)
	if errors.Is(err, registrations.ErrTooManyPending) {
		failure(w, r, http.StatusTooManyRequests, "Too many pending registrations")
		return
	}
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

// allowNew counts a request for a device without a registration against
// the limit of device registrations, and reports whether the request may
// go on; when not, the limiter or the failed lookup answered it.
func (h *handlers) allowNew(w http.ResponseWriter, r *http.Request, deviceID string) bool {
	if h.limit == nil {
		return true
	}
	existing, err := h.regs.ByDeviceID(r.Context(), deviceID)
	if err != nil {
		h.log.Error("Register request error:", err)
		failure(w, r, http.StatusInternalServerError, "Internal server error")
		return false
	}
	if existing != nil {
		return true
	}
	allowed := false
	h.limit(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { allowed = true })).ServeHTTP(w, r)
	return allowed
}

// statusDeviceID is the device id a status request names: device_id as
// text, the first one when the query gives it more than once or as a
// list; "" for none and for an object.
func statusDeviceID(r *http.Request) string {
	v := jsbind.Query(r.URL.RawQuery, "device_id")
	if v.IsArray() {
		items := v.Items()
		if len(items) == 0 {
			return ""
		}
		v = items[0]
	}
	if v.IsString() {
		return v.Str()
	}
	return ""
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
// approved and the reason once it is rejected. The first answer after
// the approval carries the device's device token in access_token, which
// the device routes take; later answers carry "" there (deviceToken).
func (h *handlers) registerStatus(w http.ResponseWriter, r *http.Request) {
	deviceID := statusDeviceID(r)
	if deviceID == "" {
		failure(w, r, http.StatusBadRequest, "Missing device_id")
		return
	}
	reg, err := h.regs.ByDeviceID(r.Context(), deviceID)
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
			"access_token", h.deviceToken(r, deviceID, reg.Get("access_token")),
		))
	case "rejected":
		answer.Set("reason", orValue(reg.Get("rejected_reason"), ""))
	}
	kit.JSON(w, r, http.StatusOK, answer)
}

// deviceToken is the access token of an approved registration's status:
// a device token issued now for the device when the registration's token
// is due (registrations.DeliverToken), else what
// registrations.DeliveredToken shows for the stored value. The token is
// held by no account, so the approver's account may be gone. A token that
// cannot be issued (the id is no device id) is logged and stays due; the
// answer then carries "".
func (h *handlers) deviceToken(r *http.Request, deviceID string, stored any) string {
	if !registrations.TokenDue(stored) {
		return registrations.DeliveredToken(stored)
	}
	ctx := r.Context()
	token, err := h.regs.DeliverToken(ctx, deviceID, func(string) (string, error) {
		return h.d.Authn.IssueDeviceToken(ctx, deviceID, "Issued at the approval of its registration")
	})
	if err != nil {
		h.log.Error("Register status error:", err)
	}
	if token != "" {
		return token
	}
	return registrations.DeliveredToken(stored)
}
