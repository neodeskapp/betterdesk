package onboard

import (
	"context"
	"fmt"
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
)

// CallerAccount asks the API server which console account's browser viewer
// opened the relay session to deviceID (A271). It answers the account, or
// 0 and the API server's refusal code (session_not_open, too_late,
// already_used). The API server answers once per session.
func (s *Sessions) CallerAccount(ctx context.Context, deviceID, session string) (int64, string, error) {
	resp, err := s.api.Request(ctx, http.MethodPost, "/support/caller",
		goapi.Body(map[string]string{"device_id": deviceID, "session": session}),
		goapi.ValidateStatus(func(int) bool { return true }))
	if err != nil {
		return 0, "", fmt.Errorf("POST /support/caller: %w", err)
	}
	var out struct {
		Account int64  `json:"account"`
		Error   string `json:"error"`
	}
	if err := resp.Decode(&out); err != nil {
		return 0, "", fmt.Errorf("POST /support/caller: answer %d is no JSON: %w", resp.Status, err)
	}
	switch {
	case resp.Status == http.StatusOK && out.Account > 0:
		return out.Account, "", nil
	case resp.Status == http.StatusConflict && out.Error != "":
		return 0, out.Error, nil
	}
	return 0, "", fmt.Errorf("POST /support/caller: the API server answered %d %s", resp.Status, out.Error)
}
