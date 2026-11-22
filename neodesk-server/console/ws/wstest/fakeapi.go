package wstest

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

// Plan is what the made-up API server of the proxy oracles does with the
// next sockets: answer every message with itself (Echo), send a first
// message (Greet), hold the handshake until Release (Hold), or refuse
// it with a status (Refuse).
type Plan struct {
	Echo   bool   `json:"echo"`
	Greet  string `json:"greet"`
	Hold   bool   `json:"hold"`
	Refuse int    `json:"refuse"`
}

// FakeAPI is the made-up API server of the proxy oracles as an
// http.Handler: its sockets note what they get and do what the plan
// says. The sockets are named u1, u2, ... in the order their handshakes
// arrive.
type FakeAPI struct {
	// Describe gives the fields of the event a handshake is noted with.
	Describe func(r *http.Request) Event
	// GreetBinary sends the greeting as binary instead of text.
	GreetBinary bool
	// Note takes the events.
	Note func(Event)

	mu      sync.Mutex
	plan    Plan
	seq     int
	conns   map[string]*websocket.Conn
	release chan struct{}
	wg      sync.WaitGroup
}

// SetPlan sets what the next sockets do.
func (f *FakeAPI) SetPlan(p Plan) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.plan = p
	if f.release == nil {
		f.release = make(chan struct{})
	}
}

// Release lets the held handshakes go on.
func (f *FakeAPI) Release() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.release != nil {
		close(f.release)
	}
	f.release = make(chan struct{})
}

// Conn is the API server's side of socket name.
func (f *FakeAPI) Conn(name string) *websocket.Conn {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conns[name]
}

// Up runs one step of the API server's side: send text, binary, bytes as
// a text frame without checking them, close with a code, or drop the
// connection.
func (f *FakeAPI) Up(name string, text, binary, bytes *string, closeCode int, reason string, drop bool) {
	c := f.Conn(name)
	if c == nil {
		panic(fmt.Sprintf("no upstream %s", name))
	}
	ctx := context.Background()
	switch {
	case text != nil:
		_ = c.Write(ctx, websocket.MessageText, []byte(*text))
	case binary != nil:
		b, _ := base64.StdEncoding.DecodeString(*binary)
		_ = c.Write(ctx, websocket.MessageBinary, b)
	case bytes != nil:
		b, _ := base64.StdEncoding.DecodeString(*bytes)
		_ = c.Write(ctx, websocket.MessageText, b)
	case closeCode != 0:
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			_ = c.Close(websocket.StatusCode(closeCode), reason)
		}()
	case drop:
		_ = c.CloseNow()
	}
}

// Wait lets the held handshakes go and waits until every socket's handler
// has returned.
func (f *FakeAPI) Wait() {
	f.Release()
	f.mu.Lock()
	for _, c := range f.conns {
		_ = c.CloseNow()
	}
	f.mu.Unlock()
	f.wg.Wait()
}

func (f *FakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.wg.Add(1)
	defer f.wg.Done()
	f.mu.Lock()
	f.seq++
	name := fmt.Sprintf("u%d", f.seq)
	plan, release := f.plan, f.release
	f.mu.Unlock()
	e := f.Describe(r)
	e["upstream"] = name
	f.Note(e)
	if plan.Hold {
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
	}
	if plan.Refuse != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Connection", "close")
		w.WriteHeader(plan.Refuse)
		_, _ = w.Write([]byte(`{"error":"no"}` + "\n"))
		return
	}
	opts := &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionDisabled}
	if offered := r.Header.Get("Sec-WebSocket-Protocol"); offered != "" {
		first, _, _ := strings.Cut(offered, ",")
		opts.Subprotocols = []string{strings.TrimSpace(first)}
	}
	c, err := websocket.Accept(w, r, opts)
	if err != nil {
		return
	}
	c.SetReadLimit(-1)
	f.mu.Lock()
	if f.conns == nil {
		f.conns = map[string]*websocket.Conn{}
	}
	f.conns[name] = c
	f.mu.Unlock()
	ctx := context.Background()
	if plan.Greet != "" {
		typ := websocket.MessageText
		if f.GreetBinary {
			typ = websocket.MessageBinary
		}
		_ = c.Write(ctx, typ, []byte(plan.Greet))
	}
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			code := int(websocket.CloseStatus(err))
			reason := ""
			var ce websocket.CloseError
			if code == -1 {
				code = 1006
			} else if errors.As(err, &ce) {
				reason = ce.Reason
			}
			f.Note(Event{"upstream": name, "close": float64(code), "reason": reason})
			return
		}
		if typ == websocket.MessageBinary {
			f.Note(Event{"upstream": name, "binary": base64.StdEncoding.EncodeToString(data)})
		} else {
			f.Note(Event{"upstream": name, "text": string(data)})
		}
		if plan.Echo {
			_ = c.Write(ctx, typ, data)
		}
	}
}
