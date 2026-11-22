package console

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// echoAPI is the scripted API server of the recording
// (listen/testdata/README.md), written again in Go:
// requests with an X-Oracle header get an echo of what arrived, others
// have their connection closed.
type echoAPI struct {
	*httptest.Server
	elsewhere *httptest.Server
	mu        sync.Mutex
	hits      int
}

type echo struct {
	Method           string   `json:"method"`
	URL              string   `json:"url"`
	Host             *string  `json:"host"`
	Headers          []string `json:"headers"`
	XFF              *string  `json:"xff"`
	XRI              *string  `json:"xri"`
	XFP              *string  `json:"xfp"`
	XFH              *string  `json:"xfh"`
	Authorization    *string  `json:"authorization"`
	Cookie           *string  `json:"cookie"`
	ContentType      *string  `json:"contentType"`
	ContentLength    *string  `json:"contentLength"`
	TransferEncoding *string  `json:"transferEncoding"`
	AcceptEncoding   *string  `json:"acceptEncoding"`
	UserAgent        *string  `json:"userAgent"`
	Expect           *string  `json:"expect"`
	BodyLength       int      `json:"bodyLength"`
	BodySha          string   `json:"bodySha"`
}

func newEchoAPI(t *testing.T) *echoAPI {
	t.Helper()
	e := &echoAPI{}
	e.Server = httptest.NewServer(http.HandlerFunc(e.serve))
	t.Cleanup(e.Server.Close)
	e.elsewhere = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "elsewhere saw "+r.Method+" "+r.RequestURI)
	}))
	t.Cleanup(e.elsewhere.Close)
	return e
}

func (e *echoAPI) host() string { return strings.TrimPrefix(e.URL, "http://") }

func abort(w http.ResponseWriter) {
	if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
		_ = conn.Close()
	}
}

func (e *echoAPI) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Oracle") == "" {
		abort(w)
		return
	}
	e.mu.Lock()
	e.hits++
	e.mu.Unlock()
	data, _ := io.ReadAll(r.Body)
	mode := ""
	if u, err := r.URL.Parse(r.RequestURI); err == nil {
		mode = u.Query().Get("upstream")
	}
	value := func(name string) *string {
		values := r.Header.Values(name)
		if len(values) == 0 {
			return nil
		}
		v := strings.Join(values, ", ")
		return &v
	}
	names := map[string]bool{}
	for name := range r.Header {
		names[strings.ToLower(name)] = true
	}
	if r.Host != "" {
		names["host"] = true
	}
	// net/http moves the Trailer header of a chunked request to r.Trailer
	if len(r.Trailer) > 0 {
		names["trailer"] = true
	}
	var te *string
	if len(r.TransferEncoding) > 0 {
		names["transfer-encoding"] = true
		v := strings.Join(r.TransferEncoding, ", ")
		te = &v
	}
	list := make([]string, 0, len(names))
	for name := range names {
		list = append(list, name)
	}
	sort.Strings(list)
	host := r.Host
	if host == e.host() {
		host = "upstream"
	}
	sum := sha256.Sum256(data)
	text, _ := httpx.Marshal(echo{
		Method: r.Method, URL: r.RequestURI, Host: &host, Headers: list,
		XFF: value("X-Forwarded-For"), XRI: value("X-Real-IP"), XFP: value("X-Forwarded-Proto"), XFH: value("X-Forwarded-Host"),
		Authorization: value("Authorization"), Cookie: value("Cookie"), ContentType: value("Content-Type"),
		ContentLength: value("Content-Length"), TransferEncoding: te, AcceptEncoding: value("Accept-Encoding"),
		UserAgent: value("User-Agent"), Expect: value("Expect"),
		BodyLength: len(data), BodySha: hex.EncodeToString(sum[:])[:16],
	})
	h := w.Header()
	h["Date"] = nil
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("X-Upstream", "echo")
	h.Set("Cache-Control", "upstream")
	h.Set("Connection", "close")
	status := http.StatusOK
	switch {
	case strings.HasPrefix(mode, "status:"):
		status, _ = strconv.Atoi(mode[len("status:"):])
	case mode == "close":
		abort(w)
		return
	case mode == "cookies":
		h.Add("Set-Cookie", "a=1; Path=/")
		h.Add("Set-Cookie", "b=2; Path=/")
	case strings.HasPrefix(mode, "slow:"):
		ms, _ := strconv.Atoi(mode[len("slow:"):])
		select {
		case <-time.After(time.Duration(ms) * time.Millisecond):
		case <-r.Context().Done():
			return
		}
	}
	if mode == "chunked" {
		w.WriteHeader(status)
		_, _ = w.Write(text[:10])
		w.(http.Flusher).Flush()
		_, _ = w.Write(text[10:])
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(text)))
	w.WriteHeader(status)
	_, _ = w.Write(text)
}

// freePortHolder keeps a port taken for a test.
func holdPort(t *testing.T) (int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln.Addr().(*net.TCPAddr).Port, func() { _ = ln.Close() }
}
