package authn

import (
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/blowfish"
)

// bcrypt as the stored hashes need it. They come from the C implementation
// of OpenBSD in the form the bcrypt library for Node has it:
//
//   - "$2b$": the password's first 72 bytes and a closing zero byte.
//   - "$2a$": the password and a closing zero byte.
//   - "$2$": the password without the closing zero byte.
//
// Blowfish reads the key round and round, 72 bytes each time, so of a
// longer password only the first 72 bytes count, for every version and
// every length.
//
// A stored hash verifies only when it is, letter for letter, what the C
// code prints for the password: two cost digits, a salt whose last letter
// carries no stray bits, 31 letters of hash. Text after a zero byte is not
// looked at, because the C code stops reading there.

const (
	bcryptAlphabet = "./ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	bcryptSaltLen  = 16
	bcryptMinCost  = 4
	// bcryptMaxCost is the highest cost of a stored hash that verifies.
	// The C code's check of the cost overflows for 31, so it refuses 31.
	bcryptMaxCost = 30
	// bcryptKeyBytes is how much of the key one pass over Blowfish's 18
	// subkeys reads.
	bcryptKeyBytes = 72
)

var bcryptMagic = []byte("OrpheanBeholderScryDoubt")

// bcryptEncode writes src in bcrypt's own base64, without padding.
func bcryptEncode(src []byte) string {
	var b strings.Builder
	for i := 0; i < len(src); {
		c1 := src[i]
		i++
		b.WriteByte(bcryptAlphabet[c1>>2])
		c1 = (c1 & 0x03) << 4
		if i >= len(src) {
			b.WriteByte(bcryptAlphabet[c1])
			break
		}
		c2 := src[i]
		i++
		c1 |= c2 >> 4
		b.WriteByte(bcryptAlphabet[c1])
		c1 = (c2 & 0x0f) << 2
		if i >= len(src) {
			b.WriteByte(bcryptAlphabet[c1])
			break
		}
		c2 = src[i]
		i++
		c1 |= c2 >> 6
		b.WriteByte(bcryptAlphabet[c1])
		b.WriteByte(bcryptAlphabet[c2&0x3f])
	}
	return b.String()
}

// bcryptDecodeSalt reads the 22 letters of a salt. ok is false when a
// letter is not in the alphabet or the last letter has bits a salt of 16
// bytes cannot have.
func bcryptDecodeSalt(text string) (salt []byte, ok bool) {
	if len(text) != 22 {
		return nil, false
	}
	var bits uint32
	var have uint
	salt = make([]byte, 0, bcryptSaltLen)
	for i := 0; i < len(text); i++ {
		v := strings.IndexByte(bcryptAlphabet, text[i])
		if v < 0 {
			return nil, false
		}
		bits = bits<<6 | uint32(v)
		have += 6
		if have >= 8 {
			have -= 8
			salt = append(salt, byte(bits>>have))
		}
	}
	if len(salt) != bcryptSaltLen || bcryptEncode(salt) != text {
		return nil, false
	}
	return salt, true
}

// bcryptKey returns the bytes of password that reach Blowfish for a hash
// of the given minor version (0 for "$2$"): the password with its closing
// zero byte, without it for "$2$". Of a long password only the first 72
// bytes count, whatever the version (bcryptRaw cuts the key there).
func bcryptKey(password string, minor byte) []byte {
	data := append([]byte(password), 0)
	var n int
	switch minor {
	case 'b':
		n = min(len(password), bcryptKeyBytes) + 1
	case 'a':
		n = len(password) + 1
	default:
		n = len(password)
	}
	if n == 0 {
		// A length of zero makes the C code read the first byte over and
		// over.
		n = 1
	}
	return data[:n]
}

// bcryptRaw computes the 23 bytes of a bcrypt hash. It gives up when ctx
// ends, which matters for stored hashes with a very high cost.
func bcryptRaw(ctx context.Context, key []byte, cost uint, salt []byte) ([]byte, error) {
	if len(key) > bcryptKeyBytes {
		key = key[:bcryptKeyBytes]
	}
	c, err := blowfish.NewSaltedCipher(key, salt)
	if err != nil {
		return nil, fmt.Errorf("bcrypt: %w", err)
	}
	rounds := uint64(1) << cost
	for i := uint64(0); i < rounds; i++ {
		if i&0x3ff == 0x3ff {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("bcrypt: %w", err)
			}
		}
		blowfish.ExpandKey(key, c)
		blowfish.ExpandKey(salt, c)
	}
	data := make([]byte, len(bcryptMagic))
	copy(data, bcryptMagic)
	for i := 0; i < len(data); i += 8 {
		for j := 0; j < 64; j++ {
			c.Encrypt(data[i:i+8], data[i:i+8])
		}
	}
	return data[:len(data)-1], nil
}

// bcryptFormat prints a hash the way the C code does.
func bcryptFormat(minor byte, cost uint, salt, hash []byte) string {
	var b strings.Builder
	b.WriteString("$2")
	if minor != 0 {
		b.WriteByte(minor)
	}
	fmt.Fprintf(&b, "$%02d$", cost)
	b.WriteString(bcryptEncode(salt))
	b.WriteString(bcryptEncode(hash))
	return b.String()
}

// bcryptParts is a stored hash taken apart.
type bcryptParts struct {
	text  string // the stored text up to its first zero byte
	minor byte   // 'a', 'b' or 0
	cost  uint
	salt  []byte
}

// parseBcrypt takes a stored hash apart. ok is false for anything the C
// code cannot have printed.
func parseBcrypt(stored string) (p bcryptParts, ok bool) {
	if i := strings.IndexByte(stored, 0); i >= 0 {
		stored = stored[:i]
	}
	p.text = stored
	rest, found := strings.CutPrefix(stored, "$2")
	if !found || rest == "" {
		return p, false
	}
	if rest[0] == 'a' || rest[0] == 'b' {
		p.minor = rest[0]
		rest = rest[1:]
	}
	// "$", two cost digits, "$", 22 letters of salt, 31 letters of hash
	if len(rest) != 1+2+1+22+31 || rest[0] != '$' || rest[3] != '$' {
		return p, false
	}
	d1, d2 := rest[1], rest[2]
	if d1 < '0' || d1 > '9' || d2 < '0' || d2 > '9' {
		return p, false
	}
	p.cost = uint(d1-'0')*10 + uint(d2-'0')
	if p.cost < bcryptMinCost || p.cost > bcryptMaxCost {
		return p, false
	}
	salt, saltOK := bcryptDecodeSalt(rest[4 : 4+22])
	if !saltOK {
		return p, false
	}
	p.salt = salt
	return p, true
}

// bcryptCompare reports whether stored is the bcrypt hash of password.
func bcryptCompare(ctx context.Context, password, stored string) (bool, error) {
	p, ok := parseBcrypt(stored)
	if !ok {
		return false, nil
	}
	hash, err := bcryptRaw(ctx, bcryptKey(password, p.minor), p.cost, p.salt)
	if err != nil {
		return false, err
	}
	want := bcryptFormat(p.minor, p.cost, p.salt, hash)
	return subtle.ConstantTimeCompare([]byte(want), []byte(p.text)) == 1, nil
}

// bcryptHash makes a "$2b$" hash of password with a salt from random.
func bcryptHash(ctx context.Context, random io.Reader, password string, cost uint) (string, error) {
	if cost < bcryptMinCost {
		cost = bcryptMinCost
	}
	if cost > bcryptMaxCost {
		cost = bcryptMaxCost
	}
	salt := make([]byte, bcryptSaltLen)
	if _, err := io.ReadFull(random, salt); err != nil {
		return "", fmt.Errorf("bcrypt salt: %w", err)
	}
	hash, err := bcryptRaw(ctx, bcryptKey(password, 'b'), cost, salt)
	if err != nil {
		return "", err
	}
	return bcryptFormat('b', cost, salt, hash), nil
}
