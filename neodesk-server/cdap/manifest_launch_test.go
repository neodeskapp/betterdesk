package cdap

import (
	"context"
	"testing"
)

// TestManifestTakesTheAudioWidgetAndTheTerminalCapability: a device
// registers with an audio widget, which the IoT device page renders, and
// with the capability terminal, which opens its terminal as the
// capability commands does. A device with neither has no terminal (BUGS
// L219).
func TestManifestTakesTheAudioWidgetAndTheTerminalCapability(t *testing.T) {
	w := newCDAPWorld(t)
	createAPIKey(t, w.db, "apikey-term-0123456789abcdef", "term", "global_admin", "")
	for _, d := range []struct {
		id           string
		capabilities []string
		terminal     bool
	}{
		{"dev-terminal", []string{"telemetry", "terminal"}, true},
		{"dev-commands", []string{"commands"}, true},
		{"dev-telemetry", []string{"telemetry"}, false},
	} {
		c := w.dial(t)
		if m := c.authWith("apikey-term-0123456789abcdef", d.id); m.Type != "auth_result" {
			t.Fatalf("auth of %s: %s %s", d.id, m.Type, m.Payload)
		}
		c.send("register", map[string]any{"manifest": map[string]any{
			"device":       map[string]any{"name": d.id, "type": "iot"},
			"capabilities": d.capabilities,
			"widgets": []any{
				map[string]any{"id": "mic", "type": "audio", "label": "Microphone"},
				map[string]any{"id": "shell", "type": "terminal", "label": "Shell"},
			},
		}})
		if m := c.read(); m.Type != "registered" {
			t.Errorf("register of %s with an audio widget and %v: %s %s", d.id, d.capabilities, m.Type, m.Payload)
			continue
		}
		c.drain()
		browser, _ := sessionBrowser(t)
		_, err := w.g.StartTerminalSession(context.Background(), browser, d.id, "u", "admin", 80, 24)
		if d.terminal && err != nil {
			t.Errorf("terminal of %s: %v", d.id, err)
		}
		if !d.terminal && err == nil {
			t.Errorf("terminal of %s started, want a refusal", d.id)
		}
	}
}
