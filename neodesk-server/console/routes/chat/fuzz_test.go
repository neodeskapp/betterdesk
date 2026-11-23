package chat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsjson"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
)

// bearerPattern is the old relay's pattern; JavaScript's \s, for the
// characters of a Latin-1 text, is the set below.
var bearerPattern = regexp.MustCompile("^Bearer[\t\n\v\f\r  ]+([^\t\n\v\f\r  ]+)$")

// FuzzBearerToken holds bearerToken to the pattern it stands for, on the
// header read as Latin-1.
func FuzzBearerToken(f *testing.F) {
	for _, seed := range []string{"Bearer abc", "bearer abc", "Bearer  a", "Bearer\xa0a", "Bearer a b", "Bearer a\xa0", "Bearer", "Bearer ", "Bearer\t\v\fx\xff", "xBearer a", "Bearer \xe9t\xc3\xa9"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, header string) {
		latin1 := wstest.FromLatin1([]byte(header))
		want := ""
		if m := bearerPattern.FindStringSubmatch(latin1); m != nil {
			want = m[1]
		}
		if got := bearerToken(header); got != want {
			t.Fatalf("bearerToken(%q) = %q, want %q", header, got, want)
		}
	})
}

// FuzzSpread: {type, ...value} of any JSON value is JSON, with the type
// first unless the value has keys that are array indexes or a type of its
// own.
func FuzzSpread(f *testing.F) {
	for _, seed := range []string{`{"id":"group:1"}`, `{"7":"seven","type":"x","id":1}`, `"made"`, `[5,6]`, `null`, `"😀\ud800"`, `{"__proto__":{"a":1}}`, `{"4294967295":1,"4294967294":2,"01":3}`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		v, err := jsjson.Parse(text)
		if err != nil {
			return
		}
		out := newObject().str("type", "group_created").spread(v).json()
		if !json.Valid(out) {
			t.Fatalf("not JSON: %q from %q", out, text)
		}
	})
}

// FuzzRelayFrames sends any message from an agent, an operator in the
// device's room and an inbox operator to a running relay. The relay must
// go on (each socket answers the hello that follows, unless the message
// ended it), and everything any socket gets must be JSON.
func FuzzRelayFrames(f *testing.F) {
	for _, seed := range []string{
		`{"type":"hello"}`, `{"type":"message","text":"hi","conversation_id":"dev1"}`, `null`, `{"type":"message","text":{"toString":1}}`,
		`{"type":"reaction","message_id":"1","emoji":"x","conversation_id":"panel"}`, `{"type":"create_group","name":"g","member_ids":[1]}`,
		`{"type":"get_history","conversation_id":"\ud800"}`, `{"type":"file_share","file_id":{"toString":1}}`, `{"type":"typing","conversation_id":5}`,
		`{"type":"get_contacts","device_id":".."}`, `{"type":"presence_update","online":false}`, `[{"type":"hello"}]`,
	} {
		for sender := uint8(0); sender < 3; sender++ {
			f.Add(sender, []byte(seed))
		}
	}
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/chat/history/"):
			_, _ = io.WriteString(w, `{"messages":[{"id":1,"text":"x"}]}`)
		case strings.HasPrefix(r.URL.Path, "/api/chat/contacts/"):
			_, _ = io.WriteString(w, `{"contacts":[{"id":"dev2"}]}`)
		case r.URL.Path == "/api/chat/groups" && r.Method == http.MethodPost:
			_, _ = io.WriteString(w, `{"id":"group:1","7":2}`)
		default:
			_, _ = io.WriteString(w, `{"groups":[]}`)
		}
	})
	relay := NewRelay(RelayOptions{
		API:          goapi.New(api),
		VerifyDevice: func(context.Context, string, string) (bool, error) { return true, nil },
	})
	reg := ws.New(ws.Options{
		Session:    wstest.OracleSession,
		Permission: func(context.Context, string, string) (bool, error) { return true, nil },
	})
	relay.Register(reg)
	srv := httptest.NewServer(reg)
	f.Cleanup(func() {
		srv.Close()
		reg.Close()
	})
	addr := strings.TrimPrefix(srv.URL, "http://")
	session := http.Header{"X-Oracle-Session": {`{"userId":1,"user":{"username":"fuzz","role":"admin"}}`}}
	targets := []struct {
		path   string
		header http.Header
	}{
		{"/ws/chat/dev1?token=t", nil},
		{"/ws/chat-operator/dev1", session},
		{"/ws/chat-operator/panel", session},
	}
	clients := make([]*wstest.Client, len(targets))
	f.Cleanup(func() {
		for _, c := range clients {
			if c != nil {
				c.Drop()
			}
		}
	})
	open := func(t *testing.T, i int) {
		if c := clients[i]; c != nil {
			if closed, _, _ := c.Closed(); !closed {
				return
			}
			c.Drop()
		}
		c, _, err := wstest.Dial(addr, targets[i].path, targets[i].header)
		if err != nil || c == nil {
			t.Fatalf("open %s: %v", targets[i].path, err)
		}
		clients[i] = c
	}
	// allJSON takes what the sockets got and checks that it is JSON text.
	allJSON := func(t *testing.T, frame []byte) {
		for j, c := range clients {
			for _, fr := range c.Take() {
				if fr.Binary || !json.Valid(fr.Data) {
					t.Fatalf("socket %s got %q after %q", targets[j].path, fr.Data, frame)
				}
			}
		}
	}
	welcomes := func(frames []wstest.Frame) int {
		n := 0
		for _, fr := range frames {
			if strings.HasPrefix(fr.Text(), `{"type":"welcome"`) {
				n++
			}
		}
		return n
	}
	f.Fuzz(func(t *testing.T, sender uint8, frame []byte) {
		for i := range targets {
			open(t, i)
		}
		allJSON(t, frame)
		i := int(sender) % len(targets)
		c := clients[i]
		_ = c.Text(string(frame))
		_ = c.Text(`{"type":"hello"}`)
		answered := c.Wait(5*time.Second, func(frames []wstest.Frame, closed bool) bool {
			return closed || welcomes(frames) > 0
		})
		if !answered {
			t.Fatalf("socket %s neither answered nor closed after %q", targets[i].path, frame)
		}
		allJSON(t, frame)
	})
}
