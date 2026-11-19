package tickets_test

import (
	"encoding/base64"
	"encoding/json"
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

// failed stands for a value Node failed to store on a database: its
// request wrote nothing there.
const failed = "\x00failed"

// createdAs makes the ticket Node made of a member of the wrong type: the
// same title, the member with the text Node stored on each database (nil
// for none).
func createdAs(title, member string, sqlite, postgres any) func(t *testing.T, w *replay.World, step replay.Step) {
	body := func(value any) string {
		switch value {
		case failed:
			return ""
		case nil:
			return `{"title":"` + title + `"}`
		}
		text, _ := json.Marshal(value)
		return `{"title":"` + title + `","` + member + `":` + string(text) + `}`
	}
	return resendBy(body(sqlite), body(postgres))
}

// set writes the value Node stored in a column of ticket 2 on each
// database, with the audit entry of the change.
func set(column string, sqlite, postgres any) func(t *testing.T, w *replay.World, step replay.Step) {
	return func(t *testing.T, w *replay.World, step replay.Step) {
		t.Helper()
		value := sqlite
		if w.DB.IsPostgres() {
			value = postgres
		}
		if value == failed {
			return
		}
		w.Exec("UPDATE tickets SET "+column+" = ?, updated_at = "+store.NowSQL(w.DB.Dialect())+" WHERE id = 2", []any{value})
		w.Exec(updated("2", column), nil)
	}
}

var (
	invalidDescription = `{"error":"Invalid description"}`
	invalidDevice      = `{"error":"Invalid device_id"}`
	invalidAssignee    = `{"error":"Invalid assigned_to"}`
	invalidCategory    = `{"error":"Invalid category. Valid: general, hardware, software, network, access, other"}`
	invalidDue         = `{"error":"Invalid sla_due_at"}`
)

// TK1: every member of a ticket has its type, on create and in a change.
var _ = ticketFixes.add("TK1", map[string]fix{
	`admin POST /api/tickets {"title":"Ticket 3","description":0}`:          {status: 400, answer: invalidDescription, after: createdAs("Ticket 3", "description", nil, nil)},
	`admin POST /api/tickets {"title":"Ticket 4","description":5}`:          {status: 400, answer: invalidDescription, after: createdAs("Ticket 4", "description", "5.0", "5")},
	`admin POST /api/tickets {"title":"Ticket 5","description":1.5}`:        {status: 400, answer: invalidDescription, after: createdAs("Ticket 5", "description", "1.5", "1.5")},
	`admin POST /api/tickets {"title":"Ticket 6","description":true}`:       {status: 400, answer: invalidDescription, after: createdAs("Ticket 6", "description", failed, "true")},
	`admin POST /api/tickets {"title":"Ticket 7","description":false}`:      {status: 400, answer: invalidDescription, after: createdAs("Ticket 7", "description", nil, nil)},
	`admin POST /api/tickets {"title":"Ticket 9","description":["one"]}`:    {status: 400, answer: invalidDescription, after: createdAs("Ticket 9", "description", "one", `{"one"}`)},
	`admin POST /api/tickets {"title":"Ticket 10","description":["a","b"]}`: {status: 400, answer: invalidDescription, after: createdAs("Ticket 10", "description", failed, `{"a","b"}`)},
	`admin POST /api/tickets {"title":"Ticket 11","description":[]}`:        {status: 400, answer: invalidDescription, after: createdAs("Ticket 11", "description", failed, "{}")},
	`admin POST /api/tickets {"title":"Ticket 12","description":[7]}`:       {status: 400, answer: invalidDescription, after: createdAs("Ticket 12", "description", "7.0", `{"7"}`)},
	`admin POST /api/tickets {"title":"Ticket 13","description":{}}`:        {status: 400, answer: invalidDescription, after: createdAs("Ticket 13", "description", failed, "{}")},
	`admin POST /api/tickets {"title":"Ticket 14","description":{"a":1}}`:   {status: 400, answer: invalidDescription, after: createdAs("Ticket 14", "description", failed, `{"a":1}`)},

	`admin POST /api/tickets {"title":"Ticket 17","device_id":0}`:         {status: 400, answer: invalidDevice, after: createdAs("Ticket 17", "device_id", nil, nil)},
	`admin POST /api/tickets {"title":"Ticket 18","device_id":5}`:         {status: 400, answer: invalidDevice, after: createdAs("Ticket 18", "device_id", "5.0", "5")},
	`admin POST /api/tickets {"title":"Ticket 19","device_id":1.5}`:       {status: 400, answer: invalidDevice, after: createdAs("Ticket 19", "device_id", "1.5", "1.5")},
	`admin POST /api/tickets {"title":"Ticket 20","device_id":true}`:      {status: 400, answer: invalidDevice, after: createdAs("Ticket 20", "device_id", failed, "true")},
	`admin POST /api/tickets {"title":"Ticket 21","device_id":false}`:     {status: 400, answer: invalidDevice, after: createdAs("Ticket 21", "device_id", nil, nil)},
	`admin POST /api/tickets {"title":"Ticket 23","device_id":["one"]}`:   {status: 400, answer: invalidDevice, after: createdAs("Ticket 23", "device_id", "one", `{"one"}`)},
	`admin POST /api/tickets {"title":"Ticket 24","device_id":["a","b"]}`: {status: 400, answer: invalidDevice, after: createdAs("Ticket 24", "device_id", failed, `{"a","b"}`)},
	`admin POST /api/tickets {"title":"Ticket 25","device_id":[]}`:        {status: 400, answer: invalidDevice, after: createdAs("Ticket 25", "device_id", failed, "{}")},
	`admin POST /api/tickets {"title":"Ticket 26","device_id":[7]}`:       {status: 400, answer: invalidDevice, after: createdAs("Ticket 26", "device_id", "7.0", `{"7"}`)},
	`admin POST /api/tickets {"title":"Ticket 27","device_id":{}}`:        {status: 400, answer: invalidDevice, after: createdAs("Ticket 27", "device_id", failed, "{}")},
	`admin POST /api/tickets {"title":"Ticket 28","device_id":{"a":1}}`:   {status: 400, answer: invalidDevice, after: createdAs("Ticket 28", "device_id", failed, `{"a":1}`)},

	`admin POST /api/tickets {"title":"Ticket 31","assigned_to":0}`:         {status: 400, answer: invalidAssignee, after: createdAs("Ticket 31", "assigned_to", nil, nil)},
	`admin POST /api/tickets {"title":"Ticket 32","assigned_to":5}`:         {status: 400, answer: invalidAssignee, after: createdAs("Ticket 32", "assigned_to", "5.0", "5")},
	`admin POST /api/tickets {"title":"Ticket 33","assigned_to":1.5}`:       {status: 400, answer: invalidAssignee, after: createdAs("Ticket 33", "assigned_to", "1.5", "1.5")},
	`admin POST /api/tickets {"title":"Ticket 34","assigned_to":true}`:      {status: 400, answer: invalidAssignee, after: createdAs("Ticket 34", "assigned_to", failed, "true")},
	`admin POST /api/tickets {"title":"Ticket 35","assigned_to":false}`:     {status: 400, answer: invalidAssignee, after: createdAs("Ticket 35", "assigned_to", nil, nil)},
	`admin POST /api/tickets {"title":"Ticket 37","assigned_to":["one"]}`:   {status: 400, answer: invalidAssignee, after: createdAs("Ticket 37", "assigned_to", "one", `{"one"}`)},
	`admin POST /api/tickets {"title":"Ticket 38","assigned_to":["a","b"]}`: {status: 400, answer: invalidAssignee, after: createdAs("Ticket 38", "assigned_to", failed, `{"a","b"}`)},
	`admin POST /api/tickets {"title":"Ticket 39","assigned_to":[]}`:        {status: 400, answer: invalidAssignee, after: createdAs("Ticket 39", "assigned_to", failed, "{}")},
	`admin POST /api/tickets {"title":"Ticket 40","assigned_to":[7]}`:       {status: 400, answer: invalidAssignee, after: createdAs("Ticket 40", "assigned_to", "7.0", `{"7"}`)},
	`admin POST /api/tickets {"title":"Ticket 41","assigned_to":{}}`:        {status: 400, answer: invalidAssignee, after: createdAs("Ticket 41", "assigned_to", failed, "{}")},
	`admin POST /api/tickets {"title":"Ticket 42","assigned_to":{"a":1}}`:   {status: 400, answer: invalidAssignee, after: createdAs("Ticket 42", "assigned_to", failed, `{"a":1}`)},

	// SQLite spread the list over the statement's parameters: the
	// description took the priority, the priority the category, and the
	// two device ids the category and the device
	`admin POST /api/tickets {"title":"Ticket 69","description":{},"device_id":["a","b"]}`: {status: 400, answer: invalidDescription, after: func(t *testing.T, w *replay.World, step replay.Step) {
		t.Helper()
		if w.DB.IsPostgres() {
			resend(`{"title":"Ticket 69","description":"{}","device_id":"{\"a\",\"b\"}"}`)(t, w, step)
			return
		}
		w.Exec("INSERT INTO tickets (title, description, priority, category, device_id, created_by, assigned_to, sla_due_at) VALUES ('Ticket 69', 'medium', 'general', 'a', 'b', 'e2e_admin', NULL, '2030-01-01T00:00:00.000Z')", nil)
		w.Exec("INSERT INTO audit_log (user_id, action, details, ip_address) VALUES (1, 'ticket_created', 'Ticket #55: Ticket 69', '203.0.113.7')", nil)
	}},

	`admin PATCH /api/tickets/2 {"description":5}`:                {status: 400, answer: invalidDescription, after: resendBy(`{"description":"5.0"}`, `{"description":"5"}`)},
	`admin PATCH /api/tickets/2 {"description":["a"]}`:            {status: 400, answer: invalidDescription, after: resendBy(`{"description":"a"}`, `{"description":"{\"a\"}"}`)},
	`admin PATCH /api/tickets/2 {"description":["a","b"]}`:        {status: 400, answer: invalidDescription, after: resendBy("", `{"description":"{\"a\",\"b\"}"}`)},
	`admin PATCH /api/tickets/2 {"description":{}}`:               {status: 400, answer: invalidDescription, after: resendBy("", `{"description":"{}"}`)},
	`admin PATCH /api/tickets/2 {"description":true}`:             {status: 400, answer: invalidDescription, after: resendBy("", `{"description":"true"}`)},
	`admin PATCH /api/tickets/2 {"category":"whatever you like"}`: {status: 400, answer: invalidCategory, after: set("category", "whatever you like", "whatever you like")},
	`admin PATCH /api/tickets/2 {"category":5}`:                   {status: 400, answer: invalidCategory, after: set("category", "5.0", "5")},
	`admin PATCH /api/tickets/2 {"category":null}`:                {status: 400, answer: invalidCategory, after: set("category", nil, nil)},
	`admin PATCH /api/tickets/2 {"category":true}`:                {status: 400, answer: invalidCategory, after: set("category", failed, "true")},
	`admin PATCH /api/tickets/2 {"assigned_to":7}`:                {status: 400, answer: invalidAssignee, after: resendBy(`{"assigned_to":"7.0"}`, `{"assigned_to":"7"}`)},
	`admin PATCH /api/tickets/2 {"sla_due_at":"not a date"}`:      {status: 400, answer: invalidDue, after: set("sla_due_at", "not a date", failed)},
	`admin PATCH /api/tickets/2 {"sla_due_at":5}`:                 {status: 400, answer: invalidDue, after: set("sla_due_at", "5.0", failed)},
	`admin PATCH /api/tickets/2 {"sla_due_at":""}`:                {status: 400, answer: invalidDue, after: set("sla_due_at", "", failed)},
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
