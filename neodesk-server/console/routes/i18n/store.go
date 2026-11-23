package i18n

import (
	"context"
	"fmt"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// updateUserLanguage stores an account's language choice.
func updateUserLanguage(ctx context.Context, db *store.DB, userID int64, code string) error {
	if _, err := db.Exec(ctx, "UPDATE users SET preferred_language = ? WHERE id = ?", code, userID); err != nil {
		return fmt.Errorf("store the language of user %d: %w", userID, err)
	}
	return nil
}
