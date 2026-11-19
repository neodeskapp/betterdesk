package i18n

import (
	"errors"
	"math"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
)

// The translations page of the console. These routes read the files in the
// language folder directly, every *.json file and not only the known
// languages, and compare them with English and Polish together.

var errNotData = errors.New("cannot read the keys of null")

// flatKeys lists the texts of a file as dot paths with their values as
// text, in file order. A repeated path keeps its first place and its last
// value.
type flatKeys struct {
	order  []string
	values map[string]string
}

func (f *flatKeys) set(key, value string) {
	if _, ok := f.values[key]; !ok {
		f.order = append(f.order, key)
	}
	f.values[key] = value
}

// flatten walks data. It fails for null, as Object.entries(null) does.
func flatten(data any, prefix string, out *flatKeys) error {
	if data == nil {
		return errNotData
	}
	var failed error
	entries(data, func(key string, value any) {
		if failed != nil {
			return
		}
		full := key
		if prefix != "" {
			full = prefix + "." + key
		}
		if inner, ok := value.(*Object); ok {
			failed = flatten(inner, full, out)
			return
		}
		text := ""
		if value != nil {
			var ok bool
			if text, ok = jsString(value); !ok {
				failed = ErrNotText
				return
			}
		}
		out.set(full, text)
	})
	return failed
}

func newFlatKeys() *flatKeys { return &flatKeys{values: map[string]string{}} }

// readJSONFile reads and parses a file; a byte order mark at its start is
// dropped, as the loader drops it. message is the error in Node's words.
func (b *Bundle) readJSONFile(path string) (data any, message string, ok bool) {
	raw, err := b.readFile(path)
	if err != nil {
		return nil, nodeFSMessage(err, "read", path), false
	}
	data, err = parseJSON(strings.TrimPrefix(decodeUTF8(raw), byteOrderMark))
	if err != nil {
		return nil, err.Error(), false
	}
	return data, "", true
}

// references are the keys of the reference languages.
type references struct {
	byCode map[string]*flatKeys
	// keys are the keys of English, then the keys only Polish has.
	keys []string
	has  map[string]bool
}

func (h *Handlers) loadReferences() (*references, error) {
	refs := &references{byCode: map[string]*flatKeys{}, has: map[string]bool{}}
	for _, code := range []string{"en", "pl"} {
		path := filepath.Join(h.Bundle.Dir, code+".json")
		if err := h.Bundle.stat(path); err != nil {
			continue
		}
		data, message, ok := h.Bundle.readJSONFile(path)
		if !ok {
			return nil, errors.New(message)
		}
		keys := newFlatKeys()
		if err := flatten(data, "", keys); err != nil {
			return nil, err
		}
		refs.byCode[code] = keys
		for _, k := range keys.order {
			if !refs.has[k] {
				refs.has[k] = true
				refs.keys = append(refs.keys, k)
			}
		}
	}
	return refs, nil
}

func (b *Bundle) jsonNames() ([]string, error) {
	items, err := b.readDir()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, item := range items {
		if strings.HasSuffix(item.Name(), ".json") {
			names = append(names, item.Name())
		}
	}
	// JavaScript's default sort: by UTF-16 code units.
	sort.SliceStable(names, func(i, j int) bool {
		a, b := units(decodeUTF8([]byte(names[i]))), units(decodeUTF8([]byte(names[j])))
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
	return names, nil
}

func isMetaKey(key string) bool {
	return strings.HasPrefix(key, "_meta") || strings.HasPrefix(key, "meta.")
}

// PanelLanguages answers GET /api/panel/languages: every language file
// with its key counts and coverage against the reference keys.
func (h *Handlers) PanelLanguages(w http.ResponseWriter, r *http.Request) {
	languages, total, err := h.panelLanguages()
	if err != nil {
		send(w, r, http.StatusInternalServerError, obj("error", "Failed to load languages"))
		return
	}
	send(w, r, http.StatusOK, obj("languages", languages, "refKeyCount", float64(total)))
}

func (h *Handlers) panelLanguages() ([]any, int, error) {
	refs, err := h.loadReferences()
	if err != nil {
		return nil, 0, err
	}
	languages := []any{}
	if len(refs.keys) == 0 {
		return languages, 0, nil
	}
	names, err := h.Bundle.jsonNames()
	if err != nil {
		return nil, 0, err
	}
	total := len(refs.keys)
	for _, file := range names {
		code := strings.Replace(decodeUTF8([]byte(file)), ".json", "", 1)
		reference := code == "en" || code == "pl"
		data, message, ok := h.Bundle.readJSONFile(filepath.Join(h.Bundle.Dir, file))
		if !ok {
			languages = append(languages, obj(
				"code", code, "name", code, "native", code, "flag", "", "rtl", false, "needs_review", true,
				"is_reference", reference, "total_keys", 0.0, "missing_keys", float64(total), "extra_keys", 0.0,
				"empty_keys", 0.0, "coverage", 0.0, "error", message))
			continue
		}
		if data == nil {
			return nil, 0, errNotData
		}
		meta := property(data, "_meta")
		if !truthy(meta) {
			meta = property(data, "meta")
		}
		or := func(fallback any, keys ...string) any {
			for _, k := range keys {
				if v := property(meta, k); truthy(v) {
					return v
				}
			}
			return fallback
		}
		keys := newFlatKeys()
		if err := flatten(data, "", keys); err != nil {
			return nil, 0, err
		}
		missing, extra, empty := 0, 0, 0
		for _, k := range refs.keys {
			if _, ok := keys.values[k]; !ok {
				missing++
			}
		}
		for _, k := range keys.order {
			if !refs.has[k] {
				if !isMetaKey(k) {
					extra++
				}
			} else if keys.values[k] == "" {
				empty++
			}
		}
		coverage := 100.0
		if code != "en" {
			coverage = jsRound(float64(total-missing) / float64(total) * 100)
		}
		if reference {
			missing = 0
		}
		languages = append(languages, obj(
			"code", code, "name", or(code, "name"), "native", or(code, "native_name", "name"), "flag", or("", "flag"),
			"rtl", or(false, "rtl"), "needs_review", or(false, "needs_review"), "is_reference", reference,
			"total_keys", float64(len(keys.order)), "missing_keys", float64(missing), "extra_keys", float64(extra),
			"empty_keys", float64(empty), "coverage", coverage, "error", nil))
	}
	return languages, total, nil
}

// jsRound is Math.round: halves go up.
func jsRound(x float64) float64 {
	return math.Floor(x + 0.5)
}

// PanelMissing answers GET /api/panel/languages/{code}/missing: the
// reference keys a language file lacks, with their English and Polish
// texts, and the keys only it has. Every character of the code that is
// not a letter or a dash is dropped before the file is looked up; the
// answer repeats the code as it was sent.
func (h *Handlers) PanelMissing(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	result, status := h.panelMissing(code)
	if status == http.StatusNotFound {
		send(w, r, status, obj("error", "Language not found"))
		return
	}
	if result == nil {
		send(w, r, http.StatusInternalServerError, obj("error", "Failed to analyze language"))
		return
	}
	send(w, r, http.StatusOK, result)
}

func (h *Handlers) panelMissing(code string) (*Object, int) {
	var letters strings.Builder
	for _, c := range code {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-' {
			letters.WriteRune(c)
		}
	}
	if letters.Len() == 0 {
		return nil, http.StatusInternalServerError
	}
	path := filepath.Join(h.Bundle.Dir, letters.String()+".json")
	if err := h.Bundle.stat(path); err != nil {
		return nil, http.StatusNotFound
	}
	refs, err := h.loadReferences()
	if err != nil {
		return nil, http.StatusInternalServerError
	}
	data, _, ok := h.Bundle.readJSONFile(path)
	if !ok {
		return nil, http.StatusInternalServerError
	}
	keys := newFlatKeys()
	if err := flatten(data, "", keys); err != nil {
		return nil, http.StatusInternalServerError
	}
	text := func(lang, key string) string {
		if ref := refs.byCode[lang]; ref != nil {
			return ref.values[key]
		}
		return ""
	}
	missing := []any{}
	for _, k := range refs.keys {
		if _, ok := keys.values[k]; !ok {
			missing = append(missing, obj("key", k, "en_value", text("en", k), "pl_value", text("pl", k)))
		}
	}
	extra := []any{}
	for _, k := range keys.order {
		if !refs.has[k] && !isMetaKey(k) {
			extra = append(extra, k)
		}
	}
	return obj("code", code, "missing", missing, "extra", extra, "total", float64(len(refs.keys))), http.StatusOK
}

// PanelFix answers POST /api/panel/languages/{code}/fix. Filling missing
// keys automatically is switched off.
func (h *Handlers) PanelFix(w http.ResponseWriter, r *http.Request) {
	send(w, r, http.StatusGone, obj("error", "Automatic language fixing is disabled. Missing keys must be translated manually in the target language."))
}
