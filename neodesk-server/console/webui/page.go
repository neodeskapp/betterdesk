package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// page is the app's page as the build has it.
type page struct {
	html string
	// csp is the Content-Security-Policy header the page goes out with.
	csp string
	// routes are the patterns of the app's pages. nil means the build has
	// no usable list, and then every address counts as a page.
	routes []*regexp.Regexp
	// mtime and routesMtime are when index.html and routes.json were last
	// written (zero for a missing routes.json).
	mtime, routesMtime time.Time
}

var (
	cspMeta     = regexp.MustCompile(`(?i)<meta http-equiv="content-security-policy" content="([^"]*)"`)
	headOnly    = regexp.MustCompile(`^(frame-ancestors|report-uri|sandbox)\b`)
	startScript = regexp.MustCompile(`__sveltekit_\w+` + jsSpace + `*=` + jsSpace + `*\{` + jsSpace + `*base:` + jsSpace + `*"([^"]*)"`)
	plainBase   = regexp.MustCompile(`^/[A-Za-z0-9._~-]+(/[A-Za-z0-9._~-]+)*$`)
	headTag     = regexp.MustCompile(`(?i)<head(` + jsSpace + `[^>]*)?>`)
)

// readCSP finds the policy the build wrote into the page's <meta> tag.
func readCSP(html string) (string, bool) {
	m := cspMeta.FindStringSubmatch(html)
	if m == nil {
		return "", false
	}
	policy := strings.ReplaceAll(m[1], "&#39;", "'")
	policy = strings.ReplaceAll(policy, "&apos;", "'")
	policy = strings.ReplaceAll(policy, "&quot;", `"`)
	policy = strings.ReplaceAll(policy, "&amp;", "&")
	return policy, true
}

// HeaderPolicy turns the policy of the build's <meta> tag into the
// Content-Security-Policy header of a page: the same directives without the
// ones a <meta> tag cannot carry, plus frame-ancestors 'none', plus
// upgrade-insecure-requests when the console runs with HTTPS. An empty
// metaPolicy stands for a build without the tag and gives a policy that
// allows the console's own scripts only.
func HeaderPolicy(metaPolicy string, https bool) string {
	if metaPolicy == "" {
		metaPolicy = "default-src 'self'; script-src 'self'"
	}
	var parts []string
	for _, directive := range strings.Split(metaPolicy, ";") {
		directive = jsTrim(directive)
		if directive == "" || headOnly.MatchString(directive) {
			continue
		}
		parts = append(parts, directive)
	}
	parts = append(parts, "frame-ancestors 'none'")
	if https {
		parts = append(parts, "upgrade-insecure-requests")
	}
	return strings.Join(parts, "; ")
}

// readBase finds the base the build was made for in the start script of
// its page (base: ""). Only "" or a plain path counts.
func readBase(html string) (string, bool) {
	m := startScript.FindStringSubmatch(html)
	if m == nil {
		return "", false
	}
	if m[1] == "" || plainBase.MatchString(m[1]) {
		return m[1], true
	}
	return "", false
}

// loadRoutes reads the build's list of pages (routes.json): one JavaScript
// regular expression per page. It answers nil, which makes every address a
// page, when the file is missing, cannot be read, is empty, or holds a
// pattern that Go's regular expressions cannot express.
func (h *Host) loadRoutes() []*regexp.Regexp {
	raw, err := h.files.ReadFile(filepath.Join(h.dir, "routes.json"))
	if err != nil {
		return nil
	}
	var list []struct {
		Pattern *string `json:"pattern"`
	}
	if err := json.Unmarshal(raw, &list); err != nil || len(list) == 0 {
		return nil
	}
	routes := make([]*regexp.Regexp, 0, len(list))
	for _, entry := range list {
		// An entry without a pattern is the empty pattern, which takes
		// every path
		source := ""
		if entry.Pattern != nil {
			source = *entry.Pattern
		}
		re, err := compileJSPattern(source)
		if err != nil {
			logging.Logf("panel", "web UI: the page pattern %q in routes.json is not usable here (%v); every address counts as a page until the build is replaced", source, err)
			return nil
		}
		routes = append(routes, re)
	}
	return routes
}

// compileJSPattern compiles the source of a JavaScript regular expression
// as SvelteKit writes them for its routes. "[^]" (any character) is written
// the way Go knows it; other JavaScript-only syntax is an error.
func compileJSPattern(source string) (*regexp.Regexp, error) {
	if strings.Contains(source, "(?=") || strings.Contains(source, "(?!") || strings.Contains(source, "(?<") {
		return nil, fmt.Errorf("look-around and named groups are not supported")
	}
	return regexp.Compile(strings.ReplaceAll(source, "[^]", `[\s\S]`))
}

func (h *Host) mtimeOf(name string) time.Time {
	info, err := h.files.Stat(name)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// loadPage reads the build's page; nil without a build.
func (h *Host) loadPage() *page {
	name := filepath.Join(h.dir, "index.html")
	raw, err := h.files.ReadFile(name)
	if err != nil {
		return nil
	}
	html := string(raw)
	meta, _ := readCSP(html)
	routes := h.loadRoutes()
	info, err := h.files.Stat(name)
	if err != nil {
		return nil
	}
	return &page{
		html:        html,
		csp:         HeaderPolicy(meta, h.https),
		routes:      routes,
		mtime:       info.ModTime(),
		routesMtime: h.mtimeOf(filepath.Join(h.dir, "routes.json")),
	}
}

// current is the page to serve now. Outside production it follows the
// build: a changed index.html or routes.json is read again, and a removed
// index.html means there is no page.
func (h *Host) current() *page {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.production {
		return h.page
	}
	info, err := h.files.Stat(filepath.Join(h.dir, "index.html"))
	if err != nil {
		h.page = nil
		return nil
	}
	if h.page == nil || !h.page.mtime.Equal(info.ModTime()) ||
		!h.page.routesMtime.Equal(h.mtimeOf(filepath.Join(h.dir, "routes.json"))) {
		h.page = h.loadPage()
	}
	return h.page
}

// isPage reports whether the app has a page at this path (without the
// base). The path is decoded the way the app decodes it before matching:
// escapes of reserved characters and %25 stay.
func (p *page) isPage(sub string) bool {
	if p.routes == nil {
		return true
	}
	parts := strings.Split(sub, "%25")
	for i, part := range parts {
		decoded, ok := decodeURI(part)
		if !ok {
			return false
		}
		parts[i] = decoded
	}
	decoded := strings.Join(parts, "%25")
	for _, re := range p.routes {
		if re.MatchString(decoded) {
			return true
		}
	}
	return false
}

func (h *Host) sendPage(w http.ResponseWriter, r *http.Request, p *page, status int, html string) {
	header := w.Header()
	header.Set("Content-Security-Policy", p.csp)
	header.Set("Cache-Control", "no-store")
	httpx.WriteBody(w, r, status, "text/html; charset=utf-8", []byte(html))
}

// ErrorOptions say how an error page differs from the plain one of its
// kind.
type ErrorOptions struct {
	// Status is the status of the answer; 0 means the kind.
	Status int
	// Title and Message replace the app's own words for the error.
	Title, Message string
}

var attrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")

// ErrorPage answers with the app's error page for an error the server
// decided: the app's page with <meta name="bd-error" content="403|404|500">
// in its head, which makes the app show the error at this address. kind
// picks the error page (anything but 403 and 404 shows the 500 page).
//
// It answers false, having written nothing, when it cannot: without a
// build, or for an address outside the build's base. The caller then
// answers in plain text.
func (h *Host) ErrorPage(w http.ResponseWriter, r *http.Request, kind int, o ErrorOptions) bool {
	p := h.current()
	if p == nil {
		return false
	}
	if path := parseRequest(r).path; h.base != "" && path != h.base && !strings.HasPrefix(path, h.base+"/") {
		return false
	}
	status := o.Status
	if status == 0 {
		status = kind
	}
	if status < 100 || status > 999 {
		// Not a status a server can send: a failure of the caller
		kind, status, o = http.StatusInternalServerError, http.StatusInternalServerError, ErrorOptions{}
	}
	code := kind
	if code != http.StatusForbidden && code != http.StatusNotFound {
		code = http.StatusInternalServerError
	}
	meta := fmt.Sprintf(`<meta name="bd-error" content="%d"`, code)
	if o.Title != "" {
		meta += ` data-title="` + attrEscaper.Replace(o.Title) + `"`
	}
	if o.Message != "" {
		meta += ` data-message="` + attrEscaper.Replace(o.Message) + `"`
	}
	meta += ">"
	html := p.html
	if at := headTag.FindStringIndex(html); at != nil {
		html = html[:at[1]] + meta + html[at[1]:]
	}
	h.sendPage(w, r, p, status, html)
	return true
}

var bodyTag = regexp.MustCompile(`(?i)<body(` + jsSpace + `[^>]*)?>`)

// PageWith answers a public page of the app (isPublicPage) with markup the
// server adds right after the page's <body> tag: what the page says for a
// browser without JavaScript, in a <noscript> block (the invite page,
// routes/onboard). The rest of the page and its headers are the plain
// page's.
//
// It answers false, having written nothing, without a build or for an
// address that is no public page under the build's base; the caller then
// lets Handle answer.
func (h *Host) PageWith(w http.ResponseWriter, r *http.Request, body string) bool {
	p := h.current()
	if p == nil {
		return false
	}
	path := parseRequest(r).path
	if h.base != "" && !strings.HasPrefix(path, h.base+"/") {
		return false
	}
	if !isPublicPage(path[len(h.base):]) {
		return false
	}
	html := p.html
	if at := bodyTag.FindStringIndex(html); at != nil {
		html = html[:at[1]] + body + html[at[1]:]
	}
	h.sendPage(w, r, p, http.StatusOK, html)
	return true
}
