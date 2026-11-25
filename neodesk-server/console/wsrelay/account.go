package wsrelay

import (
	"context"
	"sync"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws"
)

// account is the signed-in account of a bridge.
type account struct {
	u      *ws.Upgrade
	userID int64
	// session is the handle of the console session of the upgrade, "" for
	// none: the signal server's relay ticket names it (A239)
	session string
	// limited says the account had a device scope at the upgrade: the
	// bridge reads its frames, and every punch or relay request must name
	// a device of the scope.
	limited bool

	mu sync.Mutex
	// user is the account as the last read of the session found it, for
	// the device scope.
	user *session.User
	// targets are the devices of the punch and relay requests the bridge
	// let through.
	targets map[string]struct{}
}

// newAccount is the account of a signed-in upgrade. A scope that cannot
// be looked up counts as a limit, so the account's requests are checked.
func (s *Service) newAccount(u *ws.Upgrade, sn ws.Session) *account {
	a := &account{u: u, userID: sn.UserID(), user: sn.User(), targets: map[string]struct{}{}}
	if id := u.SessionID(); id != "" {
		a.session = session.Handle(id)
	}
	a.limited = s.hasLimit(u.R.Context(), a.user)
	return a
}

// hasLimit reports whether the account has a device scope; a scope that
// cannot be looked up counts as one.
func (s *Service) hasLimit(ctx context.Context, user *session.User) bool {
	if s.opts.CanAccess == nil {
		return false
	}
	unlimited, err := s.opts.CanAccess(ctx, user, nil)
	return err != nil || !unlimited
}

// reads reports whether the bridge reads the account's frames.
func (a *account) reads() bool { return a != nil && a.limited }

// currentUser is the account as the last read of the session found it.
func (a *account) currentUser() *session.User {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.user
}

// addTarget notes a device the bridge sent a request for.
func (a *account) addTarget(id string) {
	a.mu.Lock()
	a.targets[id] = struct{}{}
	a.mu.Unlock()
}

// stillAllowed reads the account's session again: still signed in as the
// same account, its role still with device.connect, and its device scope
// still holding every device the bridge sent a request for. An account
// that had no scope at the upgrade, whose frames were not read, must
// still have none. A failed lookup counts as no, as at the upgrade. The
// account of that read replaces the bridge's for later scope checks.
func (b *bridge) stillAllowed(parent context.Context) bool {
	a := b.account
	ctx, cancel := context.WithTimeout(parent, validateTimeout)
	defer cancel()
	check := *a.u
	check.R = a.u.R.WithContext(ctx)
	sn := check.Session()
	if !sn.SignedIn() || sn.UserID() != a.userID {
		return false
	}
	allowed, err := check.RoleHasPermission(sn.UserRole(), "device.connect")
	if err != nil || !allowed {
		return false
	}
	user := sn.User()
	a.mu.Lock()
	a.user = user
	targets := make([]string, 0, len(a.targets))
	for id := range a.targets {
		targets = append(targets, id)
	}
	a.mu.Unlock()
	if !b.s.hasLimit(ctx, user) {
		return true
	}
	if !a.limited {
		return false
	}
	for _, id := range targets {
		if !b.s.inScope(ctx, a, id) {
			return false
		}
	}
	return true
}

// watchSession looks at a signed-in bridge's session every SessionCheck
// and ends the bridge (cause session-ended) once stillAllowed says no.
func (b *bridge) watchSession() {
	defer b.wg.Done()
	tick := time.NewTicker(b.s.opts.SessionCheck)
	defer tick.Stop()
	for {
		select {
		case <-b.done:
			return
		case <-tick.C:
			if !b.stillAllowed(b.conn.Context()) {
				b.cleanup("session-ended", "")
				return
			}
		}
	}
}

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
	visible, err := s.opts.CanAccess(ctx, a.currentUser(), device)
	return err == nil && visible
}
