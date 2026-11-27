package onboard

// Feedback after quick support: the form an admin sets up in the console
// and the answers the person who got help gives in the quick support app
// once the session ended. The form is a row of the console's settings
// table; each answer is a record the API server keeps under
// support_feedback_<session id>, like the session records. Wire format of
// the app's side: routes/onboard/PROTOCOL.md.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The kinds of question.
const (
	// QuestionRating is 1 to 5 stars.
	QuestionRating = "rating"
	// QuestionText is a short line, or a long field when Long is set.
	QuestionText = "text"
	// QuestionChoice is one of 2 to 8 options.
	QuestionChoice = "choice"
	// QuestionYesNo is yes or no.
	QuestionYesNo = "yesno"
)

// The limits of the form and its answers.
const (
	MaxQuestions     = 10
	MaxQuestionLabel = 200
	MinChoiceOptions = 2
	MaxChoiceOptions = 8
	MaxAnswerText    = 2000
)

// FeedbackWindow is how long after its end the token of a session the
// person had allowed still opens the feedback routes.
const FeedbackWindow = 24 * time.Hour

// feedbackFormKey is the settings row of the form.
const feedbackFormKey = "quick_support_feedback_form"

// ErrGiven is an answer for a session that has one.
var ErrGiven = errors.New("already_given")

// ErrInvalidAnswers is an answer the form does not take.
var ErrInvalidAnswers = errors.New("invalid_answers")

// Question is one question of the feedback form. ID stays the same
// through edits and moves; Long only goes with a text question, Options
// only with a choice.
type Question struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Label    string   `json:"label"`
	Required bool     `json:"required"`
	Long     bool     `json:"long"`
	Options  []string `json:"options"`
}

// MarshalJSON writes long only for a text question and options only for
// a choice.
func (q Question) MarshalJSON() ([]byte, error) {
	out := struct {
		ID       string   `json:"id"`
		Kind     string   `json:"kind"`
		Label    string   `json:"label"`
		Required bool     `json:"required"`
		Long     *bool    `json:"long,omitempty"`
		Options  []string `json:"options,omitempty"`
	}{ID: q.ID, Kind: q.Kind, Label: q.Label, Required: q.Required}
	switch q.Kind {
	case QuestionText:
		long := q.Long
		out.Long = &long
	case QuestionChoice:
		out.Options = q.Options
		if out.Options == nil {
			out.Options = []string{}
		}
	}
	return json.Marshal(out)
}

// FeedbackForm is the form as it is stored.
type FeedbackForm struct {
	Enabled   bool       `json:"enabled"`
	Questions []Question `json:"questions"`
	// Next is the number of the next new question's id (q<Next>), so an
	// id never names two questions
	Next int `json:"next"`
}

// Active reports whether the app asks for feedback: the form is on and
// has questions.
func (f FeedbackForm) Active() bool { return f.Enabled && len(f.Questions) > 0 }

// FormError is a form the console does not store: Reason says what is
// wrong, Index which question (-1 for the form).
type FormError struct {
	Reason string
	Index  int
}

func (e *FormError) Error() string {
	if e.Index < 0 {
		return "invalid_form: " + e.Reason
	}
	return fmt.Sprintf("invalid_form: question %d: %s", e.Index+1, e.Reason)
}

// questionID is the form of an id the console made.
var questionID = regexp.MustCompile(`^q([1-9][0-9]{0,8})$`)

// NewForm checks the form an admin saved and gives its questions their
// ids: a question that names an id of prev keeps it, any other gets a new
// one. Labels and options are kept as they were typed.
func NewForm(prev FeedbackForm, enabled bool, questions []Question) (FeedbackForm, error) {
	if len(questions) > MaxQuestions {
		return FeedbackForm{}, &FormError{Reason: "too_many_questions", Index: -1}
	}
	next := max(prev.Next, 1)
	known := map[string]bool{}
	for _, q := range prev.Questions {
		known[q.ID] = true
		if m := questionID.FindStringSubmatch(q.ID); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n >= next {
				next = n + 1
			}
		}
	}
	out := FeedbackForm{Enabled: enabled, Questions: make([]Question, 0, len(questions))}
	used := map[string]bool{}
	for i, in := range questions {
		q := Question{ID: in.ID, Kind: in.Kind, Label: in.Label, Required: in.Required}
		if reason := checkLabel(in.Label); reason != "" {
			return FeedbackForm{}, &FormError{Reason: "label_" + reason, Index: i}
		}
		switch in.Kind {
		case QuestionRating, QuestionYesNo:
		case QuestionText:
			q.Long = in.Long
		case QuestionChoice:
			if len(in.Options) < MinChoiceOptions || len(in.Options) > MaxChoiceOptions {
				return FeedbackForm{}, &FormError{Reason: "option_count", Index: i}
			}
			seen := map[string]bool{}
			for _, o := range in.Options {
				if reason := checkLabel(o); reason != "" {
					return FeedbackForm{}, &FormError{Reason: "option_" + reason, Index: i}
				}
				if seen[o] {
					return FeedbackForm{}, &FormError{Reason: "option_twice", Index: i}
				}
				seen[o] = true
			}
			q.Options = append([]string(nil), in.Options...)
		default:
			return FeedbackForm{}, &FormError{Reason: "unknown_kind", Index: i}
		}
		if !known[q.ID] || used[q.ID] {
			q.ID = "q" + strconv.Itoa(next)
			next++
		}
		used[q.ID] = true
		out.Questions = append(out.Questions, q)
	}
	out.Next = next
	return out, nil
}

// checkLabel says what is wrong with a label or an option: "missing" when
// it is blank, "too_long" over MaxQuestionLabel characters, "" when it is
// fine.
func checkLabel(s string) string {
	if strings.TrimSpace(s) == "" {
		return "missing"
	}
	if utf8.RuneCountInString(s) > MaxQuestionLabel {
		return "too_long"
	}
	return ""
}

// LoadFeedbackForm reads the form; never saved, it is off and empty.
func LoadFeedbackForm(ctx context.Context, q store.Querier) (FeedbackForm, error) {
	var raw sql.NullString
	err := q.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`, feedbackFormKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !raw.Valid) {
		return FeedbackForm{Questions: []Question{}}, nil
	}
	if err != nil {
		return FeedbackForm{}, fmt.Errorf("read the feedback form: %w", err)
	}
	var f FeedbackForm
	if err := json.Unmarshal([]byte(raw.String), &f); err != nil {
		return FeedbackForm{}, fmt.Errorf("read the feedback form: %w", err)
	}
	if f.Questions == nil {
		f.Questions = []Question{}
	}
	return f, nil
}

// SaveFeedbackForm stores the form.
func SaveFeedbackForm(ctx context.Context, q store.Querier, f FeedbackForm) error {
	data, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("store the feedback form: %w", err)
	}
	now := store.NowSQL(q.Dialect())
	_, err = q.Exec(ctx, "INSERT INTO settings (key, value, updated_at) VALUES (?, ?, "+now+") "+
		"ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = "+now, feedbackFormKey, string(data))
	if err != nil {
		return fmt.Errorf("store the feedback form: %w", err)
	}
	return nil
}

// Answer is the answer to one question, with the question as it was
// asked. Value is a number of stars (rating), text (text, choice) or a
// bool (yesno).
type Answer struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Value any    `json:"value"`
}

// CheckAnswers checks the app's answers against the form: every id a
// question of it, every value of the question's kind (rating a whole 1 to
// 5, text up to MaxAnswerText characters, choice one of the options,
// yesno true or false) and every required question answered. A null, an
// empty text and an empty choice are no answer. It returns the answers in
// the form's order, ErrInvalidAnswers for anything else.
func CheckAnswers(f FeedbackForm, raw map[string]json.RawMessage) ([]Answer, error) {
	byID := map[string]Question{}
	for _, q := range f.Questions {
		byID[q.ID] = q
	}
	for id := range raw {
		if _, ok := byID[id]; !ok {
			return nil, ErrInvalidAnswers
		}
	}
	out := []Answer{}
	for _, q := range f.Questions {
		value, given, ok := answerValue(q, raw[q.ID])
		if !ok || (q.Required && !given) {
			return nil, ErrInvalidAnswers
		}
		if given {
			out = append(out, Answer{ID: q.ID, Kind: q.Kind, Label: q.Label, Value: value})
		}
	}
	return out, nil
}

// answerValue reads one answer: the value, whether there is one, and
// whether it is of the question's kind.
func answerValue(q Question, raw json.RawMessage) (value any, given, ok bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false, true
	}
	switch q.Kind {
	case QuestionRating:
		var n float64
		if json.Unmarshal(raw, &n) != nil || n != math.Trunc(n) || n < 1 || n > 5 {
			return nil, false, false
		}
		return int(n), true, true
	case QuestionYesNo:
		var b bool
		if json.Unmarshal(raw, &b) != nil {
			return nil, false, false
		}
		return b, true, true
	case QuestionText, QuestionChoice:
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil, false, false
		}
		if q.Kind == QuestionText {
			if utf8.RuneCountInString(s) > MaxAnswerText {
				return nil, false, false
			}
			if strings.TrimSpace(s) == "" {
				return nil, false, true
			}
			return s, true, true
		}
		if s == "" {
			return nil, false, true
		}
		for _, o := range q.Options {
			if s == o {
				return s, true, true
			}
		}
		return nil, false, false
	}
	return nil, false, false
}

// FeedbackRecord is one stored answer with the session it is about.
type FeedbackRecord struct {
	SessionID string `json:"session_id"`
	DeviceID  string `json:"device_id"`
	Hostname  string `json:"hostname"`
	// HelperID and Helper are the account that made the session and its
	// user name; HelperName its name as the console knew it then
	HelperID   int64  `json:"helper_id"`
	Helper     string `json:"helper"`
	HelperName string `json:"helper_name"`
	// Name and Email are the person's, as the session was made with them
	Name  string `json:"name"`
	Email string `json:"email"`
	// StartedAt is when the app opened the session; EndedAt is "" while it
	// runs
	StartedAt  string   `json:"started_at"`
	EndedAt    string   `json:"ended_at"`
	AnsweredAt string   `json:"answered_at"`
	Answers    []Answer `json:"answers"`
}

// Stars is the answer to the first rating question, 0 for none.
func (r FeedbackRecord) Stars() int {
	for _, a := range r.Answers {
		if a.Kind != QuestionRating {
			continue
		}
		switch v := a.Value.(type) {
		case int:
			return v
		case float64:
			return int(v)
		}
		return 0
	}
	return 0
}

// Feedback keeps the answers through the API server's store, which
// takes one answer per session.
type Feedback struct {
	api *goapi.Client
}

// NewFeedback returns the answers stored through api.
func NewFeedback(api *goapi.Client) *Feedback { return &Feedback{api: api} }

// Given reports whether the session has an answer.
func (f *Feedback) Given(ctx context.Context, sessionID string) (bool, error) {
	resp, err := f.api.Request(ctx, http.MethodGet, "/onboard/feedback/"+sessionID,
		goapi.ValidateStatus(func(int) bool { return true }))
	if err != nil {
		return false, fmt.Errorf("read the feedback of session %s: %w", sessionID, err)
	}
	switch resp.Status {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	}
	return false, fmt.Errorf("read the feedback of session %s: the API server answered %d", sessionID, resp.Status)
}

// Add stores the answer of a session; ErrGiven when it has one.
func (f *Feedback) Add(ctx context.Context, rec FeedbackRecord) error {
	resp, err := f.api.Request(ctx, http.MethodPut, "/onboard/feedback/"+rec.SessionID, goapi.Body(rec),
		goapi.ValidateStatus(func(int) bool { return true }))
	if err != nil {
		return fmt.Errorf("store the feedback of session %s: %w", rec.SessionID, err)
	}
	switch resp.Status {
	case http.StatusOK:
		return nil
	case http.StatusConflict:
		return ErrGiven
	}
	return fmt.Errorf("store the feedback of session %s: the API server answered %d", rec.SessionID, resp.Status)
}

// List returns every stored answer, in no order.
func (f *Feedback) List(ctx context.Context) ([]FeedbackRecord, error) {
	resp, err := f.api.Request(ctx, http.MethodGet, "/onboard/feedback")
	if err != nil {
		return nil, fmt.Errorf("list the feedback: %w", err)
	}
	var out struct {
		Feedback []FeedbackRecord `json:"feedback"`
	}
	if err := resp.Decode(&out); err != nil {
		return nil, fmt.Errorf("list the feedback: %w", err)
	}
	return out.Feedback, nil
}
