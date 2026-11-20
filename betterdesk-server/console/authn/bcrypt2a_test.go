package authn

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"strings"
	"testing"
)

// bcryptHash makes a "$2b$" hash of password with a salt from random, as
// older consoles stored them. The console itself writes none.
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

// TestBcrypt2aLongPasswordUsesTheFirst72Bytes: for a "$2a$" or "$2$" hash
// a password of any length counts with its first 72 bytes, as for "$2b$";
// 255 bytes and more are not cut to their length modulo 256 (BUGS.md
// AN5).
func TestBcrypt2aLongPasswordUsesTheFirst72Bytes(t *testing.T) {
	s := pureService()
	s.rand = rand.Reader
	ctx := context.Background()
	// A password whose bytes differ, so a shorter one read round and round
	// is no other key
	first72 := strings.Repeat("abcdefgh", 9)
	// For 72 bytes the three versions hash the same key: the closing zero
	// byte is the 73rd and falls off
	hash, err := bcryptHash(ctx, s.rand, first72, bcryptMinCost)
	if err != nil {
		t.Fatal(err)
	}
	for _, stored := range []string{"$2a$" + hash[4:], "$2$" + hash[4:], hash} {
		for _, n := range []int{72, 100, 254, 255, 256, 260, 326, 384} {
			if ok, err := s.VerifyPassword(ctx, first72+strings.Repeat("y", n-72), stored); err != nil || !ok {
				t.Errorf("%.4s hash of the first 72 bytes, password of %d bytes: %v, %v", stored, n, ok, err)
			}
		}
		if ok, _ := s.VerifyPassword(ctx, first72[:71], stored); ok {
			t.Errorf("%.4s hash of 72 bytes takes their first 71", stored)
		}
	}
}
