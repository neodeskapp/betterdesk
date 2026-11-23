package avatars

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTripFunc func(ctx context.Context) error

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, f(req.Context())
}

// pictureServer is a web server the tests fetch pictures from. It listens
// on loopback, which a fetch refuses, so the tests reach it the way a
// picture host on the LAN is reached: names resolve to a LAN address (or
// to whatever a test says), and the connection to a checked address is
// made to the server instead.
type pictureServer struct {
	*httptest.Server
	mu      sync.Mutex
	hits    []string
	headers []http.Header
	dialed  []string
	// addresses is what names resolve to.
	addresses map[string][]netip.Addr
	// down lists addresses that refuse connections.
	down map[string]bool
}

const big = MaxAvatarBytes + 1024

func newPictureServer(t *testing.T) *pictureServer {
	t.Helper()
	s := &pictureServer{addresses: map[string][]netip.Addr{}, down: map[string]bool{}}
	mux := http.NewServeMux()
	send := func(contentType string, body []byte) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", contentType)
			_, _ = w.Write(body)
		}
	}
	mux.HandleFunc("/a.png", send("image/png", onePixelPNG))
	mux.HandleFunc("/big.png", func(w http.ResponseWriter, _ *http.Request) {
		// No Content-Length: the limit has to hold while the body arrives
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(onePixelPNG)
		w.(http.Flusher).Flush()
		_, _ = w.Write(make([]byte, big))
	})
	mux.HandleFunc("/big-declared.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", fmt.Sprint(MaxAvatarBytes+1))
	})
	mux.HandleFunc("/fits.png", send("image/png", append(append([]byte{}, onePixelPNG...), make([]byte, MaxAvatarBytes-len(onePixelPNG))...)))
	mux.HandleFunc("/html", send("text/html", []byte("<h1>hi</h1>")))
	mux.HandleFunc("/svg", send("image/svg+xml", []byte("<svg/>")))
	mux.HandleFunc("/lies.png", send("image/png", []byte("<script>x</script>")))
	mux.HandleFunc("/jpeg-as.png", send("image/png", []byte{0xff, 0xd8, 0xff, 0xe0, 0, 0x10}))
	mux.HandleFunc("/slow.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(onePixelPNG[:4])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc("/to-loopback", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:"+s.port()+"/a.png", http.StatusFound)
	})
	mux.HandleFunc("/to-metadata", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	})
	mux.HandleFunc("/to-rebind", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://rebind.example.test:"+s.port()+"/a.png", http.StatusFound)
	})
	mux.HandleFunc("/hop/", func(w http.ResponseWriter, r *http.Request) {
		var n int
		_, _ = fmt.Sscanf(r.URL.Path, "/hop/%d", &n)
		if n > 0 {
			http.Redirect(w, r, fmt.Sprintf("/hop/%d", n-1), http.StatusFound)
			return
		}
		http.Redirect(w, r, "/a.png", http.StatusFound)
	})
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		s.headers = append(s.headers, r.Header.Clone())
		s.mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *pictureServer) port() string {
	_, port, _ := net.SplitHostPort(s.Listener.Addr().String())
	return port
}

func (s *pictureServer) options() FetchOptions {
	return FetchOptions{
		Lookup: func(_ context.Context, host string) ([]netip.Addr, error) {
			if addrs, ok := s.addresses[host]; ok {
				return addrs, nil
			}
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		},
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			s.mu.Lock()
			s.dialed = append(s.dialed, address)
			s.mu.Unlock()
			if host, _, _ := net.SplitHostPort(address); s.down[host] {
				return nil, errors.New("connection refused")
			}
			return (&net.Dialer{}).DialContext(ctx, network, s.Listener.Addr().String())
		},
	}
}

func (s *pictureServer) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.hits...)
}

func (s *pictureServer) url(host, path string) string {
	return "http://" + host + ":" + s.port() + path
}

func wantCode(t *testing.T, what string, err error, code string) {
	t.Helper()
	if codeOf(err) != code {
		t.Errorf("%s: %q (%v), want %q", what, codeOf(err), err, code)
	}
}

func TestFetchRefusesLoopbackAlthoughAServerListensThere(t *testing.T) {
	s := newPictureServer(t)
	for _, host := range []string{"127.0.0.1", "localhost", "[::1]", "LOCALHOST", "x.localhost", "0x7f.1", "2130706433"} {
		_, err := FetchURL(context.Background(), s.url(host, "/a.png"), s.options())
		wantCode(t, host, err, "address_refused")
	}
	for _, address := range []string{"http://169.254.169.254/latest/meta-data/", "http://metadata.google.internal/", "http://metadata.goog/"} {
		_, err := FetchURL(context.Background(), address, s.options())
		wantCode(t, address, err, "address_refused")
	}
	if hits := s.seen(); len(hits) != 0 || len(s.dialed) != 0 {
		t.Fatalf("requests went out: %v, connections: %v", hits, s.dialed)
	}
}

func TestFetchChecksTheAddressAfterTheNameWasResolved(t *testing.T) {
	s := newPictureServer(t)
	s.addresses["pictures.example.test"] = []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	s.addresses["mixed.example.test"] = []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("169.254.169.254")}
	s.addresses["mapped.example.test"] = []netip.Addr{netip.MustParseAddr("::ffff:127.0.0.1")}
	s.addresses["six.example.test"] = []netip.Addr{netip.MustParseAddr("fe80::1%eth0")}
	s.addresses["empty.example.test"] = nil
	// A name the parser lets through and the resolver maps to this machine
	s.addresses["localhost."] = []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	for host, code := range map[string]string{
		"pictures.example.test": "address_refused",
		"mixed.example.test":    "address_refused",
		"mapped.example.test":   "address_refused",
		"six.example.test":      "address_refused",
		"localhost.":            "address_refused",
		"empty.example.test":    "dns_failed",
		"unknown.example.test":  "fetch_failed",
	} {
		_, err := FetchURL(context.Background(), s.url(host, "/a.png"), s.options())
		wantCode(t, host, err, code)
	}
	if hits := s.seen(); len(hits) != 0 || len(s.dialed) != 0 {
		t.Fatalf("requests went out: %v, connections: %v", hits, s.dialed)
	}
}

func TestFetchRefusesOtherSchemesAndCredentials(t *testing.T) {
	s := newPictureServer(t)
	for _, address := range []string{"file:///etc/passwd", "ftp://example.com/a.png", "http://u:p@example.com/a.png", "http://u@example.com/a.png", "example.com/a.png", ""} {
		_, err := FetchURL(context.Background(), address, s.options())
		wantCode(t, address, err, "invalid_url")
	}
}

func TestFetchFromALANAddress(t *testing.T) {
	s := newPictureServer(t)
	s.addresses["idp.lan.test"] = []netip.Addr{netip.MustParseAddr("192.168.1.20")}
	for _, host := range []string{"10.0.0.5", "idp.lan.test", "[fd12:3456::1]"} {
		img, err := FetchURL(context.Background(), s.url(host, "/a.png"), s.options())
		if err != nil || img.MimeType != TypePNG || !bytes.Equal(img.Data, onePixelPNG) {
			t.Fatalf("%s: %s, %d bytes, %v", host, img.MimeType, len(img.Data), err)
		}
	}
	want := []string{"10.0.0.5:" + s.port(), "192.168.1.20:" + s.port(), "[fd12:3456::1]:" + s.port()}
	if strings.Join(s.dialed, " ") != strings.Join(want, " ") {
		t.Fatalf("connections went to %v, want %v", s.dialed, want)
	}
	// What the request carries: the two headers of the old console, a new
	// connection, no compression asked for
	h := s.headers[0]
	if h.Get("Accept") != "image/png,image/jpeg,image/webp,image/gif;q=0.9" || h.Get("User-Agent") != "NeoDesk-Console/avatar" {
		t.Errorf("request headers: %v", h)
	}
	if h.Get("Accept-Encoding") != "" || h.Get("Connection") != "close" {
		t.Errorf("Accept-Encoding %q and Connection %q, want none and close", h.Get("Accept-Encoding"), h.Get("Connection"))
	}
}

func TestFetchTriesTheNextAddressOfAName(t *testing.T) {
	s := newPictureServer(t)
	s.addresses["two.lan.test"] = []netip.Addr{netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("10.0.0.2")}
	s.down["10.0.0.1"] = true
	if _, err := FetchURL(context.Background(), s.url("two.lan.test", "/a.png"), s.options()); err != nil {
		t.Fatal(err)
	}
	if len(s.dialed) != 2 {
		t.Fatalf("connections: %v", s.dialed)
	}
	s.down["10.0.0.2"] = true
	_, err := FetchURL(context.Background(), s.url("two.lan.test", "/a.png"), s.options())
	wantCode(t, "both addresses down", err, "fetch_failed")
}

func TestFetchSizeLimit(t *testing.T) {
	s := newPictureServer(t)
	for _, path := range []string{"/big.png", "/big-declared.png"} {
		_, err := FetchURL(context.Background(), s.url("10.0.0.5", path), s.options())
		wantCode(t, path, err, "too_large")
	}
	img, err := FetchURL(context.Background(), s.url("10.0.0.5", "/fits.png"), s.options())
	if err != nil || len(img.Data) != MaxAvatarBytes {
		t.Fatalf("a picture of exactly the limit: %d bytes, %v", len(img.Data), err)
	}
}

func TestFetchRefusesWhatIsNoPicture(t *testing.T) {
	s := newPictureServer(t)
	for path, code := range map[string]string{
		"/html": "not_an_image", "/svg": "not_an_image", "/lies.png": "not_an_image",
		"/jpeg-as.png": "type_mismatch", "/missing.png": "http_status",
	} {
		_, err := FetchURL(context.Background(), s.url("10.0.0.5", path), s.options())
		wantCode(t, path, err, code)
	}
}

func TestFetchFollowsThreeRedirects(t *testing.T) {
	s := newPictureServer(t)
	if img, err := FetchURL(context.Background(), s.url("10.0.0.5", "/hop/2"), s.options()); err != nil || img.MimeType != TypePNG {
		t.Fatalf("three redirects: %v", err)
	}
	_, err := FetchURL(context.Background(), s.url("10.0.0.5", "/hop/3"), s.options())
	wantCode(t, "four redirects", err, "too_many_redirects")
	none := 0
	opts := s.options()
	opts.MaxRedirects = &none
	_, err = FetchURL(context.Background(), s.url("10.0.0.5", "/hop/0"), opts)
	wantCode(t, "a redirect when none is followed", err, "too_many_redirects")
}

func TestFetchChecksEveryRedirectTarget(t *testing.T) {
	s := newPictureServer(t)
	s.addresses["rebind.example.test"] = []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	for _, path := range []string{"/to-loopback", "/to-metadata", "/to-rebind"} {
		_, err := FetchURL(context.Background(), s.url("10.0.0.5", path), s.options())
		wantCode(t, path, err, "address_refused")
	}
	for _, hit := range s.seen() {
		if hit == "/a.png" {
			t.Fatal("the picture behind a refused redirect was asked for")
		}
	}
}

func TestFetchGivesUpAfterTheTimeout(t *testing.T) {
	s := newPictureServer(t)
	opts := s.options()
	opts.Timeout = 300 * time.Millisecond
	started := time.Now()
	_, err := FetchURL(context.Background(), s.url("10.0.0.5", "/slow.png"), opts)
	wantCode(t, "a body that never ends", err, "timeout")
	if took := time.Since(started); took > 2*time.Second {
		t.Fatalf("the fetch took %v", took)
	}
	// The time is for the whole fetch: it is used up across redirects
	opts.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		time.Sleep(120 * time.Millisecond)
		return (&net.Dialer{}).DialContext(ctx, network, s.Listener.Addr().String())
	}
	_, err = FetchURL(context.Background(), s.url("10.0.0.5", "/hop/2"), opts)
	wantCode(t, "three slow redirects", err, "timeout")
}

func TestLoadPictureKeepsTheSourceAddress(t *testing.T) {
	s := newPictureServer(t)
	address := s.url("10.0.0.5", "/a.png")
	pic, err := LoadPicture(context.Background(), "  "+address+"\n", s.options())
	if err != nil || pic.SourceURL != address || pic.Version != ImageVersion(onePixelPNG) || len(pic.Version) != 16 {
		t.Fatalf("%+v, %v", pic, err)
	}
	pic, err = LoadPicture(context.Background(), pngDataURL, s.options())
	if err != nil || pic.SourceURL != "" || pic.MimeType != TypePNG {
		t.Fatalf("a data URL: %+v, %v", pic, err)
	}
	_, err = LoadPicture(context.Background(), "   ", s.options())
	wantCode(t, "an empty value", err, "empty")
}

// The console's own transport, with nothing replaced, refuses a loopback
// address at the moment it connects.
func TestGuardedDialRefusesWithoutHelp(t *testing.T) {
	s := newPictureServer(t)
	var opts FetchOptions
	_, err := opts.guardedDial(context.Background(), "tcp", s.Listener.Addr().String())
	wantCode(t, "loopback", err, "address_refused")
	_, err = opts.guardedDial(context.Background(), "tcp", "localhost:"+s.port())
	wantCode(t, "localhost", err, "address_refused")
	if len(s.seen()) != 0 {
		t.Fatal("a connection was made")
	}
}

func TestSliceUnits(t *testing.T) {
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 5, "abc"}, {"abc", 3, "abc"}, {"abc", 2, "ab"}, {"abc", 0, ""},
		{"a\U0001F600b", 3, "a\U0001F600"}, {"a\U0001F600b", 2, "a�"}, {"a\U0001F600b", 1, "a"}, {"é€", 1, "é"},
	} {
		if got := sliceUnits(c.in, c.n); got != c.want {
			t.Errorf("sliceUnits(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}
