package tickets_test

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
)

// fix is a recorded request whose answer a bug fix changed on purpose: the
// Go console answers status and answer where Node answered what the
// recording holds. Where Node's request left rows that later steps read,
// after brings about the same rows by other means (the request as the web
// app sends it, or the rows themselves), so that the steps after it still
// compare with Node's answers.
type fix struct {
	bug    string
	status int
	answer string
	after  func(t *testing.T, w *replay.World, step replay.Step)
}

// fixKey names a recorded request: role, method, path and body.
func fixKey(step replay.Step) string {
	body := ""
	if step.Body != nil {
		body = *step.Body
	}
	return strings.Join([]string{step.Role, step.Method, step.Path, body}, " ")
}

// resend sends the step's request again with another body, which has to
// get Node's answer to the recorded one.
func resend(body string) func(t *testing.T, w *replay.World, step replay.Step) {
	return resendBy(body, body)
}

// resendBy is resend with a body per database, for a value Node stored
// differently on each; "" sends nothing on that database, where Node's
// request stored nothing.
func resendBy(sqlite, postgres string) func(t *testing.T, w *replay.World, step replay.Step) {
	return func(t *testing.T, w *replay.World, step replay.Step) {
		t.Helper()
		body := w.DB.Pick(sqlite, postgres)
		if body == "" {
			return
		}
		got := w.Do(replay.Request{Role: step.Role, Method: step.Method, Path: step.Path, Body: replay.Text(body), Headers: step.Headers})
		if got.Status != step.Status || replay.Normalize(got.Body) != replay.Normalize(step.Answer) {
			t.Errorf("%s again as %s: %d %s, want Node's %d %s", step.Name, body, got.Status, got.Body, step.Status, step.Answer)
		}
	}
}

// write runs statements that write what Node's request wrote; <now> in
// them is the database's clock.
func write(queries ...string) func(t *testing.T, w *replay.World, step replay.Step) {
	return func(t *testing.T, w *replay.World, step replay.Step) {
		t.Helper()
		for _, query := range queries {
			w.Exec(strings.ReplaceAll(query, "<now>", store.NowSQL(w.DB.Dialect())), nil)
		}
	}
}

// updated is the audit entry of a change of a ticket by admin.
func updated(ticket, columns string) string {
	return "INSERT INTO audit_log (user_id, action, details, ip_address) VALUES (1, 'ticket_updated', 'Ticket #" + ticket + " updated: " + columns + "', '203.0.113.7')"
}

// resendFile uploads the bytes Node made of the recorded data, as base64.
func resendFile(filename string, file ...byte) func(t *testing.T, w *replay.World, step replay.Step) {
	return resend(`{"filename":"` + filename + `","data":"` + base64.StdEncoding.EncodeToString(file) + `"}`)
}

// emptyFile stores the empty file Node made of the recorded data, with
// its row, as the route stores a file: the web app cannot upload one.
func emptyFile(filename string) func(t *testing.T, w *replay.World, step replay.Step) {
	return func(t *testing.T, w *replay.World, step replay.Step) {
		t.Helper()
		ticket := strings.Split(step.Path, "/")[3]
		entries, err := os.ReadDir(uploads(w))
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(uploads(w), fmt.Sprintf("%s_%016x_%s", ticket, len(entries), filename))
		if err := os.WriteFile(path, nil, 0o666); err != nil {
			t.Fatal(err)
		}
		w.Exec("INSERT INTO ticket_attachments (ticket_id, filename, mimetype, size_bytes, storage_path, uploaded_by) VALUES (?, ?, 'application/octet-stream', 0, ?, ?)",
			[]any{ticket, filename, path, replay.Users[step.Role].Username})
	}
}

// fixList holds the steps of a recording that the bug fixes change, by
// fixKey.
type fixList map[string]fix

// ticketFixes are the changed steps of testdata/node_*.json, deviceFixes
// those of testdata/node_device_*.json.
var ticketFixes, deviceFixes = fixList{}, fixList{}

// add puts the steps of a bug fix on the list.
func (l fixList) add(bug string, steps map[string]fix) bool {
	for key, f := range steps {
		if _, twice := l[key]; twice {
			panic("two fixes for " + key)
		}
		f.bug = bug
		l[key] = f
	}
	return true
}

var dataRefused = `{"error":"Filename and data (base64) are required"}`

// TK6: file data is base64 text and nothing else.
var _ = ticketFixes.add("TK6", map[string]fix{
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":5}`:                                               {status: 400, answer: dataRefused},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":true}`:                                            {status: 400, answer: dataRefused},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":[104,105]}`:                                       {status: 400, answer: dataRefused, after: resendFile("data.bin", 104, 105)},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":[]}`:                                              {status: 400, answer: dataRefused, after: emptyFile("data.bin")},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":[300,-1,"65",null,1.9,[66],{},true,"x",255,256]}`: {status: 400, answer: dataRefused, after: resendFile("data.bin", 44, 255, 65, 0, 1, 66, 0, 1, 0, 255, 0)},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":{}}`:                                              {status: 400, answer: dataRefused},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":{"type":"Buffer","data":[1,2,3]}}`:                {status: 400, answer: dataRefused, after: resendFile("data.bin", 1, 2, 3)},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":{"type":"Buffer","data":"ab"}}`:                   {status: 400, answer: dataRefused},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":{"0":65,"1":66,"length":2}}`:                      {status: 400, answer: dataRefused, after: resendFile("data.bin", 65, 66)},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":{"0":65,"length":"2"}}`:                           {status: 400, answer: dataRefused, after: emptyFile("data.bin")},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":{"length":-1}}`:                                   {status: 400, answer: dataRefused, after: emptyFile("data.bin")},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":{"0":7,"length":1.5}}`:                            {status: 400, answer: dataRefused, after: resendFile("data.bin", 7)},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":{"length":0}}`:                                    {status: 400, answer: dataRefused, after: emptyFile("data.bin")},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":{"data":[1]}}`:                                    {status: 400, answer: dataRefused},
})

var (
	titleRequired   = `{"error":"Title is required"}`
	commentRequired = `{"error":"Comment body is required"}`
)

// TK2: a title and a comment are text with something in it, also in a
// change.
var _ = ticketFixes.add("TK2", map[string]fix{
	`admin POST /api/tickets {"title":5}`:               {status: 400, answer: titleRequired},
	`admin POST /api/tickets {"title":true}`:            {status: 400, answer: titleRequired},
	`admin POST /api/tickets {"title":["x"]}`:           {status: 400, answer: titleRequired},
	`admin POST /api/tickets {"title":[]}`:              {status: 400, answer: titleRequired},
	`admin POST /api/tickets {"title":{"a":1}}`:         {status: 400, answer: titleRequired},
	`admin PATCH /api/tickets/2 {"title":""}`:           {status: 400, answer: titleRequired, after: write("UPDATE tickets SET title = '', updated_at = <now> WHERE id = 2", updated("2", "title"))},
	`admin PATCH /api/tickets/2 {"title":null}`:         {status: 400, answer: titleRequired},
	`admin PATCH /api/tickets/2 {"title":5}`:            {status: 400, answer: titleRequired},
	`admin PATCH /api/tickets/2 {"title":["x"]}`:        {status: 400, answer: titleRequired},
	`admin POST /api/tickets/5/comments {"body":5}`:     {status: 400, answer: commentRequired},
	`admin POST /api/tickets/5/comments {"body":true}`:  {status: 400, answer: commentRequired},
	`admin POST /api/tickets/5/comments {"body":["x"]}`: {status: 400, answer: commentRequired},
	`admin POST /api/tickets/5/comments {"body":{}}`:    {status: 400, answer: commentRequired},
})

// DV5: the title and the description of a device's ticket are text.
var _ = deviceFixes.add("DV5", map[string]fix{
	` POST /api/tickets/bd {"title":5}`:     {status: 400, answer: titleRequired},
	` POST /api/tickets/bd {"title":true}`:  {status: 400, answer: titleRequired},
	` POST /api/tickets/bd {"title":[]}`:    {status: 400, answer: titleRequired},
	` POST /api/tickets/bd {"title":["a"]}`: {status: 400, answer: titleRequired},
	` POST /api/tickets/bd {"title":{}}`:    {status: 400, answer: titleRequired},
	` POST /api/tickets/bd {"title":"Description number","description":7}`: {status: 400, answer: `{"error":"Invalid description"}`,
		after: resendBy(`{"title":"Description number","description":"7.0"}`, `{"title":"Description number","description":"7"}`)},
	` POST /api/tickets/bd {"title":"Description zero","description":0}`:      {status: 400, answer: `{"error":"Invalid description"}`, after: resend(`{"title":"Description zero"}`)},
	` POST /api/tickets/bd {"title":"Description false","description":false}`: {status: 400, answer: `{"error":"Invalid description"}`, after: resend(`{"title":"Description false"}`)},
	` POST /api/tickets/bd {"title":"Description list","description":["a","b"]}`: {status: 400, answer: `{"error":"Invalid description"}`,
		after: resendBy("", `{"title":"Description list","description":"{\"a\",\"b\"}"}`)},
	` POST /api/tickets/bd {"title":"Description object","description":{"a":1}}`: {status: 400, answer: `{"error":"Invalid description"}`,
		after: resendBy("", `{"title":"Description object","description":"{\"a\":1}"}`)},
	` POST /api/tickets/bd {"title":"Description true","description":true}`: {status: 400, answer: `{"error":"Invalid description"}`,
		after: resendBy("", `{"title":"Description true","description":"true"}`)},
})

// apply compares a step on the list with the answer its bug fix
// gives, and brings about the rows Node's answer left. It reports whether
// the step is one of them, and counts it in used.
func (l fixList) apply(t *testing.T, w *replay.World, step replay.Step, got replay.Answer, used map[string]int) bool {
	t.Helper()
	key := fixKey(step)
	f, listed := l[key]
	if !listed {
		return false
	}
	used[key]++
	if got.Status != f.status || replay.Normalize(got.Body) != replay.Normalize(f.answer) {
		t.Errorf("%s (%s): %s\n   fix: %d %s\n    go: %d %s", step.Name, f.bug, key, f.status, f.answer, got.Status, got.Body)
	}
	if f.after != nil {
		f.after(t, w, step)
	}
	return true
}

// unused fails for entries of the list that no step of the
// recording named, so the list holds only steps there are.
func (l fixList) unused(t *testing.T, used map[string]int) {
	t.Helper()
	for key, f := range l {
		if used[key] == 0 {
			t.Errorf("the fix of %s names no step of the recording: %s", f.bug, key)
		}
	}
}
