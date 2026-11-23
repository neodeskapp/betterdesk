package onboard

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// ErrNoKeyRequest is a device without a new key that waits for approval.
var ErrNoKeyRequest = errors.New("no_key_request")

// ApproveKey makes the device's new key its key: the computer can rejoin,
// and its old tokens and sessions end. actor is who approved it, for the
// audit log.
func (c *Codes) ApproveKey(ctx context.Context, deviceID, actor string) error {
	status, out, err := c.call(ctx, http.MethodPost, "/onboard/devices/"+url.PathEscape(deviceID)+"/key-request/approve", map[string]any{"actor": actor})
	if err != nil {
		return err
	}
	if status == http.StatusNotFound && out.Error == ErrNoKeyRequest.Error() {
		return ErrNoKeyRequest
	}
	if status != http.StatusOK || !out.Success {
		return failure("approve key", status, out)
	}
	return nil
}
