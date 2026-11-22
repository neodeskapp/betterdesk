// Command gencerts wrote the certificates the protocol check was recorded
// and is replayed with (testdata/certs): a test CA and server certificates
// that are good, for another host, expired, self-signed and without
// subject alternative names. They are for tests only. Don't run it again:
// the recording can't be made again to match new certificates.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func main() {
	dir := filepath.Join("cli", "testdata", "certs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}
	long := [2]time.Time{time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)}
	caKey := key()
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "BetterDesk test CA"},
		NotBefore: long[0], NotAfter: long[1], IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		log.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	write(dir, "ca.pem", "CERTIFICATE", caDER)

	local := []string{"localhost"}
	loopback := []net.IP{net.ParseIP("127.0.0.1")}
	for _, c := range []struct {
		name    string
		cn      string
		dns     []string
		ips     []net.IP
		valid   [2]time.Time
		selfish bool
	}{
		{name: "good", cn: "localhost", dns: local, ips: loopback, valid: long},
		{name: "wronghost", cn: "other.example", dns: []string{"other.example", "*.other.example"}, ips: []net.IP{net.ParseIP("10.9.9.9"), net.ParseIP("::1")}, valid: long},
		{name: "expired", cn: "localhost", dns: local, ips: loopback, valid: [2]time.Time{time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)}},
		{name: "self", cn: "localhost", dns: local, ips: loopback, valid: long, selfish: true},
		{name: "cnonly", cn: "localhost", valid: long},
	} {
		k := key()
		template := &x509.Certificate{
			SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: c.cn},
			NotBefore: c.valid[0], NotAfter: c.valid[1], DNSNames: c.dns, IPAddresses: c.ips,
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}
		parent, signer := ca, caKey
		if c.selfish {
			parent, signer = template, k
		}
		der, err := x509.CreateCertificate(rand.Reader, template, parent, &k.PublicKey, signer)
		if err != nil {
			log.Fatal(err)
		}
		write(dir, c.name+".pem", "CERTIFICATE", der)
		raw, err := x509.MarshalECPrivateKey(k)
		if err != nil {
			log.Fatal(err)
		}
		write(dir, c.name+"-key.pem", "EC PRIVATE KEY", raw)
	}
}

func key() *ecdsa.PrivateKey {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	return k
}

func write(dir, name, kind string, der []byte) {
	if err := os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0o644); err != nil {
		log.Fatal(err)
	}
}
