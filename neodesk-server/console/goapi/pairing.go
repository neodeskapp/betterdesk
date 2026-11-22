package goapi

import (
	"context"
	"net/http"
)

// Pairing codes. These keep the API server's own "success" inside "data"
// and carry the HTTP status of a failed call.

func (c *Client) pairing(ctx context.Context, method, path string, opts ...CallOption) *Object {
	data, e := c.data(ctx, method, path, opts...)
	if e != nil {
		o := NewObject("success", false, "error", e.Message)
		if e.Response != nil {
			o.Set("status", float64(e.Response.Status))
		}
		return o
	}
	return NewObject("success", true, "data", data)
}

// CreatePairingCode calls POST /api/pairing/codes with {label}. It returns
// {success:true, data} with the code once, or {success:false, error,
// status}; status is missing when there was no answer.
func (c *Client) CreatePairingCode(ctx context.Context, label any) *Object {
	return c.pairing(ctx, http.MethodPost, "/pairing/codes", Body(NewObject("label", or(label, ""))))
}

// ListPairingCodes calls GET /api/pairing/codes. Shapes as
// CreatePairingCode.
func (c *Client) ListPairingCodes(ctx context.Context) *Object {
	return c.pairing(ctx, http.MethodGet, "/pairing/codes")
}

// RevokePairingCode calls DELETE /api/pairing/codes/{id}. Shapes as
// CreatePairingCode.
func (c *Client) RevokePairingCode(ctx context.Context, id string) *Object {
	return c.pairing(ctx, http.MethodDelete, "/pairing/codes/"+seg(id))
}
