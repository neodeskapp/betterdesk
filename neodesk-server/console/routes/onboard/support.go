package onboard

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/onboard"
)

// supportSession is a quick support session as the supporter sees it.
type supportSession struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	Name          string `json:"name"`
	Email         string `json:"email"`
	FolderID      *int64 `json:"folder_id"`
	CreatedBy     string `json:"created_by"`
	CreatedAt     string `json:"created_at"`
	ExpiresAt     string `json:"expires_at"`
	RedeemedAt    string `json:"redeemed_at"`
	ConsentAt     string `json:"consent_at"`
	EndedAt       string `json:"ended_at"`
	EndedBy       string `json:"ended_by"`
	EndReason     string `json:"end_reason,omitempty"`
	DeviceID      string `json:"device_id"`
	Hostname      string `json:"hostname"`
	Platform      string `json:"platform"`
	PasswordReady bool   `json:"password_ready"`
}

func (h *handlers) sessionView(s onboard.Session) *supportSession {
	out := &supportSession{
		ID: s.ID, Status: s.Status, Name: s.Name, Email: s.Email, CreatedBy: s.CreatedBy,
		CreatedAt: s.CreatedAt, ExpiresAt: s.ExpiresAt, RedeemedAt: s.RedeemedAt, ConsentAt: s.ConsentAt,
		EndedAt: s.EndedAt, EndedBy: s.EndedBy, EndReason: s.EndReason, DeviceID: s.DeviceID, Hostname: s.Hostname, Platform: s.Platform,
		PasswordReady: h.sessions.HasPassword(s.ID),
	}
	if s.FolderID > 0 {
		id := s.FolderID
		out.FolderID = &id
	}
	return out
}

type sessionsAnswer struct {
	Success  bool              `json:"success"`
	Sessions []*supportSession `json:"sessions"`
}

// listSessions answers GET /api/support/sessions: the open sessions the
// account made, newest first.
func (h *handlers) listSessions(w http.ResponseWriter, r *http.Request) {
	list, err := h.sessions.OpenOf(r.Context(), kit.UserID(r))
	if err != nil {
		h.serverError(w, r, "List quick support sessions:", err)
		return
	}
	out := sessionsAnswer{Success: true, Sessions: []*supportSession{}}
	for _, s := range list {
		out.Sessions = append(out.Sessions, h.sessionView(s))
	}
	kit.JSON(w, r, http.StatusOK, out)
}

// createSession answers POST /api/support/sessions with the body {name,
// email, folder_id, send_email}: a support code for one hour and one use,
// and its session.
func (h *handlers) createSession(w http.ResponseWriter, r *http.Request) {
	req, ok := readCodeRequest(w, r)
	if !ok {
		return
	}
	text, code, ok := h.createCodeFor(w, r, onboard.KindSupport, req, onboard.SupportLifetime, 1)
	if !ok {
		return
	}
	sess, err := h.sessions.Create(r.Context(), code)
	if err != nil {
		// The code works without its stored record: the redeem makes it
		// from the code.
		h.log.Warn("Store quick support session:", err)
	}
	details := "Created quick support session " + code.ID
	if code.Name != "" {
		details += " (" + code.Name + ")"
	}
	h.audit(r, "quick_support_created", details)

	c := h.consoleOf(r)
	out := createdAnswer{
		Success: true, Session: h.sessionView(sess), Code: onboard.Normalize(text), DisplayCode: onboard.Display(text),
		Link: c.SupportLink(text), DeepLink: c.DeepLink(onboard.KindSupport, text),
	}
	if req.SendEmail {
		out.Mailed, out.MailError = h.sendCodeMail(r, code, text, req.Email)
	}
	kit.JSON(w, r, http.StatusCreated, out)
}

// ownSession returns the session of the path when the signed-in account
// made it; any other session is 404, so ids of other accounts' sessions
// tell nothing.
func (h *handlers) ownSession(w http.ResponseWriter, r *http.Request) (onboard.Session, bool) {
	id := strings.TrimSpace(r.PathValue("id"))
	if !codeID(id) {
		fail(w, r, http.StatusNotFound, "not_found")
		return onboard.Session{}, false
	}
	sess, err := h.sessions.Get(r.Context(), id)
	if errors.Is(err, onboard.ErrNotFound) || (err == nil && sess.CreatedByID != kit.UserID(r)) {
		fail(w, r, http.StatusNotFound, "not_found")
		return onboard.Session{}, false
	}
	if err != nil {
		h.serverError(w, r, "Load quick support session:", err)
		return onboard.Session{}, false
	}
	return sess, true
}

type sessionAnswer struct {
	Success bool            `json:"success"`
	Session *supportSession `json:"session"`
}

// getSession answers GET /api/support/sessions/{id}, which the waiting
// dialog polls.
func (h *handlers) getSession(w http.ResponseWriter, r *http.Request) {
	sess, ok := h.ownSession(w, r)
	if !ok {
		return
	}
	kit.JSON(w, r, http.StatusOK, sessionAnswer{Success: true, Session: h.sessionView(sess)})
}

type passwordAnswer struct {
	Success  bool   `json:"success"`
	Password string `json:"password"`
	// Session is the account's quick support session on the device, for
	// the viewer: the person accepts on their screen, nobody types a
	// password
	Session *deviceSession `json:"session,omitempty"`
}

// deviceSession is the quick support session on a device as the viewer
// sees it: the person's name (or the computer's when the link named
// nobody) and the state.
type deviceSession struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// takePassword answers POST /api/support/sessions/{id}/password: the
// session password the app handed over, once, to the account that made
// the session.
func (h *handlers) takePassword(w http.ResponseWriter, r *http.Request) {
	sess, ok := h.ownSession(w, r)
	if !ok {
		return
	}
	password, err := h.sessions.TakePassword(r.Context(), sess.ID, kit.UserID(r))
	switch {
	case errors.Is(err, onboard.ErrNoPassword):
		fail(w, r, http.StatusConflict, "no_password")
		return
	case errors.Is(err, onboard.ErrNotYours), errors.Is(err, onboard.ErrNotFound):
		fail(w, r, http.StatusNotFound, "not_found")
		return
	case err != nil:
		h.serverError(w, r, "Hand over the session password:", err)
		return
	}
	h.audit(r, "quick_support_password_taken", "Took the session password of quick support session "+sess.ID)
	kit.JSON(w, r, http.StatusOK, passwordAnswer{Success: true, Password: password})
}

// devicePassword answers POST /api/support/devices/{id}/password: the
// password of the account's allowed session on that device, once, for the
// browser viewer when the device asks for one. The viewer asks at every
// password prompt, and most devices have no quick support session, so none
// is an empty password, not an error (L429).
func (h *handlers) devicePassword(w http.ResponseWriter, r *http.Request) {
	deviceID := strings.TrimSpace(r.PathValue("id"))
	password, err := h.sessions.TakePasswordForDevice(r.Context(), deviceID, kit.UserID(r))
	if errors.Is(err, onboard.ErrNoPassword) {
		out := passwordAnswer{Success: true}
		if sess, ok := h.sessions.OpenOnDevice(r.Context(), deviceID, kit.UserID(r)); ok {
			out.Session = &deviceSession{Name: sess.Name, Status: sess.Status}
			if out.Session.Name == "" {
				out.Session.Name = sess.Hostname
			}
		}
		kit.JSON(w, r, http.StatusOK, out)
		return
	}
	if err != nil {
		h.serverError(w, r, "Hand over the session password:", err)
		return
	}
	h.audit(r, "quick_support_password_taken", "Took the session password of the quick support session on device "+deviceID)
	kit.JSON(w, r, http.StatusOK, passwordAnswer{Success: true, Password: password})
}

// endSession answers DELETE /api/support/sessions/{id}: the supporter
// ends the session; a waiting one is revoked with its code.
func (h *handlers) endSession(w http.ResponseWriter, r *http.Request) {
	sess, ok := h.ownSession(w, r)
	if !ok {
		return
	}
	if sess.Status == onboard.StatusWaiting {
		if _, err := h.codes.Revoke(r.Context(), sess.ID); err != nil && !errors.Is(err, onboard.ErrNotFound) {
			h.serverError(w, r, "Revoke support code:", err)
			return
		}
	}
	ended, err := h.sessions.End(r.Context(), sess.ID, onboard.EndedBySupporter)
	if errors.Is(err, onboard.ErrState) {
		kit.JSON(w, r, http.StatusOK, sessionAnswer{Success: true, Session: h.sessionView(sess)})
		return
	}
	if err != nil {
		h.serverError(w, r, "End quick support session:", err)
		return
	}
	h.audit(r, "quick_support_ended", "Ended quick support session "+sess.ID)
	kit.JSON(w, r, http.StatusOK, sessionAnswer{Success: true, Session: h.sessionView(ended)})
}

// mailSession answers POST /api/support/sessions/{id}/mail with the body
// {code, to}.
func (h *handlers) mailSession(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.ownSession(w, r); !ok {
		return
	}
	h.mailCode(w, r, onboard.KindSupport)
}

// removeTemporaryDevice deletes the device a session made, its folder
// place and the grant the session gave; the session's record and the
// device's history stay.
func (h *handlers) removeTemporaryDevice(ctx context.Context, sess onboard.Session) {
	if sess.DeviceID != "" {
		if _, err := h.codes.RemoveDevice(ctx, sess.DeviceID); err != nil {
			h.log.Warn("Remove quick support device:", err)
			return
		}
		if h.d.DeviceGroups != nil {
			if err := h.d.DeviceGroups.AssignDeviceToFolder(ctx, jsbind.String(sess.DeviceID), jsbind.Null); err != nil {
				h.log.Warn("Clear quick support device folder:", err)
			}
		}
		if sess.Granted && h.d.Store != nil {
			if _, err := h.d.Store.Exec(ctx, `DELETE FROM user_peer_grants WHERE user_id = ? AND peer_id = ?`, sess.CreatedByID, sess.DeviceID); err != nil {
				h.log.Warn("Remove quick support grant:", err)
			}
		}
	}
	if err := h.sessions.DeviceRemoved(ctx, sess.ID); err != nil {
		h.log.Warn("Note removed quick support device:", err)
	}
}
