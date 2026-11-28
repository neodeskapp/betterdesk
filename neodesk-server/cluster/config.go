// Package cluster lets several NeoDesk server processes share one
// installation: the Valkey (or Redis) connection for shared short-lived
// state, the record of which instances run, leases for work that must run
// once per installation, and the pod's identity. Without VALKEY_URL (or
// REDIS_URL) the server runs alone and nothing here changes its behaviour.
package cluster

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Defaults of the cluster settings.
const (
	DefaultKeyPrefix     = "neodesk:"
	DefaultClusterPort   = 21120
	DefaultShutdownGrace = 30 * time.Second
)

// Config is the cluster part of the server's settings, read from the
// environment.
type Config struct {
	// ValkeyURL is VALKEY_URL, else REDIS_URL: redis://, rediss://
	// (TLS), valkey:// or valkeys://, with an optional user and
	// password. Empty means the server runs alone.
	ValkeyURL string
	// KeyPrefix starts every key and channel (NEODESK_KEY_PREFIX), so
	// several installations can share one Valkey.
	KeyPrefix string
	// Name identifies this instance: POD_NAME, else the host name.
	Name string
	// PodIP is the address other pods reach this one at (POD_IP); empty
	// lets the server pick its first non-loopback address.
	PodIP string
	// Port is the cluster port other pods call (NEODESK_CLUSTER_PORT).
	Port int
	// Secret authenticates calls between pods (NEODESK_CLUSTER_SECRET).
	Secret string
	// ShutdownGrace is how long running relay sessions may go on after
	// SIGTERM (NEODESK_SHUTDOWN_GRACE, a Go duration or seconds).
	ShutdownGrace time.Duration
}

// FromEnv reads the cluster settings; getenv is os.Getenv in the server.
func FromEnv(getenv func(string) string) (Config, error) {
	c := Config{
		ValkeyURL:     strings.TrimSpace(getenv("VALKEY_URL")),
		KeyPrefix:     getenv("NEODESK_KEY_PREFIX"),
		Name:          strings.TrimSpace(getenv("POD_NAME")),
		PodIP:         strings.TrimSpace(getenv("POD_IP")),
		Port:          DefaultClusterPort,
		Secret:        getenv("NEODESK_CLUSTER_SECRET"),
		ShutdownGrace: DefaultShutdownGrace,
	}
	if c.ValkeyURL == "" {
		c.ValkeyURL = strings.TrimSpace(getenv("REDIS_URL"))
	}
	if c.KeyPrefix == "" {
		c.KeyPrefix = DefaultKeyPrefix
	}
	if c.Name == "" {
		if host, err := os.Hostname(); err == nil {
			c.Name = host
		}
	}
	if v := strings.TrimSpace(getenv("NEODESK_CLUSTER_PORT")); v != "" {
		port, err := strconv.Atoi(v)
		if err != nil || port <= 0 || port > 65535 {
			return c, fmt.Errorf("NEODESK_CLUSTER_PORT=%q is not a port", v)
		}
		c.Port = port
	}
	if v := strings.TrimSpace(getenv("NEODESK_SHUTDOWN_GRACE")); v != "" {
		d, err := parseDuration(v)
		if err != nil || d < 0 {
			return c, fmt.Errorf("NEODESK_SHUTDOWN_GRACE=%q is not a duration (e.g. 30s)", v)
		}
		c.ShutdownGrace = d
	}
	if c.ValkeyURL != "" {
		if _, err := redisOptions(c.ValkeyURL); err != nil {
			return c, err
		}
	}
	return c, nil
}

// Enabled reports whether this server shares its installation through
// Valkey.
func (c Config) Enabled() bool { return c.ValkeyURL != "" }

func parseDuration(v string) (time.Duration, error) {
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second, nil
	}
	return time.ParseDuration(v)
}

// RedactURL is u without its password, for logs and error messages.
func RedactURL(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return "(unparsable URL)"
	}
	return parsed.Redacted()
}
