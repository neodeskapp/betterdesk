package goapi

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/unitronix/betterdesk-server/logging"
)

// Peers and their tags.

// GetAllPeers calls GET /api/peers, with include_deleted=true when asked,
// and returns the peers normalised (NormalisePeer). Every failure gives an
// empty list.
func (c *Client) GetAllPeers(ctx context.Context, includeDeleted bool) []*Object {
	path := "/peers"
	if includeDeleted {
		path += "?include_deleted=true"
	}
	data, e := c.data(ctx, http.MethodGet, path)
	var peers []any
	if e == nil {
		peers, e = listOf(data, "peers")
	}
	if e != nil {
		logging.Logf("goapi", "getAllPeers error: %s", e.Message)
		return []*Object{}
	}
	return normalisePeers(peers, c.now())
}

// GetPeerIncludingDeleted returns the peer like GetPeer, and looks for it
// among the deleted ones (GET /api/peers?include_deleted=true) when that
// finds nothing. Nil when there is none.
func (c *Client) GetPeerIncludingDeleted(ctx context.Context, id string) *Object {
	if active := c.GetPeer(ctx, id); active != nil {
		return active
	}
	for _, p := range c.GetAllPeers(ctx, true) {
		if p != nil && p.Get("id") == id {
			return p
		}
	}
	return nil
}

// GetPeer calls GET /api/peers/{id} and returns the peer normalised. Every
// failure gives nil: a missing peer, a timeout and an error look the same.
func (c *Client) GetPeer(ctx context.Context, id string) *Object {
	data, e := c.data(ctx, http.MethodGet, "/peers/"+seg(id))
	if e != nil {
		return nil
	}
	return normalisePeer(data, c.now())
}

// DeleteOptions are the switches of DeletePeer.
type DeleteOptions struct {
	Revoke  bool
	Cascade bool
	Hard    bool
	// CascadeOnly, when not nil, names the linked peers the cascade may
	// delete; the API server keeps any other linked peer. nil lets the
	// cascade delete every linked peer.
	CascadeOnly []string
}

// DeletePeer calls DELETE /api/peers/{id} with revoke=true, cascade=true
// and hard=true for the options that are set, and cascade_only with the
// ids of CascadeOnly for a cascade that names them. W or F2.
func (c *Client) DeletePeer(ctx context.Context, id string, opts DeleteOptions) *Object {
	var flags []string
	if opts.Revoke {
		flags = append(flags, "revoke=true")
	}
	if opts.Cascade {
		flags = append(flags, "cascade=true")
		if opts.CascadeOnly != nil {
			flags = append(flags, "cascade_only="+url.QueryEscape(strings.Join(opts.CascadeOnly, ",")))
		}
	}
	if opts.Hard {
		flags = append(flags, "hard=true")
	}
	path := "/peers/" + seg(id)
	if len(flags) > 0 {
		path += "?" + strings.Join(flags, "&")
	}
	return c.wrapped(ctx, f2, http.MethodDelete, path)
}

// BanPeer calls POST /api/peers/{id}/ban with {reason}. W or F2.
func (c *Client) BanPeer(ctx context.Context, id, reason string) *Object {
	return c.wrapped(ctx, f2, http.MethodPost, "/peers/"+seg(id)+"/ban", Body(NewObject("reason", reason)))
}

// UnbanPeer calls POST /api/peers/{id}/unban. W or F2.
func (c *Client) UnbanPeer(ctx context.Context, id string) *Object {
	return c.wrapped(ctx, f2, http.MethodPost, "/peers/"+seg(id)+"/unban")
}

// RestorePeer calls POST /api/peers/{id}/restore. W or F2.
func (c *Client) RestorePeer(ctx context.Context, id string) *Object {
	return c.wrapped(ctx, f2, http.MethodPost, "/peers/"+seg(id)+"/restore")
}

// ChangePeerID calls POST /api/peers/{oldID}/change-id with {new_id}. W or
// F2.
func (c *Client) ChangePeerID(ctx context.Context, oldID, newID string) *Object {
	return c.wrapped(ctx, f2, http.MethodPost, "/peers/"+seg(oldID)+"/change-id", Body(NewObject("new_id", newID)))
}

// UpdatePeer calls PATCH /api/peers/{id} with fields as the body. The API
// server reads note, user, tags (as text) and display_name. W or F2.
func (c *Client) UpdatePeer(ctx context.Context, id string, fields any) *Object {
	return c.wrapped(ctx, f2, http.MethodPatch, "/peers/"+seg(id), Body(fields))
}

// GetLinkedPeers calls GET /api/peers/{id}/linked. W, or {success:false,
// error, linked:[], total:0}.
func (c *Client) GetLinkedPeers(ctx context.Context, id string) *Object {
	data, e := c.data(ctx, http.MethodGet, "/peers/"+seg(id)+"/linked")
	if e != nil {
		return NewObject("success", false, "error", e.Message, "linked", []any{}, "total", float64(0))
	}
	return wrap(data)
}

// LinkDevice calls PATCH /api/peers/{id} with {linked_peer_id}. The API
// server does not read that field and answers 400, so this returns F1.
func (c *Client) LinkDevice(ctx context.Context, id string, linkedPeerID any) *Object {
	return c.wrapped(ctx, f1, http.MethodPatch, "/peers/"+seg(id), Body(NewObject("linked_peer_id", or(linkedPeerID, ""))))
}

// SetPeerTags calls PUT /api/peers/{id}/tags with {tags: [...]}. Tags given
// as one text are split at commas; anything that is neither a list nor a
// text sends an empty list. W or F2.
func (c *Client) SetPeerTags(ctx context.Context, id string, tags any) *Object {
	var payload any = []any{}
	switch t := tags.(type) {
	case []any:
		payload = t
	case []string:
		list := make([]any, len(t))
		for i, tag := range t {
			list[i] = tag
		}
		payload = list
	case string:
		list := []any{}
		for _, part := range strings.Split(t, ",") {
			if part = jsTrim(part); part != "" {
				list = append(list, part)
			}
		}
		payload = list
	}
	return c.wrapped(ctx, f2, http.MethodPut, "/peers/"+seg(id)+"/tags", Body(NewObject("tags", payload)))
}

// GetPeersByTag calls GET /api/tags/{tag}/peers and returns the peers
// normalised. The rows carry no live state, so "online" is always false.
// Every failure gives an empty list.
func (c *Client) GetPeersByTag(ctx context.Context, tag string) []*Object {
	data, e := c.data(ctx, http.MethodGet, "/tags/"+seg(tag)+"/peers")
	var peers []any
	if e == nil {
		peers, e = listOf(data, "peers")
	}
	if e != nil {
		return []*Object{}
	}
	return normalisePeers(peers, c.now())
}
