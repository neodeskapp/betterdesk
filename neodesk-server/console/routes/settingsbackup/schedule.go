package settingsbackup

import (
	"errors"
	"math"
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/branding"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsreq"
	"github.com/neodeskapp/neodesk/neodesk-server/console/scheduledbackup"
)

// schedule answers GET /api/settings/backup/schedule: the stored schedule
// (a local destination with an empty path) and the state of the last run.
func (h *handlers) schedule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	config, err := h.scheduled.GetConfig(ctx)
	if err != nil {
		h.log.Error("Scheduled backup settings read error:", err)
		kit.Fail(w, r, http.StatusInternalServerError, errText(err))
		return
	}
	status, err := h.scheduled.GetStatus(ctx)
	if err != nil {
		h.log.Error("Scheduled backup settings read error:", err)
		kit.Fail(w, r, http.StatusInternalServerError, errText(err))
		return
	}
	writeData(w, r, `{"schedule":`+scheduledbackup.ConfigJSON(config)+`,"status":`+branding.Stringify(status)+`}`)
}

// scheduleStatus answers GET /api/settings/backup/schedule/status.
func (h *handlers) scheduleStatus(w http.ResponseWriter, r *http.Request) {
	status, err := h.scheduled.GetStatus(r.Context())
	if err != nil {
		kit.Fail(w, r, http.StatusInternalServerError, errText(err))
		return
	}
	writeData(w, r, branding.Stringify(status))
}

// saveSchedule answers PUT /api/settings/backup/schedule.
func (h *handlers) saveSchedule(w http.ResponseWriter, r *http.Request) {
	config, err := h.scheduled.SaveConfig(r.Context(), body(r))
	if errors.Is(err, scheduledbackup.ErrRetention) {
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "settings.backup_tab.retention_error", nil))
		return
	}
	if err != nil {
		kit.Fail(w, r, http.StatusBadRequest, errText(err))
		return
	}
	writeData(w, r, scheduledbackup.ConfigJSON(config))
}

// runSchedule answers POST /api/settings/backup/schedule/run: a scheduled
// backup now, whatever the schedule says.
func (h *handlers) runSchedule(w http.ResponseWriter, r *http.Request) {
	result, err := h.scheduled.CreateScheduledBackup(r.Context())
	if err == nil {
		err = h.d.LogAction(r, "backup_created", "Scheduled full backup created ("+result.Filename+")")
	}
	if err != nil {
		kit.Fail(w, r, http.StatusBadRequest, errText(err))
		return
	}
	data := branding.NewObject()
	data.Set("filename", result.Filename)
	data.Set("sha256", result.SHA256)
	data.Set("validation", result.Validation)
	data.Set("deleted", strings2any(result.Deleted))
	writeData(w, r, branding.Stringify(data))
}

// testDestination answers POST /api/settings/backup/destination/test.
func (h *handlers) testDestination(w http.ResponseWriter, r *http.Request) {
	destination, err := h.scheduled.TestDestination(body(r))
	if err != nil {
		kit.Fail(w, r, http.StatusBadRequest, errText(err))
		return
	}
	data := branding.NewObject()
	data.Set("valid", true)
	data.Set("destination", destination)
	writeData(w, r, branding.Stringify(data))
}

type retentionData struct {
	Count  float64 `json:"count"`
	Source string  `json:"source,omitempty"`
}

// retention answers GET /api/settings/backup/retention: how many update
// backups the updater keeps, from the setting, else from
// BACKUP_RETENTION_COUNT, else 0 (all).
func (h *handlers) retention(w http.ResponseWriter, r *http.Request) {
	value, found, err := h.setting(r, "backup_retention_count")
	if err != nil {
		h.log.Error("Get retention error:", err)
		h.serverError(w, r)
		return
	}
	data := retentionData{Count: 0, Source: "unset"}
	if found && value != "" {
		if n, ok := jsreq.ParseInt(value, 10); ok && finite(n) {
			data = retentionData{Count: n, Source: "database"}
		}
	} else if env := h.getenv("BACKUP_RETENTION_COUNT"); env != "" {
		if n, ok := jsreq.ParseInt(env, 10); ok && finite(n) {
			data = retentionData{Count: n, Source: "environment"}
		}
	}
	kit.OK(w, r, jsNumberJSON{data})
}

// saveRetention answers PUT /api/settings/backup/retention with
// {"count": 0..1000}; 0 keeps every backup.
func (h *handlers) saveRetention(w http.ResponseWriter, r *http.Request) {
	count, ok := jsreq.ParseInt(jsreq.Prop(jsreq.Body(r), "count"), 10)
	if !ok || !finite(count) || count < 0 || count > 1000 {
		kit.Fail(w, r, http.StatusBadRequest, "count must be an integer between 0 and 1000")
		return
	}
	text := jsbind.NumberString(count)
	if _, err := h.d.Store.Exec(r.Context(), h.d.Store.Pick(
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, datetime('now'))
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = datetime('now')`,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, NOW())
		 ON CONFLICT(key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`),
		"backup_retention_count", text); err != nil {
		h.log.Error("Set retention error:", err)
		h.serverError(w, r)
		return
	}
	shown := text
	if count == 0 {
		shown = "unlimited"
	}
	if err := h.d.LogAction(r, "backup_retention_changed", "Backup retention set to "+shown); err != nil {
		h.log.Error("Set retention error:", err)
		h.serverError(w, r)
		return
	}
	kit.OK(w, r, jsNumberJSON{retentionData{Count: count}})
}

// jsNumberJSON writes the retention with its count as JavaScript prints a
// number.
type jsNumberJSON struct{ d retentionData }

func (j jsNumberJSON) MarshalJSON() ([]byte, error) {
	out := `{"count":` + jsbind.Number(j.d.Count).JSON()
	if j.d.Source != "" {
		out += `,"source":` + branding.Stringify(j.d.Source)
	}
	return []byte(out + "}"), nil
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// setting reads a value of the settings table; found is false when the
// key has no row.
func (h *handlers) setting(r *http.Request, key string) (value string, found bool, err error) {
	rows, err := h.d.Store.Query(r.Context(), `SELECT value FROM settings WHERE key = ?`, key)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return "", false, rows.Err()
	}
	if err := rows.Scan(&value); err != nil {
		return "", false, err
	}
	return value, true, rows.Err()
}
