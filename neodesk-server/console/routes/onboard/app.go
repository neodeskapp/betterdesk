package onboard

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/authn"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/onboard"
)

// maxPasswordLength bounds the session password the app hands over.
const maxPasswordLength = 128

type sessionKey struct{}

// requireSessionToken lets a request through that carries the session
// token of an open quick support session (Authorization: Bearer
// <token>); the handler finds the session with appSessionOf. Anything
// else is 401 invalid_session, also the token of a session that ended.
func (h *handlers) requireSessionToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := authn.ExtractBearerToken(r.Header.Get("Authorization"))
		if !ok {
			kit.JSON(w, r, http.StatusUnauthorized, refusal{Error: "invalid_session"})
			return
		}
		sess, err := h.sessions.ByToken(r.Context(), token)
		if errors.Is(err, onboard.ErrNotFound) {
			kit.JSON(w, r, http.StatusUnauthorized, refusal{Error: "invalid_session"})
			return
		}
		if err != nil && sess.ID == "" {
			h.log.Error("Quick support session:", err)
			kit.JSON(w, r, http.StatusInternalServerError, refusal{Error: "server_error"})
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, sess)))
	})
}

func appSessionOf(r *http.Request) onboard.Session {
	sess, _ := r.Context().Value(sessionKey{}).(onboard.Session)
	return sess
}

// appView is the session as the app sees it.
type appView struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	ExpiresAt string `json:"expires_at"`
	DeviceID  string `json:"device_id"`
}

type appAnswer struct {
	Success bool    `json:"success"`
	Session appView `json:"session"`
}

func appAnswerOf(s onboard.Session) appAnswer {
	return appAnswer{Success: true, Session: appView{ID: s.ID, Status: s.Status, ExpiresAt: s.ExpiresAt, DeviceID: s.DeviceID}}
}

// appFail answers a refused step of the app: {success:false, error}.
func appFail(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	kit.JSON(w, r, status, refusal{Error: code, Message: message})
}

// redeemAnswer is POST /api/support/redeem.
type redeemAnswer struct {
	Success      bool         `json:"success"`
	SessionToken string       `json:"session_token"`
	Session      appView      `json:"session"`
	Supporter    string       `json:"supporter"`
	Company      string       `json:"company"`
	Server       serverAnswer `json:"server"`
	Console      string       `json:"console"`
}

// redeemSupport answers POST /api/support/redeem with the body {code}:
// the quick support app spends the code and gets its session token, the
// supporter's name and how to reach the server.
func (h *handlers) redeemSupport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if err := kit.Bind(r, &body); err != nil || !onboard.ValidFormat(body.Code) {
		unusable(w, r)
		return
	}
	code, _, err := h.codes.Redeem(r.Context(), body.Code, onboard.KindSupport, nil)
	if errors.Is(err, onboard.ErrUnusable) {
		unusable(w, r)
		return
	}
	if err != nil {
		h.log.Error("Redeem support code:", err)
		appFail(w, r, http.StatusInternalServerError, "server_error", "")
		return
	}
	token, sess, err := h.sessions.Redeem(r.Context(), code)
	if err != nil {
		h.log.Error("Open quick support session:", err)
		appFail(w, r, http.StatusInternalServerError, "server_error", "")
		return
	}
	h.auditAs(r, sess.CreatedByID, "quick_support_opened", "The quick support app opened session "+sess.ID)
	kit.JSON(w, r, http.StatusOK, redeemAnswer{
		Success: true, SessionToken: token, Session: appAnswerOf(sess).Session,
		Supporter: h.personName(r.Context(), sess.CreatedByID, sess.CreatedBy), Company: h.serverName(),
		Server: h.serverOf(r), Console: h.consoleOf(r).Base(),
	})
}

// auditAs writes an audit entry in the name of the account that made the
// session, for what the app does.
func (h *handlers) auditAs(r *http.Request, userID int64, action, details string) {
	if h.d.Audit == nil {
		return
	}
	if err := h.d.Audit.Write(r.Context(), userID, action, details, kit.ClientIP(r)); err != nil {
		h.log.Warn("Audit entry "+action+" failed:", err)
	}
}

// appSession answers GET /api/support/session, which the app polls: the
// session's state. It also keeps the session open (onboard.IdleLimit).
func (h *handlers) appSession(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, appAnswerOf(appSessionOf(r)))
}

// appDevice answers POST /api/support/session/device with the body
// {device_id, uuid, hostname, platform, version}: the device the app runs
// on. A device the server does not know is admitted for the session (a
// temporary device, removed after it), put into the session's folder and
// given to the supporter.
func (h *handlers) appDevice(w http.ResponseWriter, r *http.Request) {
	sess := appSessionOf(r)
	if sess.Status != onboard.StatusOpened {
		appFail(w, r, http.StatusConflict, onboard.ErrState.Error(), "")
		return
	}
	var dev onboard.Device
	if err := kit.Bind(r, &dev); err != nil || !authn.IsDeviceID(strings.TrimSpace(dev.DeviceID)) {
		appFail(w, r, http.StatusBadRequest, "invalid_device", "")
		return
	}
	dev.DeviceID = strings.TrimSpace(dev.DeviceID)
	dev.PublicKey, dev.Temporary, dev.DisplayName = "", true, sess.Name
	created, err := h.codes.Admit(r.Context(), dev)
	var refused *onboard.RefusedError
	var invalid *onboard.InvalidDeviceError
	switch {
	case errors.As(err, &refused):
		appFail(w, r, http.StatusForbidden, "device_refused", refused.Reason)
		return
	case errors.As(err, &invalid):
		appFail(w, r, http.StatusBadRequest, "invalid_device", invalid.Reason)
		return
	case err != nil:
		h.log.Error("Admit quick support device:", err)
		appFail(w, r, http.StatusInternalServerError, "server_error", "")
		return
	}
	granted := false
	if created {
		ctx := r.Context()
		if sess.FolderID > 0 && h.d.DeviceGroups != nil {
			if err := h.d.DeviceGroups.AssignDeviceToFolder(ctx, jsbind.String(dev.DeviceID), jsbind.Number(float64(sess.FolderID))); err != nil {
				h.log.Warn("Put quick support device into its folder:", err)
			}
		}
		if h.d.Store != nil && sess.CreatedByID > 0 {
			insert := `INSERT INTO user_peer_grants (user_id, peer_id) VALUES (?, ?) ON CONFLICT DO NOTHING`
			if _, err := h.d.Store.Exec(ctx, insert, sess.CreatedByID, dev.DeviceID); err != nil {
				h.log.Warn("Give the supporter the quick support device:", err)
			} else {
				granted = true
			}
		}
	}
	updated, err := h.sessions.SetDevice(r.Context(), sess.ID, dev, created, granted)
	if errors.Is(err, onboard.ErrState) {
		appFail(w, r, http.StatusConflict, onboard.ErrState.Error(), "")
		return
	}
	if err != nil {
		h.log.Error("Record quick support device:", err)
		appFail(w, r, http.StatusInternalServerError, "server_error", "")
		return
	}
	h.auditAs(r, sess.CreatedByID, "quick_support_device", "Quick support session "+sess.ID+" runs on device "+dev.DeviceID)
	kit.JSON(w, r, http.StatusOK, appAnswerOf(updated))
}

// appConsent answers POST /api/support/session/consent with the body
// {allowed}: the person's answer on their screen. Declined ends the
// session.
func (h *handlers) appConsent(w http.ResponseWriter, r *http.Request) {
	sess := appSessionOf(r)
	var body struct {
		Allowed *bool `json:"allowed"`
	}
	if err := kit.Bind(r, &body); err != nil || body.Allowed == nil {
		appFail(w, r, http.StatusBadRequest, "invalid_body", "")
		return
	}
	updated, err := h.sessions.Consent(r.Context(), sess.ID, *body.Allowed)
	if errors.Is(err, onboard.ErrState) {
		appFail(w, r, http.StatusConflict, onboard.ErrState.Error(), "")
		return
	}
	if err != nil {
		h.log.Error("Record quick support consent:", err)
		appFail(w, r, http.StatusInternalServerError, "server_error", "")
		return
	}
	action := "quick_support_allowed"
	if !*body.Allowed {
		action = "quick_support_declined"
	}
	h.auditAs(r, sess.CreatedByID, action, "Quick support session "+sess.ID+" on device "+sess.DeviceID)
	kit.JSON(w, r, http.StatusOK, appAnswerOf(updated))
}

// appPassword answers POST /api/support/session/password with the body
// {password}: the session password for the supporter, kept in memory
// until the supporter takes it or the session ends.
func (h *handlers) appPassword(w http.ResponseWriter, r *http.Request) {
	sess := appSessionOf(r)
	var body struct {
		Password string `json:"password"`
	}
	if err := kit.Bind(r, &body); err != nil || body.Password == "" || utf8.RuneCountInString(body.Password) > maxPasswordLength {
		appFail(w, r, http.StatusBadRequest, "invalid_password", "")
		return
	}
	if err := h.sessions.SetPassword(r.Context(), sess.ID, body.Password); errors.Is(err, onboard.ErrState) {
		appFail(w, r, http.StatusConflict, onboard.ErrState.Error(), "")
		return
	} else if err != nil {
		h.log.Error("Keep the session password:", err)
		appFail(w, r, http.StatusInternalServerError, "server_error", "")
		return
	}
	kit.JSON(w, r, http.StatusOK, map[string]bool{"success": true})
}

// appEnd answers POST /api/support/session/end: the app closed. The token
// stops working.
func (h *handlers) appEnd(w http.ResponseWriter, r *http.Request) {
	sess := appSessionOf(r)
	if _, err := h.sessions.End(r.Context(), sess.ID, onboard.EndedByCustomer); err != nil && !errors.Is(err, onboard.ErrState) {
		h.log.Error("End quick support session:", err)
		appFail(w, r, http.StatusInternalServerError, "server_error", "")
		return
	}
	h.auditAs(r, sess.CreatedByID, "quick_support_ended", "The quick support app ended session "+sess.ID)
	kit.JSON(w, r, http.StatusOK, map[string]bool{"success": true})
}
