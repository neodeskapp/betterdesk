package bdrelay

import (
	"context"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/ws"
)

// AgentError is a request to an agent that did not get data: the agent is
// not connected ("agent_offline"), did not answer in time
// ("agent_timeout"), or answered with an error (its text, "agent_error"
// when it gave none). Message is the text the old relay rejected with.
type AgentError struct {
	Message string
}

func (e *AgentError) Error() string { return e.Message }

// The errors of RequestFromDevice that are not the agent's own.
var (
	ErrAgentOffline = &AgentError{Message: "agent_offline"}
	ErrAgentTimeout = &AgentError{Message: "agent_timeout"}
)

// pendingRequest waits for an agent's command_response on the signal
// socket conn it was sent to.
type pendingRequest struct {
	done  chan requestResult
	timer Timer
	conn  *ws.Conn
}

type requestResult struct {
	data any
	err  error
}

// RequestFromDevice sends {protocol_version, type, request_id, payload} to
// a device's signal socket and waits for its command_response with that
// request id (requestFromDevice). timeout is held between 1 and 60
// seconds. The data is the answer's data as JSON.parse read it, null when
// it is not truthy. A request whose socket ends (closed, or replaced by a
// new socket of the device) gets ErrAgentOffline at once. ctx ends the
// wait for the caller only.
func (s *Service) RequestFromDevice(ctx context.Context, deviceID, typ string, payload any, timeout time.Duration) (any, error) {
	s.mu.Lock()
	conn, ok := s.online.get(deviceID)
	if !ok || !conn.Open() {
		s.mu.Unlock()
		return nil, ErrAgentOffline
	}
	id := s.randomUUID()
	timeout = max(time.Second, min(60*time.Second, timeout))
	p := &pendingRequest{done: make(chan requestResult, 1), conn: conn}
	p.timer = s.clock.AfterFunc(timeout, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.pending[id] == p {
			delete(s.pending, id)
			p.done <- requestResult{err: ErrAgentTimeout}
		}
	})
	s.pending[id] = p
	s.mu.Unlock()

	text, err := stringify(goapi.NewObject("protocol_version", float64(1), "type", typ, "request_id", id, "payload", payload))
	if err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		p.timer.Stop()
		s.mu.Unlock()
		return nil, err
	}
	conn.SendText(text)
	select {
	case r := <-p.done:
		return r.data, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// offlineLocked settles the pending requests sent to conn, whose socket
// has ended, with ErrAgentOffline.
func (s *Service) offlineLocked(conn *ws.Conn) {
	for id, p := range s.pending {
		if p.conn == conn {
			delete(s.pending, id)
			p.timer.Stop()
			p.done <- requestResult{err: ErrAgentOffline}
		}
	}
}

// answer settles a pending request with an agent's command_response.
func (s *Service) answer(id string, msg *goapi.Object) {
	s.mu.Lock()
	p, ok := s.pending[id]
	if ok {
		delete(s.pending, id)
		p.timer.Stop()
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	if goapi.Truthy(msg.Get("ok")) {
		data := msg.Get("data")
		if !goapi.Truthy(data) {
			data = nil
		}
		p.done <- requestResult{data: data}
		return
	}
	reason := msg.Get("error")
	if !goapi.Truthy(reason) {
		reason = "agent_error"
	}
	p.done <- requestResult{err: &AgentError{Message: errorText(reason)}}
}

// errorText is String(value), the message new Error(value) takes.
func errorText(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return jsbind.NumberString(t)
	case []any:
		parts := make([]string, len(t))
		for i, el := range t {
			if el != nil {
				parts[i] = errorText(el)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}
