// Package i18n holds the console's translations: the lang/*.json files, the
// language of a request, the strings endpoint and the language routes.
//
// The files stay JSON that translators edit. The web console and the
// Node code beside this server read them with JavaScript, so the package
// keeps their content in values that behave like JavaScript objects. Every
// answer built from them then has the bytes a JavaScript engine writes:
// key order, number format, fallbacks, and the same answers for odd files
// (a file that holds an array, a broken file, a byte order mark).
package i18n

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// knownLanguage is one row of the table of languages the console loads.
// A file whose code is not in the table is never loaded.
type knownLanguage struct {
	code, name, native, flag string
	rtl                      bool
}

var knownLanguages = []knownLanguage{
	{"en", "English", "English", "🇬🇧", false},
	{"pl", "Polish", "Polski", "🇵🇱", false},
	{"de", "German", "Deutsch", "🇩🇪", false},
	{"fr", "French", "Français", "🇫🇷", false},
	{"es", "Spanish", "Español", "🇪🇸", false},
	{"it", "Italian", "Italiano", "🇮🇹", false},
	{"pt", "Portuguese", "Português", "🇵🇹", false},
	{"nl", "Dutch", "Nederlands", "🇳🇱", false},
	{"zh", "Chinese", "中文", "🇨🇳", false},
	{"ja", "Japanese", "日本語", "🇯🇵", false},
	{"ko", "Korean", "한국어", "🇰🇷", false},
	{"ar", "Arabic", "العربية", "🇸🇦", true},
	{"he", "Hebrew", "עברית", "🇮🇱", true},
	{"uk", "Ukrainian", "Українська", "🇺🇦", false},
	{"tr", "Turkish", "Türkçe", "🇹🇷", false},
	{"hi", "Hindi", "हिन्दी", "🇮🇳", false},
	{"sv", "Swedish", "Svenska", "🇸🇪", false},
	{"nb", "Norwegian", "Norsk", "🇳🇴", false},
	{"da", "Danish", "Dansk", "🇩🇰", false},
	{"fi", "Finnish", "Suomi", "🇫🇮", false},
	{"cs", "Czech", "Čeština", "🇨🇿", false},
	{"hu", "Hungarian", "Magyar", "🇭🇺", false},
	{"ro", "Romanian", "Română", "🇷🇴", false},
	{"th", "Thai", "ไทย", "🇹🇭", false},
	{"vi", "Vietnamese", "Tiếng Việt", "🇻🇳", false},
	{"id", "Indonesian", "Bahasa Indonesia", "🇮🇩", false},
	{"zh-TW", "Chinese (Traditional)", "繁體中文", "🇹🇼", false},
}

func knownLanguageFor(code string) (knownLanguage, bool) {
	for _, l := range knownLanguages {
		if l.code == code {
			return l, true
		}
	}
	return knownLanguage{}, false
}

var validCode = regexp.MustCompile(`^[a-z]{2,8}(-[A-Z][a-zA-Z]{1,7})?$`)

// ValidCode reports whether code has the form of a language code: two to
// eight lower case letters, optionally a region or script like -TW. It is
// the check that keeps a code from naming a path.
func ValidCode(code string) bool {
	return validCode.MatchString(code)
}

// UnsafeKey reports whether a key is one the console never reads or merges
// from a language file, because in JavaScript it would reach an object's
// prototype.
func UnsafeKey(key string) bool {
	return key == "__proto__" || key == "prototype" || key == "constructor"
}

// Bundle is the set of loaded languages.
type Bundle struct {
	// Dir is the folder with the <code>.json files.
	Dir string
	// FS holds the <code>.json files at its top in place of Dir, which then
	// only names the folder in messages. Its files are read only: saving
	// or deleting a language fails as on a read-only file system.
	FS fs.FS
	// DefaultLang is the language whose texts fill what another lacks.
	DefaultLang string
	// Logf writes the load messages. log.Printf when nil.
	Logf func(format string, args ...any)

	mu           sync.RWMutex
	translations map[string]any
	available    map[string]*Object
	order        []string
}

// New returns a bundle for the files in dir. defaultLang is the fallback
// language, "en" when empty. Call Load to read the files.
func New(dir, defaultLang string) *Bundle {
	if defaultLang == "" {
		defaultLang = "en"
	}
	return &Bundle{Dir: dir, DefaultLang: defaultLang, translations: map[string]any{}, available: map[string]*Object{}}
}

func (b *Bundle) logf(format string, args ...any) {
	if b.Logf != nil {
		b.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// logValue makes a value safe for one log line: no line breaks, at most
// 120 characters.
func logValue(s string) string {
	s = strings.NewReplacer("\r", " ", "\n", " ", "\x00", " ").Replace(s)
	return sliceUnits(s, 120)
}

// Load reads every known language from Dir, in the order of the table, and
// logs how many it has. A file that cannot be read or parsed is logged and
// skipped. Languages loaded earlier stay.
func (b *Bundle) Load() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.dirExists() {
		b.logf("Language directory not found %s", logValue(b.Dir))
	} else {
		root := b.root()
		for _, l := range knownLanguages {
			b.loadLocked(root, l.code)
		}
	}
	b.logf("i18n: Loaded languages %d", len(b.order))
}

// loadLocked reads one language file. It reports whether the language is
// now loaded from it.
func (b *Bundle) loadLocked(root langRoot, code string) bool {
	if !ValidCode(code) {
		b.logf("i18n: Invalid language code rejected %s", logValue(code))
		return false
	}
	known, ok := knownLanguageFor(code)
	if !ok {
		b.logf("i18n: Skipping unsupported language code %s", logValue(code))
		return false
	}
	path, err := root.file(code)
	if err != nil {
		b.logf("Failed to load language %s %s", logValue(code), err)
		return false
	}
	if err := b.stat(path); err != nil {
		return false
	}
	raw, err := b.readFile(path)
	if err != nil {
		b.logf("Failed to load language %s %s", logValue(code), nodeFSMessage(err, "read", path))
		return false
	}
	data, err := parseJSON(strings.TrimPrefix(decodeUTF8(raw), byteOrderMark))
	if err != nil {
		b.logf("Failed to load language %s %s", logValue(code), err)
		return false
	}
	b.translations[code] = data
	if data == nil {
		// The texts are stored before the metadata is read, so a file
		// that holds null counts as a language without being listed.
		b.logf("Failed to load language %s %s", logValue(code), "Cannot read properties of null (reading '_meta')")
		return false
	}

	fileMeta := property(data, "_meta")
	if !truthy(fileMeta) {
		fileMeta = property(data, "meta")
	}
	or := func(fallback any, keys ...string) any {
		for _, k := range keys {
			if v := property(fileMeta, k); truthy(v) {
				return v
			}
		}
		return fallback
	}
	rtl := known.rtl
	if v := property(fileMeta, "rtl"); v != nil && v != undefined {
		rtl = truthy(v)
	}
	entry := NewObject()
	entry.Set("code", code)
	entry.Set("name", or(known.name, "name"))
	entry.Set("native", or(known.native, "native", "native_name"))
	entry.Set("flag", or(known.flag, "flag"))
	entry.Set("rtl", rtl)
	if _, listed := b.available[code]; !listed {
		b.order = append(b.order, code)
	}
	b.available[code] = entry
	return true
}

// Has reports whether code is a loaded language.
func (b *Bundle) Has(code string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.translations[code]
	return ok
}

// Translations returns the texts of a language as its file has them, or
// nil when the language is not loaded or its file holds a false value.
func (b *Bundle) Translations(code string) any {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if v := b.translations[code]; truthy(v) {
		return v
	}
	return nil
}

// Merged returns the texts of a language with the default language filling
// every key it lacks. For a language that is not loaded it returns the
// default language's texts.
func (b *Bundle) Merged(code string) any {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.mergedLocked(code)
}

func (b *Bundle) mergedLocked(code string) any {
	var fallback any = NewObject()
	if v := b.translations[b.DefaultLang]; truthy(v) {
		fallback = v
	}
	requested, ok := b.translations[code]
	if !ok || !truthy(requested) {
		return fallback
	}
	if code == b.DefaultLang {
		return requested
	}
	return deepMerge(fallback, requested)
}

// deepMerge lays overlay over base. Objects merge key by key; anything
// else in overlay replaces what base has, also an array or a string in
// place of an object. Unsafe keys of overlay are left out.
func deepMerge(base, overlay any) any {
	if !isObjectLike(base) {
		return overlay
	}
	if !isObjectLike(overlay) {
		return base
	}
	if list, ok := base.([]any); ok {
		// A default language whose file is a list: only elements that
		// keep the list dense are taken over.
		merged := append([]any(nil), list...)
		entries(overlay, func(key string, value any) {
			if !isArrayIndex(key) {
				return
			}
			n, err := strconv.Atoi(key)
			if err != nil {
				return
			}
			switch {
			case n < len(merged):
				merged[n] = value
			case n == len(merged):
				merged = append(merged, value)
			}
		})
		return merged
	}
	merged := base.(*Object).clone()
	entries(overlay, func(key string, value any) {
		if UnsafeKey(key) {
			return
		}
		if _, ok := value.(*Object); ok {
			if inner, ok := property(base, key).(*Object); ok {
				merged.Set(key, deepMerge(inner, value))
				return
			}
		}
		merged.Set(key, value)
	})
	return merged
}

func isObjectLike(v any) bool {
	switch v.(type) {
	case *Object, []any:
		return true
	}
	return false
}

// Meta returns a language's entry of the available list (code, name,
// native, flag, rtl), or nil when it is not listed.
func (b *Bundle) Meta(code string) *Object {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.available[code]
}

// Available returns the listed languages keyed by code, in the order they
// were loaded. A language that is saved again keeps its place; one that
// was deleted and saved again moves to the end.
func (b *Bundle) Available() *Object {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := NewObject()
	for _, code := range b.order {
		out.Set(code, b.available[code])
	}
	return out
}

// errSortKey is the failure of AvailableList when a language's name is not
// text: such a name has no way to be compared (a TypeError in JavaScript).
var errSortKey = errors.New("language name is not a string")

// AvailableList returns the listed languages sorted by their native name
// (then name, then code), ignoring case and accents. Names that compare
// equal keep their load order.
func (b *Bundle) AvailableList() ([]*Object, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	list := make([]*Object, 0, len(b.order))
	keys := make([]string, 0, len(b.order))
	for i, code := range b.order {
		entry := b.available[code]
		var key any = code
		for _, field := range []string{"native", "name"} {
			if v, _ := entry.Get(field); truthy(v) {
				key = v
				break
			}
		}
		text, isString := key.(string)
		if !isString && len(b.order) > 1 {
			// Every entry but the first is at some point the one whose
			// localeCompare method is called; the first is only ever
			// the argument, which is turned into text.
			if i > 0 {
				return nil, errSortKey
			}
			var ok bool
			if text, ok = jsString(key); !ok {
				return nil, errSortKey
			}
		}
		list = append(list, entry)
		keys = append(keys, text)
	}
	sortByBaseLetters(list, keys)
	return list, nil
}

// Var is one placeholder value for Translate.
type Var struct {
	Name  string
	Value any
}

// Vars are placeholder values in the order they are applied. As in the
// JavaScript object they stand for, names that are whole numbers come
// first in ascending order, and a repeated name keeps its first place with
// its last value.
type Vars []Var

// V builds Vars from name, value pairs: V("name", "Ada", "count", 3).
func V(pairs ...any) Vars {
	out := make(Vars, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		name, _ := pairs[i].(string)
		out = append(out, Var{Name: name, Value: pairs[i+1]})
	}
	return out
}

// Undefined is a placeholder value that prints as "undefined", like a
// variable the JavaScript caller passed without a value.
var Undefined any = undefined

func varText(v any) string {
	switch x := v.(type) {
	case int:
		v = float64(x)
	case int8:
		v = float64(x)
	case int16:
		v = float64(x)
	case int32:
		v = float64(x)
	case int64:
		v = float64(x)
	case uint:
		v = float64(x)
	case uint8:
		v = float64(x)
	case uint16:
		v = float64(x)
	case uint32:
		v = float64(x)
	case uint64:
		v = float64(x)
	case float32:
		v = float64(x)
	}
	if s, ok := jsString(v); ok {
		return s
	}
	return fmt.Sprint(v)
}

// Translate looks key up in lang, then in the default language, and
// returns the key itself when neither has it.
//
// key is a dot path ("devices.count"). A path through an unsafe key finds
// nothing. The value comes back as the file has it: usually a string, but
// a path that names an object, a list, a number, true, false or null
// returns that (null as nil), and an empty string stays an empty string
// without falling back. A path can go into a string or a list by index or
// "length".
//
// In a string value each {name} of vars is replaced by its value, in the
// order of Vars, so a value that contains another placeholder can be
// replaced again by a later variable. Placeholders without a variable
// stay as they are. Numbers print as JavaScript prints them, nil as
// "null".
func (b *Bundle) Translate(lang, key string, vars Vars) any {
	b.mu.RLock()
	defer b.mu.RUnlock()
	value := lookup(b.translations[lang], key)
	if value == undefined && lang != b.DefaultLang {
		value = lookup(b.translations[b.DefaultLang], key)
	}
	if value == undefined {
		return key
	}
	text, isString := value.(string)
	if !isString || len(vars) == 0 {
		return value
	}
	ordered := NewObject()
	for _, v := range vars {
		ordered.Set(v.Name, v.Value)
	}
	for _, name := range ordered.Keys() {
		v, _ := ordered.Get(name)
		text = strings.ReplaceAll(text, "{"+name+"}", varText(v))
	}
	return text
}

// T is Translate with the result as text, the way a JavaScript template
// string prints it: an object as "[object Object]", a list joined by
// commas, null as "null".
func (b *Bundle) T(lang, key string, vars Vars) string {
	v := b.Translate(lang, key, vars)
	if s, ok := jsString(v); ok {
		return s
	}
	return key
}

func lookup(root any, key string) any {
	if !truthy(root) || key == "" {
		return undefined
	}
	current := root
	for _, part := range strings.Split(key, ".") {
		if UnsafeKey(part) || current == nil || current == undefined {
			return undefined
		}
		current = property(current, part)
	}
	return current
}

// Report is the result of Validate.
type Report struct {
	// Found is false when the language is not loaded; the report then
	// only says so.
	Found bool
	// Valid is true when no key of the default language is missing.
	Valid bool
	// Missing are the default language's keys the language lacks, Extra
	// the keys only the language has (keys starting with _ left out).
	Missing, Extra []string
	// Coverage is the share of the default language's keys the language
	// has, in percent with one decimal ("98.7"), or "NaN" when the
	// default language has no keys. Empty for the default language.
	Coverage string
}

// Object returns the report in the shape the console's API sends.
func (r Report) Object() *Object {
	out := NewObject()
	out.Set("valid", r.Valid)
	if !r.Found {
		out.Set("error", "Language not found")
		return out
	}
	out.Set("missing", stringList(r.Missing))
	out.Set("extra", stringList(r.Extra))
	if r.Coverage != "" {
		out.Set("coverage", r.Coverage)
	}
	return out
}

func stringList(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// Validate compares a language's keys with the default language's.
func (b *Bundle) Validate(code string) Report {
	b.mu.RLock()
	defer b.mu.RUnlock()
	texts := b.translations[code]
	if !truthy(texts) {
		return Report{}
	}
	if code == b.DefaultLang {
		return Report{Found: true, Valid: true}
	}
	defaultKeys := allKeys(b.translations[b.DefaultLang], "")
	langKeys := allKeys(texts, "")
	inLang := make(map[string]bool, len(langKeys))
	for _, k := range langKeys {
		inLang[k] = true
	}
	inDefault := make(map[string]bool, len(defaultKeys))
	for _, k := range defaultKeys {
		inDefault[k] = true
	}
	r := Report{Found: true, Missing: []string{}, Extra: []string{}}
	for _, k := range defaultKeys {
		if !inLang[k] {
			r.Missing = append(r.Missing, k)
		}
	}
	for _, k := range langKeys {
		if !inDefault[k] && !strings.HasPrefix(k, "_") {
			r.Extra = append(r.Extra, k)
		}
	}
	r.Valid = len(r.Missing) == 0
	total := float64(len(defaultKeys))
	r.Coverage = toFixed1((total - float64(len(r.Missing))) / total * 100)
	return r
}

// allKeys flattens the texts into dot paths. An object adds the paths of
// its members (none when it is empty); everything else is one path.
func allKeys(v any, prefix string) []string {
	var keys []string
	if !truthy(v) {
		return keys
	}
	entries(v, func(key string, value any) {
		full := key
		if prefix != "" {
			full = prefix + "." + key
		}
		if obj, ok := value.(*Object); ok {
			keys = append(keys, allKeys(obj, full)...)
		} else {
			keys = append(keys, full)
		}
	})
	return keys
}

// Result is the outcome of Save or Delete. Error holds the text the
// console's API sends when Success is false.
type Result struct {
	Success bool
	Error   string
}

// Save writes texts to <code>.json and loads the language again.
//
// The code must pass ValidCode and the file must lie in Dir after symbolic
// links are resolved. The file is written as JSON with two spaces of
// indentation, in UTF-8, without a trailing newline, in place: the
// existing file is truncated and rewritten, not replaced by a rename. A
// code outside the table of known languages is written but not loaded.
func (b *Bundle) Save(code string, texts any) Result {
	if !ValidCode(code) {
		return Result{Error: "Invalid language code format"}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	root := b.root()
	path, err := root.file(code)
	if err != nil {
		return Result{Error: err.Error()}
	}
	if err := b.writeFile(path, []byte(stringify(texts, "  "))); err != nil {
		return Result{Error: nodeFSMessage(err, "open", path)}
	}
	b.loadLocked(root, code)
	return Result{Success: true}
}

// Delete removes <code>.json and forgets the language. English and Polish
// cannot be deleted. Deleting a language that has no file succeeds.
func (b *Bundle) Delete(code string) Result {
	if !ValidCode(code) {
		return Result{Error: "Invalid language code format"}
	}
	if code == "en" || code == "pl" {
		return Result{Error: "Cannot delete core language"}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	path, err := b.root().file(code)
	if err != nil {
		return Result{Error: err.Error()}
	}
	if err := b.stat(path); err == nil {
		if err := b.remove(path); err != nil {
			return Result{Error: nodeFSMessage(err, "unlink", path)}
		}
	}
	delete(b.translations, code)
	if _, listed := b.available[code]; listed {
		delete(b.available, code)
		for i, c := range b.order {
			if c == code {
				b.order = append(b.order[:i], b.order[i+1:]...)
				break
			}
		}
	}
	return Result{Success: true}
}

// langRoot is the language folder as an absolute path, and with its
// symbolic links resolved for comparing. In FS it is Dir as written, and
// it has no links.
type langRoot struct {
	abs, real string
	inFS      bool
}

func (b *Bundle) root() langRoot {
	if b.FS != nil {
		return langRoot{abs: b.Dir, inFS: true}
	}
	abs, err := filepath.Abs(b.Dir)
	if err != nil {
		abs = b.Dir
	}
	return langRoot{abs: abs, real: comparablePath(abs)}
}

// file returns the path of a language file and makes sure it lies in the
// folder, also after symbolic links are resolved.
func (r langRoot) file(code string) (string, error) {
	name := code + ".json"
	if strings.ContainsAny(name, "\x00/\\") || name == "." || name == ".." {
		return "", errors.New("Invalid path segment")
	}
	target := filepath.Join(r.abs, name)
	if r.inFS {
		return target, nil
	}
	rel, err := filepath.Rel(r.real, comparablePath(target))
	if err != nil || (rel != "." && (strings.HasPrefix(rel, "..") || filepath.IsAbs(rel))) {
		return "", errors.New("Path outside allowed directory")
	}
	return target, nil
}

// comparablePath resolves the symbolic links of the part of a path that
// exists and keeps the rest as written.
func comparablePath(p string) string {
	var rest []string
	existing := p
	for {
		if _, err := os.Stat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		rest = append([]string{filepath.Base(existing)}, rest...)
		existing = parent
	}
	if _, err := os.Stat(existing); err == nil {
		if real, err := filepath.EvalSymlinks(existing); err == nil {
			p = filepath.Join(append([]string{real}, rest...)...)
		}
	}
	if runtime.GOOS == "windows" {
		p = strings.ToLower(strings.TrimPrefix(p, `\\?\`))
	}
	return p
}

// nodeFSMessage words a file error the way Node does, because the text
// goes into answers and log lines: "EACCES: permission denied, open '/x'".
func nodeFSMessage(err error, call, path string) string {
	code, text := "", ""
	switch {
	case errors.Is(err, syscall.EISDIR):
		code, text = "EISDIR", "illegal operation on a directory"
	case errors.Is(err, fs.ErrNotExist):
		code, text = "ENOENT", "no such file or directory"
	case errors.Is(err, syscall.EROFS):
		code, text = "EROFS", "read-only file system"
	case errors.Is(err, syscall.ENOSPC):
		code, text = "ENOSPC", "no space left on device"
	case errors.Is(err, syscall.ENOTDIR):
		code, text = "ENOTDIR", "not a directory"
	case errors.Is(err, syscall.EPERM):
		code, text = "EPERM", "operation not permitted"
	case errors.Is(err, fs.ErrPermission):
		code, text = "EACCES", "permission denied"
	default:
		return err.Error()
	}
	if call == "read" {
		// Reading a whole file fails in open, except for a directory,
		// which opens and then cannot be read.
		if code == "EISDIR" {
			return code + ": " + text + ", read"
		}
		call = "open"
	}
	return code + ": " + text + ", " + call + " '" + path + "'"
}

// byteOrderMark is U+FEFF, which some editors put at the start of a file.
const byteOrderMark = string(rune(0xFEFF))
