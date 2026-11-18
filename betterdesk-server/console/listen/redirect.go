package listen

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// Redirect is the plain port of a console in HTTPS mode: every request,
// whatever its method, is answered 307 to the same target on
// https://<host>:<httpsPort> (the port left out when it is 443). The host
// is the request's Host without its port, "localhost" without one; the
// target is the request target as the client wrote it. The answer is
// res.redirect(307) of a bare Express app: Cache-Control: no-store,
// X-Powered-By: Express, a short body in the type the Accept header
// prefers. CONNECT is closed without an answer.
//
// The address comes from the client's own Host header and only goes back
// to that client, so it cannot send anyone else elsewhere. Hosts that are
// not valid host names never get here: the server refuses them with 400.
func Redirect(httpsPort int) http.Handler {
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
		location := expressLocation(HTTPSURL(hostname(r.Host), httpsPort, r.RequestURI))
		h := w.Header()
		h.Set("X-Powered-By", "Express")
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

// HTTPSURL is the address the redirect sends a client to
// (formatHttpsRedirectUrl in lib/privilegedPorts.js).
func HTTPSURL(host string, httpsPort int, target string) string {
	if host == "" {
		host = "localhost"
	}
	if target == "" {
		target = "/"
	}
	suffix := ""
	if httpsPort > 0 && httpsPort != 443 {
		suffix = ":" + strconv.Itoa(httpsPort)
	}
	return "https://" + host + suffix + target
}

// hostname is req.hostname of Express without trusted proxies: the Host
// header up to its port, an IPv6 literal kept whole.
func hostname(host string) string {
	offset := 0
	if strings.HasPrefix(host, "[") {
		offset = strings.IndexByte(host, ']') + 1
	}
	if i := strings.IndexByte(host[offset:], ':'); i >= 0 {
		return host[:offset+i]
	}
	return host
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
