package cluster

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// Valkey is the server's connection to Valkey or Redis. Both speak the
// same protocol; nothing here uses a command only one of them has.
type Valkey struct {
	Client redis.UniversalClient
	prefix string

	healthy atomic.Bool
	stop    context.CancelFunc
	wg      sync.WaitGroup
}

// healthInterval is how often the connection is checked; a failed check
// marks the pod not ready until one succeeds.
const healthInterval = 2 * time.Second

// redisOptions parses a Valkey or Redis URL. valkey:// and valkeys:// are
// the same as redis:// and rediss://.
func redisOptions(raw string) (*redis.Options, error) {
	u := raw
	switch {
	case strings.HasPrefix(u, "valkeys://"):
		u = "rediss://" + strings.TrimPrefix(u, "valkeys://")
	case strings.HasPrefix(u, "valkey://"):
		u = "redis://" + strings.TrimPrefix(u, "valkey://")
	}
	opts, err := redis.ParseURL(u)
	if err != nil {
		// The parser's message can quote the URL; name it without the password.
		return nil, fmt.Errorf("VALKEY_URL %s is not a valkey://, valkeys://, redis:// or rediss:// URL", RedactURL(raw))
	}
	return opts, nil
}

// Dial connects to the Valkey of c and checks it answers. The connection
// is then checked every healthInterval; Healthy reports the last result.
func Dial(ctx context.Context, c Config) (*Valkey, error) {
	opts, err := redisOptions(c.ValkeyURL)
	if err != nil {
		return nil, err
	}
	// Fail fast instead of hanging: a session that needs Valkey gets an
	// error within a few seconds when it is gone.
	opts.DialTimeout = 3 * time.Second
	opts.ReadTimeout = 2 * time.Second
	opts.WriteTimeout = 2 * time.Second
	opts.PoolTimeout = 3 * time.Second
	opts.ClientName = "neodesk:" + c.Name
	client := redis.NewClient(opts)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("Valkey at %s does not answer: %w", RedactURL(c.ValkeyURL), scrub(err, c.ValkeyURL))
	}
	v := &Valkey{Client: client, prefix: c.KeyPrefix}
	v.healthy.Store(true)
	runCtx, stop := context.WithCancel(context.Background())
	v.stop = stop
	v.wg.Add(1)
	go v.watch(runCtx, c.ValkeyURL)
	return v, nil
}

// NewForTest wraps a client that a test made, healthy from the start.
func NewForTest(client redis.UniversalClient, prefix string) *Valkey {
	v := &Valkey{Client: client, prefix: prefix, stop: func() {}}
	v.healthy.Store(true)
	return v
}

func (v *Valkey) watch(ctx context.Context, rawURL string) {
	defer v.wg.Done()
	t := time.NewTicker(healthInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		pingCtx, cancel := context.WithTimeout(ctx, healthInterval)
		err := v.Client.Ping(pingCtx).Err()
		cancel()
		was := v.healthy.Swap(err == nil)
		switch {
		case err != nil && was:
			log.Printf("[cluster] Valkey at %s stopped answering, this pod is not ready: %v", RedactURL(rawURL), scrub(err, rawURL))
		case err == nil && !was:
			log.Printf("[cluster] Valkey at %s answers again, this pod is ready", RedactURL(rawURL))
		}
	}
}

// Healthy reports whether the last check of the connection succeeded.
func (v *Valkey) Healthy() bool { return v != nil && v.healthy.Load() }

// Key is the prefixed key of parts joined by ":".
func (v *Valkey) Key(parts ...string) string { return v.prefix + strings.Join(parts, ":") }

// Prefix is the prefix every key starts with.
func (v *Valkey) Prefix() string { return v.prefix }

// Close stops the checks and closes the connection.
func (v *Valkey) Close() error {
	if v == nil {
		return nil
	}
	v.stop()
	v.wg.Wait()
	return v.Client.Close()
}

// scrub keeps an error from carrying the URL's password.
func scrub(err error, rawURL string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if opts, perr := redisOptions(rawURL); perr == nil && opts.Password != "" {
		msg = strings.ReplaceAll(msg, opts.Password, "xxxxx")
	}
	return errors.New(msg)
}
