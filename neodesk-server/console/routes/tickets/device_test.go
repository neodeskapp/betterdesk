package tickets

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/authn"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// The two computers of these tests and their device tokens.
const (
	deviceA, tokenA = "900100001", "device-token-of-the-computer-a"
	deviceB, tokenB = "900100002", "device-token-of-the-computer-b"
)

// pushed is a frame tellDevice sent.
type pushed struct {
	device, typ string
	fields      map[string]any
}

// deviceGroup is the ticket routes with the device tokens of A and B, and
// the frames sent to the computers' chat sockets.
type deviceGroup struct {
	*group
	mu     sync.Mutex
	frames []pushed
}

func newDeviceGroup(t *testing.T, db *store.DB) *deviceGroup {
	t.Helper()
	g := &deviceGroup{group: newGroup(t, db)}
	ctx := context.Background()
	for _, user := range testUsers {
		if _, err := db.Exec(ctx, `INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, 'x', ?)`, user.ID, user.Username, user.Role); err != nil {
			t.Fatal(err)
		}
	}
	for device, token := range map[string]string{deviceA: tokenA, deviceB: tokenB} {
		if _, err := db.Exec(ctx, `INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, token_kind) VALUES (?, ?, 1, ?, 'u', '2999-01-01 00:00:00', 'device')`,
			store.StoredAccessToken(authn.HashAccessToken(token)), authn.HashAccessToken(token), device); err != nil {
			t.Fatal(err)
		}
	}
	g.h.push = func(device, typ string, fields map[string]any) {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.frames = append(g.frames, pushed{device, typ, fields})
	}
	return g
}

// device sends a request with a computer's token.
func (g *deviceGroup) device(token, method, target, body string) *httptest.ResponseRecorder {
	g.t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.RemoteAddr = "203.0.113.20:1234"
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	g.serve.ServeHTTP(rec, req)
	return rec
}

// sent takes the frames sent so far.
func (g *deviceGroup) sent() []pushed {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := g.frames
	g.frames = nil
	return out
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%d %s: %v", rec.Code, rec.Body.String(), err)
	}
	return out
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// openTicket opens a ticket from computer A with a screenshot and a file,
// and returns its id.
func (g *deviceGroup) openTicket() string {
	g.t.Helper()
	body := `{"description":"My printer stops after one page\nIt started today","reporter":"Erika Muster","os":"Windows 11 Pro",` +
		`"screenshot":{"name":"screen.png","type":"image/png","data_b64":"` + b64("png bytes") + `"},` +
		`"files":[{"name":"log file.txt","type":"text/plain","data_b64":"` + b64("the log") + `"}]}`
	rec := g.device(tokenA, "POST", "/api/tickets/nd", body)
	if rec.Code != 201 {
		g.t.Fatalf("open a ticket: %d %s", rec.Code, rec.Body.String())
	}
	return strconv.Itoa(int(decode(g.t, rec)["ticket_id"].(float64)))
}

// A ticket the computer opens is the computer's: source device, its
// device, made by the agent, the reporter and the system it named, the
// title from the first line of the description, and its files with
// computer-info.json, which holds what the server stored about the
// computer. Staff see source and reporter; the list filters on source.
func TestATicketFromTheComputerIsTheComputers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		g := newDeviceGroup(t, db)
		ctx := context.Background()
		if _, err := db.Exec(ctx, `INSERT INTO device_inventory (device_id, hardware, software, collected_at, received_at) VALUES (?, ?, '{}', '2026-11-27T08:00:00Z', '2026-11-27T08:00:00Z')`,
			deviceA, `{"cpu":"Test CPU 3000"}`); err != nil {
			t.Fatal(err)
		}
		g.want(g.do("operator", "POST", "/api/tickets", `{"title":"Staff ticket","device_id":"`+deviceA+`"}`), 201, "")
		id := g.openTicket()

		staff := decode(t, g.do("admin", "GET", "/api/tickets/"+id, ""))
		for key, want := range map[string]any{"source": "device", "reporter": "Erika Muster", "device_id": deviceA,
			"created_by": "agent:" + deviceA, "title": "My printer stops after one page"} {
			if staff[key] != want {
				t.Errorf("%s: %v, want %v", key, staff[key], want)
			}
		}
		var names []string
		var info string
		for _, a := range staff["attachments"].([]any) {
			a := a.(map[string]any)
			names = append(names, a["filename"].(string))
			if a["filename"] == computerInfo {
				rec := g.do("admin", "GET", "/api/tickets/attachments/"+strconv.Itoa(int(a["id"].(float64))), "")
				info = rec.Body.String()
			}
		}
		if got := strings.Join(names, ", "); got != "screen.png, log_file.txt, computer-info.json" {
			t.Errorf("files: %s", got)
		}
		for _, part := range []string{`"device_id": "` + deviceA + `"`, `"reporter": "Erika Muster"`, `"os": "Windows 11 Pro"`, `"cpu": "Test CPU 3000"`} {
			if !strings.Contains(info, part) {
				t.Errorf("computer-info.json lacks %s: %s", part, info)
			}
		}

		// The staff list: every ticket names its source; source=device
		// lists the computers' tickets only
		all := decode(t, g.do("admin", "GET", "/api/tickets", ""))["tickets"].([]any)
		if len(all) != 2 || all[1].(map[string]any)["source"] != "staff" || all[1].(map[string]any)["reporter"] != nil {
			t.Errorf("the list: %v", all)
		}
		fromComputers := decode(t, g.do("admin", "GET", "/api/tickets?source=device", ""))["tickets"].([]any)
		if len(fromComputers) != 1 || number2(fromComputers[0]) != id {
			t.Errorf("source=device: %v", fromComputers)
		}

		// The computer's list holds its ticket and not staff's ticket about it
		list := decode(t, g.device(tokenA, "GET", "/api/tickets/nd", ""))
		tickets := list["tickets"].([]any)
		if len(tickets) != 1 || number2(tickets[0]) != id || tickets[0].(map[string]any)["unread"] != false || tickets[0].(map[string]any)["replies"] != float64(0) {
			t.Errorf("the computer's list: %v", list)
		}
	})
}

func number2(ticket any) string {
	return strconv.Itoa(int(ticket.(map[string]any)["id"].(float64)))
}

// A computer reaches only the tickets it opened: another computer's
// ticket and staff's ticket about it answer 404 as a ticket that isn't
// there, on every route, and nothing of them changes.
func TestAnotherComputersTicketIsNotThere(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		g := newDeviceGroup(t, db)
		id := g.openTicket()
		g.want(g.do("admin", "POST", "/api/tickets/"+id+"/comments", `{"body":"Did you turn it off and on?"}`), 201, "")
		staffTicket := strconv.Itoa(int(decode(t, g.do("operator", "POST", "/api/tickets", `{"title":"Staff ticket","device_id":"`+deviceB+`"}`))["ticket"].(map[string]any)["id"].(float64)))
		detail := decode(t, g.device(tokenA, "GET", "/api/tickets/nd/"+id, ""))
		var aid string
		for _, a := range detail["attachments"].([]any) {
			aid = strconv.Itoa(int(a.(map[string]any)["id"].(float64)))
		}
		g.sent()

		notFound, noFile := `{"error":"Ticket not found"}`, `{"error":"Attachment not found"}`
		for _, ticket := range []string{id, staffTicket, "999999"} {
			g.want(g.device(tokenB, "GET", "/api/tickets/nd/"+ticket, ""), 404, notFound)
			g.want(g.device(tokenB, "POST", "/api/tickets/nd/"+ticket+"/comments", `{"body":"not mine"}`), 404, notFound)
			g.want(g.device(tokenB, "POST", "/api/tickets/nd/"+ticket+"/read", ""), 404, notFound)
			g.want(g.device(tokenB, "GET", "/api/tickets/nd/"+ticket+"/attachments/"+aid, ""), 404, noFile)
		}
		// A's file under B's own ticket is no file of it either
		other := strconv.Itoa(int(decode(t, g.device(tokenB, "POST", "/api/tickets/nd", `{"title":"Mine"}`))["ticket_id"].(float64)))
		g.want(g.device(tokenB, "GET", "/api/tickets/nd/"+other+"/attachments/"+aid, ""), 404, noFile)
		if list := decode(t, g.device(tokenB, "GET", "/api/tickets/nd", ""))["tickets"].([]any); len(list) != 1 || number2(list[0]) != other {
			t.Errorf("B's list: %v", list)
		}
		// Staff's ticket about B is not B's either
		g.want(g.device(tokenB, "GET", "/api/tickets/nd/"+staffTicket, ""), 404, notFound)

		// Nothing of A's ticket changed: no comment of B, still unread for A
		after := decode(t, g.device(tokenA, "GET", "/api/tickets/nd/"+id, ""))
		if n := len(after["comments"].([]any)); n != 1 || after["ticket"].(map[string]any)["unread"] != true {
			t.Errorf("A's ticket after B: %d comments, %v", n, after["ticket"])
		}
		if got := g.sent(); len(got) != 0 {
			t.Errorf("frames sent for B's requests: %v", got)
		}
		// A reaches its own file
		if rec := g.device(tokenA, "GET", "/api/tickets/nd/"+id+"/attachments/"+aid, ""); rec.Code != 200 {
			t.Errorf("A's own file: %d %s", rec.Code, rec.Body.String())
		}
	})
}

// The computer gets the public comments and never the internal ones, nor
// a file staff put on the ticket. A staff member's public answer and a
// new status mark the ticket unread for it and send ticket.updated to its
// chat sockets; an internal note does neither. Reading and replying mark
// it read.
func TestTheComputerHearsOfPublicAnswersOnly(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		g := newDeviceGroup(t, db)
		id := g.openTicket()
		unread := func() any {
			return decode(t, g.device(tokenA, "GET", "/api/tickets/nd", ""))["tickets"].([]any)[0].(map[string]any)["unread"]
		}
		g.want(g.do("admin", "POST", "/api/tickets/"+id+"/comments", `{"body":"Internal: the printer is end of life","is_internal":true}`), 201, "")
		g.want(g.do("admin", "POST", "/api/tickets/"+id+"/attachments", `{"filename":"staff-notes.txt","data":"`+b64("staff only")+`"}`), 201, "")
		if got := g.sent(); len(got) != 0 || unread() != false {
			t.Errorf("an internal note: frames %v, unread %v", got, unread())
		}
		g.want(g.do("admin", "POST", "/api/tickets/"+id+"/comments", `{"body":"Please restart the printer"}`), 201, "")
		got := g.sent()
		if len(got) != 1 || got[0].device != deviceA || got[0].typ != "ticket.updated" {
			t.Fatalf("a public answer sent %v", got)
		}
		if fields, _ := json.Marshal(got[0].fields); string(fields) != `{"status":"open","ticket_id":`+id+`,"unread":true}` {
			t.Errorf("the frame's fields: %s", fields)
		}
		if unread() != true {
			t.Error("a public answer left the ticket read")
		}
		// The heartbeat's count, as the server's API reads it
		server := storetest.GoServer(t, db)
		if n, err := server.UnreadDeviceTickets(deviceA); err != nil || n != 1 {
			t.Errorf("unread for the heartbeat: %d %v", n, err)
		}
		if n, err := server.UnreadDeviceTickets(deviceB); err != nil || n != 0 {
			t.Errorf("unread of the other computer: %d %v", n, err)
		}

		detail := decode(t, g.device(tokenA, "GET", "/api/tickets/nd/"+id, ""))
		raw, _ := json.Marshal(detail)
		if strings.Contains(string(raw), "end of life") || strings.Contains(string(raw), "staff-notes") {
			t.Errorf("the computer sees staff's internal note or file: %s", raw)
		}
		comments := detail["comments"].([]any)
		if len(comments) != 1 || comments[0].(map[string]any)["from"] != "staff" || comments[0].(map[string]any)["author_name"] != "e2e_admin" {
			t.Errorf("comments: %v", comments)
		}
		if n := len(detail["attachments"].([]any)); n != 3 {
			t.Errorf("the computer's files: %v", detail["attachments"])
		}
		g.want(g.device(tokenA, "POST", "/api/tickets/nd/"+id+"/read", ""), 204, "")
		if unread() != false {
			t.Error("read left the ticket unread")
		}
		if n, err := server.UnreadDeviceTickets(deviceA); err != nil || n != 0 {
			t.Errorf("unread for the heartbeat after reading: %d %v", n, err)
		}

		// A new status
		g.want(g.do("admin", "PATCH", "/api/tickets/"+id, `{"status":"resolved"}`), 200, "")
		if got := g.sent(); len(got) != 1 || got[0].fields["status"] != "resolved" || unread() != true {
			t.Errorf("a new status: %v, unread %v", got, unread())
		}
		// A change of something else is not news for the computer
		g.want(g.do("admin", "PATCH", "/api/tickets/"+id, `{"priority":"high"}`), 200, "")
		if got := g.sent(); len(got) != 0 {
			t.Errorf("a new priority: %v", got)
		}

		// The computer's reply, with a file, is marked as the computer's
		rec := g.device(tokenA, "POST", "/api/tickets/nd/"+id+"/comments",
			`{"body":"Done, it works again","files":[{"name":"photo.jpg","type":"image/jpeg","data_b64":"`+b64("jpg")+`"}]}`)
		if rec.Code != 201 {
			t.Fatalf("reply: %d %s", rec.Code, rec.Body.String())
		}
		reply := decode(t, rec)["comment"].(map[string]any)
		if reply["from"] != "device" || reply["author_name"] != "Erika Muster" || reply["body"] != "Done, it works again" {
			t.Errorf("reply: %v", reply)
		}
		if unread() != false {
			t.Error("a reply left the ticket unread")
		}
		staff := decode(t, g.do("admin", "GET", "/api/tickets/"+id, ""))
		last := staff["comments"].([]any)[2].(map[string]any)
		if last["from"] != "device" || last["author"] != "agent:"+deviceA {
			t.Errorf("staff see the reply as %v", last)
		}
		detail = decode(t, g.device(tokenA, "GET", "/api/tickets/nd/"+id, ""))
		files := detail["attachments"].([]any)
		photo := files[len(files)-1].(map[string]any)
		if photo["name"] != "photo.jpg" || photo["comment_id"] != reply["id"] {
			t.Errorf("the reply's file: %v", photo)
		}
		if detail["ticket"].(map[string]any)["replies"] != float64(2) {
			t.Errorf("replies: %v", detail["ticket"])
		}
	})
}

// What a computer sends is checked: a title or a description, files that
// are {name, data_b64} with base64 data, 25 MB of them together, and a
// reporter of 200 characters at most.
func TestAComputersTicketIsChecked(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		g := newDeviceGroup(t, db)
		for body, want := range map[string]string{
			`{}`:                         `{"error":"Title is required"}`,
			`{"description":"   "}`:      `{"error":"Title is required"}`,
			`{"title":5}`:                `{"error":"Title is required"}`,
			`{"description":5}`:          `{"error":"Invalid description"}`,
			`{"title":"x","files":"a"}`:  `{"error":"Invalid files"}`,
			`{"title":"x","files":[{}]}`: `{"error":"Invalid file"}`,
			`{"title":"x","screenshot":{"name":"a.png","data_b64":"not base64!"}}`: `{"error":"Invalid file"}`,
			`{"title":"x","reporter":"` + strings.Repeat("r", 201) + `"}`:          `{"error":"Invalid reporter"}`,
			`{"title":"x","os":7}`: `{"error":"Invalid os"}`,
		} {
			g.want(g.device(tokenA, "POST", "/api/tickets/nd", body), 400, want)
		}
		var rows int
		if err := db.QueryRow(context.Background(), `SELECT COUNT(*) FROM tickets`).Scan(&rows); err != nil || rows != 0 {
			t.Errorf("tickets stored: %d %v", rows, err)
		}
		long := strings.Repeat("a", 100)
		rec := g.device(tokenA, "POST", "/api/tickets/nd", `{"description":"`+long+`"}`)
		if rec.Code != 201 {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		if title := decode(t, rec)["ticket"].(map[string]any)["title"]; title != strings.Repeat("a", 79)+"…" {
			t.Errorf("title from a long description: %v", title)
		}
	})
}
