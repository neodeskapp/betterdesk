package console

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/api"
	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/authn"
	consoleconfig "github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// The computer of the tests and its device token.
const (
	chatDevice      = "900100001"
	chatDeviceToken = "device-token-for-the-chat-0001"
)

// computerChatWorld is a console with the server's API on the same
// database, the API's listener served on a port of its own as computers
// reach it, and the computer's chat handed from there to the console
// (api.Server.SetDeviceChat, L526).
func computerChatWorld(t *testing.T) (*world, *httptest.Server, *api.Server) {
	t.Helper()
	db := storetest.Migrated(t)
	gdb := storetest.GoServer(t, db)
	apiSrv := api.New(config.DefaultConfig(), gdb, peer.NewMap(), nil, "test")
	// The chat files go to a folder of the test, not the source tree
	files := t.TempDir()
	w := newWorld(t, setup{db: db, api: apiSrv.InternalHandler(), listener: apiSrv.ListenerHandler(),
		configure: func(c *consoleconfig.Config) { c.ConsoleDir = files }})
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO peers (id) VALUES ('` + chatDevice + `')`,
	} {
		if _, err := db.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	// The computer's access token, as it gets one when it signs in: bound
	// to its id, of the kind device
	if _, err := db.Exec(ctx, `INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, token_kind) VALUES (?, ?, 1, ?, 'u', `+
		db.Pick("datetime('now', '+1 day')", "NOW() + INTERVAL '1 day'")+`, 'device')`,
		store.StoredAccessToken(authn.HashAccessToken(chatDeviceToken)), authn.HashAccessToken(chatDeviceToken), chatDevice); err != nil {
		t.Fatal(err)
	}
	apiSrv.SetDeviceChat(w.s)
	front := httptest.NewServer(apiSrv.ListenerHandler())
	t.Cleanup(front.Close)
	return w, front, apiSrv
}

func dialChat(t *testing.T, addr, path string, header http.Header) (*wstest.Client, int) {
	t.Helper()
	c, res, err := wstest.Dial(addr, path, header)
	if err != nil || c == nil {
		status := 0
		if res != nil {
			status = res.StatusCode
		}
		return nil, status
	}
	t.Cleanup(c.Drop)
	return c, http.StatusSwitchingProtocols
}

// waitText waits for a frame that has every part.
func waitText(c *wstest.Client, parts ...string) bool {
	return c.Wait(5*time.Second, func(frames []wstest.Frame, _ bool) bool {
		for _, f := range frames {
			ok := true
			for _, p := range parts {
				if !strings.Contains(f.Text(), p) {
					ok = false
				}
			}
			if ok {
				return true
			}
		}
		return false
	})
}

// TestComputersChatOnTheAPIPort: a computer reaches the support chat at
// the server's API port, where it sends everything else (L526): its device
// token opens the socket of version 2 there, and its messages reach the
// console's chat and back. The checks are the console port's.
func TestComputersChatOnTheAPIPort(t *testing.T) {
	w, front, _ := computerChatWorld(t)
	apiAddr := strings.TrimPrefix(front.URL, "http://")
	consoleAddr := strings.TrimPrefix(w.front.URL, "http://")

	admin := w.agent().signIn("admin")
	inbox, status := dialChat(t, consoleAddr, "/ws/chat-operator/panel?v=2", http.Header{"Cookie": {admin.cookieHeader()}, "Origin": {"http://" + consoleAddr}})
	if inbox == nil {
		t.Fatalf("the console's socket: %d", status)
	}
	computer, status := dialChat(t, apiAddr, "/ws/chat/"+chatDevice+"?v=2&token="+chatDeviceToken, nil)
	if computer == nil {
		t.Fatalf("the computer's socket on the API port: %d, want 101", status)
	}
	if !waitText(computer, `"type":"welcome"`, `"v":2`) {
		t.Fatalf("no welcome: %v", computer.Frames())
	}
	if err := computer.Text(`{"type":"send","text":"my printer stopped","client_id":"c1"}`); err != nil {
		t.Fatal(err)
	}
	if !waitText(inbox, `"type":"message.new"`, `"text":"my printer stopped"`) {
		t.Errorf("the console did not get the computer's message: %v", inbox.Frames())
	}
	if err := inbox.Text(`{"type":"send","conversation_id":"` + chatDevice + `","text":"looking at it"}`); err != nil {
		t.Fatal(err)
	}
	if !waitText(computer, `"type":"message.new"`, `"text":"looking at it"`) {
		t.Errorf("the computer did not get the console's answer: %v", computer.Frames())
	}

	// The bearer header works as on the console port
	if c, status := dialChat(t, apiAddr, "/ws/chat/"+chatDevice+"?v=2", http.Header{"Authorization": {"Bearer " + chatDeviceToken}}); c == nil {
		t.Errorf("the bearer header: %d", status)
	}
	// A wrong token, a token of another kind and none are refused alike
	for name, h := range map[string]http.Header{
		"no credentials":   nil,
		"a wrong token":    {"Authorization": {"Bearer device-token-that-is-wrong"}},
		"an account token": {"Authorization": {"Bearer adm_0123456789abcdef0123456789abcdef"}},
	} {
		if _, status := dialChat(t, apiAddr, "/ws/chat/"+chatDevice+"?v=2", h); status != http.StatusForbidden {
			t.Errorf("%s: %d, want 403", name, status)
		}
	}
}

// TestComputersChatOffOnTheAPIPort: with "Chat with computers" off the
// API port refuses as the console port does: 503 and chat_off.
func TestComputersChatOffOnTheAPIPort(t *testing.T) {
	w, front, _ := computerChatWorld(t)
	if err := w.s.Deps().Chat.SetSetting(context.Background(), "computers", "0"); err != nil {
		t.Fatal(err)
	}
	_, res, _ := wstest.Dial(strings.TrimPrefix(front.URL, "http://"), "/ws/chat/"+chatDevice+"?v=2&token="+chatDeviceToken, nil)
	if res == nil || res.StatusCode != http.StatusServiceUnavailable || res.Header.Get("X-NeoDesk-Chat") != "chat_off" {
		status := 0
		if res != nil {
			status = res.StatusCode
		}
		t.Errorf("chat with computers off: %d, want 503 chat_off", status)
	}
}

// TestComputersChatFilesOnTheAPIPort: the computer uploads and downloads
// its chat files at the API port with its device token; other
// credentials get the console port's refusal.
func TestComputersChatFilesOnTheAPIPort(t *testing.T) {
	w, front, _ := computerChatWorld(t)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", "note.txt")
	_, _ = part.Write([]byte("hello from the computer"))
	_ = mw.Close()
	uploadTo := func(base, token string) (*http.Response, string) {
		req, _ := http.NewRequest("POST", base+"/api/chat/device/files", bytes.NewReader(body.Bytes()))
		req.Header.Set("Content-Type", mw.FormDataContentType())
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res, string(b)
	}
	upload := func(token string) (*http.Response, string) { return uploadTo(front.URL, token) }
	// The console's own port takes the same upload (a computer behind a
	// proxy that sends to the console's address)
	if res, text := uploadTo(w.front.URL, chatDeviceToken); res.StatusCode != http.StatusOK {
		t.Errorf("upload on the console port: %d %s", res.StatusCode, text)
	}
	res, text := upload(chatDeviceToken)
	if res.StatusCode != http.StatusOK || !strings.Contains(text, `"file_id":"`) {
		t.Fatalf("upload: %d %s", res.StatusCode, text)
	}
	id := text[strings.Index(text, `"file_id":"`)+len(`"file_id":"`):]
	id = id[:strings.Index(id, `"`)]
	req, _ := http.NewRequest("GET", front.URL+"/api/chat/device/files/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+chatDeviceToken)
	got, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(got.Body)
	got.Body.Close()
	if got.StatusCode != http.StatusOK || string(data) != "hello from the computer" {
		t.Errorf("download: %d %q", got.StatusCode, data)
	}
	for _, token := range []string{"", "device-token-that-is-wrong"} {
		if res, text := upload(token); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("upload with %q: %d %s, want 401", token, res.StatusCode, text)
		}
	}
}

// TestOnlyTheComputersChatOnTheAPIPort: nothing else of the console
// answers at the API port: the console's own chat routes and sockets get
// the API's refusal, and the chat of version 1 is not there.
func TestOnlyTheComputersChatOnTheAPIPort(t *testing.T) {
	_, front, apiSrv := computerChatWorld(t)
	get := func(path string) int {
		req, _ := http.NewRequest("GET", front.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+chatDeviceToken)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	for _, path := range []string{"/api/chat/inbox", "/api/chat/upload", "/api/chat/files/0123456789abcdef0123456789abcdef", "/api/chat/team", "/api/chat/device/files/x/y"} {
		if status := get(path); status != http.StatusUnauthorized {
			t.Errorf("%s on the API port: %d, want the API's 401", path, status)
		}
	}
	addr := strings.TrimPrefix(front.URL, "http://")
	if _, status := dialChat(t, addr, "/ws/chat-operator/panel?v=2", nil); status != http.StatusUnauthorized {
		t.Errorf("the console's socket on the API port: %d, want 401", status)
	}
	if _, status := dialChat(t, addr, "/ws/chat/"+chatDevice+"?token="+chatDeviceToken, nil); status != http.StatusNotFound {
		t.Errorf("the chat of version 1 on the API port: %d, want 404", status)
	}
	// Without a console (CONSOLE_PORT=0, or a console that did not start)
	// the computer learns that this server has no chat
	apiSrv.SetDeviceChat(nil)
	if _, status := dialChat(t, addr, "/ws/chat/"+chatDevice+"?v=2&token="+chatDeviceToken, nil); status != http.StatusNotFound {
		t.Errorf("without a console: %d, want 404", status)
	}
}
