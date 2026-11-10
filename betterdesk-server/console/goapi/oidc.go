package goapi

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/logging"
)

// Single sign-on (OIDC) settings and sign-in.

// GetOIDCConfig calls GET /api/auth/oidc/config. W or F1; the client secret
// is masked.
func (c *Client) GetOIDCConfig(ctx context.Context) *Object {
	return c.wrapped(ctx, f1, http.MethodGet, "/auth/oidc/config")
}

// SaveOIDCConfig calls PUT /api/auth/oidc/config with config. W or F1.
func (c *Client) SaveOIDCConfig(ctx context.Context, config any) *Object {
	return c.wrapped(ctx, f1, http.MethodPut, "/auth/oidc/config", Body(config))
}

// TestOIDCDiscovery calls POST /api/auth/oidc/test with config. W or F1.
func (c *Client) TestOIDCDiscovery(ctx context.Context, config any) *Object {
	return c.wrapped(ctx, f1, http.MethodPost, "/auth/oidc/test", Body(config))
}

// GetOIDCStatus calls GET /api/auth/oidc/status. W or F1.
func (c *Client) GetOIDCStatus(ctx context.Context) *Object {
	return c.wrapped(ctx, f1, http.MethodGet, "/auth/oidc/status")
}

// ExchangeOIDCCode calls POST /api/auth/oidc/exchange with {code}. W or F1.
func (c *Client) ExchangeOIDCCode(ctx context.Context, code any) *Object {
	return c.wrapped(ctx, f1, http.MethodPost, "/auth/oidc/exchange", Body(NewObject("code", code)))
}

// StartOIDCAuthorize calls GET /api/auth/oidc/authorize with return_url and
// does not follow the redirect. It returns {success:true, data:{auth_url}}
// with the identity provider's address from the Location header, or
// {success:false, error}: the API server's error text, else the message of
// the failed call, else "OIDC authorize did not return an IdP redirect
// URL".
func (c *Client) StartOIDCAuthorize(ctx context.Context, returnURL string) *Object {
	path := "/auth/oidc/authorize"
	if returnURL != "" {
		path += "?" + queryOf("return_url", returnURL)
	}
	resp, e := c.do(ctx, http.MethodGet, path, NoRedirects(),
		ValidateStatus(func(status int) bool { return status >= 200 && status < 400 }))
	if resp != nil {
		if location := resp.Header.Get("Location"); isAbsoluteHTTPURL(location) {
			return NewObject("success", true, "data", NewObject("auth_url", location))
		}
	}
	if e == nil {
		return NewObject("success", false, "error", "OIDC authorize did not return an IdP redirect URL")
	}
	if text, ok := prop(e.Data(), "error").(string); ok {
		return NewObject("success", false, "error", text)
	}
	return NewObject("success", false, "error", e.Message)
}

// isAbsoluteHTTPURL reports whether value is an http or https address.
func isAbsoluteHTTPURL(value string) bool {
	value = strings.TrimFunc(value, func(r rune) bool { return r <= 0x20 })
	if value == "" {
		return false
	}
	u, err := url.Parse(value)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return (scheme == "http" || scheme == "https") && (u.Host != "" || u.Opaque != "")
}

// CallbackAnswer is what the API server answered to the identity
// provider's callback: what the console passes on to the browser.
type CallbackAnswer struct {
	Status       int
	ContentType  string
	Location     string
	CacheControl string
	Body         []byte
}

// ProxyOIDCCallback calls GET /api/auth/oidc/callback with the browser's
// query and returns the answer for the browser: status, Content-Type,
// Location, Cache-Control and body. The redirect is not followed, every
// status is an answer, and the call may take 30 seconds or the client's
// timeout if that is longer. When the API server gives no answer the
// result is status 502 with the text "OIDC callback proxy failed".
//
// Of each query parameter the first value is passed on, under its name
// without a trailing "[]". Parameters with nested names (a[b]) are left
// out.
func (c *Client) ProxyOIDCCallback(ctx context.Context, query url.Values) *CallbackAnswer {
	path := "/auth/oidc/callback"
	var pairs []string
	for _, key := range sortedValueKeys(query) {
		name := strings.TrimSuffix(key, "[]")
		if strings.ContainsAny(name, "[]") || len(query[key]) == 0 {
			continue
		}
		pairs = append(pairs, name, query[key][0])
	}
	if q := queryOf(pairs...); q != "" {
		path += "?" + q
	}
	timeout := c.timeout
	if timeout < 30*time.Second {
		timeout = 30 * time.Second
	}
	resp, e := c.do(ctx, http.MethodGet, path,
		NoRedirects(),
		ValidateStatus(func(int) bool { return true }),
		Timeout(timeout),
		Header("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8"),
		withoutHeader("Content-Type"),
	)
	if e != nil {
		logging.Logf("goapi", "OIDC callback proxy failed: %s", e.Message)
		return &CallbackAnswer{
			Status:      http.StatusBadGateway,
			ContentType: "text/plain; charset=utf-8",
			Body:        []byte("OIDC callback proxy failed"),
		}
	}
	return &CallbackAnswer{
		Status:       resp.Status,
		ContentType:  resp.Header.Get("Content-Type"),
		Location:     resp.Header.Get("Location"),
		CacheControl: resp.Header.Get("Cache-Control"),
		Body:         resp.Body,
	}
}

func sortedValueKeys(v url.Values) []string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
