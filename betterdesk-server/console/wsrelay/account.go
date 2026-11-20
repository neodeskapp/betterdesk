package wsrelay

import (
	"context"

	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/ws"
)

// account is the signed-in account of a bridge.
type account struct {
	u      *ws.Upgrade
	userID int64
	user   *session.User
	// limited says the account has a device scope: the bridge reads its
	// frames, and every punch or relay request must name a device of the
	// scope.
	limited bool
}

// newAccount is the account of a signed-in upgrade. A scope that cannot
// be looked up counts as a limit, so the account's requests are checked.
func (s *Service) newAccount(u *ws.Upgrade, sn ws.Session) *account {
	a := &account{u: u, userID: sn.UserID(), user: sn.User()}
	if s.opts.CanAccess != nil {
		unlimited, err := s.opts.CanAccess(u.R.Context(), a.user, nil)
		a.limited = err != nil || !unlimited
	}
	return a
}

// reads reports whether the bridge reads the account's frames.
func (a *account) reads() bool { return a != nil && a.limited }

// inScope reports whether the account's device scope holds the device of
// id. A device the console does not know, and a lookup or scope check
// that fails, are outside.
func (s *Service) inScope(ctx context.Context, a *account, id string) bool {
	if s.opts.Devices == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, validateTimeout)
	defer cancel()
	device, err := s.opts.Devices(ctx, id)
	if err != nil || device == nil {
		return false
	}
	visible, err := s.opts.CanAccess(ctx, a.user, device)
	return err == nil && visible
}
