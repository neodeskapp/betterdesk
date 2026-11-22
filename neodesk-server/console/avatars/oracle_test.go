package avatars

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// The tests in this file replay what lib/avatarImage.js of the Node
// console answered (testdata/image.json.gz and fetch.json.gz). Every
// record must get the same answer here.

func loadVectors(t *testing.T, name string, into any) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name+".json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(zr).Decode(into); err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
}

func unbase64(t *testing.T, text string) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		t.Fatalf("vector data: %v", err)
	}
	return data
}

// codeOf is the code of a refusal, "" for no error.
func codeOf(err error) string {
	if err == nil {
		return ""
	}
	var refusal *Error
	if errors.As(err, &refusal) {
		return refusal.Code
	}
	return "not an avatars.Error: " + err.Error()
}

type imageVectors struct {
	Sniff []struct {
		Data string  `json:"data"`
		Type *string `json:"type"`
	} `json:"sniff"`
	DataURL []struct {
		Value       *string `json:"value"`
		MaxBytes    *int    `json:"maxBytes"`
		Code        *string `json:"code"`
		MimeType    string  `json:"mimeType"`
		Data        string  `json:"data"`
		PNGPaddedTo int     `json:"pngPaddedTo"`
		Length      *int    `json:"length"`
	} `json:"data_url"`
	Refused []struct {
		IP      string `json:"ip"`
		Refused bool   `json:"refused"`
	} `json:"refused"`
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

var onePixelPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGNgYGAAAAAEAAH2FzhVAAAAAElFTkSuQmCC")

func TestOracleImage(t *testing.T) {
	var v imageVectors
	loadVectors(t, "image", &v)
	if len(v.Sniff) < 1000 || len(v.DataURL) < 2000 || len(v.Refused) < 4000 {
		t.Fatalf("vectors are missing: %d sniff, %d data URLs, %d addresses", len(v.Sniff), len(v.DataURL), len(v.Refused))
	}
	for _, rec := range v.Sniff {
		if got := SniffImageType(unbase64(t, rec.Data)); got != deref(rec.Type) {
			t.Errorf("SniffImageType(%s) = %q, Node %q", rec.Data, got, deref(rec.Type))
		}
	}
	for _, rec := range v.DataURL {
		if rec.Value == nil {
			// A picture padded with zero bytes to a size around the limit
			body := append(append([]byte{}, onePixelPNG...), make([]byte, rec.PNGPaddedTo-len(onePixelPNG))...)
			img, err := ParseDataURL("data:image/png;base64,"+base64.StdEncoding.EncodeToString(body), 0)
			if codeOf(err) != deref(rec.Code) {
				t.Errorf("a PNG of %d bytes: code %q, Node %q", rec.PNGPaddedTo, codeOf(err), deref(rec.Code))
			}
			if rec.Length != nil && len(img.Data) != *rec.Length {
				t.Errorf("a PNG of %d bytes: %d bytes kept, Node %d", rec.PNGPaddedTo, len(img.Data), *rec.Length)
			}
			continue
		}
		maxBytes := 0
		if rec.MaxBytes != nil {
			maxBytes = *rec.MaxBytes
			if maxBytes <= 0 {
				// Node took zero and negative limits as they are; the Go
				// options have no way to ask for them
				continue
			}
		}
		img, err := ParseDataURL(*rec.Value, maxBytes)
		if codeOf(err) != deref(rec.Code) {
			t.Errorf("ParseDataURL(%q, %d): code %q, Node %q", *rec.Value, maxBytes, codeOf(err), deref(rec.Code))
			continue
		}
		if err == nil && (img.MimeType != rec.MimeType || !bytes.Equal(img.Data, unbase64(t, rec.Data))) {
			t.Errorf("ParseDataURL(%q, %d) = %s, %d bytes; Node %s, %s", *rec.Value, maxBytes, img.MimeType, len(img.Data), rec.MimeType, rec.Data)
		}
	}
	for _, rec := range v.Refused {
		if got := IsRefusedAddress(rec.IP); got != rec.Refused {
			t.Errorf("IsRefusedAddress(%q) = %v, Node %v", rec.IP, got, rec.Refused)
		}
	}
}

type scriptedAnswer struct {
	Status    int               `json:"status"`
	Headers   map[string]string `json:"headers"`
	Chunks    []string          `json:"chunks"`
	Error     string            `json:"error"`
	BodyError string            `json:"bodyError"`
}

type sentRequest struct {
	Scheme   string            `json:"scheme"`
	Hostname string            `json:"hostname"`
	Port     string            `json:"port"`
	Path     string            `json:"path"`
	Method   string            `json:"method"`
	Headers  map[string]string `json:"headers"`
	Agent    bool              `json:"agent"`
	Guarded  bool              `json:"guarded"`
}

type fetchRecord struct {
	URL          string           `json:"url"`
	Value        *string          `json:"value"`
	MaxBytes     int              `json:"maxBytes"`
	MaxRedirects *int             `json:"maxRedirects"`
	Answers      []scriptedAnswer `json:"answers"`
	Code         *string          `json:"code"`
	MimeType     string           `json:"mimeType"`
	Data         string           `json:"data"`
	Version      string           `json:"version"`
	SourceURL    string           `json:"sourceUrl"`
	Sent         []sentRequest    `json:"sent"`
	VersionOf    *string          `json:"versionOf"`
}

// scriptedTransport plays the answers of a record and notes what was
// sent.
type scriptedTransport struct {
	t       *testing.T
	answers []scriptedAnswer
	sent    []sentRequest
}

type chunkBody struct {
	chunks [][]byte
	err    error
}

func (b *chunkBody) Read(p []byte) (int, error) {
	if len(b.chunks) == 0 {
		if b.err != nil {
			return 0, b.err
		}
		return 0, io.EOF
	}
	n := copy(p, b.chunks[0])
	if b.chunks[0] = b.chunks[0][n:]; len(b.chunks[0]) == 0 {
		b.chunks = b.chunks[1:]
	}
	return n, nil
}

func (b *chunkBody) Close() error { return nil }

func (s *scriptedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	hostname := req.URL.Hostname()
	if req.URL.Host != "" && req.URL.Host[0] == '[' {
		hostname = "[" + hostname + "]"
	}
	headers := map[string]string{}
	for name, values := range req.Header {
		headers[name] = values[0]
	}
	s.sent = append(s.sent, sentRequest{
		Scheme: req.URL.Scheme, Hostname: hostname, Port: req.URL.Port(), Path: req.URL.RequestURI(),
		Method: req.Method, Headers: headers, Guarded: true,
	})
	if len(s.answers) == 0 {
		return nil, errors.New("no scripted answer")
	}
	answer := s.answers[0]
	s.answers = s.answers[1:]
	if answer.Error != "" {
		return nil, errors.New(answer.Error)
	}
	res := &http.Response{StatusCode: answer.Status, Header: http.Header{}, ContentLength: -1, Request: req}
	for name, value := range answer.Headers {
		res.Header.Set(name, value)
		if name == "content-length" {
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				s.t.Fatalf("content-length %q in a vector", value)
			}
			res.ContentLength = n
		}
	}
	body := &chunkBody{}
	for _, chunk := range answer.Chunks {
		body.chunks = append(body.chunks, unbase64(s.t, chunk))
	}
	if answer.BodyError != "" {
		body.err = errors.New(answer.BodyError)
	}
	res.Body = body
	return res, nil
}

func TestOracleFetch(t *testing.T) {
	var v struct {
		Fetches []fetchRecord `json:"fetches"`
		Loads   []fetchRecord `json:"loads"`
	}
	loadVectors(t, "fetch", &v)
	if len(v.Fetches) < 1400 || len(v.Loads) < 200 {
		t.Fatalf("vectors are missing: %d fetches, %d loads", len(v.Fetches), len(v.Loads))
	}
	check := func(what string, rec fetchRecord, transport *scriptedTransport, mime string, data []byte, err error) {
		t.Helper()
		if codeOf(err) != deref(rec.Code) {
			t.Errorf("%s: code %q, Node %q", what, codeOf(err), deref(rec.Code))
			return
		}
		if err == nil && (mime != rec.MimeType || !bytes.Equal(data, unbase64(t, rec.Data))) {
			t.Errorf("%s: %s with %d bytes, Node %s with %s", what, mime, len(data), rec.MimeType, rec.Data)
		}
		if len(transport.sent) != len(rec.Sent) {
			t.Errorf("%s: %d requests sent, Node %d", what, len(transport.sent), len(rec.Sent))
			return
		}
		for i, want := range rec.Sent {
			got := transport.sent[i]
			if want.Agent || !want.Guarded || want.Method != "GET" {
				t.Fatalf("%s: the vector's request is not the guarded GET on a new connection: %+v", what, want)
			}
			if got.Scheme != want.Scheme || got.Hostname != want.Hostname || got.Port != want.Port || got.Path != want.Path {
				t.Errorf("%s: request %d to %s://%s:%s%s, Node %s://%s:%s%s", what, i+1,
					got.Scheme, got.Hostname, got.Port, got.Path, want.Scheme, want.Hostname, want.Port, want.Path)
			}
			if len(got.Headers) != len(want.Headers) {
				t.Errorf("%s: request %d headers %v, Node %v", what, i+1, got.Headers, want.Headers)
			}
			for name, value := range want.Headers {
				if got.Headers[name] != value {
					t.Errorf("%s: request %d header %s = %q, Node %q", what, i+1, name, got.Headers[name], value)
				}
			}
		}
	}
	for _, rec := range v.Fetches {
		transport := &scriptedTransport{t: t, answers: rec.Answers}
		opts := FetchOptions{Transport: transport, MaxBytes: rec.MaxBytes, MaxRedirects: rec.MaxRedirects}
		img, err := FetchURL(context.Background(), rec.URL, opts)
		check("FetchURL("+strconv.Quote(rec.URL)+")", rec, transport, img.MimeType, img.Data, err)
	}
	for _, rec := range v.Loads {
		if rec.VersionOf != nil {
			if got := ImageVersion(unbase64(t, *rec.VersionOf)); got != rec.Version {
				t.Errorf("ImageVersion(%s) = %s, Node %s", *rec.VersionOf, got, rec.Version)
			}
			continue
		}
		transport := &scriptedTransport{t: t, answers: rec.Answers}
		pic, err := LoadPicture(context.Background(), *rec.Value, FetchOptions{Transport: transport})
		what := "LoadPicture(" + strconv.Quote(*rec.Value) + ")"
		check(what, rec, transport, pic.MimeType, pic.Data, err)
		if err == nil && (pic.Version != rec.Version || pic.SourceURL != rec.SourceURL) {
			t.Errorf("%s: version %s from %q, Node %s from %q", what, pic.Version, pic.SourceURL, rec.Version, rec.SourceURL)
		}
	}
}

// The limits are the Node console's.
func TestLimitsAreTheOldOnes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "counts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var counts struct {
		Constants struct {
			MaxAvatarBytes int   `json:"MAX_AVATAR_BYTES"`
			FetchTimeoutMs int64 `json:"FETCH_TIMEOUT_MS"`
			MaxRedirects   int   `json:"MAX_REDIRECTS"`
		} `json:"constants"`
	}
	if err := json.Unmarshal(raw, &counts); err != nil {
		t.Fatal(err)
	}
	c := counts.Constants
	if c.MaxAvatarBytes != MaxAvatarBytes || c.FetchTimeoutMs != FetchTimeout.Milliseconds() || c.MaxRedirects != MaxRedirects {
		t.Fatalf("Node: %+v; here %d bytes, %v, %d redirects", c, MaxAvatarBytes, FetchTimeout, MaxRedirects)
	}
}
