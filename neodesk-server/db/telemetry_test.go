package db

import (
	"testing"
	"time"
)

func TestTelemetrySnapshotsAndCommands(t *testing.T) {
	database := newTestDB(t)

	err := database.SaveTelemetrySnapshot(&TelemetrySnapshot{
		DeviceID:    "TEL123",
		Kind:        "metrics",
		SampleID:    "sample-1",
		Status:      "ok",
		Payload:     `{"cpu_percent":12.5}`,
		CollectedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("SaveTelemetrySnapshot: %v", err)
	}

	snapshot, err := database.GetTelemetrySnapshot("TEL123", "metrics")
	if err != nil {
		t.Fatalf("GetTelemetrySnapshot: %v", err)
	}
	if snapshot == nil || snapshot.SampleID != "sample-1" || snapshot.Status != "ok" {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}

	commandID, err := database.QueueTelemetryCommand(&TelemetryCommand{
		DeviceID:  "TEL123",
		Command:   "collect.metrics",
		Args:      `{}`,
		ExpiresAt: time.Now().UTC().Add(time.Minute).Format("2006-01-02 15:04:05"),
	})
	if err != nil {
		t.Fatalf("QueueTelemetryCommand: %v", err)
	}
	commands, err := database.GetPendingTelemetryCommands("TEL123", 10)
	if err != nil {
		t.Fatalf("GetPendingTelemetryCommands: %v", err)
	}
	if len(commands) != 1 || commands[0].ID != commandID {
		t.Fatalf("unexpected commands: %#v", commands)
	}

	if err := database.CompleteTelemetryCommand("TEL123", commandID, "ok", `{"status":"ok"}`); err != nil {
		t.Fatalf("CompleteTelemetryCommand: %v", err)
	}
	commands, err = database.GetPendingTelemetryCommands("TEL123", 10)
	if err != nil {
		t.Fatalf("GetPendingTelemetryCommands after completion: %v", err)
	}
	if len(commands) != 0 {
		t.Fatalf("completed command still pending: %#v", commands)
	}
}

// TestCompleteTelemetryCommandOnlyForItsDevice: a result completes a command
// only when it names the device the command was queued for (RV168).
func TestCompleteTelemetryCommandOnlyForItsDevice(t *testing.T) {
	freshStores(t, func(t *testing.T, store Database) {
		id, err := store.QueueTelemetryCommand(&TelemetryCommand{
			DeviceID:  "TELOWN01",
			Command:   "collect.metrics",
			Args:      `{}`,
			ExpiresAt: time.Now().UTC().Add(time.Minute).Format("2006-01-02 15:04:05"),
		})
		if err != nil {
			t.Fatal(err)
		}
		pending := func() int {
			t.Helper()
			commands, err := store.GetPendingTelemetryCommands("TELOWN01", 10)
			if err != nil {
				t.Fatal(err)
			}
			return len(commands)
		}
		if err := store.CompleteTelemetryCommand("TELOTHER", id, "ok", `{"status":"ok"}`); err != nil {
			t.Fatal(err)
		}
		if got := pending(); got != 1 {
			t.Fatalf("after another device's result: %d pending, want 1", got)
		}
		if err := store.CompleteTelemetryCommand("TELOWN01", id, "ok", `{"status":"ok"}`); err != nil {
			t.Fatal(err)
		}
		if got := pending(); got != 0 {
			t.Errorf("after its own device's result: %d pending, want 0", got)
		}
	})
}
