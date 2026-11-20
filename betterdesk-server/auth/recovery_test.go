package auth

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// The recovery codes have the web console's shape (BUGS S12): eight codes
// of nine characters, which the console's sign-in field takes, stored as
// bcrypt hashes.
func TestRecoveryCodesHaveTheConsolesShape(t *testing.T) {
	codes, err := GenerateRecoveryCodes()
	if err != nil || len(codes) != 8 {
		t.Fatalf("%v, %v; want 8 codes", codes, err)
	}
	form := regexp.MustCompile(`^[0-9A-F]{4}-[0-9A-F]{4}$`)
	for _, code := range codes {
		if !form.MatchString(code) {
			t.Errorf("code %q has not the form XXXX-XXXX", code)
		}
	}
	fixed, err := GenerateRecoveryCodesFrom(strings.NewReader(strings.Repeat("\x3f\xa9\x0c\x7e", 8)))
	if err != nil || fixed[0] != "3FA9-0C7E" || fixed[7] != "3FA9-0C7E" {
		t.Errorf("codes of fixed bytes: %v, %v", fixed, err)
	}
	stored, err := HashRecoveryCodesCost(codes, bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	var hashes []string
	if err := json.Unmarshal([]byte(stored), &hashes); err != nil || len(hashes) != 8 {
		t.Fatalf("stored %q: %v", stored, err)
	}
	for i, hash := range hashes {
		if !isBcryptHash(hash) || strings.Contains(stored, codes[i]) {
			t.Errorf("entry %d %q is no hash", i, hash)
		}
	}
}

// ConsumeRecoveryCode takes a hashed code and a code stored as it was
// shown, each once, and leaves the other entries as they are.
func TestConsumeRecoveryCode(t *testing.T) {
	hashed, err := HashRecoveryCodesCost([]string{"3FA9-0C7E"}, bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	var hash []string
	_ = json.Unmarshal([]byte(hashed), &hash)
	stored := `["AB12-CD34","` + hash[0] + `","<&>"]`

	left, matched, err := ConsumeRecoveryCode(stored, " 3fa90c7e ")
	if err != nil || !matched || left != `["AB12-CD34","<&>"]` {
		t.Fatalf("hashed code: %q, %v, %v", left, matched, err)
	}
	left, matched, err = ConsumeRecoveryCode(left, "ab12-CD34")
	if err != nil || !matched || left != `["<&>"]` {
		t.Fatalf("plain code: %q, %v, %v", left, matched, err)
	}
	if again, matched, err := ConsumeRecoveryCode(left, "AB12-CD34"); err != nil || matched || again != left {
		t.Errorf("a used code: %q, %v, %v", again, matched, err)
	}
	if _, matched, _ := ConsumeRecoveryCode(left, " - "); matched {
		t.Error("an empty code matches")
	}
	for _, bad := range []string{`not json`, `{}`, `[1]`, `["A",null]`, `"A"`} {
		if _, matched, err := ConsumeRecoveryCode(bad, "A"); matched || !errors.Is(err, ErrMalformedRecoveryCodes) {
			t.Errorf("%s: %v, %v; want ErrMalformedRecoveryCodes", bad, matched, err)
		}
	}
	if left, matched, err := ConsumeRecoveryCode(`null`, "A"); matched || err != nil || left != `null` {
		t.Errorf("null: %q, %v, %v", left, matched, err)
	}
}

// A password hash made with HashPasswordFrom at PasswordIterations is the
// form HashPassword makes, which no sign-in replaces.
func TestHashPasswordFrom(t *testing.T) {
	hash, err := HashPasswordFrom(strings.NewReader(strings.Repeat("s", 16)), PasswordIterations, "pw")
	if err != nil || !strings.HasPrefix(hash, "pbkdf2-sha256$600000$73737373737373737373737373737373$") {
		t.Fatalf("%q, %v", hash, err)
	}
	if !VerifyPassword(hash, "pw") || VerifyPassword(hash, "pw2") || NeedsRehash(hash) {
		t.Errorf("hash %q: verify or rehash wrong", hash)
	}
	cheap, _ := HashPasswordFrom(strings.NewReader(strings.Repeat("s", 16)), 1000, "pw")
	if !VerifyPassword(cheap, "pw") || !NeedsRehash(cheap) {
		t.Errorf("a hash of fewer iterations: %q", cheap)
	}
	if _, err := HashPasswordFrom(strings.NewReader("short"), PasswordIterations, "pw"); err == nil {
		t.Error("a salt source without 16 bytes gives a hash")
	}
}
