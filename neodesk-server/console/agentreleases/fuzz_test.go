package agentreleases

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

// referenceBase64 is Buffer.from(text, 'base64') put another way: the
// text up to the first "=", without what is no base64 character, the
// URL-safe letters read as the standard ones, a single character left
// over at the end dropped, decoded without padding.
func referenceBase64(text string) []byte {
	text, _, _ = strings.Cut(text, "=")
	var kept strings.Builder
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '+', c == '/':
			kept.WriteByte(c)
		case c == '-':
			kept.WriteByte('+')
		case c == '_':
			kept.WriteByte('/')
		}
	}
	s := kept.String()
	if len(s)%4 == 1 {
		s = s[:len(s)-1]
	}
	// The decoder without Strict ignores the unused bits of the last character
	out, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return out
}

// FuzzDecodeBase64 checks the decoder of the signature and the key, text
// that comes from GitHub and the environment, against the reference.
func FuzzDecodeBase64(f *testing.F) {
	for _, seed := range []string{"", "YQ==", "YQ", "YW Jj", "YQ==YQ==", "-_-_", "Y!Q@=", "Y", "eXGPOvUvFfz1FGGbYogKfYEEWVWzeJEKpmI1ngAU490=", "ä ö", "YWJj=ZGVm"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if got, want := decodeBase64(text), referenceBase64(text); !bytes.Equal(got, want) {
			t.Errorf("decodeBase64(%q) = %x, want %x", text, got, want)
		}
	})
}

// FuzzChecksumLines checks that any SHA256SUMS text is read without a
// panic and that every name it yields is a plain file name with a digest.
func FuzzChecksumLines(f *testing.F) {
	f.Add(strings.Repeat("a", 64) + "  agent.zip\r\n" + strings.Repeat("b", 64) + " *agent.tar.gz\n")
	f.Add("garbage\n\n")
	f.Fuzz(func(t *testing.T, text string) {
		for _, line := range reLineBreak.Split(decodeUTF8([]byte(text)), -1) {
			m := reChecksumLine.FindStringSubmatch(jsTrim(line))
			if m == nil {
				continue
			}
			if len(m[1]) != 64 || safeName(m[2]) == "" {
				t.Errorf("line %q gave %q, %q", line, m[1], m[2])
			}
		}
	})
}
