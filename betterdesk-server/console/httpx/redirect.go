package httpx

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Redirect answers with a redirect the way res.redirect(status, target)
// did in the old console:
//
//   - Location is target made fit for a header by EncodeURL, so a target
//     that is percent-encoded already goes out as it is.
//   - The body is a short line that names the status and the address, as
//     plain text or as HTML, whichever the Accept header prefers (plain
//     text without the header). A client that accepts neither gets no body
//     and no Content-Type.
//   - Vary gets Accept, Content-Length is the length of the body, and a
//     HEAD request gets the headers only.
//
// The word "back" is an address like any other here.
func Redirect(w http.ResponseWriter, r *http.Request, status int, target string) {
	location := EncodeURL(target)
	h := w.Header()
	h.Set("Location", location)
	kind := Accepts(r, "text/plain", "text/html")
	AddVary(h, "Accept")
	body := ""
	switch kind {
	case "text/plain":
		h.Set("Content-Type", "text/plain; charset=utf-8")
		body = http.StatusText(status) + ". Redirecting to " + location
	case "text/html":
		h.Set("Content-Type", "text/html; charset=utf-8")
		body = "<p>" + http.StatusText(status) + ". Redirecting to " + escapeHTML(location) + "</p>"
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, body)
	}
}

// AddVary adds a field to the Vary header unless it is there already, as
// res.vary(field) did: the fields end up in one header, and "*" stays "*".
func AddVary(h http.Header, field string) {
	current := strings.Join(h.Values("Vary"), ", ")
	if current == "" {
		h.Set("Vary", field)
		return
	}
	for _, name := range strings.Split(current, ",") {
		name = strings.Trim(name, " ")
		if name == "*" {
			h.Set("Vary", "*")
			return
		}
		if strings.EqualFold(name, field) {
			h.Set("Vary", current)
			return
		}
	}
	h.Set("Vary", current+", "+field)
}

// EncodeURL makes an address fit for a Location header: what a URL cannot
// carry is percent-encoded as UTF-8, escapes that are already there stay,
// and a "%" that starts no escape becomes %25.
func EncodeURL(s string) string {
	if !utf8.ValidString(s) {
		s = string([]rune(s))
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	encode := func(i int) int {
		c := s[i]
		if c < utf8.RuneSelf {
			if uriKeeps(c) {
				b.WriteByte(c)
			} else {
				writePercent(&b, c)
			}
			return 1
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		for j := 0; j < size; j++ {
			writePercent(&b, s[i+j])
		}
		return size
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c != '%' && urlSafe(c):
			b.WriteByte(c)
			i++
		case c != '%':
			i += encode(i)
		case i+1 == len(s):
			b.WriteString("%25")
			i++
		case !isHex(s[i+1]):
			b.WriteString("%25")
			i++
			i += encode(i)
		case i+2 == len(s) || isHex(s[i+2]):
			b.WriteByte('%')
			i++
		default:
			b.WriteString("%25")
			b.WriteByte(s[i+1])
			i += 2
			i += encode(i)
		}
	}
	return b.String()
}
