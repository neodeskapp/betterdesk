package generator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/ssrfguard"
)

// Requests to GitHub. The template module fetches from four GitHub hosts
// only, over https, and follows up to eight redirects among them; the
// client builder asks api.github.com and follows the redirects of an
// artifact download over https to hosts the outbound guard lets through.
// The token (NEODESK_GITHUB_TOKEN, else GITHUB_TOKEN) goes to
// api.github.com only. Requests carry no Accept-Encoding, and nothing
// here logs a token.

// githubFetchHosts are the hosts the template module may fetch from.
var githubFetchHosts = map[string]bool{
	"api.github.com":                       true,
	"github.com":                           true,
	"objects.githubusercontent.com":        true,
	"release-assets.githubusercontent.com": true,
}

// defaultTransport is http.DefaultTransport without transparent
// compression, which would add Accept-Encoding to every request, and
// with the connections of a guarded download pinned to the addresses the
// guard checked.
var defaultTransport = sync.OnceValue(func() http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DisableCompression = true
	t.DialContext = dialChecked(t.DialContext)
	return t
})

func (s *Service) transport() http.RoundTripper {
	if s.opts.HTTP == http.DefaultTransport {
		return defaultTransport()
	}
	return s.opts.HTTP
}

func (s *Service) githubToken() string {
	token := s.getenv("NEODESK_GITHUB_TOKEN")
	if token == "" {
		token = s.getenv("GITHUB_TOKEN")
	}
	return jsTrim(token)
}

// allowedFetch is a URL the template module may fetch: the host from the
// list, the path and query as the URL parser wrote them, no port.
type allowedFetch struct {
	host, path, href string
}

func assertAllowedGithubFetchURL(raw string) (allowedFetch, error) {
	u, ok := ssrfguard.ParseURL(raw)
	if !ok {
		return allowedFetch{}, codeError("invalid_github_fetch_url")
	}
	if u.Protocol != "https:" {
		return allowedFetch{}, codeError("github_fetch_requires_https")
	}
	if u.Username != "" || u.Password != "" {
		return allowedFetch{}, codeError("github_fetch_url_must_not_contain_credentials")
	}
	host := strings.ToLower(u.Hostname)
	if !githubFetchHosts[host] {
		return allowedFetch{}, codeError("github_fetch_host_not_allowed:" + u.Hostname)
	}
	p := u.Pathname
	if p == "" {
		p = "/"
	}
	p += u.Search
	return allowedFetch{host: host, path: p, href: "https://" + host + p}, nil
}

// resolveLocation is new URL(location, base).href.
func resolveLocation(location, base string) (string, error) {
	if u, ok := ssrfguard.ParseURL(location); ok && u.Href != "" {
		return u.Href, nil
	}
	b, err := url.Parse(base)
	if err != nil {
		return "", codeError("Invalid URL")
	}
	ref, err := url.Parse(location)
	if err != nil {
		return "", codeError("Invalid URL")
	}
	resolved := b.ResolveReference(ref).String()
	if u, ok := ssrfguard.ParseURL(resolved); ok && u.Href != "" {
		return u.Href, nil
	}
	return resolved, nil
}

// requestTarget builds the request for a URL whose path and query go out
// exactly as given.
func requestTarget(ctx context.Context, method, scheme, host, pathAndQuery string, body io.Reader) (*http.Request, error) {
	p, q, _ := strings.Cut(pathAndQuery, "?")
	u := &url.URL{Scheme: scheme, Host: host, Opaque: "//" + host + p, RawQuery: q}
	req, err := http.NewRequestWithContext(ctx, method, "", body)
	if err != nil {
		return nil, err
	}
	req.URL = u
	req.Host = host
	return req, nil
}

// idleTimeout ends a request when no byte arrived for the duration, as
// a socket timeout did in Node.
type idleTimeout struct {
	timer *time.Timer
	d     time.Duration
	fired chan struct{}
}

func newIdleTimeout(ctx context.Context, d time.Duration) (context.Context, *idleTimeout, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	it := &idleTimeout{d: d, fired: make(chan struct{})}
	it.timer = time.AfterFunc(d, func() {
		close(it.fired)
		cancel()
	})
	return ctx, it, func() {
		it.timer.Stop()
		cancel()
	}
}

func (it *idleTimeout) touch() { it.timer.Reset(it.d) }

func (it *idleTimeout) expired() bool {
	select {
	case <-it.fired:
		return true
	default:
		return false
	}
}

// readAll reads a body and keeps the idle timeout alive while bytes come.
func (it *idleTimeout) readAll(body io.Reader, limit int64) ([]byte, int64, error) {
	var buf bytes.Buffer
	chunk := make([]byte, 64*1024)
	var size int64
	for {
		n, err := body.Read(chunk)
		if n > 0 {
			it.touch()
			size += int64(n)
			if limit <= 0 || size <= limit {
				buf.Write(chunk[:n])
			}
		}
		if err == io.EOF {
			return buf.Bytes(), size, nil
		}
		if err != nil {
			return nil, size, err
		}
	}
}

// githubGetBuffer is the template module's download: an allowed URL,
// redirects followed among allowed URLs, 200 or an error that names the
// status.
func (s *Service) githubGetBuffer(ctx context.Context, rawURL, accept string) ([]byte, error) {
	if accept == "" {
		accept = "application/octet-stream, application/json"
	}
	target := rawURL
	for redirects := 0; ; redirects++ {
		if redirects > 8 {
			return nil, codeError("too many redirects")
		}
		validated, err := assertAllowedGithubFetchURL(target)
		if err != nil {
			return nil, err
		}
		reqCtx, idle, stop := newIdleTimeout(ctx, 120*time.Second)
		req, err := requestTarget(reqCtx, http.MethodGet, "https", validated.host, validated.path, nil)
		if err != nil {
			stop()
			return nil, err
		}
		req.Header.Set("User-Agent", "NeoDesk-Console-Generator")
		req.Header.Set("Accept", accept)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if token := s.githubToken(); token != "" && validated.host == "api.github.com" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := s.transport().RoundTrip(req)
		if err != nil {
			stop()
			if idle.expired() {
				return nil, codeError("download timeout")
			}
			return nil, networkError(err, validated.host, 443)
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 && resp.Header.Get("Location") != "" {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			stop()
			next, err := resolveLocation(resp.Header.Get("Location"), validated.href)
			if err != nil {
				return nil, err
			}
			if _, err := assertAllowedGithubFetchURL(next); err != nil {
				return nil, err
			}
			target = next
			continue
		}
		if resp.StatusCode != http.StatusOK {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			stop()
			return nil, &CodedError{Message: fmt.Sprintf("HTTP %d for %s", resp.StatusCode, validated.href), StatusCode: resp.StatusCode}
		}
		body, _, err := idle.readAll(resp.Body, 0)
		resp.Body.Close()
		expired := idle.expired()
		stop()
		if err != nil {
			if expired {
				return nil, codeError("download timeout")
			}
			return nil, codeError("aborted")
		}
		return body, nil
	}
}

// githubGetJSON fetches JSON for the template module.
func (s *Service) githubGetJSON(ctx context.Context, rawURL string) (any, error) {
	body, err := s.githubGetBuffer(ctx, rawURL, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	v, err := parseJSON(decodeUTF8(body))
	if err != nil {
		return nil, syntaxError(decodeUTF8(body))
	}
	return v, nil
}

// syntaxError words a JSON.parse failure as V8 does for the start of the
// text.
func syntaxError(text string) error {
	if jsTrim(text) == "" {
		return codeError("Unexpected end of JSON input")
	}
	return codeError("Unexpected token in JSON")
}

// networkError words a failed connection as Node did, where it can.
func networkError(err error, host string, port int) error {
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &dnsErr):
		if dnsErr.IsNotFound {
			return codeError("getaddrinfo ENOTFOUND " + host)
		}
		return codeError("getaddrinfo EAI_AGAIN " + host)
	case errors.Is(err, syscall.ECONNREFUSED):
		var opErr *net.OpError
		if errors.As(err, &opErr) && opErr.Addr != nil {
			return codeError("connect ECONNREFUSED " + opErr.Addr.String())
		}
		return codeError("connect ECONNREFUSED " + net.JoinHostPort(host, strconv.Itoa(port)))
	case errors.Is(err, syscall.ECONNRESET):
		return codeError("read ECONNRESET")
	}
	return err
}

// The client builder's requests.

const maxGithubResponse = 64 * 1024 * 1024

func (s *Service) builderHeaders(h http.Header, accept, requestURL string) {
	h.Set("User-Agent", "NeoDesk-Console-Client-Builder")
	h.Set("Accept", accept)
	h.Set("X-GitHub-Api-Version", "2022-11-28")
	host := ""
	if u, ok := ssrfguard.ParseURL(requestURL); ok {
		host = strings.ToLower(u.Hostname)
	}
	if token := s.githubToken(); token != "" && host == "api.github.com" {
		h.Set("Authorization", "Bearer "+token)
	}
}

// builderRequest sends one request of the client builder: http or https
// by the URL's first letters, as the old code picked its module.
func (s *Service) builderRequest(ctx context.Context, method, rawURL string, body []byte, accept, tokenURL string, timeout time.Duration) (*http.Response, *idleTimeout, context.CancelFunc, error) {
	u, ok := ssrfguard.ParseURL(rawURL)
	if !ok {
		return nil, nil, nil, codeError("Invalid URL")
	}
	scheme := "http"
	if strings.HasPrefix(rawURL, "https:") {
		scheme = "https"
	}
	if u.Protocol != scheme+":" {
		return nil, nil, nil, codeError(`Protocol "` + u.Protocol + `" not supported. Expected "` + scheme + `:"`)
	}
	host := u.Hostname
	if u.Port != "" {
		host = net.JoinHostPort(strings.Trim(u.Hostname, "[]"), u.Port)
	}
	reqCtx, idle, stop := newIdleTimeout(ctx, timeout)
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	pathname := u.Pathname
	if pathname == "" {
		pathname = "/"
	}
	req, err := requestTarget(reqCtx, method, scheme, host, pathname+u.Search, reader)
	if err != nil {
		stop()
		return nil, nil, nil, err
	}
	s.builderHeaders(req.Header, accept, tokenURL)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Length", strconv.Itoa(len(body)))
		req.ContentLength = int64(len(body))
	}
	resp, err := s.transport().RoundTrip(req)
	if err != nil {
		stop()
		if idle.expired() {
			return nil, idle, nil, errTimeout
		}
		port := 80
		if scheme == "https" {
			port = 443
		}
		return nil, nil, nil, networkError(err, u.Hostname, port)
	}
	return resp, idle, stop, nil
}

var errTimeout = errors.New("timeout")

// githubJSON is the builder's GET of a JSON document from the API.
func (s *Service) githubJSON(ctx context.Context, rawURL string) (any, error) {
	resp, idle, stop, err := s.builderRequest(ctx, http.MethodGet, rawURL, nil, "application/vnd.github+json", "https://api.github.com", 30*time.Second)
	if err == errTimeout {
		return nil, codeError("github_request_timeout")
	}
	if err != nil {
		return nil, err
	}
	defer stop()
	data, size, err := idle.readAll(resp.Body, maxGithubResponse)
	resp.Body.Close()
	if err != nil {
		if idle.expired() {
			return nil, codeError("github_request_timeout")
		}
		return nil, codeError("aborted")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &CodedError{Message: fmt.Sprintf("GitHub HTTP %d", resp.StatusCode), StatusCode: resp.StatusCode}
	}
	if size > maxGithubResponse {
		return nil, codeError("github_response_too_large")
	}
	v, perr := parseJSON(decodeUTF8(data))
	if perr != nil {
		return nil, codeError("github_json_invalid:" + syntaxError(decodeUTF8(data)).Error())
	}
	return v, nil
}

// githubRequest is the builder's call with a JSON body (the workflow
// dispatch). An empty answer is nil.
func (s *Service) githubRequest(ctx context.Context, method, rawURL string, body any) (any, error) {
	var payload []byte
	if body != nil {
		payload = []byte(dbText(stringify(body)))
	}
	resp, idle, stop, err := s.builderRequest(ctx, method, rawURL, payload, "application/vnd.github+json", "https://api.github.com", 30*time.Second)
	if err == errTimeout {
		return nil, codeError("github_request_timeout")
	}
	if err != nil {
		return nil, err
	}
	defer stop()
	data, _, err := idle.readAll(resp.Body, maxGithubResponse)
	resp.Body.Close()
	if err != nil {
		return nil, codeError("aborted")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &CodedError{Message: fmt.Sprintf("GitHub HTTP %d", resp.StatusCode), StatusCode: resp.StatusCode}
	}
	if len(data) == 0 {
		return nil, nil
	}
	if v, err := parseJSON(decodeUTF8(data)); err == nil {
		return v, nil
	}
	return data, nil
}

// downloadToFile is the builder's download of an artifact, following up
// to eight redirects. Every address it fetches, the first and each
// redirect's, must be https and pass the outbound guard (no loopback,
// link-local or private address), and the connection goes to an address
// the guard checked (dialChecked).
func (s *Service) downloadToFile(ctx context.Context, rawURL, destination string) error {
	guard := &ssrfguard.Guard{Lookup: s.opts.Lookup}
	target := rawURL
	for redirects := 0; ; redirects++ {
		if redirects > 8 {
			return codeError("too_many_redirects")
		}
		checked, err := checkDownload(ctx, guard, target)
		if err != nil {
			return err
		}
		resp, idle, stop, err := s.builderRequest(context.WithValue(ctx, checkedHost{}, checked), http.MethodGet, target, nil, "application/octet-stream", target, 120*time.Second)
		if err == errTimeout {
			return codeError("github_download_timeout")
		}
		if err != nil {
			return err
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 && resp.Header.Get("Location") != "" {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			stop()
			target = resp.Header.Get("Location")
			continue
		}
		if resp.StatusCode != http.StatusOK {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			stop()
			return codeError(fmt.Sprintf("GitHub HTTP %d", resp.StatusCode))
		}
		out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			resp.Body.Close()
			stop()
			return nodeFSError(err, "open", destination)
		}
		_, copyErr := io.Copy(out, &touchReader{r: resp.Body, idle: idle})
		closeErr := out.Close()
		resp.Body.Close()
		expired := idle.expired()
		stop()
		switch {
		case copyErr != nil && expired:
			return codeError("github_download_timeout")
		case copyErr != nil:
			return copyErr
		case closeErr != nil:
			return closeErr
		}
		return nil
	}
}

// checkDownload checks an address of an artifact download: https, and a
// host the outbound guard lets through. It returns the host as checked.
func checkDownload(ctx context.Context, guard *ssrfguard.Guard, raw string) (ssrfguard.Host, error) {
	u, ok := ssrfguard.ParseURL(raw)
	if !ok {
		return ssrfguard.Host{}, codeError("Invalid URL")
	}
	if u.Protocol != "https:" {
		return ssrfguard.Host{}, codeError("github_download_requires_https")
	}
	_, checked, err := guard.CheckHTTPURL(ctx, raw, ssrfguard.Options{})
	return checked, err
}

// checkedHost is the key of the host the outbound guard checked for a
// request, in the request's context.
type checkedHost struct{}

type dialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// dialChecked connects a request whose context carries a checked host to
// that host's checked addresses only, one after the other, since the name
// looked up again could answer with another address. Any other
// connection, also one to a proxy, goes through base as given.
func dialChecked(base dialFunc) dialFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		checked, ok := ctx.Value(checkedHost{}).(ssrfguard.Host)
		host, port, err := net.SplitHostPort(address)
		if !ok || err != nil || !strings.EqualFold(host, checked.Name) {
			return base(ctx, network, address)
		}
		lastErr := error(&ssrfguard.LookupError{Code: "ENOTFOUND", Host: host})
		for _, a := range checked.Addresses {
			conn, err := base(ctx, network, net.JoinHostPort(a, port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

type touchReader struct {
	r    io.Reader
	idle *idleTimeout
}

func (t *touchReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		t.idle.touch()
	}
	return n, err
}
