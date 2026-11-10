// Package filetransfer holds the console's routes for file transfers:
// people start a transfer with a device, list the transfers, watch one,
// cancel it, and download the file of a completed one; a device, with its
// token instead of a session, announces a transfer, sends its chunks and
// reads how far its transfer is (device.go). The old console mounted the
// group at /api/files and at /api/bd; both are kept.
//
// The transfers themselves are in package console/filetransfer, one
// registry for both sides.
package filetransfer

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/unitronix/betterdesk-server/console/filetransfer"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/reqquery"
)

type handlers struct {
	transfers *filetransfer.Service
}

// Register adds the group's routes below the mount of d.
func Register(d *kit.Deps) {
	h := &handlers{transfers: d.FileTransfer}
	connect := d.RequirePermission("device.connect")

	r := d.Router
	r.Post("/transfer", h.start, d.Limits.Upload.Middleware, d.RequireAuth, connect)
	r.Get("/transfers", h.list, d.RequireAuth, connect)
	r.Get("/transfers/{id}", h.progress, d.RequireAuth, connect)
	r.Post("/transfers/{id}/cancel", h.cancel, d.RequireAuth, connect)
	r.Get("/transfers/{id}/download", h.download, d.Limits.FileAccess.Middleware, d.RequireAuth, connect)

	r.Post("/file-transfer", h.deviceMessage, d.RequireDeviceToken, d.RequireTokenDeviceMatch)
	r.Get("/file-transfer/{id}", h.deviceTransfer, d.RequireDeviceToken)
}

// failure is the answer {"error": text} of this group.
type failure struct {
	Error string `json:"error"`
}

func fail(w http.ResponseWriter, r *http.Request, status int, message string) {
	kit.JSON(w, r, status, failure{Error: message})
}

// bodyValue is req.body[key] as a parsed JSON value, nil when the body
// does not have the key.
func bodyValue(r *http.Request, key string) any {
	raw := kit.Body(r).Get(key).Raw()
	if raw == nil {
		return nil
	}
	value, err := goapi.Parse(raw)
	if err != nil {
		return nil
	}
	return value
}

// start answers POST /transfer with the body {direction, device_id,
// filename, size, mime_type}: it starts a transfer and answers 201 with
// its record. The size is read as a whole number, the file name is made
// safe, and the account is noted as the one who started it.
func (h *handlers) start(w http.ResponseWriter, r *http.Request) {
	device, filename, direction := bodyValue(r, "device_id"), bodyValue(r, "filename"), bodyValue(r, "direction")
	if !goapi.Truthy(device) || !goapi.Truthy(filename) {
		fail(w, r, http.StatusBadRequest, "device_id and filename are required")
		return
	}
	if direction != "upload" && direction != "download" {
		fail(w, r, http.StatusBadRequest, `direction must be "upload" or "download"`)
		return
	}
	size, err := filetransfer.ParseSize(bodyValue(r, "size"))
	if err != nil {
		fail(w, r, http.StatusBadRequest, err.Error())
		return
	}
	createdBy := "admin"
	if user := kit.User(r); user != nil && user.Username != "" {
		createdBy = user.Username
	}
	transfer, err := h.transfers.CreateTransfer(filetransfer.Params{
		Direction: direction,
		DeviceID:  device,
		Filename:  filename,
		Size:      size,
		MimeType:  bodyValue(r, "mime_type"),
		CreatedBy: createdBy,
	})
	if err != nil {
		fail(w, r, http.StatusBadRequest, err.Error())
		return
	}
	kit.JSON(w, r, http.StatusCreated, transfer)
}

// list answers GET /transfers with every transfer, newest first, or with
// those of ?device_id=, ?status= and ?direction=.
func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	query := reqquery.Of(r)
	kit.JSON(w, r, http.StatusOK, h.transfers.ListTransfers(filetransfer.Filter{
		DeviceID:  query.Get("device_id").JSON(),
		Status:    query.Get("status").JSON(),
		Direction: query.Get("direction").JSON(),
	}))
}

// progress answers GET /transfers/{id} with how far the transfer is.
func (h *handlers) progress(w http.ResponseWriter, r *http.Request) {
	progress := h.transfers.GetProgress(r.PathValue("id"))
	if progress == nil {
		fail(w, r, http.StatusNotFound, "Transfer not found")
		return
	}
	kit.JSON(w, r, http.StatusOK, progress)
}

// cancelled is the answer to a cancel.
type cancelled struct {
	Status string `json:"status"`
}

// cancel answers POST /transfers/{id}/cancel.
func (h *handlers) cancel(w http.ResponseWriter, r *http.Request) {
	if !h.transfers.CancelTransfer(r.PathValue("id")) {
		fail(w, r, http.StatusNotFound, "Transfer not found")
		return
	}
	kit.JSON(w, r, http.StatusOK, cancelled{Status: "cancelled"})
}

// download answers GET /transfers/{id}/download with the file of a
// completed transfer, as an attachment under the transfer's file name. A
// request for a part of the file that cannot be served, and a file that
// went away meanwhile, end as 500 "Download failed".
func (h *handlers) download(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	transfer := h.transfers.GetTransfer(id)
	if transfer == nil {
		fail(w, r, http.StatusNotFound, "Transfer not found")
		return
	}
	if transfer.Status != filetransfer.StatusCompleted {
		fail(w, r, http.StatusBadRequest, "Transfer is not completed yet")
		return
	}
	path := h.transfers.GetCompletedFilePath(id)
	if path == "" {
		fail(w, r, http.StatusNotFound, "File not found on server")
		return
	}
	// The error goes out as JSON under the file's own type when the file
	// was found before the request failed: the type is set by then, and
	// only its charset is put to utf-8.
	failed := func(w http.ResponseWriter, r *http.Request) {
		if fileType := w.Header().Get("Content-Type"); fileType != "" {
			media, _, _ := strings.Cut(fileType, ";")
			w.Header().Set("Content-Type", strings.TrimSpace(media)+"; charset=utf-8")
		}
		fail(w, r, http.StatusInternalServerError, "Download failed")
	}
	files := httpx.NewStatic(filepath.Dir(path), httpx.StaticOptions{
		NoFallthrough: true,
		NoIndex:       true,
		NoRedirect:    true,
		OnError:       func(w http.ResponseWriter, r *http.Request, _ int, _ error) { failed(w, r) },
	})
	w.Header().Set("Content-Disposition", `attachment; filename="`+transfer.Filename+`"`)
	files.ServeName(w, r, filepath.Base(path), http.HandlerFunc(failed))
}
