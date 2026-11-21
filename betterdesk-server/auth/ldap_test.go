package auth

import "testing"

// aliceEntry is alice in the test directory, password "secret".
var aliceEntry = fakeEntry{
	dn:       "uid=alice,ou=people,dc=corp,dc=example",
	password: "secret",
	attrs:    map[string][]string{"uid": {"alice"}, "mail": {"alice@corp.example"}},
}

// The account name of a directory sign-in is the entry's id attribute,
// lower-cased and trimmed, whatever spelling of it was typed (RV117).
func TestLDAPAccountNameComesFromTheEntry(t *testing.T) {
	entry := aliceEntry
	entry.attrs = map[string][]string{"uid": {"  Alice "}, "mail": {"alice@corp.example"}}
	dir := newFakeDirectory(t, entry)

	search := dir.config()
	search.BaseDN = "dc=corp,dc=example"
	search.UserFilter = "(uid={{username}})"
	search.UserAttrID = "uid"
	direct := dir.config()
	direct.DirectBind = true
	direct.DirectBindDN = "uid={{username}},ou=people,dc=corp,dc=example"
	direct.UserAttrID = "uid"

	for mode, cfg := range map[string]*LDAPConfig{"search": search, "direct bind": direct} {
		p := NewLDAPProvider(cfg)
		for _, typed := range []string{"alice", "Alice", "ALICE"} {
			result, err := p.Authenticate(typed, "secret")
			if err != nil || result == nil || !result.Authenticated {
				t.Fatalf("%s, %q: %+v, %v", mode, typed, result, err)
			}
			if result.Username != "alice" {
				t.Errorf("%s, typed %q: account name %q, want alice", mode, typed, result.Username)
			}
		}
	}

	// An entry without the id attribute: the name as typed, lower-cased and trimmed
	direct.UserAttrID = "sAMAccountName"
	result, err := NewLDAPProvider(direct).Authenticate("Alice", "secret")
	if err != nil || result == nil || result.Username != "alice" {
		t.Errorf("no id attribute: %+v, %v", result, err)
	}
}
