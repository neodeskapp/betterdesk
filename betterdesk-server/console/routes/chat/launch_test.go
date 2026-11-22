package chat

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestWelcomeClaimsNoEncryption: the welcome a client gets for hello lists
// what the relay does, and the relay sends and stores messages as they
// come, so end-to-end encryption is not among them (BUGS L212).
func TestWelcomeClaimsNoEncryption(t *testing.T) {
	relay := startRelay(t, RelayOptions{})
	dev1 := relay.agent(t, "dev1")
	dev1.send(t, `{"type":"hello"}`)
	welcome := ""
	for deadline := time.Now().Add(5 * time.Second); welcome == "" && time.Now().Before(deadline); {
		for _, m := range dev1.take() {
			if strings.HasPrefix(m, `{"type":"welcome"`) {
				welcome = m
			}
		}
	}
	var frame struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(welcome), &frame); err != nil {
		t.Fatalf("welcome %q: %v", welcome, err)
	}
	want := "multi_conversation contacts groups history read_receipts typing presence file_share"
	if got := strings.Join(frame.Capabilities, " "); got != want {
		t.Errorf("capabilities %q, want %q", got, want)
	}
}
