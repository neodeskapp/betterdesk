package session

import (
	"context"
	"errors"
	"net/http"
)

// ErrEphemeral is what a one-request session answers to sign-in and
// regeneration: it can carry no other account than its own.
var ErrEphemeral = errors.New("session: a request signed in by an API token has no session to change")

// Ephemeral gives a request a session signed in as user that lives for
// this request only: it is never stored, sends no cookie, and Regenerate
// and Establish fail on it, so no route can turn it into a browser
// session. Fields handlers set stay in memory until the answer. An API
// token's request gets one in place of Middleware (package admintoken).
func (m *Manager) Ephemeral(r *http.Request, user User) *http.Request {
	s := &Session{m: m, req: r, ephemeral: true, committed: true}
	s.reset()
	s.data.set("userId", mustJSON(user.ID))
	s.data.set("user", mustJSON(user))
	s.original = s.data.encode(false)
	r = r.WithContext(context.WithValue(r.Context(), contextKey{}, s))
	s.req = r
	return r
}

// Ephemeral reports whether the session lives for one request only.
func (s *Session) Ephemeral() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ephemeral
}
