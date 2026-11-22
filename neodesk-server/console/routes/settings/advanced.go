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
// caller's restart transaction, with its backup as the way back; the
// file is written only once the transaction takes the change, so while
// another operator's change waits the answer is 409 and nothing is
// written.
func (g *general) writeFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	content := jsreq.Prop(jsreq.Body(r), "content")
	var written *advancedconfig.Written
	var pending *restart.Public
	var err error
	if needs, known := g.files.RequiresRestart(id); known && needs != "none" {
		pending, err = g.d.Restart.RegisterChangeAfter(kit.UserID(r), "advanced:"+id, func() (restart.Change, error) {
			var writeErr error
			if written, writeErr = g.files.WriteFile(id, content); writeErr != nil {
				return restart.Change{}, writeErr
			}
			var backup any
			if written.BackupPath != nil {
				backup = *written.BackupPath
			}
			return restart.Change{
				Key:      "advanced:" + written.ID,
				Label:    "Advanced configuration: " + written.ID,
				Rollback: goapi.NewObject("type", "advanced-file", "fileId", written.ID, "backupPath", backup, "created", written.Created),
			}, nil
		})
	} else {
		written, err = g.files.WriteFile(id, content)
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
			status := statusOf(err)
			if status == http.StatusInternalServerError {
				g.log.Error("Write advanced config file error:", err)
			}
			kit.Fail(w, r, status, messageOr(err, kit.T(r, "errors.server_error", nil)))
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
