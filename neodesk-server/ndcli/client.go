package ndcli

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// conn is where the commands go and with what token.
type conn struct {
	url, token string
	// tokenSource says where the token came from, for messages.
	tokenSource string
	client      *http.Client
	verbose     io.Writer
	userAgent   string
}

// connError is a problem with the address, the token or the config file,
// with its exit status.
type connError struct {
	code int
	msg  string
}

func (e *connError) Error() string { return e.msg }

// connect works out the address and the token: flags first, then the
// environment (NEODESK_URL, NEODESK_TOKEN), then the config file. A token
// from the config file is used only with the address it was saved for, so
// another address (a flag, the environment) never gets it; and a token
// from a flag or the environment only with an address from a flag or the
// environment, so a config file (--config, NEODESK_CONFIG) never sends it
// elsewhere.
func connect(o *options, env *Env, needToken bool) (*conn, error) {
	path, err := env.configPath(o.config)
	if err != nil {
		return nil, &connError{ExitConfig, err.Error()}
	}
	cfg, err := loadConfig(path)
	if err != nil {
		return nil, &connError{ExitConfig, err.Error()}
	}
	rawURL, urlFromConfig := o.url, false
	if rawURL == "" {
		rawURL = env.Getenv("NEODESK_URL")
	}
	if rawURL == "" {
		rawURL, urlFromConfig = cfg.URL, true
	}
	address, err := normalizeURL(rawURL)
	if err != nil {
		return nil, &connError{ExitUsage, err.Error()}
	}
	token, source := o.token, "--token"
	if token == "" && o.tokenFile != "" {
		data, err := os.ReadFile(o.tokenFile)
		if err != nil {
			return nil, &connError{ExitConfig, fmt.Sprintf("read the token file: %v", err)}
		}
		token, source = strings.TrimSpace(string(data)), "--token-file"
	}
	if token == "" {
		token, source = strings.TrimSpace(env.Getenv("NEODESK_TOKEN")), "NEODESK_TOKEN"
	}
	if token == "" && cfg.Token != "" {
		saved, _ := normalizeURL(cfg.URL)
		if !urlFromConfig && saved != address {
			return nil, &connError{ExitAuth, fmt.Sprintf("the saved token belongs to %s, not %s. Pass a token for %s with NEODESK_TOKEN or --token-file, or run neodesk login --url %s", saved, address, address, address)}
		}
		token, source = cfg.Token, "the config file"
	}
	if token != "" && source != "the config file" && urlFromConfig {
		return nil, &connError{ExitUsage, fmt.Sprintf("the token comes from %s but the address from the config file: give the address too, with --url or NEODESK_URL", source)}
	}
	if needToken && token == "" {
		return nil, &connError{ExitAuth, "no API token: run neodesk login, or set NEODESK_TOKEN. Make a token in the console under your account menu, API tokens"}
	}
	if token != "" && !strings.HasPrefix(token, "ndt_") {
		return nil, &connError{ExitAuth, "that is no NeoDesk API token (they start with ndt_)"}
	}
	insecure := o.insecure
	caCert := o.caCert
	if caCert == "" {
		caCert = env.Getenv("NEODESK_CA_CERT")
	}
	if caCert == "" && urlFromConfig {
		caCert = cfg.CACert
	}
	allowHTTP := o.allowHTTP || env.Getenv("NEODESK_ALLOW_HTTP") == "1" || (urlFromConfig && cfg.AllowHTTP)
	if strings.HasPrefix(address, "http://") && token != "" && !allowHTTP && !loopback(address) {
		return nil, &connError{ExitUsage, fmt.Sprintf("%s is plain HTTP: the token would cross the network unencrypted. Use https, or pass --allow-http if this network is yours", address)}
	}
	// A proxy would see the token when the connection is not checked
	direct := insecure || strings.HasPrefix(address, "http://")
	client, err := httpClient(env, insecure, direct, caCert, o.timeout)
	if err != nil {
		return nil, &connError{ExitConfig, err.Error()}
	}
	c := &conn{url: address, token: token, tokenSource: source, client: client, userAgent: "neodesk-cli/" + env.Version}
	if o.verbose {
		c.verbose = env.Stderr
	}
	if insecure {
		fmt.Fprintln(env.Stderr, "neodesk: warning: the console's certificate is not checked (--insecure)")
	}
	return c, nil
}

// httpClient makes the client for the console. direct leaves out any
// proxy from the environment (HTTPS_PROXY, HTTP_PROXY).
func httpClient(env *Env, insecure, direct bool, caCert string, timeout time.Duration) (*http.Client, error) {
	transport := env.Transport
	if transport == nil {
		base := http.DefaultTransport.(*http.Transport).Clone()
		base.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		if caCert != "" {
			pem, err := os.ReadFile(caCert)
			if err != nil {
				return nil, fmt.Errorf("read the CA certificate: %v", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("%s holds no PEM certificate", caCert)
			}
			base.TLSClientConfig.RootCAs = pool
		}
		if insecure {
			base.TLSClientConfig.InsecureSkipVerify = true
		}
		// A proxy from the environment sees only an encrypted, checked
		// connection; plain HTTP and --insecure go direct
		if direct {
			base.Proxy = nil
		}
		transport = base
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		// A redirect could send the token elsewhere: none is followed
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

// request is one call of the API.
type request struct {
	method, path string
	query        url.Values
	body         any
	upload       *upload
}

type upload struct {
	field, path string
	fields      map[string]string
}

// response is an answer of the API.
type response struct {
	status      int
	contentType string
	body        []byte
	header      http.Header
}

// isJSON reports whether the answer is JSON.
func (r *response) isJSON() bool {
	return strings.Contains(r.contentType, "json")
}

// do sends a request and reads the whole answer.
func (c *conn) do(ctx context.Context, req request) (*response, error) {
	target := c.url + req.path
	if len(req.query) > 0 {
		target += "?" + req.query.Encode()
	}
	var body io.Reader
	contentType := ""
	switch {
	case req.upload != nil:
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		for k, v := range req.upload.fields {
			if err := w.WriteField(k, v); err != nil {
				return nil, err
			}
		}
		f, err := os.Open(req.upload.path)
		if err != nil {
			return nil, &connError{ExitUsage, fmt.Sprintf("open %s: %v", req.upload.path, err)}
		}
		part, err := w.CreateFormFile(req.upload.field, filepath.Base(req.upload.path))
		if err == nil {
			_, err = io.Copy(part, f)
		}
		f.Close()
		if err != nil {
			return nil, err
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		body, contentType = &buf, w.FormDataContentType()
	case req.body != nil:
		data, err := json.Marshal(req.body)
		if err != nil {
			return nil, err
		}
		body, contentType = bytes.NewReader(data), "application/json"
	case req.method != http.MethodGet && req.method != http.MethodHead && req.method != http.MethodDelete:
		body, contentType = strings.NewReader("{}"), "application/json"
	}
	httpReq, err := http.NewRequestWithContext(ctx, req.method, target, body)
	if err != nil {
		return nil, &connError{ExitUsage, err.Error()}
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", c.userAgent)
	if contentType != "" {
		httpReq.Header.Set("Content-Type", contentType)
	}
	if c.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.token)
	}
	start := time.Now()
	res, err := c.client.Do(httpReq)
	if err != nil {
		return nil, &connError{ExitNetwork, fmt.Sprintf("cannot reach %s: %v", c.url, cleanNetError(err))}
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 512<<20))
	if err != nil {
		return nil, &connError{ExitNetwork, fmt.Sprintf("reading the answer: %v", err)}
	}
	if c.verbose != nil {
		fmt.Fprintf(c.verbose, "> %s %s\n< %d %s (%d bytes, %s)\n", req.method, redactQuery(target), res.StatusCode, res.Header.Get("Content-Type"), len(data), time.Since(start).Round(time.Millisecond))
	}
	return &response{status: res.StatusCode, contentType: res.Header.Get("Content-Type"), body: data, header: res.Header}, nil
}

// cleanNetError keeps the cause of a network error without the request
// line, which Go puts in front.
func cleanNetError(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}

// redactQuery hides values of query keys that look secret in a logged
// address.
func redactQuery(target string) string {
	u, err := url.Parse(target)
	if err != nil {
		return target
	}
	q := u.Query()
	for k := range q {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "token") || strings.Contains(lk, "password") || strings.Contains(lk, "secret") || strings.Contains(lk, "key") {
			q.Set(k, "…")
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// apiError is a refusal of the server, with its exit status.
type apiError struct {
	status int
	msg    string
	code   string
}

func (e *apiError) Error() string { return e.msg }

// exitFor is the exit status for an HTTP status.
func exitFor(status int) int {
	switch {
	case status == 401:
		return ExitAuth
	case status == 403:
		return ExitForbidden
	case status == 404:
		return ExitNotFound
	case status == 400 || status == 409 || status == 413 || status == 422:
		return ExitRejected
	case status == 429:
		return ExitRateLimited
	case status >= 500:
		return ExitServer
	case status >= 300 && status < 400:
		return ExitFailed
	}
	return ExitFailed
}

// failure reads the error of an answer: the JSON's error or message, or
// the start of the text.
func (r *response) failure() *apiError {
	e := &apiError{status: r.status}
	if r.isJSON() {
		var body map[string]any
		if json.Unmarshal(r.body, &body) == nil {
			for _, key := range []string{"error", "message", "msg"} {
				if s, ok := body[key].(string); ok && s != "" {
					e.msg = s
					break
				}
				if obj, ok := body[key].(map[string]any); ok {
					if s, ok := obj["message"].(string); ok {
						e.msg = s
						break
					}
				}
			}
			e.code, _ = body["code"].(string)
		}
	}
	if e.msg == "" {
		text := strings.TrimSpace(string(r.body))
		if len(text) > 200 || strings.HasPrefix(text, "<") {
			text = ""
		}
		e.msg = text
	}
	if e.msg == "" {
		e.msg = http.StatusText(r.status)
	}
	if r.status >= 300 && r.status < 400 {
		e.msg = "the console answered with a redirect to " + r.header.Get("Location") + "; check the address (--url)"
	}
	return e
}

// ok reports whether the answer is a success: a 2xx status, and for JSON
// without "success": false.
func (r *response) ok() bool {
	if r.status < 200 || r.status > 299 {
		return false
	}
	if r.isJSON() {
		var body map[string]any
		if json.Unmarshal(r.body, &body) == nil {
			if s, ok := body["success"].(bool); ok && !s {
				return false
			}
		}
	}
	return true
}

func statusText(status int) string { return strconv.Itoa(status) + " " + http.StatusText(status) }
