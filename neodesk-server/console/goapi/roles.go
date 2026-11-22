package goapi

import (
	"context"
	"net/http"
)

// Roles and permissions. A refused call is F3: the API server's status and
// its reason. The id checks answer "Invalid role" or "Invalid permission"
// as F1 without a call.

// ListRoles calls GET /api/roles. W or F3.
func (c *Client) ListRoles(ctx context.Context) *Object {
	return c.wrapped(ctx, f3, http.MethodGet, "/roles")
}

// GetRolePermissions calls GET /api/roles/{role}/permissions: the defaults
// of the role with the overrides applied. W or F3.
func (c *Client) GetRolePermissions(ctx context.Context, role string) *Object {
	safe, e := safeID(role, "role")
	if e != nil {
		return fail(f1, e)
	}
	return c.wrapped(ctx, f3, http.MethodGet, "/roles/"+seg(safe)+"/permissions")
}

// ListRolePermissionOverrides calls GET /api/role-permissions, with role
// when one is given. W or F3.
func (c *Client) ListRolePermissionOverrides(ctx context.Context, role string) *Object {
	path := "/role-permissions"
	if role != "" {
		path += "?role=" + seg(role)
	}
	return c.wrapped(ctx, f3, http.MethodGet, path)
}

// SetRolePermission calls POST /api/role-permissions with {role,
// permission, granted}. W or F3.
func (c *Client) SetRolePermission(ctx context.Context, role, permission string, granted any) *Object {
	safeRole, e := safeID(role, "role")
	if e != nil {
		return fail(f1, e)
	}
	safePermission, e := safeID(permission, "permission")
	if e != nil {
		return fail(f1, e)
	}
	return c.wrapped(ctx, f3, http.MethodPost, "/role-permissions",
		Body(NewObject("role", safeRole, "permission", safePermission, "granted", granted)))
}

// DeleteRolePermission calls DELETE
// /api/role-permissions/{role}/{permission}. W or F3.
func (c *Client) DeleteRolePermission(ctx context.Context, role, permission string) *Object {
	safeRole, e := safeID(role, "role")
	if e != nil {
		return fail(f1, e)
	}
	safePermission, e := safeID(permission, "permission")
	if e != nil {
		return fail(f1, e)
	}
	return c.wrapped(ctx, f3, http.MethodDelete, "/role-permissions/"+seg(safeRole)+"/"+seg(safePermission))
}
