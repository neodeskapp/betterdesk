package updater

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/agentreleases"
)

const (
	githubAPI = "https://api.github.com"
	// githubSite is where a release's files are linked from.
	githubSite = "github.com"
	// maxRedirects is how many redirects a request follows.
	maxRedirects = 3
	// apiTimeout limits one call of the API (ghGet's 15 seconds).
	apiTimeout = 15 * time.Second
	// idleTimeout ends a download that has been silent this long.
	idleTimeout = 30 * time.Second
	// maxAPIBytes caps an answer of the API.
	maxAPIBytes = 8 << 20
)

// downloadHosts are the hosts a release file may come from or be
// redirected to: the site and the hosts GitHub serves release files from
// (the list of console/agentreleases).
var downloadHosts = func() map[string]bool {
	hosts := map[string]bool{}
	for _, host := range agentreleases.GithubHosts() {
		if host != "api.github.com" {
			hosts[host] = true
		}
	}
	return hosts
}()

// GitHubError is an answer of the API with an error status.
type GitHubError struct {
	Status int
	// RateLimited is a 403 or 429 for an exhausted rate limit.
	RateLimited bool
	Message     string
}

func (e *GitHubError) Error() string { return e.Message }

// IsRateLimited reports an exhausted GitHub rate limit
// (isGithubRateLimitError).
func IsRateLimited(err error) bool {
	var gh *GitHubError
	if errors.As(err, &gh) && gh.RateLimited {
		return true
	}
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "rate limit exceeded")
}

// githubClient talks to GitHub: the API for releases and comparisons,
// and the release files. Answers of the API are kept for the cache time.
type githubClient struct {
	transport http.RoundTripper
	userAgent string
	token     string
	cacheTTL  time.Duration
	now       func() time.Time

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	expires time.Time
	body    []byte
}

// apiGet fetches path (below https://api.github.com) and returns the body
// of a 2xx answer. Redirects are followed to the API host only, three at
// most. An answer of 400 or more becomes a *GitHubError with the old
// console's text.
func (g *githubClient) apiGet(ctx context.Context, path string, bypassCache bool) ([]byte, error) {
	key := path
	if !bypassCache && g.cacheTTL > 0 {
		g.mu.Lock()
		entry, ok := g.cache[key]
		g.mu.Unlock()
		if ok && entry.expires.After(g.now()) {
			return entry.body, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	target, err := url.Parse(githubAPI + path)
	if err != nil || target.Host != "api.github.com" {
		return nil, errors.New("Invalid GitHub URL")
	}
	for redirects := 0; ; redirects++ {
		res, err := g.send(ctx, target, "application/vnd.github+json", true)
		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, errors.New("GitHub API timeout")
			}
			return nil, err
		}
		if isRedirect(res.StatusCode) {
			res.Body.Close()
			location := res.Header.Get("Location")
			if location == "" || redirects >= maxRedirects {
				return nil, errors.New("GitHub API redirect limit exceeded")
			}
			next, err := target.Parse(location)
			if err != nil || !plainHTTPS(next) || !strings.EqualFold(next.Host, "api.github.com") {
				return nil, errors.New("GitHub URL host is not allowed: api.github.com")
			}
			target = next
			continue
		}
		body, tooLarge, err := readLimited(res.Body, maxAPIBytes)
		res.Body.Close()
		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, errors.New("GitHub API timeout")
			}
			return nil, fmt.Errorf("read GitHub answer: %w", err)
		}
		if res.StatusCode >= 400 {
			return nil, g.apiError(res.StatusCode, body, res.Header)
		}
		if tooLarge {
			return nil, errors.New("GitHub API answer is too large")
		}
		if g.cacheTTL > 0 {
			g.mu.Lock()
			g.cache[key] = cached{expires: g.now().Add(g.cacheTTL), body: body}
			g.mu.Unlock()
		}
		return body, nil
	}
}

// apiError is githubApiError: the rate limit with its reset time and a
// hint about the token, or the status and the start of the body.
func (g *githubClient) apiError(status int, body []byte, header http.Header) error {
	text := string(body)
	if (status == http.StatusForbidden || status == http.StatusTooManyRequests) &&
		(strings.Contains(strings.ToLower(text), "rate limit") || header.Get("X-Ratelimit-Remaining") == "0") {
		hint := "GitHub API rate limit exceeded for unauthenticated requests (60/hour). Set UPDATE_GITHUB_TOKEN in the console .env — a read-only Personal Access Token raises the limit to 5,000/hour."
		if g.token != "" {
			hint = "GitHub API rate limit exceeded for the configured token."
		}
		if reset, err := strconv.ParseInt(header.Get("X-Ratelimit-Reset"), 10, 64); err == nil {
			hint += " Resets at " + time.Unix(reset, 0).UTC().Format("2006-01-02T15:04:05.000Z") + "."
		}
		return &GitHubError{Status: status, RateLimited: true, Message: hint}
	}
	return &GitHubError{Status: status, Message: fmt.Sprintf("GitHub API %d: %s", status, cut(text, 200))}
}

// cut is the first n UTF-16 units of s (String.prototype.slice).
func cut(s string, n int) string {
	count := 0
	for i, r := range s {
		width := 1
		if r > 0xFFFF {
			width = 2
		}
		if count+width > n {
			return s[:i]
		}
		count += width
	}
	return s
}

// download fetches a release file into w. address must be the file's
// address on the GitHub site for this release: https://github.com/<owner>/
// <repo>/releases/download/<tag>/<name>. Redirects (three at most) may
// go to the site and to GitHub's file hosts only, over HTTPS on the
// default port. The body may be at most limit bytes, and the download
// ends when it has been silent for 30 seconds. The token is never sent:
// release files of a public repository need none.
func (g *githubClient) download(ctx context.Context, address string, want releaseFile, limit int64, w io.Writer) (int64, error) {
	target, err := url.Parse(address)
	if err != nil || !plainHTTPS(target) || !strings.EqualFold(target.Host, githubSite) ||
		target.RawQuery != "" || target.Fragment != "" || !want.matches(target.EscapedPath()) {
		return 0, fmt.Errorf("the address of %s is not a file of the release on github.com", want.name)
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	for redirects := 0; ; redirects++ {
		res, err := g.send(ctx, target, "application/octet-stream", false)
		if err != nil {
			if cause := context.Cause(ctx); errors.Is(cause, errIdle) {
				return 0, fmt.Errorf("download %s: %w", want.name, errIdle)
			}
			return 0, fmt.Errorf("download %s: %w", want.name, err)
		}
		if isRedirect(res.StatusCode) {
			res.Body.Close()
			location := res.Header.Get("Location")
			if location == "" || redirects >= maxRedirects {
				return 0, fmt.Errorf("download %s: too many redirects", want.name)
			}
			next, err := target.Parse(location)
			if err != nil || !plainHTTPS(next) || !downloadHosts[strings.ToLower(next.Host)] {
				return 0, fmt.Errorf("download %s: redirect to a host that is not GitHub's", want.name)
			}
			target = next
			continue
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return 0, fmt.Errorf("download %s: HTTP %d", want.name, res.StatusCode)
		}
		if res.ContentLength > limit {
			return 0, fmt.Errorf("download %s: %w (%d bytes, the limit is %d)", want.name, errTooLarge, res.ContentLength, limit)
		}
		timer := time.AfterFunc(idleTimeout, func() { cancel(errIdle) })
		defer timer.Stop()
		n, err := copyLimited(w, res.Body, limit, func() { timer.Reset(idleTimeout) })
		if err != nil {
			if cause := context.Cause(ctx); errors.Is(cause, errIdle) {
				return n, fmt.Errorf("download %s: %w", want.name, errIdle)
			}
			return n, fmt.Errorf("download %s: %w", want.name, err)
		}
		return n, nil
	}
}

var errIdle = errors.New("no data for 30 seconds")

// errTooLarge is a download longer than its limit.
var errTooLarge = errors.New("larger than allowed")

// copyLimited copies at most limit bytes and fails when there are more.
func copyLimited(w io.Writer, r io.Reader, limit int64, progress func()) (int64, error) {
	buf := make([]byte, 64<<10)
	var total int64
	for {
		n, err := r.Read(buf)
		if n > 0 {
			progress()
			total += int64(n)
			if total > limit {
				return total, errTooLarge
			}
			if _, werr := w.Write(buf[:n]); werr != nil {
				return total, werr
			}
		}
		if errors.Is(err, io.EOF) {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

func readLimited(r io.Reader, limit int64) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}

// send makes one GET request without following redirects. withToken adds
// the token, for the API host only.
func (g *githubClient) send(ctx context.Context, target *url.URL, accept string, withToken bool) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, errors.New("Invalid GitHub URL")
	}
	req.Header.Set("User-Agent", g.userAgent)
	req.Header.Set("Accept", accept)
	if withToken && g.token != "" && strings.EqualFold(target.Host, "api.github.com") {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	return g.transport.RoundTrip(req)
}

func isRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// plainHTTPS is an https address without user, password or port.
func plainHTTPS(u *url.URL) bool {
	return u.Scheme == "https" && u.User == nil && u.Port() == "" && u.Hostname() != ""
}

// releaseFile names a file of one release, for the check of its address.
type releaseFile struct {
	owner, repo, tag, name string
}

// matches reports whether an escaped path is /<owner>/<repo>/releases/
// download/<tag>/<name>; owner and repository in any case, as GitHub
// takes them.
func (f releaseFile) matches(escapedPath string) bool {
	parts := strings.Split(escapedPath, "/")
	return len(parts) == 7 && parts[0] == "" &&
		strings.EqualFold(parts[1], f.owner) && strings.EqualFold(parts[2], f.repo) &&
		parts[3] == "releases" && parts[4] == "download" &&
		parts[5] == url.PathEscape(f.tag) && parts[6] == url.PathEscape(f.name)
}
