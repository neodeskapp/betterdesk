package auth

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
)

// Signed-in devices (DECISIONS A239): the account's own sessions, from
// the account menu. An account may be signed in on several browsers; here
// it sees them and signs any of them out but the one it asks from, which
// signs out with Sign out.

// DevicesAnswer is the answer of GET /api/users/me/sessions and of the
// user page's GET /api/users/{id}/sessions.
type DevicesAnswer struct {
	Success bool        `json:"success"`
	Data    DevicesData `json:"data"`
}

// DevicesData is the list and the limit that applies to it.
type DevicesData struct {
	Sessions []session.Device `json:"sessions"`
	Limit    DevicesLimit     `json:"limit"`
}

// DevicesLimit is how many sessions the account may have, and what
// decides it: "env" (SESSION_MAX_PER_USER), "setting" (One session per
// account) or "default". Max 0 is no limit.
type DevicesLimit struct {
	Max    int    `json:"max"`
	Source string `json:"source"`
}

// ListDevices is the answer for an account's sessions; current is the
// session of the request ("" for none).
func ListDevices(r *http.Request, m *session.Manager, userID int64, current string) (DevicesAnswer, error) {
	list, err := m.Devices(r.Context(), userID, current)
	if err != nil {
		return DevicesAnswer{}, err
	}
	limit, err := m.Limit(r.Context())
	if err != nil {
		return DevicesAnswer{}, err
	}
	return DevicesAnswer{Success: true, Data: DevicesData{Sessions: list, Limit: DevicesLimit{Max: limit.Max, Source: limit.Source}}}, nil
}

// The refusals of ending a session from a list.
var (
	SessionNotFound = failure{Error: "Session not found"}
	CurrentSession  = codedFailure{Error: "This is the session of this browser. Use Sign out.", Code: "current_session"}
)

type failure struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

type codedFailure struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code"`
}

// AnswerEndError answers a refused or failed end of a session and
// reports whether there was one to answer.
func AnswerEndError(w http.ResponseWriter, r *http.Request, err error, log *kit.Logger) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, session.ErrSessionNotFound):
		kit.JSON(w, r, http.StatusNotFound, SessionNotFound)
	case errors.Is(err, session.ErrCurrentSession):
		kit.JSON(w, r, http.StatusBadRequest, CurrentSession)
	default:
		log.Error("End session error:", err)
		kit.JSON(w, r, http.StatusInternalServerError, failure{Error: kit.T(r, "errors.server_error", nil)})
	}
	return true
}

// currentSession is the id of the request's own session, "" for a request
// an API token signed in.
func currentSession(r *http.Request) string {
	sess := kit.Session(r)
	if sess.Ephemeral() {
		return ""
	}
	return sess.ID()
}

// ownDevices answers GET /api/users/me/sessions.
func (h *handlers) ownDevices(w http.ResponseWriter, r *http.Request) {
	answer, err := ListDevices(r, h.sessions, kit.UserID(r), currentSession(r))
	if err != nil {
		h.serverError(w, r, "List sessions error:", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, answer)
}

// endOwnDevice answers DELETE /api/users/me/sessions/{id}: the account
// signs out one of its other browsers. That browser is told at its next
// request that it was signed out from another device.
func (h *handlers) endOwnDevice(w http.ResponseWriter, r *http.Request) {
	userID := kit.UserID(r)
	d, err := h.sessions.EndDevice(r.Context(), userID, r.PathValue("id"), currentSession(r), session.ReasonSignedOut)
	if AnswerEndError(w, r, err, h.log) {
		return
	}
	if err := h.auditSignedOut(r, userID, d); err != nil {
		h.serverError(w, r, "End session error:", err)
		return
	}
	kit.OK(w, r, nil)
}

type endedAnswer struct {
	Success bool `json:"success"`
	Ended   int  `json:"ended"`
}

// endOtherDevices answers POST /api/users/me/sessions/end-others: "Sign out
// everywhere else" ends every session of the account but this one.
func (h *handlers) endOtherDevices(w http.ResponseWriter, r *http.Request) {
	userID := kit.UserID(r)
	ended, err := h.sessions.EndDevices(r.Context(), userID, currentSession(r), session.ReasonSignedOut)
	if err != nil {
		h.serverError(w, r, "End sessions error:", err)
		return
	}
	for _, d := range ended {
		if err := h.auditSignedOut(r, userID, d); err != nil {
			h.serverError(w, r, "End sessions error:", err)
			return
		}
	}
	kit.JSON(w, r, http.StatusOK, endedAnswer{Success: true, Ended: len(ended)})
}

// auditSignedOut notes a session the account signed out from another
// browser; the entry's address is the browser that did it.
func (h *handlers) auditSignedOut(r *http.Request, userID int64, d session.Device) error {
	name := ""
	if user := kit.User(r); user != nil {
		name = user.Username
	}
	return h.audit(r, userID, "session_signed_out",
		fmt.Sprintf("Signed out session %s (%s) of %s from another device", d.Short(), d.Describe(), name))
}
