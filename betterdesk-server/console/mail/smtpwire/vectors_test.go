package smtpwire

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/netip"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// The tests here replay testdata/vectors.json: what single parts of
// nodemailer (and Node's certificate name check) answered for many inputs,
// recorded by testdata/nodeoracle/record_vectors.js.

type vectorFile struct {
	Ehlo []struct {
		Reply          string         `json:"reply"`
		Secure         bool           `json:"secure"`
		Sent           []string       `json:"sent"`
		Connected      bool           `json:"connected"`
		Error          *recordedError `json:"error"`
		AllowsAuth     bool           `json:"allowsAuth"`
		SupportedAuth  []string       `json:"supportedAuth"`
		Extensions     []string       `json:"extensions"`
		MaxAllowedSize string         `json:"maxAllowedSize"`
		EhloLines      []string       `json:"ehloLines"`
	} `json:"ehlo"`
	Replies struct {
		Random []struct {
			Chunks    []string     `json:"chunks"`
			Events    []replyEvent `json:"events"`
			Remainder string       `json:"remainder"`
		} `json:"random"`
		Big []struct {
			Name            string       `json:"name"`
			Events          []replyEvent `json:"events"`
			RemainderLength int          `json:"remainderLength"`
		} `json:"big"`
	} `json:"replies"`
	Data []struct {
		Chunks   []string `json:"chunks"`
		Out      string   `json:"out"`
		InBytes  int64    `json:"inBytes"`
		OutBytes int64    `json:"outBytes"`
	} `json:"data"`
	Cram []struct {
		Reply string         `json:"reply"`
		User  string         `json:"user"`
		Pass  string         `json:"pass"`
		Sent  *string        `json:"sent"`
		Error *recordedError `json:"error"`
	} `json:"cram"`
	Envelope []struct {
		Envelope struct {
			From        string   `json:"from"`
			To          []string `json:"to"`
			Use8BitMime bool     `json:"use8BitMime"`
			Size        int64    `json:"size"`
		} `json:"envelope"`
		Extensions []string       `json:"extensions"`
		Sent       []string       `json:"sent"`
		Error      *recordedError `json:"error"`
		From       string         `json:"from"`
		To         []string       `json:"to"`
	} `json:"envelope"`
	Hostname []struct {
		Hostname string `json:"hostname"`
		Error    bool   `json:"error"`
		Name     string `json:"name"`
	} `json:"hostname"`
	MessageID []struct {
		Response string  `json:"response"`
		Host     string  `json:"host"`
		ID       *string `json:"id"`
	} `json:"messageId"`
	Identity []struct {
		Host     string   `json:"host"`
		CN       []string `json:"cn"`
		AltNames *string  `json:"altNames"`
		DNS      []string `json:"dns"`
		IPText   []string `json:"ipText"`
		Error    *string  `json:"error"`
	} `json:"identity"`
	Certificates []struct {
		Name     string   `json:"name"`
		CN       []string `json:"cn"`
		AltNames *string  `json:"altNames"`
	} `json:"certificates"`
}

type replyEvent struct {
	Reply       *string        `json:"reply,omitempty"`
	ReplyLength *int           `json:"replyLength,omitempty"`
	ReplySHA256 string         `json:"replySha256,omitempty"`
	Error       *recordedError `json:"error,omitempty"`
}

var (
	vectorsOnce sync.Once
	vectorsData vectorFile
	vectorsErr  error
)

func loadVectors(t testing.TB) *vectorFile {
	t.Helper()
	vectorsOnce.Do(func() {
		raw, err := os.ReadFile("testdata/vectors.json")
		if err != nil {
			vectorsErr = err
			return
		}
		vectorsErr = json.Unmarshal(raw, &vectorsData)
	})
	if vectorsErr != nil {
		t.Fatalf("testdata/vectors.json: %v", vectorsErr)
	}
	return &vectorsData
}

func unhex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

func sameStrings(a, b []string) bool {
	return len(a) == len(b) && (len(a) == 0 || reflect.DeepEqual(a, b))
}

func TestEhloVectors(t *testing.T) {
	vectors := loadVectors(t).Ehlo
	if len(vectors) < 1000 {
		t.Fatalf("only %d vectors", len(vectors))
	}
	steps := map[string]int{}
	for i, v := range vectors {
		step := afterEhlo(v.Reply, v.Secure, false)
		steps[step]++
		want := ""
		switch {
		case v.Error != nil:
			want = ehloClosing
		case len(v.Sent) == 1 && strings.HasPrefix(v.Sent[0], "HELO "):
			want = ehloRefused
		case len(v.Sent) == 1 && v.Sent[0] == "STARTTLS":
			want = ehloStartTLS
		case v.Connected:
			want = ehloAccepted
		}
		if step != want {
			t.Errorf("#%d reply %q secure %v: step %q, want %q", i, v.Reply, v.Secure, step, want)
			continue
		}
		if v.Error != nil {
			got := asRecordedError(newError("Server terminates connection. response="+v.Reply, "ECONNECTION", v.Reply, "EHLO"))
			if !reflect.DeepEqual(got, v.Error) {
				t.Errorf("#%d reply %q: error %s, want %s", i, v.Reply, jsonText(got), jsonText(v.Error))
			}
		}
		if !v.Connected {
			continue
		}
		info := parseEhlo(v.Reply)
		var mechanisms []string
		for _, mechanism := range v.SupportedAuth {
			if mechanism != "XOAUTH2" {
				mechanisms = append(mechanisms, mechanism)
			}
		}
		if info.allowsAuth != v.AllowsAuth || !sameStrings(info.mechanisms, mechanisms) {
			t.Errorf("#%d reply %q: auth %v %q, want %v %q", i, v.Reply, info.allowsAuth, info.mechanisms, v.AllowsAuth, mechanisms)
		}
		if info.smtpUTF8 != contains(v.Extensions, "SMTPUTF8") || info.eightBitMIME != contains(v.Extensions, "8BITMIME") ||
			info.pipelining != contains(v.Extensions, "PIPELINING") || info.size != contains(v.Extensions, "SIZE") {
			t.Errorf("#%d reply %q: extensions %+v, want %q", i, v.Reply, info, v.Extensions)
		}
		if jsNumber(info.maxSize) != v.MaxAllowedSize {
			t.Errorf("#%d reply %q: size limit %s, want %s", i, v.Reply, jsNumber(info.maxSize), v.MaxAllowedSize)
		}
		if !sameStrings(info.lines, v.EhloLines) {
			t.Errorf("#%d reply %q: lines %q, want %q", i, v.Reply, info.lines, v.EhloLines)
		}
	}
	for _, step := range []string{ehloClosing, ehloRefused, ehloStartTLS, ehloAccepted} {
		if steps[step] < 20 {
			t.Errorf("only %d vectors lead to %q", steps[step], step)
		}
	}
}

// feedReplies gives chunks to a reply queue the way a session does and
// returns the replies it would hand to its actions.
func feedReplies(chunks [][]byte) (events []replyEvent, remainder []byte) {
	var q replyQueue
	for _, chunk := range chunks {
		if len(chunk) == 0 {
			continue
		}
		due, tooLarge := q.add(chunk)
		if tooLarge {
			events = append(events, replyEvent{Error: asRecordedError(newError("Server response exceeds maximum allowed size", "EPROTOCOL", "", "CONN"))})
			break
		}
		for due {
			raw, ok := q.next()
			if !ok {
				break
			}
			if trimReplyBytes(raw) == "" {
				continue
			}
			reply := decodeReply(raw)
			events = append(events, replyEvent{Reply: &reply})
		}
	}
	return events, q.remainder
}

func replyEventsText(events []replyEvent) string {
	if events == nil {
		events = []replyEvent{}
	}
	return jsonText(events)
}

func TestReplyVectors(t *testing.T) {
	vectors := loadVectors(t).Replies
	if len(vectors.Random) < 1000 {
		t.Fatalf("only %d vectors", len(vectors.Random))
	}
	for i, v := range vectors.Random {
		var chunks [][]byte
		for _, chunk := range v.Chunks {
			chunks = append(chunks, unhex(t, chunk))
		}
		events, remainder := feedReplies(chunks)
		if got, want := replyEventsText(events), replyEventsText(v.Events); got != want {
			t.Errorf("#%d chunks %q:\n got %s\nwant %s", i, chunks, got, want)
		}
		if hex.EncodeToString(remainder) != v.Remainder {
			t.Errorf("#%d chunks %q: remainder %q, want %q", i, chunks, remainder, unhex(t, v.Remainder))
		}
	}
}

// TestReplySizeLimit feeds replies around the 1 MiB limit in the chunk sizes
// the recorder used.
func TestReplySizeLimit(t *testing.T) {
	chunked := func(data []byte, size int) [][]byte {
		var chunks [][]byte
		for len(data) > 0 {
			n := min(size, len(data))
			chunks = append(chunks, data[:n])
			data = data[n:]
		}
		return chunks
	}
	line := "250-" + strings.Repeat("y", 94) + "\r\n"
	x := func(n int) string { return strings.Repeat("x", n) }
	inputs := map[string][][]byte{
		"line-over":                       chunked([]byte("250 "+x(1100000)), 65536),
		"line-under-then-end":             chunked([]byte("250 "+x(1000000)+"\r\n"), 65536),
		"line-exactly-limit":              chunked([]byte(x(1048576)+"\r\n250 next\r\n"), 1048576),
		"line-limit-plus-one":             chunked([]byte(x(1048577)), 1048577),
		"line-over-in-one-chunk-with-end": chunked([]byte(x(1500000)+"\r\n"), 2000000),
		"multi-over":                      chunked([]byte(strings.Repeat(line, 11000)+"250 done\r\n"), 65536),
		"multi-under":                     chunked([]byte(strings.Repeat(line, 9000)+"250 done\r\n"), 65536),
		"multi-over-in-one-chunk":         chunked([]byte(strings.Repeat(line, 11000)+"250 done\r\n"), 2000000),
		"multi-last-line-over":            chunked([]byte(strings.Repeat(line, 10400)+"250 "+strings.Repeat("z", 60000)+"\r\n"), 2000000),
		"remainder-over-after-lines":      chunked([]byte("250 ok\r\n"+x(1048577)), 2000000),
	}
	vectors := loadVectors(t).Replies.Big
	if len(vectors) != len(inputs) {
		t.Fatalf("%d recorded cases, %d inputs", len(vectors), len(inputs))
	}
	tooLarge := 0
	for _, v := range vectors {
		chunks, ok := inputs[v.Name]
		if !ok {
			t.Errorf("%s: no input for this recorded case", v.Name)
			continue
		}
		events, remainder := feedReplies(chunks)
		for i, event := range events {
			if event.Reply != nil {
				sum := sha256.Sum256([]byte(*event.Reply))
				n := len(*event.Reply)
				events[i] = replyEvent{ReplyLength: &n, ReplySHA256: hex.EncodeToString(sum[:])}
			} else {
				tooLarge++
			}
		}
		if got, want := replyEventsText(events), replyEventsText(v.Events); got != want {
			t.Errorf("%s:\n got %s\nwant %s", v.Name, got, want)
		}
		if len(remainder) != v.RemainderLength {
			t.Errorf("%s: %d bytes left, want %d", v.Name, len(remainder), v.RemainderLength)
		}
	}
	if tooLarge < 4 {
		t.Errorf("only %d cases ran into the limit", tooLarge)
	}
}

// TestReplyQueueBound checks the one limit nodemailer does not have: replies
// that pile up behind a continued reply the server never ends.
func TestReplyQueueBound(t *testing.T) {
	var q replyQueue
	chunk := []byte(strings.Repeat("250 complete reply\r\n", 1000) + "250-never ended\r\n")
	for i := 0; i < 200; i++ {
		due, tooLarge := q.add(chunk)
		if due {
			t.Fatalf("chunk %d: replies are due although one is half received", i)
		}
		if tooLarge {
			if q.queued != 0 || len(q.complete) != 0 || len(q.current) != 0 || len(q.remainder) != 0 {
				t.Fatalf("the queue is not empty after the limit: %d bytes", q.queued)
			}
			if i < 40 {
				t.Fatalf("the limit was hit after %d chunks of %d bytes", i, len(chunk))
			}
			return
		}
		if q.queued > maxResponseSize+len(chunk) {
			t.Fatalf("%d bytes are queued", q.queued)
		}
	}
	t.Fatal("4 MB of replies were queued without hitting the limit")
}

func TestDataStreamVectors(t *testing.T) {
	vectors := loadVectors(t).Data
	if len(vectors) < 1000 {
		t.Fatalf("only %d vectors", len(vectors))
	}
	for i, v := range vectors {
		stream := newDataStream()
		var out []byte
		for _, chunk := range v.Chunks {
			out = stream.transform(out, unhex(t, chunk))
		}
		out = stream.finish(out)
		if hex.EncodeToString(out) != v.Out {
			t.Errorf("#%d chunks %q: got %q, want %q", i, v.Chunks, out, unhex(t, v.Out))
		}
		if stream.in != v.InBytes || stream.out != v.OutBytes {
			t.Errorf("#%d: counted %d in and %d out, want %d and %d", i, stream.in, stream.out, v.InBytes, v.OutBytes)
		}
	}
}

func TestCramVectors(t *testing.T) {
	vectors := loadVectors(t).Cram
	if len(vectors) < 1000 {
		t.Fatalf("only %d vectors", len(vectors))
	}
	refused := 0
	for i, v := range vectors {
		challenge, ok := cramChallenge(v.Reply)
		if v.Error != nil {
			refused++
			if ok {
				t.Errorf("#%d reply %q: challenge %q, want none", i, v.Reply, challenge)
				continue
			}
			got := asRecordedError(newError("Invalid login sequence while waiting for server challenge string", "EAUTH", v.Reply, "AUTH CRAM-MD5"))
			if !reflect.DeepEqual(got, v.Error) {
				t.Errorf("#%d reply %q: error %s, want %s", i, v.Reply, jsonText(got), jsonText(v.Error))
			}
			continue
		}
		if !ok {
			t.Errorf("#%d reply %q: no challenge, want the answer %q", i, v.Reply, *v.Sent)
			continue
		}
		if got := cramResponse(v.User, v.Pass, challenge); got != *v.Sent {
			t.Errorf("#%d reply %q: answer %q, want %q", i, v.Reply, got, *v.Sent)
		}
	}
	if refused < 20 {
		t.Errorf("only %d vectors are refused challenges", refused)
	}
}

func TestEnvelopeVectors(t *testing.T) {
	vectors := loadVectors(t).Envelope
	if len(vectors) < 1000 {
		t.Fatalf("only %d vectors", len(vectors))
	}
	kinds := map[string]int{}
	for i, v := range vectors {
		env := Envelope{From: v.Envelope.From, To: v.Envelope.To, Use8BitMime: v.Envelope.Use8BitMime, Size: v.Envelope.Size}
		state, err := newEnvelopeState(&env)
		if v.Error != nil {
			kinds[strings.SplitN(v.Error.Message, " \"", 2)[0]]++
			if got := asRecordedError(err); !reflect.DeepEqual(got, v.Error) {
				t.Errorf("#%d envelope %+v: error %s, want %s", i, v.Envelope, jsonText(got), jsonText(v.Error))
			}
			continue
		}
		if err != nil {
			t.Errorf("#%d envelope %+v: error %s, want %q", i, v.Envelope, jsonText(err), v.Sent)
			continue
		}
		ehlo := ehloInfo{
			smtpUTF8: contains(v.Extensions, "SMTPUTF8"), eightBitMIME: contains(v.Extensions, "8BITMIME"),
			pipelining: contains(v.Extensions, "PIPELINING"), size: contains(v.Extensions, "SIZE"),
		}
		command, _ := mailCommand(state, &env, &ehlo)
		want := []string{command}
		if ehlo.pipelining {
			for _, to := range state.to {
				want = append(want, "RCPT TO:<"+to+">")
			}
			want = append(want, "DATA")
		}
		if !reflect.DeepEqual(want, v.Sent) {
			t.Errorf("#%d envelope %+v with %q: commands %q, want %q", i, v.Envelope, v.Extensions, want, v.Sent)
		}
		if state.from != v.From || !sameStrings(state.to, v.To) {
			t.Errorf("#%d envelope %+v: addresses %q %q, want %q %q", i, v.Envelope, state.from, state.to, v.From, v.To)
		}
		kinds["ok"]++
	}
	for _, kind := range []string{"ok", "No recipients defined", "Invalid sender", "Invalid recipient"} {
		if kinds[kind] < 20 {
			t.Errorf("only %d vectors of kind %q", kinds[kind], kind)
		}
	}
}

func TestClientNameVectors(t *testing.T) {
	vectors := loadVectors(t).Hostname
	if len(vectors) < 20 {
		t.Fatalf("only %d vectors", len(vectors))
	}
	for _, v := range vectors {
		got := clientName(func() (string, error) {
			if v.Error {
				return "", os.ErrInvalid
			}
			return v.Hostname, nil
		})
		if got != v.Name {
			t.Errorf("host name %q (error %v): EHLO name %q, want %q", v.Hostname, v.Error, got, v.Name)
		}
	}
}

func TestServerMessageIDVectors(t *testing.T) {
	vectors := loadVectors(t).MessageID
	if len(vectors) < 100 {
		t.Fatalf("only %d vectors", len(vectors))
	}
	found := 0
	for _, v := range vectors {
		want := ""
		if v.ID != nil {
			want = *v.ID
			found++
		}
		if got := serverMessageID(v.Response, v.Host); got != want {
			t.Errorf("reply %q from %q: id %q, want %q", v.Response, v.Host, got, want)
		}
	}
	if found < 20 {
		t.Errorf("only %d vectors name an id", found)
	}
}

func TestIdentityVectors(t *testing.T) {
	vectors := loadVectors(t).Identity
	if len(vectors) < 1000 {
		t.Fatalf("only %d vectors", len(vectors))
	}
	refused := 0
	for i, v := range vectors {
		names := certNames{dns: v.DNS, commonNames: v.CN}
		if v.AltNames != nil {
			names.altNames, names.hasAlt = *v.AltNames, true
		}
		for _, text := range v.IPText {
			addr, err := netip.ParseAddr(text)
			if err != nil {
				t.Fatalf("#%d: %v", i, err)
			}
			names.ips = append(names.ips, addr.String())
		}
		got := identityProblem(v.Host, names)
		if got != "" {
			got = "Hostname/IP does not match certificate's altnames: " + got
		}
		want := ""
		if v.Error != nil {
			want = *v.Error
			refused++
		}
		if got != want {
			t.Errorf("#%d host %q, names %q, CN %q:\n got %q\nwant %q", i, v.Host, names.altNames, v.CN, got, want)
		}
	}
	if refused < 100 || len(vectors)-refused < 100 {
		t.Errorf("%d of %d vectors are refusals", refused, len(vectors))
	}
}

func loadCertificate(t testing.TB, name string) *x509.Certificate {
	t.Helper()
	raw, err := os.ReadFile("testdata/certs/" + name + ".chain.pem")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		t.Fatalf("%s: no certificate", name)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return cert
}

// TestCertificateNames compares how the names of the test certificates are
// read with what Node prints for them.
func TestCertificateNames(t *testing.T) {
	vectors := loadVectors(t).Certificates
	if len(vectors) < 20 {
		t.Fatalf("only %d certificates", len(vectors))
	}
	for _, v := range vectors {
		names := namesOf(loadCertificate(t, v.Name))
		if !sameStrings(names.commonNames, v.CN) {
			t.Errorf("%s: common names %q, want %q", v.Name, names.commonNames, v.CN)
		}
		if names.hasAlt != (v.AltNames != nil) {
			t.Errorf("%s: alternative names present %v, want %v", v.Name, names.hasAlt, v.AltNames != nil)
		}
		if v.AltNames != nil && names.altNames != *v.AltNames {
			t.Errorf("%s: alternative names\n got %q\nwant %q", v.Name, names.altNames, *v.AltNames)
		}
	}
}

// TestDataStreamChunks checks that the encoding does not depend on where a
// message is cut into pieces.
func TestDataStreamChunks(t *testing.T) {
	message := []byte(".\r\n.a\r.\n.\r\r\n\n..\r\n.\rx\n.y\r\n.")
	whole := newDataStream()
	want := whole.finish(whole.transform(nil, message))
	for first := 0; first <= len(message); first++ {
		for second := first; second <= len(message); second++ {
			stream := newDataStream()
			got := stream.transform(nil, message[:first])
			got = stream.transform(got, message[first:second])
			got = stream.transform(got, message[second:])
			got = stream.finish(got)
			if !bytes.Equal(got, want) {
				t.Fatalf("cut at %d and %d: %q, want %q", first, second, got, want)
			}
		}
	}
	if !bytes.Equal(want, []byte("..\r\n..a\r\n..\r\n..\r\n\r\n\r\n...\r\n..\r\nx\r\n..y\r\n..\r\n.\r\n")) {
		t.Fatalf("encoded as %q", want)
	}
}
