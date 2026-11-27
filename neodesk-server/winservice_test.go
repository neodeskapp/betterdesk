package main

import (
	"sync"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/updater"
)

// reports collects what the service sends to the service manager.
type reports struct {
	mu   sync.Mutex
	list []serviceReport
}

func (r *reports) add(s serviceReport) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list = append(r.list, s)
}

func (r *reports) all() []serviceReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]serviceReport(nil), r.list...)
}

// A stop of the service closes the server's stop channel and waits until
// the server has stopped all its parts: the status is "stop pending" with
// a growing checkpoint until then, and the service ends with status 0, so
// the recovery settings do not start it again.
func TestServiceStopWaitsForTheServer(t *testing.T) {
	stopped := make(chan struct{})
	server := func(stop <-chan struct{}) int {
		<-stop
		time.Sleep(60 * time.Millisecond) // the deferred stops of serverMain
		close(stopped)
		return 0
	}
	requests := make(chan serviceRequest)
	var got reports
	result := make(chan int, 1)
	go func() { result <- superviseServer(server, requests, got.add, 10*time.Millisecond, time.Minute) }()

	requests <- requestInterrogate
	requests <- requestStop
	code := <-result
	select {
	case <-stopped:
	default:
		t.Fatal("the service ended before the server had stopped")
	}
	if code != 0 {
		t.Errorf("exit status %d after a stop, want 0", code)
	}
	list := got.all()
	if len(list) < 4 {
		t.Fatalf("reports %+v: want running, running again, then stop pending with checkpoints", list)
	}
	if list[0].State != serviceRunning || list[1].State != serviceRunning {
		t.Errorf("the service did not report running first and on the interrogation: %+v", list[:2])
	}
	pending := list[2:]
	for i, r := range pending {
		if r.State != serviceStopPending {
			t.Fatalf("report %d during the stop: %+v", i, r)
		}
		if r.WaitHint <= 0 {
			t.Errorf("report %d has no wait hint: %+v", i, r)
		}
		if i > 0 && r.CheckPoint <= pending[i-1].CheckPoint {
			t.Errorf("the checkpoint did not grow: %+v", pending)
		}
	}
}

// A restart the console asks for ends the server with
// updater.ExitRestart, and the service reports that status: the
// installer's recovery settings start the service again on it.
func TestServiceEndsWithTheServersRestart(t *testing.T) {
	server := func(<-chan struct{}) int { return updater.ExitRestart }
	var got reports
	code := superviseServer(server, make(chan serviceRequest), got.add, 10*time.Millisecond, time.Minute)
	if code != updater.ExitRestart {
		t.Errorf("exit status %d, want the restart request %d", code, updater.ExitRestart)
	}
	if list := got.all(); len(list) != 1 || list[0].State != serviceRunning {
		t.Errorf("reports %+v, want running only", list)
	}
}

// A server that does not stop within the limit does not keep the
// service in "stop pending" for ever.
func TestServiceStopHasALimit(t *testing.T) {
	hang := make(chan struct{})
	defer close(hang)
	server := func(<-chan struct{}) int { <-hang; return 0 }
	requests := make(chan serviceRequest, 2)
	requests <- requestStop
	requests <- requestStop
	start := time.Now()
	code := superviseServer(server, requests, func(serviceReport) {}, 5*time.Millisecond, 50*time.Millisecond)
	if code == 0 {
		t.Errorf("a server that did not stop ended the service with status 0")
	}
	if waited := time.Since(start); waited > 5*time.Second {
		t.Errorf("the stop took %s, past its limit", waited)
	}
}
