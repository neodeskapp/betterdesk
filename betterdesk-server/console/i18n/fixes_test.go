package i18n

import (
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// The bug fixes of BUGS.md that change answers the Node recordings in
// testdata hold. Each fix rewrites the recorded vectors it changes into
// what the console answers now, before the replays compare them;
// TestEveryFixChangesItsVectors fails for a fix that changes none.

// vectorFix is one bug fix's rewrite of one file of vectors. rewrite
// changes the parsed file and returns how many vectors it changed.
type vectorFix struct {
	bug, file string
	rewrite   func(t testing.TB, file any) int
}

var vectorFixes = []vectorFix{
	// The names every JavaScript object has are no languages.
	{"L1", "real.json", inheritedNamesNotFound},
	{"L1", "edge.json", inheritedNamesNotInSnapshot},
	{"L1", "routes-edge.json", inheritedNamesNotValidated},
	{"L1", "routes-upload.json", inheritedNamesNotValidated},
	{"L1", "pick.json", pickWith(pickRules{loadedOnly: true})},

	// A query or cookie value that is not a string counts as not given.
	{"L2", "pick.json", pickWith(pickRules{loadedOnly: true, textOnly: true})},

	// A value that is no language gives way to the next source.
	{"L4", "pick.json", pickWith(pickRules{loadedOnly: true, textOnly: true, skipUnknown: true})},

	// A file that holds null is not loaded.
	{"L5", "edge.json", nullFileNotInSnapshot},
	{"L5", "defaults.json", nullFileNotInDefaultSnapshots},
	{"L5", "routes-edge.json", nullFileNoLanguage},

	// An upload of a code outside the language table is refused.
	{"L7", "upload.json", uploadsRefused(400, "Invalid language code",
		// written but never loaded
		"new code not in the table", "five letter name", "region code name", "json in the middle",
		// a string Save refused with 500
		"upper case name", "name with percent escapes", "name with nul", "meta code with traversal", "meta code dotdot",
		"meta code with slash", "meta code proto", "meta code astral", "code base64", "production: bad code format")},

	// What the form parser refuses is 413 or 400 with its message, also
	// in production.
	{"L8", "upload.json", parserRefusalsAre4xx},

	// A code that is not a string is refused like any code outside the
	// table, whatever its length.
	{"L10", "upload.json", uploadsRefused(400, "Invalid language code",
		"meta code number", "meta code array of two", "meta code true", "meta code object",
		"code twice", "code object", "code index three", "code then code object", "code object then code")},

	// A field or filename* in another charset is decoded instead of
	// dropped: these upload as the same form in UTF-8 or Latin-1 does.
	{"L11", "upload.json", uploadsAnswerAs(map[string]string{
		"extended file name other charset": "extended file name",
		"code with other charset":          "code latin1",
	})},

	// The translations page reads a file with a byte order mark as it
	// reads the same file without one.
	{"L12", "panel-edge.json", markedFileRead},

	// A file that holds null and a broken reference file get a row with
	// their error instead of a 500 for the whole page.
	{"L13", "panel-edge.json", brokenFilesListed},

	// The missing keys of a code that is not a language code are refused.
	{"L14", "panel-edge.json", missingRefusesBadCodes},
	{"L14", "panel-real.json", missingRefusesBadCodes},
}

// fixedVectors reads a file of vectors with every fix applied.
func fixedVectors(t testing.TB, name string) any {
	t.Helper()
	v := vectors(t, name)
	for _, f := range vectorFixes {
		if f.file == name {
			f.rewrite(t, v)
		}
	}
	return v
}

func TestEveryFixChangesItsVectors(t *testing.T) {
	files := map[string]any{}
	for _, f := range vectorFixes {
		v, ok := files[f.file]
		if !ok {
			v = vectors(t, f.file)
			files[f.file] = v
		}
		n := f.rewrite(t, v)
		if n == 0 {
			t.Errorf("%s changes no vector of %s", f.bug, f.file)
			continue
		}
		t.Logf("%s changes %d vectors of %s", f.bug, n, f.file)
	}
}

// nodeInheritedNames are the names every JavaScript object answers to,
// which Node took for languages.
var nodeInheritedNames = map[string]bool{
	"constructor": true, "__defineGetter__": true, "__defineSetter__": true, "hasOwnProperty": true,
	"__lookupGetter__": true, "__lookupSetter__": true, "isPrototypeOf": true, "propertyIsEnumerable": true,
	"toString": true, "valueOf": true, "__proto__": true, "toLocaleString": true,
}

const notFoundReport = `{"valid":false,"error":"Language not found"}`

// setIf sets key of the vector to value when it differs and reports
// whether it did.
func setIf(v any, key string, value any) bool {
	o := v.(*Object)
	if old, ok := o.Get(key); ok && stringify(old, "") == stringify(value, "") {
		return false
	}
	o.Set(key, value)
	return true
}

// setAll applies setIf to key, value pairs and reports whether any changed.
func setAll(v any, pairs ...any) bool {
	changed := false
	for i := 0; i+1 < len(pairs); i += 2 {
		if setIf(v, pairs[i].(string), pairs[i+1]) {
			changed = true
		}
	}
	return changed
}

// answerJSON sets a route vector's answer to a small JSON body without
// compression.
func answerJSON(v any, status int, body string) bool {
	return setAll(v, "status", float64(status), "body", body, "etag", httpx.ETag([]byte(body)),
		"contentType", "application/json; charset=utf-8", "contentEncoding", nil, "vary", nil)
}

// --- L1 ---

func inheritedNamesNotFound(t testing.TB, file any) int {
	n := 0
	validate := at(file, "validate").(*Object)
	for _, code := range validate.Keys() {
		if nodeInheritedNames[code] && setIf(validate, code, obj("text", notFoundReport)) {
			n++
		}
	}
	return n
}

func inheritedNamesNotInSnapshot(t testing.TB, file any) int {
	n := 0
	snapshot := at(file, "snapshot").(*Object)
	for _, code := range snapshot.Keys() {
		if nodeInheritedNames[code] && setAll(at(snapshot, code), "has", false, "validate", notFoundReport) {
			n++
		}
	}
	return n
}

func inheritedNamesNotValidated(t testing.TB, file any) int {
	n := 0
	for _, c := range list(file, "vectors") {
		code, isValidate := strings.CutPrefix(str(c, "path"), "/api/i18n/validate/")
		if str(c, "method") == "GET" && isValidate && nodeInheritedNames[code] &&
			answerJSON(c, 200, `{"success":true,"data":`+notFoundReport+`}`) {
			n++
		}
	}
	return n
}

// --- L5 ---

// edgeNullLanguage is the language whose file in testdata/lang-edge holds
// null.
const edgeNullLanguage = "fr"

func nullFileNotInSnapshot(t testing.TB, file any) int {
	if entry, ok := at(file, "snapshot", edgeNullLanguage).(*Object); ok && setIf(entry, "has", false) {
		return 1
	}
	return 0
}

func nullFileNotInDefaultSnapshots(t testing.TB, file any) int {
	n := 0
	for _, def := range file.(*Object).Keys() {
		n += nullFileNotInSnapshot(t, at(file, def))
	}
	return n
}

func nullFileNoLanguage(t testing.TB, file any) int {
	n := 0
	for _, c := range list(file, "vectors") {
		if slices.Contains(strs(c, "removeFiles"), edgeNullLanguage+".json") {
			continue
		}
		switch method, path := str(c, "method"), str(c, "path"); {
		case method == "GET" && path == "/api/i18n/translations/"+edgeNullLanguage:
			if answerJSON(c, 404, `{"success":false,"error":"Language not found"}`) {
				n++
			}
		case method == "POST" && path == "/api/i18n/set/"+edgeNullLanguage:
			changed := answerJSON(c, 400, `{"success":false,"error":"Language not supported"}`)
			if setAll(c, "setCookie", []any{}, "persisted", []any{}, "sessionPreferred", nil) || changed {
				n++
			}
		}
	}
	return n
}

// --- uploads ---

// uploadsRefused rewrites the named vectors of upload.json into a refusal
// that writes nothing.
func uploadsRefused(status int, message string, names ...string) func(t testing.TB, file any) int {
	return func(t testing.TB, file any) int {
		n := 0
		for _, c := range list(file) {
			if !slices.Contains(names, str(c, "name")) {
				continue
			}
			if setAll(c, "status", float64(status), "body", `{"success":false,"error":"`+message+`"}`,
				"changed", NewObject(), "audit", []any{}) {
				n++
			} else {
				t.Errorf("%s: the vector is already refused that way", str(c, "name"))
			}
		}
		if n != len(names) {
			t.Errorf("%d of the %d named vectors found", n, len(names))
		}
		return n
	}
}

// uploadsAnswerAs rewrites each named vector of upload.json into the
// answer of the vector it maps to.
func uploadsAnswerAs(like map[string]string) func(t testing.TB, file any) int {
	return func(t testing.TB, file any) int {
		byName := map[string]any{}
		for _, c := range list(file) {
			byName[str(c, "name")] = c
		}
		n := 0
		for name, other := range like {
			c, model := byName[name], byName[other]
			if c == nil || model == nil {
				t.Fatalf("no vector %q or %q", name, other)
			}
			var pairs []any
			for _, key := range []string{"status", "body", "contentTypeOut", "changed", "audit", "available"} {
				pairs = append(pairs, key, at(model, key))
			}
			if setAll(c, pairs...) {
				n++
			}
		}
		return n
	}
}

// parserMessages are the refusals of the form parser, with the status
// they get now.
var parserMessages = map[string]int{
	"File too large": 413, "Field value too long": 413,
	"Unexpected file field": 400, "Only JSON files allowed": 400, "Field name missing": 400,
	"Malformed part header": 400, "Unexpected end of form": 400, "Malformed content type": 400,
	"Multipart: Boundary not found": 400,
}

func parserRefusalsAre4xx(t testing.TB, file any) int {
	// In production Node hid the parser's message
	hidden := map[string]string{"production: wrong field": "Unexpected file field", "production: too large": "File too large"}
	n := 0
	for _, c := range list(file) {
		if num(c, "status") != 500 {
			continue
		}
		answer, err := parseJSON(str(c, "body"))
		if err != nil {
			t.Fatalf("%s: %v", str(c, "name"), err)
		}
		message := str(answer, "error")
		if m, ok := hidden[str(c, "name")]; ok {
			message = m
		}
		status, ok := parserMessages[message]
		if strings.HasPrefix(message, "Unsupported content type: ") {
			status, ok = 400, true
		}
		if ok && setAll(c, "status", float64(status), "body", `{"success":false,"error":"`+message+`"}`) {
			n++
		}
	}
	return n
}

// --- the translations page ---

// panelFolder makes the folder of a vector of panel-edge.json: the edge
// fixture with the vector's files removed and written.
func panelFolder(t testing.TB, c any) string {
	t.Helper()
	dir := copyDir(t, "testdata/lang-edge")
	for _, f := range strs(c, "removeFiles") {
		if err := os.RemoveAll(filepath.Join(dir, f)); err != nil {
			t.Fatal(err)
		}
	}
	if o, ok := at(c, "write").(*Object); ok {
		for _, k := range o.Keys() {
			if err := os.WriteFile(filepath.Join(dir, k), []byte(str(o, k)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

// panelAnswer is the answer to the vector's request on dir.
func panelAnswer(t testing.TB, dir string, c any) answer {
	t.Helper()
	h, _, _ := handlersFor(t, dir, time.Now())
	srv := httptest.NewServer(routes(h))
	defer srv.Close()
	return do(t, srv.URL, str(c, "method"), str(c, "path"), nil, nil, 0)
}

func isOverview(c any) bool {
	return str(c, "method") == "GET" && str(c, "path") == "/api/panel/languages"
}

// answerPanel sets a panel vector's answer.
func answerPanel(c any, status int, body string) bool {
	return setAll(c, "status", float64(status), "body", body, "etag", httpx.ETag([]byte(body)))
}

// withRow is an overview's body with the row of code taken from another
// overview of the same folder.
func withRow(t testing.TB, body, from, code string) string {
	t.Helper()
	rowOf := func(overview any) (int, any) {
		for i, row := range list(overview, "languages") {
			if str(row, "code") == code {
				return i, row
			}
		}
		t.Fatalf("no row of %s in %s", code, short(stringify(overview, "")))
		return 0, nil
	}
	out, source := mustParse(t, body), mustParse(t, from)
	i, _ := rowOf(out)
	_, row := rowOf(source)
	list(out, "languages")[i] = row
	return stringify(out, "")
}

// markedFileRead rewrites what the edge fixture's de.json, which starts
// with a byte order mark, changes: its row in the overview and its
// missing keys are those of the same file without the mark.
func markedFileRead(t testing.TB, file any) int {
	n := 0
	for _, c := range list(file) {
		dir := panelFolder(t, c)
		de := filepath.Join(dir, "de.json")
		raw, err := os.ReadFile(de)
		if err != nil || !strings.HasPrefix(string(raw), byteOrderMark) || len(brokenFiles(t, dir, c)) > 0 {
			continue
		}
		if err := os.WriteFile(de, raw[len(byteOrderMark):], 0o644); err != nil {
			t.Fatal(err)
		}
		plain := panelAnswer(t, dir, c)
		switch {
		case isOverview(c) && num(c, "status") == 200 && plain.status == 200 && strings.Contains(str(c, "body"), `"code":"de"`):
			if answerPanel(c, 200, withRow(t, str(c, "body"), plain.body, "de")) {
				n++
			}
		case str(c, "path") == "/api/panel/languages/de/missing" && num(c, "status") == 500 && plain.status == 200:
			if answerPanel(c, 200, plain.body) {
				n++
			}
		}
	}
	return n
}

// brokenFilesListed rewrites the answers Node failed with 500 because a
// file held null or a reference file (en.json, pl.json) was broken. The
// answers are those for the folder without these files, and the overview
// has a row with the error for each of them.
func brokenFilesListed(t testing.TB, file any) int {
	n := 0
	for _, c := range list(file) {
		if num(c, "status") != 500 {
			continue
		}
		dir := panelFolder(t, c)
		errs := brokenFiles(t, dir, c)
		target, isMissing := strings.CutSuffix(strings.TrimPrefix(str(c, "path"), "/api/panel/languages/"), "/missing")
		if _, broken := errs[target]; len(errs) == 0 || (isMissing && broken) {
			continue
		}
		for code := range errs {
			if err := os.Remove(filepath.Join(dir, code+".json")); err != nil {
				t.Fatal(err)
			}
		}
		without := panelAnswer(t, dir, c)
		if without.status != 200 {
			continue
		}
		body := without.body
		if isOverview(c) {
			body = withErrorRows(t, body, errs)
		}
		if answerPanel(c, 200, body) {
			n++
		}
	}
	return n
}

// brokenFiles are the files of dir that made Node's answer to the vector c
// fail: in the overview a file that holds null, and en.json or pl.json
// when it holds null or does not parse; for the missing keys only the
// latter. They map the language code to the error.
func brokenFiles(t testing.TB, dir string, c any) map[string]string {
	t.Helper()
	errs := map[string]string{}
	items, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		name := item.Name()
		code, isJSON := strings.CutSuffix(name, ".json")
		if !isOverview(c) && code != "en" && code != "pl" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if !isJSON || err != nil {
			continue
		}
		data, err := parseJSON(strings.TrimPrefix(decodeUTF8(raw), byteOrderMark))
		switch {
		case err == nil && data == nil:
			errs[code] = "Cannot convert undefined or null to object"
		case err != nil && (code == "en" || code == "pl"):
			errs[code] = err.Error()
		}
	}
	return errs
}

// withErrorRows adds the rows of broken files to an overview, in file name
// order.
func withErrorRows(t testing.TB, body string, errs map[string]string) string {
	t.Helper()
	overview := mustParse(t, body)
	total := num(overview, "refKeyCount")
	rows := list(overview, "languages")
	if total == 0 {
		return body
	}
	for code, message := range errs {
		rows = append(rows, obj("code", code, "name", code, "native", code, "flag", "", "rtl", false, "needs_review", true,
			"is_reference", code == "en" || code == "pl", "total_keys", 0.0, "missing_keys", total, "extra_keys", 0.0,
			"empty_keys", 0.0, "coverage", 0.0, "error", message))
	}
	slices.SortStableFunc(rows, func(a, b any) int { return strings.Compare(str(a, "code")+".json", str(b, "code")+".json") })
	return stringify(obj("languages", rows, "refKeyCount", total), "")
}

// missingRefusesBadCodes rewrites the missing keys of a code that is not a
// language code into 400. The vectors of panel-real.json hold a hash of
// the body.
func missingRefusesBadCodes(t testing.TB, file any) int {
	const refused = `{"error":"Invalid language code"}`
	n := 0
	for _, c := range list(file) {
		escaped, isMissing := strings.CutSuffix(strings.TrimPrefix(str(c, "path"), "/api/panel/languages/"), "/missing")
		code, err := url.PathUnescape(escaped)
		if !isMissing || err != nil || ValidCode(code) || goMuxRedirects(str(c, "path")) {
			continue
		}
		changed := false
		if _, hashed := c.(*Object).Get("bodySha"); hashed {
			changed = setAll(c, "status", 400.0, "bodySha", sha(refused), "bodyLen", float64(len(refused)), "etag", httpx.ETag([]byte(refused)))
		} else {
			changed = answerPanel(c, 400, refused)
		}
		if changed {
			n++
		}
	}
	return n
}

// --- the request language (L1, L2, L4) ---

// pickRules are the rules of the request language: Node's (the zero
// value) and the fixes that changed them.
type pickRules struct {
	// loadedOnly: only a loaded language counts, no inherited name (L1).
	loadedOnly bool
	// textOnly: a query or cookie value counts only when it is a string
	// (L2).
	textOnly bool
	// skipUnknown: a value that is no language gives way to the next
	// source instead of to the default language (L4).
	skipUnknown bool
}

// referencePick is the request language of one vector of pick.json,
// written out by hand under the rules, on b's languages. Under the zero
// rules it gives Node's answers (TestRequestLanguageLikeNode checks that).
// It returns the language, the Set-Cookie value or "", and the error
// message of a request that fails.
func referencePick(b *Bundle, c any, rules pickRules, now time.Time) (lang, setCookie, throws string) {
	has := func(name string) bool {
		if _, ok := b.translations[name]; ok {
			return true
		}
		return !rules.loadedOnly && nodeInheritedNames[name]
	}
	given := func(v any) bool {
		if _, isText := v.(string); rules.textOnly && !isText {
			return false
		}
		return v != undefined && truthy(v)
	}

	query := qsParam(str(c, "query"), "lang")
	var cookie any = undefined
	if header, ok := at(c, "cookie").(string); ok {
		raw := toBytes(header)
		if value, found := cookieValue(latin1(raw), len(raw), CookieName); found {
			cookie = value
			if strings.HasPrefix(value, "j:") {
				if parsed, err := parseJSON(value[2:]); err == nil && truthy(parsed) {
					cookie = parsed
				}
			}
		}
	}
	accept := ""
	if header, ok := at(c, "accept").(string); ok {
		accept = toBytes(header)
	}

	// query, preference, cookie, Accept-Language, default: Node takes the
	// first given one and the default when that is no language
	var sources []any
	if given(query) {
		sources = append(sources, query)
	}
	if flag(c, "hasPref") && str(c, "pref") != "" {
		sources = append(sources, str(c, "pref"))
	}
	if given(cookie) {
		sources = append(sources, cookie)
	}
	if picked := acceptedLanguage(has, accept); picked != "" {
		sources = append(sources, picked)
	}
	lang = str(c, "def")
	for _, source := range sources {
		name, ok := jsString(source)
		if !ok {
			return "", "", ErrNotText.Error()
		}
		if has(name) {
			lang = name
			break
		}
		if !rules.skipUnknown {
			break
		}
	}
	if given(query) {
		if name, _ := jsString(query); has(name) {
			value := name
			if isObjectLike(query) {
				value = "j:" + stringify(query, "")
			}
			setCookie = LangCookie(value, false, now)
		}
	}
	return lang, setCookie, ""
}

// pickWith rewrites the vectors of pick.json to the answers under rules.
func pickWith(rules pickRules) func(t testing.TB, file any) int {
	return func(t testing.TB, file any) int {
		b, _ := loaded(t, copyDir(t, realLangDir), "en")
		now := time.UnixMilli(int64(num(file, "now")))
		n := 0
		for _, c := range list(file, "vectors") {
			lang, cookie, throws := referencePick(b, c, rules, now)
			cookies := []any{}
			if cookie != "" {
				cookies = []any{cookie}
			}
			var langValue, throwsValue any = lang, nil
			if throws != "" {
				langValue, throwsValue = nil, throws
			}
			if setAll(c, "lang", langValue, "throws", throwsValue, "setCookie", cookies) {
				n++
			}
		}
		return n
	}
}
