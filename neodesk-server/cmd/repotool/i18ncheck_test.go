package main

import (
	"encoding/json"
	"strings"
	"testing"
)

const consoleLang = "betterdesk-server/console/static/files/lang/"

// jsonEscape is a JSON \u escape of the code point in hex, written
// without the escape in this file.
func jsonEscape(hex string) string { return "\\" + "u" + hex }

func i18nTree() map[string]string {
	common := `"ok": "OK", "code": "ABC_1", "url": "https://x.example", "ph": "{{count}}", "file": "config.json", ` +
		`"num": 5, "flag": true, "nil": null, "list": ["a", "b"], "tech": "Toolkit", "dotted": "Ok."`
	return map[string]string{
		consoleLang + "en.json": `{
  "nav": {"home": "Home", "devices": "Devices", "pairing": "Pairing"},
  "common": {` + common + `, "save": "Save", "long": "A long sentence here", "stop": "Stopped.", "id": "Identifier", "short": "Hi!", "nbsp": "ab` + jsonEscape("00a0") + `cdefghijkl"},
  "b": "x", "10": "y", "2": "z",
  "_meta": {"lang": "en"}
}`,
		consoleLang + "pl.json": `{
  "nav": {"home": "Start", "devices": "Urzadzenia", "pairing": "Parowanie"},
  "common": {` + common + `, "save": "Zapisz", "long": "Dlugie zdanie", "stop": "Zatrzymano.", "id": "Identyfikator", "short": "Hej!", "nbsp": "x"},
  "b": "x", "10": "y", "2": "z", "plonly": "tylko"
}`,
		// A byte order mark before the JSON is fine
		consoleLang + "de.json": string(rune(0xFEFF)) + `{
  "nav": {"home": "Home", "devices": "Devices"},
  "common": {` + common + `, "save": "Save", "long": "A long sentence here", "stop": "Stopped.", "id": "Identifier", "short": "Hi!", "nbsp": "ab` + jsonEscape("00a0") + `cdefghijkl"},
  "b": " ", "10": "y", "2": "z", "plonly": "", "extra": {"k": "v"}, "_meta": {"x": 1}
}`,
		consoleLang + "fr.json": `{"nav":{}}`,
	}
}

const i18nTableHead = "" +
	"┌─────────┬───────────┬──────┬─────────┬───────┬──────────────┬───────┬─────────────────┬────────┐\n" +
	"│ (index) │ file      │ keys │ missing │ extra │ ignoredExtra │ empty │ englishFallback │ status │\n" +
	"├─────────┼───────────┼──────┼─────────┼───────┼──────────────┼───────┼─────────────────┼────────┤\n"

const i18nConsoleReport = `
BetterDesk i18n audit
Reference baseline: union of en.json and pl.json keys
English fallback check: exact EN values filtered through a technical-term allowlist

=== Web Console (betterdesk-server/console/static/files/lang) ===
files=4 baseline=25 en=24 pl=24 en_missing_vs_pl=1 pl_missing_vs_en=1
  EN missing keys present in PL: plonly
  PL missing keys present in EN: _meta.lang
` + i18nTableHead +
	"│ 0       │ 'de.json' │ 25   │ 2       │ 1     │ 1            │ 3     │ 5               │ 'FAIL' │\n" +
	"│ 1       │ 'en.json' │ 24   │ 1       │ 0     │ 0            │ 1     │ 0               │ 'FAIL' │\n" +
	"│ 2       │ 'fr.json' │ 0    │ 25      │ 0     │ 0            │ 0     │ 0               │ 'FAIL' │\n" +
	"│ 3       │ 'pl.json' │ 24   │ 1       │ 0     │ 0            │ 1     │ 0               │ 'FAIL' │\n" +
	"└─────────┴───────────┴──────┴─────────┴───────┴──────────────┴───────┴─────────────────┴────────┘\n" +
	`-- de.json
  missing(2): nav.pairing, _meta.lang
  extra(1): extra.k
  empty(3): common.nil, b, plonly
  english-fallback(5): nav.devices, common.save, common.long, common.stop, common.nbsp
-- en.json
  missing(1): plonly
  empty(1): common.nil
-- fr.json
  missing(25): 2, 10, nav.home, nav.devices, nav.pairing, common.ok, common.code, common.url, common.ph, common.file, common.num, common.flag, common.nil, common.list, common.tech, common.dotted, common.save, common.long, common.stop, common.id, common.short, common.nbsp, b, _meta.lang, plonly
-- pl.json
  missing(1): _meta.lang
  empty(1): common.nil

`

const i18nFailed = "i18n audit failed: every active locale must match the EN+PL key baseline, parse cleanly, and contain no empty values.\n"

func TestI18nCheckReport(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, i18nTree())
	runCommand(i18nCheck, root, "--system", "console").want(t, 1, i18nConsoleReport+i18nFailed, "")

	// Without --system every system is audited; the missing ones are skipped
	runCommand(i18nCheck, root).want(t, 1, i18nConsoleReport+`=== Agent Client (agent-client) ===
Skipped: Directory not found: betterdesk-agent-client/src/locales

=== MGMT Client (mgmt) ===
Skipped: Directory not found: betterdesk-mgmt/src/locales

`+i18nFailed, "")
	// --system without a name is all systems too
	if r := runCommand(i18nCheck, root, "--system"); r.code != 1 || !strings.Contains(r.stdout, "MGMT Client") {
		t.Errorf("--system alone: %+v", r)
	}
}

func TestI18nCheckVerbose(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, i18nTree())
	r := runCommand(i18nCheck, root, "--verbose", "--system", "console")
	if !strings.Contains(r.stdout, "  ignored-extra(1): _meta.x\n") {
		t.Errorf("verbose report lacks the ignored keys:\n%s", r.stdout)
	}
	if strings.Contains(runCommand(i18nCheck, root, "--system", "console").stdout, "ignored-extra") {
		t.Error("ignored-extra without --verbose")
	}

	// A file without findings is listed only with --verbose
	clean := `{"a": "Ja"}`
	writeTree(t, root, map[string]string{consoleLang + "en.json": clean, consoleLang + "pl.json": clean, consoleLang + "de.json": clean, consoleLang + "fr.json": clean})
	if r := runCommand(i18nCheck, root, "--system", "console"); strings.Contains(r.stdout, "-- de.json") {
		t.Errorf("a clean file is listed:\n%s", r.stdout)
	}
	if r := runCommand(i18nCheck, root, "--system", "console", "--verbose"); !strings.Contains(r.stdout, "-- de.json\n-- en.json\n-- fr.json\n-- pl.json\n") {
		t.Errorf("--verbose does not list the clean files:\n%s", r.stdout)
	}
}

func TestI18nCheckJSON(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, i18nTree())
	r := runCommand(i18nCheck, root, "--json")
	if r.code != 1 || r.stderr != "" {
		t.Fatalf("got code %d, stderr %q", r.code, r.stderr)
	}
	var results []map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || results[1]["skipped"] != true || results[1]["reason"] != "Directory not found: betterdesk-agent-client/src/locales" {
		t.Fatalf("results: %v", results)
	}
	de := results[0]["rows"].([]any)[0].(map[string]any)
	if got, _ := json.Marshal(de); string(got) != `{"empty":["common.nil","b","plonly"],"englishFallback":["nav.devices","common.save","common.long","common.stop","common.nbsp"],"extra":["extra.k"],"file":"de.json","ignoredExtra":["_meta.x"],"keys":25,"missing":["nav.pairing","_meta.lang"]}` {
		t.Errorf("de.json: %s", got)
	}
	// The fields come in the report's order
	if !strings.Contains(r.stdout, `{
    "id": "console",
    "label": "Web Console",
    "dir": "betterdesk-server/console/static/files/lang",
    "files": 4,
    "baselineCount": 25,
    "enCount": 24,
    "plCount": 24,
    "enMissingVsPL": [
      "plonly"
    ],`) {
		t.Errorf("JSON order:\n%s", r.stdout)
	}
}

func TestI18nCheckClean(t *testing.T) {
	root := t.TempDir()
	// Numbers are leaves as JavaScript writes them
	clean := `{"a": {"x": "Ja", "y": "Nein"}, "n": 1e+21, "m": 1e-06, "big": 1000000, "neg": -1.5e-07}`
	writeTree(t, root, map[string]string{consoleLang + "en.json": clean, consoleLang + "pl.json": clean, consoleLang + "de.json": clean})
	ok := "│ %d       │ '%s.json' │ 6    │ 0       │ 0     │ 0            │ 0     │ 0               │ 'OK'   │\n"
	runCommand(i18nCheck, root, "--system", "console").want(t, 0, `
BetterDesk i18n audit
Reference baseline: union of en.json and pl.json keys
English fallback check: exact EN values filtered through a technical-term allowlist

=== Web Console (betterdesk-server/console/static/files/lang) ===
files=3 baseline=6 en=6 pl=6 en_missing_vs_pl=0 pl_missing_vs_en=0
`+i18nTableHead+
		strings.NewReplacer("%d", "0", "%s", "de").Replace(ok)+
		strings.NewReplacer("%d", "1", "%s", "en").Replace(ok)+
		strings.NewReplacer("%d", "2", "%s", "pl").Replace(ok)+
		"└─────────┴───────────┴──────┴─────────┴───────┴──────────────┴───────┴─────────────────┴────────┘\n"+
		"\ni18n audit passed: no parity, empty, or fallback findings.\n", "")

	r := runCommand(i18nCheck, root, "--system", "console", "--json")
	want := `[
  {
    "id": "console",
    "label": "Web Console",
    "dir": "betterdesk-server/console/static/files/lang",
    "files": 3,
    "baselineCount": 6,
    "enCount": 6,
    "plCount": 6,
    "enMissingVsPL": [],
    "plMissingVsEN": [],
    "rows": [
` + strings.Join([]string{jsonRow("de"), jsonRow("en"), jsonRow("pl")}, ",\n") + `
    ]
  }
]
`
	r.want(t, 0, want, "")
}

func jsonRow(name string) string {
	return `      {
        "file": "` + name + `.json",
        "keys": 6,
        "missing": [],
        "extra": [],
        "ignoredExtra": [],
        "empty": [],
        "englishFallback": []
      }`
}

func TestI18nCheckParseError(t *testing.T) {
	root := t.TempDir()
	tree := i18nTree()
	tree[consoleLang+"de.json"] = `{"nav": `
	delete(tree, consoleLang+"fr.json")
	writeTree(t, root, tree)
	r := runCommand(i18nCheck, root, "--system", "console")
	for _, want := range []string{
		"│ 0       │ 'de.json' │ 0    │ 0       │ 0     │ 0            │ 0     │ 0               │ 'PARSE_ERROR' │\n",
		"-- de.json\n  parse-error: ",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("report lacks %q:\n%s", want, r.stdout)
		}
	}
	if r.code != 1 {
		t.Errorf("exit %d", r.code)
	}
	r = runCommand(i18nCheck, root, "--system", "console", "--json")
	if !strings.Contains(r.stdout, `"file": "de.json",
        "parseError": "`) || !strings.Contains(r.stdout, `"keys": 0,`) {
		t.Errorf("JSON:\n%s", r.stdout)
	}
}

func TestI18nCheckArguments(t *testing.T) {
	root := t.TempDir()
	runCommand(i18nCheck, root, "--fix", "--help").want(t, 2, "", "Automatic i18n --fix is disabled. Missing keys must be translated in the target language, not filled with English fallback text.\n")
	runCommand(i18nCheck, root, "-h").want(t, 0, i18nHelp, "")
	runCommand(i18nCheck, root, "--system", "nope").want(t, 2, "", "Unknown i18n system: nope\nKnown systems: console, agent-client, mgmt\n")

	tree := i18nTree()
	delete(tree, consoleLang+"pl.json")
	writeTree(t, root, tree)
	runCommand(i18nCheck, root, "--system", "console").want(t, 1, "", "i18n audit failed: console: missing reference file pl.json\n")
}

func TestLooksLikeEnglishFallback(t *testing.T) {
	for _, c := range []struct {
		key, value string
		want       bool
	}{
		{"x.save", "Save", true},               // a hint word
		{"x.a", "Bonjour tout le monde", true}, // a space and 12 or more characters
		{"x.a", "Bonjour mond", true},          // exactly 12
		{"x.a", "Hallo Welt", false},           // a space, but under 12 characters
		{"x.a", "Fertig!", false},              // a stop, but under 8 characters
		{"x.a", "Erledigt!", true},             // a stop and 8 or more
		{"x.a", "Terminal", false},             // a shared term, though a hint word
		{"x.a", "OpenID Connect", false},       // a shared name, though long with a space
		{"x.a", "ABC_1", false},                // short capitals
		{"x.a", "https://x", false},            // an address
		{"x.a", "{{count}}", false},            // a placeholder
		{"x.a", "config.json", false},          // a file name
		{"x.id", "Bonjour tout le monde", false},
		{"x.ldap_host", "Bonjour tout le monde", false},
		{"x.a", "12345 678901", false},                                             // no letters
		{"x.a", "Abcdefghijk" + string(rune(0xA0)) + "x", true},                    // a no-break space is a space
		{"x.a", "Abcdefgh" + string(rune(0x1F600)) + string(rune(0x1F600)), false}, // long, but no space and no stop
		{"x.a", "Abcde" + string(rune(0x1F600)) + "!", true},                       // a stop and 8 UTF-16 code units (7 characters)
	} {
		if got := looksLikeEnglishFallback(c.key, c.value); got != c.want {
			t.Errorf("looksLikeEnglishFallback(%q, %q) = %v, want %v", c.key, c.value, got, c.want)
		}
	}
}
