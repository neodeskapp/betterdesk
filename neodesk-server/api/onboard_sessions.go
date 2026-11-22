package api

// The records of quick support sessions. The console keeps each session
// and writes it here as JSON under support_session_<id>, so a session
// outlives a restart and stays as history after it ended. The routes
// answer only callers in the same process; they store what the console
// sends and check nothing but the id and the size.

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
)

const (
	supportSessionPrefix = "support_session_"
	// supportSessionMaxBytes bounds one stored record.
	supportSessionMaxBytes = 16 << 10
)

// supportSessionID is the form of a session id: the 16 hex digits of the
// support code's id.
var supportSessionID = regexp.MustCompile(`^[a-f0-9]{16}$`)

// onboardSessionRoutes adds the routes of the session records.
func (s *Server) onboardSessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/onboard/sessions", s.inProcessOnly(s.handleSupportSessionList))
	mux.HandleFunc("PUT /api/onboard/sessions/{id}", s.inProcessOnly(s.handleSupportSessionPut))
	mux.HandleFunc("DELETE /api/onboard/sessions/{id}", s.inProcessOnly(s.handleSupportSessionDelete))
}

// handleSupportSessionList answers every stored record.
// GET /api/onboard/sessions
func (s *Server) handleSupportSessionList(w http.ResponseWriter, _ *http.Request) {
	rows, err := s.db.ListConfigByPrefix(supportSessionPrefix)
	if err != nil {
		onboardFail(w, http.StatusInternalServerError, "list_failed")
		return
	}
	sessions := make([]json.RawMessage, 0, len(rows))
	for _, row := range rows {
		if json.Valid([]byte(row.Value)) {
			sessions = append(sessions, json.RawMessage(row.Value))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "sessions": sessions})
}

// handleSupportSessionPut stores a record as it is sent.
// PUT /api/onboard/sessions/{id}
func (s *Server) handleSupportSessionPut(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !supportSessionID.MatchString(id) {
		onboardFail(w, http.StatusBadRequest, "invalid_id")
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, supportSessionMaxBytes+1))
	if err != nil || len(data) > supportSessionMaxBytes || !json.Valid(data) {
		onboardFail(w, http.StatusBadRequest, "invalid_record")
		return
	}
	if err := s.db.SetConfig(supportSessionPrefix+id, string(data)); err != nil {
		onboardFail(w, http.StatusInternalServerError, "store_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// handleSupportSessionDelete deletes a record.
// DELETE /api/onboard/sessions/{id}
func (s *Server) handleSupportSessionDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !supportSessionID.MatchString(id) {
		onboardFail(w, http.StatusBadRequest, "invalid_id")
		return
	}
	if err := s.db.DeleteConfig(supportSessionPrefix + id); err != nil {
		onboardFail(w, http.StatusInternalServerError, "delete_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}
