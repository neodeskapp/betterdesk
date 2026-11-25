package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// inviteState is GET /api/onboard/codes/{id}.
type inviteState struct {
	Success bool        `json:"success"`
	Error   string      `json:"error"`
	Record  onboardCode `json:"record"`
	Joined  []struct {
		DeviceID string `json:"device_id"`
		JoinedAt string `json:"joined_at"`
	} `json:"joined"`
	Refused *struct {
		DeviceID string `json:"device_id"`
		Reason   string `json:"reason"`
		At       string `json:"at"`
	} `json:"refused"`
}

func inviteStateOf(t *testing.T, srv *Server, id string) (int, inviteState) {
	t.Helper()
	status, raw := asConsole(t, srv, http.MethodGet, "/api/onboard/codes/"+id, "")
	var out inviteState
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("not JSON: %v: %s", err, raw)
	}
	return status, out
}

// TestAnInviteKeepsTheComputersThatJoinedIt: the console's invite sheet
// follows the join, also of an invite that the join used up, until the
// invite would have expired.
func TestAnInviteKeepsTheComputersThatJoinedIt(t *testing.T) {
	onboardStores(t, func(t *testing.T, srv *Server) {
		made := makeOnboardCode(t, srv, map[string]any{"kind": "join", "name": "Anna's laptop"})
		status, state := inviteStateOf(t, srv, made.Record.ID)
		if status != http.StatusOK || state.Record.Name != "Anna's laptop" || len(state.Joined) != 0 || state.Refused != nil {
			t.Fatalf("before the join: %d %+v", status, state)
		}

		status, out := onboardCall(t, srv, http.MethodPost, "/api/onboard/codes/redeem", map[string]any{
			"code": made.Code, "kind": "join", "device": map[string]any{"device_id": "500000001", "uuid": "u-1"},
		})
		if status != http.StatusOK {
			t.Fatalf("join: %d %+v", status, out)
		}
		status, state = inviteStateOf(t, srv, made.Record.ID)
		if status != http.StatusOK || len(state.Joined) != 1 || state.Joined[0].DeviceID != "500000001" ||
			state.Joined[0].JoinedAt == "" || state.Record.UsesLeft != 0 || state.Record.Name != "Anna's laptop" {
			t.Fatalf("after the join that used it up: %d %+v", status, state)
		}
		// The used-up invite is not open any more
		if _, list := onboardCall(t, srv, http.MethodGet, "/api/onboard/codes?kind=join", nil); len(list.Codes) != 0 {
			t.Fatalf("the used-up invite is listed: %+v", list.Codes)
		}

		srv.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
		onboardCall(t, srv, http.MethodPost, "/api/onboard/codes/prune", map[string]any{})
		if status, _ := inviteStateOf(t, srv, made.Record.ID); status != http.StatusNotFound {
			t.Fatalf("after the invite's end: %d", status)
		}
		if status, _ := inviteStateOf(t, srv, "0123456789abcdef"); status != http.StatusNotFound {
			t.Fatalf("an unknown invite: %d", status)
		}
	})
}

// TestAnInviteNotesAComputerRefusedForItsKey: the sheet offers to approve
// the new key of a computer that was here before.
func TestAnInviteNotesAComputerRefusedForItsKey(t *testing.T) {
	onboardStores(t, func(t *testing.T, srv *Server) {
		key := newJoinKey(t)
		enrolledLabComputer(t, srv, key)
		made := makeOnboardCode(t, srv, map[string]any{"kind": "join"})
		other := newJoinKey(t)
		if status, _ := joinAs(t, srv, made.Code, map[string]any{"uuid": "machine-1", "public_key": other.b64}); status != http.StatusForbidden {
			t.Fatalf("join with a new key: %d", status)
		}
		status, state := inviteStateOf(t, srv, made.Record.ID)
		if status != http.StatusOK || state.Refused == nil || state.Refused.DeviceID != "289485454" || state.Refused.Reason != "key_changed" ||
			len(state.Joined) != 0 || state.Record.UsesLeft != 1 {
			t.Fatalf("after the refusal: %d %+v", status, state)
		}
	})
}
