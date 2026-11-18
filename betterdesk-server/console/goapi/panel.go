package goapi

import (
	"context"
	"math"
	"net/http"
)

// The panel catalog and the panel grouping of the API server
// (/api/panel/...): device groups with their members and access lists,
// folders with their devices, and the devices granted to one account. The
// API server writes these; the console's device group routes send their
// writes here. opts carry As with the operator's identity while the panel
// trust bridge is on.

// GetPanelCatalog calls GET /api/panel/catalog. W or F1.
func (c *Client) GetPanelCatalog(ctx context.Context, opts ...CallOption) *Object {
	return c.wrapped(ctx, f1, http.MethodGet, "/panel/catalog", opts...)
}

// ListPanelDeviceGroups calls GET /api/panel/device-groups. W or F1.
func (c *Client) ListPanelDeviceGroups(ctx context.Context, opts ...CallOption) *Object {
	return c.wrapped(ctx, f1, http.MethodGet, "/panel/device-groups", opts...)
}

// SavePanelDeviceGroup calls POST /api/panel/device-groups with payload:
// a group without guid is created, one with a guid changed. W or F4.
func (c *Client) SavePanelDeviceGroup(ctx context.Context, payload any, opts ...CallOption) *Object {
	return c.wrapped(ctx, f4, http.MethodPost, "/panel/device-groups", append([]CallOption{Body(payload)}, opts...)...)
}

// DeletePanelDeviceGroup calls DELETE /api/panel/device-groups/{guid}. W or
// F4; a guid with other characters than letters, digits and . _ : @ % + -
// gives "Invalid guid" without a call.
func (c *Client) DeletePanelDeviceGroup(ctx context.Context, guid string, opts ...CallOption) *Object {
	safe, e := safeID(guid, "guid")
	if e != nil {
		return fail(f4, e)
	}
	return c.wrapped(ctx, f4, http.MethodDelete, "/panel/device-groups/"+seg(safe), opts...)
}

// GetPanelDeviceGroupPeers calls GET /api/panel/device-groups/{guid}/peers:
// the ids of the group's members. W or F1, as DeletePanelDeviceGroup for
// the guid.
func (c *Client) GetPanelDeviceGroupPeers(ctx context.Context, guid string, opts ...CallOption) *Object {
	safe, e := safeID(guid, "guid")
	if e != nil {
		return fail(f1, e)
	}
	return c.wrapped(ctx, f1, http.MethodGet, "/panel/device-groups/"+seg(safe)+"/peers", opts...)
}

// SetPanelDeviceGroupPeers calls PUT /api/panel/device-groups/{guid}/peers
// with {peers}: the group's members become peers, which is a list or else
// empty. W or F4, as DeletePanelDeviceGroup for the guid.
func (c *Client) SetPanelDeviceGroupPeers(ctx context.Context, guid string, peers any, opts ...CallOption) *Object {
	safe, e := safeID(guid, "guid")
	if e != nil {
		return fail(f4, e)
	}
	body := Body(NewObject("peers", listOrEmpty(peers)))
	return c.wrapped(ctx, f4, http.MethodPut, "/panel/device-groups/"+seg(safe)+"/peers", append([]CallOption{body}, opts...)...)
}

// GetPanelDeviceGroupACL calls GET /api/panel/device-groups/{guid}/acl: the
// accounts and user groups that may see the group. W or F1, as
// DeletePanelDeviceGroup for the guid.
func (c *Client) GetPanelDeviceGroupACL(ctx context.Context, guid string, opts ...CallOption) *Object {
	safe, e := safeID(guid, "guid")
	if e != nil {
		return fail(f1, e)
	}
	return c.wrapped(ctx, f1, http.MethodGet, "/panel/device-groups/"+seg(safe)+"/acl", opts...)
}

// SetPanelDeviceGroupACL calls PUT /api/panel/device-groups/{guid}/acl with
// the allowed_users and allowed_user_groups of acl, each a list or else
// empty. W or F4, as DeletePanelDeviceGroup for the guid.
func (c *Client) SetPanelDeviceGroupACL(ctx context.Context, guid string, acl any, opts ...CallOption) *Object {
	safe, e := safeID(guid, "guid")
	if e != nil {
		return fail(f4, e)
	}
	body := Body(NewObject(
		"allowed_users", listOrEmpty(prop(acl, "allowed_users")),
		"allowed_user_groups", listOrEmpty(prop(acl, "allowed_user_groups")),
	))
	return c.wrapped(ctx, f4, http.MethodPut, "/panel/device-groups/"+seg(safe)+"/acl", append([]CallOption{body}, opts...)...)
}

// ListPanelFolders calls GET /api/panel/folders: the folders and which
// device is in which. W or F1.
func (c *Client) ListPanelFolders(ctx context.Context, opts ...CallOption) *Object {
	return c.wrapped(ctx, f1, http.MethodGet, "/panel/folders", opts...)
}

// CreatePanelFolder calls POST /api/panel/folders with folder. W or F4.
func (c *Client) CreatePanelFolder(ctx context.Context, folder any, opts ...CallOption) *Object {
	return c.wrapped(ctx, f4, http.MethodPost, "/panel/folders", append([]CallOption{Body(folder)}, opts...)...)
}

// UpdatePanelFolder calls PUT /api/panel/folders/{id} with folder. The id
// in the path is JavaScript's Number(id), so text that is no number gives
// NaN. W or F4.
func (c *Client) UpdatePanelFolder(ctx context.Context, id, folder any, opts ...CallOption) *Object {
	return c.wrapped(ctx, f4, http.MethodPut, "/panel/folders/"+jsNumberString(jsNumber(id)), append([]CallOption{Body(folder)}, opts...)...)
}

// DeletePanelFolder calls DELETE /api/panel/folders/{id}, the id as in
// UpdatePanelFolder. W or F4.
func (c *Client) DeletePanelFolder(ctx context.Context, id any, opts ...CallOption) *Object {
	return c.wrapped(ctx, f4, http.MethodDelete, "/panel/folders/"+jsNumberString(jsNumber(id)), opts...)
}

// SetPanelDeviceFolder calls PUT /api/panel/devices/{id}/folder with
// {folder_id}: Number(folderID), 0 for no number, which takes the device
// out of its folder. W or F4; an id with other characters than letters,
// digits and . _ : @ % + - gives "Invalid device id" without a call.
func (c *Client) SetPanelDeviceFolder(ctx context.Context, deviceID string, folderID any, opts ...CallOption) *Object {
	safe, e := safeID(deviceID, "device id")
	if e != nil {
		return fail(f4, e)
	}
	folder := jsNumber(folderID)
	if math.IsNaN(folder) || folder == 0 {
		folder = 0
	}
	body := Body(NewObject("folder_id", folder))
	return c.wrapped(ctx, f4, http.MethodPut, "/panel/devices/"+seg(safe)+"/folder", append([]CallOption{body}, opts...)...)
}

// ListPanelUserGrants calls GET /api/panel/users/{id}/grants: the devices
// granted to the account directly, the id as in UpdatePanelFolder. W or
// F1.
func (c *Client) ListPanelUserGrants(ctx context.Context, userID any, opts ...CallOption) *Object {
	return c.wrapped(ctx, f1, http.MethodGet, "/panel/users/"+jsNumberString(jsNumber(userID))+"/grants", opts...)
}

// SetPanelUserGrants calls PUT /api/panel/users/{id}/grants with {peers}, a
// list or else empty, the id as in UpdatePanelFolder. W or F4.
func (c *Client) SetPanelUserGrants(ctx context.Context, userID, peers any, opts ...CallOption) *Object {
	body := Body(NewObject("peers", listOrEmpty(peers)))
	return c.wrapped(ctx, f4, http.MethodPut, "/panel/users/"+jsNumberString(jsNumber(userID))+"/grants", append([]CallOption{body}, opts...)...)
}

// listOrEmpty is "Array.isArray(v) ? v : []" for a parsed JSON value.
func listOrEmpty(v any) []any {
	if list, ok := v.([]any); ok {
		return list
	}
	return []any{}
}
