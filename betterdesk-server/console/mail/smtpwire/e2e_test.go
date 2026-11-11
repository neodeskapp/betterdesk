package smtpwire_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/mail"
	"github.com/unitronix/betterdesk-server/console/mail/smtpwire"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The sessions of testdata/e2e_sessions.json were made by calling the Node
// console's real services/emailService.js, with real nodemailer, against
// the scripted server. TestEndToEnd makes the same calls on the Go mail
// service with its real transport (the message builder and the SMTP
// client) against the same scripted server, and compares:
//
//   - what the function returned, error texts included,
//   - every command line the client sent, in order,
//   - where TLS started and with which server name,
//   - the message after DATA, byte for byte. The three things nodemailer
//     made up for a message (the Date, the MIME boundary, the Message-ID)
//     are read from the recording and given to the Go side as its clock
//     and its random bytes, so nothing is masked,
//   - when the client closed,
//   - the lines the service logged.
//
// Not compared: timing, and the TLS version and cipher. No mail leaves:
// every connection goes to the scripted server on loopback.

type e2eFile struct {
	PanelBaseURL string       `json:"panelBaseUrl"`
	Sessions     []e2eSession `json:"sessions"`
}

type e2eSession struct {
	Name     string          `json:"name"`
	Hostname string          `json:"hostname"`
	Config   json.RawMessage `json:"config"`
	Port     int             `json:"port"`
	Call     struct {
		Fn   string            `json:"fn"`
		Args []json.RawMessage `json:"args"`
	} `json:"call"`
	Result json.RawMessage `json:"result"`
	Logs   []struct {
		Level string `json:"level"`
		Text  string `json:"text"`
	} `json:"logs"`
	Server *smtpwire.SinkScript `json:"server"`
	Client []smtpwire.SinkEvent `json:"client"`
}

// logLines collects what the service logs, as "level text".
type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) add(level string, args []any) {
	l.mu.Lock()
	l.lines = append(l.lines, level+" [Email] "+fmt.Sprint(args...))
	l.mu.Unlock()
}
func (l *logLines) Info(args ...any)  { l.add("log", args) }
func (l *logLines) Warn(args ...any)  { l.add("warn", args) }
func (l *logLines) Error(args ...any) { l.add("error", args) }

// jsValues reads JSON values as a route would get them in a request body.
func jsValues(t *testing.T, members map[string]json.RawMessage) middleware.JSONObject {
	t.Helper()
	raw, err := json.Marshal(members)
	if err != nil {
		t.Fatal(err)
	}
	var body middleware.JSONObject
	steps := middleware.Bodies(middleware.BodyOptions{})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		body = middleware.Body(r)
	}))
	req := httptest.NewRequest("POST", "/", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	steps.ServeHTTP(httptest.NewRecorder(), req)
	return body
}

// recipients reads sendMail's `to`: a text, a list of texts, or nothing.
func recipients(t *testing.T, raw json.RawMessage) (to []string, list bool) {
	t.Helper()
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}, false
	}
	if err := json.Unmarshal(raw, &to); err != nil {
		t.Fatalf("recipients %s: %v", raw, err)
	}
	return to, true
}

var (
	boundaryOf  = regexp.MustCompile(`boundary="--_NmP-([0-9a-f]{16})-Part_1"`)
	messageIDOf = regexp.MustCompile(`\r\nMessage-ID: <([0-9a-f]{8})-([0-9a-f]{4})-([0-9a-f]{4})-([0-9a-f]{4})-([0-9a-f]{12})@`)
	dateOf      = regexp.MustCompile(`\r\nDate: ([^\r]+)\r\n`)
)

// madeUp reads from a recorded message what nodemailer made up for it,
// as the clock and the random bytes that make the Go builder write the
// same: 8 bytes for the boundary, 8 more for each of two parts (their
// boundaries are never written), then the 16 of the Message-ID.
func madeUp(t *testing.T, recorded []byte) (now func() time.Time, random *bytes.Reader) {
	t.Helper()
	var stream []byte
	if m := boundaryOf.FindSubmatch(recorded); m != nil {
		boundary, _ := hex.DecodeString(string(m[1]))
		stream = append(append(stream, boundary...), make([]byte, 16)...)
	} else {
		stream = append(stream, make([]byte, 8)...)
	}
	id := messageIDOf.FindSubmatch(recorded)
	date := dateOf.FindSubmatch(recorded)
	if id == nil || date == nil {
		t.Fatalf("the recorded message has no Message-ID or Date:\n%s", recorded)
	}
	for _, part := range id[1:] {
		raw, _ := hex.DecodeString(string(part))
		stream = append(stream, raw...)
	}
	at, err := time.Parse("Mon, 02 Jan 2006 15:04:05 -0700", string(date[1]))
	if err != nil {
		t.Fatalf("the recorded Date: %v", err)
	}
	return func() time.Time { return at }, bytes.NewReader(stream)
}

func TestEndToEnd(t *testing.T) {
	raw, err := os.ReadFile("testdata/e2e_sessions.json")
	if err != nil {
		t.Fatal(err)
	}
	var file e2eFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Sessions) < 60 {
		t.Fatalf("only %d sessions in testdata/e2e_sessions.json", len(file.Sessions))
	}
	roots := smtpwire.TrustedRoots(t)
	for i := range file.Sessions {
		sess := &file.Sessions[i]
		t.Run(sess.Name, func(t *testing.T) { replayEndToEnd(t, sess, file.PanelBaseURL, roots) })
	}
}

func replayEndToEnd(t *testing.T, sess *e2eSession, panelBaseURL string, roots *x509.CertPool) {
	// The scripted server, or a port of loopback where nothing listens
	var sink *smtpwire.Sink
	var port string
	if sess.Server != nil {
		sink = smtpwire.StartSink(t, *sess.Server)
		_, port, _ = net.SplitHostPort(sink.Addr())
	} else {
		closed, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		_, port, _ = net.SplitHostPort(closed.Addr().String())
		closed.Close()
	}
	recordedPort := strconv.Itoa(sess.Port)
	samePort := func(text, from string) string { return strings.ReplaceAll(text, from, "{port}") }

	// The stored settings, with the server's port put where the recorded
	// one was
	db := storetest.Migrated(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if string(sess.Config) != "null" {
		stored := string(sess.Config)
		var text string
		if json.Unmarshal(sess.Config, &text) == nil {
			stored = text
		}
		stored = strings.ReplaceAll(stored, recordedPort, port)
		if _, err := db.Exec(ctx, "INSERT INTO settings (key, value) VALUES (?, ?)", "smtp_config", stored); err != nil {
			t.Fatal(err)
		}
	}

	// What nodemailer made up for the recorded message
	var recordedData []byte
	for _, event := range sess.Client {
		if event.E == "data" && event.B64 != nil {
			recordedData, _ = base64.StdEncoding.DecodeString(*event.B64)
		}
	}
	transport := &mail.SMTP{Wire: smtpwire.Options{
		RootCAs:           roots,
		Hostname:          func() (string, error) { return sess.Hostname, nil },
		Families:          func() (bool, bool) { return true, false },
		ConnectionTimeout: 30 * time.Second,
		GreetingTimeout:   30 * time.Second,
		SocketTimeout:     30 * time.Second,
	}}
	if recordedData != nil {
		now, random := madeUp(t, recordedData)
		transport.Now, transport.Rand = now, random
	}
	logged := &logLines{}
	svc := mail.New(mail.Options{
		DB: db, Transport: transport, Host: "0.0.0.0", Port: 5000, HTTPRedirect: true, Logger: logged,
	})
	if got := svc.GetPanelBaseURL(); got != panelBaseURL {
		t.Errorf("the console's address in mails is %q, want %q", got, panelBaseURL)
	}

	// The call
	arg := func(i int) map[string]json.RawMessage {
		t.Helper()
		var members map[string]json.RawMessage
		if i >= len(sess.Call.Args) || json.Unmarshal(sess.Call.Args[i], &members) != nil {
			t.Fatalf("argument %d of %s is no object", i, sess.Call.Fn)
		}
		return members
	}
	text := func(raw json.RawMessage) *string {
		if len(raw) == 0 {
			return nil
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatalf("a text argument is %s", raw)
		}
		return &s
	}
	var result any
	var sent bool
	var threw error
	switch sess.Call.Fn {
	case "sendEmail":
		m := arg(0)
		email := mail.Email{Text: text(m["text"]), HTML: text(m["html"])}
		email.To, email.ToList = recipients(t, m["to"])
		if subject := text(m["subject"]); subject != nil {
			email.Subject = *subject
		}
		sent, threw = svc.SendEmail(ctx, email)
		result = sent
	case "sendAlertEmail":
		alert := jsbind.Of(jsValues(t, map[string]json.RawMessage{"v": sess.Call.Args[0]}).Get("v"))
		rule := jsbind.Of(jsValues(t, map[string]json.RawMessage{"v": sess.Call.Args[1]}).Get("v"))
		sent, threw = svc.SendAlertEmail(ctx, mail.Alert{
			Severity: alert.Member("severity"), DeviceID: alert.Member("device_id"),
			Message: alert.Member("message"), TriggeredAt: alert.Member("triggered_at"),
		}, mail.AlertRule{
			Name: rule.Member("name"), Description: rule.Member("description"),
			ConditionType: rule.Member("condition_type"), ConditionOp: rule.Member("condition_op"),
			ConditionValue: rule.Member("condition_value"), NotifyEmails: rule.Member("notify_emails"),
		})
		result = sent
	case "sendHelpRequestEmail":
		m := arg(0)
		values := jsbind.Of(jsValues(t, map[string]json.RawMessage{"v": sess.Call.Args[0]}).Get("v"))
		help := values.Member("helpRequest")
		email := mail.HelpRequestEmail{
			HelpRequest: mail.HelpRequest{Hostname: help.Member("hostname"), DeviceID: help.Member("device_id"), Message: help.Member("message")},
			FolderName:  values.Member("folderName"),
		}
		email.To, email.ToList = recipients(t, m["to"])
		sent, threw = svc.SendHelpRequestEmail(ctx, email)
		result = sent
	case "testConnection":
		result, threw = svc.TestConnection(ctx)
	default:
		t.Fatalf("no such call: %s", sess.Call.Fn)
	}
	if threw != nil {
		t.Fatalf("%s failed where the Node function returned: %v", sess.Call.Fn, threw)
	}
	got, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	if err := json.Compact(&want, sess.Result); err != nil {
		t.Fatal(err)
	}
	if samePort(string(got), port) != samePort(want.String(), recordedPort) {
		t.Errorf("%s returned %s, want %s", sess.Call.Fn, got, want.String())
	}

	// The log lines
	var wantLogs []string
	for _, line := range sess.Logs {
		wantLogs = append(wantLogs, line.Level+" "+line.Text)
	}
	gotLogs := make([]string, len(logged.lines))
	for i, line := range logged.lines {
		gotLogs[i] = samePort(line, port)
	}
	if strings.Join(gotLogs, "\n") != strings.Join(wantLogs, "\n") {
		t.Errorf("logged:\n  %s\nwant:\n  %s", strings.Join(gotLogs, "\n  "), strings.Join(wantLogs, "\n  "))
	}

	// The client's side of the session
	var events []smtpwire.SinkEvent
	if sink != nil {
		events = sink.Wait(t, 20*time.Second)
	}
	if len(events) != len(sess.Client) {
		t.Fatalf("the client's side has %d events, want %d\n got: %s\nwant: %s", len(events), len(sess.Client), eventsText(events), eventsText(sess.Client))
	}
	for i, wantEvent := range sess.Client {
		gotEvent := events[i]
		same := gotEvent.E == wantEvent.E && gotEvent.S == wantEvent.S
		switch {
		case !same:
		case wantEvent.E == "tls":
			same = gotEvent.SNI != nil && wantEvent.SNI != nil && *gotEvent.SNI == *wantEvent.SNI || gotEvent.SNI == nil && wantEvent.SNI == nil
		case wantEvent.E == "data" && gotEvent.B64 != nil:
			gotData, _ := base64.StdEncoding.DecodeString(*gotEvent.B64)
			same = bytes.Equal(gotData, recordedData)
			if !same {
				t.Errorf("the message differs\n got:\n%s\nwant:\n%s", gotData, recordedData)
			}
		case wantEvent.E == "data":
			// The scripted server keeps a long message as its length and hash
			sum := sha256.Sum256(recordedData)
			same = gotEvent.Len == len(recordedData) && gotEvent.SHA256 == hex.EncodeToString(sum[:])
		}
		if !same {
			t.Errorf("event %d of the client's side differs\n got: %s\nwant: %s", i, eventsText(events), eventsText(sess.Client))
			break
		}
	}
}

func eventsText(events []smtpwire.SinkEvent) string {
	var out strings.Builder
	for _, event := range events {
		raw, _ := json.Marshal(event)
		if len(raw) > 300 {
			raw = append(raw[:300], "..."...)
		}
		out.WriteString("\n    ")
		out.Write(raw)
	}
	return out.String()
}
