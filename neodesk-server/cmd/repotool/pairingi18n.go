package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// checkPairingI18n is "check-pairing-i18n": every console language file
// has every key of the "pairing" namespace that en.json has, and the
// sidebar entry nav.pairing. A key that is there but empty in a language
// other than English is reported as a warning, not a failure.
func checkPairingI18n(root string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, "usage: repotool check-pairing-i18n")
		return 2
	}
	const namespace = "pairing"
	langDir := filepath.Join(root, "betterdesk-server", "console", "static", "files", "lang")

	entries, err := os.ReadDir(langDir)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	var files []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			files = append(files, e.Name())
		}
	}
	slices.Sort(files)
	if len(files) == 0 {
		fmt.Fprintln(stderr, "error: no locale files found")
		return 2
	}

	en, err := readJSONFile(filepath.Join(langDir, "en.json"))
	if err != nil {
		fmt.Fprintf(stderr, "error: en.json: %v\n", err)
		return 1
	}
	expected := jsKeys(property(en, namespace))
	if len(expected) == 0 {
		fmt.Fprintf(stderr, "error: en.json has no %q namespace\n", namespace)
		return 2
	}

	var failures []string
	for _, name := range files {
		locale := strings.TrimSuffix(name, ".json")
		data, err := readJSONFile(filepath.Join(langDir, name))
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: invalid JSON (%v)", name, err))
			continue
		}
		ns := property(data, namespace)
		var missing, empty []string
		for _, key := range expected {
			if !jsHas(ns, key) {
				missing = append(missing, key)
			}
			if v, _ := propertyValue(ns, key); !jsTruthy(v) || jsTrim(jsString(v)) == "" {
				empty = append(empty, key)
			}
		}
		if len(missing) > 0 {
			failures = append(failures, fmt.Sprintf("%s: missing %s.%s", locale, namespace, strings.Join(missing, ", ")))
		}
		if len(empty) > 0 && locale != "en" {
			// A new namespace ships with translations; an empty one is
			// shown, not failed, so an untranslated string stays visible
			fmt.Fprintf(stderr, "warn: %s: untranslated %s.%s\n", locale, namespace, strings.Join(empty, ", "))
		}
		if nav := property(data, "nav"); !jsTruthy(nav) || !jsTruthy(property(nav, "pairing")) {
			failures = append(failures, locale+": missing nav.pairing (sidebar entry)")
		}
	}

	if len(failures) > 0 {
		fmt.Fprintln(stderr, "Pairing i18n parity failed:")
		for _, f := range failures {
			fmt.Fprintf(stderr, "  - %s\n", f)
		}
		return 1
	}
	fmt.Fprintf(stdout, "Pairing i18n OK (%d keys x %d locales)\n", len(expected), len(files))
	return 0
}

// readJSONFile reads and parses a JSON file as JSON.parse would.
func readJSONFile(path string) (any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseJSON(data)
}

// property is value[key] for an object, or nil.
func property(value any, key string) any {
	v, _ := propertyValue(value, key)
	return v
}

func propertyValue(value any, key string) (any, bool) {
	switch t := value.(type) {
	case *jsObject:
		return t.get(key)
	case []any:
		if i, err := strconv.Atoi(key); err == nil && isArrayIndex(key) && i < len(t) {
			return t[i], true
		}
	}
	return nil, false
}

// jsKeys is Object.keys(value || {}) for an object or array.
func jsKeys(value any) []string {
	switch t := value.(type) {
	case *jsObject:
		return slices.Clone(t.keys)
	case []any:
		keys := make([]string, len(t))
		for i := range t {
			keys[i] = strconv.Itoa(i)
		}
		return keys
	}
	return nil
}

// objectPrototypeKeys are the properties every object inherits, which
// the "in" operator finds on any object.
var objectPrototypeKeys = []string{
	"constructor", "__defineGetter__", "__defineSetter__", "hasOwnProperty",
	"__lookupGetter__", "__lookupSetter__", "isPrototypeOf", "propertyIsEnumerable",
	"toString", "valueOf", "__proto__", "toLocaleString",
}

// jsHas is `key in (value || {})`.
func jsHas(value any, key string) bool {
	if _, ok := propertyValue(value, key); ok {
		return true
	}
	return slices.Contains(objectPrototypeKeys, key)
}
