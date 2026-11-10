package i18n

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const fuzzBoundary = "fuzz-boundary-6a1f0c2e9b7d"

var savedFileName = regexp.MustCompile(`^[a-z]{2,8}(-[A-Z][a-zA-Z]{1,7})?\.json$`)

// fuzzSandbox is a folder with the language folder and a file next to it
// that nothing may touch.
func fuzzSandbox(t *testing.T) (root, langDir string) {
	t.Helper()
	root = t.TempDir()
	langDir = filepath.Join(root, "lang")
	if err := os.Mkdir(langDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"en.json": `{"meta":{"name":"English"},"a":"A","b":{"c":"C"}}`,
		"pl.json": `{"a":"A-pl"}`,
		"de.json": `{"a":"A-de"}`,
	} {
		if err := os.WriteFile(filepath.Join(langDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "canary.txt"), []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, langDir
}

// checkSandbox fails when anything was written outside the language
// folder, or inside it under a name that is not <language code>.json.
func checkSandbox(t *testing.T, root, langDir string) {
	t.Helper()
	top, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 2 {
		var names []string
		for _, e := range top {
			names = append(names, e.Name())
		}
		t.Fatalf("entries next to the language folder: %q", names)
	}
	if raw, err := os.ReadFile(filepath.Join(root, "canary.txt")); err != nil || string(raw) != "untouched" {
		t.Fatalf("the file next to the language folder changed: %q, %v", raw, err)
	}
	inside, err := os.ReadDir(langDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range inside {
		if e.IsDir() || !e.Type().IsRegular() || !savedFileName.MatchString(e.Name()) {
			t.Fatalf("unexpected entry in the language folder: %q (%v)", e.Name(), e.Type())
		}
	}
}

// unsafeName is the test's own list of the keys that must never be read
// or merged, so that it does not depend on the filter it checks.
func unsafeName(key string) bool {
	return key == "__proto__" || key == "prototype" || key == "constructor"
}

// unsafePaths lists dot paths of v that pass through an unsafe key.
func unsafePaths(v any, prefix string, tainted bool, out *[]string, depth int) {
	o, ok := v.(*Object)
	if !ok || depth > 6 || len(*out) > 20 {
		return
	}
	for _, k := range o.Keys() {
		if k == "" || strings.Contains(k, ".") {
			continue
		}
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		bad := tainted || unsafeName(k)
		if bad {
			*out = append(*out, path)
		}
		child, _ := o.Get(k)
		unsafePaths(child, path, bad, out, depth+1)
	}
}

// FuzzUploadLanguage sends arbitrary files, codes and file names through
// the upload route, and arbitrary bytes as the whole form. Whatever comes
// in: no panic, an answer in the route's shape, nothing written outside
// the language folder or under a name that is not a language code, the
// stored file reads back as what was sent, and no text is reachable
// through an unsafe key.
func FuzzUploadLanguage(f *testing.F) {
	f.Add([]byte(`{"a":"b"}`), "fr", "fr.json", "application/json", false)
	f.Add([]byte(`{"_meta":{"code":"it"},"__proto__":{"x":"y"},"a":{"constructor":{"z":"1"}}}`), "", "de.json", "application/json", false)
	f.Add([]byte(`{"_meta":{"code":"../../etc/passwd"}}`), "../x", "../../canary.txt", "application/json", false)
	f.Add([]byte(`null`), "en", "en.json", "application/json", false)
	f.Add([]byte(`[1,2]`), "zh-TW", "x", "text/plain", false)
	f.Add([]byte("\xef\xbb\xbf{}"), "..", "..", "application/json", false)
	f.Add([]byte(`{"_meta":{"code":["a","b"]}}`), "code\x00", "a/b\\c.json", "application/json", false)
	f.Add([]byte(`{"prototype":{"a":"b"},"a":"1"}`), "de", "C:\\x\\de.json", "application/json", false)
	f.Add([]byte("--"+fuzzBoundary+"\r\nContent-Disposition: form-data; name=\"file\"; filename=\"nl.json\"\r\nContent-Type: application/json\r\n\r\n{\"a\":1}\r\n--"+fuzzBoundary+"--\r\n"), "", "", "", true)
	f.Add([]byte("--"+fuzzBoundary+"\r\nContent-Disposition: form-data; name=\"code[\r\n"), "", "", "", true)
	f.Add([]byte("--"+fuzzBoundary+"\r\nContent-Disposition: form-data; name=\"code[4000000000]\"\r\n\r\nx\r\n--"+fuzzBoundary+"\r\nContent-Disposition: form-data; name=file; filename*=utf-8''%c3%28.json\r\nContent-Type: application/json\r\n\r\n5\r\n--"+fuzzBoundary+"--"), "", "", "", true)

	f.Fuzz(func(t *testing.T, data []byte, code, filename, fileType string, rawForm bool) {
		root, langDir := fuzzSandbox(t)
		b := New(langDir, "en")
		b.Logf = func(string, ...any) {}
		b.Load()
		h := &Handlers{Bundle: b, Logf: func(string, ...any) {}, Now: time.Now}

		body := data
		if !rawForm {
			if bytes.Contains(data, []byte(fuzzBoundary)) || strings.Contains(code+filename+fileType, fuzzBoundary) {
				t.Skip()
			}
			var form bytes.Buffer
			form.WriteString("--" + fuzzBoundary + "\r\nContent-Disposition: form-data; name=\"code\"\r\n\r\n" + code + "\r\n")
			form.WriteString("--" + fuzzBoundary + "\r\nContent-Disposition: form-data; name=\"file\"; filename=\"" + filename + "\"\r\nContent-Type: " + fileType + "\r\n\r\n")
			form.Write(data)
			form.WriteString("\r\n--" + fuzzBoundary + "--\r\n")
			body = form.Bytes()
		}
		r := httptest.NewRequest("POST", "/api/i18n/upload", bytes.NewReader(body))
		r.Header.Set("Content-Type", "multipart/form-data; boundary="+fuzzBoundary)
		r.Header.Set("Content-Length", strconv.Itoa(len(body)))
		w := httptest.NewRecorder()
		h.Upload(w, r)

		checkSandbox(t, root, langDir)
		answer, err := parseJSON(w.Body.String())
		if err != nil {
			t.Fatalf("the answer is not JSON: %q", w.Body.String())
		}
		switch w.Code {
		case 400, 500:
			if at(answer, "success") != false || str(answer, "error") == "" {
				t.Fatalf("%d with %s", w.Code, w.Body.String())
			}
			return
		case 200:
		default:
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}

		saved := str(answer, "data", "code")
		if !ValidCode(saved) {
			t.Fatalf("saved under the code %q", saved)
		}
		raw, err := os.ReadFile(filepath.Join(langDir, saved+".json"))
		if err != nil {
			t.Fatalf("success without the file: %v", err)
		}
		stored, err := parseJSON(string(raw))
		if err != nil {
			t.Fatalf("the stored file does not parse: %v", err)
		}
		if stringify(stored, "  ") != string(raw) {
			t.Fatal("the stored file is not what the bundle writes for its content")
		}

		// What an unsafe key holds is never read, in the language itself
		// or through the merge with the default language.
		var paths []string
		unsafePaths(stored, "", false, &paths, 0)
		for _, p := range paths {
			if got := b.Translate(saved, p, nil); got != p {
				t.Fatalf("Translate(%q, %q) = %#v: read through an unsafe key", saved, p, got)
			}
		}
		if merged, ok := b.Merged(saved).(*Object); ok && saved != "en" {
			for _, k := range merged.Keys() {
				if unsafeName(k) {
					t.Fatalf("the merged texts of %q have the key %q", saved, k)
				}
			}
		}
	})
}

// FuzzCodeAndKey calls the bundle with arbitrary codes and keys: no panic,
// nothing outside the folder, and no write at all for a code that is not
// a language code.
func FuzzCodeAndKey(f *testing.F) {
	for _, seed := range [][2]string{
		{"de", "a"}, {"en", "b.c"}, {"../canary", "__proto__.x"}, {"..", "constructor"}, {"de/../../x", "a..b"},
		{"zh-TW", "b.c.length"}, {"constructor", "prototype"}, {"en\x00", ""}, {"a\\b", "."}, {"canary.txt", "a.0"},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, code, key string) {
		root, langDir := fuzzSandbox(t)
		b := New(langDir, "en")
		b.Logf = func(string, ...any) {}
		b.Load()
		before, _ := os.ReadDir(langDir)

		value := b.Translate(code, key, V("name", code))
		for _, part := range strings.Split(key, ".") {
			if unsafeName(part) && value != key {
				t.Fatalf("Translate(%q, %q) = %#v: read through an unsafe key", code, key, value)
			}
		}
		_ = b.T(code, key, nil)
		_ = b.Validate(code).Object()
		_ = stringify(b.Merged(code), "")
		_ = b.Has(code)
		_ = b.Meta(code)

		texts := obj(key, code, "a", "saved")
		saved := b.Save(code, texts)
		if saved.Success != ValidCode(code) {
			t.Fatalf("Save(%q) = %+v, ValidCode = %v", code, saved, ValidCode(code))
		}
		checkSandbox(t, root, langDir)
		if !saved.Success {
			after, _ := os.ReadDir(langDir)
			if len(after) != len(before) {
				t.Fatalf("a refused Save(%q) changed the folder", code)
			}
		}
		deleted := b.Delete(code)
		checkSandbox(t, root, langDir)
		if deleted.Success && (code == "en" || code == "pl" || !ValidCode(code)) {
			t.Fatalf("Delete(%q) succeeded", code)
		}
		for _, core := range []string{"en.json", "pl.json"} {
			if _, err := os.Stat(filepath.Join(langDir, core)); err != nil {
				t.Fatalf("%s is gone after Delete(%q)", core, code)
			}
		}
	})
}
