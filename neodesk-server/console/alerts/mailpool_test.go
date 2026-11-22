package alerts

import (
	"context"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/mail"
)

// holdingMailer holds every mail until release is closed and counts how
// many it held at once.
type holdingMailer struct {
	release         chan struct{}
	inFlight, most  atomic.Int32
	sent, cancelled atomic.Int32
}

func (m *holdingMailer) SendAlertEmail(ctx context.Context, _ mail.Alert, _ mail.AlertRule) (bool, error) {
	n := m.inFlight.Add(1)
	defer m.inFlight.Add(-1)
	for {
		most := m.most.Load()
		if n <= most || m.most.CompareAndSwap(most, n) {
			break
		}
	}
	select {
	case <-m.release:
		m.sent.Add(1)
		return true, nil
	case <-ctx.Done():
		m.cancelled.Add(1)
		return false, ctx.Err()
	}
}

// A sweep that finds 3,000 devices offline sends their alert mails through
// a few connections at a time, with a bounded number waiting; the mails
// beyond that are dropped and logged instead of opening a connection each
// (BUGS RV35).
func TestAlertMailsGoThroughASmallPool(t *testing.T) {
	devices := make([]*goapi.Object, 3000)
	for i := range devices {
		devices[i] = device("id", "off-"+strconv.Itoa(i), "online", false, "last_online", "2026-11-09T10:00:00Z")
	}
	mem := &memStore{t: t, c: &memCase{Rules: []map[string]string{{"id": "1", "name": `"Offline"`, "condition_type": `"offline_duration"`,
		"condition_op": `"gt"`, "condition_value": "60", "notify_emails": `"ops@example.com"`}}}}
	mailer := &holdingMailer{release: make(chan struct{})}
	logs := &logLines{}
	engine := New(Options{Logger: logs, Mail: mailer, Devices: listOf(devices), MailWorkers: 3, MailQueue: 50})
	engine.store = mem
	before := runtime.NumGoroutine()

	if err := engine.PeriodicCheckAt(context.Background(), sweepNow); err != nil {
		t.Fatal(err)
	}
	if len(mem.created) != 3000 {
		t.Fatalf("%d alerts, want one per device", len(mem.created))
	}
	waitFor(t, "three mails on their way", func() bool { return mailer.inFlight.Load() == 3 })
	time.Sleep(50 * time.Millisecond)
	if most := mailer.most.Load(); most != 3 {
		t.Fatalf("%d mails on their way at once, want 3", most)
	}
	if extra := runtime.NumGoroutine() - before; extra > 10 {
		t.Fatalf("%d goroutines more than before the sweep", extra)
	}

	logs.mu.Lock()
	dropped := 0
	for _, line := range logs.lines {
		if line[0] == "error" && strings.HasPrefix(line[1], "[AlertEngine] Email dropped: ") {
			dropped++
		}
	}
	logs.mu.Unlock()
	close(mailer.release)
	engine.Wait()
	sent := int(mailer.sent.Load())
	if sent < 50 || sent > 53 || sent+dropped != 3000 {
		t.Fatalf("%d mails sent and %d dropped, want 50 to 53 sent and the rest dropped", sent, dropped)
	}
	if mailer.most.Load() != 3 {
		t.Fatalf("%d mails on their way at once, want 3", mailer.most.Load())
	}

	// The workers end with the queue, and a later alert starts one again
	waitFor(t, "the workers to end", func() bool {
		engine.mailMu.Lock()
		defer engine.mailMu.Unlock()
		return engine.mailWorkers == 0
	})
	engine.mailAlert(mail.Alert{}, mail.AlertRule{})
	engine.Wait()
	if got := mailer.sent.Load(); int(got) != sent+1 {
		t.Fatalf("%d mails after one more alert, want %d", got, sent+1)
	}
}
