package mail

import (
	"context"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

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
				`{"host":"h","port":"0465","user":null,"pass":null,"from":"f@x.org","alert_email":null}`,
				`{"host":"h","port":465,"secure":false,"user":"","pass":"p","from":"f@x.org","alert_email":""}`,
			},
			{
				`{"host":"h","port":null,"pass":"********","from":"f@x.org","alert_email":"","tlsVerify":true}`,
				`{"host":"h","port":587,"secure":false,"user":"","pass":"p","from":"f@x.org","alert_email":"","tlsVerify":true}`,
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
