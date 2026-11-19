package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	return Scope{
		Device: func(_ context.Context, id string) *goapi.Object {
			for _, known := range scopeDevices {
				if id == known {
					device := goapi.NewObject()
					device.Set("id", id)
					return device
				}
			}
			return nil
		},
		Of: groups.DeviceScopeForUser,
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
}

func (a *recordingAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	call := r.Method + " " + strings.TrimPrefix(r.RequestURI, "/api")
	a.mu.Lock()
	a.calls = append(a.calls, call)
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
