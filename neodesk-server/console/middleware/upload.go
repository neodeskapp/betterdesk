package middleware

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The codes of an UploadError the console's routes look at.
const (
	UploadFileTooLarge    = "LIMIT_FILE_SIZE"
	UploadUnexpectedFile  = "LIMIT_UNEXPECTED_FILE"
	UploadFieldTooLong    = "LIMIT_FIELD_VALUE"
	UploadMissingName     = "MISSING_FIELD_NAME"
	UploadArrayIndexLarge = "LIMIT_FIELD_ARRAY_INDEX"
	UploadFieldNameLong   = "LIMIT_FIELD_KEY"
	UploadTooManyFields   = "LIMIT_FIELD_COUNT"
	UploadTooManyParts    = "LIMIT_PART_COUNT"
)

var uploadMessages = map[string]string{
	UploadFileTooLarge:    "File too large",
	UploadUnexpectedFile:  "Unexpected file field",
	UploadFieldTooLong:    "Field value too long",
	UploadMissingName:     "Field name missing",
	UploadArrayIndexLarge: "Field name array index too large",
	UploadFieldNameLong:   "Field name too long",
	UploadTooManyFields:   "Too many fields",
	UploadTooManyParts:    "Too many parts",
}

// UploadError is a multipart upload that broke one of the limits.
type UploadError struct {
	// Code is one of the Upload... constants.
	Code string
	// Field is the form field the error is about, if it has one.
	Field string
	// Filename is the file's name on the client, for file errors.
	Filename string
	Message  string
}

func (e *UploadError) Error() string { return e.Message }

func uploadError(code, field, filename string) *UploadError {
	return &UploadError{Code: code, Field: field, Filename: filename, Message: uploadMessages[code]}
}

// UploadErrorCode is err.code of the JavaScript routes: the code of an
// UploadError, "" for any other error.
func UploadErrorCode(err error) string {
	var ue *UploadError
	if errors.As(err, &ue) {
		return ue.Code
	}
	return ""
}

// UploadedFile is the file of an upload, req.file of the JavaScript
// routes.
type UploadedFile struct {
	// FieldName is the form field the file came in.
	FieldName string
	// OriginalName is the file's name on the client, without any folder.
	// It comes from the client: never use it as a path. A name that is not
	// ASCII arrives with each of its bytes as one Latin-1 letter, unless
	// the client sent it as filename*.
	OriginalName string
	// Encoding is the part's Content-Transfer-Encoding, 7bit by default.
	Encoding string
	// MIMEType is the part's Content-Type as the client claims it.
	MIMEType string
	// Size is the file's length in bytes.
	Size int64
	// Buffer holds the file when it was kept in memory.
	Buffer []byte
	// Destination, Filename and Path say where the file was written when
	// it went to disk.
	Destination string
	Filename    string
	Path        string
}

// Upload is a parsed upload request.
type Upload struct {
	// File is the uploaded file, nil when the request had none or the
	// filter skipped it.
	File *UploadedFile
	// Fields is req.body: the text fields of the form. A field sent twice
	// is an array, a[b] is a member of the object a, a[] and a[0] build
	// arrays.
	Fields JSONObject
}

// UploadOptions describe the one file a route takes.
type UploadOptions struct {
	// Field is the form field that may carry the file. A file in another
	// field, or a second file, is an UploadUnexpectedFile error.
	Field string
	// MaxFileSize is the largest file in bytes; a larger one is an
	// UploadFileTooLarge error. Zero means no limit.
	MaxFileSize int64
	// Dir is the folder the file is written to. With "" it is kept in
	// memory (UploadedFile.Buffer).
	Dir string
	// Filename names the file in Dir. It sees the file before its bytes
	// are read. Without it the name is 32 random hex digits.
	Filename func(file *UploadedFile) (string, error)
	// Filter decides before the bytes are read whether the file is taken.
	// false skips it without an error, an error ends the upload with it.
	Filter func(file *UploadedFile) (bool, error)
}

const (
	// maxFieldSize is the longest text field of an upload, 1 MB.
	maxFieldSize = 1 << 20
	// maxFieldIndex is the largest array index a field name may use.
	maxFieldIndex = 10000
	// maxFieldSlots is how many places the arrays of a form's fields may
	// have together, holes included: a hole is written as null.
	maxFieldSlots = 100000
	// maxFieldNameSize is the longest field name in bytes, the default of
	// multer's fieldNameSize.
	maxFieldNameSize = 100
	// maxFields is how many text fields a form may have, net/http's limit
	// of 1000 parts for a form it reads; maxParts counts every part,
	// skipped files and parts without a name included.
	maxFields = 1000
	maxParts  = 2000
)

// ParseUpload reads a multipart/form-data request with one file field, as
// upload.single(field) of the JavaScript routes did. A request of another
// type is not read: the result then has no file and the fields of
// Body(r).
//
// On an error nothing is left behind: a file already written is removed,
// and the rest of the request is read and dropped before the function
// returns, so the client gets the answer. The routes answer in two ways:
// the settings uploads with
//
//	400 {"success":false,"error":"File too large (max 2 MB)"}   for UploadFileTooLarge
//	400 {"success":false,"error":<err.Error()>}                  for anything else
//
// and the others pass the error to the console's error handler, which
// answers 500.
func ParseUpload(r *http.Request, opts UploadOptions) (*Upload, error) {
	mt, ok := requestMediaType(r)
	if !ok || mt.major != "multipart" {
		return &Upload{Fields: Body(r)}, nil
	}
	header := headerValue(r.Header, "Content-Type")
	major, minor, params, ok := partContentType(header)
	if !ok {
		return nil, errors.New("Malformed content type")
	}
	if major != "multipart" || minor != "form-data" {
		return nil, errors.New("Unsupported content type: " + latin1(header))
	}
	boundary, hasBoundary := params["boundary"]
	if !hasBoundary {
		return nil, errors.New("Multipart: Boundary not found")
	}

	up := &Upload{}
	fields := newFieldObject()
	slots := 1
	parts, fieldCount := 0, 0
	fail := func(err error) (*Upload, error) {
		if up.File != nil && up.File.Path != "" {
			_ = os.Remove(up.File.Path)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		return nil, err
	}
	if boundary == "" {
		// Nothing can end a form whose boundary is empty
		return fail(errors.New("Unexpected end of form"))
	}
	reader := multipart.NewReader(r.Body, boundary)
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail(formError(err))
		}
		if parts++; parts > maxParts {
			return fail(uploadError(UploadTooManyParts, "", ""))
		}
		p, ok := describePart(part)
		if !ok {
			continue
		}
		if !p.isFile {
			if !p.hasName {
				return fail(uploadError(UploadMissingName, "", ""))
			}
			if fieldCount++; fieldCount > maxFields {
				return fail(uploadError(UploadTooManyFields, "", ""))
			}
			if len(p.name) > maxFieldNameSize {
				return fail(uploadError(UploadFieldNameLong, p.name, ""))
			}
			raw, err := io.ReadAll(io.LimitReader(part, maxFieldSize+1))
			if err != nil {
				return fail(formError(err))
			}
			if len(raw) > maxFieldSize {
				return fail(uploadError(UploadFieldTooLong, p.name, ""))
			}
			value := decodeUTF8(raw)
			if isLatin1Charset(p.charset) {
				value = latin1(string(raw))
			}
			if err := fields.append(p.name, value); err != nil {
				return fail(err)
			}
			continue
		}

		if !p.hasName {
			return fail(uploadError(UploadMissingName, "", ""))
		}
		if !p.hasFilename {
			continue
		}
		file := &UploadedFile{FieldName: p.name, OriginalName: p.filename, Encoding: p.encoding, MIMEType: p.mimeType}
		if p.name != opts.Field || slots <= 0 {
			return fail(uploadError(UploadUnexpectedFile, p.name, p.filename))
		}
		if opts.Filter != nil {
			accept, err := opts.Filter(file)
			if err != nil {
				return fail(err)
			}
			if !accept {
				continue
			}
		}
		slots--
		if err := storeUpload(part, file, opts); err != nil {
			return fail(err)
		}
		up.File = file
	}
	if fields.slots() > maxFieldSlots {
		return fail(uploadError(UploadArrayIndexLarge, "", ""))
	}
	up.Fields = fields.object()
	return up, nil
}

// formError names a broken multipart body in the words clients of the
// console know.
func formError(err error) error {
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return errors.New("Unexpected end of form")
	case strings.Contains(err.Error(), "malformed MIME header"):
		return errors.New("Malformed part header")
	}
	return err
}

// storeUpload reads the file's bytes into memory or onto the disk and
// stops one byte past the limit.
func storeUpload(part io.Reader, file *UploadedFile, opts UploadOptions) error {
	var source io.Reader = part
	if opts.MaxFileSize > 0 {
		source = io.LimitReader(part, opts.MaxFileSize+1)
	}
	tooLarge := func(n int64) bool { return opts.MaxFileSize > 0 && n > opts.MaxFileSize }

	if opts.Dir == "" {
		data, err := io.ReadAll(source)
		if err != nil {
			return formError(err)
		}
		if tooLarge(int64(len(data))) {
			return uploadError(UploadFileTooLarge, file.FieldName, file.OriginalName)
		}
		file.Buffer, file.Size = data, int64(len(data))
		return nil
	}

	name := ""
	if opts.Filename != nil {
		given, err := opts.Filename(file)
		if err != nil {
			return err
		}
		name = given
	} else {
		random := make([]byte, 16)
		if _, err := rand.Read(random); err != nil {
			return err
		}
		name = hex.EncodeToString(random)
	}
	full := filepath.Join(opts.Dir, name)
	out, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		return err
	}
	written, err := io.Copy(out, source)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	switch {
	case err != nil:
		_ = os.Remove(full)
		return formError(err)
	case tooLarge(written):
		_ = os.Remove(full)
		return uploadError(UploadFileTooLarge, file.FieldName, file.OriginalName)
	}
	file.Destination, file.Filename, file.Path, file.Size = opts.Dir, name, full, written
	return nil
}

// partInfo is what the headers of one part say.
type partInfo struct {
	name        string
	hasName     bool
	filename    string
	hasFilename bool
	isFile      bool
	mimeType    string
	charset     string
	encoding    string
}

// describePart reads a part's headers. ok is false for a part that is not
// form data; such a part is skipped.
func describePart(part *multipart.Part) (partInfo, bool) {
	info := partInfo{mimeType: "text/plain", charset: "utf8", encoding: "7bit"}
	dispositions := part.Header["Content-Disposition"]
	if len(dispositions) == 0 {
		return info, false
	}
	kind, params, ok := parseDisposition(dispositions[0])
	if !ok || kind != "form-data" {
		return info, false
	}
	if name := params["name"]; name != "" {
		info.name, info.hasName = formDataName(name), true
	}
	filename := params["filename*"]
	if filename == "" {
		filename = params["filename"]
	}
	sentFilename := filename != ""
	if base := clientBasename(filename); base != "" {
		info.filename, info.hasFilename = formDataName(base), true
	}
	if types := part.Header["Content-Type"]; len(types) > 0 {
		if major, minor, typeParams, ok := partContentType(types[0]); ok {
			info.mimeType = major + "/" + minor
			if cs, has := typeParams["charset"]; has {
				info.charset = strings.ToLower(cs)
			}
		}
	}
	if encodings := part.Header["Content-Transfer-Encoding"]; len(encodings) > 0 {
		info.encoding = strings.ToLower(encodings[0])
	}
	// A part with a file name is a file, also when nothing is left of the
	// name after its folders are cut off; such a file is skipped.
	info.isFile = info.mimeType == "application/octet-stream" || sentFilename
	return info, true
}

func isLatin1Charset(name string) bool {
	switch name {
	case "latin1", "ascii", "us-ascii", "iso-8859-1", "iso8859-1", "iso88591", "iso_8859-1", "windows-1252", "iso_8859-1:1987", "cp1252", "x-cp1252":
		return true
	}
	return false
}

var formDataEscapes = regexp.MustCompile(`(?i)%0A|%0D|%22`)

// formDataName undoes the three escapes browsers use in field and file
// names: %0A, %0D and %22. Any other % is part of the name.
func formDataName(s string) string {
	return formDataEscapes.ReplaceAllStringFunc(s, func(m string) string {
		switch strings.ToUpper(m) {
		case "%0A":
			return "\n"
		case "%0D":
			return "\r"
		}
		return `"`
	})
}

// clientBasename cuts a file name sent by a client down to its last part,
// whichever slash the client uses.
func clientBasename(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	if name == ".." || name == "." {
		return ""
	}
	return name
}

func isQuotedTextByte(c byte) bool {
	return c == '\t' || c == ' ' || c == '!' || (c >= 0x23 && c <= 0x5b) || (c >= 0x5d && c <= 0x7e) || c >= 0x80
}

// partContentType reads a Content-Type the way the multipart parser does:
// type "/" subtype, then parameters.
func partContentType(s string) (major, minor string, params map[string]string, ok bool) {
	i := 0
	for i < len(s) && isTokenByte(s[i]) {
		i++
	}
	if i == 0 || i >= len(s) || s[i] != '/' {
		return "", "", nil, false
	}
	major = strings.ToLower(s[:i])
	start := i + 1
	i = start
	for i < len(s) && isTokenByte(s[i]) {
		i++
	}
	if i == start {
		return "", "", nil, false
	}
	minor = strings.ToLower(s[start:i])
	params, ok = headerParams(s, i, false)
	return major, minor, params, ok
}

// parseDisposition reads a Content-Disposition: its kind and parameters.
func parseDisposition(s string) (kind string, params map[string]string, ok bool) {
	if s == "" {
		return "", nil, false
	}
	i := 0
	for i < len(s) && isTokenByte(s[i]) {
		i++
	}
	params, ok = headerParams(s, i, true)
	return strings.ToLower(s[:i]), params, ok
}

// headerParams reads *( ";" name "=" value ) from position i. A value is
// a token or a quoted string. The first value of a name counts.
//
// With disposition set (a Content-Disposition), name*=charset'lang'value
// is read too, and a quoted value is UTF-8, as browsers send file and
// field names; bytes that are not UTF-8 become U+FFFD. Otherwise bytes
// above 0x7f are Latin-1 letters.
func headerParams(s string, i int, disposition bool) (map[string]string, bool) {
	params := map[string]string{}
	skipSpace := func() {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
	}
	for i < len(s) {
		skipSpace()
		if i == len(s) {
			break
		}
		if s[i] != ';' {
			return nil, false
		}
		i++
		skipSpace()
		if i == len(s) {
			return nil, false
		}
		start := i
		for i < len(s) && isTokenByte(s[i]) {
			i++
		}
		if i == len(s) || s[i] != '=' {
			return nil, false
		}
		name := s[start:i]
		var value string
		if disposition && strings.HasSuffix(name, "*") {
			text, next, ok := extendedValue(s, i+1)
			if !ok {
				return nil, false
			}
			value, i = text, next
		} else {
			i++
			if i == len(s) {
				return nil, false
			}
			if s[i] == '"' {
				i++
				var b []byte
				closed := false
				for i < len(s) {
					c := s[i]
					// Only \" and \\ are escapes. Any other backslash is a
					// letter, as in a Windows path.
					if c == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\') {
						b = append(b, s[i+1])
						i += 2
						continue
					}
					if c == '\\' {
						b = append(b, c)
						i++
						continue
					}
					if c == '"' {
						closed = true
						i++
						break
					}
					if !isQuotedTextByte(c) {
						return nil, false
					}
					b = append(b, c)
					i++
				}
				if !closed {
					return nil, false
				}
				if disposition {
					value = decodeUTF8(b)
				} else {
					value = latin1(string(b))
				}
			} else {
				start = i
				for i < len(s) && isTokenByte(s[i]) {
					i++
				}
				if i == start {
					return nil, false
				}
				value = s[start:i]
			}
		}
		name = strings.ToLower(name)
		if _, seen := params[name]; !seen {
			params[name] = value
		}
	}
	return params, true
}

// extendedValue reads charset'language'percent-encoded text.
func extendedValue(s string, i int) (string, int, bool) {
	start := i
	for i < len(s) && s[i] != '\'' {
		c := s[i]
		if !isTokenByte(c) && c != '{' && c != '}' {
			return "", 0, false
		}
		i++
	}
	if i == len(s) {
		return "", 0, false
	}
	charsetName := strings.ToLower(s[start:i])
	i++
	for i < len(s) && s[i] != '\'' {
		i++
	}
	if i == len(s) {
		return "", 0, false
	}
	i++
	if i == len(s) {
		return "", 0, false
	}
	var b []byte
	for i < len(s) {
		c := s[i]
		if c == '%' {
			if i+2 >= len(s) || !isHexByte(s[i+1]) || !isHexByte(s[i+2]) {
				return "", 0, false
			}
			b = append(b, hexValue(s[i+1])<<4|hexValue(s[i+2]))
			i += 3
			continue
		}
		if !isTokenByte(c) || c == '*' || c == '\'' || c == '%' {
			break
		}
		b = append(b, c)
		i++
	}
	switch {
	case charsetName == "utf-8" || charsetName == "utf8":
		return decodeUTF8(b), i, true
	case isLatin1Charset(charsetName):
		return latin1(string(b)), i, true
	}
	return "", 0, false
}

// fieldNode is one node of the text fields of an upload: a string, an
// object or an array. An array keeps its elements by index and its length
// as JavaScript counts it, so a hole costs nothing until the array is
// written out, where it is null.
type fieldNode struct {
	kind    Kind
	text    string
	keys    []string
	members map[string]*fieldNode
	at      map[int]*fieldNode
	length  int
}

func newFieldArray(items ...*fieldNode) *fieldNode {
	n := &fieldNode{kind: KindArray, at: map[int]*fieldNode{}}
	for _, item := range items {
		n.push(item)
	}
	return n
}

// push adds an element at the end of an array.
func (n *fieldNode) push(item *fieldNode) {
	n.at[n.length] = item
	n.length++
}

// slots counts the places of the arrays below n, holes included.
func (n *fieldNode) slots() int {
	total := 0
	switch n.kind {
	case KindArray:
		total = n.length
		for _, item := range n.at {
			total += item.slots()
		}
	case KindObject:
		for _, member := range n.members {
			total += member.slots()
		}
	}
	return total
}

func newFieldObject() *fieldNode {
	return &fieldNode{kind: KindObject, members: map[string]*fieldNode{}}
}

type fieldStep struct {
	key    string
	index  int
	array  bool
	append bool
	// nextArray says the step after this one is an array index
	nextArray bool
}

var (
	fieldDigits = regexp.MustCompile(`^\[([0-9]+)\]`)
	fieldName   = regexp.MustCompile(`^\[([^\]]+)\]`)
)

// fieldPath splits a field name like a[b][0][] into steps. A name that
// does not follow that form is one literal key.
func fieldPath(key string) ([]fieldStep, error) {
	literal := []fieldStep{{key: key}}
	first := key
	if i := strings.IndexByte(key, '['); i >= 0 {
		first = key[:i]
	}
	if first == "" {
		return literal, nil
	}
	steps := []fieldStep{{key: first}}
	rest := key[len(first):]
	for rest != "" {
		tail := &steps[len(steps)-1]
		if strings.HasPrefix(rest, "[]") {
			tail.append = true
			if rest = rest[2:]; rest != "" {
				return literal, nil
			}
			continue
		}
		if m := fieldDigits.FindStringSubmatch(rest); m != nil {
			index, err := strconv.Atoi(m[1])
			if err != nil || index > maxFieldIndex {
				return nil, uploadError(UploadArrayIndexLarge, key, "")
			}
			tail.nextArray = true
			steps = append(steps, fieldStep{index: index, key: strconv.Itoa(index), array: true})
			rest = rest[len(m[0]):]
			continue
		}
		if m := fieldName.FindStringSubmatch(rest); m != nil {
			steps = append(steps, fieldStep{key: m[1]})
			rest = rest[len(m[0]):]
			continue
		}
		return literal, nil
	}
	return steps, nil
}

func (n *fieldNode) get(step fieldStep) *fieldNode {
	if n.kind == KindArray {
		if !step.array {
			return nil
		}
		return n.at[step.index]
	}
	return n.members[step.key]
}

func (n *fieldNode) put(step fieldStep, child *fieldNode) {
	if n.kind == KindArray {
		// A name on an array is not part of what the array holds
		if !step.array {
			return
		}
		n.at[step.index] = child
		n.length = max(n.length, step.index+1)
		return
	}
	if _, seen := n.members[step.key]; !seen {
		n.keys = append(n.keys, step.key)
	}
	n.members[step.key] = child
}

func scalarNode(text string) *fieldNode { return &fieldNode{kind: KindString, text: text} }

// append adds one field. A name seen before turns its value into an
// array.
func (n *fieldNode) append(key, value string) error {
	steps, err := fieldPath(key)
	if err != nil {
		return err
	}
	context := n
	for i, step := range steps {
		current := context.get(step)
		if i == len(steps)-1 {
			context.setLast(step, current, value)
			break
		}
		switch {
		case current == nil:
			child := newFieldObject()
			if step.nextArray {
				child = newFieldArray()
			}
			context.put(step, child)
			context = child
		case current.kind == KindObject, current.kind == KindArray && step.nextArray:
			context = current
		case current.kind == KindArray:
			obj := newFieldObject()
			indexes := make([]int, 0, len(current.at))
			for index := range current.at {
				indexes = append(indexes, index)
			}
			slices.Sort(indexes)
			for _, index := range indexes {
				obj.put(fieldStep{key: strconv.Itoa(index)}, current.at[index])
			}
			context.put(step, obj)
			context = obj
		default:
			obj := newFieldObject()
			obj.put(fieldStep{}, current)
			context.put(step, obj)
			context = obj
		}
	}
	return nil
}

func (n *fieldNode) setLast(step fieldStep, current *fieldNode, value string) {
	switch {
	case current == nil:
		if step.append {
			n.put(step, newFieldArray(scalarNode(value)))
		} else {
			n.put(step, scalarNode(value))
		}
	case current.kind == KindArray:
		current.push(scalarNode(value))
	case current.kind == KindObject:
		current.setLast(fieldStep{}, current.members[""], value)
	default:
		n.put(step, newFieldArray(current, scalarNode(value)))
	}
}

func (n *fieldNode) writeJSON(buf *bytes.Buffer) {
	switch {
	case n == nil:
		buf.WriteString("null")
	case n.kind == KindObject:
		obj := n.object()
		raw, _ := obj.MarshalJSON()
		buf.Write(raw)
	case n.kind == KindArray:
		buf.WriteByte('[')
		for i := range n.length {
			if i > 0 {
				buf.WriteByte(',')
			}
			n.at[i].writeJSON(buf)
		}
		buf.WriteByte(']')
	default:
		buf.Write(stringValue(n.text).raw)
	}
}

// object turns an object node into the JSONObject handlers read.
func (n *fieldNode) object() JSONObject {
	obj := JSONObject{values: map[string]Value{}}
	for _, key := range n.keys {
		var buf bytes.Buffer
		n.members[key].writeJSON(&buf)
		obj.set(key, rawValue(buf.Bytes()))
	}
	obj.sortKeys()
	return obj
}

var safeSegment = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// ConfinedUploadPath checks a path before an upload's file is deleted: it
// has to lie below the system's temporary folder or below dataDir, and
// every part of it below that folder may only have letters, digits, dot,
// underscore and dash. It returns the cleaned path.
func ConfinedUploadPath(filePath, dataDir string) (string, bool) {
	if filePath == "" || strings.ContainsRune(filePath, 0) {
		return "", false
	}
	resolved, err := filepath.Abs(filePath)
	if err != nil {
		return "", false
	}
	roots := []string{os.TempDir()}
	if dataDir != "" {
		roots = append(roots, dataDir)
	}
	for _, root := range roots {
		root, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, resolved)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		safe := true
		for _, segment := range strings.Split(rel, string(filepath.Separator)) {
			if !safeSegment.MatchString(segment) {
				safe = false
				break
			}
		}
		if safe {
			return resolved, true
		}
	}
	return "", false
}

// RemoveUploadedFile deletes the file of an upload that a route does not
// keep after all. It only deletes below the temporary folder and dataDir
// (see ConfinedUploadPath) and reports whether it did.
func RemoveUploadedFile(file *UploadedFile, dataDir string) bool {
	if file == nil || file.Path == "" {
		return false
	}
	confined, ok := ConfinedUploadPath(file.Path, dataDir)
	if !ok {
		return false
	}
	return os.Remove(confined) == nil
}
