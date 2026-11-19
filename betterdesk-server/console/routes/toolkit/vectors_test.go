package toolkit

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
)

// oracleNow is the clock the Node oracle ran with.
var oracleNow = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

// swapReader is a random source a test loads with the bytes one request
// is to draw.
type swapReader struct{ buf bytes.Buffer }

func (s *swapReader) Read(p []byte) (int, error) { return s.buf.Read(p) }

type textVector struct {
	Route  string `json:"route"`
	Body   string `json:"body"`
	Random string `json:"random"`
	Status int    `json:"status"`
	Answer string `json:"answer"`
}

// TestNodeTextVectors replays testdata/text-vectors.jsonl.gz, recorded by
// testdata/nodeoracle/gen.js from the handlers of the Node console: every
// request must get the same status and the same bytes.
func TestNodeTextVectors(t *testing.T) {
	f, err := os.Open("testdata/text-vectors.jsonl.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	random := &swapReader{}
	// The handlers behind the body parser alone: the guards have tests of
	// their own, and a session lookup for each of 13,000 requests is slow
	h := newHandlers(kit.NewLogger("error", true), Options{Now: func() time.Time { return oracleNow }, Random: random})
	routes := map[string]http.HandlerFunc{"hash": h.hash, "password": h.password, "base64": h.base64, "urlencode": h.urlencode, "jwt-decode": h.jwtDecode}
	steps := middleware.Bodies(middleware.BodyOptions{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routes[strings.TrimPrefix(r.URL.Path, "/api/toolkit/")](w, kit.Begin(r, httpx.TrustProxy{}, nil))
	}))
	post := func(route, body string) (int, string) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/toolkit/"+route, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		steps.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	counts := map[string]int{}
	failures, changed := 0, 0
	lines := bufio.NewScanner(zr)
	lines.Buffer(make([]byte, 1<<20), 1<<26)
	for n := 1; lines.Scan(); n++ {
		var v textVector
		if err := json.Unmarshal(lines.Bytes(), &v); err != nil {
			t.Fatalf("vector %d: %v", n, err)
		}
		counts[v.Route]++
		drawn, err := hex.DecodeString(v.Random)
		if err != nil {
			t.Fatalf("vector %d: %v", n, err)
		}
		random.buf.Reset()
		random.buf.Write(drawn)
		if v.Route == "password" && strings.HasPrefix(v.Body, "[") && v.Status == 200 {
			// SC9: a body that is an array names no length, so the password
			// has the default 16 characters, drawn from bytes Node never drew
			random.buf.Reset()
			random.buf.Write(bytes.Repeat([]byte{1, 2, 3, 4}, 64))
			status, body := post(v.Route, v.Body)
			var got struct {
				Data struct{ Length int } `json:"data"`
			}
			if err := json.Unmarshal([]byte(body), &got); status != 200 || err != nil || got.Data.Length != 16 {
				t.Errorf("vector %d: password %s: %d %s, want 16 characters", n, clip(v.Body), status, clip(body))
			}
			changed++
			continue
		}
		status, body := post(v.Route, v.Body)
		if status != v.Status || body != v.Answer {
			failures++
			if failures <= 15 {
				t.Errorf("vector %d: %s %s\n got  %d %s\n node %d %s", n, v.Route, clip(v.Body), status, clip(body), v.Status, clip(v.Answer))
			}
		}
		if v.Route == "password" && random.buf.Len() != 0 {
			t.Errorf("vector %d: password %s left %d of the %d random bytes Node drew", n, clip(v.Body), random.buf.Len(), len(drawn))
		}
	}
	if err := lines.Err(); err != nil {
		t.Fatal(err)
	}
	if failures > 15 {
		t.Errorf("%d more differences not shown", failures-15)
	}
	for route, least := range map[string]int{"hash": 30, "password": 2000, "base64": 3000, "urlencode": 4000, "jwt-decode": 3000} {
		if counts[route] < least {
			t.Errorf("%d vectors for %s, want at least %d: is the recorded file complete?", counts[route], route, least)
		}
	}
	t.Logf("replayed %v; %d passwords of an array body have the default length (SC9)", counts, changed)
}

func clip(s string) string {
	if len(s) > 300 {
		return fmt.Sprintf("%s... (%d bytes)", s[:300], len(s))
	}
	return s
}

type sslVector struct {
	Server  string `json:"server"`
	Trusted bool   `json:"trusted"`
	Now     int64  `json:"now"`
	Status  int    `json:"status"`
	Answer  string `json:"answer"`
}

// TestNodeSSLVectors replays what the Node console's certificate check
// answered for TLS servers showing the certificates of testdata/certs
// (testdata/nodeoracle/ssl.js), once with the test root trusted and once
// without. The check gets the same certificates here, as the result of a
// handshake with the protocol and cipher Node had, and must answer the
// same bytes.
func TestNodeSSLVectors(t *testing.T) {
	total := 0
	for _, file := range []string{"ssl-vectors-plain.json", "ssl-vectors-trusted.json"} {
		raw, err := os.ReadFile("testdata/" + file)
		if err != nil {
			t.Fatal(err)
		}
		var vectors []sslVector
		if err := json.Unmarshal(raw, &vectors); err != nil {
			t.Fatal(err)
		}
		for _, v := range vectors {
			total++
			t.Run(fmt.Sprintf("%s/trusted=%v", v.Server, v.Trusted), func(t *testing.T) {
				asked := ""
				s := newSite(t, Options{
					Network: Network{
						Roots: rootsOf(t, v.Trusted),
						Handshake: func(_ context.Context, _ string, addresses []string, port int) (*tls.ConnectionState, error) {
							asked = fmt.Sprintf("%s:%d", strings.Join(addresses, ","), port)
							return &tls.ConnectionState{PeerCertificates: sentBy(t, v.Server), Version: tls.VersionTLS13, CipherSuite: tls.TLS_AES_256_GCM_SHA384}, nil
						},
					},
					Now: func() time.Time { return time.UnixMilli(v.Now) },
				})
				// Node asked about 0.0.0.0, which the guard refuses since BUGS.md
				// SC3. The certificates name 10.1.2.3 as well, so the answers stay
				status, body := s.post(s.signIn("admin"), "/api/toolkit/ssl", `{"host":"10.1.2.3","port":8443}`)
				if status != v.Status || body != v.Answer {
					t.Errorf("\n got  %d %s\n node %d %s", status, body, v.Status, v.Answer)
				}
				if asked != "10.1.2.3:8443" {
					t.Errorf("the handshake went to %q", asked)
				}
			})
		}
	}
	if total != 2*len(testServers) {
		t.Errorf("%d vectors, want one for each of the %d test servers with and without the root", total, len(testServers))
	}
}
