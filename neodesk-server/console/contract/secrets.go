package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// A secret is a value a case sends that must never be written down: a
// device token, a client API token, a two-factor secret. A case names it
// as {secret:<name>} in its path, a query value, a header or its body. The
// value comes from the file of that name in the secrets folder
// (Settings.SecretsDir, /stack/secrets inside "stack.sh run"), or from an
// answer earlier in the same turn (Case.CaptureSecret).
//
// Wherever a known value would show, the suite writes its placeholder
// instead: the golden's request keeps the placeholder as the case wrote
// it, and every answer is masked before it is compared or recorded, so a
// route that echoes a token still compares equal at that place. Failure
// messages and the summary are masked too. A recording that would write a
// known value anyway (one the masking did not reach, as inside a longer
// word) refuses to write that golden.

// minSecretLength is the shortest value the suite takes as a secret. A
// shorter one could stand in an answer by chance, and masking it there
// would change answers that have nothing to do with it.
const minSecretLength = 8

// secretPlaceholder is how a secret stands in a case and in everything the
// suite writes.
func secretPlaceholder(name string) string { return "{secret:" + name + "}" }

// secretSet holds the secret values one run knows: the files of the
// secrets folder, and every value a case captured as a secret. It is safe
// for concurrent use, and a nil set knows nothing.
type secretSet struct {
	mu sync.Mutex
	// dir is the secrets folder.
	dir string
	// files are the values of the secrets folder by file name. short names
	// the files whose value is too short to be a secret.
	files map[string]string
	short map[string]bool
	// names maps every known value to the name it is masked with: the name
	// it was first known by.
	names map[string]string
	// masker replaces the known values, longest first; nil when it has to
	// be built again.
	masker *strings.Replacer
}

// loadSecrets reads the secrets folder: every file in it is a secret named
// after the file, its content without white space around it. A folder
// that does not exist holds no secrets.
func loadSecrets(dir string) (*secretSet, error) {
	set := &secretSet{dir: dir, files: map[string]string{}, short: map[string]bool{}, names: map[string]string{}}
	if dir == "" {
		return set, nil
	}
	entries, err := os.ReadDir(dir)
	if isNotExist(err) {
		return set, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the secrets folder %s: %w", dir, err)
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read the secret %s: %w", entry.Name(), err)
		}
		value := strings.TrimSpace(string(content))
		if len(value) < minSecretLength {
			set.short[entry.Name()] = true
			continue
		}
		set.files[entry.Name()] = value
		set.addLocked(entry.Name(), value)
	}
	return set, nil
}

// file is the value of the secret file name.
func (s *secretSet) file(name string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("secret %s: the run has no secrets", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.files[name]; ok {
		return value, nil
	}
	where := filepath.ToSlash(filepath.Join(s.dir, name))
	if s.short[name] {
		return "", fmt.Errorf("secret %s: the file %s holds fewer than %d characters, too few to mask in answers", name, where, minSecretLength)
	}
	return "", fmt.Errorf("secret %s: no case of this turn captured it and there is no file %s", name, where)
}

// add makes a value known under a name. A value shorter than
// minSecretLength is refused.
func (s *secretSet) add(name, value string) error {
	if len(value) < minSecretLength {
		return fmt.Errorf("secret %s: the value has %d characters, a secret needs at least %d", name, len(value), minSecretLength)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addLocked(name, value)
	return nil
}

func (s *secretSet) addLocked(name, value string) {
	if _, known := s.names[value]; known {
		return
	}
	s.names[value] = name
	s.masker = nil
}

// mask replaces every known value in text by its placeholder.
func (s *secretSet) mask(text string) string {
	if s == nil {
		return text
	}
	s.mu.Lock()
	if len(s.names) == 0 {
		s.mu.Unlock()
		return text
	}
	if s.masker == nil {
		values := make([]string, 0, len(s.names))
		for value := range s.names {
			values = append(values, value)
		}
		// At one place the first value in the list that fits wins: the
		// longest, so a value that holds another is masked as a whole.
		sort.Slice(values, func(i, j int) bool {
			if len(values[i]) != len(values[j]) {
				return len(values[i]) > len(values[j])
			}
			return values[i] < values[j]
		})
		pairs := make([]string, 0, 2*len(values))
		for _, value := range values {
			pairs = append(pairs, value, secretPlaceholder(s.names[value]))
		}
		s.masker = strings.NewReplacer(pairs...)
	}
	masker := s.masker
	s.mu.Unlock()
	return masker.Replace(text)
}

// foundIn lists the names of the known secrets whose value is in data,
// as it is or as a JSON string writes it.
func (s *secretSet) foundIn(data []byte) []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	found := map[string]bool{}
	for value, name := range s.names {
		escaped, _ := json.Marshal(value)
		if bytes.Contains(data, []byte(value)) || bytes.Contains(data, bytes.Trim(escaped, `"`)) {
			found[name] = true
		}
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
