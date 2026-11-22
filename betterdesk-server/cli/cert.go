package cli

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"
)

const certUsage = "Usage: betterdesk-server cert --out DIR [--host NAME]... [--days N] [--force]"

// certDays is how long a certificate of "cert" is valid, on every install.
const certDays = 3650

// hostList collects the repeated --host flag.
type hostList []string

func (h *hostList) String() string { return fmt.Sprint(*h) }

func (h *hostList) Set(v string) error {
	*h = append(*h, v)
	return nil
}

// Cert is "cert --out DIR [--host NAME]... [--days N] [--force]": the
// self-signed certificate of an install, for the console's HTTPS and for
// TLS on the signal and relay ports, so neither installer needs OpenSSL
// (DECISIONS A107). It writes DIR/betterdesk.crt and DIR/betterdesk.key
// (RSA 2048, the key readable by its owner only), valid for the hosts
// given and for localhost and 127.0.0.1, the first host as its common
// name. An existing pair is kept unless --force is given. Exit status: 0
// written, 1 the pair exists or cannot be written, 2 a usage error.
func Cert(args []string, env Env) int {
	fs := flag.NewFlagSet("cert", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	out := fs.String("out", "", "folder for betterdesk.crt and betterdesk.key")
	days := fs.Int("days", certDays, "days the certificate is valid")
	force := fs.Bool("force", false, "replace an existing pair")
	var hosts hostList
	fs.Var(&hosts, "host", "a host name or IP address the certificate is for (repeatable)")
	if err := fs.Parse(args); err != nil || *out == "" || *days < 1 || fs.NArg() > 0 {
		fmt.Fprintln(env.Stderr, certUsage)
		return 2
	}
	crt, key := filepath.Join(*out, "betterdesk.crt"), filepath.Join(*out, "betterdesk.key")
	if !*force {
		for _, p := range []string{crt, key} {
			if _, err := os.Stat(p); err == nil {
				fmt.Fprintf(env.Stderr, "betterdesk-server cert: %s exists; --force replaces it\n", p)
				return 1
			}
		}
	}
	certPEM, keyPEM, notAfter, err := selfSigned(hosts, env.Now(), *days)
	if err != nil {
		fmt.Fprintf(env.Stderr, "betterdesk-server cert: %v\n", err)
		return 1
	}
	if err := os.MkdirAll(*out, 0o750); err != nil {
		fmt.Fprintf(env.Stderr, "betterdesk-server cert: %v\n", err)
		return 1
	}
	if err := writeFileAtomic(key, keyPEM, 0o600); err != nil {
		fmt.Fprintf(env.Stderr, "betterdesk-server cert: %v\n", err)
		return 1
	}
	if err := writeFileAtomic(crt, certPEM, 0o644); err != nil {
		fmt.Fprintf(env.Stderr, "betterdesk-server cert: %v\n", err)
		return 1
	}
	fmt.Fprintf(env.Stdout, "wrote %s and %s, valid until %s\n", crt, key, notAfter.Format("2006-01-02"))
	return 0
}

// selfSigned makes the certificate and its key in PEM.
func selfSigned(hosts []string, now time.Time, days int) (certPEM, keyPEM []byte, notAfter time.Time, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("make the key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("make the serial number: %w", err)
	}
	all := append(slices.Clone(hosts), "localhost", "127.0.0.1")
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: all[0], Organization: []string{"BetterDesk"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(0, 0, days),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, h := range all {
		if ip := net.ParseIP(h); ip != nil {
			if !slices.ContainsFunc(tmpl.IPAddresses, ip.Equal) {
				tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
			}
		} else if h != "" && !slices.Contains(tmpl.DNSNames, h) {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("sign the certificate: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM, tmpl.NotAfter, nil
}

// writeFileAtomic writes data to a file beside path and renames it over
// path, so a reader sees the old file or the whole new one.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr, os.Chmod(name, perm)); err != nil {
		os.Remove(name)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
