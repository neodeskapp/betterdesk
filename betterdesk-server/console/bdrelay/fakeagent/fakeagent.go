// Package fakeagent is a made-up BetterDesk agent for tests: it connects
// to a console's /ws/bd-signal with a device id and a token, keeps the
// requests the console sends, and answers them the way the test says.
//
//	agent, err := fakeagent.Connect(addr, "A1001", token)
//	agent.Answer(func(req fakeagent.Request) any {
//		return map[string]any{"ok": true, "data": []string{"sshd"}}
//	})
//	... GET /api/devices/A1001/services answers {"success":true,"data":["sshd"]}
//	agent.Close()
package fakeagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// Request is one request the console sent the agent.
type Request struct {
	// Raw is the message as it came.
	Raw string
	// Type and RequestID are its type and request_id.
	Type      string
	RequestID string
	// Payload is its payload as JSON.parse read it.
	Payload any
}

// Agent is a connected made-up agent.
type Agent struct {
	c *wstest.Client

	mu     sync.Mutex
	answer func(Request) any
	got    []Request
	stop   chan struct{}
	done   chan struct{}
}

// Connect opens the signal socket at addr (host:port) and waits for the
// console's welcome.
func Connect(addr, deviceID, token string) (*Agent, error) {
	q := url.Values{"device_id": {deviceID}, "token": {token}}
	c, res, err := wstest.Dial(addr, "/ws/bd-signal?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("fakeagent: the console answered %d", res.StatusCode)
	}
	if !c.Wait(5*time.Second, func(f []wstest.Frame, closed bool) bool { return len(f) > 0 || closed }) {
		c.Drop()
		return nil, errors.New("fakeagent: no welcome")
	}
	frames := c.Take()
	if closed, code, reason := c.Closed(); closed || len(frames) == 0 {
		c.Drop()
		return nil, fmt.Errorf("fakeagent: closed with %d %q", code, reason)
	}
	a := &Agent{c: c, stop: make(chan struct{}), done: make(chan struct{})}
	go a.loop()
	return a, nil
}

// Answer sets what the agent does with a request: the fields of its
// command_response (ok, data, error; type and request_id are added), or
// nil to send nothing.
func (a *Agent) Answer(f func(Request) any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.answer = f
}

// Take returns the requests so far and forgets them.
func (a *Agent) Take() []Request {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.got
	a.got = nil
	return out
}

func (a *Agent) loop() {
	defer close(a.done)
	for {
		select {
		case <-a.stop:
			return
		default:
		}
		a.c.Wait(50*time.Millisecond, func(f []wstest.Frame, closed bool) bool { return len(f) > 0 || closed })
		for _, f := range a.c.Take() {
			a.handle(f.Text())
		}
		if closed, _, _ := a.c.Closed(); closed {
			return
		}
	}
}

func (a *Agent) handle(raw string) {
	v, err := goapi.Parse([]byte(raw))
	if err != nil {
		return
	}
	msg, _ := v.(*goapi.Object)
	req := Request{Raw: raw, Type: msg.Str("type"), RequestID: msg.Str("request_id"), Payload: msg.Get("payload")}
	a.mu.Lock()
	a.got = append(a.got, req)
	answer := a.answer
	a.mu.Unlock()
	if answer == nil || req.RequestID == "" {
		return
	}
	reply := answer(req)
	if reply == nil {
		return
	}
	out := goapi.NewObject("type", "command_response", "request_id", req.RequestID)
	fields, err := goapi.FromGo(reply)
	if err != nil {
		return
	}
	if o, ok := fields.(*goapi.Object); ok {
		for _, k := range o.Keys() {
			out.Set(k, o.Get(k))
		}
	}
	text, err := httpx.Marshal(out)
	if err != nil {
		return
	}
	_ = a.c.Text(string(text))
}

// Close ends the socket.
func (a *Agent) Close() {
	close(a.stop)
	<-a.done
	a.c.Close(1000)
}

// JSON is a reply given as JSON text, kept in its key order.
func JSON(text string) any { return json.RawMessage(text) }
