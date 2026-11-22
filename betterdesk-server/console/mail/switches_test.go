package mail

import (
	"context"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// L84: SMTP_SECURE takes every on value, not only "true".
func TestSMTPSecureFromTheEnvironmentIsASwitch(t *testing.T) {
	db := storetest.Migrated(t)
	for value, want := range map[string]string{"true": `"secure":true`, "YES": `"secure":true`, "1": `"secure":true`, "on": `"secure":true`, "off": `"secure":false`, "": `"secure":false`, "maybe": `"secure":false`} {
		s, _, _ := newTestService(db, map[string]string{"SMTP_HOST": "env.example.org", "SMTP_SECURE": value}, Options{})
		if got := marshal(t, s.GetSMTPSettings(context.Background()).Body); !strings.Contains(got, want) {
			t.Errorf("SMTP_SECURE=%q: %s, want %s", value, got, want)
		}
	}
}
