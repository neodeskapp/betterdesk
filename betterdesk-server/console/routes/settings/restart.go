package settings

import (
	"errors"
	"net/http"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/restart"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// The restart transaction: the changes an operator saved that need a
// restart of the server, which the operator cancels or confirms (package
// console/restart). The web app polls the public status while the server
// restarts.

type liveness struct {
	Status       string `json:"status"`
	CacheVersion string `json:"cacheVersion"`
	Uptime       int64  `json:"uptime"`
}

type starting struct {
	Success bool `json:"success"`
	Data    struct {
		Status string `json:"status"`
		Ready  bool   `json:"ready"`
	} `json:"data"`
	Error string `json:"error"`
}

// restartStatus answers GET /api/settings/restart-status, for everyone:
// with ?job= and ?token= the public status of that restart, else that
// this process serves requests, since when and in which version.
func (g *general) restartStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
	job, token := jsreq.Query(r, "job"), jsreq.Query(r, "token")
	if goapi.Truthy(job) && goapi.Truthy(token) {
		status, err := g.d.Restart.GetPublicStatus(r.Context(), jsreq.String(job), jsreq.String(token))
		if err != nil {
			var out starting
			out.Data.Status = "starting"
			out.Error = err.Error()
			kit.JSON(w, r, http.StatusServiceUnavailable, out)
			return
		}
		kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: status})
		return
	}
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: liveness{
		Status:       "ok",
		CacheVersion: g.cacheVersion,
		Uptime:       int64(time.Since(g.started) / time.Second),
	}})
}

// restartPending answers GET /api/settings/restart/pending: the caller's
// transaction, or null.
func (g *general) restartPending(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	pending, err := g.d.Restart.GetPending(kit.UserID(r))
	if err != nil {
		g.d.WebUI.ServerError(w, r, err, http.StatusInternalServerError)
		return
	}
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: pending})
}

// refusal is the answer of a failed cancel or confirm: the message and,
// for a rollback that failed, the changes that could not be undone.
type refusal struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Details []any  `json:"details,omitempty"`
}

func (g *general) refuse(w http.ResponseWriter, r *http.Request, err error) {
	out := refusal{Error: err.Error()}
	var known *restart.Error
	if errors.As(err, &known) {
		out.Details = known.Details
	}
	kit.JSON(w, r, statusOf(err), out)
}

// transactionID is the body's id as text, empty when it is falsy.
func transactionID(r *http.Request) string {
	return jsreq.StringOr(jsreq.Prop(jsreq.Body(r), "id"), "")
}

// restartCancel answers POST /api/settings/restart/cancel with {id}:
// every change of the caller's pending transaction is undone.
func (g *general) restartCancel(w http.ResponseWriter, r *http.Request) {
	result, err := g.d.Restart.Cancel(r.Context(), kit.UserID(r), transactionID(r))
	if err == nil {
		err = g.d.LogAction(r, "settings_restart_canceled", "Canceled restart transaction "+result.ID)
	}
	if err != nil {
		g.refuse(w, r, err)
		return
	}
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: result})
}

// restartConfirm answers POST /api/settings/restart/confirm with {id}:
// the server is restarted for the caller's transaction.
func (g *general) restartConfirm(w http.ResponseWriter, r *http.Request) {
	result, err := g.d.Restart.Confirm(r.Context(), kit.UserID(r), transactionID(r))
	if err == nil {
		err = g.d.LogAction(r, "settings_restart_confirmed", "Confirmed restart transaction "+result.ID())
	}
	if err != nil {
		g.refuse(w, r, err)
		return
	}
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: result})
}

// restartComplete answers POST /api/settings/restart/complete with {id}:
// a transaction that is ready or failed is removed.
func (g *general) restartComplete(w http.ResponseWriter, r *http.Request) {
	result, err := g.d.Restart.Complete(kit.UserID(r), transactionID(r))
	if err != nil {
		kit.Fail(w, r, statusOf(err), err.Error())
		return
	}
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: result})
}

// confirmPending confirms the caller's transaction for the two routes
// that restart after one setting: the answer, or false when the caller
// has none. noneKey is the text of that refusal.
func (g *general) confirmPending(w http.ResponseWriter, r *http.Request, noneKey string) (*restart.Public, string, bool) {
	pending, err := g.d.Restart.GetPending(kit.UserID(r))
	if err == nil && pending == nil {
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, noneKey, nil))
		return nil, "", false
	}
	var result *restart.Public
	if err == nil {
		result, err = g.d.Restart.Confirm(r.Context(), kit.UserID(r), pending.ID())
	}
	if err != nil {
		g.log.Error("Restart error:", err)
		kit.Fail(w, r, statusOf(err), messageOr(err, kit.T(r, "errors.server_error", nil)))
		return nil, "", false
	}
	return result, pending.ID(), true
}

// advancedRestart answers POST /api/settings/advanced/restart with
// {fileId}: it confirms the caller's transaction, whatever file it holds.
func (g *general) advancedRestart(w http.ResponseWriter, r *http.Request) {
	fileID, isText := jsreq.Prop(jsreq.Body(r), "fileId").(string)
	if !isText || fileID == "" {
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "settings.advanced_error_unknown", nil))
		return
	}
	result, id, ok := g.confirmPending(w, r, "settings.advanced_restart_failed")
	if !ok {
		return
	}
	if err := g.d.LogAction(r, "advanced_config_restart", "Confirmed restart transaction ("+fileID+"): "+id); err != nil {
		g.log.Error("Advanced config restart error:", err)
		kit.Fail(w, r, http.StatusInternalServerError, messageOr(err, kit.T(r, "errors.server_error", nil)))
		return
	}
	kit.JSON(w, r, http.StatusOK, answerMessage{Success: true, Data: result, Message: kit.T(r, "settings.advanced_restart_started", nil)})
}

// connectionModeRestart answers POST
// /api/settings/connection-mode/restart: it confirms the caller's
// transaction.
func (g *general) connectionModeRestart(w http.ResponseWriter, r *http.Request) {
	result, id, ok := g.confirmPending(w, r, "settings.connection_mode_not_configurable")
	if !ok {
		return
	}
	if err := g.d.LogAction(r, "connection_mode_restart", "Confirmed restart transaction "+id); err != nil {
		g.log.Error("Connection mode restart error:", err)
		kit.Fail(w, r, http.StatusInternalServerError, messageOr(err, kit.T(r, "errors.server_error", nil)))
		return
	}
	kit.JSON(w, r, http.StatusOK, answerMessage{Success: true, Data: result, Message: kit.T(r, "settings.connection_mode_restart_started", nil)})
}
