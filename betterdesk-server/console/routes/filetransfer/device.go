package filetransfer

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// The two routes a device calls with its token instead of a session. They
// work on the same transfers as the routes for people, so a transfer
// someone starts in the console is the one the device sends its chunks to,
// and a transfer a device announces is listed in the console.
//
// Under /api/bd a write without a session needs no CSRF token; under
// /api/files it needs one like a browser's write (BUGS DV2). A request
// that also carries a session goes through the panel access guard of
// /api/files there.

// deviceMessage answers POST /file-transfer: a message of the device about
// its transfers, the body as it came ({action, transfer_id, chunk_index,
// data, filename, size, mime_type, path}; see
// filetransfer.Service.HandleMessage). The answer is the service's, with
// status 200 also when it is {"action":"error"}. The service turns every
// failure into such an answer, so the handler has no failure of its own.
//
// Any device may send a chunk into or cancel any transfer: the message is
// not checked against the transfer's device (BUGS FT2).
func (h *handlers) deviceMessage(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, h.transfers.HandleMessage(kit.Device(r).DeviceID, jsreq.Body(r)))
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
