package authn

import (
	"context"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// Checks that refuse something the Node console could not even produce,
// so no vector of it exists: the stored text is made here.

// A hash with a cost below 4 is refused, also when it is the true bcrypt
// of the password at that cost.
func TestBcryptRefusesLowCosts(t *testing.T) {
	ctx := context.Background()
	salt, ok := bcryptDecodeSalt("KiXeOj5vHpJRJHGMhWzade")
	if !ok {
		t.Fatal("salt")
	}
	for cost := uint(0); cost <= 4; cost++ {
		raw, err := bcryptRaw(ctx, bcryptKey("pw", 'b'), cost, salt)
		if err != nil {
			t.Fatal(err)
		}
		stored := bcryptFormat('b', cost, salt, raw)
		got, err := pureService().VerifyPassword(ctx, "pw", stored)
		if want := cost == 4; err != nil || got != want {
			t.Errorf("cost %d (%s): %v, %v; want %v", cost, stored, got, err, want)
		}
	}
}

// PBKDF2 with zero iterations is refused, also when the stored hash is
// what one round gives.
func TestPBKDF2RefusesZeroIterations(t *testing.T) {
	ctx := context.Background()
	salt := []byte("0123456789abcdef")
	key, err := pbkdf2.Key(sha256.New, "pw", salt, 1, 32)
	if err != nil {
		t.Fatal(err)
	}
	for _, iterations := range []string{"0", "-1", "+0", " 0"} {
		stored := fmt.Sprintf("pbkdf2-sha256$%s$%x$%x", iterations, salt, key)
		if got, err := pureService().VerifyPassword(ctx, "pw", stored); err != nil || got {
			t.Errorf("%s: %v, %v; want a refusal", stored, got, err)
		}
	}
	one := fmt.Sprintf("pbkdf2-sha256$1$%x$%x", salt, key)
	if got, err := pureService().VerifyPassword(ctx, "pw", one); err != nil || !got {
		t.Errorf("%s: %v, %v; one round is allowed", one, got, err)
	}
}

// The old PBKDF2 form is only the exact one: 32 hex digits of salt and 64
// of hash. A shorter salt is not read as PBKDF2, also when the hash fits
// it.
func TestLegacyPBKDF2NeedsItsExactForm(t *testing.T) {
	ctx := context.Background()
	for _, saltLength := range []int{15, 16, 17} {
		salt := make([]byte, saltLength)
		for i := range salt {
			salt[i] = byte(i + 1)
		}
		key, err := pbkdf2.Key(sha256.New, "pw", salt, pbkdf2LegacyIterations, 32)
		if err != nil {
			t.Fatal(err)
		}
		stored := hex.EncodeToString(salt) + ":" + hex.EncodeToString(key)
		valid, err := pureService().VerifyPassword(ctx, "pw", stored)
		if want := saltLength == 16; err != nil || valid != want || isPBKDF2Hash(stored) != want {
			t.Errorf("salt of %d bytes: %v, %v; want valid and PBKDF2 = %v", saltLength, valid, err, want)
		}
	}
}

func TestTokenLengthRules(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, _, _ := testService(t, db, nil)
		id := addUser(t, s, "alice", "pw", "operator", "local")
		insert := `INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, token_kind) VALUES (?, ?, ?, ?, 'u', ` +
			db.Pick("datetime('now', '+1 day')", "NOW() + INTERVAL '1 day'") + `, 'device')`
		// Device tokens of other lengths.
		for _, token := range []string{"legacy-token-of-another-length", "short12"} {
			mustExec(t, db, insert, store.StoredAccessToken(HashAccessToken(token)), HashAccessToken(token), id, "device-a")
		}

		if row, err := s.LookupAccessToken(ctx, "legacy-token-of-another-length"); err != nil || row == nil {
			t.Fatalf("the row is not found at all: %v, %v", row, err)
		}
		// Only a token of 64 characters is a client's access token.
		if user, err := s.ValidateAccessToken(ctx, "legacy-token-of-another-length"); err != nil || user != nil {
			t.Errorf("a token of 30 characters validates: %+v, %v", user, err)
		}
		// A device socket takes any token of 8 characters or more. (On
		// PostgreSQL the check fails as a whole without the signal server's
		// token table, BUGS.md AN9.)
		if !db.IsPostgres() {
			if ok, err := s.VerifyDeviceWSAuth(ctx, "device-a", "legacy-token-of-another-length"); err != nil || !ok {
				t.Errorf("the socket check refuses a bound token of 30 characters: %v, %v", ok, err)
			}
		}
		if ok, err := s.VerifyDeviceWSAuth(ctx, "device-a", "short12"); err != nil || ok {
			t.Errorf("the socket check accepts a token of 7 characters: %v, %v", ok, err)
		}
	})
}

func TestRevokeWithoutClientRevokesAll(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, _, _ := testService(t, db, nil)
		id := addUser(t, s, "alice", "pw", "operator", "local")
		other := addUser(t, s, "bob", "pw", "operator", "local")
		first, _ := s.GenerateSignInToken(ctx, id, "client-a", "uuid-a", "")
		second, _ := s.GenerateSignInToken(ctx, id, "client-b", "uuid-b", "")
		foreign, _ := s.GenerateSignInToken(ctx, other, "client-a", "uuid-a", "")

		live := func(token string) bool {
			t.Helper()
			user, err := s.ValidateAccessToken(ctx, token)
			if err != nil {
				t.Fatal(err)
			}
			return user != nil
		}
		// With both names: only the tokens of that client UUID.
		if err := s.RevokeClientTokens(ctx, id, "client-a", "uuid-a"); err != nil {
			t.Fatal(err)
		}
		if live(first) || !live(second) || !live(foreign) {
			t.Errorf("after revoking one client: first %v, second %v, the other user's %v", live(first), live(second), live(foreign))
		}
		// Without a client id: all of the user's tokens, whatever the UUID.
		third, _ := s.GenerateSignInToken(ctx, id, "client-c", "uuid-c", "")
		if err := s.RevokeClientTokens(ctx, id, "", "uuid-c"); err != nil {
			t.Fatal(err)
		}
		if live(second) || live(third) || !live(foreign) {
			t.Errorf("after revoking without a client id: second %v, third %v, the other user's %v", live(second), live(third), live(foreign))
		}
	})
}

// Access tokens are kept as their hash only: the token column holds
// "sha256 " and the hash, never the token, and every lookup goes by the
// hash. A row that holds a token without its hash finds nothing, and
// neither does what a row holds in place of a token (BUGS RV128).
func TestTokensAreKeptAsTheirHashOnly(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		db = storetest.Prepare(t, db, storetest.GoServerFirst)
		ctx := context.Background()
		s, _, _ := testService(t, db, nil)
		id := addUser(t, s, "alice", "pw", "operator", "local")
		signIn, err := s.GenerateSignInToken(ctx, id, "client-a", "uuid-a", "")
		if err != nil {
			t.Fatal(err)
		}
		device, err := s.IssueDeviceToken(ctx, id, "900100001", "")
		if err != nil {
			t.Fatal(err)
		}
		for name, token := range map[string]string{"sign-in": signIn, "device": device} {
			var stored, hash string
			if err := db.QueryRow(ctx, `SELECT token, token_hash FROM access_tokens WHERE token_hash = ?`, HashAccessToken(token)).Scan(&stored, &hash); err != nil {
				t.Fatalf("the %s token's row: %v", name, err)
			}
			if stored != "sha256 "+HashAccessToken(token) || strings.Contains(stored, token) {
				t.Errorf("the %s token's row holds %q", name, stored)
			}
			if row, err := s.LookupAccessToken(ctx, stored); row != nil || err != nil {
				t.Errorf("what the %s token's row holds finds %+v (%v)", name, row, err)
			}
		}
		if user, err := s.ValidateAccessToken(ctx, signIn); user == nil || err != nil {
			t.Errorf("the sign-in token: %v", err)
		}
		if got, err := s.RequireDeviceToken(ctx, "Bearer "+device); err != nil || got.DeviceID != "900100001" {
			t.Errorf("the device token: %+v, %v", got, err)
		}
		var used store.Time
		if err := store.SelectRow(ctx, db, `SELECT last_used FROM access_tokens WHERE token_hash = ?`, HashAccessToken(device)).Scan(&used); err != nil || used.IsNull() {
			t.Errorf("the use of the device token was not noted: %v", err)
		}

		plain := strings.Repeat("ab", 32)
		mustExec(t, db, `INSERT INTO access_tokens (token, user_id, client_id, expires_at, token_kind) VALUES (?, ?, '900100002', `+
			db.Pick("datetime('now', '+1 day')", "NOW() + INTERVAL '1 day'")+`, 'device')`, plain, id)
		if row, err := s.LookupAccessToken(ctx, plain); row != nil || err != nil {
			t.Errorf("a token kept as it is without a hash finds %+v (%v)", row, err)
		}
		if ok, err := s.VerifyDeviceWSAuth(ctx, "900100002", plain); ok || err != nil {
			t.Errorf("the device socket takes a token kept as it is: %v, %v", ok, err)
		}
	})
}
