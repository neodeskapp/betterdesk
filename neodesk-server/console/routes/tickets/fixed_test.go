package tickets_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// updated is the audit entry of a change of a ticket by admin.
func updated(ticket, columns string) string {
	return "INSERT INTO audit_log (user_id, action, details, ip_address) VALUES (1, 'ticket_updated', 'Ticket #" + ticket + " updated: " + columns + "', '203.0.113.7')"
}

// resendFile uploads the bytes Node made of the recorded data, as base64.
func resendFile(filename string, file ...byte) func(t *testing.T, w *replay.World, step replay.Step) {
	return replay.Resend(`{"filename":"` + filename + `","data":"` + base64.StdEncoding.EncodeToString(file) + `"}`)
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

// ticketFixes are the changed steps of testdata/node_*.json, deviceFixes
// those of testdata/node_device_*.json.
var ticketFixes, deviceFixes replay.Fixes

var dataRefused = `{"error":"Filename and data (base64) are required"}`

// TK6: file data is base64 text and nothing else.
var _ = ticketFixes.Add("TK6", map[string]replay.Fix{
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":5}`:                                               {Status: 400, Answer: dataRefused},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":true}`:                                            {Status: 400, Answer: dataRefused},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":[104,105]}`:                                       {Status: 400, Answer: dataRefused, After: resendFile("data.bin", 104, 105)},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":[]}`:                                              {Status: 400, Answer: dataRefused, After: emptyFile("data.bin")},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":[300,-1,"65",null,1.9,[66],{},true,"x",255,256]}`: {Status: 400, Answer: dataRefused, After: resendFile("data.bin", 44, 255, 65, 0, 1, 66, 0, 1, 0, 255, 0)},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":{}}`:                                              {Status: 400, Answer: dataRefused},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":{"type":"Buffer","data":[1,2,3]}}`:                {Status: 400, Answer: dataRefused, After: resendFile("data.bin", 1, 2, 3)},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":{"type":"Buffer","data":"ab"}}`:                   {Status: 400, Answer: dataRefused},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":{"0":65,"1":66,"length":2}}`:                      {Status: 400, Answer: dataRefused, After: resendFile("data.bin", 65, 66)},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":{"0":65,"length":"2"}}`:                           {Status: 400, Answer: dataRefused, After: emptyFile("data.bin")},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":{"length":-1}}`:                                   {Status: 400, Answer: dataRefused, After: emptyFile("data.bin")},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":{"0":7,"length":1.5}}`:                            {Status: 400, Answer: dataRefused, After: resendFile("data.bin", 7)},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":{"length":0}}`:                                    {Status: 400, Answer: dataRefused, After: emptyFile("data.bin")},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":{"data":[1]}}`:                                    {Status: 400, Answer: dataRefused},
})

var (
	titleRequired   = `{"error":"Title is required"}`
	commentRequired = `{"error":"Comment body is required"}`
)

// TK2: a title and a comment are text with something in it, also in a
// change.
var _ = ticketFixes.Add("TK2", map[string]replay.Fix{
	`admin POST /api/tickets {"title":5}`:               {Status: 400, Answer: titleRequired},
	`admin POST /api/tickets {"title":true}`:            {Status: 400, Answer: titleRequired},
	`admin POST /api/tickets {"title":["x"]}`:           {Status: 400, Answer: titleRequired},
	`admin POST /api/tickets {"title":[]}`:              {Status: 400, Answer: titleRequired},
	`admin POST /api/tickets {"title":{"a":1}}`:         {Status: 400, Answer: titleRequired},
	`admin PATCH /api/tickets/2 {"title":""}`:           {Status: 400, Answer: titleRequired, After: replay.Write("UPDATE tickets SET title = '', updated_at = <now> WHERE id = 2", updated("2", "title"))},
	`admin PATCH /api/tickets/2 {"title":null}`:         {Status: 400, Answer: titleRequired},
	`admin PATCH /api/tickets/2 {"title":5}`:            {Status: 400, Answer: titleRequired},
	`admin PATCH /api/tickets/2 {"title":["x"]}`:        {Status: 400, Answer: titleRequired},
	`admin POST /api/tickets/5/comments {"body":5}`:     {Status: 400, Answer: commentRequired},
	`admin POST /api/tickets/5/comments {"body":true}`:  {Status: 400, Answer: commentRequired},
	`admin POST /api/tickets/5/comments {"body":["x"]}`: {Status: 400, Answer: commentRequired},
	`admin POST /api/tickets/5/comments {"body":{}}`:    {Status: 400, Answer: commentRequired},
})

// DV5: the title and the description of a device's ticket are text.
var _ = deviceFixes.Add("DV5", map[string]replay.Fix{
	` POST /api/tickets/bd {"title":5}`:     {Status: 400, Answer: titleRequired},
	` POST /api/tickets/bd {"title":true}`:  {Status: 400, Answer: titleRequired},
	` POST /api/tickets/bd {"title":[]}`:    {Status: 400, Answer: titleRequired},
	` POST /api/tickets/bd {"title":["a"]}`: {Status: 400, Answer: titleRequired},
	` POST /api/tickets/bd {"title":{}}`:    {Status: 400, Answer: titleRequired},
	` POST /api/tickets/bd {"title":"Description number","description":7}`: {Status: 400, Answer: `{"error":"Invalid description"}`,
		After: replay.ResendBy(`{"title":"Description number","description":"7.0"}`, `{"title":"Description number","description":"7"}`)},
	` POST /api/tickets/bd {"title":"Description zero","description":0}`:      {Status: 400, Answer: `{"error":"Invalid description"}`, After: replay.Resend(`{"title":"Description zero"}`)},
	` POST /api/tickets/bd {"title":"Description false","description":false}`: {Status: 400, Answer: `{"error":"Invalid description"}`, After: replay.Resend(`{"title":"Description false"}`)},
	` POST /api/tickets/bd {"title":"Description list","description":["a","b"]}`: {Status: 400, Answer: `{"error":"Invalid description"}`,
		After: replay.ResendBy("", `{"title":"Description list","description":"{\"a\",\"b\"}"}`)},
	` POST /api/tickets/bd {"title":"Description object","description":{"a":1}}`: {Status: 400, Answer: `{"error":"Invalid description"}`,
		After: replay.ResendBy("", `{"title":"Description object","description":"{\"a\":1}"}`)},
	` POST /api/tickets/bd {"title":"Description true","description":true}`: {Status: 400, Answer: `{"error":"Invalid description"}`,
		After: replay.ResendBy("", `{"title":"Description true","description":"true"}`)},
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
	return replay.ResendBy(body(sqlite), body(postgres))
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
var _ = ticketFixes.Add("TK1", map[string]replay.Fix{
	`admin POST /api/tickets {"title":"Ticket 3","description":0}`:          {Status: 400, Answer: invalidDescription, After: createdAs("Ticket 3", "description", nil, nil)},
	`admin POST /api/tickets {"title":"Ticket 4","description":5}`:          {Status: 400, Answer: invalidDescription, After: createdAs("Ticket 4", "description", "5.0", "5")},
	`admin POST /api/tickets {"title":"Ticket 5","description":1.5}`:        {Status: 400, Answer: invalidDescription, After: createdAs("Ticket 5", "description", "1.5", "1.5")},
	`admin POST /api/tickets {"title":"Ticket 6","description":true}`:       {Status: 400, Answer: invalidDescription, After: createdAs("Ticket 6", "description", failed, "true")},
	`admin POST /api/tickets {"title":"Ticket 7","description":false}`:      {Status: 400, Answer: invalidDescription, After: createdAs("Ticket 7", "description", nil, nil)},
	`admin POST /api/tickets {"title":"Ticket 9","description":["one"]}`:    {Status: 400, Answer: invalidDescription, After: createdAs("Ticket 9", "description", "one", `{"one"}`)},
	`admin POST /api/tickets {"title":"Ticket 10","description":["a","b"]}`: {Status: 400, Answer: invalidDescription, After: createdAs("Ticket 10", "description", failed, `{"a","b"}`)},
	`admin POST /api/tickets {"title":"Ticket 11","description":[]}`:        {Status: 400, Answer: invalidDescription, After: createdAs("Ticket 11", "description", failed, "{}")},
	`admin POST /api/tickets {"title":"Ticket 12","description":[7]}`:       {Status: 400, Answer: invalidDescription, After: createdAs("Ticket 12", "description", "7.0", `{"7"}`)},
	`admin POST /api/tickets {"title":"Ticket 13","description":{}}`:        {Status: 400, Answer: invalidDescription, After: createdAs("Ticket 13", "description", failed, "{}")},
	`admin POST /api/tickets {"title":"Ticket 14","description":{"a":1}}`:   {Status: 400, Answer: invalidDescription, After: createdAs("Ticket 14", "description", failed, `{"a":1}`)},

	`admin POST /api/tickets {"title":"Ticket 17","device_id":0}`:         {Status: 400, Answer: invalidDevice, After: createdAs("Ticket 17", "device_id", nil, nil)},
	`admin POST /api/tickets {"title":"Ticket 18","device_id":5}`:         {Status: 400, Answer: invalidDevice, After: createdAs("Ticket 18", "device_id", "5.0", "5")},
	`admin POST /api/tickets {"title":"Ticket 19","device_id":1.5}`:       {Status: 400, Answer: invalidDevice, After: createdAs("Ticket 19", "device_id", "1.5", "1.5")},
	`admin POST /api/tickets {"title":"Ticket 20","device_id":true}`:      {Status: 400, Answer: invalidDevice, After: createdAs("Ticket 20", "device_id", failed, "true")},
	`admin POST /api/tickets {"title":"Ticket 21","device_id":false}`:     {Status: 400, Answer: invalidDevice, After: createdAs("Ticket 21", "device_id", nil, nil)},
	`admin POST /api/tickets {"title":"Ticket 23","device_id":["one"]}`:   {Status: 400, Answer: invalidDevice, After: createdAs("Ticket 23", "device_id", "one", `{"one"}`)},
	`admin POST /api/tickets {"title":"Ticket 24","device_id":["a","b"]}`: {Status: 400, Answer: invalidDevice, After: createdAs("Ticket 24", "device_id", failed, `{"a","b"}`)},
	`admin POST /api/tickets {"title":"Ticket 25","device_id":[]}`:        {Status: 400, Answer: invalidDevice, After: createdAs("Ticket 25", "device_id", failed, "{}")},
	`admin POST /api/tickets {"title":"Ticket 26","device_id":[7]}`:       {Status: 400, Answer: invalidDevice, After: createdAs("Ticket 26", "device_id", "7.0", `{"7"}`)},
	`admin POST /api/tickets {"title":"Ticket 27","device_id":{}}`:        {Status: 400, Answer: invalidDevice, After: createdAs("Ticket 27", "device_id", failed, "{}")},
	`admin POST /api/tickets {"title":"Ticket 28","device_id":{"a":1}}`:   {Status: 400, Answer: invalidDevice, After: createdAs("Ticket 28", "device_id", failed, `{"a":1}`)},

	`admin POST /api/tickets {"title":"Ticket 31","assigned_to":0}`:         {Status: 400, Answer: invalidAssignee, After: createdAs("Ticket 31", "assigned_to", nil, nil)},
	`admin POST /api/tickets {"title":"Ticket 32","assigned_to":5}`:         {Status: 400, Answer: invalidAssignee, After: createdAs("Ticket 32", "assigned_to", "5.0", "5")},
	`admin POST /api/tickets {"title":"Ticket 33","assigned_to":1.5}`:       {Status: 400, Answer: invalidAssignee, After: createdAs("Ticket 33", "assigned_to", "1.5", "1.5")},
	`admin POST /api/tickets {"title":"Ticket 34","assigned_to":true}`:      {Status: 400, Answer: invalidAssignee, After: createdAs("Ticket 34", "assigned_to", failed, "true")},
	`admin POST /api/tickets {"title":"Ticket 35","assigned_to":false}`:     {Status: 400, Answer: invalidAssignee, After: createdAs("Ticket 35", "assigned_to", nil, nil)},
	`admin POST /api/tickets {"title":"Ticket 37","assigned_to":["one"]}`:   {Status: 400, Answer: invalidAssignee, After: createdAs("Ticket 37", "assigned_to", "one", `{"one"}`)},
	`admin POST /api/tickets {"title":"Ticket 38","assigned_to":["a","b"]}`: {Status: 400, Answer: invalidAssignee, After: createdAs("Ticket 38", "assigned_to", failed, `{"a","b"}`)},
	`admin POST /api/tickets {"title":"Ticket 39","assigned_to":[]}`:        {Status: 400, Answer: invalidAssignee, After: createdAs("Ticket 39", "assigned_to", failed, "{}")},
	`admin POST /api/tickets {"title":"Ticket 40","assigned_to":[7]}`:       {Status: 400, Answer: invalidAssignee, After: createdAs("Ticket 40", "assigned_to", "7.0", `{"7"}`)},
	`admin POST /api/tickets {"title":"Ticket 41","assigned_to":{}}`:        {Status: 400, Answer: invalidAssignee, After: createdAs("Ticket 41", "assigned_to", failed, "{}")},
	`admin POST /api/tickets {"title":"Ticket 42","assigned_to":{"a":1}}`:   {Status: 400, Answer: invalidAssignee, After: createdAs("Ticket 42", "assigned_to", failed, `{"a":1}`)},

	// SQLite spread the list over the statement's parameters: the
	// description took the priority, the priority the category, and the
	// two device ids the category and the device
	`admin POST /api/tickets {"title":"Ticket 69","description":{},"device_id":["a","b"]}`: {Status: 400, Answer: invalidDescription, After: func(t *testing.T, w *replay.World, step replay.Step) {
		t.Helper()
		if w.DB.IsPostgres() {
			replay.Resend(`{"title":"Ticket 69","description":"{}","device_id":"{\"a\",\"b\"}"}`)(t, w, step)
			return
		}
		w.Exec("INSERT INTO tickets (title, description, priority, category, device_id, created_by, assigned_to, sla_due_at) VALUES ('Ticket 69', 'medium', 'general', 'a', 'b', 'e2e_admin', NULL, '2030-01-01T00:00:00.000Z')", nil)
		w.Exec("INSERT INTO audit_log (user_id, action, details, ip_address) VALUES (1, 'ticket_created', 'Ticket #55: Ticket 69', '203.0.113.7')", nil)
	}},

	`admin PATCH /api/tickets/2 {"description":5}`:                {Status: 400, Answer: invalidDescription, After: replay.ResendBy(`{"description":"5.0"}`, `{"description":"5"}`)},
	`admin PATCH /api/tickets/2 {"description":["a"]}`:            {Status: 400, Answer: invalidDescription, After: replay.ResendBy(`{"description":"a"}`, `{"description":"{\"a\"}"}`)},
	`admin PATCH /api/tickets/2 {"description":["a","b"]}`:        {Status: 400, Answer: invalidDescription, After: replay.ResendBy("", `{"description":"{\"a\",\"b\"}"}`)},
	`admin PATCH /api/tickets/2 {"description":{}}`:               {Status: 400, Answer: invalidDescription, After: replay.ResendBy("", `{"description":"{}"}`)},
	`admin PATCH /api/tickets/2 {"description":true}`:             {Status: 400, Answer: invalidDescription, After: replay.ResendBy("", `{"description":"true"}`)},
	`admin PATCH /api/tickets/2 {"category":"whatever you like"}`: {Status: 400, Answer: invalidCategory, After: set("category", "whatever you like", "whatever you like")},
	`admin PATCH /api/tickets/2 {"category":5}`:                   {Status: 400, Answer: invalidCategory, After: set("category", "5.0", "5")},
	`admin PATCH /api/tickets/2 {"category":null}`:                {Status: 400, Answer: invalidCategory, After: set("category", nil, nil)},
	`admin PATCH /api/tickets/2 {"category":true}`:                {Status: 400, Answer: invalidCategory, After: set("category", failed, "true")},
	`admin PATCH /api/tickets/2 {"assigned_to":7}`:                {Status: 400, Answer: invalidAssignee, After: replay.ResendBy(`{"assigned_to":"7.0"}`, `{"assigned_to":"7"}`)},
	`admin PATCH /api/tickets/2 {"sla_due_at":"not a date"}`:      {Status: 400, Answer: invalidDue, After: set("sla_due_at", "not a date", failed)},
	`admin PATCH /api/tickets/2 {"sla_due_at":5}`:                 {Status: 400, Answer: invalidDue, After: set("sla_due_at", "5.0", failed)},
	`admin PATCH /api/tickets/2 {"sla_due_at":""}`:                {Status: 400, Answer: invalidDue, After: set("sla_due_at", "", failed)},
})

var (
	invalidStatus   = `{"error":"Invalid status. Valid: open, in_progress, resolved, closed"}`
	invalidPriority = `{"error":"Invalid priority. Valid: low, medium, high, critical"}`
)

// TK3: a change checks status and priority whenever they are there.
var _ = ticketFixes.Add("TK3", map[string]replay.Fix{
	`admin PATCH /api/tickets/2 {"status":""}`:     {Status: 400, Answer: invalidStatus, After: set("status", "", "")},
	`admin PATCH /api/tickets/2 {"status":0}`:      {Status: 400, Answer: invalidStatus, After: set("status", "0.0", "0")},
	`admin PATCH /api/tickets/2 {"status":null}`:   {Status: 400, Answer: invalidStatus, After: set("status", nil, nil)},
	`admin PATCH /api/tickets/2 {"status":false}`:  {Status: 400, Answer: invalidStatus, After: set("status", failed, "false")},
	`admin PATCH /api/tickets/2 {"priority":null}`: {Status: 400, Answer: invalidPriority, After: set("priority", nil, nil)},
	`admin PATCH /api/tickets/2 {"priority":""}`:   {Status: 400, Answer: invalidPriority, After: set("priority", "", "")},
	`admin PATCH /api/tickets/2 {"priority":0}`:    {Status: 400, Answer: invalidPriority, After: set("priority", "0.0", "0")},
})

// TK4: a list filter is one text.
var _ = ticketFixes.Add("TK4", map[string]replay.Fix{
	"admin GET /api/tickets?status=open&status=closed ":         {Status: 400, Answer: `{"error":"Invalid status"}`},
	"admin GET /api/tickets?status[]=open ":                     {Status: 400, Answer: `{"error":"Invalid status"}`},
	"admin GET /api/tickets?status%5B%5D=open&priority[]=high ": {Status: 400, Answer: `{"error":"Invalid status"}`},
	"admin GET /api/tickets?status[0]=open ":                    {Status: 400, Answer: `{"error":"Invalid status"}`},
	"admin GET /api/tickets?status[1]=open&status[0]=closed ":   {Status: 400, Answer: `{"error":"Invalid status"}`},
	"admin GET /api/tickets?status[x]=open ":                    {Status: 400, Answer: `{"error":"Invalid status"}`},
	"admin GET /api/tickets?status[][]=open ":                   {Status: 400, Answer: `{"error":"Invalid status"}`},
	"admin GET /api/tickets?priority=high&priority= ":           {Status: 400, Answer: `{"error":"Invalid priority"}`},
	"admin GET /api/tickets?device_id[]=D1&status=open ":        {Status: 400, Answer: `{"error":"Invalid device_id"}`},
	"admin GET /api/tickets?assigned_to[]=e2e_viewer ":          {Status: 400, Answer: `{"error":"Invalid assigned_to"}`},
	"admin GET /api/tickets?category=network&category[]=x ":     {Status: 400, Answer: `{"error":"Invalid category"}`},
	"viewer GET /api/tickets?status[]=open ":                    {Status: 400, Answer: `{"error":"Invalid status"}`},
	"viewer GET /api/tickets?status=open&status=closed ":        {Status: 400, Answer: `{"error":"Invalid status"}`},
})

// TK7: file data is standard base64; white space in it is skipped.
var _ = ticketFixes.Add("TK7", map[string]replay.Fix{
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":"aGVsbG8"}`:  {Status: 400, Answer: dataRefused, After: resendFile("data.bin", 'h', 'e', 'l', 'l', 'o')},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":"a-_b"}`:     {Status: 400, Answer: dataRefused, After: resendFile("data.bin", 0x6b, 0xef, 0xdb)},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":"aGk=aGk="}`: {Status: 400, Answer: dataRefused, After: resendFile("data.bin", 'h', 'i')},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":"="}`:        {Status: 400, Answer: dataRefused, After: emptyFile("data.bin")},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":"=="}`:       {Status: 400, Answer: dataRefused, After: emptyFile("data.bin")},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":"a"}`:        {Status: 400, Answer: dataRefused, After: emptyFile("data.bin")},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":"ab"}`:       {Status: 400, Answer: dataRefused, After: resendFile("data.bin", 0x69)},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":"abc"}`:      {Status: 400, Answer: dataRefused, After: resendFile("data.bin", 0x69, 0xb7)},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":"!!!!"}`:     {Status: 400, Answer: dataRefused, After: emptyFile("data.bin")},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":"a!b@c#d$"}`: {Status: 400, Answer: dataRefused, After: resendFile("data.bin", 0x69, 0xb7, 0x1d)},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":"YQ==YQ=="}`: {Status: 400, Answer: dataRefused, After: resendFile("data.bin", 'a')},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":"YQ=YQ"}`:    {Status: 400, Answer: dataRefused, After: resendFile("data.bin", 'a')},
	`admin POST /api/tickets/5/attachments {"filename":"data.bin","data":"éééé"}`:     {Status: 400, Answer: dataRefused, After: emptyFile("data.bin")},
})

// mimeUpload is the recorded upload of mime.bin whose file names its type.
func mimeUpload(data, mimetype string) string {
	return `admin POST /api/tickets/5/attachments {"filename":"mime.bin","data":"` + data + `","mimetype":` + mimetype + `}`
}

// storedType writes the type Node stored for the attachment its answer
// names, on each database; failed means Node stored nothing there.
func storedType(sqlite, postgres any) func(t *testing.T, w *replay.World, step replay.Step) {
	return func(t *testing.T, w *replay.World, step replay.Step) {
		t.Helper()
		value := sqlite
		if w.DB.IsPostgres() {
			value = postgres
		}
		if value == failed || step.Status != 201 {
			return
		}
		var answer struct {
			Attachment struct {
				ID int64 `json:"id"`
			} `json:"attachment"`
		}
		if err := json.Unmarshal([]byte(step.Answer), &answer); err != nil {
			t.Fatal(err)
		}
		w.Exec("UPDATE ticket_attachments SET mimetype = ? WHERE id = ?", []any{value, answer.Attachment.ID})
	}
}

// both runs one stand-in after the other.
func both(first, second func(t *testing.T, w *replay.World, step replay.Step)) func(t *testing.T, w *replay.World, step replay.Step) {
	return func(t *testing.T, w *replay.World, step replay.Step) {
		t.Helper()
		first(t, w, step)
		second(t, w, step)
	}
}

// nodeUpload uploads the recorded file without its type when Node stored
// one there, and then writes the type Node stored.
func nodeUpload(data string, sqlite, postgres any) func(t *testing.T, w *replay.World, step replay.Step) {
	body := `{"filename":"mime.bin","data":"` + data + `"}`
	pick := func(value any) string {
		if value == failed {
			return ""
		}
		return body
	}
	return both(replay.ResendBy(pick(sqlite), pick(postgres)), storedType(sqlite, postgres))
}

var (
	invalidType = `{"error":"Invalid mimetype"}`
	longType    = strings.Repeat("x", 300)
	octet       = map[string]string{"Content-Type": "application/octet-stream", "Content-Disposition": `attachment; filename="mime.bin"`}
)

// TK8: an upload's type is text, stored only when it is a media type, and
// a download sends a stored type that is none as application/octet-stream.
var _ = ticketFixes.Add("TK8", map[string]replay.Fix{
	mimeUpload("bWltZSAw", `0`):               {Status: 400, Answer: invalidType, After: nodeUpload("bWltZSAw", "application/octet-stream", "application/octet-stream")},
	mimeUpload("bWltZSA1", `5`):               {Status: 400, Answer: invalidType, After: nodeUpload("bWltZSA1", "5.0", "5")},
	mimeUpload("bWltZSB0cnVl", `true`):        {Status: 400, Answer: invalidType, After: nodeUpload("bWltZSB0cnVl", failed, "true")},
	mimeUpload("bWltZSBbIngveSJd", `["x/y"]`): {Status: 400, Answer: invalidType, After: nodeUpload("bWltZSBbIngveSJd", "x/y", `{"x/y"}`)},
	mimeUpload("bWltZSB7fQ==", `{}`):          {Status: 400, Answer: invalidType, After: nodeUpload("bWltZSB7fQ==", failed, "{}")},
	mimeUpload("bWltZSAidGV4dC9odG1sXHJcblgtSW5qZWN0ZWQ6IDEi", `"text/html\r\nX-Injected: 1"`):     {Status: 201, Answer: `{"success":true,"attachment":{"id":18,"filename":"mime.bin","size_bytes":33}}`, Dialect: "sqlite", After: storedType("text/html\r\nX-Injected: 1", nil)},
	mimeUpload("bWltZSAidMOreHQvcGxhaW4i", `"tëxt/plain"`):                                         {Status: 201, Answer: `{"success":true,"attachment":{"id":19,"filename":"mime.bin","size_bytes":18}}`, Dialect: "sqlite", After: storedType("tëxt/plain", nil)},
	mimeUpload("bWltZSAiz4AveCI=", `"π/x"`):                                                        {Status: 201, Answer: `{"success":true,"attachment":{"id":20,"filename":"mime.bin","size_bytes":11}}`, Dialect: "sqlite", After: storedType("π/x", nil)},
	mimeUpload("bWltZSAiICI=", `" "`):                                                              {Status: 201, Answer: `{"success":true,"attachment":{"id":22,"filename":"mime.bin","size_bytes":8}}`, Dialect: "sqlite", After: storedType(" ", nil)},
	mimeUpload(base64.StdEncoding.EncodeToString([]byte(`mime "`+longType+`"`)), `"`+longType+`"`): {Status: 201, Answer: `{"success":true,"attachment":{"id":23,"filename":"mime.bin","size_bytes":307}}`, Dialect: "sqlite", After: storedType(longType, nil)},

	"admin GET /api/tickets/attachments/15 ": {Status: 200, Answer: "mime 5", Headers: octet, Dialect: "sqlite"},
	"admin GET /api/tickets/attachments/18 ": {Status: 200, Answer: `mime "text/html\r\nX-Injected: 1"`, Headers: octet, Dialect: "sqlite"},
	"admin GET /api/tickets/attachments/19 ": {Status: 200, Answer: `mime "tëxt/plain"`, Headers: octet, Dialect: "sqlite"},
	"admin GET /api/tickets/attachments/20 ": {Status: 200, Answer: `mime "π/x"`, Headers: octet, Dialect: "sqlite"},
	"admin GET /api/tickets/attachments/22 ": {Status: 200, Answer: `mime " "`, Headers: octet, Dialect: "sqlite"},
	"admin GET /api/tickets/attachments/23 ": {Status: 200, Answer: `mime "` + longType + `"`, Headers: octet, Dialect: "sqlite"},
})

var _ = ticketFixes.Add("TK8", map[string]replay.Fix{
	mimeUpload("bWltZSAidGV4dC9odG1sXHJcblgtSW5qZWN0ZWQ6IDEi", `"text/html\r\nX-Injected: 1"`):     {Status: 201, Answer: `{"success":true,"attachment":{"id":20,"filename":"mime.bin","size_bytes":33}}`, Dialect: "postgres", After: storedType(nil, "text/html\r\nX-Injected: 1")},
	mimeUpload("bWltZSAidMOreHQvcGxhaW4i", `"tëxt/plain"`):                                         {Status: 201, Answer: `{"success":true,"attachment":{"id":21,"filename":"mime.bin","size_bytes":18}}`, Dialect: "postgres", After: storedType(nil, "tëxt/plain")},
	mimeUpload("bWltZSAiz4AveCI=", `"π/x"`):                                                        {Status: 201, Answer: `{"success":true,"attachment":{"id":22,"filename":"mime.bin","size_bytes":11}}`, Dialect: "postgres", After: storedType(nil, "π/x")},
	mimeUpload("bWltZSAiICI=", `" "`):                                                              {Status: 201, Answer: `{"success":true,"attachment":{"id":24,"filename":"mime.bin","size_bytes":8}}`, Dialect: "postgres", After: storedType(nil, " ")},
	mimeUpload(base64.StdEncoding.EncodeToString([]byte(`mime "`+longType+`"`)), `"`+longType+`"`): {Status: 201, Answer: `{"success":true,"attachment":{"id":25,"filename":"mime.bin","size_bytes":307}}`, Dialect: "postgres", After: storedType(nil, longType)},

	"admin GET /api/tickets/attachments/15 ": {Status: 200, Answer: "mime 5", Headers: octet, Dialect: "postgres"},
	"admin GET /api/tickets/attachments/17 ": {Status: 200, Answer: "mime true", Headers: octet, Dialect: "postgres"},
	"admin GET /api/tickets/attachments/18 ": {Status: 200, Answer: `mime ["x/y"]`, Headers: octet, Dialect: "postgres"},
	"admin GET /api/tickets/attachments/19 ": {Status: 200, Answer: "mime {}", Headers: octet, Dialect: "postgres"},
	"admin GET /api/tickets/attachments/20 ": {Status: 200, Answer: `mime "text/html\r\nX-Injected: 1"`, Headers: octet, Dialect: "postgres"},
	"admin GET /api/tickets/attachments/21 ": {Status: 200, Answer: `mime "tëxt/plain"`, Headers: octet, Dialect: "postgres"},
	"admin GET /api/tickets/attachments/22 ": {Status: 200, Answer: `mime "π/x"`, Headers: octet, Dialect: "postgres"},
	"admin GET /api/tickets/attachments/24 ": {Status: 200, Answer: `mime " "`, Headers: octet, Dialect: "postgres"},
	"admin GET /api/tickets/attachments/25 ": {Status: 200, Answer: `mime "` + longType + `"`, Headers: octet, Dialect: "postgres"},
})

// TK10: a row finds its file by its name in the attachment folder. The
// rows of the recording that point at /etc/hostname and at a file above
// the folder name files the folder does not have, where Node refused the
// paths with 500 "outside allowed directory".
var _ = ticketFixes.Add("TK10", map[string]replay.Fix{
	"admin GET /api/tickets/attachments/2 ": {NodeStatus: 500, Status: 404, Answer: `{"error":"Attachment file missing"}`},
	"admin GET /api/tickets/attachments/3 ": {NodeStatus: 500, Status: 404, Answer: `{"error":"Attachment file missing"}`},
})

// C7: an id of digits beyond 2147483647 names no ticket on either
// database, where PostgreSQL failed with 500: the ticket is not found and
// its lists are empty, as on SQLite.
var _ = ticketFixes.Add("C7", map[string]replay.Fix{
	"admin GET /api/tickets/2147483648 ":                       {Dialect: "postgres", Status: 404, Answer: `{"error":"Ticket not found"}`},
	"admin GET /api/tickets/2147483648/comments ":              {Dialect: "postgres", Status: 200, Answer: `{"comments":[]}`},
	"admin GET /api/tickets/2147483648/attachments ":           {Dialect: "postgres", Status: 200, Answer: `{"attachments":[]}`},
	"admin GET /api/tickets/99999999999999999999 ":             {Dialect: "postgres", Status: 404, Answer: `{"error":"Ticket not found"}`},
	"admin GET /api/tickets/99999999999999999999/comments ":    {Dialect: "postgres", Status: 200, Answer: `{"comments":[]}`},
	"admin GET /api/tickets/99999999999999999999/attachments ": {Dialect: "postgres", Status: 200, Answer: `{"attachments":[]}`},
	"admin DELETE /api/tickets/99999999999999999999 ":          {Dialect: "postgres", Status: 404, Answer: `{"error":"Ticket not found"}`},
	"admin GET /api/tickets/attachments/2147483648 ":           {Dialect: "postgres", Status: 404, Answer: `{"error":"Attachment not found"}`},
	"admin GET /api/tickets/attachments/99999999999999999999 ": {Dialect: "postgres", Status: 404, Answer: `{"error":"Attachment not found"}`},
})

// S17: a range outside the file and a condition that does not hold say
// so, also in production, where Node said "Internal Server Error". The
// answer stays JSON without the attachment's name (TK9).
var _ = ticketFixes.Add("S17", map[string]replay.Fix{
	"admin GET /api/tickets/attachments/4 ": {NodeStatus: 416, Status: 416, Answer: `{"success":false,"error":"Range Not Satisfiable"}`,
		Headers: map[string]string{"Content-Type": "application/json; charset=utf-8", "Content-Disposition": "", "Content-Range": "bytes */12"}},
})
var _ = ticketFixes.Add("S17", map[string]replay.Fix{
	"admin GET /api/tickets/attachments/4 ": {NodeStatus: 412, Status: 412, Answer: `{"success":false,"error":"Precondition Failed"}`,
		Headers: map[string]string{"Content-Type": "application/json; charset=utf-8", "Content-Disposition": ""}},
})

// RV62: a viewer's counts take only the tickets assigned to it, as its
// list does.
var _ = ticketFixes.Add("RV62", map[string]replay.Fix{
	"viewer GET /api/tickets/stats ": {Status: 200, Answer: `{"total":2,"open":1,"in_progress":0,"resolved":1,"closed":0}`},
})

// RV62: internal comments are for accounts with device.edit; a
// guest_issuer and a server admin read a ticket without them (DECISIONS
// A98).
var internalLacks = []string{`"is_internal":1`, `"is_internal":true`}

var _ = ticketFixes.Add("RV62", map[string]replay.Fix{
	"guest_issuer GET /api/tickets/1 ":          {Status: 200, Has: []string{`"body":"public one"`, `"body":"public two"`}, Lacks: internalLacks},
	"guest_issuer GET /api/tickets/3 ":          {Status: 200, Has: []string{`"comments":[]`}, Lacks: internalLacks},
	"guest_issuer GET /api/tickets/1/comments ": {Status: 200, Has: []string{`"body":"public one"`}, Lacks: internalLacks},
	"guest_issuer GET /api/tickets/5 ":          {Status: 200, Has: []string{`"body":"trimmed"`}, Lacks: internalLacks},
	"guest_issuer GET /api/tickets/5/comments ": {Status: 200, Has: []string{`"body":"trimmed"`}, Lacks: internalLacks},
	"server_admin GET /api/tickets/1 ":          {Status: 200, Has: []string{`"body":"public one"`, `"body":"public two"`}, Lacks: internalLacks},
	"server_admin GET /api/tickets/3 ":          {Status: 200, Has: []string{`"comments":[]`}, Lacks: internalLacks},
	"server_admin GET /api/tickets/1/comments ": {Status: 200, Has: []string{`"body":"public one"`}, Lacks: internalLacks},
})
