package users

import (
	"context"
	"fmt"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The last Super admin: the console keeps at least one account with a
// super admin role (super_admin, or the old admin) that can sign in. No
// route and no background job deletes, disables, demotes or ends the last
// one; an account that is disabled, or whose end date has passed, does
// not count.

// otherActiveSuperAdmins is the number of accounts other than the one with
// the id that have a super admin role, are not disabled and have no end
// date that has passed at now.
func otherActiveSuperAdmins(ctx context.Context, db *store.DB, id int64, now time.Time) (int64, error) {
	var n int64
	err := db.QueryRow(ctx, `SELECT COUNT(*) FROM users u LEFT JOIN account_lifecycle l ON l.user_id = u.id
		WHERE u.role IN ('admin', 'super_admin') AND u.id <> ?
		AND `+db.Pick(`COALESCE(u.disabled, 0) = 0`, `COALESCE(u.disabled, FALSE) = FALSE`)+`
		AND (l.expires_ms IS NULL OR l.expires_ms > ?)`, id, now.UnixMilli()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count active super admins: %w", err)
	}
	return n, nil
}

// activeSuperAdminIDs are the accounts with a super admin role that are
// not disabled and have no end date that has passed at now.
func activeSuperAdminIDs(ctx context.Context, db *store.DB, now time.Time) (map[int64]bool, error) {
	rows, err := db.Query(ctx, `SELECT u.id FROM users u LEFT JOIN account_lifecycle l ON l.user_id = u.id
		WHERE u.role IN ('admin', 'super_admin')
		AND `+db.Pick(`COALESCE(u.disabled, 0) = 0`, `COALESCE(u.disabled, FALSE) = FALSE`)+`
		AND (l.expires_ms IS NULL OR l.expires_ms > ?)`, now.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("list active super admins: %w", err)
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// isLastSuperAdmin reports whether the account has a super admin role and
// no other account with one could still sign in.
func isLastSuperAdmin(ctx context.Context, db *store.DB, id int64, role string, now time.Time) (bool, error) {
	if !access.IsSuperAdminRole(role) {
		return false, nil
	}
	n, err := otherActiveSuperAdmins(ctx, db, id, now)
	return err == nil && n == 0, err
}
