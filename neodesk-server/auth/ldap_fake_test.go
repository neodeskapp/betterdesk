package auth

import (
	"net"
	"strings"
	"sync"
	"testing"

	ber "github.com/go-asn1-ber/asn1-ber"
	ldapv3 "github.com/go-ldap/ldap/v3"
)

// fakeDirectory is a small LDAP server for tests. It answers simple binds,
// searches whose filter is one (attribute=value) or (objectClass=*), and
// unbinds. Like a real directory it compares DNs and values without regard
// to case.
type fakeDirectory struct {
	t       *testing.T
	ln      net.Listener
	entries []fakeEntry
	mu      sync.Mutex
	binds   []string // the DNs of binds, in order
}

type fakeEntry struct {
	dn       string
	password string
	attrs    map[string][]string
}

func newFakeDirectory(t *testing.T, entries ...fakeEntry) *fakeDirectory {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	d := &fakeDirectory{t: t, ln: ln, entries: entries}
	go d.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return d
}

// config is an enabled LDAP configuration for this directory.
func (d *fakeDirectory) config() *LDAPConfig {
	return &LDAPConfig{Enabled: true, Host: "127.0.0.1", Port: d.ln.Addr().(*net.TCPAddr).Port, ConnTimeoutSec: 5}
}

func (d *fakeDirectory) bindDNs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.binds...)
}

func (d *fakeDirectory) serve() {
	for {
		conn, err := d.ln.Accept()
		if err != nil {
			return
		}
		go d.handle(conn)
	}
}

func (d *fakeDirectory) handle(conn net.Conn) {
	defer conn.Close()
	for {
		packet, err := ber.ReadPacket(conn)
		if err != nil || len(packet.Children) < 2 {
			return
		}
		id := packet.Children[0].Value.(int64)
		op := packet.Children[1]
		switch op.Tag {
		case ldapv3.ApplicationBindRequest:
			dn := op.Children[1].Value.(string)
			password := op.Children[2].Data.String()
			d.mu.Lock()
			d.binds = append(d.binds, dn)
			d.mu.Unlock()
			code := int64(ldapv3.LDAPResultInvalidCredentials)
			if e := d.find(dn); e != nil && e.password == password {
				code = ldapv3.LDAPResultSuccess
			}
			d.write(conn, id, ldapResult(ldapv3.ApplicationBindResponse, code))
		case ldapv3.ApplicationSearchRequest:
			base := op.Children[0].Value.(string)
			scope := op.Children[1].Value.(int64)
			filter, _ := ldapv3.DecompileFilter(op.Children[6])
			for _, e := range d.entries {
				if d.matches(e, base, scope, filter) {
					d.write(conn, id, searchEntry(e))
				}
			}
			d.write(conn, id, ldapResult(ldapv3.ApplicationSearchResultDone, ldapv3.LDAPResultSuccess))
		default:
			return
		}
	}
}

func (d *fakeDirectory) find(dn string) *fakeEntry {
	for i := range d.entries {
		if strings.EqualFold(d.entries[i].dn, dn) {
			return &d.entries[i]
		}
	}
	return nil
}

func (d *fakeDirectory) matches(e fakeEntry, base string, scope int64, filter string) bool {
	if scope == ldapv3.ScopeBaseObject {
		return strings.EqualFold(e.dn, base)
	}
	if !strings.HasSuffix(strings.ToLower(e.dn), strings.ToLower(base)) {
		return false
	}
	attr, value, ok := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(filter, "("), ")"), "=")
	if !ok {
		return false
	}
	if strings.EqualFold(attr, "objectClass") && value == "*" {
		return true
	}
	for name, values := range e.attrs {
		if !strings.EqualFold(name, attr) {
			continue
		}
		for _, v := range values {
			if strings.EqualFold(strings.TrimSpace(v), strings.TrimSpace(value)) {
				return true
			}
		}
	}
	return false
}

func (d *fakeDirectory) write(conn net.Conn, id int64, op *ber.Packet) {
	msg := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "LDAP Response")
	msg.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, id, "MessageID"))
	msg.AppendChild(op)
	_, _ = conn.Write(msg.Bytes())
}

func ldapResult(tag ber.Tag, code int64) *ber.Packet {
	op := ber.Encode(ber.ClassApplication, ber.TypeConstructed, tag, nil, "Result")
	op.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagEnumerated, code, "resultCode"))
	op.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "matchedDN"))
	op.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "diagnosticMessage"))
	return op
}

func searchEntry(e fakeEntry) *ber.Packet {
	op := ber.Encode(ber.ClassApplication, ber.TypeConstructed, ldapv3.ApplicationSearchResultEntry, nil, "Entry")
	op.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, e.dn, "objectName"))
	attrs := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "attributes")
	for name, values := range e.attrs {
		attr := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "attribute")
		attr.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, name, "type"))
		set := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSet, nil, "vals")
		for _, v := range values {
			set.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, v, "val"))
		}
		attr.AppendChild(set)
		attrs.AppendChild(attr)
	}
	op.AppendChild(attrs)
	return op
}
