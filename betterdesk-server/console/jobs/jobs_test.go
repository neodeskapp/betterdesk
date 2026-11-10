package jobs

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestJobsRunFromStartToStop(t *testing.T) {
	r := New()
	var running atomic.Int32
	started := make(chan string, 3)
	job := func(name string) func(ctx context.Context) {
		return func(ctx context.Context) {
			running.Add(1)
			started <- name
			<-ctx.Done()
			time.Sleep(20 * time.Millisecond)
			running.Add(-1)
		}
	}
	r.Add("first", job("first"))
	r.Add("second", job("second"))
	select {
	case name := <-started:
		t.Fatalf("job %s ran before Start", name)
	case <-time.After(30 * time.Millisecond):
	}

	r.Start(context.Background())
	r.Start(context.Background())
	<-started
	<-started
	r.Add("late", job("late"))
	if name := <-started; name != "late" {
		t.Errorf("a job added after Start did not start, got %s", name)
	}
	if got := strings.Join(r.Names(), ","); got != "first,second,late" {
		t.Errorf("Names = %s", got)
	}
	if n := running.Load(); n != 3 {
		t.Fatalf("%d jobs running, want 3 (a second Start must not start them again)", n)
	}

	if !r.Stop() {
		t.Error("Stop reports jobs still running")
	}
	if n := running.Load(); n != 0 {
		t.Errorf("%d jobs still running after Stop: Stop must wait for them", n)
	}
	r.Add("after", job("after"))
	r.Start(context.Background())
	select {
	case name := <-started:
		t.Errorf("job %s ran after Stop", name)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestJobsEndWithTheContext(t *testing.T) {
	r := New()
	ended := make(chan struct{})
	r.Add("loop", func(ctx context.Context) {
		<-ctx.Done()
		close(ended)
	})
	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)
	cancel()
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("the job did not end with the server's context")
	}
	r.Stop()
}

func TestEveryCallsOnTheInterval(t *testing.T) {
	r := New()
	var calls atomic.Int32
	r.Every("tick", 10*time.Millisecond, func(context.Context) { calls.Add(1) })
	r.Start(context.Background())
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	r.Stop()
	if calls.Load() < 3 {
		t.Fatalf("%d calls, want at least 3", calls.Load())
	}
	after := calls.Load()
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != after {
		t.Error("the timer still fires after Stop")
	}
}

func TestPanickingJobDoesNotTakeTheOthersDown(t *testing.T) {
	r := New()
	ok := make(chan struct{})
	r.Add("broken", func(context.Context) { panic("boom") })
	r.Add("fine", func(ctx context.Context) {
		close(ok)
		<-ctx.Done()
	})
	r.Start(context.Background())
	select {
	case <-ok:
	case <-time.After(2 * time.Second):
		t.Fatal("the other job did not run")
	}
	if !r.Stop() {
		t.Error("Stop did not finish")
	}
}

func TestStopWithoutStart(t *testing.T) {
	if !New().Stop() {
		t.Error("Stop on a registry that never started reports running jobs")
	}
}
