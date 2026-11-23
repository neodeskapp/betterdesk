package mail

import (
	"context"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// The console's SMTP_REQUIRE_TLS reaches the connection with every stored
// and every environment configuration, so a login never goes out without
// TLS unless the switch is off (BUGS EM10).
func TestRequireTLSReachesTheConnection(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	for _, require := range []bool{true, false} {
		for _, stored := range []string{`{"host":"h","user":"u","pass":"p"}`, ""} {
			storeConfig(t, db, stored)
			s, transport, _ := newTestService(db, map[string]string{"SMTP_HOST": "env.example.org", "SMTP_USER": "u", "SMTP_PASS": "p"}, Options{RequireTLS: require})
			if _, err := s.TestConnection(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SendEmail(ctx, Email{To: []string{"a@example.org"}, Subject: "s"}); err != nil {
				t.Fatal(err)
			}
			if len(transport.verifies) != 1 || len(transport.sends) != 1 ||
				transport.verifies[0].RequireTLS != require || transport.sends[0].settings.RequireTLS != require {
				t.Errorf("stored %q, require %v: verified with %+v, sent with %+v", stored, require, transport.verifies, transport.sends)
			}
		}
	}
	settings := Settings{Host: "h", Port: 25, User: "u", Pass: "p", RequireTLS: true}
	if opts := (&SMTP{}).options(settings); !opts.RequireTLS {
		t.Errorf("the SMTP transport drops RequireTLS: %+v", opts)
	}
}
