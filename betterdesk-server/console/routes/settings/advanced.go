package settings

import (
	"errors"
	"net/http"

	"github.com/unitronix/betterdesk-server/console/advancedconfig"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/restart"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// The configuration file editor (package console/advancedconfig). These
// files hold secrets: no log line here names their content.

// fileRefusals are the refusals of the editor with the text each answers
// with; an unknown file and a missing file are 404, the others 400.
var fileRefusals = []struct {
	err    error
	key    string
	status int
}{
	{advancedconfig.ErrUnknownFile, "settings.advanced_error_unknown", http.StatusNotFound},
	{advancedconfig.ErrNotFound, "settings.advanced_error_not_found", http.StatusNotFound},
	{advancedconfig.ErrNotAFile, "settings.advanced_error_not_file", http.StatusBadRequest},
	{advancedconfig.ErrTooLarge, "settings.advanced_error_too_large", http.StatusBadRequest},
	{advancedconfig.ErrBinary, "settings.advanced_error_binary", http.StatusBadRequest},
	{advancedconfig.ErrInvalidContent, "settings.advanced_error_invalid", http.StatusBadRequest},
	{advancedconfig.ErrNotWritable, "settings.advanced_error_not_writable", http.StatusBadRequest},
}

// refuseFile answers a refusal of the editor and reports whether err was
// one.
func refuseFile(w http.ResponseWriter, r *http.Request, err error) bool {
	for _, refusal := range fileRefusals {
		if errors.Is(err, refusal.err) {
			kit.Fail(w, r, refusal.status, kit.T(r, refusal.key, nil))
			return true
		}
	}
	return false
}

// listFiles answers GET /api/settings/advanced/files: the files of the
// catalog with what the file system says about each.
func (g *general) listFiles(w http.ResponseWriter, r *http.Request) {
	kit.OK(w, r, g.files.ListFiles())
}

// readFile answers GET /api/settings/advanced/files/{id}: one file with
// its content.
func (g *general) readFile(w http.ResponseWriter, r *http.Request) {
	content, err := g.files.ReadFile(r.PathValue("id"))
	if err != nil {
		if refuseFile(w, r, err) {
			return
		}
		g.serverError(w, r, "Read advanced config file error", err)
		return
	}
	kit.OK(w, r, content)
}

type writtenData struct {
	advancedconfig.Written
	RestartRequired *restart.Public `json:"restartRequired"`
}

type writtenAnswer struct {
	Success         bool            `json:"success"`
	Data            writtenData     `json:"data"`
	Message         string          `json:"message"`
	RestartRequired *restart.Public `json:"restartRequired"`
}

// writeFile answers PUT /api/settings/advanced/files/{id} with {content}.
// A file the server or the console reads at its start goes into the
// caller's restart transaction, with its backup as the way back.
func (g *general) writeFile(w http.ResponseWriter, r *http.Request) {
	written, err := g.files.WriteFile(r.PathValue("id"), jsreq.Prop(jsreq.Body(r), "content"))
	var pending *restart.Public
	if err == nil && written.RequiresRestart != "none" {
		var backup any
		if written.BackupPath != nil {
			backup = *written.BackupPath
		}
		pending, err = g.d.Restart.RegisterChange(kit.UserID(r), restart.Change{
			Key:      "advanced:" + written.ID,
			Label:    "Advanced configuration: " + written.ID,
			Rollback: goapi.NewObject("type", "advanced-file", "fileId", written.ID, "backupPath", backup, "created", written.Created),
		})
	}
	if err == nil {
		details := "Saved advanced config: " + written.Path
		if written.BackupPath != nil && *written.BackupPath != "" {
			details += " (backup: " + *written.BackupPath + ")"
		}
		err = g.d.LogAction(r, "advanced_config_saved", details)
	}
	if err != nil {
		if !refuseFile(w, r, err) {
			g.log.Error("Write advanced config file error:", err)
			kit.Fail(w, r, http.StatusInternalServerError, messageOr(err, kit.T(r, "errors.server_error", nil)))
		}
		return
	}
	kit.JSON(w, r, http.StatusOK, writtenAnswer{
		Success:         true,
		Data:            writtenData{Written: *written, RestartRequired: pending},
		Message:         kit.T(r, "settings.advanced_saved", nil),
		RestartRequired: pending,
	})
}
