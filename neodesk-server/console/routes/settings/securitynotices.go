package settings

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsreq"
	"github.com/neodeskapp/neodesk/neodesk-server/console/securitymail"
)

// Settings, Sign-in, Security notices: which mails a person gets about
// their own account (securitymail). The server keeps one switch per
// notice in its configuration; a switch never saved is on.

// noticeFields are the members of the answer and the body, by switch.
var noticeFields = []struct{ field, key string }{
	{"new_signin", securitymail.KeyNewSignIn},
	{"password_changed", securitymail.KeyPasswordChanged},
	{"two_factor_changed", securitymail.KeyTwoFactorChanged},
	{"email_changed", securitymail.KeyEmailChanged},
}

// getSecurityNotices answers GET /api/settings/security-notices:
// {new_signin, password_changed, two_factor_changed, email_changed}, each
// true or false.
func (g *general) getSecurityNotices(w http.ResponseWriter, r *http.Request) {
	out := goapi.NewObject()
	for _, f := range noticeFields {
		out.Set(f.field, parseSessionBool(sessionConfig(g.d.GoAPI.GetConfig(r.Context(), f.key)), true))
	}
	kit.OK(w, r, out)
}

// setSecurityNotices answers PUT /api/settings/security-notices with any
// of the members of the answer, each true or false; the others stay as
// they are. A body without one, or with a value that is not true or
// false, is refused.
func (g *general) setSecurityNotices(w http.ResponseWriter, r *http.Request) {
	body := jsreq.Body(r)
	fields := make([]string, len(noticeFields))
	for i, f := range noticeFields {
		fields[i] = f.field
	}
	if refuseNothingToSave(w, r, body, fields...) {
		return
	}
	obj, _ := body.(*goapi.Object)
	var pairs []configPair
	var changed []string
	for _, f := range noticeFields {
		if !obj.Has(f.field) {
			continue
		}
		on, isBool := obj.Get(f.field).(bool)
		if !isBool {
			kit.Fail(w, r, http.StatusBadRequest, "Invalid value for "+f.field)
			return
		}
		pairs = append(pairs, configPair{f.key, strconv.FormatBool(on)})
		changed = append(changed, f.field+"="+strconv.FormatBool(on))
	}
	if result, ok := g.setConfigs(r, pairs); !ok {
		kit.JSON(w, r, http.StatusInternalServerError, failure{Error: orText(result.Get("error"), "Failed to save the security notices")})
		return
	}
	if err := g.d.LogAction(r, "security_notices_changed", "Security notices: "+strings.Join(changed, ", ")); err != nil {
		g.serverError(w, r, "Save security notices error", err)
		return
	}
	kit.OK(w, r, nil)
}
