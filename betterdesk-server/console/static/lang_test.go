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
