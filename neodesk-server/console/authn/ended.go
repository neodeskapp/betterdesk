package authn

import (
	"context"
	"fmt"

	"github.com/neodeskapp/neodesk/neodesk-server/console/lifecycle"
)

// ended reports whether the account may not sign in: it is disabled, or
// its end date has passed (the background job of the users routes
// disables it then; this check keeps a missed run from letting it in). A
// failed read of the end date counts as ended.
func (s *Service) ended(ctx context.Context, user *Account) bool {
	if user.Disabled.True() {
		return true
	}
	expired, err := lifecycle.Expired(ctx, s.db, user.ID.V, s.now())
	if err != nil {
		s.log.Info(fmt.Sprintf("[AUTH] End date of '%s' unreadable: %v", logName(user.Username), err))
		return true
	}
	return expired
}
