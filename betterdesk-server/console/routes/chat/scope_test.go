package chat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/devicegroups"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// The accounts of the scope tests. Under a restricted default an account
// sees only the devices granted to it; admins see every device.
var scopeAccounts = map[string]session.User{
	"anna":  {ID: 5, Username: "anna", Role: "operator"},
	"ben":   {ID: 6, Username: "ben", Role: "operator"},
	"cleo":  {ID: 8, Username: "cleo", Role: "viewer"},
	"admin": {ID: 1, Username: "admin", Role: "admin"},
}

// scopeGrants are the devices each account is granted.
var scopeGrants = map[string][]string{"anna": {"dev1"}, "ben": {"dev2"}, "cleo": {"dev1"}}

// scopeDevices are the devices the API server knows.
var scopeDevices = []string{"dev1", "dev2", "dev3"}

// realScope is the device scope of package access on a test database
// with the accounts and grants above.
func realScope(t *testing.T, db *store.DB) Scope {
	t.Helper()
	ctx := context.Background()
	for _, a := range scopeAccounts {
		if _, err := db.Exec(ctx, "INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, 'x', ?)", a.ID, a.Username, a.Role); err != nil {
			t.Fatal(err)
		}
	}
	for name, ids := range scopeGrants {
		for _, id := range ids {
			if _, err := db.Exec(ctx, "INSERT INTO user_peer_grants (user_id, peer_id) VALUES (?, ?)", scopeAccounts[name].ID, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	scopes := access.New(db, access.NewDBRoleSource(nil), access.Options{DeviceScopeDefault: "restricted"})
	groups := devicegroups.New(devicegroups.Options{DB: db, Access: scopes})
	device := func(id string) *goapi.Object {
		d := goapi.NewObject()
		d.Set("id", id)
		return d
	}
	return Scope{
		Device: func(_ context.Context, id string) *goapi.Object {
			for _, known := range scopeDevices {
				if id == known {
					return device(id)
				}
			}
			return nil
		},
		Devices: func(context.Context) []*goapi.Object {
			var all []*goapi.Object
			for _, id := range scopeDevices {
				all = append(all, device(id))
			}
			return all
		},
		Of: groups.DeviceScopeForUser,
	}
}

// TestScopedOperator: an operator opens the rooms of the devices in its
// scope only, names no other device's conversation in a frame, and gets
// in the inbox what concerns its devices only: their status, messages,
// contacts and history.
func TestScopedOperator(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		files := t.TempDir()
		if err := os.WriteFile(filepath.Join(files, firstID+".txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := addLink(files, firstID, link{Owner: scopeAccounts["anna"].ID}); err != nil {
			t.Fatal(err)
		}
		relay := startRelay(t, RelayOptions{Scope: realScope(t, db), FilesDir: files})
		relay.api.answer("GET /chat/contacts/panel", `{"contacts":[{"id":"dev1"},{"id":"dev2"},{"id":"dev3"},{"id":"operator:admin"}]}`)
		relay.api.answer("GET /chat/history/operator?limit=100", `{"messages":[{"id":1,"from_id":"dev1"},{"id":2,"from_id":"dev2"},`+
			`{"id":3,"from_id":"operator:admin","to_id":"dev2"},{"id":4,"from_id":"operator:admin"}]}`)

		for _, refused := range []struct{ who, room string }{{"anna", "dev2"}, {"anna", "dev3"}, {"anna", "nobody"}, {"admin", "nobody"}} {
			if s, status := relay.dial(t, refused.who, "/ws/chat-operator/"+refused.room); s != nil || status != 404 {
				t.Errorf("%s in the room of %s: %d, want 404", refused.who, refused.room, status)
			}
		}
		relay.open(t, "anna", "dev1")
		relay.open(t, "admin", "dev2")

		anna := relay.open(t, "anna", "panel")
		ben := relay.open(t, "ben", "panel")
		want(t, "anna's inbox", anna.take(), `{"type":"status","agent_connected":false}`,
			`{"type":"contacts","contacts":[{"id":"dev1"},{"id":"operator:admin"}]}`)
		want(t, "ben's inbox", ben.take(), `{"type":"status","agent_connected":false}`,
			`{"type":"contacts","contacts":[{"id":"dev2"},{"id":"operator:admin"}]}`)

		dev1 := relay.agent(t, "dev1")
		dev2 := relay.agent(t, "dev2")
		dev1.take()
		dev2.take()
		want(t, "anna sees dev1 come", anna.take(), `{"type":"status","device_id":"dev1","agent_connected":true}`,
			`{"type":"presence","device_id":"dev1","online":true}`)
		want(t, "ben sees dev2 come", ben.take(), `{"type":"status","device_id":"dev2","agent_connected":true}`,
			`{"type":"presence","device_id":"dev2","online":true}`)

		dev1.send(t, `{"type":"typing"}`)
		dev1.sync(t)
		want(t, "anna gets dev1's typing", anna.take(), `{"type":"typing","from":"dev1","conversation_id":"operator"}`)
		want(t, "ben gets nothing of dev1", ben.take())

		relay.api.taken()
		for _, frame := range []string{
			`{"type":"message","text":"to dev2","conversation_id":"dev2"}`,
			`{"type":"typing","conversation_id":"dev2"}`,
			`{"type":"file_share","conversation_id":"dev2","file_id":"` + firstID + `"}`,
			`{"type":"reaction","conversation_id":"dev2","message_id":"1","emoji":"x"}`,
			`{"type":"read_receipt","conversation_id":"dev2","message_ids":[1]}`,
			`{"type":"mark_read","conversation_id":"dev2"}`,
			`{"type":"get_history","conversation_id":"dev2"}`,
			`{"type":"get_contacts","device_id":"dev2"}`,
			`{"type":"get_groups","device_id":"dev2"}`,
		} {
			anna.send(t, frame)
		}
		anna.sync(t)
		want(t, "anna on dev2", anna.take())
		want(t, "dev2 from anna", dev2.take())
		want(t, "ben from anna", ben.take())
		if calls := relay.api.taken(); len(calls) != 0 {
			t.Errorf("anna's frames on dev2 reached the API server: %q", calls)
		}
		if links, err := readLinks(files, firstID); err != nil || len(links.conversations) != 0 {
			t.Errorf("anna's file was linked to %q (%v)", links.conversations, err)
		}

		// Without the API server's contacts, the connected devices she sees
		anna.send(t, `{"type":"get_contacts","device_id":"operator:anna"}`)
		anna.sync(t)
		if got := anna.take(); len(got) != 1 || !strings.Contains(got[0], `"id":"dev1"`) || strings.Contains(got[0], `"id":"dev2"`) {
			t.Errorf("anna's contacts without the API server: %q, want dev1 and not dev2", got)
		}

		anna.send(t, `{"type":"message","text":"to dev1","conversation_id":"dev1"}`)
		anna.send(t, `{"type":"typing","conversation_id":"group:team"}`)
		anna.send(t, `{"type":"get_history","conversation_id":"operator"}`)
		anna.sync(t)
		if got := dev1.take(); len(got) != 1 || !strings.Contains(got[0], `"text":"to dev1"`) {
			t.Errorf("dev1 got %q, want anna's message", got)
		}
		want(t, "ben gets the typing of a group", ben.take(), `{"type":"typing","from":"operator","operator":"anna","conversation_id":"group:team"}`)
		got := anna.take()
		if len(got) != 2 || got[1] != `{"type":"history","conversation_id":"operator","messages":[{"id":1,"from_id":"dev1"},{"id":4,"from_id":"operator:admin"}]}` {
			t.Errorf("anna got %q, want her message and the history without dev2", got)
		}
	})
}

// TestAgentReadsItsOwnConversations: an agent reads the history of its
// device and of the groups it is in, and its own contacts.
func TestAgentReadsItsOwnConversations(t *testing.T) {
	relay := startRelay(t, RelayOptions{})
	relay.api.answer("GET /chat/groups/dev1", `{"groups":[{"id":"group:g1","members":"dev1,dev2"}]}`)
	relay.api.answer("GET /chat/history/group%3Ag1?limit=100", `{"messages":[{"id":1}]}`)
	relay.api.answer("GET /chat/history/dev2?limit=100", `{"messages":[{"id":2}]}`)
	relay.api.answer("GET /chat/contacts/dev1", `{"contacts":[{"id":"dev2"}]}`)
	dev1 := relay.agent(t, "dev1")
	dev1.take()
	for _, frame := range []string{
		`{"type":"get_history","conversation_id":"dev2"}`,
		`{"type":"get_history","conversation_id":"operator"}`,
		`{"type":"get_contacts","device_id":"dev2"}`,
		`{"type":"get_history","conversation_id":"group:g1"}`,
		`{"type":"get_contacts"}`,
	} {
		dev1.send(t, frame)
	}
	dev1.sync(t)
	want(t, "dev1", dev1.take(), `{"type":"history","conversation_id":"group:g1","messages":[{"id":1}]}`,
		`{"type":"contacts","contacts":[{"id":"dev2"}]}`)
	for _, call := range relay.api.taken() {
		if strings.Contains(call, "dev2") || strings.Contains(call, "history/operator") {
			t.Errorf("the API server was asked %s", call)
		}
	}
}

// TestNoAgentIsThePanel: "panel" is the room of the inbox, whose contacts
// are every device of the server. A device socket under that id is
// refused with 403 before its token is looked at, so a token whose
// client id is "panel" (an unbound enrollment token, a sign-in that named
// the device "panel") gets neither the inbox's contacts nor its room
// (BUGS SK8, G24).
func TestNoAgentIsThePanel(t *testing.T) {
	var mu sync.Mutex
	var verified []string
	relay := startRelay(t, RelayOptions{VerifyDevice: func(_ context.Context, deviceID, token string) (bool, error) {
		mu.Lock()
		verified = append(verified, deviceID)
		mu.Unlock()
		return token == "tok-"+deviceID, nil
	}})
	relay.api.answer("GET /chat/contacts/panel", `{"contacts":[{"id":"dev1"},{"id":"dev2"}]}`)
	for _, path := range []string{"/ws/chat/panel?token=tok-panel", "/ws/chat/panel"} {
		if s, status := relay.dial(t, "", path); s != nil || status != http.StatusForbidden {
			t.Errorf("%s: %d, want 403", path, status)
		}
	}
	if calls := relay.api.taken(); len(calls) != 0 {
		t.Errorf("the API server was asked %q", calls)
	}
	// A device of another name still comes in
	relay.agent(t, "dev1")
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(verified, " ") != "dev1" {
		t.Errorf("tokens looked at for %q, want dev1's only", verified)
	}
}

// want compares the messages a socket got with the ones it should have.
func want(t *testing.T, what string, got []string, messages ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(messages, "\n") {
		t.Errorf("%s:\n got %q\nwant %q", what, got, messages)
	}
}

// signedIn is the X-Test-User header of an account.
func signedIn(name string) []string {
	return []string{"X-Test-User", name}
}

// TestDownloadFollowsTheConversation: a chat file is downloaded by the
// account that uploaded it, by an account that sees a conversation it was
// shared into, and by an account that sees every device; any other
// account does not find it. Only an account that may download a file can
// share it into a conversation.
func TestDownloadFollowsTheConversation(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t)
		w.h.scope = realScope(t, db)
		relay := startRelay(t, RelayOptions{Scope: w.h.scope, FilesDir: w.dir})
		const missing = `{"success":false,"error":"File not found"}`
		download := func(id, who string) *httptest.ResponseRecorder {
			return w.get("", "GET", "/api/chat/files/"+id, signedIn(who)...)
		}

		up := w.uploadBy("anna", part{"file", "note.txt", []byte("for dev1")})
		if up.Code != 200 {
			t.Fatalf("upload: %d %s", up.Code, up.Body)
		}
		expect(t, "the uploader", download(firstID, "anna"), 200, "for dev1")
		expect(t, "an admin", download(firstID, "admin"), 200, "for dev1")
		expect(t, "an operator before a share", download(firstID, "ben"), 404, missing)
		expect(t, "a viewer of dev1 before a share", download(firstID, "cleo"), 404, missing)

		anna := relay.open(t, "anna", "panel")
		ben := relay.open(t, "ben", "panel")
		ben.send(t, `{"type":"file_share","conversation_id":"operator:ben","file_id":"`+strings.ToUpper(firstID)+`"}`)
		ben.sync(t)
		expect(t, "a share by an account that may not download the file", download(firstID, "ben"), 404, missing)
		anna.send(t, `{"type":"file_share","conversation_id":"dev1","file_id":"`+strings.ToUpper(firstID)+`"}`)
		anna.sync(t)
		expect(t, "a viewer of the conversation", download(firstID, "cleo"), 200, "for dev1")
		expect(t, "an operator who does not see the conversation", download(firstID, "ben"), 404, missing)
		anna.send(t, `{"type":"file_share","conversation_id":"group:team","file_id":"`+firstID+`"}`)
		anna.sync(t)
		expect(t, "an operator of a conversation that is no device's", download(firstID, "ben"), 200, "for dev1")

		w.put(secondID+".txt", "no links")
		expect(t, "a file without links, an operator", download(secondID, "anna"), 404, missing)
		expect(t, "a file without links, an admin", download(secondID, "admin"), 200, "no links")
		anna.send(t, `{"type":"file_share","conversation_id":"dev1","file_id":"`+secondID+`"}`)
		anna.sync(t)
		expect(t, "a file without links shared by an account that may not download it", download(secondID, "cleo"), 404, missing)
	})
}

// testRelay is a relay on a test server where every role may chat, with
// the accounts of scopeAccounts.
type testRelay struct {
	relay *Relay
	addr  string
	api   *recordingAPI
}

// recordingAPI is the API server of the relay tests: its answers by
// "METHOD path", 500 for anything else, and every call noted.
type recordingAPI struct {
	mu      sync.Mutex
	answers map[string]string
	calls   []string
	// posts are the POST calls with their bodies
	posts []string
}

func (a *recordingAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	call := r.Method + " " + strings.TrimPrefix(r.RequestURI, "/api")
	body, _ := io.ReadAll(r.Body)
	a.mu.Lock()
	a.calls = append(a.calls, call)
	if r.Method == http.MethodPost {
		a.posts = append(a.posts, call+" "+string(body))
	}
	answer, ok := a.answers[call]
	a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if !ok {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"error":"internal"}`))
		return
	}
	_, _ = w.Write([]byte(answer))
}

// answer sets the answer to a call ("GET /chat/contacts/panel").
func (a *recordingAPI) answer(call, body string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.answers[call] = body
}

// takenPosts returns the POST calls so far with their bodies and forgets
// them.
func (a *recordingAPI) takenPosts() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	posts := a.posts
	a.posts = nil
	return posts
}

// taken returns the calls so far and forgets them.
func (a *recordingAPI) taken() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	calls := a.calls
	a.calls = nil
	return calls
}

func startRelay(t *testing.T, opts RelayOptions) *testRelay {
	t.Helper()
	check := wstest.Goroutines(t)
	api := &recordingAPI{answers: map[string]string{}}
	opts.API = goapi.New(api, goapi.WithTimeout(5*time.Second))
	if opts.VerifyDevice == nil {
		opts.VerifyDevice = func(_ context.Context, deviceID, token string) (bool, error) {
			return token == "tok-"+deviceID, nil
		}
	}
	relay := NewRelay(opts)
	reg := ws.New(ws.Options{
		Session:    wstest.OracleSession,
		Permission: func(context.Context, string, string) (bool, error) { return true, nil },
	})
	relay.Register(reg)
	srv := httptest.NewServer(reg)
	t.Cleanup(func() {
		srv.Close()
		reg.Close()
		check()
	})
	return &testRelay{relay: relay, addr: strings.TrimPrefix(srv.URL, "http://"), api: api}
}

// testSocket is a socket of a relay test: it keeps every text message.
type testSocket struct {
	conn *websocket.Conn
	mu   sync.Mutex
	got  []string
	done chan struct{}
}

// open opens an operator's socket to a room, failing the test when the
// relay refuses it.
func (tr *testRelay) open(t *testing.T, who, room string) *testSocket {
	t.Helper()
	s, status := tr.dial(t, who, "/ws/chat-operator/"+room)
	if s == nil {
		t.Fatalf("%s in %s: refused with %d", who, room, status)
	}
	return s
}

// agent opens a device's socket with its token.
func (tr *testRelay) agent(t *testing.T, device string) *testSocket {
	t.Helper()
	s, status := tr.dial(t, "", "/ws/chat/"+device+"?token=tok-"+device)
	if s == nil {
		t.Fatalf("the agent of %s: refused with %d", device, status)
	}
	return s
}

// dial opens a socket as an account ("" for none) and returns it, or nil
// and the status of the refusal.
func (tr *testRelay) dial(t *testing.T, who, path string) (*testSocket, int) {
	t.Helper()
	header := http.Header{}
	if who != "" {
		a := scopeAccounts[who]
		session, _ := json.Marshal(map[string]any{"userId": a.ID, "user": map[string]any{"id": a.ID, "username": a.Username, "role": a.Role}})
		header.Set("X-Oracle-Session", string(session))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, "ws://"+tr.addr+path, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		if resp != nil {
			return nil, resp.StatusCode
		}
		return nil, 0
	}
	s := &testSocket{conn: conn, done: make(chan struct{})}
	go s.read()
	t.Cleanup(func() {
		_ = conn.CloseNow()
		<-s.done
	})
	return s, http.StatusSwitchingProtocols
}

func (s *testSocket) read() {
	defer close(s.done)
	for {
		_, data, err := s.conn.Read(context.Background())
		if err != nil {
			return
		}
		s.mu.Lock()
		s.got = append(s.got, string(data))
		s.mu.Unlock()
	}
}

func (s *testSocket) send(t *testing.T, text string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.conn.Write(ctx, websocket.MessageText, []byte(text)); err != nil {
		t.Fatalf("send %s: %v", text, err)
	}
}

// take waits a moment for what is on its way and returns the messages so
// far, forgetting them.
func (s *testSocket) take() []string {
	time.Sleep(150 * time.Millisecond)
	s.mu.Lock()
	defer s.mu.Unlock()
	got := s.got
	s.got = nil
	return got
}

// sync waits until the relay has handled what the socket sent before: it
// sends hello and waits for the welcome.
func (s *testSocket) sync(t *testing.T) {
	t.Helper()
	s.send(t, `{"type":"hello"}`)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		for i, m := range s.got {
			if strings.HasPrefix(m, `{"type":"welcome"`) {
				s.got = append(s.got[:i:i], s.got[i+1:]...)
				s.mu.Unlock()
				return
			}
		}
		s.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no welcome")
}
