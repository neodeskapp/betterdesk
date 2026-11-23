package settingsupdates

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/managedconfig"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
	"github.com/neodeskapp/neodesk/neodesk-server/console/restart"
	"github.com/neodeskapp/neodesk/neodesk-server/console/updater"
)

// capabilities answers GET /api/settings/management/capabilities.
func (h *handlers) capabilities(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: h.d.Updater.Capabilities()})
}

// configView is the answer of GET /api/settings/management/config.
type configView struct {
	Keys    []string          `json:"keys"`
	Values  map[string]string `json:"values"`
	EnvPath string            `json:"envPath"`
}

// getConfig answers GET /api/settings/management/config: the allowlist,
// the values the env file sets for its keys (secrets masked) and the env
// file's path.
func (h *handlers) getConfig(w http.ResponseWriter, r *http.Request) {
	paths := h.managed.Paths()
	values, err := managedconfig.ReadEnvFile(paths.EnvPath)
	if err != nil {
		h.log.Error("Management configuration read error:", err)
		kit.JSON(w, r, http.StatusInternalServerError, failure{Error: err.Error()})
		return
	}
	view := configView{Keys: h.managed.AllowedKeys(), Values: map[string]string{}, EnvPath: paths.EnvPath}
	for _, key := range view.Keys {
		if value, ok := values[key]; ok {
			view.Values[key] = managedconfig.Mask(key, value)
		}
	}
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: view})
}

// dryRun is the answer of a save with dryRun.
type dryRun struct {
	DryRun       bool                 `json:"dryRun"`
	Changes      *goapi.Object        `json:"changes"`
	Capabilities updater.Capabilities `json:"capabilities"`
}

// blocked is the answer when the env file cannot be written.
type blocked struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code"`
	Data    any    `json:"data"`
}

// saved is the answer of a save.
type saved struct {
	Success bool      `json:"success"`
	Data    savedData `json:"data"`
	Message string    `json:"message"`
}

type savedData struct {
	EnvPath        string          `json:"envPath"`
	BackupPath     string          `json:"backupPath"`
	Changed        []string        `json:"changed"`
	Values         *goapi.Object   `json:"values"`
	PendingRestart *restart.Public `json:"pendingRestart"`
}

// putConfig answers PUT /api/settings/management/config with the body
// {changes: {KEY: value}, dryRun}: the allowlisted keys are checked,
// written with a backup and registered as a change that needs a restart.
// When the registration fails, the backup goes back.
func (h *handlers) putConfig(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	changes, isObject := changesOf(body.Get("changes"))
	settings, err := h.managed.ValidateFromConsole(changes, isObject)
	if err != nil {
		h.configFailure(w, r, err)
		return
	}
	capabilities := h.d.Updater.Capabilities()
	if dry := body.Get("dryRun"); isTrue(dry) {
		masked := goapi.NewObject()
		for _, s := range settings {
			masked.Set(s.Key, managedconfig.Mask(s.Key, s.Value))
		}
		kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: dryRun{DryRun: true, Changes: masked, Capabilities: capabilities}})
		return
	}
	if !capabilities.Groups.Config.Ready {
		kit.JSON(w, r, http.StatusConflict, blocked{
			Error: "Configuration path is not writable", Code: "CONFIG_CAPABILITY_BLOCKED",
			Data: map[string]any{"capabilities": capabilities},
		})
		return
	}
	applied, err := h.managed.Apply(settings)
	if err != nil {
		h.configFailure(w, r, err)
		return
	}
	options := goapi.NewObject()
	p := h.managed.Paths()
	for _, kv := range [][2]string{{"consolePath", p.ConsolePath}, {"serverPath", p.ServerPath}, {"dataPath", p.DataPath},
		{"backupPath", p.BackupPath}, {"envPath", p.EnvPath}, {"examplePath", p.ExamplePath}} {
		options.Set(kv[0], kv[1])
	}
	pending, err := h.d.Restart.RegisterChange(kit.UserID(r), restart.Change{
		Key:      "managed-env:" + strings.Join(applied.Changed, ","),
		Label:    "Managed configuration: " + strings.Join(applied.Changed, ", "),
		Rollback: goapi.NewObject("type", "env-config", "backupPath", applied.BackupPath, "options", options),
	})
	if err != nil {
		if restoreErr := h.managed.RestoreBackup(applied.BackupPath); restoreErr != nil {
			h.log.Error("Managed configuration rollback error:", restoreErr)
		}
		h.configFailure(w, r, err)
		return
	}
	_ = h.d.LogAction(r, "managed_config_saved", "Saved allowlisted environment keys: "+strings.Join(applied.Changed, ", "))
	values := goapi.NewObject()
	for _, s := range applied.Values {
		values.Set(s.Key, s.Value)
	}
	kit.JSON(w, r, http.StatusOK, saved{
		Success: true,
		Data: savedData{
			EnvPath: applied.EnvPath, BackupPath: applied.BackupPath, Changed: applied.Changed, Values: values, PendingRestart: pending,
		},
		Message: "Configuration saved; service restart confirmation is required",
	})
}

// configFailure answers a save that failed: 400 for a change the
// configuration refuses, the restart transaction's own status for its
// refusal (409 restart_transaction_owned), 500 for the rest.
func (h *handlers) configFailure(w http.ResponseWriter, r *http.Request, err error) {
	message := err.Error()
	status := http.StatusInternalServerError
	var invalid *managedconfig.ValidationError
	var refusal *restart.Error
	switch {
	case errors.As(err, &invalid):
		status = http.StatusBadRequest
	case errors.As(err, &refusal):
		message, status = refusal.Message, refusal.StatusCode()
	}
	if message == "" {
		message = "configuration_write_failed"
	}
	kit.JSON(w, r, status, failure{Error: message})
}

// isTrue is value === true || value === 'true'.
func isTrue(v middleware.Value) bool {
	switch v.Kind() {
	case middleware.KindBool:
		b, _ := v.Bool()
		return b
	case middleware.KindString:
		s, _ := v.Str()
		return s == "true"
	}
	return false
}

// changesOf reads the changes of a save as Object.entries saw them: the
// members of an object (not an array, not null), keys that are array
// indexes first and in their numeric order, the others in the order of
// the body. A string, a number or a boolean is a value; anything
// else is passed on to be refused.
func changesOf(v middleware.Value) ([]managedconfig.Change, bool) {
	obj, ok := v.Object()
	if !ok {
		return nil, false
	}
	var out []managedconfig.Change
	for _, key := range jsKeyOrder(obj.Keys()) {
		member := obj.Get(key)
		var value any
		switch member.Kind() {
		case middleware.KindString:
			value, _ = member.Str()
		case middleware.KindNumber:
			if f, ok := member.Float(); ok {
				value = f
			}
		case middleware.KindBool:
			value, _ = member.Bool()
		}
		out = append(out, managedconfig.Change{Key: key, Value: value})
	}
	return out, true
}

// jsKeyOrder orders keys as JavaScript lists an object's own keys:
// array indexes (0 to 2^32-2 written without leading zeros) first, in
// numeric order, then the others as they came.
func jsKeyOrder(keys []string) []string {
	var indexes, others []string
	for _, key := range keys {
		if isArrayIndex(key) {
			indexes = append(indexes, key)
		} else {
			others = append(others, key)
		}
	}
	sort.SliceStable(indexes, func(i, j int) bool {
		a, _ := strconv.ParseUint(indexes[i], 10, 64)
		b, _ := strconv.ParseUint(indexes[j], 10, 64)
		return a < b
	})
	return append(indexes, others...)
}

func isArrayIndex(key string) bool {
	if key == "" || (len(key) > 1 && key[0] == '0') {
		return false
	}
	n, err := strconv.ParseUint(key, 10, 64)
	return err == nil && n < 1<<32-1
}
