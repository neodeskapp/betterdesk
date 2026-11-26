package settings

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"

	"github.com/neodeskapp/neodesk/neodesk-server/console/authn"
	"github.com/neodeskapp/neodesk/neodesk-server/console/billingclock"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/serverinfo"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
)

// GET /api/panel/settings/sources says, for each server setting the
// settings pages show, where the value in force comes from, so a page can
// say "Set here", "Set in the environment (ENROLLMENT_MODE)" or "Built-in
// default" next to it. Every entry is built from the code that resolves
// the value (the getters of this package, the access rules, the mail,
// session, sign-in, clock and server info services), not from a second
// list of defaults.

// The value sources of an entry.
const (
	// sourceStored: saved in the console (or the server's database).
	sourceStored = "stored"
	// sourceEnvironment: an environment variable, the environment file, a
	// systemd unit or a compose file sets it (env_var names it).
	sourceEnvironment = "environment"
	// sourceDefault: nothing sets it, the built-in default applies.
	sourceDefault = "default"
)

// defaultEnrollmentMode is the mode when the server says none.
const defaultEnrollmentMode = "open"

// settingSource is one entry of the answer.
type settingSource struct {
	ValueSource string `json:"value_source"`
	// EnvVar is the variable that sets it or would set it.
	EnvVar string `json:"env_var,omitempty"`
	// Locked says the environment wins: the console can't change it.
	Locked bool `json:"locked"`
	// Default is the built-in default as the server applies it, left out
	// where there is none.
	Default any `json:"default,omitempty"`
}

// sources answers GET /api/panel/settings/sources (server.config): an
// object of entries by setting key, "<area>.<field>" with the field's
// name in its own route's answer. A part whose source can't be read (the
// API server does not answer) is left out rather than guessed.
func (g *general) sources(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := map[string]settingSource{}
	g.enrollmentSources(ctx, out)
	if err := g.deviceScopeSources(ctx, out); err != nil {
		g.serverError(w, r, "Settings sources error", err)
		return
	}
	g.serverConfigSources(ctx, out)
	if err := g.consoleSessionSources(ctx, out); err != nil {
		g.serverError(w, r, "Settings sources error", err)
		return
	}
	g.publicEndpointSources(out)
	if err := g.connectionModeSources(out); err != nil {
		g.serverError(w, r, "Settings sources error", err)
		return
	}
	g.smtpSources(ctx, out)
	if err := g.clockSources(out); err != nil {
		g.serverError(w, r, "Settings sources error", err)
		return
	}
	kit.OK(w, r, out)
}

// asFields is a settings struct as its JSON fields, the defaults of an
// area by the fields' names.
func asFields(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	return out, json.Unmarshal(raw, &out)
}

func (g *general) getenv(name string) string {
	if g.d.Config == nil || g.d.Config.Getenv == nil {
		return ""
	}
	return g.d.Config.Getenv(name)
}

// enrollmentSources: the server resolves the mode at its start
// (config.ResolveEnrollmentMode) and its read says where it came from.
func (g *general) enrollmentSources(ctx context.Context, out map[string]settingSource) {
	res := g.d.GoAPI.GetEnrollmentMode(ctx)
	if !goapi.Truthy(res.Get("success")) {
		return
	}
	src := settingSource{EnvVar: "ENROLLMENT_MODE", Default: defaultEnrollmentMode}
	switch source, _ := goapi.Dig(res, "data", "source").(string); {
	case source == "environment" || (source == "" && goapi.Truthy(goapi.Dig(res, "data", "env_override"))):
		src.ValueSource, src.Locked = sourceEnvironment, true
	case source == "configuration":
		// nothing is saved: the mode in force is the server's default
		src.ValueSource = sourceDefault
		if mode, ok := goapi.Dig(res, "data", "mode").(string); ok && mode != "" {
			src.Default = mode
		}
	default:
		src.ValueSource = sourceStored
	}
	out["enrollment.mode"] = src
}

// deviceScopeSources: the access rules' default (the saved setting, else
// DEVICE_SCOPE_DEFAULT).
func (g *general) deviceScopeSources(ctx context.Context, out map[string]settingSource) error {
	_, stored, err := g.d.Access.DeviceScopeDefault(ctx)
	if err != nil {
		return err
	}
	src := settingSource{ValueSource: sourceDefault, EnvVar: "DEVICE_SCOPE_DEFAULT", Default: scopeMode(false)}
	switch {
	case stored:
		src.ValueSource = sourceStored
	case g.getenv("DEVICE_SCOPE_DEFAULT") != "":
		src.ValueSource = sourceEnvironment
	}
	out["device_scope.mode"] = src
	return nil
}

// serverConfigSources: the settings the server keeps in its configuration,
// read as their getters read them. A value the getter would not take is
// none, so the default applies.
func (g *general) serverConfigSources(ctx context.Context, out map[string]settingSource) {
	saved := func(key string, takes func(any) bool) (string, bool) {
		res := g.d.GoAPI.GetConfig(ctx, key)
		if !goapi.Truthy(res.Get("success")) {
			return "", false
		}
		if takes(sessionConfig(res)) {
			return sourceStored, true
		}
		return sourceDefault, true
	}
	days := func(v any) bool { _, ok := sessionDays(v); return ok }
	flag := func(v any) bool { _, ok := sessionBool(v); return ok }
	retention := func(v any) bool { _, ok := retentionDays(v); return ok }
	for _, e := range []struct {
		key, config string
		takes       func(any) bool
		def         any
	}{
		{"client_sessions.sliding", "client_session_sliding", flag, defaultSessionSliding},
		{"client_sessions.max_days", "client_session_max_days", days, float64(defaultSessionMaxDays)},
		{"availability_history.enabled", availabilityEnabledKey, savedSwitch, false},
		{"availability_history.retention_days", availabilityRetentionKey, retention, float64(defaultRetentionDays)},
		{"two_factor.required", authn.RequireTwoFactorKey, flag, defaultTwoFactorRequired},
	} {
		if source, ok := saved(e.config, e.takes); ok {
			out[e.key] = settingSource{ValueSource: source, Default: e.def}
		}
	}

	// The session duration: the saved one, else API_TOKEN_EXPIRY_DAYS, as
	// a token is issued (authn.TokenLifetimeDays)
	expiry := settingSource{ValueSource: sourceDefault, EnvVar: authn.TokenExpiryEnv, Default: float64(defaultSessionExpiryDays)}
	var stored bool
	if g.d.Authn != nil {
		_, stored = g.d.Authn.TokenLifetimeSaved(ctx)
	} else if source, ok := saved(authn.SessionDurationKey, days); ok {
		stored = source == sourceStored
	} else {
		return
	}
	switch {
	case stored:
		expiry.ValueSource = sourceStored
	case g.getenv(authn.TokenExpiryEnv) != "":
		expiry.ValueSource = sourceEnvironment
	}
	out["client_sessions.expiry_days"] = expiry
}

// consoleSessionSources: SESSION_MAX_PER_USER decides over "One session
// per account" (session.Manager.Limit).
func (g *general) consoleSessionSources(ctx context.Context, out map[string]settingSource) error {
	if g.d.Sessions == nil {
		return nil
	}
	limit, err := g.d.Sessions.Limit(ctx)
	if err != nil {
		return err
	}
	_, saved, err := g.d.Sessions.SingleSessionSaved(ctx)
	if err != nil {
		return err
	}
	src := settingSource{ValueSource: sourceDefault, EnvVar: "SESSION_MAX_PER_USER", Default: false}
	switch {
	case limit.Source == session.LimitFromEnv:
		src.ValueSource, src.Locked = sourceEnvironment, true
	case saved:
		src.ValueSource = sourceStored
	}
	out["console_sessions.single_session"] = src
	return nil
}

// publicEndpointSources: serverinfo's lookup, locked where
// PUBLIC_ENDPOINTS_ENV_OVERRIDE makes the environment win.
func (g *general) publicEndpointSources(out map[string]settingSource) {
	locked := map[string]bool{}
	for _, field := range g.d.ServerInfo.EnvLockedEndpoints() {
		locked[field] = true
	}
	for field, source := range g.d.ServerInfo.PublicEndpointSources() {
		src := settingSource{ValueSource: sourceDefault, EnvVar: serverinfo.PublicEndpointEnvVars[field], Locked: locked[field]}
		switch source {
		case serverinfo.SourceSaved:
			src.ValueSource = sourceStored
		case serverinfo.SourceEnv:
			src.ValueSource = sourceEnvironment
		}
		out["public_endpoints."+field] = src
	}
}

// connectionModeSources: where GetConnectionMode reads the settings. The
// server keeps them (go-api): stored. A unit or a compose file sets them:
// environment where it has the variable, the default where not; the
// console writes that file, so nothing is locked.
func (g *general) connectionModeSources(out map[string]settingSource) error {
	source, set, err := g.d.ServerInfo.ConnectionModeSources()
	if err != nil {
		return err
	}
	defaults, err := asFields(serverinfo.DefaultConnectionMode())
	if err != nil {
		return err
	}
	for field, vars := range serverinfo.ConnectionModeEnvVars {
		src := settingSource{ValueSource: sourceDefault, Default: defaults[field]}
		switch {
		case source == serverinfo.ConnectionSourceGoAPI:
			src.ValueSource = sourceStored
		case source == serverinfo.ConnectionSourceSystemd || source == serverinfo.ConnectionSourceDocker:
			src.EnvVar = vars[0]
			if set[field] {
				src.ValueSource = sourceEnvironment
			}
		}
		out["connection_mode."+field] = src
	}
	return nil
}

// smtpSources: the stored configuration wins over the SMTP_ variables
// (mail.Service.LoadSMTPConfig); the console can save over the
// environment, so nothing is locked.
func (g *general) smtpSources(ctx context.Context, out map[string]settingSource) {
	if g.d.Mail == nil {
		return
	}
	valueSource := sourceDefault
	switch g.d.Mail.SMTPSource(ctx) {
	case mail.SMTPFromStore:
		valueSource = sourceStored
	case mail.SMTPFromEnv:
		valueSource = sourceEnvironment
	}
	defaults := map[string]any{"port": float64(mail.DefaultSMTPPort), "secure": mail.DefaultSMTPSecure, "from": mail.DefaultSMTPFrom}
	for field, envVar := range mail.SMTPEnvVars {
		out["smtp."+field] = settingSource{ValueSource: valueSource, EnvVar: envVar, Default: defaults[field]}
	}
}

// clockSources: the clock settings live in the console's environment
// file, which the console writes (not locked), and in a container in the
// server's database (stored). The clock service is the one the billing
// routes build (commercialization.NewClock), without the service
// definition a save needs.
func (g *general) clockSources(out map[string]settingSource) error {
	opts := billingclock.Options{GoAPI: g.d.GoAPI, Linux: runtime.GOOS == "linux"}
	if cfg := g.d.Config; cfg != nil {
		opts.EnvPath, opts.Docker, opts.Getenv = cfg.EnvPath, cfg.Docker, cfg.Getenv
	}
	clock := billingclock.New(opts)
	throughServer, set, err := clock.SetInFile()
	if err != nil {
		return err
	}
	defaults, err := asFields(clock.Defaults())
	if err != nil {
		return err
	}
	for field, envVar := range billingclock.EnvVars {
		src := settingSource{ValueSource: sourceDefault, EnvVar: envVar, Default: defaults[field]}
		switch {
		case throughServer:
			src.ValueSource, src.EnvVar = sourceStored, ""
		case set[field]:
			src.ValueSource = sourceEnvironment
		}
		out["clock."+field] = src
	}
	return nil
}
