package console

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/authn"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
)

// The second computer of these tests and its device token.
const (
	otherDevice      = "900100002"
	otherDeviceToken = "device-token-for-the-chat-0002"
)

// twoComputersWorld is computerChatWorld with a second computer.
func twoComputersWorld(t *testing.T) (*world, *httptest.Server) {
	t.Helper()
	w, front, _ := computerChatWorld(t)
	ctx := context.Background()
	db := w.s.Deps().Store
	if _, err := db.Exec(ctx, `INSERT INTO peers (id) VALUES ('`+otherDevice+`')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, token_kind) VALUES (?, ?, 1, ?, 'u2', `+
		db.Pick("datetime('now', '+1 day')", "NOW() + INTERVAL '1 day'")+`, 'device')`,
		store.StoredAccessToken(authn.HashAccessToken(otherDeviceToken)), authn.HashAccessToken(otherDeviceToken), otherDevice); err != nil {
		t.Fatal(err)
	}
	return w, front
}

// asComputer sends a request to base with a computer's device token.
func asComputer(t *testing.T, base, token, method, path, body string) (int, string) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, base+path, reader)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(data)
}

// TestComputersTicketsOnTheAPIPort: the app reaches its tickets at the
// server's API port, where it reaches its chat (A277). A staff member's
// public answer sends ticket.updated to the socket of the computer that
// opened the ticket and to no other computer's; the server counts the
// ticket as unread for the heartbeat until the computer reads it.
func TestComputersTicketsOnTheAPIPort(t *testing.T) {
	w, front := twoComputersWorld(t)
	apiAddr := strings.TrimPrefix(front.URL, "http://")
	status, body := asComputer(t, front.URL, chatDeviceToken, "POST", "/api/tickets/nd", `{"description":"The printer is jammed","reporter":"Erika"}`)
	if status != http.StatusCreated {
		t.Fatalf("open a ticket on the API port: %d %s", status, body)
	}
	var created struct {
		TicketID int64 `json:"ticket_id"`
	}
	_ = json.Unmarshal([]byte(body), &created)
	id := strconv.FormatInt(created.TicketID, 10)
	for _, path := range []string{"/api/tickets/nd", "/api/tickets/nd/" + id} {
		if status, body := asComputer(t, front.URL, chatDeviceToken, "GET", path, ""); status != http.StatusOK || !strings.Contains(body, `"The printer is jammed"`) {
			t.Errorf("GET %s on the API port: %d %s", path, status, body)
		}
	}
	if status, body := asComputer(t, front.URL, otherDeviceToken, "GET", "/api/tickets/nd/"+id, ""); status != http.StatusNotFound {
		t.Errorf("the other computer on the API port: %d %s", status, body)
	}

	mine, status := dialChat(t, apiAddr, "/ws/chat/"+chatDevice+"?v=2&token="+chatDeviceToken, nil)
	if mine == nil {
		t.Fatalf("the computer's socket: %d", status)
	}
	other, status := dialChat(t, apiAddr, "/ws/chat/"+otherDevice+"?v=2&token="+otherDeviceToken, nil)
	if other == nil {
		t.Fatalf("the other computer's socket: %d", status)
	}
	if !waitText(mine, `"type":"welcome"`) || !waitText(other, `"type":"welcome"`) {
		t.Fatal("no welcome")
	}

	gdb := storetest.GoServer(t, w.s.Deps().Store)
	if n, err := gdb.UnreadDeviceTickets(chatDevice); err != nil || n != 0 {
		t.Errorf("unread before the answer: %d %v", n, err)
	}
	admin := w.agent().signIn("admin")
	if res := admin.post("/api/tickets/"+id+"/comments", `{"body":"An internal note","is_internal":true}`); res.status != http.StatusCreated {
		t.Fatalf("internal note: %d %s", res.status, res.body)
	}
	if res := admin.post("/api/tickets/"+id+"/comments", `{"body":"I'll be there in five minutes"}`); res.status != http.StatusCreated {
		t.Fatalf("answer: %d %s", res.status, res.body)
	}
	if !waitText(mine, `"type":"ticket.updated"`, `"ticket_id":`+id+`,`, `"status":"open"`, `"unread":true`) {
		t.Errorf("the computer got no ticket.updated: %v", mine.Frames())
	}
	// One frame for the public answer, none for the internal note
	updates := 0
	for _, f := range mine.Frames() {
		if strings.Contains(f.Text(), "ticket.updated") {
			updates++
		}
	}
	if updates != 1 {
		t.Errorf("%d ticket.updated frames, want 1: %v", updates, mine.Frames())
	}
	other.Wait(300*time.Millisecond, func([]wstest.Frame, bool) bool { return false })
	for _, f := range other.Frames() {
		if strings.Contains(f.Text(), "ticket") {
			t.Errorf("the other computer got %s", f.Text())
		}
	}
	if n, err := gdb.UnreadDeviceTickets(chatDevice); err != nil || n != 1 {
		t.Errorf("unread after the answer: %d %v", n, err)
	}
	if n, err := gdb.UnreadDeviceTickets(otherDevice); err != nil || n != 0 {
		t.Errorf("the other computer's unread: %d %v", n, err)
	}
	if status, body := asComputer(t, front.URL, chatDeviceToken, "POST", "/api/tickets/nd/"+id+"/read", "{}"); status != http.StatusNoContent {
		t.Errorf("read on the API port: %d %s", status, body)
	}
	if n, err := gdb.UnreadDeviceTickets(chatDevice); err != nil || n != 0 {
		t.Errorf("unread after reading: %d %v", n, err)
	}
	// Other paths below /api/tickets stay the API's
	if status, _ := asComputer(t, front.URL, chatDeviceToken, "GET", "/api/tickets/"+id, ""); status != http.StatusUnauthorized {
		t.Errorf("the staff route on the API port: %d, want the API's 401", status)
	}
}

// TestAComputerGetsNoOtherComputersChat: a device token opens the chat of
// its own computer only. Another computer's socket path with it is
// refused, another computer's chat file is not found, and the history
// and messages of one computer's conversation never reach the other's
// socket.
func TestAComputerGetsNoOtherComputersChat(t *testing.T) {
	w, front := twoComputersWorld(t)
	apiAddr := strings.TrimPrefix(front.URL, "http://")
	consoleAddr := strings.TrimPrefix(w.front.URL, "http://")

	mine, status := dialChat(t, apiAddr, "/ws/chat/"+chatDevice+"?v=2&token="+chatDeviceToken, nil)
	if mine == nil {
		t.Fatalf("the computer's socket: %d", status)
	}
	if err := mine.Text(`{"type":"send","text":"my password is on a sticky note","client_id":"c1"}`); err != nil {
		t.Fatal(err)
	}
	if !waitText(mine, `"type":"message.new"`, `sticky note`) {
		t.Fatalf("the message was not stored: %v", mine.Frames())
	}

	// The other computer's token on this computer's socket path
	for _, addr := range []string{apiAddr, consoleAddr} {
		if c, status := dialChat(t, addr, "/ws/chat/"+chatDevice+"?v=2&token="+otherDeviceToken, nil); c != nil || status != http.StatusForbidden {
			t.Errorf("the other token on %s: %d, want 403", addr, status)
		}
		if c, status := dialChat(t, addr, "/ws/chat/"+chatDevice+"?v=2", http.Header{"Authorization": {"Bearer " + otherDeviceToken}}); c != nil || status != http.StatusForbidden {
			t.Errorf("the other token as a bearer on %s: %d, want 403", addr, status)
		}
	}
	other, status := dialChat(t, apiAddr, "/ws/chat/"+otherDevice+"?v=2&token="+otherDeviceToken, nil)
	if other == nil {
		t.Fatalf("the other computer's socket: %d", status)
	}
	if !waitText(other, `"type":"history"`) {
		t.Fatalf("no history: %v", other.Frames())
	}
	// An operator's answer to the first computer
	admin := w.agent().signIn("admin")
	inbox, status := dialChat(t, consoleAddr, "/ws/chat-operator/panel?v=2", http.Header{"Cookie": {admin.cookieHeader()}, "Origin": {"http://" + consoleAddr}})
	if inbox == nil {
		t.Fatalf("the console's socket: %d", status)
	}
	if err := inbox.Text(`{"type":"send","conversation_id":"` + chatDevice + `","text":"please change it today"}`); err != nil {
		t.Fatal(err)
	}
	if !waitText(mine, `please change it today`) {
		t.Errorf("the first computer did not get the answer: %v", mine.Frames())
	}
	other.Wait(300*time.Millisecond, func([]wstest.Frame, bool) bool { return false })
	for _, f := range other.Frames() {
		if strings.Contains(f.Text(), "sticky note") || strings.Contains(f.Text(), "change it today") || strings.Contains(f.Text(), `"`+chatDevice+`"`) {
			t.Errorf("the other computer's socket got %s", f.Text())
		}
	}

	// The first computer's chat file
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("file", "secret.txt")
	_, _ = part.Write([]byte("only for the first computer"))
	_ = mw.Close()
	req, _ := http.NewRequest("POST", front.URL+"/api/chat/device/files", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+chatDeviceToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var uploaded struct {
		FileID string `json:"file_id"`
	}
	if json.Unmarshal(data, &uploaded) != nil || uploaded.FileID == "" {
		t.Fatalf("upload: %d %s", res.StatusCode, data)
	}
	for _, base := range []string{front.URL, w.front.URL} {
		if status, body := asComputer(t, base, otherDeviceToken, "GET", "/api/chat/device/files/"+uploaded.FileID, ""); status != http.StatusNotFound || strings.Contains(body, "only for the first") {
			t.Errorf("the other computer downloads the file at %s: %d %s", base, status, body)
		}
	}
	if status, body := asComputer(t, front.URL, chatDeviceToken, "GET", "/api/chat/device/files/"+uploaded.FileID, ""); status != http.StatusOK || body != "only for the first computer" {
		t.Errorf("the first computer's own file: %d %s", status, body)
	}
}

// TestStaffHearOfTheComputersTickets: the bell lists a ticket the person
// at a computer opened and the person's reply, with the link to the
// ticket, for an account that may read the ticket; a viewer the ticket
// isn't assigned to gets neither. Mark all read covers them.
func TestStaffHearOfTheComputersTickets(t *testing.T) {
	w, front := twoComputersWorld(t)
	status, body := asComputer(t, front.URL, chatDeviceToken, "POST", "/api/tickets/nd", `{"description":"Outlook keeps asking for my password","reporter":"Erika Muster"}`)
	if status != http.StatusCreated {
		t.Fatalf("open a ticket: %d %s", status, body)
	}
	var created struct {
		TicketID int64 `json:"ticket_id"`
	}
	_ = json.Unmarshal([]byte(body), &created)
	id := strconv.FormatInt(created.TicketID, 10)
	if status, body := asComputer(t, front.URL, chatDeviceToken, "POST", "/api/tickets/nd/"+id+"/comments", `{"body":"It happens again after the restart"}`); status != http.StatusCreated {
		t.Fatalf("reply: %d %s", status, body)
	}
	// Staff's own comment is no news for the bell
	admin := w.agent().signIn("admin")
	if res := admin.post("/api/tickets/"+id+"/comments", `{"body":"Looking at it"}`); res.status != http.StatusCreated {
		t.Fatalf("staff comment: %d %s", res.status, res.body)
	}

	type item struct {
		ID, Kind, Title, Message, Link, Reporter string
		DeviceID                                 string `json:"device_id"`
		Read                                     bool
	}
	bell := func(a *agent) ([]item, float64) {
		res := a.get("/api/nd/notifications?limit=50")
		var out struct {
			Items  []item  `json:"items"`
			Unread float64 `json:"unread_count"`
		}
		if res.status != http.StatusOK || json.Unmarshal([]byte(res.body), &out) != nil {
			t.Fatalf("notifications: %d %s", res.status, res.body)
		}
		var mine []item
		for _, i := range out.Items {
			if strings.HasPrefix(i.Kind, "ticket") {
				mine = append(mine, i)
			}
		}
		return mine, out.Unread
	}
	items, unread := bell(admin)
	if len(items) != 2 || unread < 2 {
		t.Fatalf("admin's bell: %+v (unread %v)", items, unread)
	}
	byKind := map[string]item{}
	for _, i := range items {
		byKind[i.Kind] = i
	}
	ticket, reply := byKind["ticket"], byKind["ticket_reply"]
	if ticket.Title != "Outlook keeps asking for my password" || ticket.Reporter != "Erika Muster" || ticket.DeviceID != chatDevice ||
		ticket.Link != "/tickets?ticket="+id || ticket.ID != "ticket:"+id || ticket.Read {
		t.Errorf("the new ticket: %+v", ticket)
	}
	if reply.Message != "It happens again after the restart" || reply.Link != "/tickets?ticket="+id || !strings.HasPrefix(reply.ID, "ticket_reply:") {
		t.Errorf("the reply: %+v", reply)
	}
	if items, _ := bell(w.agent().signIn("viewer")); len(items) != 0 {
		t.Errorf("a viewer the ticket isn't assigned to: %+v", items)
	}
	if res := admin.post("/api/nd/notifications/read-all", `{}`); res.status != http.StatusOK {
		t.Fatalf("read all: %d %s", res.status, res.body)
	}
	if items, _ := bell(admin); len(items) != 2 || !items[0].Read || !items[1].Read {
		t.Errorf("after read all: %+v", items)
	}
}
