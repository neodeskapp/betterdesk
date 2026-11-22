package smtpwire

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// certError is a certificate the client refuses. Its text is the one Node
// reports for the same certificate.
type certError struct{ text string }

func (e *certError) Error() string { return e.text }

// tlsConfig is the TLS setup of a session, for implicit TLS and for
// STARTTLS. The server name is sent (SNI) when the host is a name. With
// TLSVerify the chain and the name are checked by verifyPeer; without it
// any certificate passes, which is what tls.rejectUnauthorized=false means.
func (c *client) tlsConfig() *tls.Config {
	config := &tls.Config{
		// crypto/tls would check the name its own way and with its own
		// error texts; verifyPeer does both checks instead
		InsecureSkipVerify: true,
	}
	if ipFamily(c.host) == 0 {
		config.ServerName = c.host
	}
	if c.opts.TLSVerify {
		config.VerifyConnection = func(state tls.ConnectionState) error {
			return verifyPeer(state.PeerCertificates, c.host, c.opts.RootCAs, c.now())
		}
	}
	return config
}

// verifyPeer checks the certificates a server presented: first that they
// chain to a trusted root and are valid now, then that the leaf is for host.
func verifyPeer(certs []*x509.Certificate, host string, roots *x509.CertPool, now time.Time) error {
	if len(certs) == 0 {
		return &certError{"unable to verify the first certificate"}
	}
	if problem := chainProblem(certs, roots, now); problem != "" {
		return &certError{problem}
	}
	if reason := identityProblem(host, namesOf(certs[0])); reason != "" {
		return &certError{"Hostname/IP does not match certificate's altnames: " + reason}
	}
	return nil
}

// chainProblem verifies the chain and returns OpenSSL's text for what is
// wrong with it, "" when it is fine.
func chainProblem(certs []*x509.Certificate, roots *x509.CertPool, now time.Time) string {
	intermediates := x509.NewCertPool()
	for _, cert := range certs[1:] {
		intermediates.AddCert(cert)
	}
	_, err := certs[0].Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if err == nil {
		return ""
	}
	var invalid x509.CertificateInvalidError
	var unknown x509.UnknownAuthorityError
	var noRoots x509.SystemRootsError
	switch {
	case errors.As(err, &invalid):
		switch invalid.Reason {
		case x509.Expired:
			if invalid.Cert != nil && now.Before(invalid.Cert.NotBefore) {
				return "certificate is not yet valid"
			}
			return "certificate has expired"
		case x509.NotAuthorizedToSign:
			return untrustedChainText(certs)
		case x509.IncompatibleUsage:
			return "unsuitable certificate purpose"
		case x509.TooManyIntermediates:
			return "path length constraint exceeded"
		}
	case errors.As(err, &unknown), errors.As(err, &noRoots):
		return untrustedChainText(certs)
	}
	return err.Error()
}

// untrustedChainText says why a chain is not trusted, in the words OpenSSL
// has for it. It follows the issuers among the presented certificates: an
// issuer whose key usage forbids signing certificates or that is no CA is
// named first, otherwise the text says where the chain ends.
func untrustedChainText(certs []*x509.Certificate) string {
	current := certs[0]
	used := map[int]bool{0: true}
	notCA := false
	end := func(text string) string {
		if notCA {
			return "invalid CA certificate"
		}
		return text
	}
	for length := 1; ; length++ {
		if bytes.Equal(current.RawSubject, current.RawIssuer) {
			if length == 1 {
				return end("self-signed certificate")
			}
			return end("self-signed certificate in certificate chain")
		}
		issuer := -1
		for i, candidate := range certs {
			if !used[i] && bytes.Equal(candidate.RawSubject, current.RawIssuer) &&
				candidate.CheckSignature(current.SignatureAlgorithm, current.RawTBSCertificate, current.Signature) == nil {
				issuer = i
				break
			}
		}
		if issuer < 0 {
			if length == 1 {
				return end("unable to verify the first certificate")
			}
			return end("unable to get local issuer certificate")
		}
		used[issuer] = true
		current = certs[issuer]
		if current.KeyUsage != 0 && current.KeyUsage&x509.KeyUsageCertSign == 0 {
			return "key usage does not include certificate signing"
		}
		if !current.BasicConstraintsValid || !current.IsCA {
			notCA = true
		}
	}
}

// certNames are the names of a certificate the identity check looks at.
type certNames struct {
	// altNames is the subjectAltName extension as Node prints it, hasAlt
	// says the certificate has one
	altNames string
	hasAlt   bool
	// dns are its DNS names, ips its IP addresses in canonical text form
	dns []string
	ips []string
	// commonNames are the CN values of the subject
	commonNames []string
}

var (
	oidSubjectAltName = asn1.ObjectIdentifier{2, 5, 29, 17}
	oidCommonName     = asn1.ObjectIdentifier{2, 5, 4, 3}
)

// namesOf reads the names out of a certificate. The alternative names keep
// the order they have in the certificate, because the error text lists them.
func namesOf(cert *x509.Certificate) certNames {
	var names certNames
	for _, attribute := range cert.Subject.Names {
		if attribute.Type.Equal(oidCommonName) {
			if value, ok := attribute.Value.(string); ok {
				names.commonNames = append(names.commonNames, value)
			}
		}
	}
	for _, extension := range cert.Extensions {
		if !extension.Id.Equal(oidSubjectAltName) {
			continue
		}
		var sequence asn1.RawValue
		if _, err := asn1.Unmarshal(extension.Value, &sequence); err != nil {
			continue
		}
		names.hasAlt = true
		var parts []string
		rest := sequence.Bytes
		for len(rest) > 0 {
			var name asn1.RawValue
			var err error
			if rest, err = asn1.Unmarshal(rest, &name); err != nil {
				break
			}
			if name.Class != asn1.ClassContextSpecific {
				continue
			}
			switch name.Tag {
			case 1:
				parts = append(parts, "email:"+altNameText(name.Bytes))
			case 2:
				parts = append(parts, "DNS:"+altNameText(name.Bytes))
				names.dns = append(names.dns, string(name.Bytes))
			case 6:
				parts = append(parts, "URI:"+altNameText(name.Bytes))
			case 7:
				text, canonical := ipAltName(name.Bytes)
				parts = append(parts, "IP Address:"+text)
				if canonical != "" {
					names.ips = append(names.ips, canonical)
				}
			case 8:
				var oid asn1.ObjectIdentifier
				full := append([]byte{asn1.TagOID, byte(len(name.Bytes))}, name.Bytes...)
				if _, err := asn1.Unmarshal(full, &oid); err == nil {
					parts = append(parts, "Registered ID:"+oid.String())
				} else {
					parts = append(parts, "Registered ID:<invalid>")
				}
			case 0:
				parts = append(parts, "othername:<unsupported>")
			case 3:
				parts = append(parts, "X400Name:<unsupported>")
			case 4:
				parts = append(parts, "DirName:<unsupported>")
			case 5:
				parts = append(parts, "EdiPartyName:<unsupported>")
			}
		}
		names.altNames = strings.Join(parts, ", ")
	}
	return names
}

// altNameText prints the value of an alternative name. A value with a
// comma, a quote, a backslash or a character outside printable ASCII is
// quoted like a JSON string, so that the list stays unambiguous.
func altNameText(value []byte) string {
	safe := true
	for _, b := range value {
		if b < ' ' || b > '~' || b == '"' || b == '\\' || b == ',' || b == '\'' {
			safe = false
			break
		}
	}
	if safe {
		return string(value)
	}
	const hex = "0123456789abcdef"
	var out strings.Builder
	out.WriteByte('"')
	for _, b := range value {
		switch {
		case b == '\\':
			out.WriteString(`\\`)
		case b == '"':
			out.WriteString(`\"`)
		case b >= ' ' && b <= '~' && b != ',':
			out.WriteByte(b)
		default:
			out.WriteString(`\u00`)
			out.WriteByte(hex[b>>4])
			out.WriteByte(hex[b&0xF])
		}
	}
	out.WriteByte('"')
	return out.String()
}

// ipAltName prints an IP address of the extension (IPv6 as eight groups in
// upper-case hex) and gives its canonical form for comparing.
func ipAltName(value []byte) (text, canonical string) {
	switch len(value) {
	case 4:
		addr := netip.AddrFrom4([4]byte(value))
		return addr.String(), addr.String()
	case 16:
		groups := make([]string, 8)
		for i := range groups {
			groups[i] = strings.ToUpper(strconv.FormatUint(uint64(value[2*i])<<8|uint64(value[2*i+1]), 16))
		}
		return strings.Join(groups, ":"), netip.AddrFrom16([16]byte(value)).String()
	}
	return fmt.Sprintf("<invalid length=%d>", len(value)), ""
}

// identityProblem says why the certificate is not for host, "" when it is.
//
// An IP address must be among the certificate's IP addresses. A name is
// matched against the DNS names, with a wildcard allowed as (part of) the
// leftmost label of a name with at least three labels; only a certificate
// without DNS names is matched against the common name instead.
func identityProblem(host string, names certNames) string {
	ascii := strings.TrimSuffix(domainToASCII(host), ".")
	host = strings.TrimSuffix(host, ".")
	hostIsIP := ipFamily(host) != 0
	if !hostIsIP && ipFamily(ascii) != 0 {
		return "Host is a non-canonical IP address"
	}
	if hostIsIP {
		canonical := canonicalIP(host)
		for _, ip := range names.ips {
			if ip == canonical {
				return ""
			}
		}
		return "IP: " + host + " is not in the cert's list: " + strings.Join(names.ips, ", ")
	}
	hasCommonName := len(names.commonNames) > 1 || (len(names.commonNames) == 1 && names.commonNames[0] != "")
	if len(names.dns) == 0 && !hasCommonName {
		return "Cert does not contain a DNS name"
	}
	hostParts := splitHost(ascii)
	if len(names.dns) > 0 {
		for _, pattern := range names.dns {
			if matchesName(hostParts, pattern) {
				return ""
			}
		}
		return "Host: " + host + ". is not in the cert's altnames: " + names.altNames
	}
	for _, pattern := range names.commonNames {
		if matchesName(hostParts, pattern) {
			return ""
		}
	}
	return "Host: " + host + ". is not cert's CN: " + strings.Join(names.commonNames, ",")
}

func canonicalIP(host string) string {
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	return addr.WithZone("").String()
}

// splitHost lower-cases the ASCII letters of a name, drops one trailing dot
// and splits it into labels.
func splitHost(host string) []string {
	return strings.Split(lowerASCII(strings.TrimSuffix(host, ".")), ".")
}

// matchesName reports whether the labels of a host match a name of the
// certificate.
func matchesName(hostParts []string, pattern string) bool {
	if pattern == "" {
		return false
	}
	patternParts := splitHost(pattern)
	if len(hostParts) != len(patternParts) {
		return false
	}
	for _, part := range patternParts {
		if part == "" {
			return false
		}
		// names with blanks, control characters or anything outside ASCII
		// never match
		for _, r := range part {
			if r < 0x21 || r > 0x7F {
				return false
			}
		}
	}
	for i := len(hostParts) - 1; i > 0; i-- {
		if hostParts[i] != patternParts[i] {
			return false
		}
	}
	hostLabel, patternLabel := hostParts[0], patternParts[0]
	pieces := strings.Split(patternLabel, "*")
	// a label without a wildcard, or a punycode label, matches literally
	if len(pieces) == 1 || strings.Contains(patternLabel, "xn--") {
		return hostLabel == patternLabel
	}
	// one wildcard at most, and never for a name of two labels (*.tld)
	if len(pieces) > 2 || len(patternParts) <= 2 {
		return false
	}
	prefix, suffix := pieces[0], pieces[1]
	if len(prefix)+len(suffix) > len(hostLabel) {
		return false
	}
	return strings.HasPrefix(hostLabel, prefix) && strings.HasSuffix(hostLabel, suffix)
}

// domainToASCII normalizes a host name the way a URL parser does before the
// certificate check: lower case, and a name whose last label is a number is
// read as an IPv4 address in any of its spellings ("127.1", "0x7f.0.0.1").
// "" means the name is no valid host. Names outside ASCII are not converted
// to punycode here and count as invalid.
func domainToASCII(host string) string {
	for i := 0; i < len(host); i++ {
		b := host[i]
		if b <= ' ' || b >= 0x7F || strings.IndexByte("#/:<>?@[\\]^|%", b) >= 0 {
			return ""
		}
	}
	host = lowerASCII(host)
	labels := strings.Split(host, ".")
	if labels[len(labels)-1] == "" {
		if len(labels) == 1 {
			return ""
		}
		labels = labels[:len(labels)-1]
	}
	last := labels[len(labels)-1]
	if _, ok := ipv4Number(last); !ok && !allDigits(last) {
		return host
	}
	if len(labels) > 4 {
		return ""
	}
	var address uint64
	for i, label := range labels {
		n, ok := ipv4Number(label)
		if !ok {
			return ""
		}
		if i < len(labels)-1 {
			if n > 255 {
				return ""
			}
			address += n << (8 * uint(3-i))
			continue
		}
		if n >= 1<<(8*uint(5-len(labels))) {
			return ""
		}
		address += n
	}
	return netip.AddrFrom4([4]byte{byte(address >> 24), byte(address >> 16), byte(address >> 8), byte(address)}).String()
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

// ipv4Number parses one part of an IPv4 address as a URL parser does:
// decimal, octal with a leading 0, hex with a leading 0x.
func ipv4Number(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	base := 10
	switch {
	case len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X"):
		s, base = s[2:], 16
	case len(s) >= 2 && s[0] == '0':
		s, base = s[1:], 8
	}
	if s == "" {
		return 0, true
	}
	n, err := strconv.ParseUint(s, base, 64)
	if err != nil {
		var numErr *strconv.NumError
		if errors.As(err, &numErr) && errors.Is(numErr.Err, strconv.ErrRange) {
			// too large for any address, but still a number
			return 1 << 40, true
		}
		return 0, false
	}
	return n, true
}
