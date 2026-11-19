package goapi

import (
	"context"
	"net/http"
)

// Enrollment: the mode and the devices that wait for approval.

// GetEnrollmentMode calls GET /api/enrollment/mode. W or F3.
func (c *Client) GetEnrollmentMode(ctx context.Context) *Object {
	return c.wrapped(ctx, f3, http.MethodGet, "/enrollment/mode")
}

// SetEnrollmentMode calls PUT /api/enrollment/mode with {mode}. W or F3.
func (c *Client) SetEnrollmentMode(ctx context.Context, mode any) *Object {
	return c.wrapped(ctx, f3, http.MethodPut, "/enrollment/mode", Body(NewObject("mode", mode)))
}

// GetEnrollmentPending calls GET /api/enrollment/pending. It returns
// {success:true, data: <devices>, count}, or {success:false, error,
// data:[], count:0}.
func (c *Client) GetEnrollmentPending(ctx context.Context) *Object {
	return c.enrollmentList(ctx, "/enrollment/pending")
}

// GetEnrollmentHistory calls GET /api/enrollment/history, with status
// ("approved" or "rejected") when one is given. Same shapes as
// GetEnrollmentPending.
func (c *Client) GetEnrollmentHistory(ctx context.Context, status any) *Object {
	var opts []CallOption
	if Truthy(status) {
		opts = append(opts, Query("status", status))
	}
	return c.enrollmentList(ctx, "/enrollment/history", opts...)
}

func (c *Client) enrollmentList(ctx context.Context, path string, opts ...CallOption) *Object {
	data, e := c.data(ctx, http.MethodGet, path, opts...)
	var devices, count any
	if e == nil {
		devices, e = propOf(data, "devices")
	}
	if e == nil {
		count, e = propOf(data, "count")
	}
	if e != nil {
		return NewObject("success", false, "error", e.Message, "data", []any{}, "count", float64(0))
	}
	return NewObject("success", true, "data", or(devices, []any{}), "count", or(count, float64(0)))
}

// ApproveEnrollment calls POST /api/enrollment/approve/{deviceID} with
// {display_name, sync_mode, tags}; sync_mode is "standard" when empty. W or
// F1.
func (c *Client) ApproveEnrollment(ctx context.Context, deviceID string, displayName, syncMode, tags any) *Object {
	return c.wrapped(ctx, f1, http.MethodPost, "/enrollment/approve/"+seg(deviceID), Body(NewObject(
		"display_name", or(displayName, ""),
		"sync_mode", or(syncMode, "standard"),
		"tags", or(tags, ""),
	)))
}

// RejectEnrollment calls POST /api/enrollment/reject/{deviceID} with {ban}.
// W or F1.
func (c *Client) RejectEnrollment(ctx context.Context, deviceID string, ban bool) *Object {
	return c.wrapped(ctx, f1, http.MethodPost, "/enrollment/reject/"+seg(deviceID), Body(NewObject("ban", ban)))
}

// ClearEnrollmentRejection calls POST
// /api/enrollment/clear-rejection/{deviceID}. W or F1.
func (c *Client) ClearEnrollmentRejection(ctx context.Context, deviceID string) *Object {
	return c.wrapped(ctx, f1, http.MethodPost, "/enrollment/clear-rejection/"+seg(deviceID))
}
