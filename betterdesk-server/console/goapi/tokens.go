package goapi

import (
	"context"
	"net/http"
)

// Device tokens. The id checks answer "Invalid tokenId" or "Invalid peerId"
// as F1 without a call.

// ListDeviceTokens calls GET /api/tokens, with include_revoked=true when
// asked. W or F1; "tokens" is null when there are none.
func (c *Client) ListDeviceTokens(ctx context.Context, includeRevoked bool) *Object {
	var opts []CallOption
	if includeRevoked {
		opts = append(opts, Query("include_revoked", "true"))
	}
	return c.wrapped(ctx, f1, http.MethodGet, "/tokens", opts...)
}

// CreateDeviceToken calls POST /api/tokens with body (name, max_uses,
// expires_in, note). W or F1; the answer holds the token itself once.
func (c *Client) CreateDeviceToken(ctx context.Context, body any) *Object {
	return c.wrapped(ctx, f1, http.MethodPost, "/tokens", Body(body))
}

// GetDeviceToken calls GET /api/tokens/{id}. W or F1.
func (c *Client) GetDeviceToken(ctx context.Context, id string) *Object {
	safe, e := safeID(id, "tokenId")
	if e != nil {
		return fail(f1, e)
	}
	return c.wrapped(ctx, f1, http.MethodGet, "/tokens/"+seg(safe))
}

// UpdateDeviceToken calls PUT /api/tokens/{id} with body (name, max_uses,
// note). W or F1.
func (c *Client) UpdateDeviceToken(ctx context.Context, id string, body any) *Object {
	safe, e := safeID(id, "tokenId")
	if e != nil {
		return fail(f1, e)
	}
	return c.wrapped(ctx, f1, http.MethodPut, "/tokens/"+seg(safe), Body(body))
}

// RevokeDeviceToken calls DELETE /api/tokens/{id}. W or F1.
func (c *Client) RevokeDeviceToken(ctx context.Context, id string) *Object {
	safe, e := safeID(id, "tokenId")
	if e != nil {
		return fail(f1, e)
	}
	return c.wrapped(ctx, f1, http.MethodDelete, "/tokens/"+seg(safe))
}

// BulkGenerateTokens calls POST /api/tokens/generate-bulk with body (count,
// name_prefix, max_uses, expires_in). W or F1.
func (c *Client) BulkGenerateTokens(ctx context.Context, body any) *Object {
	return c.wrapped(ctx, f1, http.MethodPost, "/tokens/generate-bulk", Body(body))
}

// BindTokenToPeer calls POST /api/tokens/{id}/bind with {peer_id}. W or F1.
func (c *Client) BindTokenToPeer(ctx context.Context, id, peerID string) *Object {
	safe, e := safeID(id, "tokenId")
	if e != nil {
		return fail(f1, e)
	}
	safePeer, e := safeID(peerID, "peerId")
	if e != nil {
		return fail(f1, e)
	}
	return c.wrapped(ctx, f1, http.MethodPost, "/tokens/"+seg(safe)+"/bind", Body(NewObject("peer_id", safePeer)))
}
