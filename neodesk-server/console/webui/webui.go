// Package webui serves the console's web app: the build of web-ui.
//
// A Host answers three kinds of requests, at the base the build was made
// for ("" for the root unless the build says otherwise):
//
//   - Hashed files under <base>/_ui/immutable/ are cached for a year and
//     sent precompressed (brotli or gzip) when the browser accepts it.
//   - The other files of the build (version.json, fonts, images) are cached
//     for an hour. An .html file is never sent as a file.
//   - Every other GET gets the app's page. Console pages need a session;
//     someone signed out is sent to the sign-in page with the way back. The
//     sign-in pages, the portal, device links, the invitation page and the
//     remote viewer open without one. An address no page of the app has (routes.json in the
//     build) gets the page with status 404, and the app shows its not-found
//     page there. /api and /ws never get the page.
//
// With the build at the root, the console's old addresses under /app answer
// 301 with the same address at the root.
//
// The page's Content-Security-Policy comes from the build: SvelteKit writes
// the hashes of its inline scripts into a <meta> tag, and the header
// carries the same policy plus the directives a <meta> tag cannot set.
//
// The server's routes come first. What they do not answer goes to Handle,
// and what Handle does not answer goes to NotFound:
//
//	if !host.Handle(w, r) {
//	    host.NotFound(w, r)
//	}
//
// Handle reads the request line as the client sent it (r.RequestURI), so
// it must not sit behind something that cleans or redirects paths, such as
// http.ServeMux with a "/" pattern.
//
// The build is the one in the binary (Options.Embedded), which
// cmd/embed-webui copies in before go build, or a build directory on disk
// (Options.Dir, from WEB_UI_DIR, for development). Both answer the same
// for the same build: every embedded file keeps the modification time it
// had on disk, and with it its Last-Modified and ETag. A binary built
// without the step has no build in it.
package webui

import (
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
)

// oldBase is where the console lived while old and new pages ran side by
// side.
const oldBase = "/app"

// Options configure a Host.
type Options struct {
	// Embedded serves the build in the binary; Dir is then not read.
	Embedded bool
	// Dir is a build directory on disk. Empty (and Embedded false) means no
	// build.
	Dir string
	// Base is the URL prefix the app is served under: "" for the root, or a
	// path such as "/console". nil means the base the build was made for,
	// and the root without a build.
	Base *string
	// HTTPS says the console runs with HTTPS on. Pages then carry
	// upgrade-insecure-requests.
	HTTPS bool
	// Production turns off following the build while the server runs
	// (index.html and routes.json are read once) and hides error detail.
	Production bool
	// Session says who is signed in on a request: whether there is a
	// session with an account, and that account's role. nil means nobody
	// ever is.
	Session func(r *http.Request) (signedIn bool, role string)
	// Secure says whether a request came over HTTPS, counting a trusted
	// proxy. nil means the connection's own TLS state. Only the hint in the
	// CSRF error depends on it.
	Secure func(r *http.Request) bool
	// ClientIP is the client's address as TRUST_PROXY gives it, for the
	// log. nil means the connection's address.
	ClientIP func(r *http.Request) string
}

// Host serves one build of the web app.
type Host struct {
	dir        string
	base       string
	https      bool
	production bool
	session    func(r *http.Request) (bool, string)
	secure     func(r *http.Request) bool
	clientIP   func(r *http.Request) string
	files      fileSystem

	mu   sync.Mutex
	page *page
}

// New makes a Host for the build in the binary or in opts.Dir. A missing
// build is not an error: the Host then answers no pages and no files, and
// for a directory with Options.Production off it starts to as soon as a
// build appears there.
func New(opts Options) *Host {
	if opts.Embedded {
		build, err := embedded()
		return newEmbedded(opts, build, err)
	}
	dir := opts.Dir
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if opts.Dir == "" {
		// No directory is no build, not the directory the server runs in
		return newHost(opts, dir, noFiles{})
	}
	return newHost(opts, dir, osFiles{})
}

// newHost makes a Host whose files have names below dir.
func newHost(opts Options, dir string, files fileSystem) *Host {
	h := &Host{
		dir:        filepath.Clean(dir),
		https:      opts.HTTPS,
		production: opts.Production,
		session:    opts.Session,
		secure:     opts.Secure,
		clientIP:   opts.ClientIP,
		files:      files,
	}
	h.page = h.loadPage()
	switch {
	case opts.Base != nil:
		h.base = *opts.Base
	case h.page != nil:
		h.base, _ = readBase(h.page.html)
	}
	return h
}

// Base is the URL prefix the app is served under ("" for the root).
func (h *Host) Base() string { return h.base }

// Handle answers a request for a file of the build, a page of the app or
// one of the redirects, and reports whether it did. On false it has written
// no status and no body, and the caller answers (NotFound).
//
// A request it can make no sense of is answered too: 400 for a path that
// does not decode, and the answers of ServerError for a failed precondition
// (412), a range outside the file (416) and a file that cannot be read.
func (h *Host) Handle(w http.ResponseWriter, r *http.Request) bool {
	req := parseRequest(r)
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	// The first route whose path matches but does not decode decides the
	// answer: the routes and the files after it are skipped.
	var failure *httpError

	if h.base == "" {
		if rest, ok := matchOldBase(req.path); ok {
			if _, decodes := decodeURIComponent(rest); !decodes {
				failure = decodeFailure(rest)
			} else if read {
				httpx.Redirect(w, r, http.StatusMovedPermanently, withoutOldBase(req.original))
				return true
			}
		}
	}

	if rest, ok := h.matchImmutable(req.path); ok && failure == nil {
		if _, decodes := decodeURIComponent(rest); !decodes {
			failure = decodeFailure(rest)
		} else if read {
			answered, err := h.serveImmutable(w, r, req.path[len(h.base):])
			if err != nil {
				h.fail(w, r, err)
				return true
			}
			if answered {
				return true
			}
		}
	}

	if failure == nil && read {
		if sub, ok := h.underBase(req.path); ok {
			decoded, decodes := decodeURIComponent(sub)
			if !decodes {
				endEmpty(w, r, http.StatusBadRequest)
				return true
			}
			if !isHTMLName(decoded) {
				answered, err := h.serveStatic(w, r, decoded)
				if err != nil {
					h.fail(w, r, err)
					return true
				}
				if answered {
					return true
				}
			}
		}
	}

	if rest, ok := h.matchPage(req.path); ok && failure == nil {
		if _, decodes := decodeURIComponent(rest); !decodes {
			failure = decodeFailure(rest)
		} else if read && h.servePage(w, r, req) {
			return true
		}
	}

	if failure != nil {
		h.fail(w, r, failure)
		return true
	}
	return false
}

// matchOldBase reports whether a path is /app or under it (in any letter
// case), and gives what follows "/app/".
func matchOldBase(path string) (rest string, ok bool) {
	if asciiEqualFold(path, oldBase) {
		return "", true
	}
	if hasPrefixFold(path, oldBase+"/") {
		return path[len(oldBase)+1:], true
	}
	return "", false
}

// withoutOldBase is the same address without /app: "/app/devices?open=1" is
// "/devices?open=1". Slashes after /app collapse into one, so
// "/app//evil.example" stays on this server.
func withoutOldBase(original string) string {
	if len(original) < len(oldBase) {
		return "/"
	}
	return "/" + strings.TrimLeft(original[len(oldBase):], `/\`)
}

func (h *Host) matchImmutable(path string) (rest string, ok bool) {
	prefix := h.base + "/_ui/immutable/"
	if !hasPrefixFold(path, prefix) {
		return "", false
	}
	return path[len(prefix):], true
}

// underBase reports whether a path is the base or under it, and gives the
// path without the base.
func (h *Host) underBase(path string) (sub string, ok bool) {
	if h.base == "" {
		return path, true
	}
	if !hasPrefixFold(path, h.base) {
		return "", false
	}
	sub = path[len(h.base):]
	switch {
	case sub == "":
		return "/", true
	case sub[0] != '/':
		return "", false
	case strings.HasPrefix(sub, "//"):
		return sub[1:], true
	}
	return sub, true
}

// matchPage reports whether a path may be a page of the app, and gives the
// part of it that has to decode.
func (h *Host) matchPage(path string) (rest string, ok bool) {
	if h.base == "" {
		return path, true
	}
	if asciiEqualFold(path, h.base) {
		return "", true
	}
	if hasPrefixFold(path, h.base+"/") {
		return path[len(h.base)+1:], true
	}
	return "", false
}

func isHTMLName(decoded string) bool {
	return hasSuffixFold(decoded, ".html") || hasSuffixFold(decoded, ".htm")
}

func hasSuffixFold(s, suffix string) bool {
	return len(s) >= len(suffix) && asciiEqualFold(s[len(s)-len(suffix):], suffix)
}

// isSegment reports whether path is prefix itself or under it.
func isSegment(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// isServerPath reports whether a path belongs to the server's API or its
// sockets, which never get the app's page.
func isServerPath(path string) bool {
	for _, prefix := range []string{"/api", "/ws"} {
		if asciiEqualFold(path, prefix) || hasPrefixFold(path, prefix+"/") {
			return true
		}
	}
	return false
}

// isPublicPage reports whether anyone may open a page of the app: the
// sign-in pages, the portal, device links, the page that redeems an
// organization invitation, the page where an invite or reset link sets a
// password or a new email address is confirmed, the pages of invites (/join/<code>) and quick
// support links (/s/<code>), and the remote viewer (/remote/<device id>),
// which also opens for guest links and mesh shares. The viewer page holds
// no data; its data endpoint checks access.
func isPublicPage(sub string) bool {
	if isSegment(sub, "/login") || isSegment(sub, "/portal") || strings.HasPrefix(sub, "/d/") || isSegment(sub, "/invite") || isSegment(sub, "/set-password") || isSegment(sub, "/confirm-email") ||
		strings.HasPrefix(sub, "/join/") || strings.HasPrefix(sub, "/s/") ||
		isSegment(sub, "/remote/login") || isSegment(sub, "/remote/guest") {
		return true
	}
	id, ok := strings.CutPrefix(sub, "/remote/")
	if !ok || len(id) < 3 || len(id) > 64 || id == "login" || id == "guest" {
		return false
	}
	for i := 0; i < len(id); i++ {
		if c := id[i]; !isAlphanumeric(c) && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func (h *Host) who(r *http.Request) (signedIn bool, role string) {
	if h.session == nil {
		return false, ""
	}
	return h.session(r)
}

// servePage answers a GET for a page of the app. It answers false for the
// server's own paths, without a build, and for a path that names a file.
func (h *Host) servePage(w http.ResponseWriter, r *http.Request, req request) bool {
	if isServerPath(req.path) {
		return false
	}
	p := h.current()
	if p == nil {
		return false
	}
	sub := req.path[len(h.base):]
	if sub == "" {
		sub = "/"
	}
	if extname(sub) != "" && !strings.HasSuffix(sub, ".html") {
		return false
	}
	// An old address of the console goes to its new place first, signed
	// in or not: the sign-in page then carries the new address
	if target, ok := redirectFor(p.redirects, sub, req.query); ok {
		httpx.Redirect(w, r, http.StatusFound, h.base+target)
		return true
	}
	if !p.isPage(sub) {
		h.sendPage(w, r, p, http.StatusNotFound, p.html)
		return true
	}
	signedIn, role := h.who(r)
	// The sign-in page is for people who are not signed in: someone who is
	// goes where ?return= points when that is a console path, or to the
	// start. An account with the role "pro" has no console and stays.
	if isSegment(sub, "/login") && signedIn && role != "pro" {
		target := "/"
		if wanted, ok := queryString(req.query, "return"); ok {
			if safe, ok := httpx.SafeReturnPath(wanted); ok {
				target = safe
			}
		}
		httpx.Redirect(w, r, http.StatusFound, target)
		return true
	}
	if !isPublicPage(sub) && (!signedIn || role == "pro") {
		home := h.base
		if home == "" {
			home = "/"
		}
		if isSegment(sub, "/remote") {
			target := home
			if strings.HasPrefix(req.original, "/") && !strings.HasPrefix(req.original, "//") {
				target = req.original
			}
			httpx.Redirect(w, r, http.StatusFound, h.base+"/remote/login?return="+httpx.EncodeURIComponent(target))
			return true
		}
		back, ok := httpx.SafeReturnPath(req.original)
		if !ok {
			back = home
		}
		// The sign-in page is under the base, as the remote one above
		httpx.Redirect(w, r, http.StatusFound, h.base+httpx.LoginRedirectURL(r, &back))
		return true
	}
	h.sendPage(w, r, p, http.StatusOK, p.html)
	return true
}
