package cluster

import (
	"context"
	"hash/fnv"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Leases decides which instance runs the work that must run once per
// installation (cleanups, mails, scheduled backups, the fleet scheduler).
// A job asks Held before each run and skips the run when another instance
// holds its lease.
type Leases interface {
	// Held reports whether this instance holds the lease name now. The
	// first call for a name starts trying to take it.
	Held(name string) bool
	// Close gives up every lease.
	Close()
}

// Solo is the lease set of a server that runs alone: it holds every lease.
type Solo struct{}

func (Solo) Held(string) bool { return true }
func (Solo) Close()           {}

// leaseLockClass is the first key of the PostgreSQL advisory locks that
// back leases; the second is a hash of the lease name. The console's
// schema lock uses class 1651794787, migrations migrateLockClass.
const (
	leaseLockClass   = 1851876709 // "nede" as a number, unique to leases
	migrateLockClass = 1851876710
)

// leaseRetry is how often an instance tries to take the leases it doesn't
// hold, and checks the connection that holds the ones it has.
const leaseRetry = 5 * time.Second

// PGLeases backs leases with PostgreSQL session advisory locks held on one
// dedicated connection. When this instance dies or loses the connection,
// PostgreSQL frees its locks and another instance takes them within
// leaseRetry. No TTL to tune and no window where two instances both think
// they hold a lease while the database is reachable.
type PGLeases struct {
	pool *pgxpool.Pool
	self string

	mu     sync.Mutex
	conn   *pgxpool.Conn
	wanted map[string]bool // name -> held
	stop   context.CancelFunc
	wg     sync.WaitGroup
}

// NewPGLeases starts the lease loop on pool; self names this instance in
// logs.
func NewPGLeases(pool *pgxpool.Pool, self string) *PGLeases {
	ctx, stop := context.WithCancel(context.Background())
	l := &PGLeases{pool: pool, self: self, wanted: map[string]bool{}, stop: stop}
	l.wg.Add(1)
	go l.loop(ctx)
	return l
}

// Held reports whether this instance holds name. A name asked for the
// first time is taken right away when it is free.
func (l *PGLeases) Held(name string) bool {
	l.mu.Lock()
	held, known := l.wanted[name]
	if !known {
		l.wanted[name] = false
	}
	l.mu.Unlock()
	if known {
		return held
	}
	l.tryAll(context.Background())
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.wanted[name]
}

func (l *PGLeases) loop(ctx context.Context) {
	defer l.wg.Done()
	t := time.NewTicker(leaseRetry)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		l.tryAll(ctx)
	}
}

// tryAll checks the lock connection and tries every lease not held yet.
func (l *PGLeases) tryAll(ctx context.Context) {
	l.mu.Lock()
	defer l.mu.Unlock()
	opCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if l.conn != nil {
		if err := l.conn.Ping(opCtx); err != nil {
			l.dropLocked("connection lost: " + err.Error())
		}
	}
	if l.conn == nil {
		conn, err := l.pool.Acquire(opCtx)
		if err != nil {
			return
		}
		l.conn = conn
	}
	for name, held := range l.wanted {
		if held {
			continue
		}
		var got bool
		if err := l.conn.QueryRow(opCtx, `SELECT pg_try_advisory_lock($1, $2)`, leaseLockClass, leaseKey(name)).Scan(&got); err != nil {
			l.dropLocked("lock query failed: " + err.Error())
			return
		}
		if got {
			l.wanted[name] = true
			log.Printf("[cluster] %s now runs %q for the installation", l.self, name)
		}
	}
}

// dropLocked forgets every held lease and the connection; PostgreSQL
// frees the locks when the connection goes.
func (l *PGLeases) dropLocked(why string) {
	for name, held := range l.wanted {
		if held {
			log.Printf("[cluster] %s gives up %q: %s", l.self, name, why)
			l.wanted[name] = false
		}
	}
	if l.conn != nil {
		// Hijack and close so the pool doesn't hand out a connection that
		// may still hold locks.
		_ = l.conn.Hijack().Close(context.Background())
		l.conn = nil
	}
}

// Close releases every lease.
func (l *PGLeases) Close() {
	l.stop()
	l.wg.Wait()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dropLocked("shutting down")
}

func leaseKey(name string) int32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return int32(h.Sum32())
}

// WithMigrationLock runs fn while holding the installation's migration
// lock, so instances that start together migrate one after the other.
// pool nil (SQLite) runs fn at once.
func WithMigrationLock(ctx context.Context, pool *pgxpool.Pool, fn func() error) error {
	if pool == nil {
		return fn()
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1, 0)`, migrateLockClass); err != nil {
		return err
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1, 0)`, migrateLockClass)
	}()
	return fn()
}
