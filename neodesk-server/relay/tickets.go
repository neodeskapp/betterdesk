package relay

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"time"
)

// Ticket permits one relay pair: the two sides of the session UUID, from
// Initiator to Target, for the console account behind it (0 for none) and
// the console session (a handle, "" for none) whose viewer opened it. A
// zero Expires means the store's own short expiry (config.RelayPairTimeout).
type Ticket struct {
	UUID           string
	Initiator      string
	Target         string
	Account        int64
	ConsoleSession string
	Expires        time.Time
}

var (
	// ErrTicketRejected means the store refused the ticket: the UUID is
	// spent or taken by another pair. Signal refuses the session.
	ErrTicketRejected = errors.New("relay ticket rejected")
	// ErrTicketsUnavailable means the store could not be asked in time.
	// Signal may pick another relay or refuse the session; it must not
	// treat it as a refusal of the pair.
	ErrTicketsUnavailable = errors.New("relay ticket store unavailable")
)

// Tickets is the side of a ticket store that signal uses: it issues a
// ticket before it names the relay to either peer. The in-process store
// is *AuthorizationRegistry; other stores hold the tickets elsewhere, so a
// caller that issues tickets on several stores revokes on each of them.
type Tickets interface {
	// Authorize issues t. A retry of an active ticket must name the same
	// pair, account and console session. It returns nil, an error that
	// wraps ErrTicketRejected or one that wraps ErrTicketsUnavailable.
	Authorize(ctx context.Context, t Ticket) error
	// Admitted reports whether uuid has a ticket for the pair, unclaimed
	// or live.
	Admitted(uuid, initiator, target string) bool
	// RevokeForPeer drops the unclaimed tickets of peer (a ban).
	RevokeForPeer(peer string)
	// Live returns the session of uuid while it runs.
	Live(uuid string) (LivePair, bool)
}

// TicketClaims is the side of a ticket store that the relay uses: each of
// the two connections of a session claims the ticket once.
type TicketClaims interface {
	// Claim reserves one of the two connections of uuid; the second
	// claim spends the ticket and makes the session live.
	Claim(uuid string) bool
	// Release returns a first claim whose partner never came.
	Release(uuid string)
	// WaitForAuthorization waits until uuid has a ticket or ctx ends.
	WaitForAuthorization(ctx context.Context, uuid string) bool
	// End forgets the live session of uuid once its sides part.
	End(uuid string)
	// Live returns the session of uuid while it runs.
	Live(uuid string) (LivePair, bool)
}

// SessionStats is what the relay carried in one session: the bytes each
// side sent (First is the side that came first).
type SessionStats struct {
	BytesFromFirst  int64
	BytesFromSecond int64
}

// SessionReporter hears of relay sessions: when both sides are paired and
// when they part. Calls come from the session's goroutine and must not
// block for long.
type SessionReporter interface {
	RelayStarted(uuid string)
	RelayEnded(uuid string, stats SessionStats)
}

// SessionReporterFuncs adapts two funcs to a SessionReporter; nil funcs
// are skipped.
type SessionReporterFuncs struct {
	Started func(uuid string)
	Ended   func(uuid string, stats SessionStats)
}

func (f SessionReporterFuncs) RelayStarted(uuid string) {
	if f.Started != nil {
		f.Started(uuid)
	}
}

func (f SessionReporterFuncs) RelayEnded(uuid string, stats SessionStats) {
	if f.Ended != nil {
		f.Ended(uuid, stats)
	}
}

// DefaultTickets is the in-process store that the signal and relay
// servers of one process share unless they are given another.
func DefaultTickets() *AuthorizationRegistry { return defaultAuthorizationRegistry }

// byteMeter counts the bytes written through it and passes them on to
// next, the bandwidth pacer, when there is one.
type byteMeter struct {
	n    atomic.Int64
	next io.Writer
}

func (m *byteMeter) Write(p []byte) (int, error) {
	m.n.Add(int64(len(p)))
	if m.next != nil {
		_, _ = m.next.Write(p)
	}
	return len(p), nil
}
