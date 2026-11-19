package authn

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
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

// TestTOTPWithShortAndNonASCIISecrets: a stored secret is read as the API
// server reads it, and its bytes are the key as they are. A short secret
// has the codes authenticator apps compute for it, and a secret that is
// not base32 (a character outside ASCII, lower case) refuses every code
// instead of failing (BUGS.md AN8).
func TestTOTPWithShortAndNonASCIISecrets(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		now := time.UnixMilli(1794287700123)
		s, _, _ := testService(t, db, func(o *Options) { o.Now = func() time.Time { return now } })
		id := addUser(t, s, "alice", "pw", "admin", "local")
		setSecret := func(secret string) {
			t.Helper()
			mustExec(t, db, `UPDATE users SET totp_secret = ?, totp_enabled = `+db.Pick("1", "TRUE")+` WHERE id = ?`, secret, id)
		}

		// "12345" as base32: five bytes
		setSecret("GEZDGNBV")
		want, err := auth.ComputeTOTP("GEZDGNBV", now)
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := s.VerifyTOTPCode(ctx, id, want); err != nil || !ok {
			t.Errorf("the code of the short secret: %v, %v", ok, err)
		}

		for _, secret := range []string{"GEZDGNBVé", "gezdgnbv", "GEZD GNBV"} {
			setSecret(secret)
			if ok, err := s.VerifyTOTPCode(ctx, id, want); err != nil || ok {
				t.Errorf("secret %q: %v, %v; want a wrong code", secret, ok, err)
			}
		}
		mustExec(t, db, `UPDATE users SET totp_secret = ?, totp_enabled = `+db.Pick("0", "FALSE")+` WHERE id = ?`, "GEZDGNBVé", id)
		if _, err := s.VerifyAndEnableTOTP(ctx, id, want); !errors.Is(err, ErrInvalidTOTPCode) {
			t.Errorf("turning two-factor on with a secret that is not base32: %v, want ErrInvalidTOTPCode", err)
		}
	})
}
