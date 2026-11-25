package users

import (
	"fmt"
	"net/http"

	authroutes "github.com/neodeskapp/neodesk/neodesk-server/console/routes/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// The user's page shows the account's sessions to someone with user.edit
// (DECISIONS A239): the same list as the account's own Signed-in devices,
// with Sign out per entry and for all. An account above the caller's own
// (mayChange) stays out of reach, as for its other changes.

// sessionTarget reads the account of the path and checks that the caller
// may change it. It answers the request itself when not.
func (h *handlers) sessionTarget(w http.ResponseWriter, r *http.Request, write bool) (*account, bool) {
	userID, ok := pathID(r)
	if !ok {
		invalidID(w, r)
		return nil, false
	}
	user, err := accountByID(r.Context(), h.d.Store, userID)
	if err != nil {
		h.serverError(w, r, "User sessions error:", err)
		return nil, false
	}
	if user == nil {
		failKey(w, r, http.StatusNotFound, "users.not_found")
		return nil, false
	}
	if write {
		if ok, err := h.mayChange(r, user); err != nil {
			h.serverError(w, r, "User sessions error:", err)
			return nil, false
		} else if !ok {
			sent(w, r, errRoleAboveOwn)
			return nil, false
		}
	}
	return user, true
}

// callerSession is the id of the caller's own session, "" for a request an
// API token signed in.
func callerSession(r *http.Request) string {
	sess := kit.Session(r)
	if sess.Ephemeral() {
		return ""
	}
	return sess.ID()
}

// userSessions answers GET /api/users/{id}/sessions: the account's live
// sessions, as its own Signed-in devices lists them.
func (h *handlers) userSessions(w http.ResponseWriter, r *http.Request) {
	user, ok := h.sessionTarget(w, r, false)
	if !ok {
		return
	}
	list, err := authroutes.ListDevices(r, h.d.Sessions, user.ID.V, callerSession(r))
	if err != nil {
		h.serverError(w, r, "User sessions error:", err)
		return
	}
	answer(w, r, http.StatusOK, list)
}

// endUserSession answers DELETE /api/users/{id}/sessions/{sessionId}. The
// browser of that session is told at its next request that an
// administrator signed it out.
func (h *handlers) endUserSession(w http.ResponseWriter, r *http.Request) {
	user, ok := h.sessionTarget(w, r, true)
	if !ok {
		return
	}
	d, err := h.d.Sessions.EndDevice(r.Context(), user.ID.V, r.PathValue("sessionId"), callerSession(r), session.ReasonEndedByAdmin)
	if authroutes.AnswerEndError(w, r, err, h.log) {
		return
	}
	if err := h.auditEndedByAdmin(r, user, d); err != nil {
		h.serverError(w, r, "User sessions error:", err)
		return
	}
	answer(w, r, http.StatusOK, done{Success: true})
}

type endedSessions struct {
	Success bool `json:"success"`
	Ended   int  `json:"ended"`
}

// endUserSessions answers POST /api/users/{id}/sessions/end-all: every
// session of the account ends, but the caller's own when the account is
// the caller's.
func (h *handlers) endUserSessions(w http.ResponseWriter, r *http.Request) {
	user, ok := h.sessionTarget(w, r, true)
	if !ok {
		return
	}
	keep := ""
	if isSessionAccount(r, user.ID.V) {
		keep = callerSession(r)
	}
	ended, err := h.d.Sessions.EndDevices(r.Context(), user.ID.V, keep, session.ReasonEndedByAdmin)
	if err != nil {
		h.serverError(w, r, "User sessions error:", err)
		return
	}
	for _, d := range ended {
		if err := h.auditEndedByAdmin(r, user, d); err != nil {
			h.serverError(w, r, "User sessions error:", err)
			return
		}
	}
	answer(w, r, http.StatusOK, endedSessions{Success: true, Ended: len(ended)})
}

// auditEndedByAdmin notes who ended whose session: the entry belongs to
// the caller and carries the caller's address.
func (h *handlers) auditEndedByAdmin(r *http.Request, user *account, d session.Device) error {
	return h.d.LogAction(r, "session_ended_by_admin",
		fmt.Sprintf("Ended session %s (%s) of %s", d.Short(), d.Describe(), user.username()))
}
