package authn

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestSignInLogLinesMaskTheName: the sign-in's log lines carry a user
// name shortened where the line is built, whatever the name looks like: a
// dot, an e-mail address, a space, a role's name, over 64 characters
// (BUGS.md AL4).
func TestSignInLogLinesMaskTheName(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	s, stub, logs := testService(t, db, nil)
	long := strings.Repeat("n", 70)
	names := []string{"john.doe", "john.doe@example.com", "Jane Doe", "admin", long}
	addUser(t, s, "john.doe", "pw", "operator", "local")
	addUser(t, s, "john.doe@example.com", "pw", "operator", "oidc")
	addUser(t, s, "Jane Doe", "pw", "operator", "ldap")
	addUser(t, s, "admin", "pw", "admin", "local")

	attempts := []struct{ name, password string }{
		{"john.doe", "wrong"},          // password mismatch
		{"john.doe", "pw"},             // signed in
		{"john.doe@example.com", "pw"}, // single sign-on account
		{"Jane Doe", "pw"},             // directory refuses
		{"admin", "pw"},                // signed in
		{long, "pw"},                   // unknown
	}
	for _, a := range attempts {
		if _, err := s.Authenticate(ctx, a.name, a.password); err != nil {
			t.Fatalf("%s: %v", a.name, err)
		}
	}
	// The directory accepts the name: the row is synced
	stub.load(&goScript{Login: []json.RawMessage{json.RawMessage(`{"status":200,"body":{"token":"t","role":"viewer","auth_provider":"ldap"}}`)}})
	if _, err := s.Authenticate(ctx, "Jane Doe", "pw"); err != nil {
		t.Fatal(err)
	}

	lines := logs.take()
	if len(lines) < len(attempts) {
		t.Fatalf("only %d log lines", len(lines))
	}
	for _, line := range lines {
		text := fmt.Sprint(line)
		for _, name := range names {
			if strings.Contains(text, "'"+name+"'") || strings.Contains(text, ": "+name) {
				t.Errorf("the line %q names %q", text, name)
			}
		}
	}
}
