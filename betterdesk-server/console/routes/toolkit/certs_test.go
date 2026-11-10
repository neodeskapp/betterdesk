package toolkit

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The certificates of testdata/certs are what the Node oracle's TLS
// servers showed (testdata/nodeoracle/ssl.js) and what the Go test hands
// to the certificate check, so both describe the same certificates. Only
// the certificates are kept: their keys were needed for the recording
// alone. TestWriteCerts makes new ones, with keys next to them that git
// ignores:
//
//	TOOLKIT_WRITE_CERTS=1 go test ./console/routes/toolkit/ -run TestWriteCerts
//
// and the oracle's answers have to be recorded again afterwards.

// testServer is a TLS server of the recording: the certificates it sent,
// in order.
type testServer struct {
	Name  string   `json:"name"`
	Chain []string `json:"chain"`
}

var testServers = []testServer{
	{"full", []string{"leaf-full", "inter", "root"}},
	{"no-root", []string{"leaf-full", "inter"}},
	{"leaf-only", []string{"leaf-full"}},
	{"shuffled", []string{"leaf-full", "root", "inter"}},
	{"with-a-stranger", []string{"leaf-full", "leaf-selfsigned", "inter"}},
	{"selfsigned", []string{"leaf-selfsigned"}},
	{"multi-cn", []string{"leaf-multicn"}},
	{"expired", []string{"leaf-expired", "inter", "root"}},
	{"unknown-issuer", []string{"leaf-unknown"}},
	{"no-cert-sign", []string{"leaf-nocertsign"}},
	{"empty-cn", []string{"leaf-emptycn"}},
}

func san(tag int, value []byte) asn1.RawValue {
	return asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: tag, Bytes: value}
}

// issued is a certificate with its key.
type issuedCert struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

// issue makes a certificate from a template, signed by parent or by
// itself. A template without dates is valid from 2026 to 2036.
func issue(t testing.TB, template *x509.Certificate, parent *issuedCert) *issuedCert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if template.NotBefore.IsZero() {
		template.NotBefore = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		template.NotAfter = time.Date(2036, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	signer, signerKey := template, key
	if parent != nil {
		signer, signerKey = parent.cert, parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, template, signer, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &issuedCert{cert, key}
}

func authority(cn string, serial int64) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: cn, Organization: []string{"BetterDesk Test"}},
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
}

func TestWriteCerts(t *testing.T) {
	if os.Getenv("TOOLKIT_WRITE_CERTS") != "1" {
		t.Skip("set TOOLKIT_WRITE_CERTS=1 to make the test certificates again")
	}
	dir := filepath.Join("testdata", "certs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	made := map[string]*issuedCert{}
	write := func(name string, template *x509.Certificate, issuer string) {
		c := issue(t, template, made[issuer])
		made[name] = c
		keyDER, err := x509.MarshalECPrivateKey(c.key)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.cert.Raw}), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("root", authority("Toolkit Test Root", 0x0A0B0C), "")
	write("other-root", authority("Unknown Root", 0x0D0E0F), "")
	write("inter", authority("Toolkit Test Intermediate", 0x1001), "root")

	directory, err := asn1.Marshal(pkix.Name{CommonName: "dir", Organization: []string{"Dir Org"}, Country: []string{"DE"}}.ToRDNSequence())
	if err != nil {
		t.Fatal(err)
	}
	names, err := asn1.Marshal([]asn1.RawValue{
		san(2, []byte("tls.test")), san(2, []byte("*.tls.test")), san(7, net.IPv4zero.To4()), san(7, net.ParseIP("10.1.2.3").To4()),
		san(7, net.ParseIP("2001:db8::1")), san(1, []byte("admin@tls.test")), san(6, []byte("https://tls.test/x?y=1")),
		san(2, []byte("odd,name")), san(2, []byte(`quo"te`)), san(2, []byte("tab\there")), san(2, []byte("DNS:inner")), san(6, []byte("http://DNS:8/x")),
		san(8, []byte{0x2A, 0x03, 0x04}),
		{Class: asn1.ClassContextSpecific, Tag: 4, IsCompound: true, Bytes: directory},
	})
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := new(big.Int).SetString("F1A2B3C4D5E6F708", 16)
	write("leaf-full", &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "tls.test", Organization: []string{"Leaf Org"}},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		ExtraExtensions: []pkix.Extension{{Id: oidSubjectAltName, Value: names}},
	}, "inter")
	write("leaf-selfsigned", &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{Organization: []string{"Only Org"}}}, "")
	write("leaf-multicn", &x509.Certificate{
		SerialNumber: big.NewInt(0x0200),
		Subject: pkix.Name{ExtraNames: []pkix.AttributeTypeAndValue{
			{Type: oidCommonName, Value: "first.test"}, {Type: oidCommonName, Value: "second.test"},
			{Type: oidOrganization, Value: "Multi Org"}, {Type: oidOrganization, Value: "Second Org"},
		}},
		DNSNames: []string{"first.test", "second.test"},
	}, "root")
	write("leaf-expired", &x509.Certificate{
		SerialNumber: big.NewInt(0x0300), Subject: pkix.Name{CommonName: "expired.test"},
		NotBefore: time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2020, 1, 1, 12, 30, 0, 0, time.UTC),
		IPAddresses: []net.IP{net.IPv4zero},
	}, "inter")
	write("leaf-unknown", &x509.Certificate{SerialNumber: big.NewInt(0x0400), Subject: pkix.Name{CommonName: "unknown.test"}, DNSNames: []string{"unknown.test"}}, "other-root")
	write("leaf-nocertsign", &x509.Certificate{SerialNumber: big.NewInt(0x0500), Subject: pkix.Name{CommonName: "nocertsign.test"}, KeyUsage: x509.KeyUsageDigitalSignature}, "")
	write("leaf-emptycn", &x509.Certificate{
		SerialNumber: big.NewInt(0),
		Subject:      pkix.Name{ExtraNames: []pkix.AttributeTypeAndValue{{Type: oidCommonName, Value: ""}, {Type: oidOrganization, Value: "Fallback Org"}}},
	}, "")

	manifest, _ := json.MarshalIndent(testServers, "", " ")
	if err := os.WriteFile(filepath.Join(dir, "servers.json"), append(manifest, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// loadCert reads a certificate of testdata/certs.
func loadCert(t testing.TB, name string) *x509.Certificate {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "certs", name+".crt"))
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// sentBy is the certificates a test server of the recording sent.
func sentBy(t testing.TB, name string) []*x509.Certificate {
	t.Helper()
	for _, s := range testServers {
		if s.Name == name {
			var chain []*x509.Certificate
			for _, cert := range s.Chain {
				chain = append(chain, loadCert(t, cert))
			}
			return chain
		}
	}
	t.Fatalf("no test server %q", name)
	return nil
}

// rootsOf is a pool with the test root, or an empty one.
func rootsOf(t testing.TB, trusted bool) *x509.CertPool {
	pool := x509.NewCertPool()
	if trusted {
		pool.AddCert(loadCert(t, "root"))
	}
	return pool
}

// liveChain is a root, an intermediate and a server certificate for
// live.test made for one test, with the key a TLS server needs.
type liveChain struct {
	root   *issuedCert
	server tls.Certificate
}

func newLiveChain(t testing.TB) *liveChain {
	t.Helper()
	root := issue(t, authority("Live Test Root", 0x0A0B0C), nil)
	inter := issue(t, authority("Live Test Intermediate", 0x1001), root)
	leaf := issue(t, &x509.Certificate{
		SerialNumber: big.NewInt(0xF1A2B3), Subject: pkix.Name{CommonName: "live.test", Organization: []string{"Leaf Org"}},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"live.test", "*.live.test"},
	}, inter)
	return &liveChain{root: root, server: tls.Certificate{Certificate: [][]byte{leaf.cert.Raw, inter.cert.Raw}, PrivateKey: leaf.key}}
}

func (c *liveChain) roots() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(c.root.cert)
	return pool
}

// serve starts a TLS server that shows the chain and returns its port.
func (c *liveChain) serve(t testing.TB, config *tls.Config) int {
	t.Helper()
	config.Certificates = []tls.Certificate{c.server}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.(*tls.Conn).Handshake()
				buf := make([]byte, 1)
				_, _ = conn.Read(buf)
			}()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port
}
