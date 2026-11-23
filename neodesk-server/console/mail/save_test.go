package mail

import (
	"context"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// The stored password can be removed: an empty password clears it, also
// with another host or user, where the mask and no password member keep it
// (BUGS RV40). The settings page sends the mask for a field left as it is.
func TestSaveClearsThePasswordForAnEmptyOne(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, transport, _ := newTestService(db, nil, Options{})
		save := func(body string) {
			t.Helper()
			if answer := s.PutSMTPSettings(ctx, bodyOf(body)); answer.Status != 200 {
				t.Fatalf("%s: %d %s", body, answer.Status, marshal(t, answer.Body))
			}
		}
		save(`{"host":"mail.example.org","user":"alerts","pass":"first-secret","from":"f@example.org"}`)
		save(`{"host":"mail.example.org","user":"","pass":"","from":"f@example.org"}`)
		if got := stored(t, db); got != `{"host":"mail.example.org","port":587,"secure":false,"user":"","pass":"","from":"f@example.org","alert_email":""}` {
			t.Fatalf("after a save with an empty user and password: %s", got)
		}
		if got := marshal(t, s.GetSMTPSettings(ctx).Body); !strings.Contains(got, `"pass":""`) {
			t.Fatalf("read back: %s", got)
		}
		// A user entered again does not bring the old password back
		save(`{"host":"mail.example.org","user":"alerts","pass":"********","from":"f@example.org"}`)
		s.TestSMTPSettings(ctx)
		if len(transport.verifies) != 1 || transport.verifies[0].Pass != "" {
			t.Fatalf("the test connected with %+v", transport.verifies)
		}
		// An empty password with another host is no refusal: nothing is kept
		save(`{"host":"mail.example.org","user":"alerts","pass":"second-secret","from":"f@example.org"}`)
		save(`{"host":"relay.example.net","user":"alerts","pass":"","from":"f@example.org"}`)
		if got := stored(t, db); !strings.Contains(got, `"host":"relay.example.net"`) || !strings.Contains(got, `"pass":""`) {
			t.Fatalf("an empty password for another host: %s", got)
		}
	})
}

// The stored password is kept only for the server and the account it was
// given for. A save that changes the host, the port or the user and sends
// the mask or no password is refused, the row stays as it was,
// and the connection test still signs in at the stored server (BUGS RV33).
func TestSaveAsksForThePasswordOnAnotherServer(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, transport, _ := newTestService(db, nil, Options{})
		first := `{"host":"mail.example.org","port":465,"secure":true,"user":"alerts","pass":"first-secret","from":"f@example.org"}`
		if answer := s.PutSMTPSettings(ctx, bodyOf(first)); answer.Status != 200 {
			t.Fatalf("first save: %d %s", answer.Status, marshal(t, answer.Body))
		}
		before := stored(t, db)
		for _, body := range []string{
			`{"host":"attacker.example","port":465,"secure":true,"user":"alerts","pass":"********","from":"f@example.org","tlsVerify":false}`,
			`{"host":"attacker.example","port":465,"user":"alerts","from":"f@example.org"}`,
			`{"host":"attacker.example","port":465,"user":"alerts","pass":null,"from":"f@example.org"}`,
			`{"host":"mail.example.org","port":2525,"user":"alerts","pass":"********","from":"f@example.org"}`,
			`{"host":"mail.example.org","user":"alerts","pass":"********","from":"f@example.org"}`,
			`{"host":"mail.example.org","port":465,"user":"someone-else","pass":"********","from":"f@example.org"}`,
			`{"host":"mail.example.org","port":465,"pass":"********","from":"f@example.org"}`,
		} {
			answer := s.PutSMTPSettings(ctx, bodyOf(body))
			if answer.Status != 400 || marshal(t, answer.Body) != `{"error":"SMTP password is required when the host, port or user changes"}` || answer.Audit != nil {
				t.Errorf("%s: %d %s %+v, want 400", body, answer.Status, marshal(t, answer.Body), answer.Audit)
			}
			if got := stored(t, db); got != before {
				t.Fatalf("%s: a refused save stored %s", body, got)
			}
		}
		s.TestSMTPSettings(ctx)
		if len(transport.verifies) != 1 || transport.verifies[0].Host != "mail.example.org" || transport.verifies[0].Pass != "first-secret" {
			t.Fatalf("the test connected with %+v", transport.verifies)
		}

		// The same server and user keep it, with the port as text, the host
		// with white space around it, and whatever else changes
		for _, body := range []string{
			`{"host":" mail.example.org ","port":"465","user":"alerts","pass":"********","from":"other@example.org","tlsVerify":false}`,
			`{"host":"mail.example.org","port":465,"secure":false,"user":"alerts","from":"f@example.org"}`,
		} {
			if answer := s.PutSMTPSettings(ctx, bodyOf(body)); answer.Status != 200 {
				t.Fatalf("%s: %d %s", body, answer.Status, marshal(t, answer.Body))
			}
			if got := stored(t, db); !strings.Contains(got, `"pass":"first-secret"`) {
				t.Fatalf("%s: stored %s", body, got)
			}
		}
		// Another server with its own password is taken
		if answer := s.PutSMTPSettings(ctx, bodyOf(`{"host":"relay.example.net","port":25,"user":"","pass":"relay-secret","from":"f@example.org"}`)); answer.Status != 200 {
			t.Fatalf("a new server with a password: %d %s", answer.Status, marshal(t, answer.Body))
		}
		if got := stored(t, db); got != `{"host":"relay.example.net","port":25,"secure":false,"user":"","pass":"relay-secret","from":"f@example.org","alert_email":""}` {
			t.Fatalf("stored %s", got)
		}
		// A row without port and user stands for 587 and no user
		storeConfig(t, db, `{"host":"h","pass":"kept","from":"f@example.org"}`)
		if answer := s.PutSMTPSettings(ctx, bodyOf(`{"host":"h","port":587,"user":"","pass":"********","from":"f@example.org"}`)); answer.Status != 200 || !strings.Contains(stored(t, db), `"pass":"kept"`) {
			t.Fatalf("a row without port and user: %d %s, stored %s", answer.Status, marshal(t, answer.Body), stored(t, db))
		}
		// With no password stored there is nothing to keep, and any server
		// may be saved without one
		storeConfig(t, db, `{"host":"h","port":587,"user":"u","pass":"","from":"f@example.org"}`)
		if answer := s.PutSMTPSettings(ctx, bodyOf(`{"host":"other","port":25,"user":"v","pass":"********","from":"f@example.org"}`)); answer.Status != 200 || !strings.Contains(stored(t, db), `"pass":""`) {
			t.Fatalf("nothing stored: %d %s, stored %s", answer.Status, marshal(t, answer.Body), stored(t, db))
		}
	})
}

// A save of the SMTP settings takes each member only with its type: the
// host as text that is not blank, the port as a whole number from 1 to
// 65535 (or text of its digits), secure and tlsVerify as flags, user,
// pass and from as text, and the alert address as plain addresses.
// Anything else is refused with 400 and nothing is stored (BUGS EM5, and
// EM16 for the alert address).
func TestSaveRefusesMembersOfTheWrongType(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, _, _ := newTestService(db, nil, Options{})
		for body, want := range map[string]string{
			`{"host":" \t "}`:                               "SMTP host is required",
			`{"host":5}`:                                    "Invalid host",
			`{"host":true}`:                                 "Invalid host",
			`{"host":["h"]}`:                                "Invalid host",
			`{"host":{"toString":1}}`:                       "Invalid host",
			`{"host":"h","port":"25abc"}`:                   "Invalid port",
			`{"host":"h","port":" 25"}`:                     "Invalid port",
			`{"host":"h","port":"0x19"}`:                    "Invalid port",
			`{"host":"h","port":"1e3"}`:                     "Invalid port",
			`{"host":"h","port":"+25"}`:                     "Invalid port",
			`{"host":"h","port":"25.0"}`:                    "Invalid port",
			`{"host":"h","port":""}`:                        "Invalid port",
			`{"host":"h","port":"65536"}`:                   "Invalid port",
			`{"host":"h","port":1e21}`:                      "Invalid port",
			`{"host":"h","port":25.5}`:                      "Invalid port",
			`{"host":"h","port":0}`:                         "Invalid port",
			`{"host":"h","port":65536}`:                     "Invalid port",
			`{"host":"h","port":true}`:                      "Invalid port",
			`{"host":"h","port":[25]}`:                      "Invalid port",
			`{"host":"h","port":{"toString":1}}`:            "Invalid port",
			`{"host":"h","secure":"false"}`:                 "Invalid secure",
			`{"host":"h","secure":0}`:                       "Invalid secure",
			`{"host":"h","secure":null}`:                    "Invalid secure",
			`{"host":"h","tlsVerify":"yes"}`:                "Invalid tlsVerify",
			`{"host":"h","tlsVerify":null}`:                 "Invalid tlsVerify",
			`{"host":"h","user":5}`:                         "Invalid user",
			`{"host":"h","pass":["x"]}`:                     "Invalid pass",
			`{"host":"h","pass":false}`:                     "Invalid pass",
			`{"host":"h","from":7}`:                         "Invalid from",
			`{"host":"h","alert_email":8}`:                  "Invalid alert_email",
			`{"host":"h","alert_email":{"toString":1}}`:     "Invalid alert_email",
			`{"host":"h","alert_email":"a@b.c\r\nCc: x@y"}`: "Invalid alert_email",
			`{"host":"h","alert_email":"Ops <ops@x.org>"}`:  "Invalid alert_email",
			`{"host":"h","alert_email":"ops@1.2.3"}`:        "Invalid alert_email",
		} {
			answer := s.PutSMTPSettings(ctx, bodyOf(body))
			if answer.Status != 400 || marshal(t, answer.Body) != `{"error":"`+want+`"}` || answer.Audit != nil {
				t.Errorf("%s: %d %s, want 400 %s", body, answer.Status, marshal(t, answer.Body), want)
			}
		}
		if got := stored(t, db); got != "" {
			t.Fatalf("a refused save stored %s", got)
		}
		for _, c := range []struct{ body, want string }{
			{
				`{"host":" h ","port":2525,"secure":true,"user":"u","pass":"p","from":"f@x.org","alert_email":" a@x.org, b@y.org ","tlsVerify":false}`,
				`{"host":"h","port":2525,"secure":true,"user":"u","pass":"p","from":"f@x.org","alert_email":"a@x.org, b@y.org","tlsVerify":false}`,
			},
			{
				`{"host":"h","port":"02525","user":"u","pass":null,"from":"f@x.org","alert_email":null}`,
				`{"host":"h","port":2525,"secure":false,"user":"u","pass":"p","from":"f@x.org","alert_email":""}`,
			},
			{
				`{"host":"h","port":null,"user":null,"pass":"q","from":"f@x.org"}`,
				`{"host":"h","port":587,"secure":false,"user":"","pass":"q","from":"f@x.org","alert_email":""}`,
			},
			{
				`{"host":"h","pass":"********","from":"f@x.org","alert_email":"","tlsVerify":true}`,
				`{"host":"h","port":587,"secure":false,"user":"","pass":"q","from":"f@x.org","alert_email":"","tlsVerify":true}`,
			},
		} {
			if answer := s.PutSMTPSettings(ctx, bodyOf(c.body)); answer.Status != 200 {
				t.Fatalf("%s: %d %s", c.body, answer.Status, marshal(t, answer.Body))
			}
			if got := stored(t, db); got != c.want {
				t.Errorf("%s stored %s, want %s", c.body, got, c.want)
			}
		}
	})
}
