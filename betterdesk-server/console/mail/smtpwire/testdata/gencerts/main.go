// Command gencerts wrote the test certificates and keys of the smtpwire
// tests into the directory given as its argument. They are checked in
// because the recordings in testdata were made with them. Don't run it
// again: new certificates would no longer match the recordings, which
// can't be made again.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

type spec struct {
	name      string
	cn        string
	extraCNs  []string
	dns       []string
	ips       []string
	emails    []string
	uris      []string
	ca        bool
	parent    string
	notBefore time.Time
	notAfter  time.Time
	// chain lists the certificates the server presents, leaf first
	chain   []string
	trusted bool
	rawSAN  []asn1.RawValue
	// clientOnly makes a certificate that is not for servers
	clientOnly bool
}

var (
	longAgo  = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	farAhead = time.Date(2120, 1, 1, 0, 0, 0, 0, time.UTC)
)

func specs() []spec {
	valid := func(s spec) spec {
		s.notBefore, s.notAfter = longAgo, farAhead
		return s
	}
	return []spec{
		valid(spec{name: "localhost", cn: "localhost", dns: []string{"localhost"}, ips: []string{"127.0.0.1", "::1"}, ca: true, trusted: true}),
		valid(spec{name: "other", cn: "other.example.test", dns: []string{"other.example.test"}, ca: true, trusted: true}),
		valid(spec{name: "untrusted", cn: "localhost", dns: []string{"localhost"}, ips: []string{"127.0.0.1"}, ca: true}),
		valid(spec{name: "ca", cn: "smtpwire test CA", ca: true, trusted: true}),
		valid(spec{name: "ca-leaf", cn: "localhost", dns: []string{"localhost"}, ips: []string{"127.0.0.1"}, parent: "ca"}),
		{name: "ca-expired", cn: "localhost", dns: []string{"localhost"}, ips: []string{"127.0.0.1"}, parent: "ca",
			notBefore: longAgo, notAfter: time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)},
		{name: "ca-future", cn: "localhost", dns: []string{"localhost"}, ips: []string{"127.0.0.1"}, parent: "ca",
			notBefore: time.Date(2110, 1, 1, 0, 0, 0, 0, time.UTC), notAfter: farAhead},
		valid(spec{name: "rogue-ca", cn: "smtpwire rogue CA", ca: true}),
		valid(spec{name: "rogue-leaf", cn: "localhost", dns: []string{"localhost"}, ips: []string{"127.0.0.1"}, parent: "rogue-ca"}),
		valid(spec{name: "rogue-leaf-root", cn: "localhost", dns: []string{"localhost"}, ips: []string{"127.0.0.1"}, parent: "rogue-ca",
			chain: []string{"rogue-leaf-root", "rogue-ca"}}),
		valid(spec{name: "rogue-inter", cn: "smtpwire rogue intermediate", ca: true, parent: "rogue-ca"}),
		valid(spec{name: "rogue-inter-leaf", cn: "localhost", dns: []string{"localhost"}, ips: []string{"127.0.0.1"}, parent: "rogue-inter",
			chain: []string{"rogue-inter-leaf", "rogue-inter"}}),
		valid(spec{name: "rogue-inter-leaf-root", cn: "localhost", dns: []string{"localhost"}, ips: []string{"127.0.0.1"}, parent: "rogue-inter",
			chain: []string{"rogue-inter-leaf-root", "rogue-inter", "rogue-ca"}}),
		valid(spec{name: "client-only", cn: "localhost", dns: []string{"localhost"}, ips: []string{"127.0.0.1"}, parent: "ca", clientOnly: true}),
		valid(spec{name: "issued-by-leaf", cn: "localhost", dns: []string{"localhost"}, ips: []string{"127.0.0.1"}, parent: "ca-leaf",
			chain: []string{"issued-by-leaf", "ca-leaf"}}),
		valid(spec{name: "cn-only", cn: "localhost", ca: true, trusted: true}),
		valid(spec{name: "cn-other", cn: "elsewhere.example.test", ca: true, trusted: true}),
		valid(spec{name: "ip-only", cn: "ip only cert", ips: []string{"127.0.0.1"}, ca: true, trusted: true}),
		valid(spec{name: "ip-other", cn: "localhost", dns: []string{"localhost"}, ips: []string{"10.1.2.3", "fe80::1"}, ca: true, trusted: true}),
		valid(spec{name: "no-names", ca: true, trusted: true}),
		valid(spec{name: "mixed-san", cn: "mixed", dns: []string{"a.example.test", "*.b.example.test"}, ips: []string{"192.0.2.7", "2001:db8::a:1"},
			emails: []string{"postmaster@example.test"}, uris: []string{"https://example.test/path?x=1"}, ca: true, trusted: true}),
		valid(spec{name: "wildcard", cn: "wild", dns: []string{"*.localhost", "*.example.test"}, ca: true, trusted: true}),
		valid(spec{name: "multi-cn", cn: "first.example.test", extraCNs: []string{"localhost"}, ca: true, trusted: true}),
		{name: "expired-untrusted", cn: "localhost", dns: []string{"localhost"}, ips: []string{"127.0.0.1"}, ca: true,
			notBefore: longAgo, notAfter: time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)},
		{name: "expired-trusted", cn: "localhost", dns: []string{"localhost"}, ips: []string{"127.0.0.1"}, ca: true, trusted: true,
			notBefore: longAgo, notAfter: time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)},
		valid(spec{name: "odd-san", cn: "odd", ca: true, trusted: true, rawSAN: []asn1.RawValue{
			{Class: asn1.ClassContextSpecific, Tag: 2, Bytes: []byte("plain.example.test")},
			{Class: asn1.ClassContextSpecific, Tag: 2, Bytes: []byte("with, comma.example.test")},
			{Class: asn1.ClassContextSpecific, Tag: 2, Bytes: []byte("quote\"d.example.test")},
			{Class: asn1.ClassContextSpecific, Tag: 2, Bytes: []byte("back\\slash.example.test")},
			{Class: asn1.ClassContextSpecific, Tag: 2, Bytes: []byte("sp ace.example.test")},
			{Class: asn1.ClassContextSpecific, Tag: 2, Bytes: []byte("ctl\x01\n.example.test")},
			{Class: asn1.ClassContextSpecific, Tag: 1, Bytes: []byte("a, b@example.test")},
			{Class: asn1.ClassContextSpecific, Tag: 6, Bytes: []byte("https://example.test/a,b")},
			{Class: asn1.ClassContextSpecific, Tag: 7, Bytes: []byte{192, 0, 2, 1}},
			{Class: asn1.ClassContextSpecific, Tag: 7, Bytes: []byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x0a, 0xbc, 0xde}},
			{Class: asn1.ClassContextSpecific, Tag: 8, Bytes: []byte{0x2a, 0x03, 0x04}},
		}}),
	}
}

type made struct {
	cert *x509.Certificate
	der  []byte
	key  *ecdsa.PrivateKey
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: gencerts <directory>")
		os.Exit(2)
	}
	dir := os.Args[1]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(err)
	}
	all := map[string]made{}
	list := specs()
	var trusted []byte
	index := map[string]any{}
	for i, s := range list {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			fail(err)
		}
		subject := pkix.Name{}
		if s.cn != "" {
			subject.CommonName = s.cn
		}
		for _, cn := range s.extraCNs {
			subject.ExtraNames = append(subject.ExtraNames, pkix.AttributeTypeAndValue{Type: asn1.ObjectIdentifier{2, 5, 4, 3}, Value: cn})
		}
		if len(s.extraCNs) > 0 {
			subject.ExtraNames = append([]pkix.AttributeTypeAndValue{{Type: asn1.ObjectIdentifier{2, 5, 4, 3}, Value: s.cn}}, subject.ExtraNames...)
			subject.CommonName = ""
		}
		// Every certificate gets its own subject: OpenSSL finds a trust anchor
		// by subject name and takes one of several with the same name.
		subject.Organization = []string{"smtpwire test " + s.name}
		tmpl := &x509.Certificate{
			SerialNumber:          big.NewInt(int64(1000 + i)),
			Subject:               subject,
			NotBefore:             s.notBefore,
			NotAfter:              s.notAfter,
			KeyUsage:              x509.KeyUsageDigitalSignature,
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			BasicConstraintsValid: true,
			IsCA:                  s.ca,
			DNSNames:              s.dns,
			EmailAddresses:        s.emails,
		}
		if s.ca {
			tmpl.KeyUsage |= x509.KeyUsageCertSign
		}
		if s.clientOnly {
			tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		}
		for _, ip := range s.ips {
			tmpl.IPAddresses = append(tmpl.IPAddresses, net.ParseIP(ip))
		}
		for _, u := range s.uris {
			parsed, err := url.Parse(u)
			if err != nil {
				fail(err)
			}
			tmpl.URIs = append(tmpl.URIs, parsed)
		}
		if s.rawSAN != nil {
			value, err := asn1.Marshal(s.rawSAN)
			if err != nil {
				fail(err)
			}
			tmpl.ExtraExtensions = []pkix.Extension{{Id: asn1.ObjectIdentifier{2, 5, 29, 17}, Value: value}}
		}
		parent, parentKey := tmpl, key
		if s.parent != "" {
			p, ok := all[s.parent]
			if !ok {
				fail(fmt.Errorf("%s: parent %s comes later", s.name, s.parent))
			}
			parent, parentKey = p.cert, p.key
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, parentKey)
		if err != nil {
			fail(fmt.Errorf("%s: %w", s.name, err))
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			fail(fmt.Errorf("%s: %w", s.name, err))
		}
		all[s.name] = made{cert: cert, der: der, key: key}

		keyDER, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			fail(err)
		}
		write(filepath.Join(dir, s.name+".key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
		chain := s.chain
		if chain == nil {
			chain = []string{s.name}
		}
		var chainPEM []byte
		for _, name := range chain {
			chainPEM = append(chainPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: all[name].der})...)
		}
		write(filepath.Join(dir, s.name+".chain.pem"), chainPEM)
		if s.trusted {
			trusted = append(trusted, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
		}
		index[s.name] = map[string]any{"chain": chain, "trusted": s.trusted}
	}
	write(filepath.Join(dir, "trusted.pem"), trusted)
	out, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		fail(err)
	}
	write(filepath.Join(dir, "index.json"), append(out, '\n'))
}

func write(path string, data []byte) {
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "gencerts:", err)
	os.Exit(1)
}
