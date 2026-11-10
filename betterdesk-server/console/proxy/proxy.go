// Package proxy passes requests the Go console does not handle yet to the
// Node console on its internal port, HTTP and websockets alike. It exists
// only while routes move from Node to Go.
package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/logging"
)

// New returns a handler that forwards every request to upstream.
//
// The Node console must see the same client the Go console sees, so the
// forwarding headers a client sent are dropped and replaced by what trust
// worked out: one address in X-Forwarded-For, the scheme and the host.
// Node trusts these because it listens on loopback only while it runs
// behind Go (TRUST_PROXY=loopback).
func New(upstream *url.URL, trust httpx.TrustProxy) http.Handler {
	transport := &http.Transport{
		MaxIdleConns:        256,
		MaxIdleConnsPerHost: 256,
		IdleConnTimeout:     90 * time.Second,
	}
	return &httputil.ReverseProxy{
		Transport:     transport,
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(upstream)
			pr.Out.URL.Path = pr.In.URL.Path
			pr.Out.URL.RawPath = pr.In.URL.RawPath
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			host := trust.Host(pr.In)
			pr.Out.Host = host
			pr.Out.Header.Del("Forwarded")
			pr.Out.Header.Del("X-Real-IP")
			pr.Out.Header.Set("X-Forwarded-For", trust.ClientIP(pr.In))
			pr.Out.Header.Set("X-Forwarded-Proto", trust.Scheme(pr.In))
			pr.Out.Header.Set("X-Forwarded-Host", host)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logging.Logf("console", "node upstream failed for %s %s: %v", r.Method, r.URL.Path, err)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "Console backend unavailable"})
		},
	}
}
