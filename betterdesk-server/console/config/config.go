// Package config reads the console's settings from the environment.
package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// Config is the console's configuration.
type Config struct {
	// Host and Port are where the console listens (HOST, CONSOLE_PORT).
	// Port 0 means the Go console is off.
	Host string
	Port int

	// TrustProxy says which reverse proxies may speak for the client
	// (TRUST_PROXY).
	TrustProxy httpx.TrustProxy

	// NodeUpstream is the Node console's internal address while routes
	// move from Node to Go (CONSOLE_NODE_UPSTREAM). nil when Go serves
	// everything.
	NodeUpstream *url.URL
}

// Load reads the configuration through getenv (os.Getenv in production).
func Load(getenv func(string) string) (*Config, error) {
	cfg := &Config{Host: strings.TrimSpace(getenv("HOST"))}
	if cfg.Host == "" {
		cfg.Host = "0.0.0.0"
	}

	if raw := strings.TrimSpace(getenv("CONSOLE_PORT")); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 0 || port > 65535 {
			return nil, fmt.Errorf("CONSOLE_PORT %q is not a port", raw)
		}
		cfg.Port = port
	}

	trust, err := httpx.ParseTrustProxy(getenv("TRUST_PROXY"))
	if err != nil {
		return nil, fmt.Errorf("TRUST_PROXY: %w", err)
	}
	cfg.TrustProxy = trust

	if raw := strings.TrimSpace(getenv("CONSOLE_NODE_UPSTREAM")); raw != "" {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("CONSOLE_NODE_UPSTREAM %q is not an http(s) URL", raw)
		}
		cfg.NodeUpstream = u
	}
	return cfg, nil
}

// Addr is the listen address.
func (c *Config) Addr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}
