package goapi

import (
	"context"
	"net/http"
)

// Unattended access policies and the stored connect password.

// GetAccessPolicy calls GET /api/peers/{id}/access-policy. W or F1; an id
// with other characters than letters, digits and . _ : @ % + - gives
// "Invalid peerId" without a call.
func (c *Client) GetAccessPolicy(ctx context.Context, id string) *Object {
	safe, e := safeID(id, "peerId")
	if e != nil {
		return fail(f1, e)
	}
	return c.wrapped(ctx, f1, http.MethodGet, "/peers/"+seg(safe)+"/access-policy")
}

// SaveAccessPolicy calls PUT /api/peers/{id}/access-policy with policy. W
// or F1, as GetAccessPolicy.
func (c *Client) SaveAccessPolicy(ctx context.Context, id string, policy any) *Object {
	safe, e := safeID(id, "peerId")
	if e != nil {
		return fail(f1, e)
	}
	return c.wrapped(ctx, f1, http.MethodPut, "/peers/"+seg(safe)+"/access-policy", Body(policy))
}

// DeleteAccessPolicy calls DELETE /api/peers/{id}/access-policy. W or F1,
// as GetAccessPolicy.
func (c *Client) DeleteAccessPolicy(ctx context.Context, id string) *Object {
	safe, e := safeID(id, "peerId")
	if e != nil {
		return fail(f1, e)
	}
	return c.wrapped(ctx, f1, http.MethodDelete, "/peers/"+seg(safe)+"/access-policy")
}

// GetPeerConnectPassword calls GET /api/peers/{id}/connect-password and
// returns the answer as it is. It is the one method that returns the
// failed call: the error is an *Error, a 404 carries {password_set:false}
// as its data, and a bad id gives "Invalid peerId" without a call. opts
// change the call; the device route makes it as the signed-in account
// (As), so the API server's organization membership check decides.
func (c *Client) GetPeerConnectPassword(ctx context.Context, id string, opts ...CallOption) (any, error) {
	safe, e := safeID(id, "peerId")
	if e != nil {
		return nil, e
	}
	data, e := c.data(ctx, http.MethodGet, "/peers/"+seg(safe)+"/connect-password", opts...)
	if e != nil {
		return nil, e
	}
	return data, nil
}
