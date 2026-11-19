package i18n

import (
	"encoding/base64"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// The upload form is read the way multer on busboy reads one, so that the
// requests those accept are accepted here and the ones they refuse are
// refused with the same message: one file in the field
// "file" of type application/json and at most 1 MiB, text fields of at
// most 1 MiB each, and the parser's own idea of what a part is.

const (
	uploadFileField = "file"
	uploadFileType  = "application/json"
	// One byte more than allowed is read, so that the limit itself passes.
	uploadFileLimit  = 1024*1024 + 1
	uploadFieldLimit = 1024*1024 + 1
	// The form has one file and a code field; the parser stops at the
	// first part or text field over these.
	uploadMaxParts  = 12
	uploadMaxFields = 10

	maxPartHeaderBytes = 16 * 1024
	maxPartHeaderPairs = 2000
)

// upload is what the form parser hands to the route.
type upload struct {
	// hasFile is set when a file was accepted.
	hasFile      bool
	originalName string
	data         []byte
	// code is the form's "code" value as the route sees it: undefined,
	// a string, a list or an object.
	code any
	// failure is the message of the error that stopped the parser, which
	// the route answers with status 400, or 413 when tooLarge is set: a
	// part was over its size limit, or the form had too many parts or
	// fields.
	failure  string
	tooLarge bool
}

var (
	mediaTypeRE     = regexp.MustCompile(`^ *([A-Za-z0-9][A-Za-z0-9!#$&^_-]{0,126})/([A-Za-z0-9][A-Za-z0-9!#$&^_.+-]{0,126}) *$`)
	mediaSubtypeRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9!#$&^_.-]{0,126}$`)
	mediaNameRE     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9!#$&^_-]{0,126}$`)
	mediaParamRE    = regexp.MustCompile("^; *([!#$%&'*+\\-.0-9A-Z^_`a-z|~]+) *= *(\"(?:[ !\\x{23}-\\x{5b}\\x{5d}-\\x{7e}\\x{80}-\\x{ff}]|\\\\[\\x{20}-\\x{7e}])*\"|[!#$%&'*+\\-.0-9A-Z^_`a-z|~]+) *")
	formDataEscapes = strings.NewReplacer("%0A", "\n", "%0a", "\n", "%0D", "\r", "%0d", "\r", "%22", `"`)
)

// isMultipart is the check that decides whether the form parser runs at
// all: the request has a body and a well-formed multipart/* content type.
func isMultipart(r *http.Request) bool {
	if len(r.TransferEncoding) == 0 && r.Header.Get("Content-Length") == "" && r.ContentLength <= 0 {
		return false
	}
	value := latin1(r.Header.Get("Content-Type"))
	if value == "" {
		return false
	}
	typePart, params := value, ""
	if i := strings.IndexByte(value, ';'); i >= 0 {
		typePart, params = value[:i], value[i:]
	}
	m := mediaTypeRE.FindStringSubmatch(strings.ToLower(typePart))
	if m == nil {
		return false
	}
	for params != "" {
		loc := mediaParamRE.FindStringIndex(params)
		if loc == nil {
			return false
		}
		params = params[loc[1]:]
	}
	subtype, suffix := m[2], ""
	if i := strings.LastIndexByte(subtype, '+'); i >= 0 {
		subtype, suffix = subtype[:i], subtype[i+1:]
	}
	if !mediaSubtypeRE.MatchString(subtype) || (suffix != "" && !mediaNameRE.MatchString(suffix)) {
		return false
	}
	return m[1] == "multipart"
}

// readUpload reads the form of an upload request.
func readUpload(r *http.Request) upload {
	if !isMultipart(r) {
		return upload{code: undefined}
	}
	header := latin1(r.Header.Get("Content-Type"))
	contentType, ok := parseContentType(header)
	if !ok {
		return upload{failure: "Malformed content type"}
	}
	if contentType.kind != "multipart" || contentType.subtype != "form-data" {
		return upload{failure: "Unsupported content type: " + header}
	}
	boundary, ok := contentType.params["boundary"]
	if !ok {
		return upload{failure: "Multipart: Boundary not found"}
	}
	p := &formParser{needle: []byte("\r\n--" + toBytesLatin1(boundary)), fileSlots: 1, body: &formNode{obj: NewObject()}}
	p.write([]byte("\r\n"))
	buf := make([]byte, 32*1024)
	for p.failure == "" && !p.complete {
		n, err := r.Body.Read(buf)
		if n > 0 {
			p.write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	if p.failure == "" {
		p.finish()
	}
	// The rest of the body is read and dropped, so the client can
	// finish sending before it gets the answer.
	_, _ = io.Copy(io.Discard, r.Body)
	out := upload{failure: p.failure, tooLarge: p.tooLarge, code: undefined}
	if p.failure == "" {
		out.hasFile, out.originalName, out.data = p.stored, p.storedName, p.storedData
		out.code = p.body.value("code")
	}
	return out
}

func toBytesLatin1(s string) string {
	var b strings.Builder
	for _, r := range s {
		b.WriteByte(byte(r))
	}
	return b.String()
}

// formParser is the multipart reader: a search for the boundary, and what
// happens with the bytes between two boundaries.
type formParser struct {
	needle []byte
	carry  []byte

	complete bool
	failure  string
	tooLarge bool

	// after a boundary: 1 expects - or CR, 2 expects the second -, 3
	// expects LF
	postBoundary int
	inHeader     bool
	header       partHeaderParser
	skipPart     bool

	// the open part
	inFile, inField bool
	fileSize        int
	fileAccepted    bool
	fileName        string
	fileData        []byte
	fieldName       string
	fieldNamed      bool
	fieldCharset    string
	fieldData       []byte
	fieldSize       int
	fieldTruncated  bool

	fileSlots  int
	parts      int
	fields     int
	stored     bool
	storedName string
	storedData []byte
	body       *formNode
}

func (p *formParser) fail(message string) {
	if p.failure == "" {
		p.failure = message
	}
}

// failTooLarge is fail for a part over its size limit, or a part or field
// too many.
func (p *formParser) failTooLarge(message string) {
	if p.failure == "" {
		p.failure, p.tooLarge = message, true
	}
}

// write feeds body bytes to the boundary search.
func (p *formParser) write(chunk []byte) {
	data := append(p.carry, chunk...)
	p.carry = nil
	for p.failure == "" && !p.complete {
		i := indexBytes(data, p.needle)
		if i < 0 {
			break
		}
		p.data(data[:i])
		p.boundary()
		data = data[i+len(p.needle):]
	}
	if p.failure != "" || p.complete {
		return
	}
	// Keep what could be the start of a boundary for the next chunk.
	keep := len(p.needle) - 1
	if keep > len(data) {
		keep = len(data)
	}
	for keep > 0 && !hasPrefixBytes(p.needle, data[len(data)-keep:]) {
		keep--
	}
	p.data(data[:len(data)-keep])
	p.carry = append([]byte(nil), data[len(data)-keep:]...)
}

func indexBytes(data, needle []byte) int {
	if len(needle) == 0 {
		return 0
	}
	return strings.Index(string(data), string(needle))
}

func hasPrefixBytes(needle, prefix []byte) bool {
	return len(prefix) <= len(needle) && string(needle[:len(prefix)]) == string(prefix)
}

// finish is the end of the body.
func (p *formParser) finish() {
	if !p.complete {
		p.data(p.carry)
		p.carry = nil
	}
	switch {
	case p.failure != "":
	case !p.complete:
		p.fail("Unexpected end of form")
	case p.inHeader:
		p.fail("Malformed part header")
	}
}

// data handles bytes that are not part of a boundary.
func (p *formParser) data(data []byte) {
	for len(data) > 0 && p.failure == "" && !p.complete {
		if p.inHeader {
			n, done, bad := p.header.push(data)
			if bad {
				p.inHeader = false
				p.header = partHeaderParser{}
				p.fail("Malformed part header")
				return
			}
			if done {
				p.inHeader = false
				p.startPart(p.header.header)
				p.header = partHeaderParser{}
			}
			if n >= len(data) {
				return
			}
			data = data[n:]
		}
		if p.postBoundary != 0 {
			if p.postBoundary == 1 {
				switch data[0] {
				case '-':
					p.postBoundary = 2
					data = data[1:]
				case '\r':
					p.postBoundary = 3
					data = data[1:]
				default:
					p.postBoundary = 0
				}
				if len(data) == 0 {
					return
				}
			}
			switch p.postBoundary {
			case 2:
				p.postBoundary = 0
				if data[0] == '-' {
					p.complete = true
					return
				}
				p.content([]byte{'-'})
			case 3:
				p.postBoundary = 0
				if data[0] == '\n' {
					data = data[1:]
					p.inHeader = true
					continue
				}
				p.content([]byte{'\r'})
			}
		}
		p.content(data)
		return
	}
}

// isCodeField reports whether a text field can change the form's "code"
// value. Only those fields are kept; the others are counted and dropped,
// so a form with many fields does not use memory.
func isCodeField(name string) bool {
	return name == "code" || strings.HasPrefix(name, "code[")
}

// content is the body of the open part.
func (p *formParser) content(data []byte) {
	if p.skipPart || p.failure != "" {
		return
	}
	switch {
	case p.inFile:
		room := uploadFileLimit - p.fileSize
		if len(data) > room {
			data = data[:room]
		}
		p.fileSize += len(data)
		if p.fileAccepted {
			p.fileData = append(p.fileData, data...)
		}
		if p.fileSize == uploadFileLimit {
			p.skipPart = true
			if p.fileAccepted {
				p.failTooLarge("File too large")
			}
		}
	case p.inField:
		room := uploadFieldLimit - p.fieldSize
		if len(data) > room {
			data = data[:room]
		}
		p.fieldSize += len(data)
		if isCodeField(p.fieldName) {
			p.fieldData = append(p.fieldData, data...)
		}
		if p.fieldSize == uploadFieldLimit {
			p.skipPart = true
			p.fieldTruncated = true
		}
	}
}

// boundary ends the open part.
func (p *formParser) boundary() {
	p.postBoundary = 1
	switch {
	case p.inFile:
		p.inFile = false
		if p.fileAccepted && p.failure == "" {
			p.stored, p.storedName, p.storedData = true, p.fileName, p.fileData
		}
		p.fileAccepted, p.fileData = false, nil
	case p.inField:
		p.inField = false
		data, size := p.fieldData, p.fieldSize
		p.fieldData, p.fieldSize = nil, 0
		switch {
		case !p.fieldNamed:
			p.fail("Field name missing")
		case p.fieldTruncated:
			p.failTooLarge("Field value too long")
		case isCodeField(p.fieldName):
			var value any = ""
			if size > 0 {
				value = decodeField(data, p.fieldCharset)
			}
			p.body.append(p.fieldName, value)
		}
	}
}

// startPart decides from a part's header what the part is.
func (p *formParser) startPart(header map[string][]string) {
	p.skipPart = false
	if p.parts == uploadMaxParts {
		p.failTooLarge("Too many parts")
		return
	}
	p.parts++
	partType, charset := "text/plain", "utf8"
	disposition := header["content-disposition"]
	if len(disposition) == 0 {
		p.skipPart = true
		return
	}
	kind, params, ok := parseDisposition(disposition[0])
	if !ok || kind != "form-data" {
		p.skipPart = true
		return
	}
	name, named := params["name"]
	named = named && name != ""
	filename, isFile := params["filename*"]
	if filename == "" {
		filename, isFile = params["filename"]
	}
	isFile = isFile && filename != ""
	if isFile {
		filename = baseName(filename)
	}
	if values := header["content-type"]; len(values) > 0 {
		if ct, ok := parseContentType(values[0]); ok {
			partType = ct.kind + "/" + ct.subtype
			if cs, ok := ct.params["charset"]; ok {
				charset = strings.ToLower(cs)
			}
		}
	}
	name = formDataEscapes.Replace(name)
	if partType != "application/octet-stream" && !isFile {
		if p.fields == uploadMaxFields {
			p.failTooLarge("Too many fields")
			return
		}
		p.fields++
		p.inField = true
		p.fieldName, p.fieldNamed, p.fieldCharset = name, named, charset
		p.fieldData, p.fieldSize, p.fieldTruncated = nil, 0, false
		return
	}
	p.inFile = true
	p.fileSize, p.fileAccepted, p.fileData = 0, false, nil
	switch {
	case !named:
		p.fail("Field name missing")
	case filename == "":
		// A file input that was left empty: its bytes are dropped.
	case name != uploadFileField || p.fileSlots <= 0:
		p.fail("Unexpected file field")
	case partType != uploadFileType:
		p.fail("Only JSON files allowed")
	default:
		p.fileSlots--
		p.fileAccepted = true
		p.fileName = formDataEscapes.Replace(filename)
	}
}

func baseName(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		path = path[i+1:]
	}
	if path == ".." || path == "." {
		return ""
	}
	return path
}

// decodeField turns a text field's bytes into a string by the charset of
// its part. An unknown charset gives undefined.
func decodeField(data []byte, charset string) any {
	switch charsetFamily(charset) {
	case "utf8":
		return decodeUTF8(data)
	case "latin1":
		return latin1(string(data))
	case "utf16le":
		u := make([]uint16, len(data)/2)
		for i := range u {
			u[i] = uint16(data[2*i]) | uint16(data[2*i+1])<<8
		}
		return fromUnits(u)
	case "base64":
		return base64.StdEncoding.EncodeToString(data)
	}
	return undefined
}

func charsetFamily(charset string) string {
	for range 2 {
		switch charset {
		case "utf-8", "utf8":
			return "utf8"
		case "latin1", "ascii", "us-ascii", "iso-8859-1", "iso8859-1", "iso88591", "iso_8859-1", "windows-1252", "iso_8859-1:1987", "cp1252", "x-cp1252":
			return "latin1"
		case "utf16le", "utf-16le", "ucs2", "ucs-2":
			return "utf16le"
		case "base64":
			return "base64"
		}
		charset = strings.ToLower(charset)
	}
	return ""
}

// --- part headers ---

// partHeaderParser reads the header lines of one part. It keeps the
// limits and the looseness of busboy's: 16 KiB, 2000 lines, names
// made of token characters, values of visible characters, folded lines.
type partHeaderParser struct {
	header    map[string][]string
	pairCount int
	byteCount int
	state     int // 0 name, 1 space before the value, 2 value
	name      []byte
	value     []byte
	crlf      int
}

// push consumes header bytes. n is how many were used, done is set at the
// empty line, bad for a malformed header.
func (h *partHeaderParser) push(chunk []byte) (n int, done, bad bool) {
	if h.header == nil {
		h.header = map[string][]string{}
	}
	pos, end := 0, len(chunk)
	count := func() bool {
		if h.byteCount == maxPartHeaderBytes {
			return false
		}
		h.byteCount++
		return true
	}
	for pos < end {
		switch h.state {
		case 0:
			c := chunk[pos]
			if !count() {
				return 0, false, true
			}
			if tokenChar[c] {
				h.name = append(h.name, c)
				pos++
				continue
			}
			if c != ':' || len(h.name) == 0 {
				return 0, false, true
			}
			pos++
			h.state = 1
		case 1:
			c := chunk[pos]
			if !count() {
				return 0, false, true
			}
			if c == ' ' || c == '\t' {
				pos++
				continue
			}
			h.state = 2
		case 2:
			switch h.crlf {
			case 0:
				c := chunk[pos]
				if !count() {
					return 0, false, true
				}
				if fieldVChar[c] {
					h.value = append(h.value, c)
					pos++
					continue
				}
				if c != '\r' {
					return 0, false, true
				}
				h.crlf = 1
				pos++
			case 1:
				if !count() {
					return 0, false, true
				}
				if chunk[pos] != '\n' {
					return 0, false, true
				}
				pos++
				h.crlf = 2
			case 2:
				if !count() {
					return 0, false, true
				}
				c := chunk[pos]
				if c == ' ' || c == '\t' {
					h.crlf = 0
					continue
				}
				h.pairCount++
				if h.pairCount < maxPartHeaderPairs {
					name := strings.ToLower(string(h.name))
					h.header[name] = append(h.header[name], latin1(string(h.value)))
				}
				if c == '\r' {
					h.crlf = 3
					pos++
				} else {
					h.crlf, h.state = 0, 0
					h.name, h.value = nil, nil
				}
			case 3:
				if !count() {
					return 0, false, true
				}
				if chunk[pos] != '\n' {
					return 0, false, true
				}
				return pos + 1, true, false
			}
		}
	}
	return pos, false, false
}

var tokenChar, fieldVChar, quotedTextChar, charsetChar, extendedValueChar [256]bool

func init() {
	for c := 0; c < 256; c++ {
		alnum := c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
		tokenChar[c] = alnum || strings.IndexByte("!#$%&'*+-.^_`|~", byte(c)) >= 0 && c != 0
		fieldVChar[c] = c == '\t' || (c >= 0x20 && c != 0x7F)
		quotedTextChar[c] = c == '\t' || (c >= 0x20 && c != 0x7F && c != '"' && c != '\\')
		charsetChar[c] = alnum || strings.IndexByte("!#$%&+-^_`{}~", byte(c)) >= 0 && c != 0
		extendedValueChar[c] = alnum || strings.IndexByte("!#$&+-.^_`|~", byte(c)) >= 0 && c != 0
	}
}

func isToken(r rune) bool         { return r < 256 && tokenChar[r] }
func isQuotedText(r rune) bool    { return r < 256 && quotedTextChar[r] }
func isCharsetChar(r rune) bool   { return r < 256 && charsetChar[r] }
func isExtendedValue(r rune) bool { return r < 256 && extendedValueChar[r] }

type contentType struct {
	kind, subtype string
	params        map[string]string
}

// parseContentType reads "type/subtype; name=value" from a header value.
func parseContentType(value string) (contentType, bool) {
	s := []rune(value)
	if len(s) == 0 {
		return contentType{}, false
	}
	i := 0
	for ; i < len(s); i++ {
		if !isToken(s[i]) {
			if s[i] != '/' || i == 0 {
				return contentType{}, false
			}
			break
		}
	}
	if i == len(s) {
		return contentType{}, false
	}
	out := contentType{kind: strings.ToLower(string(s[:i])), params: map[string]string{}}
	i++
	start := i
	for ; i < len(s); i++ {
		if !isToken(s[i]) {
			if i == start {
				return contentType{}, false
			}
			if !parseParams(s, i, out.params, false) {
				return contentType{}, false
			}
			break
		}
	}
	if i == start {
		return contentType{}, false
	}
	out.subtype = strings.ToLower(string(s[start:i]))
	return out, true
}

// parseDisposition reads "form-data; name=...; filename=..." from a
// Content-Disposition value.
func parseDisposition(value string) (kind string, params map[string]string, ok bool) {
	s := []rune(value)
	if len(s) == 0 {
		return "", nil, false
	}
	params = map[string]string{}
	i := 0
	for ; i < len(s); i++ {
		if !isToken(s[i]) {
			if !parseParams(s, i, params, true) {
				return "", nil, false
			}
			break
		}
	}
	return strings.ToLower(string(s[:i])), params, true
}

// parseParams reads the ;name=value list of a header value from position
// i. extended allows name*=charset'language'percent-encoded values.
func parseParams(s []rune, i int, params map[string]string, extended bool) bool {
	n := len(s)
	skipSpace := func() {
		for i < n && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
	}
	for i < n {
		skipSpace()
		if i == n {
			break
		}
		if s[i] != ';' {
			return false
		}
		i++
		skipSpace()
		if i == n {
			return false
		}
		nameStart := i
		for ; i < n; i++ {
			if !isToken(s[i]) {
				if s[i] != '=' {
					return false
				}
				break
			}
		}
		if i == n {
			return false
		}
		name := string(s[nameStart:i])
		var value string
		if extended && strings.HasSuffix(name, "*") {
			i++
			charsetStart := i
			for ; i < n; i++ {
				if !isCharsetChar(s[i]) {
					if s[i] != '\'' {
						return false
					}
					break
				}
			}
			if i == n {
				return false
			}
			charset := string(s[charsetStart:i])
			i++
			for ; i < n; i++ {
				if s[i] == '\'' {
					break
				}
			}
			if i == n {
				return false
			}
			i++
			if i == n {
				return false
			}
			var raw []rune
			for ; i < n; i++ {
				if isExtendedValue(s[i]) {
					raw = append(raw, s[i])
					continue
				}
				if s[i] != '%' {
					break
				}
				if i+2 >= n {
					return false
				}
				hi, lo := hexValue(uint16(min(s[i+1], 0xFFFF))), hexValue(uint16(min(s[i+2], 0xFFFF)))
				if hi < 0 || lo < 0 {
					return false
				}
				raw = append(raw, rune(hi<<4|lo))
				i += 2
			}
			decoded, ok := decodeParam(raw, charset)
			if !ok {
				return false
			}
			value = decoded
		} else {
			i++
			if i == n {
				return false
			}
			if s[i] == '"' {
				i++
				valueStart := i
				escaping := false
				var b []rune
				closed := false
				for ; i < n; i++ {
					c := s[i]
					if c == '\\' {
						if escaping {
							valueStart = i
							escaping = false
						} else {
							b = append(b, s[valueStart:i]...)
							escaping = true
						}
						continue
					}
					if c == '"' {
						if escaping {
							valueStart = i
							escaping = false
							continue
						}
						b = append(b, s[valueStart:i]...)
						closed = true
						break
					}
					if escaping {
						valueStart = i - 1
						escaping = false
					}
					if !isQuotedText(c) {
						return false
					}
				}
				if !closed {
					return false
				}
				i++
				value = string(b)
			} else {
				valueStart := i
				for ; i < n; i++ {
					if !isToken(s[i]) {
						if i == valueStart {
							return false
						}
						break
					}
				}
				value = string(s[valueStart:i])
			}
		}
		name = strings.ToLower(name)
		if _, seen := params[name]; !seen {
			params[name] = value
		}
	}
	return true
}

// decodeParam decodes an extended parameter value, given as one rune per
// byte, by its charset.
func decodeParam(raw []rune, charset string) (string, bool) {
	if len(raw) == 0 {
		return "", true
	}
	bytes := make([]byte, len(raw))
	for i, r := range raw {
		bytes[i] = byte(r)
	}
	switch charsetFamily(charset) {
	case "utf8":
		return decodeUTF8(bytes), true
	case "latin1":
		return string(raw), true
	case "utf16le":
		v, _ := decodeField(bytes, "utf16le").(string)
		return v, true
	case "base64":
		return base64.StdEncoding.EncodeToString(bytes), true
	}
	return "", false
}

// --- the form's text fields ---

// formNode is a list or object built from field names like code[], a[b]
// and a[2], as multer builds them. Lists can have gaps and
// are kept sparse.
type formNode struct {
	list   bool
	length int64
	items  map[int64]any
	obj    *Object
}

type formStep struct {
	index    int64
	isIndex  bool
	key      string
	nextList bool
	appendTo bool
	last     bool
}

var (
	formFirstKey = regexp.MustCompile(`^[^\[]*`)
	formDigits   = regexp.MustCompile(`^\[(\d+)\]`)
	formNormal   = regexp.MustCompile(`^\[([^\]]+)\]`)
)

func formPath(key string) []formStep {
	failure := []formStep{{key: key, last: true}}
	first := formFirstKey.FindString(key)
	if first == "" {
		return failure
	}
	steps := []formStep{{key: first}}
	pos := len(first)
	for pos < len(key) {
		tail := &steps[len(steps)-1]
		if strings.HasPrefix(key[pos:], "[]") {
			pos += 2
			tail.appendTo = true
			if pos != len(key) {
				return failure
			}
			continue
		}
		if m := formDigits.FindStringSubmatch(key[pos:]); m != nil {
			pos += len(m[0])
			tail.nextList = true
			index, err := strconv.ParseInt(m[1], 10, 64)
			if err != nil {
				return failure
			}
			steps = append(steps, formStep{index: index, isIndex: true, key: strconv.FormatInt(index, 10)})
			continue
		}
		if m := formNormal.FindStringSubmatch(key[pos:]); m != nil {
			pos += len(m[0])
			steps = append(steps, formStep{key: m[1]})
			continue
		}
		return failure
	}
	steps[len(steps)-1].last = true
	return steps
}

func (n *formNode) get(step formStep) any {
	if n.list {
		if step.isIndex {
			if v, ok := n.items[step.index]; ok {
				return v
			}
		}
		return undefined
	}
	if v, ok := n.obj.Get(step.key); ok {
		return v
	}
	return undefined
}

func (n *formNode) set(step formStep, v any) {
	if !n.list {
		n.obj.Set(step.key, v)
		return
	}
	if !step.isIndex || step.index > 4294967294 {
		return
	}
	if n.items == nil {
		n.items = map[int64]any{}
	}
	n.items[step.index] = v
	if step.index >= n.length {
		n.length = step.index + 1
	}
}

func (n *formNode) push(v any) {
	n.set(formStep{index: n.length, isIndex: true}, v)
}

// append adds one field to the form.
func (n *formNode) append(name string, value any) {
	context := n
	for _, step := range formPath(name) {
		current := context.get(step)
		if step.last {
			context.setLast(step, current, value)
			return
		}
		node, isNode := current.(*formNode)
		switch {
		case current == undefined:
			node = &formNode{obj: NewObject()}
			if step.nextList {
				node = &formNode{list: true}
			}
			context.set(step, node)
		case isNode && !node.list:
		case isNode && step.nextList:
		case isNode:
			obj := &formNode{obj: NewObject()}
			for i := int64(0); i < node.length; i++ {
				if item, ok := node.items[i]; ok && item != undefined {
					obj.obj.Set(strconv.FormatInt(i, 10), item)
				}
			}
			context.set(step, obj)
			node = obj
		default:
			node = &formNode{obj: NewObject()}
			node.obj.Set("", current)
			context.set(step, node)
		}
		context = node
	}
}

func (n *formNode) setLast(step formStep, current, value any) {
	node, isNode := current.(*formNode)
	switch {
	case current == undefined:
		if step.appendTo {
			list := &formNode{list: true}
			list.push(value)
			n.set(step, list)
		} else {
			n.set(step, value)
		}
	case isNode && node.list:
		node.push(value)
	case isNode:
		empty := formStep{key: "", last: true}
		node.setLast(empty, node.get(empty), value)
	default:
		list := &formNode{list: true}
		list.push(current)
		list.push(value)
		n.set(step, list)
	}
}

// value returns a top-level field as the route reads it.
func (n *formNode) value(name string) any {
	v, ok := n.obj.Get(name)
	if !ok {
		return undefined
	}
	return v
}
