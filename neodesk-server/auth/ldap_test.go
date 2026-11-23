package auth

import (
	"net"
	"sync"
	"testing"
	"time"
)

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

// silentServer accepts connections and never answers, like a directory
// that hangs or a TLS port that never finishes its handshake.
func silentServer(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return ln.Addr().(*net.TCPAddr).Port
}

// Connection timeout covers connecting: the TLS handshake of LDAPS and the
// StartTLS request give up after the configured seconds, not after go-ldap's
// 60 seconds or never (L347).
func TestLDAPConnectionTimeoutCoversConnecting(t *testing.T) {
	for name, tweak := range map[string]func(*LDAPConfig){
		"ldaps":    func(c *LDAPConfig) { c.UseTLS = true },
		"starttls": func(c *LDAPConfig) { c.StartTLS = true },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := &LDAPConfig{Enabled: true, Host: "127.0.0.1", Port: silentServer(t), ConnTimeoutSec: 1, DirectBind: true,
				DirectBindDN: "uid={{username}},dc=corp,dc=example"}
			tweak(cfg)
			done := make(chan error, 1)
			start := time.Now()
			go func() {
				_, err := NewLDAPProvider(cfg).Authenticate("alice", "secret")
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("a directory that never answers signed someone in")
				}
				if took := time.Since(start); took > 4*time.Second {
					t.Errorf("gave up after %s, want about 1s", took)
				}
			case <-time.After(8 * time.Second):
				t.Fatal("still waiting after 8s with a timeout of 1s")
			}
		})
	}
}

const (
	serviceDN  = "cn=svc,dc=corp,dc=example"
	groupsBase = "ou=groups,dc=corp,dc=example"
)

var serviceEntry = fakeEntry{dn: serviceDN, password: "svc-pw", attrs: map[string][]string{"cn": {"svc"}}}

// A directory whose groups only the service account may read, as with
// OpenLDAP's default ACL: the group search runs as the service account
// (Bind DN), not as the person signing in, so the role map still applies
// (L348).
func TestLDAPGroupSearchRunsAsTheServiceAccount(t *testing.T) {
	admins := fakeEntry{
		dn:      "cn=neodesk-admins," + groupsBase,
		attrs:   map[string][]string{"cn": {"neodesk-admins"}, "member": {aliceEntry.dn}},
		readers: []string{serviceDN},
	}
	dir := newFakeDirectory(t, serviceEntry, aliceEntry, admins)
	cfg := dir.config()
	cfg.BindDN, cfg.BindPassword = serviceDN, "svc-pw"
	cfg.BaseDN = "dc=corp,dc=example"
	cfg.UserFilter = "(uid={{username}})"
	cfg.UserAttrID = "uid"
	cfg.GroupBaseDN = groupsBase
	cfg.GroupFilter = "(member={{dn}})"
	cfg.GroupRoleMap = "neodesk-admins=admin"
	result, err := NewLDAPProvider(cfg).Authenticate("alice", "secret")
	if err != nil || result == nil || !result.Authenticated {
		t.Fatalf("sign-in: %+v, %v", result, err)
	}
	if result.Role != RoleAdmin {
		t.Errorf("role %q with groups %v, want admin", result.Role, result.Groups)
	}
}

// Group name attribute names the attribute of a group entry that the role
// map's names are compared with, for the groups the group search finds
// (L349).
func TestLDAPGroupNameAttributeIsUsedByTheRoleMap(t *testing.T) {
	admins := fakeEntry{
		dn:    "cn=neodesk-admins," + groupsBase,
		attrs: map[string][]string{"cn": {"neodesk-admins"}, "description": {"nd-admin-desc"}, "member": {aliceEntry.dn}},
	}
	dir := newFakeDirectory(t, aliceEntry, admins)
	cfg := dir.config()
	cfg.DirectBind = true
	cfg.DirectBindDN = "uid={{username}},ou=people,dc=corp,dc=example"
	cfg.UserAttrID = "uid"
	cfg.GroupBaseDN = groupsBase
	cfg.GroupFilter = "(member={{dn}})"
	cfg.GroupAttrName = "description"
	for mapping, want := range map[string]string{
		"nd-admin-desc=admin":  RoleAdmin,
		"ND-Admin-Desc=admin":  RoleAdmin,
		"neodesk-admins=admin": RoleAdmin, // the CN still matches
		"other-desc=admin":     RoleViewer,
	} {
		cfg.GroupRoleMap = mapping
		result, err := NewLDAPProvider(cfg).Authenticate("alice", "secret")
		if err != nil || result == nil || !result.Authenticated {
			t.Fatalf("%s: %+v, %v", mapping, result, err)
		}
		if result.Role != want {
			t.Errorf("%s: role %q, want %q", mapping, result.Role, want)
		}
	}
}
