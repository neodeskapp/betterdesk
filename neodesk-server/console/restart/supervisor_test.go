package restart

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// A supervised process ends after an update or a restore through
// ExitLater, with the route's delay, and only then.
func TestExitLater(t *testing.T) {
	var delay time.Duration
	var scheduled func()
	exits := 0
	s := newService(t, Options{
		Exit:  func() { exits++ },
		After: func(d time.Duration, f func()) { delay, scheduled = d, f },
	})
	if !s.Supervised() {
		t.Fatal("a service with Exit is not supervised")
	}
	if !s.ExitLater(2*time.Second, "after the update") {
		t.Fatal("ExitLater refused although Exit is set")
	}
	if scheduled == nil || delay != 2*time.Second || exits != 0 {
		t.Fatalf("scheduled %v after %v, exits %d", scheduled != nil, delay, exits)
	}
	scheduled()
	if exits != 1 {
		t.Errorf("Exit ran %d times, want once", exits)
	}
}

// Without a supervisor nothing is scheduled and the caller learns it.
func TestExitLaterWithoutSupervisor(t *testing.T) {
	scheduled := false
	s := newService(t, Options{After: func(time.Duration, func()) { scheduled = true }})
	if s.Supervised() {
		t.Fatal("a service without Exit is supervised")
	}
	if s.ExitLater(time.Second, "x") || scheduled {
		t.Errorf("ExitLater without Exit: scheduled %v", scheduled)
	}
}

// A confirmed restart of a supervised process runs no command, keeps the
// transaction restarting and ends the process after ExitDelay.
func TestConfirmUnderASupervisor(t *testing.T) {
	var scheduled func()
	exits := 0
	s := newService(t, Options{
		RestartServer: SupervisorRestart("systemd"),
		Exit:          func() { exits++ },
		After:         func(_ time.Duration, f func()) { scheduled = f },
	})
	public := mustRegister(t, s, 1, "k")
	confirmed, err := s.Confirm(context.Background(), 1, public.ID())
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Phase() != PhaseRestarting {
		t.Fatalf("phase %q, want restarting", confirmed.Phase())
	}
	raw, err := json.Marshal(confirmed.Get("restart"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"source":"supervisor","daemonReload":null,"restarts":[{"component":"server","service":"systemd","success":true,"method":"exit"}]}` {
		t.Errorf("restart = %s", raw)
	}
	if scheduled == nil {
		t.Fatal("no exit scheduled")
	}
	scheduled()
	if exits != 1 {
		t.Errorf("Exit ran %d times", exits)
	}
}
