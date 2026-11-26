package impersonate

import (
	"net/http"
	"strconv"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/impersonation"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
)

// statusData is an impersonation as the web app sees it. It never holds
// the admin's session.
type statusData struct {
	Active bool `json:"active"`
	// Username is the account viewed, AdminUsername the admin.
	Username      string `json:"username,omitempty"`
	AdminUsername string `json:"admin_username,omitempty"`
	Reason        string `json:"reason,omitempty"`
	// StartedAt and ExpiresAt are in milliseconds.
	StartedAt int64 `json:"started_at,omitempty"`
	ExpiresAt int64 `json:"expires_at,omitempty"`
	// CanExtend says the one extension is still there.
	CanExtend bool `json:"can_extend"`
}

func statusOf(imp *session.Impersonation, username string) statusData {
	if imp == nil {
		return statusData{}
	}
	return statusData{Active: true, Username: username, AdminUsername: imp.AdminUsername, Reason: imp.Reason,
		StartedAt: imp.StartedAt, ExpiresAt: imp.ExpiresAt, CanExtend: !imp.Extended}
}

type statusAnswer struct {
	Success bool       `json:"success"`
	Data    statusData `json:"data"`
}

func (h *handlers) answerStatus(w http.ResponseWriter, r *http.Request) {
	username := ""
	if u := kit.User(r); u != nil {
		username = u.Username
	}
	kit.JSON(w, r, http.StatusOK, statusAnswer{Success: true, Data: statusOf(kit.Impersonation(r), username)})
}

// status answers GET /api/impersonation: the impersonation of this
// browser, {active:false} without one.
func (h *handlers) status(w http.ResponseWriter, r *http.Request) {
	h.answerStatus(w, r)
}

// notActive answers a request to change an impersonation that isn't
// there.
func (h *handlers) notActive(w http.ResponseWriter, r *http.Request) {
	h.failKey(w, r, http.StatusBadRequest, "impersonation_not_active", "impersonation.not_active",
		"You are not viewing NeoDesk as another account.")
}

// extend answers POST /api/impersonation/extend: the impersonation lasts
// until impersonation.MaxPeriod after its start, once.
func (h *handlers) extend(w http.ResponseWriter, r *http.Request) {
	imp := kit.Impersonation(r)
	if imp == nil {
		h.notActive(w, r)
		return
	}
	if imp.Extended {
		h.failKey(w, r, http.StatusConflict, "impersonation_extended_once", "impersonation.extended_once",
			"You can make it longer only once. Stop and start again if you need more time.")
		return
	}
	imp.Extended = true
	imp.ExpiresAt = time.UnixMilli(imp.StartedAt).Add(impersonation.MaxPeriod).UnixMilli()
	kit.Session(r).SetImpersonation(*imp)
	user := kit.User(r)
	h.svc.Audit(r, imp.AdminID, user.ID, impersonation.ActionExtended,
		"Viewing NeoDesk as "+user.Username+" until "+time.UnixMilli(imp.ExpiresAt).UTC().Format("15:04 UTC")+" for: "+imp.Reason)
	h.answerStatus(w, r)
}

// stopAnswer says whether there was an impersonation to stop.
type stopAnswer struct {
	Success bool `json:"success"`
	Stopped bool `json:"stopped"`
}

// stop answers POST /api/impersonation/stop: the impersonation session
// ends and the browser is back on the admin's own session, which stayed
// as it was. Without an impersonation it answers stopped false.
func (h *handlers) stop(w http.ResponseWriter, r *http.Request) {
	imp := kit.Impersonation(r)
	if imp == nil {
		kit.JSON(w, r, http.StatusOK, stopAnswer{Success: true})
		return
	}
	user := kit.User(r)
	h.d.Sessions.EndImpersonation(w, r, session.ReasonImpersonationStopped)
	minutes := max(0, int(h.svc.Now().Sub(time.UnixMilli(imp.StartedAt)).Round(time.Minute).Minutes()))
	h.svc.Audit(r, imp.AdminID, user.ID, impersonation.ActionStopped,
		"Stopped viewing NeoDesk as "+user.Username+" after "+strconv.Itoa(minutes)+" min")
	kit.JSON(w, r, http.StatusOK, stopAnswer{Success: true, Stopped: true})
}
