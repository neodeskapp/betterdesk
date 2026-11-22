package middleware

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// trackedBody counts what the parsers take from a request body and the
// most they hold of it at once is checked through what they hand on.
type trackedBody struct {
	r    io.Reader
	read int
}

func (b *trackedBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.read += n
	return n, err
}

func (b *trackedBody) Close() error { return nil }

const fuzzLimit = 256

// FuzzJSONBody: whatever the bytes and the Content-Type, the parser never
// panics, never keeps more than the limit, and accepts exactly the bodies
// that are an object or an array for encoding/json (after the charset is
// decoded). An accepted object reads like JSON.parse made it: the last of
// two members with one name counts.
func FuzzJSONBody(f *testing.F) {
	seeds := []string{`{}`, `[]`, `{"a":1}`, ` {"a":[1,2,{"b":null}]} `, `{"a":1,"a":2}`, `{"a":1} x`, `1`, `"s"`, `null`, ``, ` `, `{"a":`, `{'a':1}`, `[1,]`,
		"\xEF\xBB\xBF{\"a\":1}", "{\"a\":\"\xff\"}", `{"a":"\ud800"}`, `{"id":12345678901234567890,"f":1.0,"e":1e400}`, strings.Repeat("[", 300) + strings.Repeat("]", 300),
		`{"a":"` + strings.Repeat("x", 300) + `"}`, `{"__proto__":1,"constructor":{}}`, "{\"a\":1}\x00", `{"a":01}`, `{"a":+1}`, `{"1":1,"0":0,"b":2,"a":3}`}
	types := []string{"application/json", "application/json; charset=utf-8", "application/json; charset=utf-16le", "application/json; charset=utf-16",
		"application/json; charset=latin1", "APPLICATION/JSON", "application/json;", "text/plain", "application/vnd.api+json", "", "application/json; charset=\"utf-8\"",
		"application/x-www-form-urlencoded"}
	for i, s := range seeds {
		f.Add([]byte(s), types[i%len(types)], uint8(i))
		f.Add([]byte(s), "application/json", uint8(0))
	}
	f.Fuzz(func(t *testing.T, data []byte, contentType string, mode uint8) {
		chunked := mode&1 != 0
		encoding := []string{"", "gzip", "deflate", "br"}[int(mode>>1)&3]
		sent := data
		if encoding == "gzip" && mode&8 != 0 {
			// a well-formed gzip body; otherwise the bytes go out as they are
			var buf bytes.Buffer
			zw := gzip.NewWriter(&buf)
			_, _ = zw.Write(data)
			_ = zw.Close()
			sent = buf.Bytes()
		}
		body := &trackedBody{r: bytes.NewReader(sent)}
		req := httptest.NewRequest("POST", "/api/x", body)
		req.ContentLength = int64(len(sent))
		req.Header.Set("Content-Length", "1")
		if chunked {
			req.ContentLength = -1
			req.TransferEncoding = []string{"chunked"}
			req.Header.Del("Content-Length")
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if encoding != "" {
			req.Header.Set("Content-Encoding", encoding)
		}

		var gotErr *BodyError
		var gotStatus int
		var passed *http.Request
		Bodies(BodyOptions{Limit: fuzzLimit, OnError: func(_ http.ResponseWriter, _ *http.Request, status int, err error) {
			gotStatus = status
			if !errors.As(err, &gotErr) {
				t.Fatalf("OnError with %T, want *BodyError", err)
			}
		}})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { passed = r })).ServeHTTP(httptest.NewRecorder(), req)

		if (passed == nil) == (gotErr == nil) {
			t.Fatalf("next called: %v, error: %v", passed != nil, gotErr)
		}
		if gotErr != nil {
			if gotStatus != gotErr.Status || (gotStatus != 400 && gotStatus != 413 && gotStatus != 415) || gotErr.Message == "" {
				t.Fatalf("error %+v with status %d", gotErr, gotStatus)
			}
			return
		}
		raw := BodyValue(passed).Raw()
		if len(raw) > fuzzLimit {
			t.Fatalf("%d bytes kept, the limit is %d", len(raw), fuzzLimit)
		}
		if !json.Valid(raw) || (raw[0] != '{' && raw[0] != '[') {
			t.Fatalf("kept %q, which is no JSON object or array", raw)
		}
		mt, isBody := requestMediaType(req)
		if !isBody || (mt.full != "application/json" && mt.full != "application/x-www-form-urlencoded") {
			if body.read != 0 || BodyParsed(passed) || string(raw) != "{}" {
				t.Fatalf("a body of type %q was read (%d bytes)", contentType, body.read)
			}
			return
		}

		// Reading the members never panics, and agrees with encoding/json
		obj := Body(passed)
		var reference map[string]json.RawMessage
		if raw[0] == '{' {
			if err := json.Unmarshal(raw, &reference); err != nil {
				t.Fatalf("encoding/json refuses what was kept: %v", err)
			}
			if obj.Len() != len(reference) {
				t.Fatalf("%d members, encoding/json has %d in %q", obj.Len(), len(reference), raw)
			}
		} else if obj.Len() != 0 {
			t.Fatalf("an array body has members")
		}
		for _, key := range obj.Keys() {
			v := obj.Get(key)
			want, ok := reference[key]
			if !ok || !bytes.Equal(bytes.TrimSpace(want), bytes.TrimSpace(v.Raw())) {
				t.Fatalf("member %q is %q, encoding/json has %q (the last one of a name counts)", key, v.Raw(), want)
			}
			_ = v.Truthy()
			_ = v.Text("")
			_ = v.Int(0)
			_ = v.Flag(false)
			_, _ = v.Str()
			_, _ = v.Number()
			_, _ = v.Float()
			if inner, ok := v.Object(); ok {
				_ = inner.Keys()
			}
			_, _ = v.Array()
		}

		// The rule itself, for a plain UTF-8 JSON body
		if mt.full == "application/json" && encoding == "" && lookupCharset(charsetOf(mt)) == charsetUTF8 {
			text := bytes.TrimPrefix([]byte(decodeUTF8(data)), []byte(byteOrderMark))
			trimmed := bytes.Trim(text, " \t\n\r")
			wantAccept := len(text) == 0 || (len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') && json.Valid(trimmed))
			if !wantAccept {
				t.Fatalf("accepted %q", data)
			}
		}
	})
}

func charsetOf(mt mediaType) string {
	if cs := strings.ToLower(mt.params["charset"]); cs != "" {
		return cs
	}
	return "utf-8"
}

// FuzzJSONBodyRefusals is the other half of the rule: a plain UTF-8 JSON
// body that encoding/json takes as an object or array is never refused,
// unless it is over the limit.
func FuzzJSONBodyRefusals(f *testing.F) {
	for _, s := range []string{`{}`, `[]`, `{"a":1}`, ` [1,2] `, "\xEF\xBB\xBF{}", `{"a":{"b":[]}}`, ``, `{"a":"` + strings.Repeat("x", 300) + `"}`,
		`{"a":`, `{} x`, `1`, `[1,]`, ` `, "{\"a\":1}\x00"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		req := httptest.NewRequest("POST", "/api/x", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Length", "1")
		var gotErr *BodyError
		Bodies(BodyOptions{Limit: fuzzLimit, OnError: func(_ http.ResponseWriter, _ *http.Request, _ int, err error) {
			errors.As(err, &gotErr)
		}})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(httptest.NewRecorder(), req)

		text := bytes.TrimPrefix([]byte(decodeUTF8(data)), []byte(byteOrderMark))
		trimmed := bytes.Trim(text, " \t\n\r")
		valid := len(text) == 0 || (len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') && json.Valid(trimmed))
		switch {
		case len(data) > fuzzLimit:
			if gotErr == nil || gotErr.Status != 413 {
				t.Fatalf("%d bytes with a limit of %d: %v", len(data), fuzzLimit, gotErr)
			}
		case valid && gotErr != nil:
			t.Fatalf("refused %q: %v", data, gotErr)
		case !valid && (gotErr == nil || gotErr.Status != 400 || gotErr.Type != ErrTypeParseFailed):
			t.Fatalf("%q: %v, want 400 %s", data, gotErr, ErrTypeParseFailed)
		}
	})
}

// FuzzFormBody: a form body never panics the parser, yields text that is
// UTF-8, and for bodies without escapes gives back exactly the pairs that
// were sent.
func FuzzFormBody(f *testing.F) {
	for _, s := range []string{"a=1&b=2", "a=1&a=2", "a[]=1&a[b]=2", "a", "=", "&&", "a+b=c%2Bd", "n=%C3%A9", "bad=%zz&x=%E9", "x=%ED%A0%80", "%=%", "a=b=c",
		"\xff=\xfe", "\xEF\xBB\xBFa=1", strings.Repeat("k=v&", 1000), strings.Repeat("&", 1001), "x=\xf0\x9f\x98\x80%E9", "__proto__=1&constructor=2", "a=%00"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		req := httptest.NewRequest("POST", "/login", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Content-Length", "1")
		var gotErr *BodyError
		var passed *http.Request
		Bodies(BodyOptions{Limit: 8192, OnError: func(_ http.ResponseWriter, _ *http.Request, _ int, err error) {
			errors.As(err, &gotErr)
		}})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { passed = r })).ServeHTTP(httptest.NewRecorder(), req)

		text := strings.TrimPrefix(decodeUTF8(data), byteOrderMark)
		switch {
		case len(data) > 8192:
			if gotErr == nil || gotErr.Status != 413 || gotErr.Type != ErrTypeTooLarge {
				t.Fatalf("a body over the limit: %v", gotErr)
			}
			return
		case text != "" && strings.Count(text, "&") >= maxFormParameters:
			if gotErr == nil || gotErr.Status != 413 || gotErr.Type != ErrTypeTooManyParameters {
				t.Fatalf("%d pairs: %v", strings.Count(text, "&")+1, gotErr)
			}
			return
		case gotErr != nil:
			t.Fatalf("refused %q: %v", data, gotErr)
		}
		obj := Body(passed)
		if !json.Valid(BodyValue(passed).Raw()) {
			t.Fatalf("the form is kept as %q", BodyValue(passed).Raw())
		}
		var sent []string
		for _, pair := range strings.Split(text, "&") {
			if pair == "" {
				continue
			}
			if !strings.Contains(pair, "=") {
				pair += "="
			}
			sent = append(sent, pair)
		}
		var got []string
		total := 0
		for _, key := range obj.Keys() {
			if !utf8.ValidString(key) {
				t.Fatalf("key %q is not UTF-8", key)
			}
			v := obj.Get(key)
			values := []Value{v}
			if items, ok := v.Array(); ok {
				if len(items) < 2 {
					t.Fatalf("key %q has an array of %d", key, len(items))
				}
				values = items
			}
			for _, item := range values {
				s, ok := item.Str()
				if !ok || !utf8.ValidString(s) {
					t.Fatalf("value of %q is %s", key, item.Raw())
				}
				got = append(got, key+"="+s)
				total++
			}
		}
		if total != len(sent) {
			t.Fatalf("%d values from %d pairs in %q", total, len(sent), data)
		}
		// Without escapes the pairs come back as sent, grouped by key
		if !strings.ContainsAny(text, "%+") {
			count := map[string]int{}
			for _, pair := range sent {
				count[pair]++
			}
			for _, pair := range got {
				count[pair]--
			}
			for pair, n := range count {
				if n != 0 {
					t.Fatalf("pair %q: sent and read differ by %d in %q", pair, n, data)
				}
			}
		}
	})
}

// FuzzMultipart: whatever the body and the boundary, ParseUpload never
// panics, keeps its limits, and leaves no file behind when it fails or
// takes no file.
func FuzzMultipart(f *testing.F) {
	dir := f.TempDir()
	const maxFile = 64
	part := func(headers, content string) string { return "--B\r\n" + headers + "\r\n\r\n" + content + "\r\n" }
	file := func(field, name, content string) string {
		return part(`Content-Disposition: form-data; name="`+field+`"; filename="`+name+`"`+"\r\nContent-Type: image/png", content)
	}
	field := func(name, value string) string { return part(`Content-Disposition: form-data; name="`+name+`"`, value) }
	seeds := []string{
		file("f", "a.png", "x") + "--B--\r\n",
		field("a", "1") + file("f", "a.png", strings.Repeat("x", maxFile)) + field("a", "2") + "--B--\r\n",
		file("f", "a.png", strings.Repeat("x", maxFile+1)) + "--B--\r\n",
		file("g", "a.png", "x") + "--B--\r\n",
		file("f", "a.png", "x") + file("f", "b.png", "y") + "--B--\r\n",
		file("f", "a.png", "x"),
		field("a[b][c]", "1") + field("a[b][]", "2") + field("a[0]", "3") + field("x[9999]", "4") + field("x[10001]", "5") + "--B--\r\n",
		field("", "v") + "--B--\r\n",
		"--B--\r\n", "", "--B\r\n", "\r\n--B\r\n\r\n\r\n--B--",
		part("Content-Disposition: form-data; name=\"f\"; filename*=UTF-8''%C3%A9.png", "x") + "--B--\r\n",
		part("Content-Disposition: attachment", "x") + part("X: y", "z") + "--B--\r\n",
		file("f", "../../../etc/passwd", "x") + "--B--\r\n",
		file("f", `C:\x\..\y`, "x") + "--B--\r\n",
		"preamble\r\n" + file("f", "a.png", "\r\n--Bx\r\n--") + "--B--\r\nepilogue",
	}
	for i, s := range seeds {
		f.Add([]byte(s), "B", uint8(i))
	}
	f.Fuzz(func(t *testing.T, data []byte, boundary string, mode uint8) {
		if strings.ContainsAny(boundary, "\r\n\x00") {
			return
		}
		body := &trackedBody{r: bytes.NewReader(data)}
		req := httptest.NewRequest("POST", "/api/x/upload", body)
		req.ContentLength = int64(len(data))
		req.Header.Set("Content-Length", "1")
		contentType := "multipart/form-data; boundary=" + boundary
		if mode&1 != 0 {
			contentType = `multipart/form-data; boundary="` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(boundary) + `"`
		}
		req.Header.Set("Content-Type", contentType)
		opts := UploadOptions{Field: "f", MaxFileSize: maxFile}
		if mode&2 != 0 {
			opts.Dir = dir
		}
		if mode&4 != 0 {
			opts.Filter = func(file *UploadedFile) (bool, error) {
				if strings.HasSuffix(file.OriginalName, ".bad") {
					return false, errors.New("refused")
				}
				return !strings.HasSuffix(file.OriginalName, ".skip"), nil
			}
		}
		up, err := ParseUpload(req, opts)

		stored := ""
		switch {
		case err != nil && up != nil:
			t.Fatalf("an upload and an error: %v", err)
		case err != nil:
			if err.Error() == "" {
				t.Fatal("an error without text")
			}
			// Once the form is being read, a failure reads the rest off
			if readsForm(contentType) && body.read != len(data) {
				t.Fatalf("after %q %d of %d bytes were read off", err, body.read, len(data))
			}
		default:
			if f := up.File; f != nil {
				if f.Size > maxFile || f.FieldName != "f" || f.OriginalName == "" || strings.ContainsAny(f.OriginalName, `/\`) {
					t.Fatalf("file %+v", f)
				}
				if opts.Dir == "" {
					if int64(len(f.Buffer)) != f.Size || f.Path != "" {
						t.Fatalf("memory file: size %d, %d bytes, path %q", f.Size, len(f.Buffer), f.Path)
					}
				} else {
					info, statErr := os.Stat(f.Path)
					if statErr != nil || info.Size() != f.Size || f.Buffer != nil {
						t.Fatalf("disk file %q: %v", f.Path, statErr)
					}
					stored = f.Filename
				}
			}
			raw, _ := json.Marshal(up.Fields)
			if !json.Valid(raw) {
				t.Fatalf("fields %q", raw)
			}
			for _, key := range up.Fields.Keys() {
				if s, ok := up.Fields.String(key); ok && len(s) > maxFieldSize {
					t.Fatalf("field %q has %d bytes", key, len(s))
				}
			}
		}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if e.Name() != stored {
				t.Fatalf("file %q left behind (error: %v)", e.Name(), err)
			}
			_ = os.Remove(dir + "/" + e.Name())
		}
		if stored != "" && len(entries) != 1 {
			t.Fatalf("the stored file is missing")
		}
	})
}

// readsForm reports whether a Content-Type gets as far as reading the
// form: it has to parse, be multipart/form-data and name a boundary.
func readsForm(header string) bool {
	major, minor, params, ok := partContentType(header)
	_, hasBoundary := params["boundary"]
	return ok && major == "multipart" && minor == "form-data" && hasBoundary
}
