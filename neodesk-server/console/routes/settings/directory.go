package settings

import (
	"encoding/json"
	"math"
	"net/http"
	"reflect"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/authn"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsreq"
)

// The directory (LDAP) and single sign-on (OIDC) settings live in the
// server, which masks the bind password and the client secret in what it
// answers. The routes pass the request body on as it came and the
// server's answer back; the connection tests are made by the server.

// passOn answers with what a read of the server gave: its data, or 500
// with its error or fallback.
func passOn(w http.ResponseWriter, r *http.Request, result *goapi.Object, fallback string) {
	if !goapi.Truthy(result.Get("success")) {
		kit.JSON(w, r, http.StatusInternalServerError, failure{Error: orText(result.Get("error"), fallback)})
		return
	}
	kit.JSON(w, r, http.StatusOK, result.Get("data"))
}

// saved answers a save in the server: 400 with its error or fallback, or
// {success: true} once the audit entry is written.
func (g *general) saved(w http.ResponseWriter, r *http.Request, result *goapi.Object, fallback, action, details, what string) {
	if !goapi.Truthy(result.Get("success")) {
		kit.JSON(w, r, http.StatusBadRequest, failure{Error: orText(result.Get("error"), fallback)})
		return
	}
	if err := g.d.LogAction(r, action, details); err != nil {
		g.serverError(w, r, what, err)
		return
	}
	kit.OK(w, r, nil)
}

// tested answers a connection test of the server: its result, or 200
// {success: false} with its error or fallback.
func tested(w http.ResponseWriter, r *http.Request, result *goapi.Object, fallback string) {
	if !goapi.Truthy(result.Get("success")) {
		kit.JSON(w, r, http.StatusOK, failure{Error: orText(result.Get("error"), fallback)})
		return
	}
	kit.JSON(w, r, http.StatusOK, result.Get("data"))
}

// directoryCall runs a save or a test of the server's directory or single
// sign-on settings. It returns {success: true, data} or {success: false,
// error} with the reason the server gave for a refusal ("LDAP host is
// required"), not only the status of the call.
func (g *general) directoryCall(r *http.Request, method, path string, body any) *goapi.Object {
	resp, err := g.d.GoAPI.Request(r.Context(), method, path, goapi.Body(body))
	if err != nil {
		if reason, ok := goapi.Dig(goapi.AsError(err).Data(), "error").(string); ok && reason != "" {
			return goapi.NewObject("success", false, "error", reason)
		}
		return goapi.NewObject("success", false, "error", err.Error())
	}
	data := resp.Data()
	if o, ok := data.(*goapi.Object); ok && o != nil && o.Has("error") {
		return goapi.NewObject("success", false, "error", o.Get("error"))
	}
	return goapi.NewObject("success", true, "data", data)
}

// settingRefusal is the answer to a refused directory or single sign-on
// setting, in the request's language.
func settingRefusal(r *http.Request, err *auth.SettingError) fieldFailure {
	text := kit.T(r, "settings.signin.check_"+err.Code, kit.Vars("line", err.Line, "text", err.Text))
	return fieldFailure{Error: text, Field: err.Field}
}

// wholeNumberField refuses a number setting that is no whole number from 0
// to max: text, a fraction or a value out of range. A missing or empty
// one means the default.
func wholeNumberField(body any, field, code string, max float64) *auth.SettingError {
	switch v := jsreq.Prop(body, field).(type) {
	case nil:
		return nil
	case float64:
		if v == math.Trunc(v) && v >= 0 && v <= max {
			return nil
		}
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
	}
	return &auth.SettingError{Field: field, Code: code}
}

// decodeSettings reads a request body into the server's settings type;
// false when it does not fit (the server then refuses it as it always
// did).
func decodeSettings(body any, into any) bool {
	raw, err := json.Marshal(body)
	return err == nil && json.Unmarshal(raw, into) == nil
}

// checkLDAP refuses LDAP settings a sign-in or the connection test can't
// use, before they reach the server. Switched off, a save is not checked.
func checkLDAP(body any, forTest bool) *auth.SettingError {
	if err := wholeNumberField(body, "port", "port_range", 65535); err != nil {
		return err
	}
	if err := wholeNumberField(body, "conn_timeout_sec", "timeout_range", 120); err != nil {
		return err
	}
	var cfg auth.LDAPConfig
	if !decodeSettings(body, &cfg) || (!cfg.Enabled && !forTest) {
		return nil
	}
	return auth.CheckLDAPConfig(&cfg, forTest)
}

// getLDAP answers GET /api/settings/ldap.
func (g *general) getLDAP(w http.ResponseWriter, r *http.Request) {
	passOn(w, r, g.d.GoAPI.GetLDAPConfig(r.Context()), "Failed to load LDAP config")
}

// The settings the server keeps for LDAP and OIDC, by their names in JSON.
var (
	ldapFields = jsonFields(auth.LDAPConfig{})
	oidcFields = jsonFields(auth.OIDCConfig{})
)

// jsonFields are the JSON names of the fields of a struct.
func jsonFields(v any) []string {
	var names []string
	t := reflect.TypeOf(v)
	for i := 0; i < t.NumField(); i++ {
		if name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ","); name != "" && name != "-" {
			names = append(names, name)
		}
	}
	return names
}

// refuseNothingToSave answers 400 for a body that is no object or has
// none of fields, which a route that saves settings would otherwise take
// as "set the defaults". It reports whether it answered.
func refuseNothingToSave(w http.ResponseWriter, r *http.Request, body any, fields ...string) bool {
	if obj, ok := body.(*goapi.Object); ok {
		for _, field := range fields {
			if obj.Has(field) {
				return false
			}
		}
	}
	kit.Fail(w, r, http.StatusBadRequest, "No settings to save")
	return true
}

// saveLDAP answers PUT /api/settings/ldap; a body with none of the
// settings is refused.
func (g *general) saveLDAP(w http.ResponseWriter, r *http.Request) {
	if refuseNothingToSave(w, r, jsreq.Body(r), ldapFields...) {
		return
	}
	if err := checkLDAP(jsreq.Body(r), false); err != nil {
		kit.JSON(w, r, http.StatusBadRequest, settingRefusal(r, err))
		return
	}
	result := g.directoryCall(r, http.MethodPut, "/auth/ldap/config", jsreq.Body(r))
	g.saved(w, r, result, "Failed to save LDAP config", "ldap_config_updated", "Updated LDAP configuration", "Save LDAP config error")
}

// testLDAP answers POST /api/settings/ldap/test: the server tries to
// connect with the settings of the body.
func (g *general) testLDAP(w http.ResponseWriter, r *http.Request) {
	if err := checkLDAP(jsreq.Body(r), true); err != nil {
		kit.JSON(w, r, http.StatusOK, settingRefusal(r, err))
		return
	}
	tested(w, r, g.directoryCall(r, http.MethodPost, "/auth/ldap/test", jsreq.Body(r)), "Connection test failed")
}

// getOIDC answers GET /api/settings/oidc.
func (g *general) getOIDC(w http.ResponseWriter, r *http.Request) {
	passOn(w, r, g.d.GoAPI.GetOIDCConfig(r.Context()), "Failed to load OIDC config")
}

// saveOIDC answers PUT /api/settings/oidc. Switched on, single sign-on
// needs a Redirect URL with the path of the callback; a body with none of
// the settings is refused.
func (g *general) saveOIDC(w http.ResponseWriter, r *http.Request) {
	body := jsreq.Body(r)
	if refuseNothingToSave(w, r, body, oidcFields...) {
		return
	}
	if goapi.Truthy(jsreq.Prop(body, "enabled")) {
		redirect, _ := jsreq.Prop(body, "redirect_url").(string)
		if !authn.IsValidOIDCRedirectURL(redirect) {
			kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "settings.oidc_redirect_url_invalid", nil))
			return
		}
	}
	var cfg auth.OIDCConfig
	if decodeSettings(body, &cfg) {
		if err := auth.CheckOIDCConfig(r.Context(), &cfg); err != nil {
			kit.JSON(w, r, http.StatusBadRequest, settingRefusal(r, err))
			return
		}
	}
	result := g.directoryCall(r, http.MethodPut, "/auth/oidc/config", body)
	g.saved(w, r, result, "Failed to save OIDC config", "oidc_config_updated", "Updated OIDC configuration", "Save OIDC config error")
}

// testOIDC answers POST /api/settings/oidc/test: the server fetches the
// discovery document of the issuer in the body.
func (g *general) testOIDC(w http.ResponseWriter, r *http.Request) {
	tested(w, r, g.directoryCall(r, http.MethodPost, "/auth/oidc/test", jsreq.Body(r)), "Discovery test failed")
}
