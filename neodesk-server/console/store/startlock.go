package store

import (
	"context"
	"database/sql/driver"
	"fmt"
)

// installLockClass is the first key of the installation's migration lock,
// the PostgreSQL advisory lock (installLockClass, 0) that the server takes
// around its migrations (cluster.WithMigrationLock, migrateLockClass
// there). The two must stay equal.
const installLockClass = 1851876710

// LockInstallation takes the installation's migration lock on PostgreSQL
// and returns the function that releases it, so instances that start
// together run the console's one-time start steps (tables beside the
// schema, imports, the first account) one after the other. The lock is
// held on its own connection; statements of the steps run on others. On
// SQLite, which has one instance, it takes nothing.
func (d *DB) LockInstallation(ctx context.Context) (unlock func(), err error) {
	if d.dialect != Postgres {
		return func() {}, nil
	}
	conn, err := d.write.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("take the installation lock: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1, 0)`, installLockClass); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("take the installation lock: %w", err)
	}
	return func() {
		if _, err := conn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1, 0)`, installLockClass); err != nil {
			// A connection whose lock cannot be released is closed for
			// good, which releases it too
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = conn.Close()
	}, nil
}
