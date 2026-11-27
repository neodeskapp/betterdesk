package api

// The answers people give after quick support. The console checks each
// answer against its feedback form and writes it here as JSON under
// support_feedback_<session id>, one per session. Like the session
// records the routes answer only callers in the same process; they check
// the id, the size and that the session has no answer yet.

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
)

const (
	supportFeedbackPrefix = "support_feedback_"
	// supportFeedbackMaxBytes bounds one stored answer: ten questions with
	// their labels and texts of up to 2000 characters fit
	supportFeedbackMaxBytes = 128 << 10
)

// feedbackMu keeps the check for an answer and its write together, so a
// session gets one answer.
var feedbackMu sync.Mutex

// onboardFeedbackRoutes adds the routes of the answers.
func (s *Server) onboardFeedbackRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/onboard/feedback", s.inProcessOnly(s.handleSupportFeedbackList))
	mux.HandleFunc("GET /api/onboard/feedback/{id}", s.inProcessOnly(s.handleSupportFeedbackGet))
	mux.HandleFunc("PUT /api/onboard/feedback/{id}", s.inProcessOnly(s.handleSupportFeedbackPut))
}

// handleSupportFeedbackList answers every stored answer.
// GET /api/onboard/feedback
func (s *Server) handleSupportFeedbackList(w http.ResponseWriter, _ *http.Request) {
	rows, err := s.db.ListConfigByPrefix(supportFeedbackPrefix)
	if err != nil {
		onboardFail(w, http.StatusInternalServerError, "list_failed")
		return
	}
	list := make([]json.RawMessage, 0, len(rows))
	for _, row := range rows {
		if json.Valid([]byte(row.Value)) {
			list = append(list, json.RawMessage(row.Value))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "feedback": list})
}

// handleSupportFeedbackGet answers the answer of a session, 404 for none.
// GET /api/onboard/feedback/{id}
func (s *Server) handleSupportFeedbackGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !supportSessionID.MatchString(id) {
		onboardFail(w, http.StatusBadRequest, "invalid_id")
		return
	}
	raw, err := s.db.GetConfig(supportFeedbackPrefix + id)
	if err != nil || raw == "" || !json.Valid([]byte(raw)) {
		onboardFail(w, http.StatusNotFound, "not_found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "feedback": json.RawMessage(raw)})
}

// handleSupportFeedbackPut stores the answer of a session as it is sent;
// 409 already_given when the session has one.
// PUT /api/onboard/feedback/{id}
func (s *Server) handleSupportFeedbackPut(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !supportSessionID.MatchString(id) {
		onboardFail(w, http.StatusBadRequest, "invalid_id")
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, supportFeedbackMaxBytes+1))
	if err != nil || len(data) > supportFeedbackMaxBytes || !json.Valid(data) {
		onboardFail(w, http.StatusBadRequest, "invalid_record")
		return
	}
	feedbackMu.Lock()
	defer feedbackMu.Unlock()
	if raw, err := s.db.GetConfig(supportFeedbackPrefix + id); err == nil && raw != "" {
		onboardFail(w, http.StatusConflict, "already_given")
		return
	}
	if err := s.db.SetConfig(supportFeedbackPrefix+id, string(data)); err != nil {
		onboardFail(w, http.StatusInternalServerError, "store_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}
