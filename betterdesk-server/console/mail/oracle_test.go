package mail

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The recordings in testdata were made by testdata/nodeoracle/mail.js: the
// Node console's emailService and smtpSettingsHandlers on the real database
// adapter, with nodemailer replaced by a stub, and the real nodemailer
// asked what it would do with each set of options.

type oracleProbe struct {
	Crash   string `json:"crash"`
	Error   string `json:"error"`
	Connect *struct {
		TLS                bool   `json:"tls"`
		Host               string `json:"host"`
		Port               string `json:"port"`
		Threw              string `json:"threw"`
		RejectUnauthorized string `json:"rejectUnauthorized"`
	} `json:"connect"`
	Derived struct {
		Host   string `json:"host"`
		Port   string `json:"port"`
		Secure bool   `json:"secure"`
		Threw  string `json:"threw"`
	} `json:"derived"`
	Auth *struct {
		User string `json:"user"`
		Pass string `json:"pass"`
	} `json:"auth"`
}

type oracleTransport struct {
	Options string      `json:"options"`
	Probe   oracleProbe `json:"probe"`
}

type oracleText struct {
	Text   *string `json:"text"`
	Throws *string `json:"throws"`
}

type oracleStep struct {
	Op string `json:"op"`

	// state, env
	Stored *string           `json:"stored"`
	Env    map[string]string `json:"env"`
	Cfg    struct {
		SMTPTLSVerify bool   `json:"smtpTlsVerify"`
		Host          string `json:"host"`
		Port          int    `json:"port"`
		HTTPRedirect  bool   `json:"httpRedirect"`
	} `json:"cfg"`

	// what the mail server does
	Verify    *string `json:"verify"`
	SendError *string `json:"sendError"`

	// inputs
	Mail    map[string]string `json:"mail"`
	Alert   map[string]string `json:"alert"`
	Rule    map[string]string `json:"rule"`
	Help    map[string]string `json:"help"`
	To      string            `json:"to"`
	Folder  string            `json:"folder"`
	Request *string           `json:"request"`
	Args    []string          `json:"args"`

	// what Node answered
	Config      string          `json:"config"`
	AlertEmail  oracleText      `json:"alertEmail"`
	Status      int             `json:"status"`
	Body        string          `json:"body"`
	Ret         json.RawMessage `json:"ret"`
	Throws      *string         `json:"throws"`
	URL         string          `json:"url"`
	StoredAfter *string         `json:"storedAfter"`
	Audit       [][]string      `json:"audit"`
	Logs        [][2]string     `json:"logs"`
	Created     []oracleTransport
	Used        *oracleTransport `json:"used"`
	Verified    int              `json:"verified"`
	Sent        []struct {
		From     string     `json:"from"`
		FromText oracleText `json:"fromText"`
		To       string     `json:"to"`
		Subject  string     `json:"subject"`
		Text     string     `json:"text"`
		HTML     string     `json:"html"`
	} `json:"sent"`
	Rows   [][]string `json:"rows"`
	SentAt []string   `json:"sentAt"`
}

func readGzip(t testing.TB, name string, into any) {
	t.Helper()
	file, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	unzipped, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(unzipped).Decode(into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

// bodyOf is req.body for a JSON request with the given text.
func bodyOf(raw string) middleware.JSONObject {
	var body middleware.JSONObject
	steps := middleware.Bodies(middleware.BodyOptions{})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		body = middleware.Body(r)
	}))
	req := httptest.NewRequest("POST", "/", strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	steps.ServeHTTP(httptest.NewRecorder(), req)
	return body
}

// bindValue is a step's input value: JSON text, or one of the words JSON
// lacks.
func bindValue(t testing.TB, text string) jsbind.Value {
	t.Helper()
	switch text {
	case "", "undefined":
		return jsbind.Undefined
	case "NaN":
		return jsbind.Number(math.NaN())
	}
	body := bodyOf(`{"v":` + text + `}`)
	if !body.Has("v") {
		t.Fatalf("input %q is no JSON", text)
	}
	return jsbind.Of(body.Get("v"))
}

// dumpValue writes a value as dump() of the oracle's lib.js does.
func dumpValue(v value) string {
	switch v.kind {
	case kindUndefined:
		return "undefined"
	case kindNumber:
		switch {
		case math.IsNaN(v.num):
			return "NaN"
		case v.num == 0 && math.Signbit(v.num):
			return "-0"
		}
		return jsbind.NumberString(v.num)
	case kindArray:
		parts := make([]string, len(v.items))
		for i, item := range v.items {
			parts[i] = dumpValue(item)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case kindObject:
		var parts []string
		for _, key := range v.orderedKeys() {
			parts = append(parts, string(appendQuoted(nil, key))+":"+dumpValue(v.members[key]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	return v.json()
}

// dumped reads a dump of the oracle back. ok is false for the dumps that
// are no JSON (undefined, NaN and -0 inside a list or an object).
func dumped(text string) (value, bool) {
	switch text {
	case "undefined":
		return undefined, true
	case "NaN":
		return numberValue(math.NaN()), true
	case "-0":
		return numberValue(math.Copysign(0, -1)), true
	case "Infinity":
		return numberValue(math.Inf(1)), true
	case "-Infinity":
		return numberValue(math.Inf(-1)), true
	}
	v, err := parseJSON(text)
	return v, err == nil
}

type sendCall struct {
	settings Settings
	mail     Outgoing
}

type fakeTransport struct {
	sendErr, verifyErr error
	sends              []sendCall
	verifies           []Settings
}

func (f *fakeTransport) Send(_ context.Context, settings Settings, mail Outgoing) error {
	f.sends = append(f.sends, sendCall{settings, mail})
	return f.sendErr
}

func (f *fakeTransport) Verify(_ context.Context, settings Settings) error {
	f.verifies = append(f.verifies, settings)
	return f.verifyErr
}

type logLines struct{ lines [][2]string }

func (l *logLines) add(level string, args []any) {
	parts := make([]string, len(args))
	for i, arg := range args {
		parts[i], _ = arg.(string)
	}
	l.lines = append(l.lines, [2]string{level, strings.Join(parts, " ")})
}
func (l *logLines) Info(args ...any)  { l.add("info", args) }
func (l *logLines) Warn(args ...any)  { l.add("warn", args) }
func (l *logLines) Error(args ...any) { l.add("error", args) }

// replay holds what a recording's steps work on.
type replay struct {
	t         *testing.T
	db        *store.DB
	env       map[string]string
	service   *Service
	transport *fakeTransport
	log       *logLines
	counts    map[string]int
	// refusedSave is set after a save the Go console refused and Node
	// stored, until the next state
	refusedSave bool
}

func (r *replay) setState(step oracleStep) {
	ctx := context.Background()
	if step.Stored == nil {
		if _, err := r.db.Exec(ctx, "DELETE FROM settings WHERE key = ?", settingKey); err != nil {
			r.t.Fatal(err)
		}
	} else if err := setSetting(ctx, r.db, settingKey, *step.Stored); err != nil {
		r.t.Fatal(err)
	}
	r.env = step.Env
	r.transport = &fakeTransport{}
	r.log = &logLines{}
	r.service = New(Options{
		DB:           r.db,
		Transport:    r.transport,
		Getenv:       func(name string) string { return r.env[name] },
		TLSVerify:    step.Cfg.SMTPTLSVerify,
		Host:         step.Cfg.Host,
		Port:         step.Cfg.Port,
		HTTPRedirect: step.Cfg.HTTPRedirect,
		Logger:       r.log,
	})
}

var tlsOption = regexp.MustCompile(`"tls":\{"rejectUnauthorized":(true|false)\}\}$`)

// refusal is the error nodemailer failed with before it had a connection,
// or "" when it connected.
func (p oracleProbe) refusal() (string, bool) {
	switch {
	case p.Crash != "":
		return p.Crash, true
	case p.Connect == nil || p.Connect.Threw != "":
		return p.Error, true
	}
	return "", false
}

// storedTLSVerifyIsNoFlag reports whether the configuration in use has a
// tlsVerify that is neither true nor false. Node read such a value by its
// truth; the Go console takes the default for it (BUGS EM1), and leaves it
// out of the settings answer.
func (r *replay) storedTLSVerifyIsNoFlag() bool {
	cfg := r.service.LoadSMTPConfig(context.Background())
	if cfg == nil {
		return false
	}
	v := cfg.get("tlsVerify")
	if v.isUndefined() || v.kind == kindBool {
		return false
	}
	r.counts["EM1: a stored tlsVerify that is no flag"]++
	return true
}

// invalidConfig reports whether the configuration in use has a host that
// is no text or a port that is set and no number, or text of a number,
// that is whole and from 1 to 65535 (BUGS EM4).
func (r *replay) invalidConfig() bool {
	cfg := r.service.LoadSMTPConfig(context.Background())
	if cfg == nil {
		return false
	}
	port := cfg.get("port")
	n, err := port.toNumber()
	goodPort := (port.kind == kindNumber || port.kind == kindString) && err == nil && n == math.Trunc(n) && n >= 1 && n <= 65535
	return cfg.get("host").kind != kindString || port.truthy() && !goodPort
}

// refusedForCause reports whether a save's request has the fault its
// refusal names (BUGS EM5): read with encoding/json, a member of another
// type than the save takes.
func refusedForCause(request, message string) bool {
	var members map[string]json.RawMessage
	if err := json.Unmarshal([]byte(request), &members); err != nil {
		return message == "SMTP host is required"
	}
	text := func(name string) (string, bool) {
		var s string
		err := json.Unmarshal(members[name], &s)
		return s, err == nil && len(members[name]) > 0 && members[name][0] == '"'
	}
	missing := func(name string) bool {
		raw, ok := members[name]
		return !ok || string(raw) == "null"
	}
	number := func(name string) (float64, bool) {
		raw := string(members[name])
		if raw == "" || !strings.ContainsAny(raw[:1], "-0123456789") {
			return 0, false
		}
		n, err := strconv.ParseFloat(raw, 64)
		return n, err == nil
	}
	switch message {
	case "SMTP host is required":
		host, isText := text("host")
		n, isNumber := number("host")
		raw := string(members["host"])
		return missing("host") || isText && jsbind.Trim(host) == "" || isNumber && n == 0 || raw == "false"
	case "Invalid host":
		_, isText := text("host")
		n, isNumber := number("host")
		raw := string(members["host"])
		return !missing("host") && !isText && !(isNumber && n == 0) && raw != "false"
	case "Invalid port":
		if missing("port") {
			return false
		}
		n, isNumber := number("port")
		if digits, isText := text("port"); isText {
			isNumber = digits != "" && strings.Trim(digits, "0123456789") == ""
			n, _ = strconv.ParseFloat(digits, 64)
		}
		return !isNumber || n != math.Trunc(n) || n < 1 || n > 65535
	case "Invalid secure", "Invalid tlsVerify":
		raw, ok := members[strings.TrimPrefix(message, "Invalid ")]
		return ok && string(raw) != "true" && string(raw) != "false"
	case "Invalid user", "Invalid pass", "Invalid from":
		name := strings.TrimPrefix(message, "Invalid ")
		_, isText := text(name)
		return !missing(name) && !isText
	case "Invalid alert_email":
		emails, isText := text("alert_email")
		return !missing("alert_email") && (!isText || !PlainAddressList(jsbind.Trim(emails)))
	}
	return false
}

// tlsVerifyMember is the member a settings answer ends with when the
// stored configuration has a tlsVerify.
var tlsVerifyMember = regexp.MustCompile(`,"tlsVerify":(true|false)\}$`)

// wantSettings is what the recording says nodemailer connected with.
func (r *replay) wantSettings(used oracleTransport) (Settings, bool) {
	var want Settings
	host, ok := dumped(used.Probe.Derived.Host)
	if !ok || host.kind != kindString {
		r.t.Fatalf("nodemailer connected with the host %s", used.Probe.Derived.Host)
	}
	want.Host = wellFormed(host.str)
	port, err := strconv.Atoi(used.Probe.Derived.Port)
	if err != nil {
		r.t.Fatalf("nodemailer connected with the port %s", used.Probe.Derived.Port)
	}
	if used.Probe.Connect.Port != used.Probe.Derived.Port {
		r.t.Fatalf("port %s at the socket, %s in the connection", used.Probe.Connect.Port, used.Probe.Derived.Port)
	}
	want.Port = port
	want.Secure = used.Probe.Derived.Secure
	if used.Probe.Connect.TLS != want.Secure {
		r.t.Fatalf("secure %v but TLS %v", want.Secure, used.Probe.Connect.TLS)
	}
	m := tlsOption.FindStringSubmatch(used.Options)
	if m == nil {
		r.t.Fatalf("no tls option in %s", used.Options)
	}
	want.TLSVerify = m[1] == "true"
	if want.Secure && used.Probe.Connect.RejectUnauthorized != m[1] {
		r.t.Fatalf("rejectUnauthorized %s at the socket, %s in the options", used.Probe.Connect.RejectUnauthorized, m[1])
	}
	if r.storedTLSVerifyIsNoFlag() {
		want.TLSVerify = r.service.opts.TLSVerify
	}
	if auth := used.Probe.Auth; auth != nil {
		user, okUser := dumped(auth.User)
		pass, okPass := dumped(auth.Pass)
		if !okUser || !okPass {
			r.t.Fatalf("credentials %s %s", auth.User, auth.Pass)
		}
		userText, errUser := user.toString()
		passText, errPass := pass.toString()
		if errUser != nil || errPass != nil {
			// nodemailer would have failed at the AUTH command, which no
			// recording reaches. The service refuses up front.
			r.counts["credentials that are no text"]++
			return want, false
		}
		want.User, want.Pass = wellFormed(userText), wellFormed(passText)
	}
	return want, true
}

// nodeLogs are the lines Node printed for a step, without the "[Email] "
// prefix, and the line it printed under "[Settings]".
func nodeLogs(t *testing.T, step oracleStep) (email [][2]string, settings string) {
	for _, line := range step.Logs {
		switch {
		case strings.HasPrefix(line[1], "[Email] "):
			email = append(email, [2]string{line[0], strings.TrimPrefix(line[1], "[Email] ")})
		case strings.HasPrefix(line[1], "[Settings] ") && line[0] == "error":
			settings = strings.TrimPrefix(line[1], "[Settings] ")
		default:
			t.Fatalf("unexpected log line %q", line)
		}
	}
	return email, settings
}

func sameLogs(a, b [][2]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func optional(dump string) (*string, bool) {
	if dump == "undefined" {
		return nil, true
	}
	var s string
	if err := json.Unmarshal([]byte(dump), &s); err != nil {
		return nil, false
	}
	return &s, true
}

func samePointer(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// toOf reads the recipients of a step's input.
func toOf(t testing.TB, text string) (to []string, list bool) {
	t.Helper()
	if text == "" || text == "undefined" {
		return nil, false
	}
	if strings.HasPrefix(text, "[") {
		if err := json.Unmarshal([]byte(text), &to); err != nil {
			t.Fatalf("recipients %s: %v", text, err)
		}
		return to, true
	}
	var one string
	if err := json.Unmarshal([]byte(text), &one); err != nil {
		t.Fatalf("recipients %s: %v", text, err)
	}
	return []string{one}, false
}

// checkSend compares a step that sends a mail: the answer, the log, and
// what the transport was given.
func (r *replay) checkSend(i int, step oracleStep, got bool, err error) {
	t := r.t
	wantLogs, _ := nodeLogs(t, step)
	if err == nil && (step.Used != nil || step.Throws != nil) && r.invalidConfig() {
		// BUGS EM4: Node failed with the name lookup's exception, its
		// socket's error or the TypeError of its log line; the Go console
		// connects nothing and logs the configuration as invalid
		r.counts["EM4: a stored configuration that is invalid"]++
		last := [2]string{}
		if len(r.log.lines) > 0 {
			last = r.log.lines[len(r.log.lines)-1]
		}
		if got || len(r.transport.sends) != 0 || last != [2]string{"error", "Send failed: Invalid SMTP configuration"} {
			t.Fatalf("step %d %s: got %v, %d sends, logs %q", i, step.Op, got, len(r.transport.sends), r.log.lines)
		}
		return
	}
	if step.Throws != nil {
		if err == nil || err.Error() != *step.Throws {
			t.Fatalf("step %d %s: error %v, Node threw %q", i, step.Op, err, *step.Throws)
		}
		if len(r.transport.sends) != 0 || !sameLogs(r.log.lines, wantLogs) {
			t.Fatalf("step %d %s: sent %d, logs %q, Node logged %q", i, step.Op, len(r.transport.sends), r.log.lines, wantLogs)
		}
		return
	}
	if err != nil {
		t.Fatalf("step %d %s: error %v, Node answered %s", i, step.Op, err, step.Ret)
	}
	want := string(step.Ret) == `"true"`

	if step.Used == nil {
		// Node did not get as far as a transporter
		if got != want || len(r.transport.sends) != 0 || !sameLogs(r.log.lines, wantLogs) {
			t.Fatalf("step %d %s: got %v, sent %d, logs %q\nNode: %s, logs %q", i, step.Op, got, len(r.transport.sends), r.log.lines, step.Ret, wantLogs)
		}
		return
	}
	sent := step.Sent[0]
	// The outcome line is the last one; Go's differs where the real
	// nodemailer would not have sent what the stub took.
	failed := func(reason string, message *string) {
		t.Helper()
		r.counts[reason]++
		if got || len(r.transport.sends) != 0 {
			t.Fatalf("step %d %s: got %v with %d sends, want a failure (%s)", i, step.Op, got, len(r.transport.sends), reason)
		}
		last := r.log.lines[len(r.log.lines)-1]
		if !sameLogs(r.log.lines[:len(r.log.lines)-1], wantLogs[:len(wantLogs)-1]) || last[0] != "error" || !strings.HasPrefix(last[1], "Send failed: ") {
			t.Fatalf("step %d %s: logs %q, Node's start with %q", i, step.Op, r.log.lines, wantLogs)
		}
		if message != nil && last[1] != "Send failed: "+*message {
			t.Fatalf("step %d %s: %q, want the failure %q", i, step.Op, last[1], *message)
		}
	}
	if message, refused := step.Used.Probe.refusal(); refused {
		// nodemailer failed this way only on configurations the Go
		// console takes as invalid (EM4)
		t.Fatalf("step %d %s: nodemailer failed before it connected (%s) on a valid configuration", i, step.Op, message)
	}
	wantSettings, ok := r.wantSettings(*step.Used)
	if !ok {
		failed("credentials that are no text", nil)
		return
	}
	from, okFrom := dumped(sent.From)
	if !okFrom {
		t.Fatalf("step %d: sender %s", i, sent.From)
	}
	wantFrom, fromErr := fromText(from)
	if fromErr != nil {
		message := fromErr.Error()
		failed("a sender that is no text", &message)
		return
	}
	if from.kind == kindString {
		wantFrom = *sent.FromText.Text
	} else {
		r.counts["a sender that is no text, sent as text"]++
	}
	wantTo, okTo := []string(nil), true
	wantList := false
	switch to, _ := dumped(sent.To); to.kind {
	case kindUndefined:
	case kindString:
		wantTo = []string{wellFormed(to.str)}
	case kindArray:
		wantList = true
		wantTo = []string{}
		for _, item := range to.items {
			if item.kind != kindString {
				okTo = false
			}
			wantTo = append(wantTo, wellFormed(item.str))
		}
	default:
		okTo = false
	}
	if !okTo {
		// notify_emails that is no text and no list of texts
		r.counts["recipients that are no text"]++
		if len(r.transport.sends) > 1 {
			t.Fatalf("step %d: %d sends", i, len(r.transport.sends))
		}
		return
	}

	if got != want || len(r.transport.sends) != 1 || !sameLogs(r.log.lines, wantLogs) {
		t.Fatalf("step %d %s: got %v, sent %d, logs %q\nNode: %s, logs %q", i, step.Op, got, len(r.transport.sends), r.log.lines, step.Ret, wantLogs)
	}
	call := r.transport.sends[0]
	if call.settings != wantSettings {
		t.Fatalf("step %d %s: settings %+v\nnodemailer: %+v (%s)", i, step.Op, call.settings, wantSettings, step.Used.Options)
	}
	subject, _ := optional(sent.Subject)
	text, okText := optional(sent.Text)
	html, okHTML := optional(sent.HTML)
	if !okText || !okHTML {
		t.Fatalf("step %d: bodies %s %s", i, sent.Text, sent.HTML)
	}
	wantSubject := ""
	if subject != nil {
		wantSubject = *subject
	}
	m := call.mail
	if m.From != wantFrom || !sameStrings(m.To, wantTo) || (m.To == nil) != (wantTo == nil) || m.ToList != wantList ||
		m.Subject != wantSubject || !samePointer(m.Text, text) || !samePointer(m.HTML, html) {
		t.Fatalf("step %d %s: message\n got %s\nwant from %q to %q (list %v) subject %q text %s html %s",
			i, step.Op, describe(m), wantFrom, wantTo, wantList, wantSubject, sent.Text, sent.HTML)
	}
}

func describe(m Outgoing) string {
	show := func(p *string) string {
		if p == nil {
			return "undefined"
		}
		return strconv.Quote(*p)
	}
	return "from " + strconv.Quote(m.From) + " to " + strings.Join(m.To, "|") + " (list " + strconv.FormatBool(m.ToList) + ") subject " +
		strconv.Quote(m.Subject) + " text " + show(m.Text) + " html " + show(m.HTML)
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func errorOf(message *string) error {
	if message == nil {
		return nil
	}
	return errors.New(*message)
}

func (r *replay) run(i int, step oracleStep) {
	t := r.t
	ctx := context.Background()
	if r.service != nil {
		r.transport.sends, r.transport.verifies = nil, nil
		r.transport.sendErr, r.transport.verifyErr = errorOf(step.SendError), errorOf(step.Verify)
		r.log.lines = nil
	}
	r.counts[step.Op]++
	if r.refusedSave && step.Op != "state" {
		// BUGS EM5: Node stored the save the step before refused, so it
		// read another configuration than the Go console
		if step.Op != "load" && step.Op != "get" {
			t.Fatalf("step %d %s after a save Node took and the Go console refused", i, step.Op)
		}
		r.counts["EM5: a read after a refused save"]++
		return
	}
	switch step.Op {
	case "state":
		r.refusedSave = false
		r.setState(step)
	case "env":
		r.env = step.Env
	case "reset":
		r.service.ResetTransporter()
	case "load":
		cfg := r.service.LoadSMTPConfig(ctx)
		got := "null"
		if cfg != nil {
			got = dumpValue(cfg.doc)
		}
		if got != step.Config {
			t.Fatalf("step %d load: %s\nNode: %s", i, got, step.Config)
		}
		email, err := r.service.GetAlertEmail(ctx)
		switch {
		case step.AlertEmail.Throws != nil:
			if err == nil || err.Error() != *step.AlertEmail.Throws {
				t.Fatalf("step %d alert email: error %v, Node threw %q", i, err, *step.AlertEmail.Throws)
			}
		case err != nil || email != *step.AlertEmail.Text:
			t.Fatalf("step %d alert email: %q %v, Node: %q", i, email, err, *step.AlertEmail.Text)
		}
	case "get":
		if r.storedTLSVerifyIsNoFlag() {
			step.Body = tlsVerifyMember.ReplaceAllString(step.Body, "}")
		}
		r.checkAnswer(i, step, r.service.GetSMTPSettings(ctx))
	case "test":
		answer := r.service.TestSMTPSettings(ctx)
		if r.invalidConfig() {
			// BUGS EM4, as in checkSend
			r.counts["EM4: a stored configuration that is invalid"]++
			if answer.Status != 200 || marshal(t, answer.Body) != `{"success":false,"error":"Invalid SMTP configuration"}` || len(r.transport.verifies) != 0 || len(r.log.lines) != 0 {
				t.Fatalf("step %d test: %d %s, %d connections, logs %q", i, answer.Status, marshal(t, answer.Body), len(r.transport.verifies), r.log.lines)
			}
			return
		}
		wantLogs, wantSettingsLog := nodeLogs(t, step)
		if !sameLogs(r.log.lines, wantLogs) || answer.LogError != wantSettingsLog {
			t.Fatalf("step %d test: logs %q and %q\nNode: %q", i, r.log.lines, answer.LogError, step.Logs)
		}
		wantBody := step.Body
		wantVerifies := step.Verified
		if step.Used != nil {
			if message, refused := step.Used.Probe.refusal(); refused {
				t.Fatalf("step %d test: nodemailer failed before it connected (%s) on a valid configuration", i, message)
			}
			if want, ok := r.wantSettings(*step.Used); !ok {
				wantBody, wantVerifies = marshal(t, failedTest(errNotPrimitive.Error())), 0
			} else if len(r.transport.verifies) != 1 || r.transport.verifies[0] != want {
				t.Fatalf("step %d test: verified with %+v\nnodemailer: %+v (%s)", i, r.transport.verifies, want, step.Used.Options)
			}
		}
		if answer.Status != step.Status || marshal(t, answer.Body) != wantBody || len(r.transport.verifies) != wantVerifies || answer.Audit != nil {
			t.Fatalf("step %d test: %d %s (%d connections)\nwant %d %s (%d)", i, answer.Status, marshal(t, answer.Body), len(r.transport.verifies), step.Status, wantBody, wantVerifies)
		}
	case "send":
		to, list := toOf(t, step.Mail["to"])
		subject, _ := optional(orUndefined(step.Mail["subject"]))
		text, _ := optional(orUndefined(step.Mail["text"]))
		html, _ := optional(orUndefined(step.Mail["html"]))
		m := Email{To: to, ToList: list, Text: text, HTML: html}
		if subject != nil {
			m.Subject = *subject
		}
		got, err := r.service.SendEmail(ctx, m)
		r.checkSend(i, step, got, err)
	case "alert":
		alert := Alert{
			Severity:    bindValue(t, step.Alert["severity"]),
			DeviceID:    bindValue(t, step.Alert["device_id"]),
			Message:     bindValue(t, step.Alert["message"]),
			TriggeredAt: bindValue(t, step.Alert["triggered_at"]),
		}
		rule := AlertRule{
			Name:           bindValue(t, step.Rule["name"]),
			Description:    bindValue(t, step.Rule["description"]),
			ConditionType:  bindValue(t, step.Rule["condition_type"]),
			ConditionOp:    bindValue(t, step.Rule["condition_op"]),
			ConditionValue: bindValue(t, step.Rule["condition_value"]),
			NotifyEmails:   bindValue(t, step.Rule["notify_emails"]),
		}
		if len(step.Sent) > 0 {
			html, _ := optional(step.Sent[0].HTML)
			if html != nil {
				if escaped := escapedAlertCells(alert, *html); escaped != *html {
					r.counts["EM3: an alert value with markup is escaped"]++
					step.Sent = append(step.Sent[:0:0], step.Sent...)
					step.Sent[0].HTML = marshal(t, escaped)
				}
			}
		}
		got, err := r.service.SendAlertEmail(ctx, alert, rule)
		r.checkSend(i, step, got, err)
	case "help":
		to, list := toOf(t, step.To)
		help := HelpRequest{
			Hostname: bindValue(t, step.Help["hostname"]),
			DeviceID: bindValue(t, step.Help["device_id"]),
			Message:  bindValue(t, step.Help["message"]),
		}
		if len(step.Sent) > 0 {
			step.Sent = append(step.Sent[:0:0], step.Sent...)
			if text, _ := optional(step.Sent[0].Text); text != nil {
				r.counts["EM7: a help request mail with its blank lines"]++
				spaced := spacedHelpText(t, help, *text)
				if r.noPublicURL() {
					// BUGS EM8: without a public address the mail has no link
					r.counts["EM8: a help request mail without a link"]++
					spaced, _, _ = strings.Cut(spaced, "\n\nOpen in panel: ")
				}
				step.Sent[0].Text = marshal(t, spaced)
			}
			if html, _ := optional(step.Sent[0].HTML); html != nil && r.noPublicURL() {
				step.Sent[0].HTML = marshal(t, helpLinkRow.ReplaceAllString(*html, ""))
			}
		}
		got, err := r.service.SendHelpRequestEmail(ctx, HelpRequestEmail{To: to, ToList: list, HelpRequest: help, FolderName: bindValue(t, step.Folder)})
		r.checkSend(i, step, got, err)
	case "baseurl":
		want := step.URL
		if r.noPublicURL() {
			// BUGS EM8: Node built the address from the listen address; the
			// Go console has none without a public URL or host
			r.counts["EM8: no address without a public URL"]++
			want = ""
		}
		if got := r.service.GetPanelBaseURL(); got != want {
			t.Fatalf("step %d: base URL %q, want %q, Node: %q (env %v)", i, got, want, step.URL, r.env)
		}
	case "put":
		body := middleware.JSONObject{}
		if step.Request != nil {
			body = bodyOf(*step.Request)
		}
		before, hadRow, err := getSetting(ctx, r.db, settingKey)
		if err != nil {
			t.Fatal(err)
		}
		answer := r.service.PutSMTPSettings(ctx, body)
		if refused := marshal(t, answer.Body); answer.Status == 400 && (step.Status != 400 || step.Body != refused) {
			// BUGS EM5: a member of the wrong type is refused, where Node
			// stored it or failed on it
			r.counts["EM5: a save with a member of the wrong type"]++
			var reason errorBody
			if err := json.Unmarshal([]byte(refused), &reason); err != nil || step.Request == nil || !refusedForCause(*step.Request, reason.Error) {
				t.Fatalf("step %d put %s: refused with %s", i, show(step.Request), refused)
			}
			after, hasRow, err := getSetting(ctx, r.db, settingKey)
			if err != nil || hasRow != hadRow || after != before || answer.Audit != nil || answer.LogError != "" || len(r.log.lines) != 0 {
				t.Fatalf("step %d put %s: a refused save changed the row to %q (%v), audit %+v, logs %q %q", i, *step.Request, after, err, answer.Audit, answer.LogError, r.log.lines)
			}
			r.refusedSave = step.StoredAfter != nil && (!hadRow || *step.StoredAfter != before)
			return
		}
		r.checkAnswer(i, step, answer)
		stored, ok, err := getSetting(ctx, r.db, settingKey)
		if err != nil || ok != (step.StoredAfter != nil) || (ok && stored != *step.StoredAfter) {
			t.Fatalf("step %d put %s: stored %q %v %v\nNode: %v", i, *step.Request, stored, ok, err, show(step.StoredAfter))
		}
		switch {
		case len(step.Audit) == 0:
			if answer.Audit != nil {
				t.Fatalf("step %d put: audit entry %+v, Node wrote none", i, answer.Audit)
			}
		default:
			action, _ := optional(step.Audit[0][1])
			details, _ := optional(step.Audit[0][2])
			if answer.Audit == nil || action == nil || details == nil || answer.Audit.Action != *action || answer.Audit.Details != *details {
				t.Fatalf("step %d put: audit entry %+v, Node: %q", i, answer.Audit, step.Audit[0])
			}
		}
	case "has":
		got, err := r.service.HasEmailNotificationSent(ctx, step.Args[0], step.Args[1], step.Args[2])
		if err != nil || strconv.FormatBool(got) != string(step.Ret) {
			t.Fatalf("step %d has %q: %v %v, Node: %s", i, step.Args, got, err, step.Ret)
		}
	case "logsent":
		if err := r.service.LogEmailNotificationSent(ctx, step.Args[0], step.Args[1], step.Args[2]); (err != nil) != (step.Throws != nil) {
			t.Fatalf("step %d log %q: %v, Node: %v", i, step.Args, err, show(step.Throws))
		}
	case "logcount":
		rows, err := store.Select(ctx, r.db, "SELECT event_type, event_id, recipient, sent_at FROM email_notification_log ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		n := 0
		for rows.Next() {
			var eventType, eventID, recipient string
			var sentAt store.Time
			if err := rows.Scan(&eventType, &eventID, &recipient, &sentAt); err != nil {
				t.Fatal(err)
			}
			if n >= len(step.Rows) || eventType != step.Rows[n][0] || eventID != step.Rows[n][1] || recipient != step.Rows[n][2] {
				t.Fatalf("log row %d: %q %q %q, Node: %q", n, eventType, eventID, recipient, step.Rows)
			}
			// SQLite: the text of datetime('now'). PostgreSQL: a time.
			form := `^"\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}"$`
			if r.db.IsPostgres() {
				form = `^"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z"$`
				if !strings.HasPrefix(step.SentAt[n], `{"$date":`) {
					t.Fatalf("Node read sent_at as %s", step.SentAt[n])
				}
			} else if !regexp.MustCompile(form).MatchString(step.SentAt[n]) {
				t.Fatalf("Node read sent_at as %s", step.SentAt[n])
			}
			if got := marshal(t, sentAt); !regexp.MustCompile(form).MatchString(got) {
				t.Fatalf("sent_at reads %s", got)
			}
			n++
		}
		if n != len(step.Rows) {
			t.Fatalf("%d log rows, Node: %d", n, len(step.Rows))
		}
	default:
		t.Fatalf("step %d: unknown op %q", i, step.Op)
	}
}

// escapedAlertCells is the HTML of an alert mail Node sent, with the
// severity, the device and the time escaped as the Go console prints them
// (BUGS EM3). Node printed those three as they were.
func escapedAlertCells(alert Alert, html string) string {
	text := func(v jsbind.Value) string {
		s, _ := fromBind(v).toString()
		return s
	}
	device := "N/A"
	if alert.DeviceID.Truthy() {
		device = text(alert.DeviceID)
	}
	row := `</td></tr>
                <tr><td style="padding: 8px; border-bottom: 1px solid #eee; font-weight: bold;">`
	for _, cell := range []struct{ value, after string }{
		{text(alert.Severity), row + "Device</td>"},
		{device, row + "Message</td>"},
		{text(alert.TriggeredAt), "</td></tr>\n            </table>"},
	} {
		html = strings.Replace(html, ">"+cell.value+cell.after, ">"+escapeHTML(cell.value)+cell.after, 1)
	}
	return html
}

// noPublicURL reports whether the environment of the replay names no
// public URL. The replay's service has no public host, so its mails have
// no link then (BUGS EM8).
func (r *replay) noPublicURL() bool {
	return r.env["PANEL_PUBLIC_URL"] == "" && r.env["PUBLIC_URL"] == ""
}

// helpLinkRow is the link of a help request mail's HTML.
var helpLinkRow = regexp.MustCompile(`\n            <p style="margin-top: 16px;"><a href="[^"]*">Open help requests in BetterDesk</a></p>`)

// spacedHelpText is the text of a help request mail Node sent, with the
// blank lines Node dropped (BUGS EM7): after the first line, and before
// the message and the link.
func spacedHelpText(t *testing.T, help HelpRequest, text string) string {
	t.Helper()
	message := ""
	if help.Message.Truthy() {
		message, _ = fromBind(help.Message).toString()
	}
	const first = "A new help request was submitted.\n"
	link := strings.LastIndex(text, "\nOpen in panel: ")
	head, tail := text[:link], text[link:]
	body, ok := strings.CutSuffix(head, "\nMessage: "+message)
	if !ok || !strings.HasPrefix(body, first) {
		t.Fatalf("help request text %q", text)
	}
	return first + "\n" + body[len(first):] + "\n\nMessage: " + message + "\n" + tail
}

func orUndefined(text string) string {
	if text == "" {
		return "undefined"
	}
	return text
}

func show(p *string) string {
	if p == nil {
		return "nothing"
	}
	return strconv.Quote(*p)
}

func marshal(t testing.TB, v any) string {
	t.Helper()
	out, err := httpx.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// checkAnswer compares a handler's answer with what Node sent and logged.
func (r *replay) checkAnswer(i int, step oracleStep, answer Answer) {
	r.t.Helper()
	wantLogs, wantSettingsLog := nodeLogs(r.t, step)
	if answer.Status != step.Status || marshal(r.t, answer.Body) != step.Body || answer.LogError != wantSettingsLog || !sameLogs(r.log.lines, wantLogs) {
		r.t.Fatalf("step %d %s %s: %d %s, log %q %q\nNode: %d %s, logs %q", i, step.Op, show(step.Request), answer.Status, marshal(r.t, answer.Body),
			answer.LogError, r.log.lines, step.Status, step.Body, step.Logs)
	}
}

// TestNodeRecording replays every step Node was recorded with, on the
// database the recording was made on.
func TestNodeRecording(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		var recording struct {
			Dialect string       `json:"dialect"`
			Steps   []oracleStep `json:"steps"`
		}
		readGzip(t, "node_"+db.Dialect().String()+".json.gz", &recording)
		if recording.Dialect != db.Dialect().String() || len(recording.Steps) < 15000 {
			t.Fatalf("recording of %s with %d steps", recording.Dialect, len(recording.Steps))
		}
		r := &replay{t: t, db: db, counts: map[string]int{}}
		for i, step := range recording.Steps {
			r.run(i, step)
		}
		// Every kind of step and every special case has to have been met
		for _, name := range []string{"state", "env", "reset", "load", "get", "test", "send", "alert", "help", "baseurl", "put", "has", "logsent", "logcount",
			"credentials that are no text", "a sender that is no text", "a sender that is no text, sent as text",
			"recipients that are no text", "EM1: a stored tlsVerify that is no flag", "EM3: an alert value with markup is escaped",
			"EM7: a help request mail with its blank lines", "EM4: a stored configuration that is invalid",
			"EM5: a save with a member of the wrong type", "EM5: a read after a refused save",
			"EM8: no address without a public URL", "EM8: a help request mail without a link"} {
			if r.counts[name] == 0 {
				t.Errorf("the recording has no %q", name)
			}
		}
		t.Logf("%d steps: %v", len(recording.Steps), r.counts)
	})
}
