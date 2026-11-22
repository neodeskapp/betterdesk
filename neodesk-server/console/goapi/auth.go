package goapi

import (
	"context"
	"net/http"
	"time"
)

// The sign-in calls of the console's auth service. They reach paths of the
// API server that need no credential, with nothing but the JSON body, and
// they share the API server's sign-in limit of five calls in five minutes,
// which counts per caller address: every one of these calls comes from the
// console's address.

const (
	healthCacheFor = 10 * time.Second
	healthTimeout  = 2 * time.Second
	ssoCacheFor    = 60 * time.Second
	signInTimeout  = 5 * time.Second
	fallbackWait   = 3 * time.Second
)

// plain are the options of a call that carries no header of the axios
// client: these calls were made with Node's own HTTP client.
func plain(hasBody bool) []CallOption {
	opts := []CallOption{outsideAPI(), withoutHeader("Accept"), withoutHeader("User-Agent")}
	if !hasBody {
		opts = append(opts, withoutHeader("Content-Type"))
	}
	return opts
}

// cappedTimeout is the client's timeout, at most the sign-in cap of five
// seconds.
func (c *Client) cappedTimeout() time.Duration {
	if c.timeout <= 0 || c.timeout > c.signInCap {
		return c.signInCap
	}
	return c.timeout
}

// ownTimeout is the client's timeout, or three seconds when it has none.
func (c *Client) ownTimeout() time.Duration {
	if c.timeout <= 0 {
		return fallbackWait
	}
	return c.timeout
}

// signIn posts a JSON body to a sign-in path and returns the status and the
// parsed body. ok is false when there was no answer or the body is not
// JSON.
func (c *Client) signIn(ctx context.Context, path string, body *Object, timeout time.Duration) (status int, parsed any, ok bool) {
	opts := append(plain(true), Body(body), Timeout(timeout), ValidateStatus(func(int) bool { return true }))
	resp, e := c.do(ctx, http.MethodPost, path, opts...)
	if e != nil {
		return 0, nil, false
	}
	parsed, err := Parse(resp.Body)
	if err != nil {
		return resp.Status, nil, false
	}
	return resp.Status, parsed, true
}

// CheckGoServerHealth calls GET /api/health with a two second limit and
// reports whether the status was 200 to 399. The result is kept for ten
// seconds.
func (c *Client) CheckGoServerHealth(ctx context.Context) bool {
	c.cacheMu.Lock()
	if c.healthKnown && c.now().Sub(c.healthAt) < healthCacheFor {
		ok := c.healthOK
		c.cacheMu.Unlock()
		return ok
	}
	c.cacheMu.Unlock()

	opts := append(plain(false), Timeout(healthTimeout), ValidateStatus(func(int) bool { return true }))
	resp, e := c.do(ctx, http.MethodGet, "/api/health", opts...)
	ok := e == nil && resp.Status >= 200 && resp.Status < 400

	c.cacheMu.Lock()
	c.healthOK, c.healthKnown, c.healthAt = ok, true, c.now()
	c.cacheMu.Unlock()
	return ok
}

// AuthenticateViaGo calls POST /api/auth/login with {username, password}.
// It returns the API server's body for status 200 ({token, role, username}
// or {requires_2fa, partial_token}), {rejected:true, status, error} for 400
// and 401, {rejected:true, rateLimited:true, error} for 429, and nil for
// everything else, a missing answer included.
func (c *Client) AuthenticateViaGo(ctx context.Context, username, password string) any {
	status, parsed, ok := c.signIn(ctx, "/api/auth/login",
		NewObject("username", username, "password", password), c.cappedTimeout())
	if !ok {
		return nil
	}
	if status == http.StatusOK {
		return parsed
	}
	return rejection(status, parsed, true)
}

// VerifyTOTPViaGo calls POST /api/auth/login/2fa with {partial_token,
// code}. It returns the API server's body for status 200 when it has a
// token, {rejected:true, error} for 400 and 401, {rejected:true,
// rateLimited:true, error} for 429, and nil for everything else.
func (c *Client) VerifyTOTPViaGo(ctx context.Context, partialToken, code string) any {
	status, parsed, ok := c.signIn(ctx, "/api/auth/login/2fa",
		NewObject("partial_token", partialToken, "code", code), c.cappedTimeout())
	if !ok || parsed == nil {
		return nil
	}
	if status == http.StatusOK && Truthy(prop(parsed, "token")) {
		return parsed
	}
	return rejection(status, parsed, false)
}

// rejection builds the object for a refused sign-in, or nil when the status
// is none of 400, 401 and 429. A null body is no answer at all.
func rejection(status int, parsed any, withStatus bool) any {
	if parsed == nil {
		return nil
	}
	o := NewObject("rejected", true)
	switch status {
	case http.StatusUnauthorized, http.StatusBadRequest:
		if withStatus {
			o.Set("status", float64(status))
		}
	case http.StatusTooManyRequests:
		o.Set("rateLimited", true)
	default:
		return nil
	}
	if body, isObject := parsed.(*Object); isObject && body.Has("error") {
		o.Set("error", body.Get("error"))
	}
	return o
}

// GetGoSSOStatus calls GET /api/auth/sso/status and returns {ldap, oidc,
// any}, all false when the call fails. An answer is kept for 60 seconds,
// also one with another status than 200; a missing answer is not kept.
func (c *Client) GetGoSSOStatus(ctx context.Context) *Object {
	now := c.now()
	c.cacheMu.Lock()
	if c.sso != nil && now.Sub(c.ssoAt) < ssoCacheFor {
		cached := c.sso
		c.cacheMu.Unlock()
		return cached.Clone()
	}
	c.cacheMu.Unlock()

	value := NewObject("ldap", false, "oidc", false, "any", false)
	opts := append(plain(false), Timeout(c.ownTimeout()), ValidateStatus(func(int) bool { return true }))
	resp, e := c.do(ctx, http.MethodGet, "/api/auth/sso/status", opts...)
	if e != nil {
		return value
	}
	if resp.Status == http.StatusOK {
		if parsed, err := Parse(resp.Body); err == nil && parsed != nil {
			value = NewObject(
				"ldap", Truthy(prop(parsed, "ldap_enabled")),
				"oidc", Truthy(prop(parsed, "oidc_enabled")),
				"any", Truthy(prop(parsed, "any_enabled")),
			)
		}
	}
	c.cacheMu.Lock()
	c.sso, c.ssoAt = value, now
	c.cacheMu.Unlock()
	return value.Clone()
}

// TryGoServerAuth calls POST /api/auth/login with {username, password}. It
// returns {role, auth_provider} when the API server signed the user in,
// {requires2fa:true} when it asks for a second factor (that answer names
// neither role nor provider), {account_disabled:true} when the credentials
// are right and the account is disabled, and nil for everything else:
// wrong credentials, the sign-in limit (429) and a missing answer look the
// same.
func (c *Client) TryGoServerAuth(ctx context.Context, username, password string) *Object {
	status, parsed, ok := c.signIn(ctx, "/api/auth/login",
		NewObject("username", username, "password", password), c.ownTimeout())
	if ok && status == http.StatusForbidden && prop(parsed, "code") == "account_disabled" {
		return NewObject("account_disabled", true)
	}
	if !ok || status != http.StatusOK || parsed == nil {
		return nil
	}
	if Truthy(prop(parsed, "token")) && Truthy(prop(parsed, "role")) {
		o := NewObject("role", prop(parsed, "role"))
		if body, isObject := parsed.(*Object); isObject && body.Has("auth_provider") {
			o.Set("auth_provider", body.Get("auth_provider"))
		}
		return o
	}
	if Truthy(prop(parsed, "requires_2fa")) {
		return NewObject("requires2fa", true)
	}
	return nil
}

// TryLDAPVerifyOnGo calls POST /api/auth/ldap/verify with {username,
// password} and reports whether the API server answered 200 with
// "authenticated" set.
func (c *Client) TryLDAPVerifyOnGo(ctx context.Context, username, password string) bool {
	status, parsed, ok := c.signIn(ctx, "/api/auth/ldap/verify",
		NewObject("username", username, "password", password), c.ownTimeout())
	return ok && status == http.StatusOK && parsed != nil && Truthy(prop(parsed, "authenticated"))
}
