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

	// A direct bind by another attribute than the id: the id attribute's value
	smith := fakeEntry{
		dn:       "cn=Alice Smith,ou=staff,dc=corp,dc=example",
		password: "secret",
		attrs:    map[string][]string{"uid": {"ASmith"}, "cn": {"Alice Smith"}},
	}
	byName := newFakeDirectory(t, smith).config()
	byName.DirectBind = true
	byName.DirectBindDN = "cn={{username}},ou=staff,dc=corp,dc=example"
	byName.UserAttrID = "uid"
	if result, err := NewLDAPProvider(byName).Authenticate("alice smith", "secret"); err != nil || result == nil || result.Username != "asmith" {
		t.Errorf("direct bind by name: %+v, %v", result, err)
	}

	// An entry without the id attribute: the name as typed, lower-cased and trimmed
	direct.UserAttrID = "sAMAccountName"
	result, err := NewLDAPProvider(direct).Authenticate("Alice", "secret")
	if err != nil || result == nil || result.Username != "alice" {
		t.Errorf("no id attribute: %+v, %v", result, err)
	}
}

// A group mapping written as a full DN fits that group only, not a group of
// the same CN in another container; a mapping written as a bare name or as
// CN=<name> fits the group's CN anywhere (RV119).
func TestLDAPGroupMatches(t *testing.T) {
	const admins = "CN=NeoDesk-Admins,OU=Security,DC=corp,DC=example"
	for _, tc := range []struct {
		key, group string
		want       bool
	}{
		{admins, admins, true},
		{admins, "cn=neodesk-admins, ou=security, dc=corp, dc=example", true},
		{admins, "CN=NeoDesk-Admins,OU=Self-Service,OU=Students,DC=corp,DC=example", false},
		{admins, "CN=NeoDesk-Admins", false},
		{"NeoDesk-Admins", "CN=NeoDesk-Admins,OU=Self-Service,DC=corp,DC=example", true},
		{"CN=NeoDesk-Admins", "CN=NeoDesk-Admins,OU=Self-Service,DC=corp,DC=example", true},
		{"cn=neodesk-admins", admins, true},
		{"Other", admins, false},
		// An escaped comma is part of the CN
		{"Admins, Berlin", `CN=Admins\, Berlin,OU=Groups,DC=corp,DC=example`, true},
		{`CN=Admins\, Berlin`, `CN=Admins\, Berlin,OU=Groups,DC=corp,DC=example`, true},
		{"Admins", `CN=Admins\, Berlin,OU=Groups,DC=corp,DC=example`, false},
		{"", admins, false},
		{admins, "", false},
	} {
		if got := ldapGroupMatches(tc.key, tc.group); got != tc.want {
			t.Errorf("ldapGroupMatches(%q, %q) = %t, want %t", tc.key, tc.group, got, tc.want)
		}
	}
}

// Direct bind puts the typed name into the DN template escaped as a DN
// value: a name with a comma cannot reach an entry in another container
// (RV129).
func TestLDAPDirectBindEscapesTheName(t *testing.T) {
	contractor := fakeEntry{
		dn:       "uid=bob,ou=contractors,ou=people,dc=corp,dc=example",
		password: "pw",
		attrs:    map[string][]string{"uid": {"bob"}},
	}
	dir := newFakeDirectory(t, aliceEntry, contractor)
	cfg := dir.config()
	cfg.DirectBind = true
	cfg.DirectBindDN = "uid={{username}},ou=people,dc=corp,dc=example"
	cfg.UserAttrID = "uid"
	p := NewLDAPProvider(cfg)

	result, err := p.Authenticate("bob,ou=contractors", "pw")
	if err != nil || result == nil || result.Authenticated {
		t.Errorf("a name with a comma: %+v, %v", result, err)
	}
	binds := dir.bindDNs()
	if len(binds) != 1 || binds[0] != `uid=bob\,ou=contractors,ou=people,dc=corp,dc=example` {
		t.Errorf("bound as %q", binds)
	}
	if result, err := p.Authenticate("alice", "secret"); err != nil || result == nil || !result.Authenticated {
		t.Errorf("alice: %+v, %v", result, err)
	}
}
