package phase45

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
)

// profileFields are the profile columns of users, in the order the
// profile is written.
var profileFields = []string{"first_name", "last_name", "email", "phone", "role_display", "avatar_url"}

// userRow is what the profile routes read of an account. The cells hold
// what the database holds, which on SQLite need not be text.
type userRow struct {
	ID          store.Int64String
	Username    jsbind.Cell
	Role        jsbind.Cell
	FirstName   jsbind.Cell
	LastName    jsbind.Cell
	Email       jsbind.Cell
	Phone       jsbind.Cell
	RoleDisplay jsbind.Cell
	AvatarURL   jsbind.Cell
}

// getUserByID reads an account, nil when there is none.
func getUserByID(ctx context.Context, db *store.DB, id int64) (*userRow, error) {
	var u userRow
	err := store.SelectRow(ctx, db,
		"SELECT id, username, role, first_name, last_name, email, phone, role_display, avatar_url FROM users WHERE id = ?", id).
		Scan(&u.ID, &u.Username, &u.Role, &u.FirstName, &u.LastName, &u.Email, &u.Phone, &u.RoleDisplay, &u.AvatarURL)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read user %d: %w", id, err)
	}
	return &u, nil
}

// updateUserProfile writes the six profile columns of an account.
func updateUserProfile(ctx context.Context, db *store.DB, id int64, values []string) error {
	_, err := db.Exec(ctx,
		"UPDATE users SET first_name = ?, last_name = ?, email = ?, phone = ?, role_display = ?, avatar_url = ? WHERE id = ?",
		values[0], values[1], values[2], values[3], values[4], values[5], id)
	if err != nil {
		return fmt.Errorf("update profile of user %d: %w", id, err)
	}
	return nil
}
