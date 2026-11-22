package goapi

import (
	"context"
	"net/http"
)

// GetMeshStatus calls GET /api/mesh/status. W, or {success:false, error,
// data:{enabled:false}}.
func (c *Client) GetMeshStatus(ctx context.Context) *Object {
	data, e := c.data(ctx, http.MethodGet, "/mesh/status")
	if e != nil {
		return NewObject("success", false, "error", e.Message, "data", NewObject("enabled", false))
	}
	return wrap(data)
}

// GetCDAPStatus calls GET /api/cdap/status. W or F1.
func (c *Client) GetCDAPStatus(ctx context.Context) *Object {
	return c.wrapped(ctx, f1, http.MethodGet, "/cdap/status")
}

// GetCDAPDevices calls GET /api/cdap/devices. W or F1.
func (c *Client) GetCDAPDevices(ctx context.Context) *Object {
	return c.wrapped(ctx, f1, http.MethodGet, "/cdap/devices")
}

// GetCDAPDeviceInfo calls GET /api/cdap/devices/{id}. W or F1.
func (c *Client) GetCDAPDeviceInfo(ctx context.Context, id string) *Object {
	return c.wrapped(ctx, f1, http.MethodGet, "/cdap/devices/"+seg(id))
}

// GetCDAPDeviceManifest calls GET /api/cdap/devices/{id}/manifest. W or F1.
func (c *Client) GetCDAPDeviceManifest(ctx context.Context, id string) *Object {
	return c.wrapped(ctx, f1, http.MethodGet, "/cdap/devices/"+seg(id)+"/manifest")
}

// GetCDAPDeviceState calls GET /api/cdap/devices/{id}/state. W or F1.
func (c *Client) GetCDAPDeviceState(ctx context.Context, id string) *Object {
	return c.wrapped(ctx, f1, http.MethodGet, "/cdap/devices/"+seg(id)+"/state")
}

// SendCDAPCommand calls POST /api/cdap/devices/{id}/command with
// {widget_id, action, value, reason}; an empty reason is sent as "". W or
// F1.
func (c *Client) SendCDAPCommand(ctx context.Context, id string, widgetID, action, value, reason any) *Object {
	return c.wrapped(ctx, f1, http.MethodPost, "/cdap/devices/"+seg(id)+"/command",
		Body(NewObject("widget_id", widgetID, "action", action, "value", value, "reason", or(reason, ""))))
}

// GetCDAPAlerts calls GET /api/cdap/alerts, with device_id when one is
// given. W, or {success:false, error, alerts:[], total:0}.
func (c *Client) GetCDAPAlerts(ctx context.Context, deviceID any) *Object {
	var opts []CallOption
	if Truthy(deviceID) {
		opts = append(opts, Query("device_id", deviceID))
	}
	data, e := c.data(ctx, http.MethodGet, "/cdap/alerts", opts...)
	if e != nil {
		return NewObject("success", false, "error", e.Message, "alerts", []any{}, "total", float64(0))
	}
	return wrap(data)
}
