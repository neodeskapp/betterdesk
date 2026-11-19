package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Answer is what the suite keeps of one HTTP answer: the status, the
// headers on the allowlist after normalising, and the body.
type Answer struct {
	Status int `json:"status"`
	// Failed says why there is no answer: "timeout" when the console did
	// not answer in time, "connection" when the connection broke.
	Failed string `json:"failed,omitempty"`
	// Redirects lists the Location of every redirect that was followed
	// before this answer (cases with FollowRedirects).
	Redirects []string            `json:"redirects,omitempty"`
	Headers   map[string][]string `json:"headers,omitempty"`
	Body      Body                `json:"body"`

	// raw is the answer as the console sent it, for what normalizing takes
	// out: a header off the allowlist or a cookie's value for a capture, a
	// secret for a secret capture, and to normalize the answer again once
	// a new secret is known. It is nil for an answer that did not come
	// from a console, and it is never written.
	raw *rawAnswer
}

// rawAnswer is an HTTP answer before normalizing.
type rawAnswer struct {
	status    int
	header    http.Header
	body      []byte
	undecoded bool
}

// The kinds of body an answer has.
const (
	// BodyEmpty is no body at all.
	BodyEmpty = "empty"
	// BodyJSON is a JSON body, kept parsed.
	BodyJSON = "json"
	// BodyBytes is any other body, kept as media type, length and hash.
	BodyBytes = "bytes"
	// BodyEncoded is a body in a content encoding the suite cannot undo;
	// only the encoding (a header) is compared.
	BodyEncoded = "encoded"
	// BodyStream is a body that never ends (text/event-stream); it is not read.
	BodyStream = "stream"
)

// Body is the body of an answer.
type Body struct {
	Kind string `json:"kind"`
	// JSON is the parsed body when Kind is BodyJSON.
	JSON json.RawMessage `json:"json,omitempty"`
	// Type, Length and SHA256 describe a body that is not JSON. Length and
	// hash are taken after the instance's own address was replaced, and are
	// left out when the body is marked volatile.
	Type   string `json:"type,omitempty"`
	Length int    `json:"length,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	// Head is the start of a text body (300 bytes), to make a diff readable.
	Head string `json:"head,omitempty"`
}

// headBytes is how much of a text body a golden keeps.
const headBytes = 300

type headerRule int

const (
	// keepValue compares the header as sent.
	keepValue headerRule = iota
	// keepNumber replaces numbers that count time or requests by <n>.
	keepNumber
	// keepPresence notes that the header is there, without its value.
	keepPresence
	// keepDate replaces an HTTP date by <date>.
	keepDate
	// keepCookie reduces Set-Cookie to name and attributes.
	keepCookie
	// keepPolicy is a Content-Security-Policy: nonces are replaced.
	keepPolicy
)

// keptHeaders is the allowlist: the headers an answer is compared on, by
// name in lower case. Every
// other header (Date, Content-Length, Connection, Keep-Alive, ...) is left
// out. Access-Control-* headers are kept as sent (see headerRuleFor).
var keptHeaders = map[string]headerRule{
	"content-type":          keepValue,
	"content-disposition":   keepValue,
	"content-encoding":      keepValue,
	"content-language":      keepValue,
	"content-range":         keepValue,
	"accept-ranges":         keepValue,
	"cache-control":         keepValue,
	"pragma":                keepValue,
	"expires":               keepDate,
	"location":              keepValue,
	"vary":                  keepValue,
	"allow":                 keepValue,
	"www-authenticate":      keepValue,
	"retry-after":           keepNumber,
	"ratelimit-policy":      keepValue,
	"ratelimit-limit":       keepValue,
	"ratelimit-remaining":   keepNumber,
	"ratelimit-reset":       keepNumber,
	"ratelimit":             keepNumber,
	"x-ratelimit-limit":     keepValue,
	"x-ratelimit-remaining": keepNumber,
	"x-ratelimit-reset":     keepNumber,
	"set-cookie":            keepCookie,
	"etag":                  keepPresence,
	"last-modified":         keepPresence,

	"content-security-policy":             keepPolicy,
	"content-security-policy-report-only": keepPolicy,
	"strict-transport-security":           keepValue,
	"cross-origin-embedder-policy":        keepValue,
	"cross-origin-opener-policy":          keepValue,
	"cross-origin-resource-policy":        keepValue,
	"origin-agent-cluster":                keepValue,
	"permissions-policy":                  keepValue,
	"referrer-policy":                     keepValue,
	"x-content-type-options":              keepValue,
	"x-dns-prefetch-control":              keepValue,
	"x-download-options":                  keepValue,
	"x-frame-options":                     keepValue,
	"x-permitted-cross-domain-policies":   keepValue,
	"x-powered-by":                        keepValue,
	"x-robots-tag":                        keepValue,
	"x-xss-protection":                    keepValue,
	"x-branding-revision":                 keepValue,
}

func headerRuleFor(name string) (headerRule, bool) {
	name = strings.ToLower(name)
	if strings.HasPrefix(name, "access-control-") {
		return keepValue, true
	}
	rule, ok := keptHeaders[name]
	return rule, ok
}

// Normalizer takes what belongs to one instance out of an answer, so two
// instances give the same answer: the console's own address, the address
// the suite's requests come from, and the values of the secrets the run
// knows (secrets.go).
type Normalizer struct {
	replacer *strings.Replacer
	clientIP *regexp.Regexp
	// consoleIP is the IP address the console's host name stands for, as
	// the network tools report it when a case points them at the console.
	consoleIP *regexp.Regexp
	secrets   *secretSet
}

// addressPattern finds an IP address in text where it is not part of a
// longer number.
func addressPattern(ip string) *regexp.Regexp {
	return regexp.MustCompile(`(^|[^0-9.])` + regexp.QuoteMeta(ip) + `($|[^0-9.]|\.[^0-9]|\.$)`)
}

// replaceAddress replaces every match of an address pattern by word.
func replaceAddress(s string, pattern *regexp.Regexp, word string) string {
	if pattern == nil || !pattern.MatchString(s) {
		return s
	}
	// Twice, because two addresses in a row share the character between them.
	s = pattern.ReplaceAllString(s, "${1}"+word+"${2}")
	return pattern.ReplaceAllString(s, "${1}"+word+"${2}")
}

// NewNormalizer returns a Normalizer for a console at origin (scheme and
// host, "http://bdgo-c1:5000"). clientIP is the address the console sees
// the suite at, or "" when it is not known.
func NewNormalizer(origin, clientIP string) *Normalizer {
	n := &Normalizer{}
	var pairs []string
	if scheme, host, ok := strings.Cut(origin, "://"); ok && host != "" {
		pairs = append(pairs, scheme+"://"+host, "<base>", host, "<host>")
		if name, _, hasPort := strings.Cut(host, ":"); hasPort && name != "" && !strings.HasPrefix(host, "[") {
			pairs = append(pairs, name, "<hostname>")
		}
	}
	n.replacer = strings.NewReplacer(pairs...)
	if clientIP != "" {
		n.clientIP = addressPattern(clientIP)
	}
	return n
}

// withConsoleIP makes the Normalizer write the console's own IP address
// as <console-ip>. An empty or loopback address is left alone.
func (n *Normalizer) withConsoleIP(ip string) *Normalizer {
	if parsed := net.ParseIP(ip); parsed != nil && !parsed.IsLoopback() {
		n.consoleIP = addressPattern(ip)
	}
	return n
}

// Text replaces the known secrets and the instance's addresses in s.
func (n *Normalizer) Text(s string) string {
	if n == nil {
		return s
	}
	s = n.secrets.mask(s)
	s = n.replacer.Replace(s)
	s = replaceAddress(s, n.clientIP, "<client-ip>")
	return replaceAddress(s, n.consoleIP, "<console-ip>")
}

// maskJSON masks the known secrets in the texts and keys of a decoded JSON
// value, where the text of the body could not show them: a secret the
// console wrote with an escape in it ("\/" for "/").
func (n *Normalizer) maskJSON(value any) any {
	if n == nil || n.secrets == nil {
		return value
	}
	switch v := value.(type) {
	case string:
		return n.secrets.mask(v)
	case []any:
		for i, element := range v {
			v[i] = n.maskJSON(element)
		}
	case map[string]any:
		for _, key := range sortedKeys(v) {
			child := v[key]
			masked := n.secrets.mask(key)
			if masked != key {
				delete(v, key)
			}
			v[masked] = n.maskJSON(child)
		}
	}
	return value
}

// NewAnswer builds the Answer for a response. body is the body as sent,
// after any Content-Encoding the suite understands was undone; undecoded
// says the encoding could not be undone.
func NewAnswer(status int, header http.Header, body []byte, undecoded bool, n *Normalizer) Answer {
	a := Answer{Status: status, Headers: NormalizeHeaders(header, n)}
	mediaType := mediaTypeOf(header.Get("Content-Type"))
	switch {
	case mediaType == "text/event-stream":
		a.Body = Body{Kind: BodyStream}
	case undecoded:
		a.Body = Body{Kind: BodyEncoded}
	case len(body) == 0:
		a.Body = Body{Kind: BodyEmpty}
	default:
		a.Body = newBody(mediaType, body, n)
	}
	return a
}

func newBody(mediaType string, body []byte, n *Normalizer) Body {
	if isJSONType(mediaType) {
		text := []byte(n.Text(string(body)))
		if value, err := decodeJSON(text); err == nil {
			return Body{Kind: BodyJSON, JSON: encodeJSON(n.maskJSON(value))}
		}
	}
	b := Body{Kind: BodyBytes, Type: mediaType}
	if isTextType(mediaType) {
		body = []byte(n.Text(string(body)))
		head := body
		if len(head) > headBytes {
			head = head[:headBytes]
		}
		b.Head = strings.ToValidUTF8(string(head), "�")
	}
	sum := sha256.Sum256(body)
	b.Length = len(body)
	b.SHA256 = hex.EncodeToString(sum[:])
	return b
}

func mediaTypeOf(contentType string) string {
	if contentType == "" {
		return ""
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	}
	return mediaType
}

func isJSONType(mediaType string) bool {
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

func isTextType(mediaType string) bool {
	return strings.HasPrefix(mediaType, "text/") ||
		strings.Contains(mediaType, "javascript") ||
		strings.Contains(mediaType, "xml") ||
		strings.Contains(mediaType, "json")
}

// decodeJSON parses a JSON document and keeps numbers as written.
func decodeJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errTrailingJSON
	}
	return value, nil
}

type jsonError string

func (e jsonError) Error() string { return string(e) }

const errTrailingJSON = jsonError("more than one JSON value")

// encodeJSON writes a value the way goldens hold it: keys sorted, no HTML
// escaping, one line.
func encodeJSON(value any) json.RawMessage {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		// Values come from decodeJSON, which only yields what encodes.
		panic("contract: encode JSON: " + err.Error())
	}
	return json.RawMessage(bytes.TrimRight(buf.Bytes(), "\n"))
}

// NormalizeHeaders reduces response headers to the allowlist. Names are in
// lower case. What changes from one answer to the next without meaning
// anything is replaced by a placeholder: see the rules at keptHeaders.
func NormalizeHeaders(header http.Header, n *Normalizer) map[string][]string {
	out := map[string][]string{}
	for name, values := range header {
		rule, ok := headerRuleFor(name)
		if !ok || len(values) == 0 {
			continue
		}
		kept := make([]string, 0, len(values))
		for _, value := range values {
			value = n.Text(strings.TrimSpace(value))
			switch rule {
			case keepNumber:
				value = digits.ReplaceAllString(value, "<n>")
			case keepPresence:
				value = "<present>"
			case keepDate:
				value = normalizeDate(value)
			case keepCookie:
				value = normalizeSetCookie(value)
			case keepPolicy:
				value = nonce.ReplaceAllString(value, "'nonce-<nonce>'")
			}
			kept = append(kept, value)
		}
		if rule == keepCookie {
			sort.SliceStable(kept, func(i, j int) bool { return cookieName(kept[i]) < cookieName(kept[j]) })
		}
		out[strings.ToLower(name)] = kept
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

var (
	digits = regexp.MustCompile(`[0-9]+`)
	nonce  = regexp.MustCompile(`'nonce-[^']*'`)
)

// normalizeDate replaces an HTTP date by <date>, or by <past> when it lies
// before the year 2000, which is how a cookie or an answer is expired.
// Anything that is not a date stays as it is ("0", "-1").
func normalizeDate(value string) string {
	t, err := http.ParseTime(value)
	if err != nil {
		return value
	}
	if t.Before(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) {
		return "<past>"
	}
	return "<date>"
}

// normalizeSetCookie keeps the cookie's name and attributes and drops what
// differs per answer: the value becomes <value> (an empty value stays
// empty, that is a cookie being cleared), Expires becomes <date> or <past>,
// and a positive Max-Age becomes <n>.
func normalizeSetCookie(value string) string {
	parts := strings.Split(value, ";")
	for i, part := range parts {
		part = strings.TrimSpace(part)
		name, val, hasValue := strings.Cut(part, "=")
		switch {
		case i == 0:
			if hasValue && val != "" {
				part = name + "=<value>"
			}
		case strings.EqualFold(name, "Expires"):
			part = name + "=" + normalizeDate(val)
		case strings.EqualFold(name, "Max-Age"):
			if seconds, err := strconv.Atoi(val); err == nil && seconds > 0 {
				part = name + "=<n>"
			}
		}
		parts[i] = part
	}
	return strings.Join(parts, "; ")
}

func cookieName(setCookie string) string {
	name, _, _ := strings.Cut(setCookie, "=")
	name, _, _ = strings.Cut(name, ";")
	return name
}
