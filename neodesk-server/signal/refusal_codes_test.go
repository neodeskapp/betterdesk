package signal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestEveryRefusalHasAReasonCode: the console's viewer turns the signal
// server's refusal values into reason codes through one table
// (web-ui/src/lib/remote/server-refusals.json); a refusal without a code
// would reach the viewer as an unknown failure (L556).
func TestEveryRefusalHasAReasonCode(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "web-ui", "src", "lib", "remote", "server-refusals.json"))
	if err != nil {
		t.Fatal(err)
	}
	var codes map[string]string
	if err := json.Unmarshal(data, &codes); err != nil {
		t.Fatal(err)
	}
	refusals := []string{
		refuseTargetOffline, refuseInitiatorNotAuthorized, refuseBillingSuspended, refuseBillingBlocked,
		refuseRelayTicketRejected, refuseRelayProtocolMismatch, quickSupportRefusal,
	}
	for _, r := range featureRefusals {
		refusals = append(refusals, r)
	}
	for _, r := range refusals {
		if codes[r] == "" {
			t.Errorf("the refusal %q has no reason code in server-refusals.json", r)
		}
	}
}
