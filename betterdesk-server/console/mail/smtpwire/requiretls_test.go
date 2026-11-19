package smtpwire

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

// With RequireTLS the login never goes out in plain text (BUGS EM10).
// Every recorded session with credentials is played again with the
// option: the client may send AUTH and what follows it only after TLS is
// on, and on a connection that is not secure it asks for STARTTLS even
// when the server does not offer it. A server that refuses EHLO does not
// get HELO and a login.
func TestCredentialsNeverGoOutWithoutTLS(t *testing.T) {
	scenarios := loadSessions(t)
	roots := trustedRoots(t)
	played := 0
	for i := range scenarios {
		sc := &scenarios[i]
		if sc.Options.User == "" || sc.Options.Pass == "" || (sc.Server.Listen != nil && !*sc.Server.Listen) {
			continue
		}
		played++
		t.Run(sc.Name, func(t *testing.T) {
			t.Parallel()
			s := startSink(t, sc.Server)
			opts := scenarioOptions(t, sc, s, roots)
			opts.RequireTLS = true
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if sc.Call == "verify" {
				_ = Verify(ctx, opts)
			} else {
				env := Envelope{To: sc.Envelope.To}
				if from, ok := sc.Envelope.From.(string); ok {
					env.From = from
				}
				_, _ = Send(ctx, opts, env, sc.Message.bytes())
			}
			encrypted := sc.Server.TLS || sc.Options.Secure
			for _, event := range s.wait(t, 20*time.Second) {
				switch {
				// A handshake record: a server that does not speak TLS
				// takes the handshake for lines
				case event.E == "tls", event.E == "line" && strings.HasPrefix(event.S, "\x16\x03"):
					encrypted = true
				case event.E == "line" && !encrypted && !strings.HasPrefix(event.S, "EHLO ") && event.S != "STARTTLS\r\n":
					t.Fatalf("the client sent %q before TLS", event.S)
				}
			}
		})
	}
	if played < 500 {
		t.Fatalf("only %d sessions with credentials", played)
	}
}

// The two recorded sessions where nodemailer gave the password away: a
// server that does not offer STARTTLS is asked for it anyway, and one that
// refuses EHLO gets no HELO.
func TestRequireTLSOnTheRecordedDowngrades(t *testing.T) {
	roots := trustedRoots(t)
	all := loadSessions(t)
	byName := map[string]*scenario{}
	for i := range all {
		byName[all[i].Name] = &all[i]
	}
	for name, want := range map[string]struct {
		lines []string
		err   *recordedError
	}{
		"starttls/none-offered/verify": {
			lines: []string{"EHLO client.example.test\r\n", "STARTTLS\r\n", "tls", "EHLO client.example.test\r\n", "AUTH PLAIN AGNvbnNvbGVAZXhhbXBsZS50ZXN0AGNvcnJlY3QgaG9yc2UgYmF0dGVyeQ==\r\n", "QUIT\r\n"},
		},
		"ehlo/refused-500/helo-ok/send-creds": {
			lines: []string{"EHLO client.example.test\r\n"},
			err: &recordedError{
				// nodemailer adds the reply to the text a second time
				Message: "EHLO failed but HELO does not support required STARTTLS. response=500 5.5.1 Command unrecognized: 500 5.5.1 Command unrecognized",
				Code:    "ECONNECTION", Response: "500 5.5.1 Command unrecognized", ResponseCode: 500, Command: "EHLO",
			},
		},
	} {
		sc := byName[name]
		if sc == nil {
			t.Fatalf("no recorded session %s", name)
		}
		s := startSink(t, sc.Server)
		opts := scenarioOptions(t, sc, s, roots)
		opts.RequireTLS = true
		var err error
		if sc.Call == "verify" {
			err = Verify(context.Background(), opts)
		} else {
			_, err = Send(context.Background(), opts, Envelope{From: "sender@example.test", To: sc.Envelope.To}, sc.Message.bytes())
		}
		var got []string
		for _, event := range s.wait(t, 20*time.Second) {
			switch event.E {
			case "line":
				got = append(got, event.S)
			case "tls":
				got = append(got, "tls")
			}
		}
		if !reflect.DeepEqual(got, want.lines) {
			t.Errorf("%s: the client sent %q, want %q", name, got, want.lines)
		}
		var gotErr *recordedError
		if e, ok := err.(*Error); ok {
			gotErr = asRecordedError(e)
		} else if err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(gotErr, want.err) {
			t.Errorf("%s: error %s, want %s", name, jsonText(gotErr), jsonText(want.err))
		}
	}
	// Without credentials nothing changes: the session goes on in plain
	// text, HELO included
	sc := byName["ehlo/refused-500/helo-ok/send-creds"]
	s := startSink(t, sc.Server)
	opts := scenarioOptions(t, sc, s, roots)
	opts.RequireTLS, opts.User, opts.Pass = true, "", ""
	if _, err := Send(context.Background(), opts, Envelope{From: "sender@example.test", To: sc.Envelope.To}, sc.Message.bytes()); err != nil {
		t.Errorf("without credentials: %v", err)
	}
}
