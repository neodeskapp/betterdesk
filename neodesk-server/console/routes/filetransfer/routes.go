// Package filetransfer holds the console's routes for file transfers:
// people start a transfer with a device, list the transfers, watch one,
// cancel it, and download the file of a completed one; a device, with its
// token instead of a session, announces a transfer, sends its chunks and
// reads how far its transfer is (device.go). The old console mounted the
// group at /api/files and at /api/nd; both are kept.
//
// The transfers themselves are in package console/filetransfer, one
// registry for both sides.
package filetransfer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/neodeskapp/neodesk/neodesk-server/console/filetransfer"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/reqquery"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
)

type handlers struct {
	transfers *filetransfer.Service
	d         *kit.Deps
}

// Register adds the group's routes below the mount of d.
//
// The routes for people keep to the account's device scope: the list
// leaves out the transfers of devices the account may not see, their
// progress, cancel and download answer as for no transfer, and a start
// with such a device is 403. The rule is kit.RequireDeviceInScope's: the
// four admin roles see every device, and a device the device layer does
// not know, or a transfer whose device is no text, is kept from nobody.
func Register(d *kit.Deps) {
	h := &handlers{transfers: d.FileTransfer, d: d}
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

// scopeFailed answers a scope check that failed.
func (h *handlers) scopeFailed(w http.ResponseWriter, r *http.Request, err error) {
	h.d.Log.Error("Device scope guard error:", err.Error())
	fail(w, r, http.StatusInternalServerError, kit.T(r, "errors.server_error", nil))
}

// seesEveryDevice reports whether the account of the request has no device
// scope to keep to.
func seesEveryDevice(user *session.User) bool {
	if user == nil {
		return true
	}
	switch user.Role {
	case "super_admin", "admin", "global_admin", "server_admin":
		return true
	}
	return false
}

// maySee reports whether an account outside the admin roles may see the
// device of a transfer: true for a device that is no text or that the
// device layer does not know.
func (h *handlers) maySee(ctx context.Context, user *session.User, device any) (bool, error) {
	id, isText := device.(string)
	if !isText || id == "" {
		return true, nil
	}
	row := h.d.Backend.GetDeviceByID(ctx, id, true)
	if row == nil {
		return true, nil
	}
	allowed, err := h.d.DeviceGroups.UserCanAccessDevice(ctx, user, row)
	if err != nil {
		return false, fmt.Errorf("check the scope of device %s: %w", id, err)
	}
	return allowed, nil
}

// visibleTransfer is the transfer with the id when the account of the
// request may see its device, nil when there is no such transfer or the
// account may not see it.
func (h *handlers) visibleTransfer(r *http.Request, id string) (*filetransfer.Transfer, error) {
	transfer := h.transfers.GetTransfer(id)
	user := kit.User(r)
	if transfer == nil || seesEveryDevice(user) {
		return transfer, nil
	}
	allowed, err := h.maySee(r.Context(), user, transfer.DeviceID)
	if err != nil || !allowed {
		return nil, err
	}
	return transfer, nil
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
// its record. The device is text, so the transfer is the one the device's
// token reads. The size is read as a whole number, the file name is made
// safe, and the account is noted as the one who started it. A start
// beyond the open transfers the device or the console may have answers
// 429 with the service's error.
func (h *handlers) start(w http.ResponseWriter, r *http.Request) {
	device, filename, direction := bodyValue(r, "device_id"), bodyValue(r, "filename"), bodyValue(r, "direction")
	if !goapi.Truthy(device) || !goapi.Truthy(filename) {
		fail(w, r, http.StatusBadRequest, "device_id and filename are required")
		return
	}
	if _, isText := device.(string); !isText {
		fail(w, r, http.StatusBadRequest, "device_id must be text")
		return
	}
	if direction != "upload" && direction != "download" {
		fail(w, r, http.StatusBadRequest, `direction must be "upload" or "download"`)
		return
	}
	if user := kit.User(r); !seesEveryDevice(user) {
		allowed, err := h.maySee(r.Context(), user, device)
		if err != nil {
			h.scopeFailed(w, r, err)
			return
		}
		if !allowed {
			fail(w, r, http.StatusForbidden, "Access denied")
			return
		}
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
	if errors.Is(err, filetransfer.ErrTooManyTransfers) {
		fail(w, r, http.StatusTooManyRequests, err.Error())
		return
	}
	if err != nil {
		fail(w, r, http.StatusBadRequest, err.Error())
		return
	}
	kit.JSON(w, r, http.StatusCreated, transfer)
}

// list answers GET /transfers with every transfer the account may see,
// newest first, or with those of ?device_id=, ?status= and ?direction=.
func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	query := reqquery.Of(r)
	transfers := h.transfers.ListTransfers(filetransfer.Filter{
		DeviceID:  query.Get("device_id").JSON(),
		Status:    query.Get("status").JSON(),
		Direction: query.Get("direction").JSON(),
	})
	if user := kit.User(r); !seesEveryDevice(user) {
		visible := map[any]bool{}
		kept := transfers[:0]
		for _, transfer := range transfers {
			seen, known := visible[transfer.DeviceID]
			if !known {
				var err error
				if seen, err = h.maySee(r.Context(), user, transfer.DeviceID); err != nil {
					h.scopeFailed(w, r, err)
					return
				}
				visible[transfer.DeviceID] = seen
			}
			if seen {
				kept = append(kept, transfer)
			}
		}
		transfers = kept
	}
	kit.JSON(w, r, http.StatusOK, transfers)
}

// progress answers GET /transfers/{id} with how far the transfer is.
func (h *handlers) progress(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	transfer, err := h.visibleTransfer(r, id)
	if err != nil {
		h.scopeFailed(w, r, err)
		return
	}
	var progress *filetransfer.Progress
	if transfer != nil {
		progress = h.transfers.GetProgress(id)
	}
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
	id := r.PathValue("id")
	transfer, err := h.visibleTransfer(r, id)
	if err != nil {
		h.scopeFailed(w, r, err)
		return
	}
	if transfer == nil || !h.transfers.CancelTransfer(id) {
		fail(w, r, http.StatusNotFound, "Transfer not found")
		return
	}
	kit.JSON(w, r, http.StatusOK, cancelled{Status: "cancelled"})
}

// download answers GET /transfers/{id}/download with the file of a
// completed transfer, as an attachment under the transfer's file name. A
// part of the file beyond its end is 416, a failed condition 412, and a
// file that went away meanwhile 500, each {"error":"Download failed"}.
func (h *handlers) download(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	transfer, err := h.visibleTransfer(r, id)
	if err != nil {
		h.scopeFailed(w, r, err)
		return
	}
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
	// A request the file server refuses gets its status (416 for a range
	// beyond the file, 412 for a condition that fails, 500 for a disk
	// error) and the JSON error without the file's type and name
	failed := func(w http.ResponseWriter, r *http.Request, status int) {
		w.Header().Del("Content-Type")
		w.Header().Del("Content-Disposition")
		fail(w, r, status, "Download failed")
	}
	files := httpx.NewStatic(filepath.Dir(path), httpx.StaticOptions{
		NoFallthrough: true,
		NoIndex:       true,
		NoRedirect:    true,
		OnError:       func(w http.ResponseWriter, r *http.Request, status int, _ error) { failed(w, r, status) },
	})
	w.Header().Set("Content-Disposition", `attachment; filename="`+transfer.Filename+`"`)
	files.ServeName(w, r, filepath.Base(path), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failed(w, r, http.StatusInternalServerError)
	}))
}
