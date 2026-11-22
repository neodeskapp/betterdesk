package commercialization

import (
	"context"
	"fmt"

	"github.com/unitronix/betterdesk-server/console/store"
)

// accountNamed reports whether the console has an account of exactly
// this name.
func accountNamed(ctx context.Context, db *store.DB, name string) (bool, error) {
	var n int64
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE username = ?`, name).Scan(&n); err != nil {
		return false, fmt.Errorf("look up an account by name: %w", err)
	}
	return n > 0, nil
}
