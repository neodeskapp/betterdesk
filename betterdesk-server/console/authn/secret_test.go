package authn

import (
	"bytes"
	"testing"
)

// TestGenerateSecretKeepsEveryBit: a new two-factor secret is the base32
// of its 20 random bytes as they are, 160 bits (BUGS.md AN1).
func TestGenerateSecretKeepsEveryBit(t *testing.T) {
	secretOf := func(b byte) string {
		s := pureService()
		s.rand = bytes.NewReader(bytes.Repeat([]byte{b}, totpKeyLength))
		secret, err := s.generateSecret()
		if err != nil {
			t.Fatal(err)
		}
		return secret
	}
	high, low := secretOf(0xff), secretOf(0x7f)
	if high == low {
		t.Errorf("the bytes 0xff and 0x7f give the same secret %s", high)
	}
	if want := "77777777777777777777777777777777"; high != want {
		t.Errorf("20 bytes of 0xff: %s, want %s", high, want)
	}
}
