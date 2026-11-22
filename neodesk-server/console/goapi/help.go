package goapi

import (
	"context"
	"net/http"
)

// Help requests.

// ListHelpRequests calls GET /api/help/requests with the status, device_id
// and limit of filter (an *Object or map[string]any) that are set. It
// returns {success:true, data: <requests>}, or F2.
func (c *Client) ListHelpRequests(ctx context.Context, filter any) *Object {
	var opts []CallOption
	for _, key := range []string{"status", "device_id", "limit"} {
		if v := prop(filter, key); Truthy(v) {
			opts = append(opts, Query(key, v))
		}
	}
	data, e := c.data(ctx, http.MethodGet, "/help/requests", opts...)
	if e != nil {
		return fail(f2, e)
	}
	if _, isList := data.([]any); isList {
		return NewObject("success", true, "data", data)
	}
	requests, e := propOf(data, "requests")
	if e != nil {
		return fail(f2, e)
	}
	return NewObject("success", true, "data", or(requests, []any{}))
}

// AcknowledgeHelpRequest calls POST /api/help/requests/{id}/acknowledge. W
// or F2; a bad id gives "Invalid requestId" without a call.
func (c *Client) AcknowledgeHelpRequest(ctx context.Context, id string) *Object {
	return c.helpRequestAction(ctx, id, "acknowledge")
}

// ResolveHelpRequest calls POST /api/help/requests/{id}/resolve. W or F2,
// as AcknowledgeHelpRequest.
func (c *Client) ResolveHelpRequest(ctx context.Context, id string) *Object {
	return c.helpRequestAction(ctx, id, "resolve")
}

func (c *Client) helpRequestAction(ctx context.Context, id, action string) *Object {
	safe, e := safeID(id, "requestId")
	if e != nil {
		return fail(f2, e)
	}
	return c.wrapped(ctx, f2, http.MethodPost, "/help/requests/"+seg(safe)+"/"+action)
}
