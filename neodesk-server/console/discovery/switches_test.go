package discovery

import "testing"

// L84: every off value of PANEL_MDNS turns the announcement off; other
// values leave it on.
func TestPanelMDNSTakesEveryOffValue(t *testing.T) {
	for value, want := range map[string]bool{"off": false, "0": false, "no": false, "N": false, "FALSE": false, "on": true, "1": true, "yes": true, "lan": true} {
		if got := (Settings{MDNS: value}).mdnsEnabled(); got != want {
			t.Errorf("PANEL_MDNS=%q: announcement %v, want %v", value, got, want)
		}
	}
}
