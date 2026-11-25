package api

// What happened with an invite (a join code), for the console's invite
// sheet, which follows the join and then walks the administrator through
// the new computer's first steps: the computers that joined with it and
// the last computer it refused, with why. The note lives under
// onboard_joins_<code id> next to the code and outlives a code the joins
// used up, until the time the code would have expired.

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const onboardJoinsPrefix = "onboard_joins_"

// onboardJoinsMax is how many joined computers a note keeps, the most a
// code can be used.
const onboardJoinsMax = onboardMaxUses

// onboardJoined is a computer that joined with an invite.
type onboardJoined struct {
	DeviceID string `json:"device_id"`
	JoinedAt string `json:"joined_at"`
}

// onboardRefused is a computer an invite's join refused: reason is
// key_changed (the ID is enrolled with another key, or with none the
// server could check; an administrator approves the new key) or refused
// (any other refusal).
type onboardRefused struct {
	DeviceID string `json:"device_id"`
	Reason   string `json:"reason"`
	At       string `json:"at"`
}

// onboardJoins is the note of an invite: the code's record as it was at
// the last join or refusal, and what happened.
type onboardJoins struct {
	Record  onboardCode     `json:"record"`
	Joined  []onboardJoined `json:"joined"`
	Refused *onboardRefused `json:"refused"`
}

func (s *Server) loadOnboardJoins(id string) *onboardJoins {
	raw, err := s.db.GetConfig(onboardJoinsPrefix + id)
	if err != nil || raw == "" {
		return nil
	}
	var j onboardJoins
	if json.Unmarshal([]byte(raw), &j) != nil {
		return nil
	}
	return &j
}

// noteOnboardJoin writes what a join did into the invite's note: a joined
// device, or with refusal set the refused one. Called under onboardMu.
func (s *Server) noteOnboardJoin(record *pairingRecord, deviceID, refusal string) {
	j := s.loadOnboardJoins(record.ID)
	if j == nil {
		j = &onboardJoins{Joined: []onboardJoined{}}
	}
	j.Record = onboardView(record)
	now := s.now().UTC().Format(time.RFC3339)
	if refusal == "" {
		known := false
		for _, joined := range j.Joined {
			known = known || joined.DeviceID == deviceID
		}
		if !known && len(j.Joined) < onboardJoinsMax {
			j.Joined = append(j.Joined, onboardJoined{DeviceID: deviceID, JoinedAt: now})
		}
		if j.Refused != nil && j.Refused.DeviceID == deviceID {
			j.Refused = nil
		}
	} else {
		reason := "refused"
		if refusal == keyChangedRefusal {
			reason = "key_changed"
		}
		j.Refused = &onboardRefused{DeviceID: deviceID, Reason: reason, At: now}
	}
	data, err := json.Marshal(j)
	if err != nil {
		return
	}
	_ = s.db.SetConfig(onboardJoinsPrefix+record.ID, string(data))
}

// pruneOnboardJoins deletes the notes of invites past their end.
func (s *Server) pruneOnboardJoins() {
	configs, err := s.db.ListConfigByPrefix(onboardJoinsPrefix)
	if err != nil {
		return
	}
	now := s.now().UTC()
	for _, cfg := range configs {
		var j onboardJoins
		if json.Unmarshal([]byte(cfg.Value), &j) != nil {
			_ = s.db.DeleteConfig(cfg.Key)
			continue
		}
		if expires, err := time.Parse(time.RFC3339, j.Record.ExpiresAt); err != nil || !now.Before(expires) {
			_ = s.db.DeleteConfig(cfg.Key)
		}
	}
}

// handleOnboardCode answers an invite with what happened with it: its
// record (the open code's, else the one of its last join), the computers
// that joined and the last one it refused. 404 for an id that is no join
// code, or one past its end.
// GET /api/onboard/codes/{id}
func (s *Server) handleOnboardCode(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	s.onboardMu.Lock()
	defer s.onboardMu.Unlock()
	j := s.loadOnboardJoins(id)
	var open *onboardCode
	for _, entry := range s.pairingRecords() {
		if entry.Record.ID == id && entry.Record.Kind == onboardKindJoin {
			view := onboardView(&entry.Record)
			open = &view
		}
	}
	answer := onboardJoins{Joined: []onboardJoined{}}
	switch {
	case open != nil:
		answer.Record = *open
	case j != nil:
		answer.Record = j.Record
		answer.Record.UsesLeft = 0
	default:
		onboardFail(w, http.StatusNotFound, "not_found")
		return
	}
	if expires, err := time.Parse(time.RFC3339, answer.Record.ExpiresAt); err != nil || !s.now().UTC().Before(expires) {
		onboardFail(w, http.StatusNotFound, "not_found")
		return
	}
	if j != nil {
		answer.Joined = j.Joined
		answer.Refused = j.Refused
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "record": answer.Record, "joined": answer.Joined, "refused": answer.Refused,
	})
}
