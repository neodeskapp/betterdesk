package onboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
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

// HasKeyRequest reports whether the device brought a new key that waits
// for an administrator.
func (c *Codes) HasKeyRequest(ctx context.Context, deviceID string) (bool, error) {
	resp, err := c.api.Request(ctx, http.MethodGet, "/onboard/devices/"+url.PathEscape(deviceID)+"/key-request",
		goapi.ValidateStatus(func(int) bool { return true }))
	if err != nil {
		return false, err
	}
	var out struct {
		Success bool            `json:"success"`
		Request json.RawMessage `json:"request"`
	}
	if err := resp.Decode(&out); err != nil {
		return false, err
	}
	if resp.Status != http.StatusOK || !out.Success {
		return false, errors.New("read key request: the API server answered " + http.StatusText(resp.Status))
	}
	return len(out.Request) > 0 && string(out.Request) != "null", nil
}
