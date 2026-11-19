package i18n

import (
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
