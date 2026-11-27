package onboard_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// quickSupportBy opens a quick support session of role on device, with
// the person's answer: "allow", "decline" or "" for none yet. It returns
// the app and its header with the session token.
func quickSupportBy(t *testing.T, w *world, role, name, device, answer string) (*browser, []string) {
	t.Helper()
	made := w.browser(role).do(http.MethodPost, "/api/support/sessions", `{"name":"`+name+`","email":"`+strings.ToLower(name)+`@example.com"}`).json(t)
	app := w.app()
	token := str(app.do(http.MethodPost, "/api/support/redeem", `{"code":"`+str(made, "code")+`"}`).json(t), "session_token")
	header := []string{"Authorization", "Bearer " + token}
	app.do(http.MethodPost, "/api/support/session/device", `{"device_id":"`+device+`","hostname":"`+strings.ToLower(name)+`-laptop"}`, header...)
	switch answer {
	case "allow":
		app.do(http.MethodPost, "/api/support/session/consent", `{"allowed":true}`, header...)
	case "decline":
		app.do(http.MethodPost, "/api/support/session/consent", `{"allowed":false}`, header...)
	}
	return app, header
}

// saveForm saves the feedback form as admin and returns the stored form.
func saveForm(t *testing.T, w *world, body string) map[string]any {
	t.Helper()
	res := w.browser("admin").do(http.MethodPut, "/api/support/feedback/form", body)
	if res.status != http.StatusOK {
		t.Fatalf("save form %s: %d %s", body, res.status, res.body)
	}
	return res.json(t)["form"].(map[string]any)
}

func questionsOf(form map[string]any) []map[string]any {
	var out []map[string]any
	for _, q := range form["questions"].([]any) {
		out = append(out, q.(map[string]any))
	}
	return out
}

// TestTheFeedbackFormKeepsItsQuestionsIDs: only server.config sets up the
// form. Switched on without questions it starts with the two default
// ones; a question keeps its id through edits and moves, a new one gets
// an id never used before, and labels stay as typed. What the form does
// not take is refused with the reason, and every change is in the audit
// log.
func TestTheFeedbackFormKeepsItsQuestionsIDs(t *testing.T) {
	w := newWorld(t, storetest.Migrated(t))
	for _, role := range []string{"operator", "viewer"} {
		if res := w.browser(role).do(http.MethodGet, "/api/support/feedback/form", ""); res.status != http.StatusForbidden {
			t.Errorf("%s reads the form: %d", role, res.status)
		}
		if res := w.browser(role).do(http.MethodPut, "/api/support/feedback/form", `{"enabled":true}`); res.status != http.StatusForbidden {
			t.Errorf("%s saves the form: %d", role, res.status)
		}
	}
	res := w.browser("admin").do(http.MethodGet, "/api/support/feedback/form", "")
	if res.status != http.StatusOK || res.body != `{"success":true,"form":{"enabled":false,"questions":[]}}` {
		t.Fatalf("a form never saved: %d %s", res.status, res.body)
	}

	form := saveForm(t, w, `{"enabled":true,"questions":[]}`)
	qs := questionsOf(form)
	if len(qs) != 2 || qs[0]["id"] != "q1" || qs[0]["kind"] != "rating" || qs[0]["label"] != "How was the help?" || qs[0]["required"] != true ||
		qs[1]["id"] != "q2" || qs[1]["kind"] != "text" || qs[1]["long"] != true || qs[1]["required"] != false {
		t.Fatalf("the default questions: %v", qs)
	}

	// q2 moves up and changes, q1 goes, two new ones come; a made-up id is no id
	form = saveForm(t, w, `{"enabled":true,"questions":[
		{"id":"q2","kind":"text","label":"  Anything else? ","required":false,"long":false},
		{"kind":"choice","label":"Which part?","options":["Mail","Printer"]},
		{"id":"q77","kind":"yesno","label":"Solved?","required":true}]}`)
	qs = questionsOf(form)
	if len(qs) != 3 || qs[0]["id"] != "q2" || qs[0]["label"] != "  Anything else? " || qs[0]["long"] != false ||
		qs[1]["id"] != "q3" || qs[2]["id"] != "q4" || qs[1]["options"] == nil || qs[2]["options"] != nil || qs[2]["long"] != nil {
		t.Fatalf("after edits: %v", qs)
	}
	// q1 is gone, and its id is not given again
	form = saveForm(t, w, `{"enabled":false,"questions":[{"kind":"rating","label":"Stars"}]}`)
	if qs = questionsOf(form); form["enabled"] != false || len(qs) != 1 || qs[0]["id"] != "q5" {
		t.Fatalf("after q1 went: %v", form)
	}

	many := make([]string, 11)
	for i := range many {
		many[i] = `{"kind":"yesno","label":"Q"}`
	}
	for body, reason := range map[string]string{
		`{"enabled":true,"questions":[` + strings.Join(many, ",") + `]}`:                              "too_many_questions",
		`{"enabled":true,"questions":[{"kind":"choice","label":"One","options":["Only"]}]}`:           "option_count",
		`{"enabled":true,"questions":[{"kind":"choice","label":"Twice","options":["A","A"]}]}`:        "option_twice",
		`{"enabled":true,"questions":[{"kind":"stars","label":"Odd"}]}`:                               "unknown_kind",
		`{"enabled":true,"questions":[{"kind":"rating","label":"   "}]}`:                              "label_missing",
		`{"enabled":true,"questions":[{"kind":"rating","label":"` + strings.Repeat("é", 201) + `"}]}`: "label_too_long",
	} {
		res := w.browser("admin").do(http.MethodPut, "/api/support/feedback/form", body)
		if res.status != http.StatusBadRequest || str(res.json(t), "code") != "invalid_form" || str(res.json(t), "reason") != reason {
			t.Errorf("%s: %d %s, want 400 %s", reason, res.status, res.body, reason)
		}
	}
	ok := saveForm(t, w, `{"enabled":true,"questions":[{"kind":"rating","label":"`+strings.Repeat("é", 200)+`"}]}`)
	if len(questionsOf(ok)) != 1 {
		t.Fatalf("a label of 200 characters: %v", ok)
	}

	var n int
	if err := w.db.QueryRow(context.Background(), `SELECT COUNT(*) FROM audit_log WHERE action = 'quick_support_feedback_form' AND user_id = 1`).Scan(&n); err != nil || n != 4 {
		t.Errorf("audit entries: %d %v, want 4", n, err)
	}
}

// TestFeedbackAfterQuickSupport: the person answers the form once, while
// the session is allowed or after it ended having been allowed; the
// answers are checked against the form as it is and kept with the
// questions as they were asked. A session the person never allowed gets
// wrong_state, a form that is off feedback_off, and the other app routes
// still refuse an ended session.
func TestFeedbackAfterQuickSupport(t *testing.T) {
	w := newWorld(t, storetest.Migrated(t))
	get := func(app *browser, header []string) answer {
		t.Helper()
		return app.do(http.MethodGet, "/api/support/session/feedback", "", header...)
	}
	give := func(app *browser, header []string, answers string) answer {
		t.Helper()
		return app.do(http.MethodPost, "/api/support/session/feedback", `{"answers":`+answers+`}`, header...)
	}
	wantError := func(why string, res answer, status int, code string) {
		t.Helper()
		if res.status != status || str(res.json(t), "error") != code {
			t.Errorf("%s: %d %s, want %d %s", why, res.status, res.body, status, code)
		}
	}

	ana, anaHeader := quickSupportBy(t, w, "operator", "Ana", "523456791", "allow")
	if res := ana.do(http.MethodGet, "/api/support/session/feedback", ""); res.status != http.StatusUnauthorized {
		t.Errorf("without the token: %d", res.status)
	}
	wantError("a form never set up", get(ana, anaHeader), http.StatusNotFound, "feedback_off")
	saveForm(t, w, `{"enabled":true,"questions":[]}`)
	saveForm(t, w, `{"enabled":true,"questions":[
		{"id":"q1","kind":"rating","label":"How was the help?","required":true},
		{"id":"q2","kind":"text","label":"Anything else?","long":true},
		{"kind":"choice","label":"Which part?","options":["Mail","Printer"]},
		{"kind":"yesno","label":"Solved?"}]}`)

	// Before Allow, and declined: no feedback
	early, earlyHeader := quickSupportBy(t, w, "operator", "Ben", "523456792", "")
	wantError("before Allow", get(early, earlyHeader), http.StatusConflict, "wrong_state")
	declined, declinedHeader := quickSupportBy(t, w, "operator", "Cleo", "523456793", "decline")
	wantError("declined", get(declined, declinedHeader), http.StatusConflict, "wrong_state")
	wantError("declined, giving", give(declined, declinedHeader, `{"q1":5}`), http.StatusConflict, "wrong_state")
	early.do(http.MethodPost, "/api/support/session/end", `{}`, earlyHeader...)
	wantError("ended before Allow", get(early, earlyHeader), http.StatusConflict, "wrong_state")

	// While allowed: the form as it is now
	res := get(ana, anaHeader)
	if res.status != http.StatusOK {
		t.Fatalf("the form: %d %s", res.status, res.body)
	}
	var got struct {
		Success bool                         `json:"success"`
		Given   bool                         `json:"given"`
		Form    map[string][]json.RawMessage `json:"form"`
	}
	if err := json.Unmarshal([]byte(res.body), &got); err != nil || !got.Success || got.Given || len(got.Form["questions"]) != 4 {
		t.Fatalf("the form: %s", res.body)
	}
	for i, want := range []string{
		`{"id":"q1","kind":"rating","label":"How was the help?","required":true}`,
		`{"id":"q2","kind":"text","label":"Anything else?","required":false,"long":true}`,
		`{"id":"q3","kind":"choice","label":"Which part?","required":false,"options":["Mail","Printer"]}`,
		`{"id":"q4","kind":"yesno","label":"Solved?","required":false}`,
	} {
		if string(got.Form["questions"][i]) != want {
			t.Errorf("question %d: %s, want %s", i+1, got.Form["questions"][i], want)
		}
	}

	for why, answers := range map[string]string{
		"an unknown id":           `{"q1":5,"q9":"x"}`,
		"the required one gone":   `{"q2":"fine"}`,
		"a required null":         `{"q1":null}`,
		"stars as text":           `{"q1":"5"}`,
		"no stars":                `{"q1":0}`,
		"six stars":               `{"q1":6}`,
		"half a star":             `{"q1":4.5}`,
		"text as a number":        `{"q1":5,"q2":3}`,
		"text over 2000":          `{"q1":5,"q2":"` + strings.Repeat("ä", 2001) + `"}`,
		"a choice not offered":    `{"q1":5,"q3":"Phone"}`,
		"yes or no as text":       `{"q1":5,"q4":"yes"}`,
		"answers that are a list": `["q1"]`,
	} {
		wantError(why, give(ana, anaHeader, answers), http.StatusBadRequest, "invalid_answers")
	}
	// Ana's app ends the session; the answer still comes in, once
	ana.do(http.MethodPost, "/api/support/session/end", `{}`, anaHeader...)
	if res := ana.do(http.MethodGet, "/api/support/session", "", anaHeader...); res.status != http.StatusUnauthorized {
		t.Fatalf("the other routes take an ended session's token: %d %s", res.status, res.body)
	}
	if res := give(ana, anaHeader, `{"q1":4,"q2":"`+strings.Repeat("ä", 2000)+`","q3":"Printer","q4":true}`); res.status != http.StatusOK || res.body != `{"success":true}` {
		t.Fatalf("the answer after the end: %d %s", res.status, res.body)
	}
	wantError("a second answer", give(ana, anaHeader, `{"q1":5}`), http.StatusConflict, "already_given")
	if res := get(ana, anaHeader); res.status != http.StatusOK || res.json(t)["given"] != true {
		t.Fatalf("given after the answer: %d %s", res.status, res.body)
	}

	// Dan answers while the session runs, for the admin, with what is optional left out
	dan, danHeader := quickSupportBy(t, w, "admin", "Dan", "523456794", "allow")
	if res := give(dan, danHeader, `{"q1":2,"q2":"   ","q3":""}`); res.status != http.StatusOK {
		t.Fatalf("Dan's answer: %d %s", res.status, res.body)
	}
	// A later edit does not change what was asked
	saveForm(t, w, `{"enabled":true,"questions":[{"id":"q1","kind":"rating","label":"Stars, please","required":true}]}`)
	// Switched off, nobody is asked
	eve, eveHeader := quickSupportBy(t, w, "operator", "Eve", "523456795", "allow")
	saveForm(t, w, `{"enabled":false,"questions":[{"id":"q1","kind":"rating","label":"Stars, please","required":true}]}`)
	wantError("switched off", get(eve, eveHeader), http.StatusNotFound, "feedback_off")
	wantError("switched off, giving", give(eve, eveHeader, `{"q1":5}`), http.StatusNotFound, "feedback_off")

	// The list: newest first, with who, the computer, the person and the stars
	for _, role := range []string{"viewer", "guest_issuer"} {
		if res := w.browser(role).do(http.MethodGet, "/api/support/feedback", ""); res.status != http.StatusForbidden {
			t.Errorf("%s reads the answers: %d", role, res.status)
		}
	}
	list := w.browser("operator").do(http.MethodGet, "/api/support/feedback", "")
	if list.status != http.StatusOK {
		t.Fatalf("list: %d %s", list.status, list.body)
	}
	var all struct {
		Feedback []struct {
			Helper     string `json:"helper"`
			HelperID   int64  `json:"helper_id"`
			Hostname   string `json:"hostname"`
			DeviceID   string `json:"device_id"`
			Name       string `json:"name"`
			Email      string `json:"email"`
			StartedAt  string `json:"started_at"`
			EndedAt    string `json:"ended_at"`
			AnsweredAt string `json:"answered_at"`
			Stars      *int   `json:"stars"`
			Answers    []struct {
				ID    string `json:"id"`
				Kind  string `json:"kind"`
				Label string `json:"label"`
				Value any    `json:"value"`
			} `json:"answers"`
		} `json:"feedback"`
		Count   int      `json:"count"`
		Average *float64 `json:"average"`
		Helpers []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"helpers"`
	}
	if err := json.Unmarshal([]byte(list.body), &all); err != nil {
		t.Fatal(err)
	}
	if all.Count != 2 || len(all.Feedback) != 2 || all.Average == nil || *all.Average != 3 || len(all.Helpers) != 2 {
		t.Fatalf("list: %s", list.body)
	}
	newest, older := all.Feedback[0], all.Feedback[1]
	if newest.Name != "Dan" || newest.Helper != "e2e_admin" || newest.EndedAt != "" || *newest.Stars != 2 || len(newest.Answers) != 1 {
		t.Errorf("Dan's answer: %+v", newest)
	}
	if older.Name != "Ana" || older.Email != "ana@example.com" || older.Hostname != "ana-laptop" || older.DeviceID != "523456791" ||
		older.Helper != "Olga Operator" || older.HelperID != 5 || older.EndedAt == "" || older.StartedAt == "" || older.AnsweredAt == "" || *older.Stars != 4 {
		t.Errorf("Ana's answer: %+v", older)
	}
	if len(older.Answers) != 4 || older.Answers[0].Label != "How was the help?" || older.Answers[2].Value != "Printer" || older.Answers[3].Value != true {
		t.Errorf("Ana's answers as asked: %+v", older.Answers)
	}
	one := w.browser("operator").do(http.MethodGet, "/api/support/feedback?helper=5", "").json(t)
	if one["count"] != float64(1) || one["average"] != float64(4) || len(one["helpers"].([]any)) != 2 {
		t.Errorf("one helper's answers: %v", one)
	}
	if res := w.browser("operator").do(http.MethodGet, "/api/support/feedback?helper=x", ""); res.status != http.StatusBadRequest {
		t.Errorf("a helper that is no id: %d", res.status)
	}
}
