package onboard

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// TestFeedbackTokenWorksForADayAfterAnAllowedSession: the token of a
// session the person allowed opens the feedback routes while it runs and
// for 24 hours after its end, while the other app routes refuse it from
// the end. The token of a session the person never allowed is wrong_state
// in that time, and nothing after it.
func TestFeedbackTokenWorksForADayAfterAnAllowedSession(t *testing.T) {
	ctx := context.Background()
	s, _, c := newSessions(t)
	t0 := c.now
	token, sess, _ := s.Redeem(ctx, supportCode("aaaaaaaaaaaaaaaa", 5, c.now))
	_, _ = s.SetDevice(ctx, sess.ID, Device{DeviceID: "123456789"}, false, false)
	if _, err := s.FeedbackSession(ctx, token); !errors.Is(err, ErrState) {
		t.Fatalf("before Allow: %v, want wrong_state", err)
	}
	_, _ = s.Consent(ctx, sess.ID, true, nil)
	if got, err := s.FeedbackSession(ctx, token); err != nil || got.ID != sess.ID {
		t.Fatalf("while allowed: %+v %v", got, err)
	}
	if _, err := s.End(ctx, sess.ID, EndedByCustomer); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ByToken(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the other routes take the token of an ended session: %v", err)
	}
	c.now = t0.Add(FeedbackWindow - time.Second)
	if got, err := s.FeedbackSession(ctx, token); err != nil || got.Status != StatusEnded {
		t.Fatalf("a second before the day is over: %+v %v", got, err)
	}
	c.now = t0.Add(FeedbackWindow + time.Second)
	if _, err := s.FeedbackSession(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after the day: %v, want not found", err)
	}

	// A declined session, also after a restart of the console
	c.now = t0
	declinedToken, declined, _ := s.Redeem(ctx, supportCode("bbbbbbbbbbbbbbbb", 5, c.now))
	_, _ = s.SetDevice(ctx, declined.ID, Device{DeviceID: "223456789"}, false, false)
	_, _ = s.Consent(ctx, declined.ID, false, nil)
	restarted := NewSessions(s.api, c.Now)
	for name, sessions := range map[string]*Sessions{"running": s, "restarted": restarted} {
		if _, err := sessions.FeedbackSession(ctx, declinedToken); !errors.Is(err, ErrState) {
			t.Errorf("%s, declined: %v, want wrong_state", name, err)
		}
	}
	c.now = t0.Add(FeedbackWindow - time.Second)
	if _, err := restarted.FeedbackSession(ctx, token); err != nil {
		t.Errorf("an allowed session's token after a restart: %v", err)
	}
	if _, err := restarted.ByToken(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Errorf("the other routes take an ended session's token after a restart: %v", err)
	}
	if _, err := s.FeedbackSession(ctx, "short"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a token that is no token: %v", err)
	}
}

func TestCheckAnswers(t *testing.T) {
	form, err := NewForm(FeedbackForm{}, true, []Question{
		{Kind: QuestionRating, Label: "Stars", Required: true},
		{Kind: QuestionText, Label: "Words"},
		{Kind: QuestionChoice, Label: "Pick", Options: []string{"A", "B"}},
		{Kind: QuestionYesNo, Label: "Fine?", Required: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	read := func(s string) map[string]json.RawMessage {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	got, err := CheckAnswers(form, read(`{"q4":false,"q1":3,"q3":"B","q2":"ok"}`))
	if err != nil || len(got) != 4 || got[0].Value != 3 || got[0].Label != "Stars" || got[3].Value != false || got[2].Value != "B" {
		t.Fatalf("all answered: %+v %v", got, err)
	}
	// A false yes or no is an answer; blank text and choices are none
	if got, err = CheckAnswers(form, read(`{"q1":5,"q2":" ","q3":"","q4":false}`)); err != nil || len(got) != 2 {
		t.Fatalf("optional ones blank: %+v %v", got, err)
	}
	for _, bad := range []string{`{"q1":5}`, `{"q4":true}`, `{"q1":1.5,"q4":true}`, `{"q1":5,"q4":true,"q3":"C"}`, `{"q1":5,"q4":1}`} {
		if _, err := CheckAnswers(form, read(bad)); !errors.Is(err, ErrInvalidAnswers) {
			t.Errorf("%s: %v, want invalid_answers", bad, err)
		}
	}
}
