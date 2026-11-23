package upstreamname

import "testing"

func TestNames(t *testing.T) {
	for in, want := range map[string]string{
		"NeoDesk Server Management":                                            "BetterDesk Server Management",
		"/opt/neodesk and NEODESK_API_URL":                                     "/opt/betterdesk and BETTERDESK_API_URL",
		"https://neode.sk/docs, neodeskapp":                                    "https://betterde.sk/docs, trybetterdesk",
		"ND_REGISTER_ALLOW_OPEN, AND_X":                                        "BD_REGISTER_ALLOW_OPEN, AND_X",
		"nd.sid=1; nd-theme-choice; and.sid":                                   "bd.sid=1; bd-theme-choice; and.sid",
		"/api/nd/register, /API/ND/x, /api/ndx, /ws/nd-relay, X-ND-Background": "/api/bd/register, /API/BD/x, /api/ndx, /ws/bd-relay, X-BD-Background",
		"github.com/neodeskapp/neodesk/neodesk-server/console":                 "github.com/unitronix/betterdesk-server/console",
		"UNITRONIX/BetterDesk-Client":                                          "UNITRONIX/BetterDesk-Client",
	} {
		if got := Names(in); got != want {
			t.Errorf("Names(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHolds(t *testing.T) {
	for in, want := range map[string]bool{
		"NeoDesk":              false,
		"the BetterDesk agent": true,
		"/opt/betterdesk":      true,
		"Better Desk":          true,
		"betterde.sk":          true,
		"a fork of github.com/UNITRONIX/BetterDesk":  false,
		"UNITRONIX/BetterDesk-Client and betterdesk": true,
	} {
		if got := Holds(in); got != want {
			t.Errorf("Holds(%q) = %v, want %v", in, got, want)
		}
	}
}
