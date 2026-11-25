package settings

import (
	"net/http"
	"strconv"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsreq"
)

// consoleSessionsData is Settings, Sign-in, "One session per account":
// the switch, and the limit in force with what decides it ("env" when
// SESSION_MAX_PER_USER is set, which then decides over the switch;
// "setting"; "default"). env_value is SESSION_MAX_PER_USER as set, null
// when it is not.
type consoleSessionsData struct {
	SingleSession bool    `json:"single_session"`
	Limit         int     `json:"limit"`
	Source        string  `json:"source"`
	EnvValue      *string `json:"env_value"`
}

// getConsoleSessions answers GET /api/settings/console-sessions.
func (g *general) getConsoleSessions(w http.ResponseWriter, r *http.Request) {
	limit, err := g.d.Sessions.Limit(r.Context())
	if err != nil {
		g.serverError(w, r, "Read console session settings error", err)
		return
	}
	data := consoleSessionsData{SingleSession: limit.Single, Limit: limit.Max, Source: limit.Source}
	if limit.EnvValue != "" {
		data.EnvValue = &limit.EnvValue
	}
	kit.OK(w, r, data)
}

// setConsoleSessions answers PUT /api/settings/console-sessions with
// {single_session}: true gives every account one session again, so a new
// sign-in ends the account's other one. It applies from the next sign-in;
// sessions open now stay. A body without the value is refused.
func (g *general) setConsoleSessions(w http.ResponseWriter, r *http.Request) {
	body := jsreq.Body(r)
	if refuseNothingToSave(w, r, body, "single_session") {
		return
	}
	on := jsreq.Prop(body, "single_session") == true
	if err := g.d.Sessions.SetSingleSession(r.Context(), on); err != nil {
		g.serverError(w, r, "Save console session settings error", err)
		return
	}
	if err := g.d.LogAction(r, "console_sessions_policy_changed", "One session per account: "+strconv.FormatBool(on)); err != nil {
		g.serverError(w, r, "Save console session settings error", err)
		return
	}
	kit.OK(w, r, nil)
}
