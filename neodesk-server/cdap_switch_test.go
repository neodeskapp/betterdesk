package main

import (
	"testing"

	"github.com/unitronix/betterdesk-server/config"
	"github.com/unitronix/betterdesk-server/db"
)

// TestSavedGatewaySwitchWinsOverTheEnvironment: Enable gateway and Disable
// gateway on the IoT devices page save cdap_enabled; at the next start the
// saved value decides whether the gateway runs, whatever CDAP_ENABLED
// says. Without a saved value (none, or the empty value a cancelled
// change puts back) CDAP_ENABLED decides, and the gateway is on by
// default (BUGS L219, DECISIONS A105).
func TestSavedGatewaySwitchWinsOverTheEnvironment(t *testing.T) {
	eachEnrollmentDatabase(t, func(t *testing.T, database db.Database) {
		for _, c := range []struct {
			name, saved, env string
			want             bool
		}{
			{"nothing saved, no variable", "", "", true},
			{"nothing saved, variable off", "", "N", false},
			{"saved off, no variable", "N", "", false},
			{"saved off, variable on", "N", "Y", false},
			{"saved on, variable off", "Y", "N", true},
			{"saved on in another spelling", "true", "0", true},
			{"saved value that is no switch", "maybe", "N", false},
		} {
			t.Run(c.name, func(t *testing.T) {
				if err := database.SetConfig("cdap_enabled", c.saved); err != nil {
					t.Fatal(err)
				}
				t.Setenv("CDAP_ENABLED", c.env)
				cfg := config.DefaultConfig()
				cfg.LoadEnv()
				applySavedCDAPEnabled(cfg, database)
				if cfg.CDAPEnabled != c.want {
					t.Errorf("gateway on = %v, want %v", cfg.CDAPEnabled, c.want)
				}
			})
		}
	})
}
