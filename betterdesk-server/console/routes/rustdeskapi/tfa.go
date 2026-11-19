package rustdeskapi

import (
	"sync"
	"time"
)

// tfaSession is a sign-in that waits for its second factor, with the
// client id and UUID of the first step ("" where the body had no text).
type tfaSession struct {
	userID     int64
	username   string
	role       string
	clientID   string
	clientUUID string
	createdAt  time.Time
}

// tfaSessions holds the pending second steps of one listener in memory
// (req.app.locals._tfaSessions): a sign-in started on one port is finished
// on the same port. A step is good for five minutes: an older one is
// refused when it is used, and dropped then or when the next one is added.
type tfaSessions struct {
	mu      sync.Mutex
	pending map[string]tfaSession
}

const tfaMaxAge = 5 * time.Minute

// add stores a pending second step under its secret and drops the ones
// older than five minutes.
func (s *tfaSessions) add(secret string, session tfaSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		s.pending = map[string]tfaSession{}
	}
	s.pending[secret] = session
	for key, old := range s.pending {
		if session.createdAt.Sub(old.createdAt) > tfaMaxAge {
			delete(s.pending, key)
		}
	}
}

// get returns the pending second step of a secret that is not older than
// five minutes at now. A secret that is not text matches none.
func (s *tfaSessions) get(secret any, now time.Time) (tfaSession, bool) {
	text, ok := secret.(string)
	if !ok {
		return tfaSession{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, found := s.pending[text]
	if found && now.Sub(session.createdAt) > tfaMaxAge {
		delete(s.pending, text)
		return tfaSession{}, false
	}
	return session, found
}

// remove drops a pending second step once it is used.
func (s *tfaSessions) remove(secret string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, secret)
}
