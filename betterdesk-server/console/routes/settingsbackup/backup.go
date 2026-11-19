package settingsbackup

import (
	"errors"
	"net/http"
	"strings"

	"github.com/unitronix/betterdesk-server/console/backup"
	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// stats answers GET /api/settings/backup/stats: what a full backup would
// hold.
func (h *handlers) stats(w http.ResponseWriter, r *http.Request) {
	kit.OK(w, r, h.backups.FullBackupStats(r.Context()))
}

// download answers GET /api/settings/backup with the full backup as a
// .tar.gz file, or with ?format=json the settings snapshot.
func (h *handlers) download(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if format, _ := jsreq.Query(r, "format").(string); format == "json" {
		snapshot, err := h.backups.CreateBackup(ctx)
		if err != nil {
			h.log.Error("Backup download error:", err)
			h.serverError(w, r)
			return
		}
		text, err := backup.SnapshotJSON(snapshot)
		if err != nil {
			h.log.Error("Backup download error:", err)
			h.serverError(w, r)
			return
		}
		filename := "betterdesk-config-" + store10(snapshot) + ".json"
		size := backup.ToFixed(int64(backup.UTF16Len(string(text))), 1024, 1)
		if err := h.d.LogAction(r, "backup_created", "Config snapshot downloaded ("+size+" KB)"); err != nil {
			h.log.Error("Backup download error:", err)
			h.serverError(w, r)
			return
		}
		// res.send, not res.json: the old console did not compress it.
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		httpx.WriteBody(w, r, http.StatusOK, "application/json; charset=utf-8", text)
		return
	}

	archive, filename, err := h.backups.CreateFullBackup(ctx)
	if err != nil {
		h.log.Error("Backup download error:", err)
		h.serverError(w, r)
		return
	}
	size := backup.ToFixed(int64(len(archive)), 1024*1024, 2)
	if err := h.d.LogAction(r, "backup_created", "Full backup downloaded ("+size+" MB)"); err != nil {
		h.log.Error("Backup download error:", err)
		h.serverError(w, r)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	httpx.WriteBody(w, r, http.StatusOK, "application/gzip", archive)
}

// store10 is the date of the snapshot: new Date().toISOString().slice(0, 10).
func store10(snapshot *goapi.Object) string {
	created := snapshot.Str("_created")
	if len(created) < 10 {
		return created
	}
	return created[:10]
}

// restore answers POST /api/settings/restore: a multipart upload with the
// file in "backup" and the parts to restore as text fields. A gzip file
// is a full backup (restoreDatabase and restoreUploads default to on,
// restoreSecrets, restoreEnv and restoreGoDb to off); anything else is
// read as a settings snapshot (restoreUsers defaults to off, the other
// parts to on).
func (h *handlers) restore(w http.ResponseWriter, r *http.Request) {
	upload, err := middleware.ParseUpload(r, middleware.UploadOptions{Field: "backup", MaxFileSize: maxUpload})
	if err != nil {
		h.d.WebUI.ServerError(w, r, err, 0)
		return
	}
	if upload.File == nil {
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "backup.no_file", nil))
		return
	}
	buf := upload.File.Buffer
	notFalse := func(name string) bool {
		s, ok := upload.Fields.Get(name).Str()
		return !ok || s != "false"
	}
	isTrue := func(name string) bool {
		s, ok := upload.Fields.Get(name).Str()
		return ok && s == "true"
	}
	ctx := r.Context()

	if len(buf) > 2 && buf[0] == 0x1f && buf[1] == 0x8b {
		result, err := h.backups.RestoreFullBackup(ctx, buf, backup.RestoreOptions{
			Database: notFalse("restoreDatabase"),
			Uploads:  notFalse("restoreUploads"),
			Secrets:  isTrue("restoreSecrets"),
			Env:      isTrue("restoreEnv"),
			GoDB:     isTrue("restoreGoDb"),
		})
		if err != nil {
			kit.Fail(w, r, http.StatusBadRequest, errText(err))
			return
		}
		h.audit(r, "Full restore | Restored: "+strings.Join(result.Restored, ", ")+" | Skipped: "+strings.Join(result.Skipped, ", "))
		data := branding.NewObject()
		data.Set("type", "full")
		data.Set("restored", strings2any(result.Restored))
		data.Set("skipped", strings2any(result.Skipped))
		data.Set("warnings", strings2any(result.Warnings))
		data.Set("requiresRestart", result.RequiresRestart)
		writeData(w, r, branding.Stringify(data))
		return
	}

	parsed, err := branding.ParseJSON(buf)
	if err != nil {
		kit.Fail(w, r, http.StatusBadRequest, kit.T(r, "backup.invalid_json", nil))
		return
	}
	valid, errs, err := backup.ValidateBackup(parsed)
	if err != nil {
		h.log.Error("Restore error:", err)
		h.serverError(w, r)
		return
	}
	if !valid {
		kit.Fail(w, r, http.StatusBadRequest, strings.Join(errs, "; "))
		return
	}
	result := h.backups.RestoreBackup(ctx, parsed, backup.SnapshotOptions{
		Settings:     notFalse("restoreSettings"),
		Branding:     notFalse("restoreBranding"),
		Users:        isTrue("restoreUsers"),
		Folders:      notFalse("restoreFolders"),
		Groups:       notFalse("restoreGroups"),
		AddressBooks: notFalse("restoreAddressBooks"),
	})
	h.audit(r, "Config restore | Restored: "+strings.Join(result.Restored, ", ")+" | Skipped: "+strings.Join(result.Skipped, ", "))
	data := branding.NewObject()
	data.Set("type", "config")
	data.Set("restored", strings2any(result.Restored))
	data.Set("skipped", strings2any(result.Skipped))
	data.Set("warnings", strings2any(result.Warnings))
	data.Set("backupDate", member(parsed, "_created"))
	data.Set("backupVersion", member(parsed, "_console_version"))
	writeData(w, r, branding.Stringify(data))
}

// audit writes the audit entry of a restore. The restore is done when it
// runs, so a failed entry is logged and the answer is still the result.
// A download is different: it is refused without its entry.
func (h *handlers) audit(r *http.Request, details string) {
	if err := h.d.LogAction(r, "backup_restored", details); err != nil {
		h.log.Error("Restore audit error:", err)
	}
}

// member is v[key], undefined when v has no such key.
func member(v any, key string) any {
	o, ok := v.(*branding.Object)
	if !ok {
		return branding.Undefined
	}
	value, has := o.Get(key)
	if !has {
		return branding.Undefined
	}
	return value
}

func strings2any(items []string) []any {
	out := make([]any, len(items))
	for i, s := range items {
		out[i] = s
	}
	return out
}

// errText is err.message.
func errText(err error) string {
	var js *branding.Error
	if errors.As(err, &js) {
		return js.Message
	}
	return err.Error()
}
