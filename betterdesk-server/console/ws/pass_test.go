package ws_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// TestDialHandlerRefused: an answer other than 101 is an error, and
// nothing of the in-memory connection is left behind.
func TestDialHandlerRefused(t *testing.T) {
	check := wstest.Goroutines(t)
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"no"}`, http.StatusForbidden)
	})
	up, err := ws.DialHandler(context.Background(), h, "/api/x", ws.DialOptions{})
	if err == nil || up != nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("dial: %v, %v", up, err)
	}
	check()
}

// TestDialHandlerNeedsTheSubprotocol: when the dial offers subprotocols,
// an answer that names none is refused, as the old client refused it.
func TestDialHandlerNeedsTheSubprotocol(t *testing.T) {
	check := wstest.Goroutines(t)
	done := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		_, _, _ = c.Read(context.Background())
		_ = c.CloseNow()
	})
	up, err := ws.DialHandler(context.Background(), h, "/x", ws.DialOptions{Subprotocols: []string{"cdap-terminal"}})
	if !errors.Is(err, ws.ErrNoSubprotocol) || up != nil {
		t.Fatalf("dial: %v, %v", up, err)
	}
	<-done
	check()
}

// TestDialHandlerSeesTheRequest: the handler gets the path, the query and
// the headers of the dial; an answer that names one of the offered
// subprotocols is taken.
func TestDialHandlerSeesTheRequest(t *testing.T) {
	check := wstest.Goroutines(t)
	var got *http.Request
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, Subprotocols: []string{"b"}})
		if err != nil {
			return
		}
		_, _, _ = c.Read(context.Background())
		_ = c.CloseNow()
	})
	header := http.Header{"X-Username": {"anna"}}
	up, err := ws.DialHandler(context.Background(), h, "/api/cdap/devices/x/terminal?q=1", ws.DialOptions{Header: header, Subprotocols: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.RequestURI != "/api/cdap/devices/x/terminal?q=1" || got.Header.Get("X-Username") != "anna" || got.Header.Get("Sec-WebSocket-Protocol") != "a,b" {
		t.Errorf("request %s %v", got.RequestURI, got.Header)
	}
	up.Close()
	up.Wait()
	check()
}

// TestUpstreamLimits: a message over the limit and text that is not UTF-8
// end the upstream with 1009 and 1007, as the old client closed it.
func TestUpstreamLimits(t *testing.T) {
	for _, c := range []struct {
		name string
		typ  websocket.MessageType
		data []byte
		err  error
		code websocket.StatusCode
	}{
		{"at the limit", websocket.MessageBinary, make([]byte, 64), nil, 0},
		{"over the limit", websocket.MessageBinary, make([]byte, 65), ws.ErrTooBig, websocket.StatusMessageTooBig},
		{"not UTF-8", websocket.MessageText, []byte{0xc3, 0x28}, ws.ErrInvalidText, websocket.StatusInvalidFramePayloadData},
		{"binary that is not UTF-8", websocket.MessageBinary, []byte{0xc3, 0x28}, nil, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			check := wstest.Goroutines(t)
			closed := make(chan websocket.StatusCode, 1)
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
				if err != nil {
					return
				}
				_ = conn.Write(context.Background(), c.typ, c.data)
				_, _, err = conn.Read(context.Background())
				closed <- websocket.CloseStatus(err)
			})
			up, err := ws.DialHandler(context.Background(), h, "/x", ws.DialOptions{MaxPayload: 64})
			if err != nil {
				t.Fatal(err)
			}
			_, data, err := up.Read()
			if !errors.Is(err, c.err) {
				t.Errorf("read: %v, want %v", err, c.err)
			}
			if c.err == nil {
				if len(data) != len(c.data) {
					t.Errorf("read %d bytes", len(data))
				}
				up.Close()
			}
			if got := <-closed; c.code != 0 && got != c.code {
				t.Errorf("the handler saw the close %d, want %d", got, c.code)
			}
			up.Wait()
			check()
		})
	}
}

// TestPassKeepsWhatWaits: messages that arrive while the dial waits are
// sent in order once the upstream is open, also more than the queue
// holds (the socket is then not read until there is room); none is lost.
func TestPassKeepsWhatWaits(t *testing.T) {
	check := wstest.Goroutines(t)
	const n = 1500
	release := make(chan struct{})
	got := make(chan []string, 1)
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			got <- nil
			return
		}
		var texts []string
		for len(texts) < n {
			_, data, err := c.Read(context.Background())
			if err != nil {
				break
			}
			texts = append(texts, string(data))
		}
		got <- texts
		_, _, _ = c.Read(context.Background())
	})
	reg := ws.New(ws.Options{})
	reg.Handle("/p", func(u *ws.Upgrade) {
		conn, ok := u.Accept(ws.AcceptOptions{})
		if !ok {
			return
		}
		defer conn.Wait()
		ws.Pass(conn, ws.PassOptions{Queue: true, Dial: func(ctx context.Context) (*ws.Upstream, error) {
			return ws.DialHandler(ctx, upstream, "/up", ws.DialOptions{})
		}})
	})
	srv := httptest.NewServer(reg)
	client, _, err := wstest.Dial(strings.TrimPrefix(srv.URL, "http://"), "/p", nil)
	if err != nil || client == nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if err := client.Text(fmt.Sprint(i)); err != nil {
				return
			}
		}
	}()
	time.Sleep(200 * time.Millisecond)
	close(release)
	texts := <-got
	wg.Wait()
	if len(texts) != n {
		t.Fatalf("the upstream got %d messages, want %d", len(texts), n)
	}
	for i, text := range texts {
		if text != fmt.Sprint(i) {
			t.Fatalf("message %d is %q", i, text)
		}
	}
	client.Close(1000)
	srv.Close()
	reg.Close()
	check()
}
