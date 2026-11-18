package bdrelay

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

func newRelay(t *testing.T) (*Service, string) {
	t.Helper()
	svc := New(Options{VerifyDevice: oracleVerify})
	reg := ws.New(ws.Options{})
	svc.Register(reg)
	srv := httptest.NewServer(reg)
	t.Cleanup(func() {
		srv.Close()
		reg.Close()
	})
	return svc, strings.TrimPrefix(srv.URL, "http://")
}

// TestTokenOfAnotherLength: Node's token compare threw on a token of
// another length and the process ended (BUGS WS2). The Go relay drops
// that connection without a close frame, keeps the session and goes on.
func TestTokenOfAnotherLength(t *testing.T) {
	// Checked after the server of the test has stopped
	t.Cleanup(wstest.Goroutines(t))
	svc, addr := newRelay(t)
	id, initiatorToken, _ := svc.CreateRelaySession("dev2", "dev1")
	q := url.Values{"session": {id}, "token": {"short"}, "role": {"initiator"}}
	c, _, err := wstest.Dial(addr, "/ws/bd-relay?"+q.Encode(), nil)
	if err != nil || c == nil {
		t.Fatalf("dial: %v", err)
	}
	if !c.Wait(3*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
		t.Fatal("the connection stayed open")
	}
	if _, code, _ := c.Closed(); code != 1006 {
		t.Errorf("closed with %d, want the connection dropped (1006)", code)
	}
	if svc.GetRelaySession(id) == nil {
		t.Error("the session is gone")
	}
	q.Set("token", initiatorToken)
	good, _, err := wstest.Dial(addr, "/ws/bd-relay?"+q.Encode(), nil)
	if err != nil || good == nil {
		t.Fatalf("dial: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if closed, code, reason := good.Closed(); closed {
		t.Errorf("the right token was closed with %d %q", code, reason)
	}
	good.Close(1000)
	c.Drop()
}

// TestRequestEndsWithItsCaller: a caller that goes away stops waiting;
// the request stays listed until its timeout, as in Node.
func TestRequestEndsWithItsCaller(t *testing.T) {
	svc, addr := newRelay(t)
	agent, _, err := wstest.Dial(addr, "/ws/bd-signal?device_id=dev1&token=tok-dev1-good", nil)
	if err != nil || agent == nil {
		t.Fatalf("dial: %v", err)
	}
	defer agent.Close(1000)
	agent.Wait(2*time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) > 0 })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := svc.RequestFromDevice(ctx, "dev1", "x", goapi.NewObject(), time.Second)
		done <- err
	}()
	agent.Wait(2*time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) > 1 })
	cancel()
	if err := <-done; err != context.Canceled {
		t.Errorf("err %v", err)
	}
	if _, _, pending := svc.Counts(); pending != 1 {
		t.Errorf("pending %d", pending)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, _, pending := svc.Counts(); pending != 0 {
		t.Errorf("pending after the timeout %d", pending)
	}
}

// TestNothingIsTakenBeforeTheTokenCheck: what an agent sends while its
// token is checked is dropped, as in Node, where no message listener was
// attached yet. A socket whose token then fails got no answer to its
// heartbeat, and its answer to another socket's request was not taken.
func TestNothingIsTakenBeforeTheTokenCheck(t *testing.T) {
	t.Cleanup(wstest.Goroutines(t))
	release := make(chan struct{})
	svc := New(Options{VerifyDevice: func(ctx context.Context, deviceID, token string) (bool, error) {
		if token != "slow-bad" {
			return oracleVerify(ctx, deviceID, token)
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return false, nil
	}})
	reg := ws.New(ws.Options{})
	svc.Register(reg)
	srv := httptest.NewServer(reg)
	t.Cleanup(func() {
		srv.Close()
		reg.Close()
	})
	addr := strings.TrimPrefix(srv.URL, "http://")

	agent, _, err := wstest.Dial(addr, "/ws/bd-signal?device_id=dev1&token=tok-dev1-good", nil)
	if err != nil || agent == nil {
		t.Fatalf("dial: %v", err)
	}
	defer agent.Close(1000)
	agent.Wait(2*time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) > 0 })
	result := make(chan any, 1)
	go func() {
		data, err := svc.RequestFromDevice(context.Background(), "dev1", "x", goapi.NewObject(), 5*time.Second)
		if err != nil {
			data = err.Error()
		}
		result <- data
	}()
	if !agent.Wait(2*time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) > 1 }) {
		t.Fatal("the agent got no request")
	}
	request, err := goapi.Parse(agent.Frames()[1].Data)
	if err != nil {
		t.Fatal(err)
	}
	requestID, _ := request.(*goapi.Object).Get("request_id").(string)

	other, _, err := wstest.Dial(addr, "/ws/bd-signal?device_id=dev1&token=slow-bad", nil)
	if err != nil || other == nil {
		t.Fatalf("dial: %v", err)
	}
	defer other.Drop()
	_ = other.Text(`{"type":"heartbeat"}`)
	_ = other.Text(`{"type":"command_response","request_id":` + quote(requestID) + `,"ok":true,"data":"forged"}`)
	time.Sleep(200 * time.Millisecond)
	close(release)
	if !other.Wait(2*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
		t.Fatal("the socket with the bad token stayed open")
	}
	if _, code, reason := other.Closed(); code != 4403 || reason != "Invalid token" {
		t.Errorf("closed with %d %q", code, reason)
	}
	if f := other.Frames(); len(f) != 0 {
		t.Errorf("the socket got %q before its token was checked", f[0].Text())
	}
	select {
	case data := <-result:
		t.Fatalf("the request was answered by the socket with the bad token: %v", data)
	case <-time.After(100 * time.Millisecond):
	}
	_ = agent.Text(`{"type":"command_response","request_id":` + quote(requestID) + `,"ok":true,"data":"real"}`)
	select {
	case data := <-result:
		if data != "real" {
			t.Errorf("answer %v", data)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the agent's answer was not taken")
	}
}

// TestSweepWaitsTwiceTheTTL: the sweep ends a pending session once it is
// more than twice SessionTTL old (Node: now - createdAt > SESSION_TTL_MS *
// 2), not at the TTL a join checks.
func TestSweepWaitsTwiceTheTTL(t *testing.T) {
	clock := &fakeClock{now: time.UnixMilli(1_700_000_000_000)}
	svc := New(Options{VerifyDevice: oracleVerify, Clock: clock})
	id, _, _ := svc.CreateRelaySession("dev2", "dev1")
	clock.Advance(SessionTTL + time.Millisecond)
	svc.Cleanup()
	if svc.GetRelaySession(id) == nil {
		t.Fatal("ended just after the TTL")
	}
	clock.Advance(SessionTTL - time.Millisecond)
	svc.Cleanup()
	if svc.GetRelaySession(id) == nil {
		t.Fatal("ended at twice the TTL")
	}
	clock.Advance(time.Millisecond)
	svc.Cleanup()
	if svc.GetRelaySession(id) != nil {
		t.Fatal("still there after twice the TTL")
	}
}

// FuzzSignalMessage feeds agent messages to the signal handler: nothing
// panics, and only a heartbeat or an accepted relay request is answered.
func FuzzSignalMessage(f *testing.F) {
	for _, s := range []string{`{"type":"heartbeat"}`, `{"type":"relay_accept","session_id":"s"}`, `{"type":"relay_reject","session_id":"s"}`,
		`{"type":"command_response","request_id":"r","ok":true,"data":[1]}`, `null`, `[]`, `"x"`, `{"type":5}`, "\xff{", `{"type":"command_response","request_id":"r","error":{"a":[null]}}`} {
		f.Add([]byte(s), false)
	}
	svc := New(Options{VerifyDevice: oracleVerify})
	id, _, _ := svc.CreateRelaySession("dev2", "dev1")
	f.Fuzz(func(t *testing.T, data []byte, binary bool) {
		text := strings.ReplaceAll(string(data), `"s"`, `"`+id+`"`)
		svc.pending["r"] = &pendingRequest{done: make(chan requestResult, 1), timer: realClock{}.AfterFunc(time.Hour, func() {})}
		reply := svc.signalMessage("dev1", binary, []byte(text))
		if reply != "" && !strings.HasPrefix(reply, `{"type":"heartbeat_ack","ts":`) && !strings.HasPrefix(reply, `{"type":"relay_ready","session_id":"`+id+`",`) {
			t.Fatalf("%q answered %q", text, reply)
		}
		if p := svc.pending["r"]; p != nil {
			p.timer.Stop()
			delete(svc.pending, "r")
		}
	})
}
