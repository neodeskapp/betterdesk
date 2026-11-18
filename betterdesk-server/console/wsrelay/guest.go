package wsrelay

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/guestaccess"
)

// guestGrant is a guest link the API server found good for an upgrade.
type guestGrant struct {
	// data is the API server's answer: {valid, id, peer_ids, view_only,
	// expires_at, ...}.
	data      any
	token     string
	peerID    string
	sessionID string
}

// id is grant.id as the API server sent it (any JSON value).
func (g *guestGrant) id() any { return goapi.Dig(g.data, "id") }

// expiresAt is new Date(grant.expires_at).getTime(); ok is false for NaN,
// which a missing expires_at gives (null gives 0).
func (g *guestGrant) expiresAt() (int64, bool) {
	obj, ok := g.data.(*goapi.Object)
	if !ok || !obj.Has("expires_at") {
		return 0, false
	}
	return jsDate(obj.Get("expires_at"))
}

// maxTimerDelay is the longest delay setTimeout takes; a longer one fires
// after a millisecond.
const maxTimerDelay = 2147483647 * time.Millisecond

// jsDelay is the delay setTimeout uses for d.
func jsDelay(d time.Duration) time.Duration {
	if d > maxTimerDelay || d < time.Millisecond {
		return time.Millisecond
	}
	return d
}

// stateKey is the key of the guest session's use of the link:
// `${grant.id || 'unknown'}\0${sessionId}`.
func (g *guestGrant) stateKey() string {
	id := "unknown"
	if v := g.id(); goapi.Truthy(v) {
		id, _ = guestaccess.JSText(v)
	}
	return id + "\x00" + g.sessionID
}

// consumeState is a use of a link being counted, or counted: the guest
// sessions with the same link and session id share it, so one guest
// session uses the link once.
type consumeState struct {
	done chan struct{}
	err  error
}

// consume counts one use of the link for the guest session, once per link
// and session id (consumeGuestSession). A counted use is remembered until
// the link ends (a second at least; 15 seconds when the link has no end);
// a failed one is forgotten, and every bridge that waited for it fails.
func (s *Service) consume(ctx context.Context, g *guestGrant) error {
	key := g.stateKey()
	s.mu.Lock()
	if st, ok := s.consumes[key]; ok {
		s.mu.Unlock()
		<-st.done
		return st.err
	}
	st := &consumeState{done: make(chan struct{})}
	s.consumes[key] = st
	s.mu.Unlock()

	_, err := s.opts.GoAPI.Post(ctx, "/guest/access-links/consume",
		goapi.Body(goapi.NewObject("token", g.token, "peer_id", g.peerID)))
	if err == nil {
		lifetime := GuestRevalidationInterval
		if end, ok := g.expiresAt(); ok {
			left := end - s.clock.Now().UnixMilli()
			if left < 1000 {
				left = 1000
			}
			lifetime = time.Duration(left) * time.Millisecond
		}
		s.clock.AfterFunc(jsDelay(lifetime), func() {
			s.mu.Lock()
			delete(s.consumes, key)
			s.mu.Unlock()
		})
	} else {
		s.mu.Lock()
		delete(s.consumes, key)
		s.mu.Unlock()
	}
	st.err = err
	close(st.done)
	return err
}

// registerGrant adds a bridge to the bridges of its link, so taking the
// link back ends it. Only a link whose id is a non-empty text can be
// taken back by id; others are not listed.
func (s *Service) registerGrant(g *guestGrant, b *bridge) func() {
	id, isText := g.id().(string)
	if !isText || id == "" {
		return func() {}
	}
	s.mu.Lock()
	s.grants[id] = append(s.grants[id], b)
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		list := s.grants[id]
		for i, other := range list {
			if other == b {
				list = append(list[:i:i], list[i+1:]...)
				break
			}
		}
		if len(list) == 0 {
			delete(s.grants, id)
		} else {
			s.grants[id] = list
		}
	}
}

// InvalidateGuestGrant ends at once every bridge of a link that was taken
// back and forgets the uses its guest sessions counted
// (invalidateGuestGrant). It returns the number of the link's bridges
// still listed afterwards, which is 0 once they have ended, as the old
// function returned.
func (s *Service) InvalidateGuestGrant(grantID string) int {
	s.mu.Lock()
	for key := range s.consumes {
		if strings.HasPrefix(key, grantID+"\x00") {
			delete(s.consumes, key)
		}
	}
	list := append([]*bridge(nil), s.grants[grantID]...)
	_, known := s.grants[grantID]
	s.mu.Unlock()
	if !known {
		return 0
	}
	for _, b := range list {
		b.cleanup("guest-grant-revoked", "")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.grants[grantID])
}

// jsDate is new Date(value).getTime() for what an "expires_at" holds: a
// number of milliseconds, null (0), a flag (0 or 1), or a time as text in
// the forms the API server writes (RFC 3339 with or without a fraction,
// with a zone; a plain date in UTC). ok is false for NaN: no value, and
// any other text, where the old console's date was not a time either or
// a form the API server never sends.
func jsDate(value any) (int64, bool) {
	switch t := value.(type) {
	case nil:
		return 0, true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) || math.Abs(t) > 8.64e15 {
			return 0, false
		}
		return int64(math.Trunc(t)), true
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04Z07:00"} {
			if parsed, err := time.Parse(layout, t); err == nil {
				return parsed.UnixMilli(), true
			}
		}
		if parsed, err := time.Parse("2006-01-02", t); err == nil {
			return parsed.UnixMilli(), true
		}
	}
	return 0, false
}
