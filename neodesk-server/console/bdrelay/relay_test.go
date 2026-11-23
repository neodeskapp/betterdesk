package bdrelay

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
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

// openSignal opens a signal socket of a device with headers and waits for
// its welcome or its close: welcomed is false when it was closed, with the
// code.
func openSignal(t *testing.T, addr, device string, header http.Header) (c *wstest.Client, welcomed bool, code int) {
	t.Helper()
	c, _, err := wstest.Dial(addr, "/ws/nd-signal?device_id="+device+"&token=tok-"+device+"-good", header)
	if err != nil || c == nil {
		t.Fatalf("dial: %v", err)
	}
	if !c.Wait(3*time.Second, func(f []wstest.Frame, closed bool) bool { return closed || len(f) > 0 }) {
		t.Fatalf("%s got nothing", device)
	}
	closed, code, _ := c.Closed()
	return c, !closed, code
}

// TestAddressLimitCountsTheTrustedAddress: the limit of MaxRelayPerIP
// sockets per address counts the address the console's TRUST_PROXY gives.
// Without a trusted proxy an X-Forwarded-For the client wrote buys no
// fresh budget; behind a trusted proxy each client has its own.
func TestAddressLimitCountsTheTrustedAddress(t *testing.T) {
	t.Cleanup(wstest.Goroutines(t))
	loopback, err := httpx.ParseTrustProxy("loopback")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name      string
		clientIP  func(*http.Request) string
		otherGets bool
		// relayCode is the close of a relay socket that says it is
		// 10.0.0.2 and names no session: the limit (4429) is checked first
		relayCode int
	}{
		{"trust nobody", nil, false, 4429},
		{"trust loopback", loopback.ClientIP, true, 4404},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc := New(Options{VerifyDevice: oracleVerify})
			reg := ws.New(ws.Options{ClientIP: c.clientIP})
			svc.Register(reg)
			srv := httptest.NewServer(reg)
			defer func() {
				srv.Close()
				reg.Close()
			}()
			addr := strings.TrimPrefix(srv.URL, "http://")
			from := func(ip string) http.Header { return http.Header{"X-Forwarded-For": {ip}} }
			for i := range MaxRelayPerIP {
				s, ok, code := openSignal(t, addr, fmt.Sprintf("dev%d", i), from("10.0.0.1"))
				defer s.Close(1000)
				if !ok {
					t.Fatalf("socket %d closed with %d", i, code)
				}
			}
			over, ok, code := openSignal(t, addr, "dev10", from("10.0.0.1"))
			defer over.Drop()
			if ok || code != 4429 {
				t.Errorf("the eleventh socket of 10.0.0.1: welcomed %v, closed with %d", ok, code)
			}
			other, ok, code := openSignal(t, addr, "dev11", from("10.0.0.2"))
			defer other.Drop()
			if ok != c.otherGets {
				t.Errorf("a socket that says it is 10.0.0.2: welcomed %v (closed with %d), want %v", ok, code, c.otherGets)
			}
			relay, _, err := wstest.Dial(addr, "/ws/nd-relay?session=none&token=x&role=initiator", from("10.0.0.2"))
			if err != nil || relay == nil {
				t.Fatalf("dial: %v", err)
			}
			defer relay.Drop()
			if !relay.Wait(3*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
				t.Fatal("the relay socket stayed open")
			}
			if _, code, _ := relay.Closed(); code != c.relayCode {
				t.Errorf("a relay socket that says it is 10.0.0.2 closed with %d, want %d", code, c.relayCode)
			}
		})
	}
}

// TestTokenOfAnotherLength: a relay token of another length is refused
// with 4403 "Invalid token" like a wrong one (GHSA-98mc-f6p7-gj85: Node's
// compare threw on it and the process ended), the session stays, and the
// right token still joins.
func TestTokenOfAnotherLength(t *testing.T) {
	// Checked after the server of the test has stopped
	t.Cleanup(wstest.Goroutines(t))
	svc, addr := newRelay(t)
	id, initiatorToken, _ := svc.CreateRelaySession("dev2", "dev1")
	for _, token := range []string{"x", initiatorToken[:63], initiatorToken + "0", initiatorToken + initiatorToken, strings.Repeat("é", 32)} {
		q := url.Values{"session": {id}, "token": {token}, "role": {"initiator"}}
		c, _, err := wstest.Dial(addr, "/ws/nd-relay?"+q.Encode(), nil)
		if err != nil || c == nil {
			t.Fatalf("dial: %v", err)
		}
		if !c.Wait(3*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
			t.Fatalf("token of %d bytes: the connection stayed open", len(token))
		}
		if _, code, reason := c.Closed(); code != 4403 || reason != "Invalid token" {
			t.Errorf("token of %d bytes: closed with %d %q, want 4403 Invalid token", len(token), code, reason)
		}
		c.Drop()
	}
	if svc.GetRelaySession(id) == nil {
		t.Fatal("the session is gone")
	}
	q := url.Values{"session": {id}, "token": {initiatorToken}, "role": {"initiator"}}
	good, _, err := wstest.Dial(addr, "/ws/nd-relay?"+q.Encode(), nil)
	if err != nil || good == nil {
		t.Fatalf("dial: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if closed, code, reason := good.Closed(); closed {
		t.Errorf("the right token was closed with %d %q", code, reason)
	}
	good.Close(1000)
}

// TestTokensMatch is the comparison of tests/bdRelay.tokenCompare.test.js:
// the exact token matches; a wrong token of the same length, a shorter or
// longer one, and an empty one on either side (also two empty ones) do
// not.
func TestTokensMatch(t *testing.T) {
	right := strings.Repeat("0123456789abcdef", 4)
	wrong := strings.Repeat("fedcba9876543210", 4)
	for _, c := range []struct {
		provided, expected string
		want               bool
	}{
		{right, right, true},
		{wrong, right, false},
		{"x", right, false},
		{right + right, right, false},
		{right[:63], right, false},
		{"", right, false},
		{right, "", false},
		{"", "", false},
	} {
		if got := tokensMatch(c.provided, c.expected); got != c.want {
			t.Errorf("tokensMatch(%q, %q) = %v, want %v", c.provided, c.expected, got, c.want)
		}
	}
}

// TestSlotTakenIsNotCounted: a socket refused with 4409 "Slot already
// taken" is counted for its address only while it is refused, so ten of
// them do not use up the address's limit and the other side still joins.
func TestSlotTakenIsNotCounted(t *testing.T) {
	t.Cleanup(wstest.Goroutines(t))
	svc, addr := newRelay(t)
	id, initiatorToken, targetToken := svc.CreateRelaySession("dev2", "dev1")
	join := func(role, token string) *wstest.Client {
		q := url.Values{"session": {id}, "token": {token}, "role": {role}}
		c, _, err := wstest.Dial(addr, "/ws/nd-relay?"+q.Encode(), nil)
		if err != nil || c == nil {
			t.Fatalf("dial: %v", err)
		}
		return c
	}
	first := join("initiator", initiatorToken)
	defer first.Close(1000)
	time.Sleep(50 * time.Millisecond)
	for range MaxRelayPerIP {
		again := join("initiator", initiatorToken)
		if !again.Wait(3*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
			t.Fatal("the second initiator stayed open")
		}
		if _, code, reason := again.Closed(); code != 4409 {
			t.Fatalf("second initiator closed with %d %q", code, reason)
		}
		again.Drop()
	}
	target := join("target", targetToken)
	defer target.Close(1000)
	if !target.Wait(3*time.Second, func(f []wstest.Frame, closed bool) bool { return closed || len(f) > 0 }) {
		t.Fatal("the target got nothing")
	}
	if closed, code, reason := target.Closed(); closed {
		t.Fatalf("the target was closed with %d %q", code, reason)
	}
	if f := target.Frames(); len(f) == 0 || !strings.Contains(f[0].Text(), `"paired"`) {
		t.Errorf("target frames %v", f)
	}
}

// TestRequestEndsWithItsCaller: a caller that goes away stops waiting;
// the request stays listed until its timeout, as in Node.
func TestRequestEndsWithItsCaller(t *testing.T) {
	svc, addr := newRelay(t)
	agent, _, err := wstest.Dial(addr, "/ws/nd-signal?device_id=dev1&token=tok-dev1-good", nil)
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

	agent, _, err := wstest.Dial(addr, "/ws/nd-signal?device_id=dev1&token=tok-dev1-good", nil)
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

	other, _, err := wstest.Dial(addr, "/ws/nd-signal?device_id=dev1&token=slow-bad", nil)
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

// waitCount waits until the address 127.0.0.1 has want sockets counted,
// and checks that the count stays there for a while.
func waitCount(t *testing.T, svc *Service, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for svc.perIP.Count("127.0.0.1") != want {
		if time.Now().After(deadline) {
			t.Fatalf("127.0.0.1 has %d sockets counted, want %d", svc.perIP.Count("127.0.0.1"), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if got := svc.perIP.Count("127.0.0.1"); got != want {
		t.Fatalf("127.0.0.1 has %d sockets counted, want %d", got, want)
	}
}

// TestAnErrorCountsDownOnce: a socket that ends with a protocol error (a
// message over the limit) is counted down once for its address, like any
// other end, on the signal socket and on both sides of a relay, so the
// address cannot hold more than MaxRelayPerIP sockets afterwards.
func TestAnErrorCountsDownOnce(t *testing.T) {
	t.Cleanup(wstest.Goroutines(t))
	t.Run("signal", func(t *testing.T) {
		svc, addr := newRelay(t)
		var open []*wstest.Client
		for i := range MaxRelayPerIP {
			c, ok, code := openSignal(t, addr, fmt.Sprintf("dev%d", i), nil)
			if !ok {
				t.Fatalf("socket %d closed with %d", i, code)
			}
			open = append(open, c)
		}
		if err := open[0].Text(strings.Repeat("x", SignalMaxPayload+1)); err != nil {
			t.Fatal(err)
		}
		if !open[0].Wait(3*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
			t.Fatal("the message over the limit did not close the socket")
		}
		waitCount(t, svc, MaxRelayPerIP-1)
		again, ok, code := openSignal(t, addr, "dev20", nil)
		if !ok {
			t.Errorf("the socket after the error closed with %d", code)
		}
		over, ok, code := openSignal(t, addr, "dev21", nil)
		if ok || code != 4429 {
			t.Errorf("an eleventh socket: welcomed %v, closed with %d", ok, code)
		}
		for _, c := range append(open, again, over) {
			c.Drop()
		}
	})
	t.Run("relay", func(t *testing.T) {
		svc, addr := newRelay(t)
		// Two signal sockets of the same address stay open throughout
		var open []*wstest.Client
		for i := range 2 {
			c, ok, code := openSignal(t, addr, fmt.Sprintf("dev%d", i), nil)
			if !ok {
				t.Fatalf("signal socket %d closed with %d", i, code)
			}
			open = append(open, c)
		}
		id, initiatorToken, targetToken := svc.CreateRelaySession("dev2", "dev1")
		join := func(role, token string) *wstest.Client {
			q := url.Values{"session": {id}, "token": {token}, "role": {role}}
			c, _, err := wstest.Dial(addr, "/ws/nd-relay?"+q.Encode(), nil)
			if err != nil || c == nil {
				t.Fatalf("dial: %v", err)
			}
			return c
		}
		initiator := join("initiator", initiatorToken)
		target := join("target", targetToken)
		if !target.Wait(3*time.Second, func(f []wstest.Frame, closed bool) bool { return closed || len(f) > 0 }) {
			t.Fatal("the target was not paired")
		}
		waitCount(t, svc, 4)
		if err := initiator.Binary(make([]byte, MaxFrameBytes+1)); err != nil {
			t.Fatal(err)
		}
		if !target.Wait(5*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
			t.Fatal("the other side stayed open")
		}
		if _, code, reason := target.Closed(); code != 1011 || reason != "WebSocket error" {
			t.Errorf("the other side closed with %d %q, want 1011 WebSocket error", code, reason)
		}
		waitCount(t, svc, 2)
		for _, c := range append(open, initiator, target) {
			c.Drop()
		}
	})
}

// TestSignalEndedDuringTheCheckIsNotCounted: a signal socket that ends
// while its token is checked is neither counted for its address nor
// listed as online, and does not replace the device's open socket: ten of
// them leave the address its whole budget.
func TestSignalEndedDuringTheCheckIsNotCounted(t *testing.T) {
	t.Cleanup(wstest.Goroutines(t))
	release := make(chan struct{})
	checking := make(chan struct{}, MaxRelayPerIP)
	svc := New(Options{VerifyDevice: func(ctx context.Context, deviceID, token string) (bool, error) {
		if token != "slow" {
			return oracleVerify(ctx, deviceID, token)
		}
		checking <- struct{}{}
		<-release
		return true, nil
	}})
	reg := ws.New(ws.Options{})
	svc.Register(reg)
	srv := httptest.NewServer(reg)
	t.Cleanup(func() {
		srv.Close()
		reg.Close()
	})
	addr := strings.TrimPrefix(srv.URL, "http://")

	first, ok, code := openSignal(t, addr, "dev0", nil)
	if !ok {
		t.Fatalf("dev0 closed with %d", code)
	}
	defer first.Close(1000)
	for i := range MaxRelayPerIP {
		c, _, err := wstest.Dial(addr, fmt.Sprintf("/ws/nd-signal?device_id=dev%d&token=slow", i), nil)
		if err != nil || c == nil {
			t.Fatalf("dial: %v", err)
		}
		<-checking
		c.Drop()
	}
	time.Sleep(200 * time.Millisecond)
	close(release)
	waitCount(t, svc, 1)
	if online := svc.OnlineDeviceIDs(); len(online) != 1 || online[0] != "dev0" {
		t.Errorf("online %v, want [dev0]", online)
	}
	if closed, code, reason := first.Closed(); closed {
		t.Errorf("dev0's socket was closed with %d %q", code, reason)
	}
	var open []*wstest.Client
	for i := 1; i < MaxRelayPerIP; i++ {
		c, ok, code := openSignal(t, addr, fmt.Sprintf("dev%d", 20+i), nil)
		if !ok {
			t.Fatalf("socket %d after the ended ones closed with %d", i, code)
		}
		open = append(open, c)
	}
	for _, c := range open {
		c.Close(1000)
	}
}

// TestDeleteSessionClosesTheJoinedSides: deleting a session (DELETE
// /api/nd/session/<id>) ends it: a side that already joined is closed with
// 1000 "Session deleted" instead of waiting with nothing to end it, and its
// address is counted down.
func TestDeleteSessionClosesTheJoinedSides(t *testing.T) {
	t.Cleanup(wstest.Goroutines(t))
	svc, addr := newRelay(t)
	join := func(id, role, token string) *wstest.Client {
		q := url.Values{"session": {id}, "token": {token}, "role": {role}}
		c, _, err := wstest.Dial(addr, "/ws/nd-relay?"+q.Encode(), nil)
		if err != nil || c == nil {
			t.Fatalf("dial: %v", err)
		}
		return c
	}
	closedWith := func(c *wstest.Client, name string) {
		t.Helper()
		if !c.Wait(3*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
			t.Fatalf("the %s stayed open", name)
		}
		if _, code, reason := c.Closed(); code != 1000 || reason != "Session deleted" {
			t.Errorf("the %s closed with %d %q, want 1000 Session deleted", name, code, reason)
		}
		c.Drop()
	}

	waiting, initiatorToken, _ := svc.CreateRelaySession("dev2", "dev1")
	alone := join(waiting, "initiator", initiatorToken)
	waitCount(t, svc, 1)
	if !svc.DeleteSession(waiting) {
		t.Fatal("DeleteSession found no session")
	}
	closedWith(alone, "side that waited")

	paired, initiatorToken, targetToken := svc.CreateRelaySession("dev2", "dev1")
	initiator := join(paired, "initiator", initiatorToken)
	target := join(paired, "target", targetToken)
	if !target.Wait(3*time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) > 0 }) {
		t.Fatal("not paired")
	}
	svc.DeleteSession(paired)
	closedWith(initiator, "initiator")
	closedWith(target, "target")
	waitCount(t, svc, 0)
	if svc.GetRelaySession(waiting) != nil || svc.GetRelaySession(paired) != nil {
		t.Error("a deleted session is still there")
	}
	if svc.DeleteSession(paired) {
		t.Error("a second delete found the session")
	}
}

// TestRequestOfAClosedSocketSettlesAtOnce: a request whose agent's signal
// socket ends (it closes, or a new socket of the device replaces it) is
// answered with ErrAgentOffline at once instead of waiting for its
// timeout, so the route serves what it serves for an agent that is gone.
func TestRequestOfAClosedSocketSettlesAtOnce(t *testing.T) {
	t.Cleanup(wstest.Goroutines(t))
	svc, addr := newRelay(t)
	request := func() <-chan error {
		done := make(chan error, 1)
		go func() {
			_, err := svc.RequestFromDevice(context.Background(), "dev1", "x", goapi.NewObject(), 30*time.Second)
			done <- err
		}()
		return done
	}
	settled := func(done <-chan error, how string) {
		t.Helper()
		select {
		case err := <-done:
			if !errors.Is(err, ErrAgentOffline) {
				t.Errorf("%s: %v, want agent_offline", how, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: the request still waits", how)
		}
	}
	gotRequest := func(c *wstest.Client) {
		t.Helper()
		if !c.Wait(3*time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) > 1 }) {
			t.Fatal("the agent got no request")
		}
	}

	agent, ok, code := openSignal(t, addr, "dev1", nil)
	if !ok {
		t.Fatalf("closed with %d", code)
	}
	done := request()
	gotRequest(agent)
	agent.Close(1000)
	settled(done, "closed socket")

	first, _, _ := openSignal(t, addr, "dev1", nil)
	done = request()
	gotRequest(first)
	second, ok, code := openSignal(t, addr, "dev1", nil)
	if !ok {
		t.Fatalf("the second socket closed with %d", code)
	}
	settled(done, "replaced socket")
	if _, _, pending := svc.Counts(); pending != 0 {
		t.Errorf("%d requests still listed", pending)
	}
	first.Drop()
	second.Close(1000)
}

// TestAgentThatStopsAnsweringIsDropped: an agent whose connection died
// without a close answers no ping; after two unanswered pings its signal
// socket is dropped, the device is no longer online and its request is
// answered as offline.
func TestAgentThatStopsAnsweringIsDropped(t *testing.T) {
	t.Cleanup(wstest.Goroutines(t))
	svc := New(Options{VerifyDevice: oracleVerify, PingInterval: 50 * time.Millisecond})
	reg := ws.New(ws.Options{})
	svc.Register(reg)
	srv := httptest.NewServer(reg)
	t.Cleanup(func() {
		srv.Close()
		reg.Close()
	})
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, "GET /ws/nd-signal?device_id=dev1&token=tok-dev1-good HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\n"+
		"Connection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	if head, err := bufio.NewReader(conn).ReadString('\n'); err != nil || !strings.HasPrefix(head, "HTTP/1.1 101") {
		t.Fatalf("%q %v", head, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for online, _ := svc.IsDeviceOnline("dev1"); !online; online, _ = svc.IsDeviceOnline("dev1") {
		if time.Now().After(deadline) {
			t.Fatal("the agent never came online")
		}
		time.Sleep(5 * time.Millisecond)
	}
	done := make(chan error, 1)
	go func() {
		_, err := svc.RequestFromDevice(context.Background(), "dev1", "x", goapi.NewObject(), 30*time.Second)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrAgentOffline) {
			t.Errorf("request: %v, want agent_offline", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the request still waits: the agent was not dropped")
	}
	if online, _ := svc.IsDeviceOnline("dev1"); online {
		t.Error("the agent is still online")
	}
	waitCount(t, svc, 0)
}
