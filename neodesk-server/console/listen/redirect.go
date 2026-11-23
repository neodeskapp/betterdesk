package listen

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
)

// Redirect is the plain port of a console in HTTPS mode: every request,
// whatever its method, is answered 307 to https://<host>:<httpsPort>
// (the port left out when it is 443) and the request target when that is
// a path (see SafeRedirectURL). host is the one RedirectHost chose when
// the listener was made; the request's Host header never reaches the
// answer. The answer is res.redirect(307) of a bare Express app without
// its X-Powered-By: Cache-Control: no-store, a short body in the type the
// Accept header prefers. CONNECT is closed without an answer.
func Redirect(host string, httpsPort int) http.Handler {
	origin := redirectOrigin(host, httpsPort)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Served(r)
		if r.Method == http.MethodConnect {
			closeUnanswered(w)
			return
		}
		if nonASCII(r.RequestURI) || r.ProtoMajor != 1 {
			badRequest(w)
			return
		}
		location := expressLocation(origin + samePath(r.RequestURI))
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("Location", location)
		httpx.AddVary(h, "Accept")
		body := ""
		switch httpx.Accepts(r, "text/plain", "text/html") {
		case "text/plain":
			h.Set("Content-Type", "text/plain; charset=utf-8")
			body = "Temporary Redirect. Redirecting to " + location
		case "text/html":
			h.Set("Content-Type", "text/html; charset=utf-8")
			body = "<p>Temporary Redirect. Redirecting to " + escapeHTML(location) + "</p>"
		}
		h.Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusTemporaryRedirect)
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, body)
		}
	})
}

// PublicHostPage is the plain port of a console in HTTPS mode that has no
// public host (PANEL_PUBLIC_HOST names none) and binds every address: with
// nothing to name the HTTPS address, every request gets a short page that
// says to open the console with https:// and asks the administrator to
// set PANEL_PUBLIC_HOST, instead of a redirect to a host the console would
// have to guess. The page is plain HTML in English, with no script and
// nothing of the request in it. CONNECT is closed without an answer, and
// a target the redirect refuses is refused the same way.
func PublicHostPage(httpsPort int) http.Handler {
	page := publicHostPage(httpsPort)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Served(r)
		if r.Method == http.MethodConnect {
			closeUnanswered(w)
			return
		}
		if nonASCII(r.RequestURI) || r.ProtoMajor != 1 {
			badRequest(w)
			return
		}
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Content-Security-Policy", "default-src 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Length", strconv.Itoa(len(page)))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, page)
		}
	})
}

// publicHostPage is the page of PublicHostPage.
func publicHostPage(httpsPort int) string {
	open := "Open it with https:// at the address you use for this server."
	if httpsPort > 0 && httpsPort != 443 {
		open = "Open it with https:// and port " + strconv.Itoa(httpsPort) + " at the address you use for this server."
	}
	return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>NeoDesk: use HTTPS</title>
</head>
<body>
<h1>This console uses HTTPS</h1>
<p>` + open + `</p>
<p>Administrators: set PANEL_PUBLIC_HOST to the host name or address of this console and restart it. This port then sends browsers to HTTPS.</p>
</body>
</html>
`
}

// PublicHostWarning is the start log's warning for a console in HTTPS mode
// whose plain port answers PublicHostPage: configured is the value of
// PANEL_PUBLIC_HOST, which names no host. It is "" when configured names
// one.
func PublicHostWarning(configured string, plainPort int) string {
	if _, ok := normalizeConfiguredHost(configured); ok {
		return ""
	}
	what := "is not set"
	if jsTrim(configured) != "" {
		what = fmt.Sprintf("%q names no host (write the host name or address alone, without a port or path)", configured)
	}
	return fmt.Sprintf("HTTPS is on and PANEL_PUBLIC_HOST %s: the plain HTTP port %d answers a page that asks to set it instead of redirecting to HTTPS", what, plainPort)
}

// expressLocation is the Location header res.location made of an
// address: the address made fit for a header (httpx.EncodeURL), unless
// that changed the host Node's url.parse finds in it, or url.parse
// refused one of the two; then the address goes out as it is.
func expressLocation(address string) string {
	encoded := httpx.EncodeURL(address)
	lower := strings.ToLower(address)
	if !strings.HasPrefix(lower, "https://") && !strings.HasPrefix(lower, "http://") {
		return encoded
	}
	original, ok := urlParseHost(address)
	if !ok {
		return address
	}
	after, ok := urlParseHost(encoded)
	if !ok || original != after {
		return address
	}
	return encoded
}

// urlParseHost is the host url.parse of Node gives an http(s) address,
// and false where url.parse throws.
func urlParseHost(address string) (string, bool) {
	rest := address[strings.Index(address, "//")+2:]
	hostEnd, atSign, nonHost := -1, -1, -1
	for i := 0; i < len(rest) && hostEnd == -1; i++ {
		switch rest[i] {
		case '\t', '\n', '\r', ' ', '"', '%', '\'', ';', '<', '>', '\\', '^', '`', '{', '|', '}':
			if nonHost == -1 {
				nonHost = i
			}
		case '#', '/', '?':
			if nonHost == -1 {
				nonHost = i
			}
			hostEnd = i
		case '@':
			atSign = i
			nonHost = -1
		}
	}
	start := 0
	if atSign != -1 {
		start = atSign + 1
	}
	host := rest[start:]
	if nonHost != -1 {
		host = rest[start:nonHost]
	}
	port := ""
	if i := strings.LastIndexByte(host, ':'); i >= 0 && allDigits(host[i+1:]) {
		port = host[i+1:]
		host = host[:i]
	}
	name := host
	ipv6 := len(name) >= 2 && name[0] == '[' && name[len(name)-1] == ']'
	if !ipv6 {
		if i := strings.IndexAny(name, "/\\#?:"); i >= 0 {
			name = name[:i]
		}
	}
	if len(name) > 255 {
		name = ""
	}
	name = strings.ToLower(name)
	if name != "" {
		forbidden := "\x00\t\n\r #%/:<>?@[\\]^|"
		if ipv6 {
			forbidden = "\x00\t\n\r #%/<>?@\\^|"
		}
		if strings.ContainsAny(name, forbidden) {
			return "", false
		}
	}
	if port != "" {
		return name + ":" + port, true
	}
	return name, true
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")

func escapeHTML(s string) string { return htmlEscaper.Replace(s) }

// nonASCII reports a request target with a byte above 0x7f, which Node's
// HTTP parser refused with 400.
func nonASCII(target string) bool {
	for i := 0; i < len(target); i++ {
		if target[i] >= 0x80 {
			return true
		}
	}
	return false
}

// badRequest is the 400 Node's HTTP parser answered a request it could
// not read with.
func badRequest(w http.ResponseWriter) {
	w.Header().Set("Connection", "close")
	w.WriteHeader(http.StatusBadRequest)
}
