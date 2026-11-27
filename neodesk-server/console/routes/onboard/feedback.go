package onboard

// Feedback after quick support: the quick support app asks the person
// who got help the console's questions once the session ended
// (GET/POST /api/support/session/feedback, PROTOCOL.md), the admins set up
// the form (server.config) and read the answers (device.connect, the
// quick support permission).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/authn"
	"github.com/neodeskapp/neodesk/neodesk-server/console/i18n"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/onboard"
)

// requireFeedbackToken lets a request through that carries the session
// token of a session the person allowed: while it is allowed and for
// onboard.FeedbackWindow after its end. A token of no such session is 401
// invalid_session, as on the other app routes; one of a session the
// person never allowed is 409 wrong_state.
func (h *handlers) requireFeedbackToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := authn.ExtractBearerToken(r.Header.Get("Authorization"))
		if !ok {
			kit.JSON(w, r, http.StatusUnauthorized, refusal{Error: "invalid_session"})
			return
		}
		sess, err := h.sessions.FeedbackSession(r.Context(), token)
		switch {
		case errors.Is(err, onboard.ErrNotFound):
			kit.JSON(w, r, http.StatusUnauthorized, refusal{Error: "invalid_session"})
			return
		case errors.Is(err, onboard.ErrState):
			appFail(w, r, http.StatusConflict, onboard.ErrState.Error(), "")
			return
		case err != nil:
			h.log.Error("Quick support feedback session:", err)
			appFail(w, r, http.StatusInternalServerError, "server_error", "")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, sess)))
	})
}

// appForm is the form as the app gets it.
type appForm struct {
	Questions []onboard.Question `json:"questions"`
}

type appFeedbackAnswer struct {
	Success bool    `json:"success"`
	Form    appForm `json:"form"`
	Given   bool    `json:"given"`
}

// activeForm reads the form; ok is false when the answer is done: the
// form is off (404 feedback_off) or could not be read.
func (h *handlers) activeForm(w http.ResponseWriter, r *http.Request) (onboard.FeedbackForm, bool) {
	if h.d.Store == nil {
		appFail(w, r, http.StatusNotFound, "feedback_off", "")
		return onboard.FeedbackForm{}, false
	}
	form, err := onboard.LoadFeedbackForm(r.Context(), h.d.Store)
	if err != nil {
		h.log.Error("Quick support feedback form:", err)
		appFail(w, r, http.StatusInternalServerError, "server_error", "")
		return onboard.FeedbackForm{}, false
	}
	if !form.Active() {
		appFail(w, r, http.StatusNotFound, "feedback_off", "")
		return onboard.FeedbackForm{}, false
	}
	return form, true
}

// appFeedbackForm answers GET /api/support/session/feedback: the form as
// it is now, and whether the session has an answer.
func (h *handlers) appFeedbackForm(w http.ResponseWriter, r *http.Request) {
	form, ok := h.activeForm(w, r)
	if !ok {
		return
	}
	given, err := h.feedback.Given(r.Context(), appSessionOf(r).ID)
	if err != nil {
		h.log.Error("Quick support feedback:", err)
		appFail(w, r, http.StatusInternalServerError, "server_error", "")
		return
	}
	kit.JSON(w, r, http.StatusOK, appFeedbackAnswer{Success: true, Form: appForm{Questions: form.Questions}, Given: given})
}

// appFeedbackGive answers POST /api/support/session/feedback with the body
// {answers: {<question id>: <answer>}}: checked against the form as it is
// now and stored with the questions as they were asked, once per session.
func (h *handlers) appFeedbackGive(w http.ResponseWriter, r *http.Request) {
	sess := appSessionOf(r)
	form, ok := h.activeForm(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	given, err := h.feedback.Given(ctx, sess.ID)
	if err != nil {
		h.log.Error("Quick support feedback:", err)
		appFail(w, r, http.StatusInternalServerError, "server_error", "")
		return
	}
	if given {
		appFail(w, r, http.StatusConflict, onboard.ErrGiven.Error(), "")
		return
	}
	var body struct {
		Answers map[string]json.RawMessage `json:"answers"`
	}
	if err := kit.Bind(r, &body); err != nil || body.Answers == nil {
		appFail(w, r, http.StatusBadRequest, onboard.ErrInvalidAnswers.Error(), "")
		return
	}
	answers, err := onboard.CheckAnswers(form, body.Answers)
	if err != nil {
		appFail(w, r, http.StatusBadRequest, onboard.ErrInvalidAnswers.Error(), "")
		return
	}
	started := sess.RedeemedAt
	if started == "" {
		started = sess.CreatedAt
	}
	rec := onboard.FeedbackRecord{
		SessionID: sess.ID, DeviceID: sess.DeviceID, Hostname: sess.Hostname,
		HelperID: sess.CreatedByID, Helper: sess.CreatedBy, HelperName: h.personName(ctx, sess.CreatedByID, sess.CreatedBy),
		Name: sess.Name, Email: sess.Email, StartedAt: started, EndedAt: sess.EndedAt,
		AnsweredAt: h.now().UTC().Format(answeredFormat), Answers: answers,
	}
	if err := h.feedback.Add(ctx, rec); errors.Is(err, onboard.ErrGiven) {
		appFail(w, r, http.StatusConflict, onboard.ErrGiven.Error(), "")
		return
	} else if err != nil {
		h.log.Error("Store quick support feedback:", err)
		appFail(w, r, http.StatusInternalServerError, "server_error", "")
		return
	}
	kit.JSON(w, r, http.StatusOK, map[string]bool{"success": true})
}

// answeredFormat is the time of an answer: to the millisecond and of one
// length, so the times sort as text.
const answeredFormat = "2006-01-02T15:04:05.000Z07:00"

/* ── The console's side ── */

// consoleForm is the form as the console's settings show it.
type consoleForm struct {
	Enabled   bool               `json:"enabled"`
	Questions []onboard.Question `json:"questions"`
}

type formAnswer struct {
	Success bool        `json:"success"`
	Form    consoleForm `json:"form"`
}

// getFeedbackForm answers GET /api/support/feedback/form.
func (h *handlers) getFeedbackForm(w http.ResponseWriter, r *http.Request) {
	form, err := onboard.LoadFeedbackForm(r.Context(), h.d.Store)
	if err != nil {
		h.serverError(w, r, "Quick support feedback form:", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, formAnswer{Success: true, Form: consoleForm{Enabled: form.Enabled, Questions: form.Questions}})
}

// formRefusal is a form the console does not store: reason names what is
// wrong, index the question (from 0), absent for the whole form.
type formRefusal struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code"`
	Reason  string `json:"reason"`
	Index   *int   `json:"index,omitempty"`
}

// defaultQuestions are the questions a form switched on without any
// starts with, in the request's language.
func defaultQuestions(r *http.Request) []onboard.Question {
	return []onboard.Question{
		{Kind: onboard.QuestionRating, Label: kit.T(r, "quick_support_feedback.default_rating", nil), Required: true},
		{Kind: onboard.QuestionText, Label: kit.T(r, "quick_support_feedback.default_text", nil), Long: true},
	}
}

// saveFeedbackForm answers PUT /api/support/feedback/form with the body
// {enabled, questions: [{id?, kind, label, required, long?, options?}]}:
// the whole form. A question that names an id it had keeps it; any other
// gets a new one. Switched on without questions it starts with the two
// default ones. The audit log gets quick_support_feedback_form.
func (h *handlers) saveFeedbackForm(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled   *bool              `json:"enabled"`
		Questions []onboard.Question `json:"questions"`
	}
	if err := kit.Bind(r, &body); err != nil || body.Enabled == nil {
		fail(w, r, http.StatusBadRequest, "invalid_body")
		return
	}
	ctx := r.Context()
	prev, err := onboard.LoadFeedbackForm(ctx, h.d.Store)
	if err != nil {
		h.serverError(w, r, "Quick support feedback form:", err)
		return
	}
	questions := body.Questions
	if *body.Enabled && len(questions) == 0 {
		questions = defaultQuestions(r)
	}
	form, err := onboard.NewForm(prev, *body.Enabled, questions)
	var bad *onboard.FormError
	if errors.As(err, &bad) {
		limits := i18n.V("min", onboard.MinChoiceOptions, "max", onboard.MaxQuestionLabel)
		if bad.Reason == "option_count" {
			limits = i18n.V("min", onboard.MinChoiceOptions, "max", onboard.MaxChoiceOptions)
		}
		out := formRefusal{Error: kit.T(r, "quick_support_feedback.errors."+bad.Reason, limits), Code: "invalid_form", Reason: bad.Reason}
		if out.Error == "" || out.Error == "quick_support_feedback.errors."+bad.Reason {
			out.Error = bad.Reason
		}
		if bad.Index >= 0 {
			i := bad.Index
			out.Index = &i
		}
		kit.JSON(w, r, http.StatusBadRequest, out)
		return
	}
	if err := onboard.SaveFeedbackForm(ctx, h.d.Store, form); err != nil {
		h.serverError(w, r, "Store the quick support feedback form:", err)
		return
	}
	state := "off"
	if form.Enabled {
		state = "on"
	}
	h.audit(r, "quick_support_feedback_form", "Feedback after quick support: "+state+", "+strconv.Itoa(len(form.Questions))+" questions")
	kit.JSON(w, r, http.StatusOK, formAnswer{Success: true, Form: consoleForm{Enabled: form.Enabled, Questions: form.Questions}})
}

// feedbackItem is one answer as the console lists it.
type feedbackItem struct {
	SessionID  string           `json:"session_id"`
	AnsweredAt string           `json:"answered_at"`
	StartedAt  string           `json:"started_at"`
	EndedAt    string           `json:"ended_at"`
	DeviceID   string           `json:"device_id"`
	Hostname   string           `json:"hostname"`
	HelperID   int64            `json:"helper_id"`
	Helper     string           `json:"helper"`
	Name       string           `json:"name"`
	Email      string           `json:"email"`
	Stars      *int             `json:"stars"`
	Answers    []onboard.Answer `json:"answers"`
}

type helperChoice struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type feedbackList struct {
	Success  bool           `json:"success"`
	Feedback []feedbackItem `json:"feedback"`
	Count    int            `json:"count"`
	// Average is the mean of the stars of the listed answers that have
	// some, null when none has
	Average *float64       `json:"average"`
	Helpers []helperChoice `json:"helpers"`
}

// listFeedback answers GET /api/support/feedback?helper=<account id>: the
// answers newest first, with their number, the average stars and the
// helpers that have answers. helper limits the answers, the number and
// the average to one helper's sessions.
func (h *handlers) listFeedback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	all, err := h.feedback.List(ctx)
	if err != nil {
		h.serverError(w, r, "List quick support feedback:", err)
		return
	}
	var helper int64
	if raw := strings.TrimSpace(r.URL.Query().Get("helper")); raw != "" {
		if helper, err = strconv.ParseInt(raw, 10, 64); err != nil || helper <= 0 {
			fail(w, r, http.StatusBadRequest, "invalid_helper")
			return
		}
	}
	names := map[int64]string{}
	nameOf := func(rec onboard.FeedbackRecord) string {
		if name, ok := names[rec.HelperID]; ok {
			return name
		}
		fallback := rec.HelperName
		if fallback == "" {
			fallback = rec.Helper
		}
		name := h.personName(ctx, rec.HelperID, fallback)
		names[rec.HelperID] = name
		return name
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].AnsweredAt != all[j].AnsweredAt {
			return all[i].AnsweredAt > all[j].AnsweredAt
		}
		return all[i].SessionID > all[j].SessionID
	})
	out := feedbackList{Success: true, Feedback: []feedbackItem{}, Helpers: []helperChoice{}}
	seen := map[int64]bool{}
	sum, rated := 0, 0
	for _, rec := range all {
		name := nameOf(rec)
		if !seen[rec.HelperID] {
			seen[rec.HelperID] = true
			out.Helpers = append(out.Helpers, helperChoice{ID: rec.HelperID, Name: name})
		}
		if helper > 0 && rec.HelperID != helper {
			continue
		}
		item := feedbackItem{
			SessionID: rec.SessionID, AnsweredAt: rec.AnsweredAt, StartedAt: rec.StartedAt, EndedAt: rec.EndedAt,
			DeviceID: rec.DeviceID, Hostname: rec.Hostname, HelperID: rec.HelperID, Helper: name,
			Name: rec.Name, Email: rec.Email, Answers: rec.Answers,
		}
		if item.Answers == nil {
			item.Answers = []onboard.Answer{}
		}
		if stars := rec.Stars(); stars > 0 {
			item.Stars = &stars
			sum += stars
			rated++
		}
		out.Feedback = append(out.Feedback, item)
	}
	out.Count = len(out.Feedback)
	if rated > 0 {
		avg := float64(sum) / float64(rated)
		out.Average = &avg
	}
	sort.SliceStable(out.Helpers, func(i, j int) bool {
		return strings.ToLower(out.Helpers[i].Name) < strings.ToLower(out.Helpers[j].Name)
	})
	kit.JSON(w, r, http.StatusOK, out)
}

// receiverOfferSetting is the setting "Offer to install after quick
// support" as the console reads and writes it.
type receiverOfferSetting struct {
	Success bool `json:"success"`
	Enabled bool `json:"enabled"`
}

// getReceiverOffer answers GET /api/support/receiver-offer.
func (h *handlers) getReceiverOffer(w http.ResponseWriter, r *http.Request) {
	on, err := onboard.LoadReceiverOffer(r.Context(), h.d.Store)
	if err != nil {
		h.serverError(w, r, "Quick support receiver offer:", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, receiverOfferSetting{Success: true, Enabled: on})
}

// saveReceiverOffer answers PUT /api/support/receiver-offer with the body
// {enabled}. The audit log gets quick_support_receiver_offer.
func (h *handlers) saveReceiverOffer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := kit.Bind(r, &body); err != nil || body.Enabled == nil {
		fail(w, r, http.StatusBadRequest, "invalid_body")
		return
	}
	if err := onboard.SaveReceiverOffer(r.Context(), h.d.Store, *body.Enabled); err != nil {
		h.serverError(w, r, "Store the quick support receiver offer:", err)
		return
	}
	state := "off"
	if *body.Enabled {
		state = "on"
	}
	h.audit(r, "quick_support_receiver_offer", "Offer to install after quick support: "+state)
	kit.JSON(w, r, http.StatusOK, receiverOfferSetting{Success: true, Enabled: *body.Enabled})
}
