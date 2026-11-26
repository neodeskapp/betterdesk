package relay

import (
	"context"
	"sync"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
)

// AuthorizationRegistry permits exactly one relay pair for a UUID that signal
// has authorized. Relay traffic cannot carry an additional NeoDesk-specific
// credential without changing RustDesk's wire framing, so the signal server
// records the already-negotiated UUID here before either peer reaches relay.
type AuthorizationRegistry struct {
	mu      sync.Mutex
	tickets map[string]*relayAuthorization
	used    map[string]time.Time
	now     func() time.Time
	waiters map[string]chan struct{} // one notification channel per UUID
	// live holds the sessions whose ticket both sides claimed, until the
	// relay ends them (End) or liveMaxAge passes
	live map[string]livePair
}

type relayAuthorization struct {
	initiatorID string
	targetID    string
	account     int64
	session     string
	expiresAt   time.Time
	claims      uint8
}

// LivePair is a relay session that runs: who started it, to which device,
// the console account signal named for it (0 for none), the console
// session of that account whose viewer opened it (a handle, "" for none)
// and when its second side arrived.
type LivePair struct {
	Initiator      string
	Target         string
	Account        int64
	ConsoleSession string
	Since          time.Time
}

type livePair struct {
	LivePair
	since time.Time
}

// liveMaxAge is how long a live entry is kept when the relay never
// reported the session's end.
const liveMaxAge = 7 * 24 * time.Hour

// NewAuthorizationRegistry creates an in-memory authorization store. Tickets
// are deliberately process-local: signal and relay run together in NeoDesk's
// all-in-one server mode, and no ticket is persisted across restarts.
func NewAuthorizationRegistry() *AuthorizationRegistry {
	return &AuthorizationRegistry{
		tickets: make(map[string]*relayAuthorization),
		used:    make(map[string]time.Time),
		now:     time.Now,
		waiters: make(map[string]chan struct{}),
		live:    make(map[string]livePair),
	}
}

// defaultAuthorizationRegistry is shared by the signal and relay packages in
// the all-in-one process. Keeping it package-owned avoids adding a token field
// to RustDesk's RequestRelay framing.
var defaultAuthorizationRegistry = NewAuthorizationRegistry()

// AuthorizeRelayPair records an authorized initiator/target pair for uuid.
// An active ticket may be retried only by the same pair. A consumed UUID cannot
// be re-authorized until its short expiry passes, preventing relay replay.
func AuthorizeRelayPair(uuid, initiatorID, targetID string) bool {
	return defaultAuthorizationRegistry.Authorize(uuid, initiatorID, targetID)
}

// AuthorizeRelayPairFor is AuthorizeRelayPair for a session that a console
// account starts (0 for none): the live session names the account.
func AuthorizeRelayPairFor(uuid, initiatorID, targetID string, account int64) bool {
	return defaultAuthorizationRegistry.AuthorizeFor(uuid, initiatorID, targetID, account)
}

// AuthorizeRelayPairForSession is AuthorizeRelayPairFor that also names the
// console session (a handle) whose viewer opened the session: an account
// may be signed in on several browsers (A239), and saved sign-in binds its
// one use to the browser that opened the relay session.
func AuthorizeRelayPairForSession(uuid, initiatorID, targetID string, account int64, session string) bool {
	return defaultAuthorizationRegistry.AuthorizeForSession(uuid, initiatorID, targetID, account, session)
}

// RelayPairAdmitted reports whether the signal server already issued the
// ticket of uuid for this pair, unclaimed or live.
func RelayPairAdmitted(uuid, initiatorID, targetID string) bool {
	return defaultAuthorizationRegistry.Admitted(uuid, initiatorID, targetID)
}

// ClaimRelayPair reserves one of the two connections for a signal-authorized
// UUID on the shared all-in-one registry (same store Claim uses in hbbr).
func ClaimRelayPair(uuid string) bool {
	return defaultAuthorizationRegistry.Claim(uuid)
}

// RevokeRelayPairsForPeer removes unpaired tickets involving a banned peer.
func RevokeRelayPairsForPeer(peerID string) {
	defaultAuthorizationRegistry.RevokeForPeer(peerID)
}

func (r *AuthorizationRegistry) Authorize(uuid, initiatorID, targetID string) bool {
	return r.AuthorizeFor(uuid, initiatorID, targetID, 0)
}

// AuthorizeFor is Authorize with the console account behind the session.
// A retry of an active ticket must name the same pair and account.
func (r *AuthorizationRegistry) AuthorizeFor(uuid, initiatorID, targetID string, account int64) bool {
	return r.AuthorizeForSession(uuid, initiatorID, targetID, account, "")
}

// AuthorizeForSession is AuthorizeFor with the console session behind the
// session; a retry must name the same one too.
func (r *AuthorizationRegistry) AuthorizeForSession(uuid, initiatorID, targetID string, account int64, session string) bool {
	if uuid == "" || initiatorID == "" || targetID == "" {
		return false
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	r.pruneLocked(now)
	if _, used := r.used[uuid]; used {
		return false
	}
	if ticket, exists := r.tickets[uuid]; exists {
		return ticket.initiatorID == initiatorID && ticket.targetID == targetID && ticket.account == account && ticket.session == session
	}
	r.tickets[uuid] = &relayAuthorization{
		initiatorID: initiatorID,
		targetID:    targetID,
		account:     account,
		session:     session,
		expiresAt:   now.Add(config.RelayPairTimeout),
	}
	r.signalWaitersLocked(uuid)
	return true
}

// WaitForAuthorization waits until signal authorizes uuid or ctx expires.
// The check and notification channel are coordinated under the registry lock,
// so authorization cannot race with waiter registration and get missed. Each
// UUID has its own channel, avoiding unrelated authorization wakeups.
func (r *AuthorizationRegistry) WaitForAuthorization(ctx context.Context, uuid string) bool {
	if uuid == "" {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}

	for {
		r.mu.Lock()
		now := r.now()
		r.pruneLocked(now)
		if _, used := r.used[uuid]; used {
			r.mu.Unlock()
			return false
		}
		if _, authorized := r.tickets[uuid]; authorized {
			r.mu.Unlock()
			return true
		}
		if r.waiters == nil {
			r.waiters = make(map[string]chan struct{})
		}
		waiter := r.waiters[uuid]
		if waiter == nil {
			waiter = make(chan struct{})
			r.waiters[uuid] = waiter
		}
		r.mu.Unlock()

		select {
		case <-waiter:
		case <-ctx.Done():
			r.removeWaiter(uuid, waiter)
			return false
		}
	}
}

// Admitted reports whether uuid has a ticket for the pair, waiting for its
// claims or live.
func (r *AuthorizationRegistry) Admitted(uuid, initiatorID, targetID string) bool {
	if uuid == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(r.now())
	if ticket, ok := r.tickets[uuid]; ok {
		return ticket.initiatorID == initiatorID && ticket.targetID == targetID
	}
	pair, ok := r.live[uuid]
	return ok && pair.Initiator == initiatorID && pair.Target == targetID
}

// Claim reserves one of the two connections needed for an authorized relay
// pair. The second successful claim consumes the ticket permanently.
func (r *AuthorizationRegistry) Claim(uuid string) bool {
	if uuid == "" {
		return false
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	r.pruneLocked(now)
	ticket := r.tickets[uuid]
	if ticket == nil || ticket.claims >= 2 {
		return false
	}
	ticket.claims++
	if ticket.claims == 2 {
		delete(r.tickets, uuid)
		r.used[uuid] = ticket.expiresAt
		if r.live == nil {
			r.live = make(map[string]livePair)
		}
		r.live[uuid] = livePair{
			LivePair: LivePair{Initiator: ticket.initiatorID, Target: ticket.targetID, Account: ticket.account, ConsoleSession: ticket.session, Since: now},
			since:    now,
		}
	}
	return true
}

// Live returns the session of uuid while it runs: from the second claim
// of its ticket until End.
func (r *AuthorizationRegistry) Live(uuid string) (LivePair, bool) {
	if uuid == "" {
		return LivePair{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(r.now())
	pair, ok := r.live[uuid]
	return pair.LivePair, ok
}

// End forgets the live session of uuid; the relay calls it when the
// session's two sides part.
func (r *AuthorizationRegistry) End(uuid string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.live, uuid)
}

// Release returns a first claim to the ticket when its pending relay
// connection times out before a second peer arrives.
func (r *AuthorizationRegistry) Release(uuid string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	r.pruneLocked(now)
	if ticket := r.tickets[uuid]; ticket != nil && ticket.claims > 0 {
		ticket.claims--
	}
}

// RevokeForPeer invalidates all unpaired relay tickets involving peerID.
// Consumed tickets remain tombstoned until expiry so a UUID cannot be replayed.
func (r *AuthorizationRegistry) RevokeForPeer(peerID string) {
	if peerID == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	r.pruneLocked(now)
	for uuid, ticket := range r.tickets {
		if ticket.initiatorID == peerID || ticket.targetID == peerID {
			delete(r.tickets, uuid)
			r.used[uuid] = ticket.expiresAt
			r.signalWaitersLocked(uuid)
		}
	}
}

func (r *AuthorizationRegistry) signalWaitersLocked(uuid string) {
	if r.waiters == nil {
		return
	}
	if waiter := r.waiters[uuid]; waiter != nil {
		delete(r.waiters, uuid)
		close(waiter)
	}
}

func (r *AuthorizationRegistry) removeWaiter(uuid string, waiter chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if current := r.waiters[uuid]; current == waiter {
		delete(r.waiters, uuid)
	}
}

func (r *AuthorizationRegistry) pruneLocked(now time.Time) {
	for uuid, ticket := range r.tickets {
		if !ticket.expiresAt.After(now) {
			delete(r.tickets, uuid)
		}
	}
	for uuid, expiresAt := range r.used {
		if !expiresAt.After(now) {
			delete(r.used, uuid)
		}
	}
	for uuid, pair := range r.live {
		if now.Sub(pair.since) > liveMaxAge {
			delete(r.live, uuid)
		}
	}
}
