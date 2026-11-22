package settings

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/middleware"
)

// FuzzUploadForms reads arbitrary multipart bodies the way the three
// upload routes do (readUpload, then the text fields as JavaScript values,
// the background target and the font family): no panic, a refusal is 400
// with a reason, a file that passes is within the limit and of an accepted
// type, the fields always read as JSON, and the only error of the target
// and the family is JavaScript's for an object that cannot be made text.
func FuzzUploadForms(f *testing.F) {
	part := func(headers, content string) string { return "--B\r\n" + headers + "\r\n\r\n" + content + "\r\n" }
	file := func(field, name, contentType, content string) string {
		return part(`Content-Disposition: form-data; name="`+field+`"; filename="`+name+`"`+"\r\nContent-Type: "+contentType, content)
	}
	field := func(name, value string) string { return part(`Content-Disposition: form-data; name="`+name+`"`, value) }
	for i, s := range []string{
		file("logo", "a.png", "image/png", "x") + "--B--\r\n",
		field("target", " bgImageUrl ") + file("background", "w.JPG", "image/jpeg", "x") + "--B--\r\n",
		field("target[toString]", "x") + file("background", "w.jpg", "image/jpeg", "x") + "--B--\r\n",
		field("family", "  A Font ") + field("family", "B") + file("font", "f.ttf", "font/ttf", "x") + "--B--\r\n",
		field("family[a][b]", "c") + file("font", "f.woff2", "application/octet-stream", "x") + "--B--\r\n",
		file("logo", "a.png", "image/png", strings.Repeat("x", 65)) + "--B--\r\n",
		file("logo", "..", "image/png", "x") + "--B--\r\n",
		file("font", "x.png", "image/png", "x") + "--B--\r\n",
		"--B\r\n", "", "--B--\r\n",
	} {
		f.Add([]byte(s), uint8(i))
	}
	const limit = 64
	f.Fuzz(func(t *testing.T, data []byte, which uint8) {
		route := []struct {
			field  string
			accept func(*middleware.UploadedFile) error
		}{{"logo", acceptImage}, {"background", acceptImage}, {"font", acceptFont}}[int(which)%3]
		req := httptest.NewRequest("POST", "/api/settings/branding/upload", bytes.NewReader(data))
		req.Header.Set("Content-Type", "multipart/form-data; boundary=B")
		rec := httptest.NewRecorder()
		up, ok := readUpload(rec, req, route.field, limit, "too large", route.accept)
		if !ok {
			var answer struct {
				Success bool   `json:"success"`
				Error   string `json:"error"`
			}
			if rec.Code != http.StatusBadRequest || json.Unmarshal(rec.Body.Bytes(), &answer) != nil || answer.Success || answer.Error == "" {
				t.Fatalf("refusal: %d %s", rec.Code, rec.Body.String())
			}
			return
		}
		if rec.Body.Len() != 0 || up.File == nil || up.File.Size > limit || int64(len(up.File.Buffer)) != up.File.Size || up.File.FieldName != route.field {
			t.Fatalf("taken: %+v, answer %s", up.File, rec.Body.String())
		}
		if route.accept(up.File) != nil {
			t.Fatalf("a file of a refused type was taken: %q %q", up.File.OriginalName, up.File.MIMEType)
		}
		form, err := fields(up)
		if err != nil {
			t.Fatalf("fields: %v", err)
		}
		for _, value := range []any{branding.Field(form, "target"), branding.Field(form, "family")} {
			if _, err := jsText(value); err != nil && branding.Message(err) != "Cannot convert object to primitive value" {
				t.Fatalf("text of %s: %v", branding.Stringify(value), err)
			}
		}
		if _, err := branding.FontUploadFamily(branding.Field(form, "family"), up.File.OriginalName); err != nil && branding.Message(err) != "Cannot convert object to primitive value" {
			t.Fatalf("font family: %v", err)
		}
	})
}

// FuzzProfileID reads profile ids as parseInt(id, 10) does, checked
// against goapipath.ParseInt (parseInt without a radix), which agrees
// wherever the text does not start with 0x.
func FuzzProfileID(f *testing.F) {
	for _, s := range []string{"12", "12abc", " 7", "-0", "+5", "abc", "", "-", "1e5", "0x10", "  3", strings.Repeat("9", 30), strings.Repeat("9", 400), "9007199254740993", "-9223372036854775809", "2147483647", "2147483648", "-2147483648", "-2147483649"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		req := httptest.NewRequest("GET", "/", nil)
		req.SetPathValue("id", s)
		id, ok := parseProfileID(req)
		trimmed := strings.TrimLeft(branding.Trim(s), "+-")
		if len(trimmed) >= 2 && trimmed[0] == '0' && (trimmed[1] == 'x' || trimmed[1] == 'X') {
			return
		}
		want := goapipath.ParseInt(s)
		if math.IsNaN(want) || math.IsInf(want, 0) {
			if ok {
				t.Fatalf("%q: an id %+v where JavaScript has %v", s, id, want)
			}
			return
		}
		// an id beyond 32 bits, which no profile has, is refused (BUGS BR11)
		if want > math.MaxInt32 || want < math.MinInt32 {
			if ok {
				t.Fatalf("%q: an id %+v beyond 32 bits", s, id)
			}
			return
		}
		if !ok {
			t.Fatalf("%q: no id where JavaScript has %v", s, want)
		}
		if text, _ := branding.ToString(want); text != id.text {
			t.Fatalf("%q: written %q, JavaScript writes %q", s, id.text, text)
		}
		if float64(id.value) != want {
			t.Fatalf("%q: %d for %v", s, id.value, want)
		}
	})
}
