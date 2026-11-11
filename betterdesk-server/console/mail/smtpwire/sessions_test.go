package smtpwire

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestSessions replays the sessions recorded with the real nodemailer
// (testdata/sessions.json, made by testdata/nodeoracle/record_sessions.js).
// For every scenario the Go sink plays the same server side, and the test
// compares
//   - everything the client sent: each command line in order, the bytes
//     after DATA (or their length and SHA-256 above 4 KiB), where TLS
//     started and with which server name (SNI), and that the client closed;
//   - the outcome: for an error its message, code, response, response code,
//     command and the refused recipients; for a sent message the accepted
//     and refused recipients with their errors, the response, the envelope,
//     the message size, the EHLO lines and the server's message id.
//
// Not compared: the times in the result, the TLS version and cipher, and
// what a scenario's "cmp" switches off: the error text where it comes from
// OpenSSL with a thread id in it (cmp.message), the client's bytes once a
// TLS handshake starts against a server that does not answer it
// (cmp.clientUntil, cmp.client), whether a refused certificate ends the
// connection inside or after the handshake (cmp.tlsEvents), the SNI name
// with a trailing dot (cmp.sni), and the data when the server answers
// before it has read it (cmp.data).
//
// Both sides run with an injected host name, so the EHLO name is compared
// too. The Go side gets the lookup results the recording machine had:
// "localhost" has no A or AAAA record and is 127.0.0.1 and ::1 for the
// system, and only IPv4 is on an interface other than loopback.

type scenario struct {
	Name     string `json:"name"`
	Call     string `json:"call"`
	Hostname struct {
		Value string `json:"value"`
		Error bool   `json:"error"`
	} `json:"hostname"`
	Options struct {
		Host              string `json:"host"`
		Port              int    `json:"port"`
		Secure            bool   `json:"secure"`
		User              string `json:"user"`
		Pass              string `json:"pass"`
		TLSVerify         bool   `json:"tlsVerify"`
		ConnectionTimeout int    `json:"connectionTimeout"`
		GreetingTimeout   int    `json:"greetingTimeout"`
		SocketTimeout     int    `json:"socketTimeout"`
		DNSTimeout        int    `json:"dnsTimeout"`
	} `json:"options"`
	Cmp struct {
		Message     *bool  `json:"message"`
		Client      *bool  `json:"client"`
		ClientUntil string `json:"clientUntil"`
		TLSEvents   *bool  `json:"tlsEvents"`
		SNI         *bool  `json:"sni"`
		Data        *bool  `json:"data"`
	} `json:"cmp"`
	Envelope *struct {
		From        any      `json:"from"`
		To          []string `json:"to"`
		Use8BitMime bool     `json:"use8BitMime"`
		Size        int64    `json:"size"`
	} `json:"envelope"`
	Message *scenarioMessage `json:"message"`
	// DNS holds the A and AAAA records the host had while recording
	DNS *struct {
		A    []string `json:"a"`
		AAAA []string `json:"aaaa"`
	} `json:"dns"`
	Server  sinkScript  `json:"server"`
	Client  []sinkEvent `json:"client"`
	Outcome struct {
		OK    bool           `json:"ok"`
		Info  *recordedInfo  `json:"info"`
		Error *recordedError `json:"error"`
	} `json:"outcome"`
}

type scenarioMessage struct {
	S   *string `json:"s"`
	Gen *struct {
		Seed     uint32 `json:"seed"`
		Size     int    `json:"size"`
		Alphabet string `json:"alphabet"`
	} `json:"gen"`
}

// bytes returns the message of a scenario. Large ones come from the small
// generator the recorder has too.
func (m *scenarioMessage) bytes() []byte {
	if m.Gen == nil {
		return latin1Bytes(*m.S)
	}
	table := latin1Bytes(m.Gen.Alphabet)
	out := make([]byte, m.Gen.Size)
	x := m.Gen.Seed
	for i := range out {
		x = x*1103515245 + 12345
		out[i] = table[int(x>>16)%len(table)]
	}
	return out
}

type recordedError struct {
	Message        string           `json:"message"`
	Code           string           `json:"code"`
	Response       string           `json:"response"`
	ResponseCode   int              `json:"responseCode"`
	Command        string           `json:"command"`
	Recipient      string           `json:"recipient"`
	Rejected       []string         `json:"rejected"`
	RejectedErrors []*recordedError `json:"rejectedErrors"`
}

type recordedInfo struct {
	Accepted []string `json:"accepted"`
	Rejected []string `json:"rejected"`
	Response string   `json:"response"`
	Envelope struct {
		From any      `json:"from"`
		To   []string `json:"to"`
	} `json:"envelope"`
	MessageSize     int64            `json:"messageSize"`
	Ehlo            []string         `json:"ehlo"`
	EhloCount       int              `json:"ehloCount"`
	EhloSHA256      string           `json:"ehloSha256"`
	RejectedErrors  []*recordedError `json:"rejectedErrors"`
	ServerMessageID string           `json:"serverMessageId"`
}

func asRecordedError(err *Error) *recordedError {
	if err == nil {
		return nil
	}
	out := &recordedError{
		Message: err.Message, Code: err.Code, Response: err.Response, ResponseCode: err.ResponseCode,
		Command: err.Command, Recipient: err.Recipient, Rejected: err.Rejected,
	}
	for _, rejected := range err.RejectedErrors {
		out.RejectedErrors = append(out.RejectedErrors, asRecordedError(rejected))
	}
	return out
}

var (
	sessionsOnce sync.Once
	sessionsData []scenario
	sessionsErr  error
)

func loadSessions(t testing.TB) []scenario {
	t.Helper()
	sessionsOnce.Do(func() {
		raw, err := os.ReadFile("testdata/sessions.json")
		if err != nil {
			sessionsErr = err
			return
		}
		sessionsErr = json.Unmarshal(raw, &sessionsData)
	})
	if sessionsErr != nil {
		t.Fatalf("testdata/sessions.json: %v", sessionsErr)
	}
	return sessionsData
}

var (
	rootsOnce sync.Once
	rootsPool *x509.CertPool
)

// trustedRoots is the pool of the test certificates the client trusts, the
// same ones the recorder gave Node as extra root certificates.
func trustedRoots(t testing.TB) *x509.CertPool {
	t.Helper()
	rootsOnce.Do(func() {
		pem, err := os.ReadFile("testdata/certs/trusted.pem")
		if err != nil {
			return
		}
		pool := x509.NewCertPool()
		if pool.AppendCertsFromPEM(pem) {
			rootsPool = pool
		}
	})
	if rootsPool == nil {
		t.Fatal("testdata/certs/trusted.pem holds no certificate")
	}
	return rootsPool
}

// recordingResolver answers as the machine the sessions were recorded on,
// and with the records a scenario gave its host.
type recordingResolver struct {
	a, aaaa []string
}

func (r recordingResolver) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	records := r.a
	if network == "ip6" {
		records = r.aaaa
	}
	if len(records) == 0 {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	var ips []net.IP
	for _, record := range records {
		ips = append(ips, net.ParseIP(record))
	}
	return ips, nil
}

func (recordingResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	if strings.EqualFold(host, "localhost") {
		return []net.IPAddr{{IP: net.ParseIP("::1")}, {IP: net.ParseIP("127.0.0.1")}}, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

// scenarioOptions builds the options of a scenario. Connections to the
// recorded port on 127.0.0.1 go to the Go sink; every other address is
// dialled for real at a port nothing listens on, so that a refusal is the
// operating system's.
func scenarioOptions(t testing.TB, sc *scenario, s *sink, roots *x509.CertPool) Options {
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	opts := Options{
		Host:              sc.Options.Host,
		Port:              sc.Options.Port,
		Secure:            sc.Options.Secure,
		User:              sc.Options.User,
		Pass:              sc.Options.Pass,
		TLSVerify:         sc.Options.TLSVerify,
		ConnectionTimeout: ms(sc.Options.ConnectionTimeout),
		GreetingTimeout:   ms(sc.Options.GreetingTimeout),
		SocketTimeout:     ms(sc.Options.SocketTimeout),
		DNSTimeout:        ms(sc.Options.DNSTimeout),
		RootCAs:           roots,
		Resolver:          recordingResolver{},
		Families:          func() (bool, bool) { return true, false },
		Hostname: func() (string, error) {
			if sc.Hostname.Error {
				return "", os.ErrInvalid
			}
			return sc.Hostname.Value, nil
		},
	}
	if opts.Host == "default" {
		opts.Host = ""
	}
	if sc.DNS != nil {
		opts.Resolver = recordingResolver{a: sc.DNS.A, aaaa: sc.DNS.AAAA}
	}
	opts.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		var dialer net.Dialer
		if s != nil && host == "127.0.0.1" && port == strconv.Itoa(sc.Options.Port) {
			return dialer.DialContext(ctx, network, s.addr())
		}
		if port == "587" || port == "465" {
			return dialer.DialContext(ctx, network, address)
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(host, "1"))
	}
	return opts
}

func TestSessions(t *testing.T) {
	scenarios := loadSessions(t)
	roots := trustedRoots(t)
	if len(scenarios) < 1000 {
		t.Fatalf("only %d sessions in testdata/sessions.json", len(scenarios))
	}
	for i := range scenarios {
		sc := &scenarios[i]
		t.Run(sc.Name, func(t *testing.T) {
			t.Parallel()
			replaySession(t, sc, roots)
		})
	}
}

func replaySession(t *testing.T, sc *scenario, roots *x509.CertPool) {
	var s *sink
	if sc.Server.Listen == nil || *sc.Server.Listen {
		s = startSink(t, sc.Server)
	}
	opts := scenarioOptions(t, sc, s, roots)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var info *Info
	var err error
	if sc.Call == "verify" {
		err = Verify(ctx, opts)
	} else {
		env := Envelope{To: sc.Envelope.To, Use8BitMime: sc.Envelope.Use8BitMime, Size: sc.Envelope.Size}
		if from, ok := sc.Envelope.From.(string); ok {
			env.From = from
		}
		info, err = Send(ctx, opts, env, sc.Message.bytes())
	}
	compareOutcome(t, sc, info, err)

	if s == nil || (sc.Cmp.Client != nil && !*sc.Cmp.Client) {
		return
	}
	got := normalizeEvents(sc, s.wait(t, 20*time.Second))
	want := normalizeEvents(sc, sc.Client)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the client's side of the session differs\n got: %s\nwant: %s", eventsText(got), eventsText(want))
	}
}

func compareOutcome(t *testing.T, sc *scenario, info *Info, err error) {
	t.Helper()
	want := sc.Outcome
	if want.Error != nil {
		var got *Error
		if e, ok := err.(*Error); ok {
			got = e
		}
		if got == nil {
			t.Fatalf("got error %v (info %+v), want %+v", err, info, *want.Error)
		}
		gotFields := asRecordedError(got)
		wantFields := *want.Error
		if sc.Cmp.Message != nil && !*sc.Cmp.Message {
			gotFields.Message, wantFields.Message = "", ""
		}
		if !reflect.DeepEqual(*gotFields, wantFields) {
			t.Errorf("error differs\n got: %s\nwant: %s", jsonText(gotFields), jsonText(wantFields))
		}
		if info != nil {
			t.Errorf("a failed call returned info %+v", info)
		}
		if sc.Options.Pass != "" && strings.Contains(err.Error(), sc.Options.Pass) {
			t.Errorf("the error text holds the password: %q", err.Error())
		}
		return
	}
	if err != nil {
		t.Fatalf("got error %s, want success", jsonText(err))
	}
	if want.Info == nil {
		return
	}
	if info == nil {
		t.Fatalf("no info for a sent message")
	}
	got := recordedInfo{
		Accepted: info.Accepted, Rejected: info.Rejected, Response: info.Response,
		MessageSize: info.MessageSize, Ehlo: info.Ehlo, ServerMessageID: info.ServerMessageID,
	}
	got.Envelope.From = info.EnvelopeFrom
	got.Envelope.To = info.EnvelopeTo
	for _, rejected := range info.RejectedErrors {
		got.RejectedErrors = append(got.RejectedErrors, asRecordedError(rejected))
	}
	wantInfo := *want.Info
	if wantInfo.EhloCount > 0 {
		sum := sha256.Sum256([]byte(strings.Join(got.Ehlo, "\n")))
		got.EhloCount, got.EhloSHA256, got.Ehlo = len(got.Ehlo), hex.EncodeToString(sum[:]), nil
	}
	if sc.Cmp.Data != nil && !*sc.Cmp.Data {
		got.MessageSize, wantInfo.MessageSize = 0, 0
	}
	if !reflect.DeepEqual(got, wantInfo) {
		t.Errorf("info differs\n got: %s\nwant: %s", jsonText(got), jsonText(wantInfo))
	}
}

// normalizeEvents drops from the client's side what a scenario says not to
// compare.
func normalizeEvents(sc *scenario, events []sinkEvent) []sinkEvent {
	out := []sinkEvent{}
	for _, event := range events {
		switch event.E {
		case "tls", "tlsfail":
			if sc.Cmp.TLSEvents != nil && !*sc.Cmp.TLSEvents {
				continue
			}
			if sc.Cmp.SNI != nil && !*sc.Cmp.SNI {
				event.SNI = nil
			}
		case "data":
			if sc.Cmp.Data != nil && !*sc.Cmp.Data {
				event = sinkEvent{E: "data"}
			}
		}
		out = append(out, event)
		if sc.Cmp.ClientUntil != "" && event.E == "line" && strings.HasPrefix(event.S, sc.Cmp.ClientUntil) {
			break
		}
	}
	return out
}

func eventsText(events []sinkEvent) string {
	var out strings.Builder
	for _, event := range events {
		out.WriteString("\n    ")
		out.WriteString(jsonText(event))
	}
	return out.String()
}

func jsonText(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return err.Error()
	}
	if len(raw) > 2000 {
		return string(raw[:2000]) + "..."
	}
	return string(raw)
}
