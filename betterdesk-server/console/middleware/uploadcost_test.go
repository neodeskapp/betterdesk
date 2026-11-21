package middleware

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// TestUploadFieldsCostWhatTheyWeigh: the text fields of a form cost memory
// in proportion to the request. Indexed fields stay sparse, the arrays of
// a form hold at most maxFieldSlots places, and a form has at most 1000
// fields, 2000 parts and field names of 100 bytes (BUGS.md RV193).
func TestUploadFieldsCostWhatTheyWeigh(t *testing.T) {
	fields := func(n int, name func(i int) string) []string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = textPart(name(i), "v")
		}
		return parts
	}
	cases := []struct {
		name, code, text string
		parts            []string
	}{
		{"2000 indexed fields", "LIMIT_FIELD_COUNT", "Too many fields", fields(2000, func(i int) string { return fmt.Sprintf("f%d[10000]", i) })},
		{"999 indexed fields", "LIMIT_FIELD_ARRAY_INDEX", "Field name array index too large", fields(999, func(i int) string { return fmt.Sprintf("f%d[10000]", i) })},
		{"16 nested indexes", "LIMIT_FIELD_ARRAY_INDEX", "Field name array index too large", fields(1, func(int) string { return "a" + strings.Repeat("[9999]", 16) })},
		{"300 nested indexes", "LIMIT_FIELD_KEY", "Field name too long", fields(1, func(int) string { return "a" + strings.Repeat("[9999]", 300) })},
		{"2001 parts without a disposition", "LIMIT_PART_COUNT", "Too many parts", func() []string {
			parts := make([]string, 2001)
			for i := range parts {
				parts[i] = "Content-Type: text/plain\r\n\r\nx"
			}
			return parts
		}()},
	}
	for _, c := range cases {
		req := multipartRequest("/x", "b", c.parts...)
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		up, err := ParseUpload(req, UploadOptions{Field: "file"})
		runtime.ReadMemStats(&after)
		if n := after.TotalAlloc - before.TotalAlloc; n > 32<<20 {
			t.Errorf("%s: %d MB allocated", c.name, n>>20)
		}
		if err == nil || up != nil || UploadErrorCode(err) != c.code || err.Error() != c.text {
			t.Errorf("%s: %v (%s), want %s %q", c.name, err, UploadErrorCode(err), c.code, c.text)
		}
	}

	// Up to the limits a form is read as before
	parts := fields(1000, func(i int) string { return fmt.Sprintf("f%d", i) })
	parts[0], parts[1] = textPart("n[2]", "q"), textPart(strings.Repeat("k", 100), "v")
	up, err := ParseUpload(multipartRequest("/x", "b", parts...), UploadOptions{Field: "file"})
	if err != nil {
		t.Fatal(err)
	}
	if raw := up.Fields.Get("n").Raw(); string(raw) != `[null,null,"q"]` || !up.Fields.Has(strings.Repeat("k", 100)) || !up.Fields.Has("f999") {
		t.Errorf("1000 fields: n is %s", raw)
	}
}
