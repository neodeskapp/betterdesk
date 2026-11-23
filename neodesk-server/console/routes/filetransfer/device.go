package filetransfer

import (
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsreq"
)

// The two routes a device calls with its token instead of a session. They
// work on the same transfers as the routes for people, so a transfer
// someone starts in the console is the one the device sends its chunks to,
// and a transfer a device announces is listed in the console.
//
// A write with the device token and without a session needs no CSRF token
// at either mount; with a session it needs one like a browser's write. A
// request that also carries a session goes through the panel access guard
// of /api/files there.

// deviceMessage answers POST /file-transfer: a message of the device about
// its transfers, the body as it came ({action, transfer_id, chunk_index,
// data, filename, size, mime_type, path}; see
// filetransfer.Service.HandleMessage). The answer is the service's, with
// status 200 also when it is {"action":"error"}, except for an init
// beyond the open transfers the device or the console may have, which
// answers 429. The service turns every failure into such an answer, so
// the handler has no failure of its own.
//
// A chunk or a cancel reaches only a transfer of the token's device; one
// for another device's transfer is answered as one for no transfer.
func (h *handlers) deviceMessage(w http.ResponseWriter, r *http.Request) {
	answer, status := h.transfers.HandleMessageStatus(kit.Device(r).DeviceID, jsreq.Body(r))
	kit.JSON(w, r, status, answer)
}

// deviceTransfer answers GET /file-transfer/{id} with how far the transfer
// is, for the device the transfer is with. The transfer's device must be
// the token's device as text: a transfer someone started for the device
// 123 (a number) is no device's.
func (h *handlers) deviceTransfer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	transfer := h.transfers.GetTransfer(id)
	if transfer == nil {
		fail(w, r, http.StatusNotFound, "Transfer not found")
		return
	}
	if device, isText := transfer.DeviceID.(string); !isText || device != kit.Device(r).DeviceID {
		fail(w, r, http.StatusForbidden, "Access denied")
		return
	}
	progress := h.transfers.GetProgress(id)
	if progress == nil {
		// The housekeeping dropped the record meanwhile
		fail(w, r, http.StatusNotFound, "Transfer not found")
		return
	}
	kit.JSON(w, r, http.StatusOK, progress)
}
