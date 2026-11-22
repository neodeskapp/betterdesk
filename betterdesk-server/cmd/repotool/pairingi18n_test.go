package main

import (
	"strings"
	"testing"
)

const langDir = "betterdesk-server/console/static/files/lang/"

func TestCheckPairingI18nPasses(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		langDir + "en.json":   `{"nav":{"pairing":"Pairing"},"pairing":{"title":"T","b":"B"}}`,
		langDir + "de.json":   `{"nav":{"pairing":"Koppeln"},"pairing":{"title":"Titel","b":"Be"}}`,
		langDir + "notes.txt": "not json",
	})
	runCommand(checkPairingI18n, root).want(t, 0, "Pairing i18n OK (2 keys x 2 locales)\n", "")
}

func TestCheckPairingI18nFindings(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		// Keys that are array indexes come first, ascending
		langDir + "en.json": `{"nav":{"pairing":"Pairing"},"pairing":{"b":"B","10":"x","2":"y","a":"A"}}`,
		// Empty, blank and 0 are untranslated; an empty nav.pairing is missing
		langDir + "de.json": `{"nav":{"pairing":""},"pairing":{"a":"","2":"   ","b":0}}`,
		langDir + "fr.json": `{"pairing":{"a":"x","2":"y","10":"z","b":"q"}}`,
		// The last of two equal keys counts
		langDir + "it.json": `{"nav":"x","pairing":{"a":"x","2":"y","10":"z","b":"q","b":"  "}}`,
		langDir + "pt.json": `{"nav":{"pairing":1},"pairing":null}`,
		// No-break space and BOM are blank
		langDir + "ru.json": `{"nav":{"pairing":true},"pairing":{"a":"x","2":"y","10":"z","b":"` + string(rune(0xA0)) + string(rune(0xFEFF)) + `"}}`,
	})
	runCommand(checkPairingI18n, root).want(t, 1, "", ""+
		"warn: de: untranslated pairing.2, 10, b, a\n"+
		"warn: it: untranslated pairing.b\n"+
		"warn: pt: untranslated pairing.2, 10, b, a\n"+
		"warn: ru: untranslated pairing.b\n"+
		"Pairing i18n parity failed:\n"+
		"  - de: missing pairing.10\n"+
		"  - de: missing nav.pairing (sidebar entry)\n"+
		"  - fr: missing nav.pairing (sidebar entry)\n"+
		"  - it: missing nav.pairing (sidebar entry)\n"+
		"  - pt: missing pairing.2, 10, b, a\n")
}

func TestCheckPairingI18nInheritedKeys(t *testing.T) {
	// "in" finds what every object inherits: a key named like one is
	// never missing
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		langDir + "en.json": `{"nav":{"pairing":"P"},"pairing":{"toString":"x","a":"A"}}`,
		langDir + "de.json": `{"nav":{"pairing":"P"},"pairing":{"a":"B"}}`,
	})
	r := runCommand(checkPairingI18n, root)
	if r.code != 0 || r.stdout != "Pairing i18n OK (2 keys x 2 locales)\n" {
		t.Errorf("got %+v", r)
	}
}

func TestCheckPairingI18nEnglishMayBeEmpty(t *testing.T) {
	// An empty English value is no warning: English shows the key itself
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		langDir + "en.json": `{"nav":{"pairing":"Pairing"},"pairing":{"a":""}}`,
		langDir + "de.json": `{"nav":{"pairing":"Koppeln"},"pairing":{"a":""}}`,
	})
	runCommand(checkPairingI18n, root).want(t, 0, "Pairing i18n OK (1 keys x 2 locales)\n", "warn: de: untranslated pairing.a\n")
}

func TestCheckPairingI18nArrayNamespace(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		langDir + "en.json": `{"nav":{"pairing":"Pairing"},"pairing":["a","b"]}`,
		langDir + "de.json": `{"nav":{"pairing":"P"},"pairing":["x"]}`,
	})
	runCommand(checkPairingI18n, root).want(t, 1, "",
		"warn: de: untranslated pairing.1\nPairing i18n parity failed:\n  - de: missing pairing.1\n")
}

func TestCheckPairingI18nCannotRun(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{langDir + "readme.md": ""})
	runCommand(checkPairingI18n, root).want(t, 2, "", "error: no locale files found\n")

	writeTree(t, root, map[string]string{langDir + "en.json": `{"nav":{"pairing":"Pairing"}}`})
	runCommand(checkPairingI18n, root).want(t, 2, "", "error: en.json has no \"pairing\" namespace\n")

	writeTree(t, root, map[string]string{langDir + "en.json": `{"nav":{"pairing":"Pairing"},"pairing":{}}`})
	runCommand(checkPairingI18n, root).want(t, 2, "", "error: en.json has no \"pairing\" namespace\n")
}

func TestCheckPairingI18nInvalidJSON(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		langDir + "en.json": `{"nav":{"pairing":"Pairing"},"pairing":{"a":"A"}}`,
		langDir + "de.json": `{"nav":`,
	})
	r := runCommand(checkPairingI18n, root)
	if r.code != 1 || !strings.HasPrefix(r.stderr, "Pairing i18n parity failed:\n  - de.json: invalid JSON (") {
		t.Errorf("got %+v", r)
	}
}

func TestCheckPairingI18nOnTheRepository(t *testing.T) {
	r := runCommand(checkPairingI18n, repoRoot(t))
	if r.code != 0 || !strings.HasPrefix(r.stdout, "Pairing i18n OK (") {
		t.Errorf("got %+v", r)
	}
}
