package listen

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
)

// Files are the certificate files as read from disk, not yet parsed.
type Files struct {
	Cert []byte
	Key  []byte
	// CA is the bundle of the certificates that issued Cert, nil without
	// one.
	CA []byte
}

// ReadFiles reads the certificate, its key and the optional bundle of the
// certificates that issued it. It reports false, after logging why, when
// there is no certificate to use:
//
//   - either path is empty (nothing is logged);
//   - the certificate or the key does not exist;
//   - a file cannot be read, the bundle included.
//
// A bundle path that is empty or names nothing that exists is left out.
// The files are not parsed here: Certificate does that.
func ReadFiles(certPath, keyPath, caPath string, logf func(format string, args ...any)) (*Files, bool) {
	if certPath == "" || keyPath == "" {
		return nil, false
	}
	if !exists(certPath) {
		logf("SSL certificate not found: %s", certPath)
		return nil, false
	}
	if !exists(keyPath) {
		logf("SSL private key not found: %s", keyPath)
		return nil, false
	}
	f := &Files{}
	var err error
	if f.Cert, err = os.ReadFile(certPath); err != nil {
		logf("Failed to load SSL certificates: %v", err)
		return nil, false
	}
	if f.Key, err = os.ReadFile(keyPath); err != nil {
		logf("Failed to load SSL certificates: %v", err)
		return nil, false
	}
	if caPath != "" && exists(caPath) {
		if f.CA, err = os.ReadFile(caPath); err != nil {
			logf("Failed to load SSL certificates: %v", err)
			return nil, false
		}
	}
	return f, true
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Certificate parses the files into the certificate the TLS listeners
// present. An error means the files are there but do not hold a
// certificate and its matching key; the console does not start then.
//
// A certificate file that holds only the server's own certificate gets
// the certificates that issued it from the bundle, one after the other up
// to the first that signed itself, so clients receive the whole chain. A
// file that holds a chain is sent as it is. Bundle entries that do not
// parse are skipped.
func (f *Files) Certificate() (*tls.Certificate, error) {
	pair, err := tls.X509KeyPair(f.Cert, f.Key)
	if err != nil {
		return nil, fmt.Errorf("certificate and key: %w", err)
	}
	if len(pair.Certificate) == 1 && len(f.CA) > 0 {
		pair.Certificate = append(pair.Certificate, issuers(pair.Certificate[0], f.CA)...)
	}
	return &pair, nil
}

// maxChain is how many issuers a chain gets at most.
const maxChain = 8

// issuers walks from the certificate der up through the bundle and
// returns the issuers it finds, nearest first.
func issuers(der []byte, bundle []byte) [][]byte {
	cur, err := x509.ParseCertificate(der)
	if err != nil {
		return nil
	}
	var pool []*x509.Certificate
	for rest := bundle; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		if c, err := x509.ParseCertificate(block.Bytes); err == nil {
			pool = append(pool, c)
		}
	}
	var chain [][]byte
	for len(chain) < maxChain && !selfSigned(cur) {
		next := issuerOf(cur, pool)
		if next == nil {
			break
		}
		chain = append(chain, next.Raw)
		cur = next
	}
	return chain
}

func selfSigned(c *x509.Certificate) bool {
	return bytes.Equal(c.RawSubject, c.RawIssuer) && c.CheckSignatureFrom(c) == nil
}

func issuerOf(c *x509.Certificate, pool []*x509.Certificate) *x509.Certificate {
	for _, candidate := range pool {
		if bytes.Equal(candidate.RawSubject, c.RawIssuer) && c.CheckSignatureFrom(candidate) == nil {
			return candidate
		}
	}
	return nil
}

// cipherSuites are the TLS 1.2 suites the old console's TLS listeners
// accepted that Go implements (its OpenSSL default list, checked by
// testdata/node_listeners.json). TLS 1.3 has its three suites on both.
var cipherSuites = []uint16{
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256,
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
	tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
	tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_RSA_WITH_AES_128_CBC_SHA256,
	tls.TLS_RSA_WITH_AES_128_CBC_SHA,
	tls.TLS_RSA_WITH_AES_256_CBC_SHA,
}

// ServerTLS is the TLS setup of the console's listeners: TLS 1.2 and 1.3,
// the suites above, no protocol negotiation (HTTP/1.1 only).
func ServerTLS(cert *tls.Certificate) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{*cert},
		MinVersion:   tls.VersionTLS12,
		CipherSuites: cipherSuites,
	}
}
