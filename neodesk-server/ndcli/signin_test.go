package ndcli

import (
	"strings"
	"testing"
)

// TestSavedSigninCommandsNeverUseOrShowThePassword: the command line sets,
// switches, clears and shows the status of a device's saved sign-in; the
// password is read from standard input rather than the command line, and
// no command uses the details or reads them back, so no API token and no
// MCP client can (L469).
func TestSavedSigninCommandsNeverUseOrShowThePassword(t *testing.T) {
	tab, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"signin":       "GET /api/devices/{id}/signin",
		"signin-set":   "PUT /api/devices/{id}/signin",
		"signin-auto":  "PATCH /api/devices/{id}/signin",
		"signin-clear": "DELETE /api/devices/{id}/signin",
	}
	for action, route := range want {
		c := tab.Find("devices", action)
		if c == nil || c.Route != route {
			t.Errorf("devices %s: %+v, want the route %s", action, c, route)
		}
	}
	if c := tab.Find("devices", "signin-set"); c != nil {
		secret := false
		for _, f := range c.Flags {
			if f.Name == "password" {
				secret = f.Secret
			}
		}
		if !secret {
			t.Error("devices signin-set takes the password on the command line only")
		}
	}
	for _, c := range tab.Commands {
		if strings.Contains(c.Route, "/signin/use") {
			t.Errorf("%s uses the saved sign-in (%s)", c.Name, c.Route)
		}
	}
	excluded := false
	for _, e := range tab.Excluded {
		if e.Route == "POST /api/devices/{id}/signin/use" {
			excluded = true
		}
	}
	if !excluded {
		t.Error("the use of a saved sign-in is not on the excluded list")
	}
}
