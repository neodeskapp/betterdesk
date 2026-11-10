package goapi

import (
	"context"
	"net/http"

	"github.com/unitronix/betterdesk-server/logging"
)

// GetAllRemoteTargets calls GET /api/remote-targets with org_id (also when
// empty) and include_disabled=true or false, and returns the targets as
// devices (see normaliseRemoteTarget). Every failure gives an empty list.
func (c *Client) GetAllRemoteTargets(ctx context.Context, orgID string, includeDisabled bool) []*Object {
	include := "false"
	if includeDisabled {
		include = "true"
	}
	data, e := c.data(ctx, http.MethodGet, "/remote-targets", Query("org_id", orgID), Query("include_disabled", include))
	var targets []any
	if e == nil {
		targets, e = listOf(data, "targets")
	}
	if e != nil {
		logging.Logf("goapi", "getAllRemoteTargets error: %s", e.Message)
		return []*Object{}
	}
	out := make([]*Object, len(targets))
	for i, t := range targets {
		out[i] = normaliseRemoteTarget(t)
	}
	return out
}

// TestRemoteTarget calls POST /api/remote-targets/test with payload. W or
// F2.
func (c *Client) TestRemoteTarget(ctx context.Context, payload any) *Object {
	return c.wrapped(ctx, f2, http.MethodPost, "/remote-targets/test", Body(payload))
}

// GetRemoteTarget calls GET /api/remote-targets/{id} and returns the target
// as a device. Every failure gives nil.
func (c *Client) GetRemoteTarget(ctx context.Context, id string) *Object {
	data, e := c.data(ctx, http.MethodGet, "/remote-targets/"+seg(id))
	if e != nil {
		return nil
	}
	return normaliseRemoteTarget(data)
}

// CreateRemoteTarget calls POST /api/remote-targets with payload. W or F2.
func (c *Client) CreateRemoteTarget(ctx context.Context, payload any) *Object {
	return c.wrapped(ctx, f2, http.MethodPost, "/remote-targets", Body(payload))
}

// UpdateRemoteTarget calls PATCH /api/remote-targets/{id} with payload. W
// or F2.
func (c *Client) UpdateRemoteTarget(ctx context.Context, id string, payload any) *Object {
	return c.wrapped(ctx, f2, http.MethodPatch, "/remote-targets/"+seg(id), Body(payload))
}

// DeleteRemoteTarget calls DELETE /api/remote-targets/{id}. W or F2.
func (c *Client) DeleteRemoteTarget(ctx context.Context, id string) *Object {
	return c.wrapped(ctx, f2, http.MethodDelete, "/remote-targets/"+seg(id))
}

// GetRemoteTargetCredentialStatus calls GET
// /api/remote-targets/{id}/credentials. W or F2.
func (c *Client) GetRemoteTargetCredentialStatus(ctx context.Context, id string) *Object {
	return c.wrapped(ctx, f2, http.MethodGet, "/remote-targets/"+seg(id)+"/credentials")
}

// SetRemoteTargetCredentials calls PUT /api/remote-targets/{id}/credentials
// with payload. W or F2.
func (c *Client) SetRemoteTargetCredentials(ctx context.Context, id string, payload any) *Object {
	return c.wrapped(ctx, f2, http.MethodPut, "/remote-targets/"+seg(id)+"/credentials", Body(payload))
}

// ClearRemoteTargetCredentials calls DELETE
// /api/remote-targets/{id}/credentials. W or F2.
func (c *Client) ClearRemoteTargetCredentials(ctx context.Context, id string) *Object {
	return c.wrapped(ctx, f2, http.MethodDelete, "/remote-targets/"+seg(id)+"/credentials")
}

// AcceptRemoteTargetCertificate calls POST
// /api/remote-targets/{id}/certificate/accept with {fingerprint}. W or F2.
func (c *Client) AcceptRemoteTargetCertificate(ctx context.Context, id string, fingerprint any) *Object {
	return c.wrapped(ctx, f2, http.MethodPost, "/remote-targets/"+seg(id)+"/certificate/accept",
		Body(NewObject("fingerprint", fingerprint)))
}
