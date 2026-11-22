package remoterelay

import (
	"fmt"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// waitUntil polls cond for up to two seconds.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A session goes once it has neither an agent nor a viewer, with its idle
// timer, so the list of sessions shows the live ones only (RM8).
func TestEmptySessionsAreDropped(t *testing.T) {
	check := wstest.Goroutines(t)
	clock := &fakeClock{now: time.UnixMilli(1700000000000)}
	svc, addr, stop := testRelay(t, func(o *Options) { o.Clock = clock })
	agent := dial(t, addr, "/ws/remote-agent/dev1?token=tok-dev1-good", nil)
	waitUntil(t, "the agent's session", func() bool { return fmt.Sprint(svc.ActiveSessions()) == "[dev1]" })
	viewer := dial(t, addr, "/ws/remote-viewer/dev1", operator)
	waitUntil(t, "the viewer", func() bool { s := svc.SessionState("dev1"); return s != nil && s.ViewerCount == 1 })
	viewer.Close(1000)
	waitUntil(t, "the viewer gone", func() bool { s := svc.SessionState("dev1"); return s != nil && s.ViewerCount == 0 })
	if got := fmt.Sprint(svc.ActiveSessions()); got != "[dev1]" {
		t.Errorf("with its agent the session is listed as %s", got)
	}
	agent.Close(1000)
	waitUntil(t, "the session dropped", func() bool { return svc.SessionState("dev1") == nil })
	if got := svc.ActiveSessions(); len(got) != 0 {
		t.Errorf("sessions %v after agent and viewer left", got)
	}
	clock.mu.Lock()
	for _, timer := range clock.timers {
		if !timer.stopped && !timer.fired {
			t.Error("an idle timer of a dropped session is still running")
		}
	}
	clock.mu.Unlock()

	// A viewer without an agent leaves no session behind
	lone := dial(t, addr, "/ws/remote-viewer/dev2", operator)
	waitUntil(t, "the lone viewer", func() bool { return svc.SessionState("dev2") != nil })
	lone.Close(1000)
	waitUntil(t, "the lone viewer's session dropped", func() bool { return svc.SessionState("dev2") == nil })
	if got := svc.ActiveSessions(); len(got) != 0 {
		t.Errorf("sessions %v after the lone viewer left", got)
	}
	stop()
	check()
}

// Issuing a token drops the tokens whose time is up, so a token nobody
// uses does not stay until it is tried (RM8).
func TestExpiredAgentTokensGo(t *testing.T) {
	clock := &fakeClock{now: time.UnixMilli(1700000000000)}
	svc, _, _ := testRelay(t, func(o *Options) { o.Clock = clock })
	svc.IssueRemoteAgentToken("dev1")
	clock.Advance(AgentTokenTTL + time.Millisecond)
	svc.IssueRemoteAgentToken("dev2")
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if _, ok := svc.tokens["dev1"]; ok {
		t.Error("the expired token of dev1 is still kept")
	}
	if _, ok := svc.tokens["dev2"]; !ok {
		t.Error("the new token of dev2 is not kept")
	}
}
