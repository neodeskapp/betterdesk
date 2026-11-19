package settings

import (
	"math"
	"net/http"
	"os"
	"strconv"

	"github.com/unitronix/betterdesk-server/console/advancedconfig"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
)

// Device scope, availability history, enrollment and the sessions of
// RustDesk clients.

type scopeData struct {
	Mode string `json:"mode"`
}

// getDeviceScope answers GET /api/settings/device-scope: whether devices
// nobody was given are hidden from accounts that are not administrators
// ("restricted") or not ("open").
func (g *general) getDeviceScope(w http.ResponseWriter, r *http.Request) {
	stored, _, err := getSetting(r.Context(), g.d.Store, "device_scope_default")
	if err != nil {
		g.serverError(w, r, "Get device scope setting error", err)
		return
	}
	mode := "open"
	if stored != "" && jsbind.ToLower(stored) == "restricted" {
		mode = "restricted"
	}
	kit.OK(w, r, scopeData{Mode: mode})
}

// setDeviceScope answers POST /api/settings/device-scope with {mode}: the
// mode in any letter case, "open" when it is missing or empty.
func (g *general) setDeviceScope(w http.ResponseWriter, r *http.Request) {
	mode := jsbind.ToLower(jsreq.StringOr(jsreq.Prop(jsreq.Body(r), "mode"), "open"))
	if mode != "open" && mode != "restricted" {
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "settings.device_scope_invalid", nil))
		return
	}
	if err := setSetting(r.Context(), g.d.Store, "device_scope_default", mode); err != nil {
		g.serverError(w, r, "Set device scope setting error", err)
		return
	}
	g.d.Access.InvalidateDeviceScopeDefault()
	if err := g.d.LogAction(r, "device_scope_default_updated", "Device scope default: "+mode); err != nil {
		g.serverError(w, r, "Set device scope setting error", err)
		return
	}
	kit.OK(w, r, scopeData{Mode: mode})
}

const (
	availabilityEnabledKey   = "availability_history_enabled"
	availabilityRetentionKey = "availability_history_retention_days"
	defaultRetentionDays     = 30
	maxRetentionDays         = 3650
)

type availabilityData struct {
	Enabled       bool    `json:"enabled"`
	RetentionDays float64 `json:"retention_days"`
}

// configValue is "res.value ?? res.data?.value" for the answer of
// GetConfig when it did not fail, else fallback.
func configValue(res *goapi.Object, fallback any) any {
	if res == nil || res.Get("success") == false {
		return fallback
	}
	if v := res.Get("value"); v != nil {
		return v
	}
	return goapi.Dig(res, "data", "value")
}

// parseTruthy reads a switch the way the availability history did: 1,
// true, y, yes and on, in any case and with space around.
func parseTruthy(v any) bool {
	s := ""
	if v != nil {
		s = jsreq.String(v)
	}
	switch jsbind.ToLower(jsbind.Trim(s)) {
	case "1", "true", "y", "yes", "on":
		return true
	}
	return false
}

// getAvailabilityHistory answers GET /api/settings/availability-history:
// whether the server records when devices come and go, and for how many
// days (the server keeps both).
func (g *general) getAvailabilityHistory(w http.ResponseWriter, r *http.Request) {
	enabled := g.d.GoAPI.GetConfig(r.Context(), availabilityEnabledKey)
	retention := g.d.GoAPI.GetConfig(r.Context(), availabilityRetentionKey)
	days := float64(defaultRetentionDays)
	if parsed, ok := jsreq.ParseInt(configValue(retention, nil), 10); ok && !math.IsInf(parsed, 0) && parsed > 0 {
		days = math.Min(parsed, maxRetentionDays)
	}
	kit.OK(w, r, availabilityData{Enabled: parseTruthy(configValue(enabled, "")), RetentionDays: days})
}

// setAvailabilityHistory answers POST /api/settings/availability-history
// with {enabled, retention_days}: days below 1 or no number are 30, more
// than 3650 are 3650.
func (g *general) setAvailabilityHistory(w http.ResponseWriter, r *http.Request) {
	body := jsreq.Body(r)
	enabled := goapi.Truthy(jsreq.Prop(body, "enabled"))
	days, ok := jsreq.ParseInt(jsreq.Prop(body, "retention_days"), 10)
	if !ok || math.IsInf(days, 0) || days < 1 {
		days = defaultRetentionDays
	}
	days = math.Min(days, maxRetentionDays)

	if res := g.d.GoAPI.SetConfig(r.Context(), availabilityEnabledKey, strconv.FormatBool(enabled)); res.Get("success") == false {
		kit.JSON(w, r, http.StatusBadGateway, failure{Error: orText(res.Get("error"), kit.T(r, "errors.server_error", nil))})
		return
	}
	daysText := jsreq.String(days)
	if res := g.d.GoAPI.SetConfig(r.Context(), availabilityRetentionKey, daysText); res.Get("success") == false {
		kit.JSON(w, r, http.StatusBadGateway, failure{Error: orText(res.Get("error"), kit.T(r, "errors.server_error", nil))})
		return
	}
	details := "Availability history: enabled=" + strconv.FormatBool(enabled) + ", retention_days=" + daysText
	if err := g.d.LogAction(r, "availability_history_updated", details); err != nil {
		g.serverError(w, r, "Set availability history setting error", err)
		return
	}
	kit.OK(w, r, availabilityData{Enabled: enabled, RetentionDays: days})
}

const (
	enrollmentRichKey      = "enrollment_rich_approve"
	enrollmentTagPickerKey = "enrollment_tag_picker"
)

// parseBoolSetting reads a stored switch: unset or empty is on, else on
// for 1, true and yes in any case.
func parseBoolSetting(value string, set bool) bool {
	if !set || value == "" {
		return true
	}
	switch jsbind.ToLower(value) {
	case "1", "true", "yes":
		return true
	}
	return false
}

type enrollmentData struct {
	Mode            any  `json:"mode"`
	RequireApproval bool `json:"require_approval"`
	RichApprove     bool `json:"rich_approve"`
	TagPicker       bool `json:"tag_picker"`
}

type enrollmentWithPending struct {
	enrollmentData
	PendingCount any `json:"pending_count"`
}

// enrollmentToggles reads the two switches of the approval dialog.
func (g *general) enrollmentToggles(r *http.Request) (rich, tags bool, err error) {
	value, set, err := getSetting(r.Context(), g.d.Store, enrollmentRichKey)
	if err != nil {
		return false, false, err
	}
	rich = parseBoolSetting(value, set)
	value, set, err = getSetting(r.Context(), g.d.Store, enrollmentTagPickerKey)
	if err != nil {
		return false, false, err
	}
	return rich, parseBoolSetting(value, set), nil
}

// modeOf is "result.success && result.data?.mode ? result.data.mode :
// fallback" for the answer of GetEnrollmentMode.
func modeOf(result *goapi.Object, fallback any) any {
	if goapi.Truthy(result.Get("success")) {
		if mode := goapi.Dig(result, "data", "mode"); goapi.Truthy(mode) {
			return mode
		}
	}
	return fallback
}

// getEnrollment answers GET /api/settings/enrollment: the server's
// enrollment mode, the two switches of the approval dialog and the number
// of devices that wait.
func (g *general) getEnrollment(w http.ResponseWriter, r *http.Request) {
	mode := modeOf(g.d.GoAPI.GetEnrollmentMode(r.Context()), "open")
	rich, tags, err := g.enrollmentToggles(r)
	if err != nil {
		g.serverError(w, r, "Get enrollment settings error", err)
		return
	}
	var pendingCount any = float64(0)
	if pending := g.d.GoAPI.GetEnrollmentPending(r.Context()); goapi.Truthy(pending.Get("success")) {
		pendingCount = orAny(pending.Get("count"), float64(0))
	}
	kit.OK(w, r, enrollmentWithPending{
		enrollmentData: enrollmentData{Mode: mode, RequireApproval: mode == "managed", RichApprove: rich, TagPicker: tags},
		PendingCount:   pendingCount,
	})
}

// orAny is "value || fallback".
func orAny(value, fallback any) any {
	if goapi.Truthy(value) {
		return value
	}
	return fallback
}

// setEnrollment answers PUT /api/settings/enrollment with {mode,
// require_approval, rich_approve, tag_picker}, each optional:
// require_approval true means "managed", false means "open" unless another
// mode than "managed" is given.
func (g *general) setEnrollment(w http.ResponseWriter, r *http.Request) {
	body, _ := jsreq.Body(r).(*goapi.Object)
	mode, hasMode := body.Get("mode"), body.Has("mode")
	switch approval := body.Get("require_approval"); {
	case approval == true:
		mode, hasMode = "managed", true
	case approval == false && (!goapi.Truthy(mode) || mode == "managed"):
		mode, hasMode = "open", true
	}
	if hasMode {
		if mode != "open" && mode != "managed" && mode != "locked" {
			kit.Fail(w, r, http.StatusBadRequest, "Invalid enrollment mode")
			return
		}
		if result := g.d.GoAPI.SetEnrollmentMode(r.Context(), mode); !goapi.Truthy(result.Get("success")) || result.Has("status") {
			kit.JSON(w, r, http.StatusInternalServerError, failure{Error: orText(result.Get("error"), "Failed to set enrollment mode")})
			return
		}
	}
	for _, toggle := range []struct{ field, key string }{{"rich_approve", enrollmentRichKey}, {"tag_picker", enrollmentTagPickerKey}} {
		if !body.Has(toggle.field) {
			continue
		}
		if err := setSetting(r.Context(), g.d.Store, toggle.key, strconv.FormatBool(goapi.Truthy(body.Get(toggle.field)))); err != nil {
			g.serverError(w, r, "Set enrollment settings error", err)
			return
		}
	}
	shown := "unchanged"
	if goapi.Truthy(mode) {
		shown = jsreq.String(mode)
	}
	if err := g.d.LogAction(r, "enrollment_settings_changed", "Enrollment settings updated (mode="+shown+")"); err != nil {
		g.serverError(w, r, "Set enrollment settings error", err)
		return
	}
	var fallback any = "open"
	if goapi.Truthy(mode) {
		fallback = mode
	}
	current := modeOf(g.d.GoAPI.GetEnrollmentMode(r.Context()), fallback)
	rich, tags, err := g.enrollmentToggles(r)
	if err != nil {
		g.serverError(w, r, "Set enrollment settings error", err)
		return
	}
	kit.OK(w, r, enrollmentData{Mode: current, RequireApproval: current == "managed", RichApprove: rich, TagPicker: tags})
}

// clampSessionDays is clampClientSessionDays: whole days from 1 to 365,
// fallback for anything that is no number or below 1.
func clampSessionDays(value any, fallback float64) float64 {
	n, ok := jsreq.ParseInt(value, 10)
	if !ok || math.IsInf(n, 0) || n < 1 {
		return fallback
	}
	return math.Min(n, 365)
}

// parseSessionBool is parseClientSessionBool.
func parseSessionBool(value any, fallback bool) bool {
	if value == nil || value == "" {
		return fallback
	}
	switch jsbind.ToLower(jsreq.String(value)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return fallback
}

type clientSessionsData struct {
	ExpiryDays float64 `json:"expiry_days"`
	Sliding    bool    `json:"sliding"`
	MaxDays    float64 `json:"max_days"`
}

// sessionConfig is the value of a GetConfig answer, the empty text when
// the call failed.
func sessionConfig(result *goapi.Object) any {
	if goapi.Truthy(result.Get("success")) {
		return goapi.Dig(result, "data", "value")
	}
	return ""
}

// getClientSessions answers GET /api/settings/client-sessions: how long
// the sign-in of a RustDesk client lasts (the server keeps the values).
func (g *general) getClientSessions(w http.ResponseWriter, r *http.Request) {
	expiry := g.d.GoAPI.GetConfig(r.Context(), "client_session_expiry_days")
	sliding := g.d.GoAPI.GetConfig(r.Context(), "client_session_sliding")
	maxDays := g.d.GoAPI.GetConfig(r.Context(), "client_session_max_days")
	kit.OK(w, r, clientSessionsData{
		ExpiryDays: clampSessionDays(sessionConfig(expiry), 7),
		Sliding:    parseSessionBool(sessionConfig(sliding), true),
		MaxDays:    clampSessionDays(sessionConfig(maxDays), 30),
	})
}

// setClientSessions answers PUT /api/settings/client-sessions with
// {expiry_days, sliding, max_days}. The server keeps the values; the
// expiry is also written into the console's .env when that file exists
// (a .env that cannot be written is only logged), and the next client
// token lasts that long.
func (g *general) setClientSessions(w http.ResponseWriter, r *http.Request) {
	body := jsreq.Body(r)
	expiryDays := clampSessionDays(jsreq.Prop(body, "expiry_days"), 7)
	maxDays := clampSessionDays(jsreq.Prop(body, "max_days"), 30)
	sliding := jsreq.Prop(body, "sliding") != false
	results := []*goapi.Object{
		g.d.GoAPI.SetConfig(r.Context(), "client_session_expiry_days", jsreq.String(expiryDays)),
		g.d.GoAPI.SetConfig(r.Context(), "client_session_sliding", strconv.FormatBool(sliding)),
		g.d.GoAPI.SetConfig(r.Context(), "client_session_max_days", jsreq.String(maxDays)),
	}
	for _, result := range results {
		if !goapi.Truthy(result.Get("success")) {
			kit.JSON(w, r, http.StatusInternalServerError, failure{Error: orText(result.Get("error"), "Failed to save client session settings")})
			return
		}
	}
	g.writeExpiryToEnv(jsreq.String(expiryDays))
	if g.d.Authn != nil {
		g.d.Authn.SetTokenExpiryDays(jsreq.String(expiryDays))
	}
	details := "RustDesk client sessions: " + jsreq.String(expiryDays) + "d sliding=" + strconv.FormatBool(sliding) + " max=" + jsreq.String(maxDays) + "d"
	if err := g.d.LogAction(r, "client_session_settings_changed", details); err != nil {
		g.serverError(w, r, "Save client session settings error", err)
		return
	}
	kit.OK(w, r, nil)
}

// writeExpiryToEnv puts API_TOKEN_EXPIRY_DAYS into the console's .env
// when the file exists.
func (g *general) writeExpiryToEnv(days string) {
	path := g.d.Config.EnvPath
	if path == "" {
		return
	}
	if _, err := os.Stat(path); err != nil {
		return
	}
	data, err := os.ReadFile(path)
	if err == nil {
		content := serverinfo.UpsertEnvKey(advancedconfig.DecodeUTF8(data), "API_TOKEN_EXPIRY_DAYS", days)
		err = os.WriteFile(path, []byte(content), 0o600)
	}
	if err != nil {
		g.log.Warn("Client sessions: could not update .env:", err)
	}
}
