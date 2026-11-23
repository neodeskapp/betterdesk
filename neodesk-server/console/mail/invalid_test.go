package mail

import (
	"context"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// A stored configuration whose host is no text or whose port is no whole
// number from 1 to 65535 is invalid: sending and the connection test fail
// with "Invalid SMTP configuration" and nothing is connected, where Node
// threw the name lookup's exception (which ended the process) or the
// TypeError of its log line (BUGS EM4, EM6).
func TestAStoredHostThatIsNoTextIsInvalid(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		for _, stored := range []string{
			`{"host":5}`, `{"host":true}`, `{"host":["127.0.0.1"]}`, `{"host":{"toString":1}}`, `{"host":{"a":1}}`,
			`{"host":"h","port":{"toString":1}}`, `{"host":"h","port":"abc"}`, `{"host":"h","port":"25.5"}`, `{"host":"h","port":25.5}`,
			`{"host":"h","port":70000}`, `{"host":"h","port":-1}`, `{"host":"h","port":[25]}`, `{"host":"h","port":true}`,
		} {
			storeConfig(t, db, stored)
			s, transport, log := newTestService(db, nil, Options{})
			answer := s.TestSMTPSettings(ctx)
			if answer.Status != 200 || marshal(t, answer.Body) != `{"success":false,"error":"Invalid SMTP configuration"}` {
				t.Errorf("%s: test %d %s", stored, answer.Status, marshal(t, answer.Body))
			}
			sent, err := s.SendEmail(ctx, Email{To: []string{"a@example.org"}, Subject: "s"})
			if sent || err != nil || len(transport.sends)+len(transport.verifies) != 0 {
				t.Errorf("%s: sent %v %v, %d connections", stored, sent, err, len(transport.sends)+len(transport.verifies))
			}
			if want := [][2]string{{"error", "Send failed: Invalid SMTP configuration"}}; !sameLogs(log.lines, want) {
				t.Errorf("%s: logged %q", stored, log.lines)
			}
		}
		// A port that is not set takes the default; one that is set is a
		// number or text of one
		for stored, port := range map[string]int{
			`{"host":"h"}`: 587, `{"host":"h","port":null}`: 587, `{"host":"h","port":0}`: 587, `{"host":"h","port":""}`: 587,
			`{"host":"h","port":false,"secure":true}`: 587, `{"host":"h","port":2525}`: 2525, `{"host":"h","port":" 2525 "}`: 2525,
		} {
			storeConfig(t, db, stored)
			s, transport, _ := newTestService(db, nil, Options{})
			if result, err := s.TestConnection(ctx); err != nil || !result.Success || len(transport.verifies) != 1 || transport.verifies[0].Port != port {
				t.Errorf("%s: %s %v %+v, want port %d", stored, marshal(t, result), err, transport.verifies, port)
			}
		}
	})
}
