package static

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// languageTexts reads every file of files/lang as its texts by dotted key.
func languageTexts(t *testing.T) map[string]map[string]string {
	t.Helper()
	names, err := filepath.Glob("files/lang/*.json")
	if err != nil || len(names) < 2 {
		t.Fatalf("language files: %v %v", names, err)
	}
	all := map[string]map[string]string{}
	for _, name := range names {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		var tree map[string]any
		if err := json.Unmarshal(raw, &tree); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		texts := map[string]string{}
		flatten(tree, "", texts)
		all[strings.TrimSuffix(filepath.Base(name), ".json")] = texts
	}
	return all
}

func flatten(node map[string]any, prefix string, out map[string]string) {
	for key, value := range node {
		switch v := value.(type) {
		case map[string]any:
			flatten(v, prefix+key+".", out)
		case string:
			out[prefix+key] = v
		}
	}
}

// findings lists "lang key: text" sorted, for an error message.
func findings(lines []string) string {
	sort.Strings(lines)
	if len(lines) > 20 {
		lines = append(lines[:20], "...")
	}
	return strings.Join(lines, "\n")
}

var (
	// mojibake is UTF-8 read as Windows-1252 or 1250: a dash, an arrow,
	// a multiplication sign or an ellipsis turned into two or three
	// letters, or the replacement character of a byte that was lost.
	mojibake = regexp.MustCompile("â€|â†|Ă—|�")
	// lostLetter is a "?" in place of a letter inside a word.
	lostLetter = regexp.MustCompile(`\p{Latin}\?\p{Latin}|\p{Cyrillic}\?\p{Cyrillic}|\?\?`)
)

// No text of a language file shows broken characters: mojibake, the
// replacement character, or "?" where letters were lost (BUGS L201).
func TestLanguageFilesHaveNoBrokenCharacters(t *testing.T) {
	all := languageTexts(t)
	english := all["en"]
	var broken []string
	for lang, texts := range all {
		for key, text := range texts {
			if mojibake.MatchString(text) || lostLetter.MatchString(text) && !lostLetter.MatchString(english[key]) {
				broken = append(broken, lang+" "+key+": "+text)
			}
		}
	}
	if len(broken) > 0 {
		t.Errorf("%d texts with broken characters:\n%s", len(broken), findings(broken))
	}
}

// foreign holds words of another language that a language's texts do not
// use: German words outside the German file, French ones in Italian,
// Spanish ones in Portuguese and Polish ones in Czech, also with the
// Polish letters dropped. A word after a hyphen is a pronoun of the
// language itself (Portuguese "copiá-los").
var foreign = map[string]*regexp.Regexp{
	"german":  regexp.MustCompile(`(?:^|[^\p{L}-])(?:nicht|wird|Sie|Ihre[nm]?|und|oder|bitte|Datei|Dateien|Gerät|Geräte|Einstellungen|Benutzer)(?:[^\p{L}]|$)`),
	"french":  regexp.MustCompile(`(?:^|[^\p{L}-])(?:les|des|du|vous|votre|une|pour|avec|serveur|être)(?:[^\p{L}]|$)`),
	"spanish": regexp.MustCompile(`(?:^|[^\p{L}-])(?:el|los|del|una|sesión|conexión|archivo|archivos)(?:[^\p{L}]|$)`),
	"polish":  regexp.MustCompile(`[ąęłńśźżŁŚŻ]|(?:^|[^\p{L}-])(?:się|sie|jest|nie|lub|oraz|ktory|który|polaczenia|polaczen|urzadzenia|urzadzen|ustawienia|zostanie|wybierz)(?:[^\p{L}]|$)`),
}

// Each language file holds its own language: the German, French, Spanish
// and Polish texts that sat in other files are translated (BUGS L160).
func TestLanguageFilesHoldNoOtherLanguage(t *testing.T) {
	all := languageTexts(t)
	english := all["en"]
	checks := map[string][]string{"it": {"french"}, "pt": {"spanish"}, "cs": {"polish"}}
	for lang := range all {
		if lang != "en" && lang != "de" {
			checks[lang] = append(checks[lang], "german")
		}
	}
	var found []string
	for lang, kinds := range checks {
		for key, text := range all[lang] {
			if text == english[key] {
				continue
			}
			for _, kind := range kinds {
				if foreign[kind].MatchString(text) {
					found = append(found, lang+" "+key+" ("+kind+"): "+text)
				}
			}
		}
	}
	if len(found) > 0 {
		t.Errorf("%d texts in another language:\n%s", len(found), findings(found))
	}
}

// The notes beside an account's role and its organizations name no
// organization roles, which the console does not have (DECISIONS A154),
// and the note on turning off two-factor sign-in asks for no password: a
// directory or single sign-on account confirms with a code, and the field
// under the note says which (BUGS L222).
func TestTextsNameNoOrganizationRolesAndNoPassword(t *testing.T) {
	english := languageTexts(t)["en"]
	for key, word := range map[string]string{
		"users.role_note":               "organization",
		"users.org_membership_hint":     "role",
		"settings.totp_disable_warning": "password",
	} {
		if text := english[key]; text == "" || strings.Contains(strings.ToLower(text), word) {
			t.Errorf("%s: %q, want a text without %q", key, text, word)
		}
	}
}

// SDK Studio says that Deploy only marks a flow, since nothing on the
// server runs flows, and the security audit's advice for old API keys
// sends the reader to the server's API, since the console has no page for
// API keys (BUGS L227).
func TestTextsPromiseNoFlowRunsAndNoKeyPage(t *testing.T) {
	english := languageTexts(t)["en"]
	if text := english["sdk_studio.page_description"]; strings.Contains(text, "runs on the server") {
		t.Errorf("sdk_studio.page_description: %q says flows run on the server", text)
	}
	if text := english["sdk_studio.howto_save"]; !strings.Contains(text, "does not run flows") {
		t.Errorf("sdk_studio.howto_save: %q, want it to say that the server does not run flows", text)
	}
	if text := english["security_audit.fix_api_key_age"]; !strings.Contains(text, "no page for API keys") || !strings.Contains(text, "server's API") {
		t.Errorf("security_audit.fix_api_key_age: %q, want it to send the reader to the server's API", text)
	}
}
