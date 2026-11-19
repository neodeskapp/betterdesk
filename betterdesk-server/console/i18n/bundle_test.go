package i18n

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func listJSON(t *testing.T, b *Bundle) string {
	t.Helper()
	l, err := b.AvailableList()
	if err != nil {
		t.Fatalf("AvailableList: %v", err)
	}
	items := make([]any, len(l))
	for i, e := range l {
		items[i] = e
	}
	return stringify(items, "")
}

// The real language files against what the Node console made of them:
// log lines, the available languages in both shapes, every language's
// texts and merged texts byte for byte (as hashes), every validation
// report, and several hundred translate calls.
func TestRealFilesLikeNode(t *testing.T) {
	want := fixedVectors(t, "real.json")
	b, l := loaded(t, copyDir(t, realLangDir), "en")

	if got, w := l.take(), strs(want, "logs"); !reflect.DeepEqual(got, w) {
		t.Errorf("log lines %q, Node logged %q", got, w)
	}
	if got, w := stringify(b.Available(), ""), str(want, "available"); got != w {
		t.Errorf("Available:\n got  %s\n want %s", got, w)
	}
	if got, w := listJSON(t, b), str(want, "list"); got != w {
		t.Errorf("AvailableList:\n got  %s\n want %s", got, w)
	}

	codes := strs(want, "codes")
	if len(codes) != 26 {
		t.Fatalf("%d language files in the vectors, want 26", len(codes))
	}
	for _, code := range codes {
		if !b.Has(code) {
			t.Errorf("Has(%q) = false", code)
			continue
		}
		if got, w := sha(stringify(b.Translations(code), "")), str(want, "plainSha", code); got != w {
			t.Errorf("%s: the texts written back as JSON differ from Node's", code)
		}
		if got, w := sha(stringify(b.Translations(code), "  ")), str(want, "prettySha", code); got != w {
			t.Errorf("%s: the texts written back with indentation differ from Node's", code)
		}
		if got, w := sha(stringify(b.Merged(code), "")), str(want, "mergedSha", code); got != w {
			t.Errorf("%s: the merged texts differ from Node's", code)
		}
	}

	validate, _ := at(want, "validate").(*Object)
	for _, code := range validate.Keys() {
		w := at(want, "validate", code)
		r := b.Validate(code)
		got := stringify(r.Object(), "")
		if text := str(w, "text"); text != "" {
			if got != text {
				t.Errorf("Validate(%q) = %s, want %s", code, got, text)
			}
			continue
		}
		if sha(got) != str(w, "sha") {
			t.Errorf("Validate(%q): valid %v, %d missing, %d extra, coverage %s; Node: valid %v, %v missing, %v extra, coverage %s (first missing %v)",
				code, r.Valid, len(r.Missing), len(r.Extra), r.Coverage,
				flag(w, "valid"), num(w, "missing"), num(w, "extra"), str(w, "coverage"), strs(w, "firstMissing"))
		}
	}

	ran, builtin := checkTranslate(t, b, list(want, "translate"), varSets(t))
	if ran < 300 {
		t.Errorf("only %d translate vectors ran (%d skipped)", ran, builtin)
	}
}

// Odd files: a byte order mark, broken JSON, files that hold null, a list,
// a string or a number, metadata of the wrong type, invalid UTF-8, unsafe
// keys, numeric keys, repeated keys.
func TestEdgeFilesLikeNode(t *testing.T) {
	want := fixedVectors(t, "edge.json")
	b, l := loaded(t, copyDir(t, "testdata/lang-edge"), "en")

	if got, w := l.take(), strs(want, "logs"); !reflect.DeepEqual(got, w) {
		t.Errorf("log lines:\n got  %q\n want %q", got, w)
	}
	if got, w := stringify(b.Available(), ""), str(want, "available"); got != w {
		t.Errorf("Available:\n got  %s\n want %s", got, w)
	}
	if got, w := listJSON(t, b), str(want, "list"); got != w {
		t.Errorf("AvailableList:\n got  %s\n want %s", got, w)
	}
	checkSnapshot(t, b, at(want, "snapshot"))
	ran, _ := checkTranslate(t, b, list(want, "translate"), varSets(t))
	if ran < 500 {
		t.Errorf("only %d translate vectors ran", ran)
	}
}

func checkSnapshot(t *testing.T, b *Bundle, snapshot any) {
	t.Helper()
	snap, _ := snapshot.(*Object)
	if snap == nil || snap.Len() == 0 {
		t.Fatal("no snapshot in the vectors")
	}
	for _, code := range snap.Keys() {
		w := at(snapshot, code)
		if got := b.Has(code); got != flag(w, "has") {
			t.Errorf("Has(%q) = %v, Node %v", code, got, flag(w, "has"))
		}
		if kind, value := wantDescribed(at(w, "translations")); kind != "function" {
			gotKind, got := described(b.Translations(code))
			if gotKind != kind || got != value {
				t.Errorf("Translations(%q) = %s %s, Node %s %s", code, gotKind, got, kind, value)
			}
		}
		if kind, value := wantDescribed(at(w, "merged")); kind != "function" {
			gotKind, got := described(b.Merged(code))
			if gotKind != kind || got != value {
				t.Errorf("Merged(%q) =\n got  %s %s\n want %s %s", code, gotKind, got, kind, value)
			}
		}
		if kind, value := wantDescribed(at(w, "meta")); kind != "function" {
			var meta any
			if m := b.Meta(code); m != nil {
				meta = m
			}
			gotKind, got := described(meta)
			if gotKind != kind || got != value {
				t.Errorf("Meta(%q) = %s %s, Node %s %s", code, gotKind, got, kind, value)
			}
		}
		if got, w := stringify(b.Validate(code).Object(), ""), str(w, "validate"); got != w {
			t.Errorf("Validate(%q) =\n got  %s\n want %s", code, got, w)
		}
	}
}

// Another default language, and one that has no file.
func TestOtherDefaultLanguageLikeNode(t *testing.T) {
	want, _ := fixedVectors(t, "defaults.json").(*Object)
	sets := varSets(t)
	for _, def := range want.Keys() {
		b, _ := loaded(t, copyDir(t, "testdata/lang-edge"), def)
		w, _ := want.Get(def)
		checkSnapshot(t, b, at(w, "snapshot"))
		if ran, _ := checkTranslate(t, b, list(w, "translate"), sets); ran < 50 {
			t.Errorf("default %s: only %d translate vectors ran", def, ran)
		}
	}
}

func TestTranslate(t *testing.T) {
	b, _ := loaded(t, copyDir(t, "testdata/lang-edge"), "en")
	cases := []struct {
		name      string
		lang, key string
		vars      Vars
		want      any
	}{
		{"own text", "de", "common.cancel", nil, "Abbrechen"},
		{"falls back to the default language", "de", "common.save", nil, "Save"},
		{"unknown language uses the default", "xx", "common.save", nil, "Save"},
		{"missing key comes back", "de", "no.such.key", nil, "no.such.key"},
		{"empty key comes back", "de", "", nil, ""},
		{"empty string is a value", "en", "common.empty", V("name", "x"), ""},
		{"null is a value", "en", "common.nothing", nil, nil},
		{"number", "en", "common.num", V("name", "x"), 12.5},
		{"placeholders", "en", "common.hello", V("name", "Ada", "count", 3), "Hello Ada, you have 3 items Ada"},
		{"missing variable stays", "en", "common.hello", V("name", "Ada"), "Hello Ada, you have {count} items Ada"},
		{"no variables", "en", "common.hello", nil, "Hello {name}, you have {count} items {name}"},
		{"a value can hold the next placeholder", "en", "common.hello", V("name", "{count}", "count", 7), "Hello 7, you have 7 items 7"},
		{"but not an earlier one", "en", "common.hello", V("count", 7, "name", "{count}"), "Hello {count}, you have 7 items {count}"},
		{"numbers print as JavaScript prints them", "en", "common.hello", V("name", nil, "count", 1e21), "Hello null, you have 1e+21 items null"},
		{"undefined", "en", "common.hello", V("name", Undefined, "count", 0.5), "Hello undefined, you have 0.5 items undefined"},
		{"odd names", "en", "common.nested.deep.leaf", V("[", "safe", "a.b", "dot", "", "none"), "Leaf {0} dot safe {b} none"},
		{"numeric names first", "en", "common.nested.deep.leaf", V("b", "{0}", "0", "zero"), "Leaf zero {a.b} {[} {0} {}"},
		{"unsafe path", "en", "__proto__.polluted", nil, "__proto__.polluted"},
		{"unsafe path in the middle", "de", "common.constructor.x", nil, "common.constructor.x"},
		{"into a string", "en", "common.cancel.length", nil, 6.0},
		{"into a list", "en", "common.list.1.x", nil, 1.0},
	}
	for _, c := range cases {
		got := b.Translate(c.lang, c.key, c.vars)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Translate(%q, %q) = %#v, want %#v", c.name, c.lang, c.key, got, c.want)
		}
	}
	if obj, ok := b.Translate("de", "only_en", nil).(*Object); !ok || stringify(obj, "") != `{"a":"A","b":{"c":"C"}}` {
		t.Errorf("a key that names an object returns the object, got %#v", b.Translate("de", "only_en", nil))
	}
	if got := b.T("de", "only_en", nil); got != "[object Object]" {
		t.Errorf("T of an object = %q", got)
	}
	if got := b.T("en", "common.list", nil); got != "a,[object Object],2,3" {
		t.Errorf("T of a list = %q", got)
	}
	if got := b.T("en", "common.nothing", nil); got != "null" {
		t.Errorf("T of null = %q", got)
	}
}

func TestMergedKeepsUnsafeKeysOut(t *testing.T) {
	b, _ := loaded(t, copyDir(t, "testdata/lang-edge"), "en")
	merged, _ := b.Merged("de").(*Object)
	if merged == nil {
		t.Fatal("Merged(de) is not an object")
	}
	if _, ok := merged.Get("constructor"); ok {
		t.Error("the overlay's constructor key was merged")
	}
	if _, ok := merged.Get("prototype"); ok {
		t.Error("the overlay's prototype key was merged")
	}
	// English has its own __proto__ key in the fixture; the German one must not replace it.
	if got := stringify(at(merged, "__proto__"), ""); got != `{"polluted":"yes"}` {
		t.Errorf("__proto__ after the merge = %s", got)
	}
	if got := stringify(at(merged, "common", "nested"), ""); got != `{"deep":{"leaf":"Leaf {0} {a.b} {[} {b} {}","extra":"E"}}` {
		t.Errorf("nested merge = %s", got)
	}
	if got := at(merged, "mixed"); got != "now a string" {
		t.Errorf("a string replaces an object, got %#v", got)
	}
	if got := stringify(at(merged, "arr_vs_obj"), ""); got != "[1,2]" {
		t.Errorf("a list replaces an object, got %s", got)
	}
	// The source objects are not changed by the merge.
	if got := stringify(at(b.Translations("en"), "common", "nested"), ""); strings.Contains(got, "extra") {
		t.Errorf("the merge changed the English texts: %s", got)
	}
}

func TestLoadSkipsWhatIsNotInTheTable(t *testing.T) {
	b, l := loaded(t, copyDir(t, "testdata/lang-edge"), "en")
	if b.Has("xx") || b.Meta("xx") != nil {
		t.Error("xx.json is loaded although xx is not a known language")
	}
	if b.Has("dir") {
		t.Error("dir.json (a folder) is loaded")
	}
	if b.Has("fr") || b.Meta("fr") != nil || b.Translations("fr") != nil {
		t.Error("a file that holds null is loaded")
	}
	if b.Has("constructor") || b.Has("__proto__") || b.Has("") || b.Has("En") {
		t.Error("Has for inherited names, empty and wrong case")
	}
	for _, line := range l.take() {
		if strings.Contains(line, "xx") {
			t.Errorf("log line about a file outside the table: %q", line)
		}
	}
}

// A language file that holds null is logged and not loaded: the language
// cannot be chosen and has no strings, as it was already not listed
// (BUGS.md L5).
func TestLoadSkipsFileThatHoldsNull(t *testing.T) {
	dir := copyDir(t, "testdata/lang-upload")
	if err := os.WriteFile(filepath.Join(dir, "fr.json"), []byte("null"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, _, l := handlersFor(t, dir, time.Now())
	if lines := l.take(); !slices.Contains(lines, "Failed to load language fr Cannot read properties of null (reading '_meta')") {
		t.Errorf("log lines %q", lines)
	}
	if h.Bundle.Has("fr") || h.Bundle.Meta("fr") != nil || h.Bundle.Translations("fr") != nil {
		t.Error("fr is loaded")
	}
	srv := httptest.NewServer(routes(h))
	defer srv.Close()
	for _, c := range []struct {
		method, path string
		status       int
		body         string
	}{
		{"POST", "/api/i18n/set/fr", 400, `{"success":false,"error":"Language not supported"}`},
		{"GET", "/api/i18n/translations/fr", 404, `{"success":false,"error":"Language not found"}`},
		{"GET", "/api/console/strings/fr", 404, `{"success":false,"error":"Language not found"}`},
	} {
		got := do(t, srv.URL, c.method, c.path, nil, nil, 0)
		if got.status != c.status || got.body != c.body || got.header.Get("Set-Cookie") != "" {
			t.Errorf("%s %s: %d %s, cookie %q; want %d %s", c.method, c.path, got.status, short(got.body), got.header.Get("Set-Cookie"), c.status, c.body)
		}
	}
}

func TestLoadMissingFolder(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no\nsuch\rfolder")
	b, l := loaded(t, missing, "en")
	lines := l.take()
	want := []string{"Language directory not found " + strings.NewReplacer("\n", " ", "\r", " ").Replace(missing), "i18n: Loaded languages 0"}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("log lines %q, want %q", lines, want)
	}
	if b.Has("en") || b.Available().Len() != 0 {
		t.Error("languages loaded from a missing folder")
	}
	if got := stringify(b.Merged("de"), ""); got != "{}" {
		t.Errorf("Merged without any language = %s", got)
	}
	if got := b.Translate("de", "a.b", nil); got != "a.b" {
		t.Errorf("Translate without any language = %v", got)
	}
}

func TestValidCode(t *testing.T) {
	valid := []string{"en", "de", "abcdefgh", "zh-TW", "en-US", "en-Latn", "sr-Cyrl", "ab-Xy"}
	invalid := []string{"", "e", "abcdefghi", "EN", "En", "en-us", "en-", "en-U", "en_US", "en-US-x", "../en", "en/..", "en.json", "en\x00", " en", "en ", "e1", "zh-TW\n", "constructor", "__proto__", "en-USAAAAAAA"}
	for _, c := range valid {
		if !ValidCode(c) {
			t.Errorf("ValidCode(%q) = false", c)
		}
	}
	for _, c := range invalid {
		if ValidCode(c) {
			t.Errorf("ValidCode(%q) = true", c)
		}
	}
}

func TestSaveWritesIndentedJSONAndReloads(t *testing.T) {
	dir := copyDir(t, "testdata/lang-upload")
	b, l := loaded(t, dir, "en")
	l.take()

	texts, err := parseJSON(`{"z":1,"meta":{"name":"French","native_name":"Français"},"10":"ten","a":{"b":[1,{"c":null}],"e":{}},"2":"two","n":1.50,"big":1e21}`)
	if err != nil {
		t.Fatal(err)
	}
	if r := b.Save("fr", texts); !r.Success || r.Error != "" {
		t.Fatalf("Save = %+v", r)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "fr.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"2\": \"two\",\n  \"10\": \"ten\",\n  \"z\": 1,\n  \"meta\": {\n    \"name\": \"French\",\n    \"native_name\": \"Français\"\n  },\n" +
		"  \"a\": {\n    \"b\": [\n      1,\n      {\n        \"c\": null\n      }\n    ],\n    \"e\": {}\n  },\n  \"n\": 1.5,\n  \"big\": 1e+21\n}"
	if string(raw) != want {
		t.Errorf("file content:\n%s\nwant:\n%s", raw, want)
	}
	if !b.Has("fr") {
		t.Fatal("the saved language is not loaded")
	}
	if got := stringify(b.Meta("fr"), ""); got != `{"code":"fr","name":"French","native":"Français","flag":"🇫🇷","rtl":false}` {
		t.Errorf("Meta(fr) = %s", got)
	}
	if got := strings.Join(b.Available().Keys(), " "); got != "en pl de fr" {
		t.Errorf("available order %q, want the new language at the end", got)
	}

	// Saving an existing language keeps its place.
	if r := b.Save("pl", texts); !r.Success {
		t.Fatalf("Save(pl) = %+v", r)
	}
	if got := strings.Join(b.Available().Keys(), " "); got != "en pl de fr" {
		t.Errorf("available order after saving pl again: %q", got)
	}
	if got := b.Translate("pl", "10", nil); got != "ten" {
		t.Errorf("the saved texts are not loaded: %v", got)
	}

	// A code outside the table is written but not loaded.
	if r := b.Save("xx", texts); !r.Success {
		t.Fatalf("Save(xx) = %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, "xx.json")); err != nil {
		t.Errorf("xx.json was not written: %v", err)
	}
	if b.Has("xx") {
		t.Error("xx is loaded")
	}
	if lines := l.take(); !reflect.DeepEqual(lines, []string{"i18n: Skipping unsupported language code xx"}) {
		t.Errorf("log lines %q", lines)
	}
}

func TestSaveRefusesBadCodes(t *testing.T) {
	dir := copyDir(t, "testdata/lang-upload")
	outside := t.TempDir()
	b, _ := loaded(t, dir, "en")
	before, _ := os.ReadDir(dir)
	for _, code := range []string{"", "e", "EN", "../x", "..", "a/b", `a\b`, "en.json", "en\x00", "abcdefghi", "constructor", "x y", filepath.Join(outside, "ab")} {
		if r := b.Save(code, NewObject()); r.Success || r.Error != "Invalid language code format" {
			t.Errorf("Save(%q) = %+v", code, r)
		}
	}
	after, _ := os.ReadDir(dir)
	if len(after) != len(before) {
		t.Errorf("the folder has %d entries after refused saves, %d before", len(after), len(before))
	}
	if left, _ := os.ReadDir(outside); len(left) != 0 {
		t.Errorf("a refused save wrote outside the folder: %v", left)
	}
}

func TestSaveAndDeleteStayInsideTheFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs symbolic links")
	}
	dir := copyDir(t, "testdata/lang-upload")
	outside := t.TempDir()
	target := filepath.Join(outside, "target.json")
	if err := os.WriteFile(target, []byte(`{"outside":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "fr.json")); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}
	b, l := loaded(t, dir, "en")
	if b.Has("fr") {
		t.Error("a language was loaded through a link that leaves the folder")
	}
	if lines := strings.Join(l.take(), "\n"); !strings.Contains(lines, "Failed to load language fr Path outside allowed directory") {
		t.Errorf("log lines: %s", lines)
	}
	if r := b.Save("fr", NewObject()); r.Success || r.Error != "Path outside allowed directory" {
		t.Errorf("Save through the link = %+v", r)
	}
	if r := b.Delete("fr"); r.Success || r.Error != "Path outside allowed directory" {
		t.Errorf("Delete through the link = %+v", r)
	}
	if raw, _ := os.ReadFile(target); string(raw) != `{"outside":true}` {
		t.Errorf("the file outside the folder was changed: %s", raw)
	}
	if _, err := os.Lstat(filepath.Join(dir, "fr.json")); err != nil {
		t.Errorf("the link was removed: %v", err)
	}
}

func TestDelete(t *testing.T) {
	dir := copyDir(t, "testdata/lang-upload")
	b, _ := loaded(t, dir, "en")

	for _, code := range []string{"en", "pl"} {
		if r := b.Delete(code); r.Success || r.Error != "Cannot delete core language" {
			t.Errorf("Delete(%q) = %+v", code, r)
		}
		if _, err := os.Stat(filepath.Join(dir, code+".json")); err != nil {
			t.Errorf("%s.json is gone", code)
		}
	}
	for _, code := range []string{"", "EN", "../de", "de.json", "abcdefghi"} {
		if r := b.Delete(code); r.Success || r.Error != "Invalid language code format" {
			t.Errorf("Delete(%q) = %+v", code, r)
		}
	}
	if r := b.Delete("de"); !r.Success {
		t.Fatalf("Delete(de) = %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, "de.json")); !os.IsNotExist(err) {
		t.Errorf("de.json still exists: %v", err)
	}
	if b.Has("de") || b.Meta("de") != nil {
		t.Error("de is still loaded")
	}
	if got := strings.Join(b.Available().Keys(), " "); got != "en pl" {
		t.Errorf("available after delete: %q", got)
	}
	// A language without a file: success, nothing happens.
	if r := b.Delete("fr"); !r.Success {
		t.Errorf("Delete(fr) = %+v", r)
	}
	// Deleted and saved again: at the end of the list.
	if r := b.Save("de", NewObject()); !r.Success {
		t.Fatal(r)
	}
	if got := strings.Join(b.Available().Keys(), " "); got != "en pl de" {
		t.Errorf("available after saving again: %q", got)
	}
}

func TestAvailableListFailsForNamesThatAreNotText(t *testing.T) {
	dir := copyDir(t, "testdata/lang-upload")
	if err := os.WriteFile(filepath.Join(dir, "de.json"), []byte(`{"_meta":{"native":["D"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ := loaded(t, dir, "en")
	if _, err := b.AvailableList(); err == nil {
		t.Error("no error for a native name that is a list")
	}
	// The first entry is only ever compared against, so a number there is fine.
	if err := os.WriteFile(filepath.Join(dir, "en.json"), []byte(`{"_meta":{"native":5}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "de.json")); err != nil {
		t.Fatal(err)
	}
	b, _ = loaded(t, dir, "en")
	l, err := b.AvailableList()
	if err != nil || len(l) != 2 {
		t.Fatalf("AvailableList = %v, %v", l, err)
	}
	if got := str(l[0], "code") + " " + str(l[1], "code"); got != "en pl" {
		t.Errorf("order %q, want the number 5 before Polski", got)
	}
}

// What the console relies on in the language files themselves: English and
// Polish exist, there is no Russian file, every file loads, carries its
// metadata and has the keys newer pages need, and Polish covers English.
func TestLanguageFilesKeepTheirPromises(t *testing.T) {
	dir := copyDir(t, realLangDir)
	b, l := loaded(t, dir, "en")
	names := fileNames(t, dir)
	if len(names) != 26 || !reflect.DeepEqual(l.take(), []string{"i18n: Loaded languages 26"}) {
		t.Fatalf("%d files, not all of them load", len(names))
	}
	if !b.Has("en") || !b.Has("pl") || b.Has("ru") {
		t.Errorf("en %v, pl %v, ru %v; want true, true, false", b.Has("en"), b.Has("pl"), b.Has("ru"))
	}
	required := []string{
		"meta.lang", "meta.name",
		"settings.ui.auth_sub_totp", "backup.schedule_title", "backup.smb_test_success",
		"mobile_nav.label", "mobile_nav.home", "mobile_nav.devices", "mobile_nav.remote", "mobile_nav.chat", "mobile_nav.more",
		"remote.input_mode_touch", "remote.input_mode_touchpad", "remote.show_keyboard", "remote.special_keys",
		"remote.phone_unsupported_title", "remote.phone_unsupported_body", "remote.phone_unsupported_back_devices",
	}
	for _, name := range names {
		code := strings.TrimSuffix(name, ".json")
		texts := b.Translations(code)
		if texts == nil {
			t.Errorf("%s is not loaded", name)
			continue
		}
		for _, key := range required {
			if !truthy(lookup(texts, key)) {
				t.Errorf("%s lacks %s", name, key)
			}
		}
	}
	native := map[string]string{
		"fr": "Français", "zh-TW": "繁體中文", "ar": "العربية", "da": "Dansk", "fi": "Suomi", "hi": "हिन्दी", "id": "Bahasa Indonesia",
		"nb": "Norsk", "ro": "Română", "sv": "Svenska", "th": "ไทย", "tr": "Türkçe", "uk": "Українська", "vi": "Tiếng Việt",
	}
	for code, want := range native {
		if got := lookup(b.Translations(code), "meta.native_name"); got != want {
			t.Errorf("%s: native name %v, want %s", code, got, want)
		}
		if got := str(b.Meta(code), "native"); got != want {
			t.Errorf("%s: listed as %q, want %s", code, got, want)
		}
	}
	if r := b.Validate("pl"); len(r.Missing)*10 > len(allKeys(b.Translations("en"), "")) {
		t.Errorf("Polish lacks %d English keys, coverage %s", len(r.Missing), r.Coverage)
	}
}
