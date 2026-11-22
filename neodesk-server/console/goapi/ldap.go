package goapi

import (
	"context"
	"net/http"
)

// Directory (LDAP) settings.

// GetLDAPConfig calls GET /api/auth/ldap/config. W or F1; the bind password
// is masked.
func (c *Client) GetLDAPConfig(ctx context.Context) *Object {
	return c.wrapped(ctx, f1, http.MethodGet, "/auth/ldap/config")
}

// SaveLDAPConfig calls PUT /api/auth/ldap/config with config. W or F1.
func (c *Client) SaveLDAPConfig(ctx context.Context, config any) *Object {
	return c.wrapped(ctx, f1, http.MethodPut, "/auth/ldap/config", Body(config))
}

// TestLDAPConnection calls POST /api/auth/ldap/test with config. W or F1;
// the API server answers 200 for both outcomes of the test.
func (c *Client) TestLDAPConnection(ctx context.Context, config any) *Object {
	return c.wrapped(ctx, f1, http.MethodPost, "/auth/ldap/test", Body(config))
}
