package toolkit

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ssrfguard"
)

// sslTimeout is how long connecting and the handshake of a certificate
// check may take.
const sslTimeout = 8 * time.Second

type sslData struct {
	Subject      any         `json:"subject"`
	Issuer       any         `json:"issuer"`
	Organization any         `json:"organization"`
	ValidFrom    string      `json:"validFrom"`
	ValidTo      string      `json:"validTo"`
	DaysLeft     int64       `json:"daysLeft"`
	Serial       string      `json:"serial"`
	Fingerprint  string      `json:"fingerprint"`
	AltNames     []string    `json:"altNames"`
	Protocol     string      `json:"protocol"`
	Cipher       cipherInfo  `json:"cipher"`
	Authorized   bool        `json:"authorized"`
	Chain        []chainLink `json:"chain"`
}

type cipherInfo struct {
	Name         string `json:"name"`
	StandardName string `json:"standardName"`
	Version      string `json:"version"`
}

type chainLink struct {
	Subject any    `json:"subject"`
	Issuer  any    `json:"issuer"`
	Serial  string `json:"serial"`
}

// failed is an answer with status 200 that says the check did not work:
// the old route kept the status for everything that went wrong after the
// request itself was accepted.
type failed struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

// cleanTarget cuts a host name or address out of what someone typed or
// pasted: a leading http:// or https://, a path and a port are dropped.
// An IPv6 address is taken whole: out of its brackets, which a port may
// follow, or bare, which no port can follow.
func cleanTarget(text string) string {
	if rest, ok := strings.CutPrefix(text, "https://"); ok {
		text = rest
	} else if rest, ok := strings.CutPrefix(text, "http://"); ok {
		text = rest
	}
	text, _, _ = strings.Cut(text, "/")
	text = strings.TrimFunc(text, isJSSpace)
	if rest, ok := strings.CutPrefix(text, "["); ok {
		if address, _, closed := strings.Cut(rest, "]"); closed {
			return address
		}
	}
	if strings.Count(text, ":") > 1 {
		return text
	}
	text, _, _ = strings.Cut(text, ":")
	return strings.TrimFunc(text, isJSSpace)
}

// ssl answers POST /api/toolkit/ssl with the body {"host":"example.com",
// "port":443}: the certificate the server at that address shows, its
// chain, and the protocol and cipher of the connection. The certificate
// is not required to be valid; "authorized" says whether it is.
//
// The host must pass the guard for an administrator's network tools:
// private addresses are fine, loopback and link-local are not.
func (h *handlers) ssl(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	host, ok := bodyText(body, "host")
	if !ok || host == "" {
		kit.Fail(w, r, http.StatusBadRequest, "Host is required")
		return
	}
	target := cleanTarget(host)
	if target == "" || utf16Len(target) > 253 || strings.ContainsFunc(target, func(c rune) bool {
		return isJSSpace(c) || strings.ContainsRune("<>{}|\\^`", c)
	}) {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid hostname")
		return
	}
	checked, err := h.guard.CheckHost(r.Context(), target, ssrfguard.Options{AllowPrivate: true})
	if err != nil {
		kit.Fail(w, r, http.StatusBadRequest, refusalText(err, "Invalid hostname"))
		return
	}
	port := parseInt(body.Get("port"))
	if math.IsNaN(port) || port == 0 {
		port = 443
	}
	if port < 1 || port > 65535 {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid port")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.sslTimeout)
	defer cancel()
	state, err := h.net.Handshake(ctx, target, checked.Addresses, int(port))
	if err != nil {
		message := err.Error()
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
			message = "Connection timed out"
		}
		kit.JSON(w, r, http.StatusOK, failed{Error: message})
		return
	}
	if len(state.PeerCertificates) == 0 {
		kit.JSON(w, r, http.StatusOK, failed{Error: "No certificate returned"})
		return
	}
	kit.OK(w, r, h.describe(state, target))
}

// refusalText is the guard's message for a refusal and fallback for any
// other error.
func refusalText(err error, fallback string) string {
	var refused *ssrfguard.BlockedError
	if errors.As(err, &refused) {
		return refused.Message
	}
	return fallback
}

// connectError is a failed connection with the message Node gave for it.
type connectError struct{ message string }

func (e *connectError) Error() string { return e.message }

// handshake connects to port of the addresses the guard checked for host,
// trying each in turn, and runs a TLS handshake that accepts any
// certificate. The name is not looked up again: a second answer could
// name an address the guard never saw.
//
// The messages are Node's where Node's had a fixed form:
//
//	one address, not reachable      connect ECONNREFUSED <address>:<port>
//	several addresses, none works   "" (Node's collected error has no text)
//	the server hangs up or resets   Client network socket disconnected before secure TLS connection was established
func (h *handlers) handshake(ctx context.Context, host string, addresses []string, port int) (*tls.ConnectionState, error) {
	var conn net.Conn
	for _, address := range addresses {
		ip, _, _ := strings.Cut(address, "%")
		c, err := h.net.Dial(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
		if err == nil {
			conn = c
			break
		}
		if ctx.Err() != nil {
			return nil, context.DeadlineExceeded
		}
		if len(addresses) == 1 {
			return nil, &connectError{"connect " + errnoName(err) + " " + address + ":" + strconv.Itoa(port)}
		}
	}
	if conn == nil {
		return nil, &connectError{""}
	}
	defer conn.Close()
	config := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12} // the certificate is what gets inspected, valid or not
	if ssrfguard.IsIP(host) == 0 {
		config.ServerName = host
	}
	client := tls.Client(conn, config)
	if err := client.HandshakeContext(ctx); err != nil {
		switch {
		case ctx.Err() != nil:
			return nil, context.DeadlineExceeded
		case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE):
			return nil, &connectError{"Client network socket disconnected before secure TLS connection was established"}
		}
		return nil, err
	}
	state := client.ConnectionState()
	return &state, nil
}

// errnoName is the name Node prints for a failed connect.
func errnoName(err error) string {
	names := []struct {
		errno syscall.Errno
		name  string
	}{
		{syscall.ECONNREFUSED, "ECONNREFUSED"}, {syscall.EHOSTUNREACH, "EHOSTUNREACH"}, {syscall.ENETUNREACH, "ENETUNREACH"},
		{syscall.ETIMEDOUT, "ETIMEDOUT"}, {syscall.ECONNRESET, "ECONNRESET"}, {syscall.EADDRNOTAVAIL, "EADDRNOTAVAIL"},
		{syscall.EACCES, "EACCES"}, {syscall.EINVAL, "EINVAL"}, {syscall.EAFNOSUPPORT, "EAFNOSUPPORT"},
	}
	for _, n := range names {
		if errors.Is(err, n.errno) {
			return n.name
		}
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "ETIMEDOUT"
	}
	return "ECONNREFUSED"
}

// describe turns a finished handshake into the answer.
func (h *handlers) describe(state *tls.ConnectionState, host string) sslData {
	leaf := state.PeerCertificates[0]
	now := h.now()

	intermediates := x509.NewCertPool()
	for _, cert := range state.PeerCertificates[1:] {
		intermediates.AddCert(cert)
	}
	_, verifyErr := leaf.Verify(x509.VerifyOptions{DNSName: host, Intermediates: intermediates, Roots: h.net.Roots, CurrentTime: now})

	data := sslData{
		Subject:      firstOf(nameValue(leaf.Subject, oidCommonName), nameValue(leaf.Subject, oidOrganization)),
		Issuer:       firstOf(nameValue(leaf.Issuer, oidCommonName), nameValue(leaf.Issuer, oidOrganization)),
		Organization: firstOf(nameValue(leaf.Issuer, oidOrganization)),
		ValidFrom:    leaf.NotBefore.UTC().Format("2006-01-02T15:04:05.000Z"),
		ValidTo:      leaf.NotAfter.UTC().Format("2006-01-02T15:04:05.000Z"),
		DaysLeft:     int64(math.Floor(float64(leaf.NotAfter.UnixMilli()-now.UnixMilli()) / 86400000)),
		Serial:       serialText(leaf),
		Fingerprint:  fingerprint(leaf),
		AltNames:     altNames(leaf),
		Protocol:     protocolName(state.Version),
		Cipher:       cipherOf(state.CipherSuite),
		Authorized:   verifyErr == nil,
		Chain:        []chainLink{},
	}

	// Every certificate of the chain gets a line, the last one also when
	// its issuer is unknown
	seen := map[string]bool{}
	for _, cert := range issuerChain(state.PeerCertificates, h.net.Roots) {
		print := fingerprint(cert)
		if seen[print] {
			break
		}
		seen[print] = true
		data.Chain = append(data.Chain, chainLink{
			Subject: firstOf(nameValue(cert.Subject, oidCommonName)),
			Issuer:  firstOf(nameValue(cert.Issuer, oidCommonName), nameValue(cert.Issuer, oidOrganization)),
			Serial:  serialText(cert),
		})
	}
	return data
}

var (
	oidCommonName   = asn1.ObjectIdentifier{2, 5, 4, 3}
	oidOrganization = asn1.ObjectIdentifier{2, 5, 4, 10}
)

// nameValue is name.CN or name.O as Node's certificate object had it:
// missing, one text, or a list when the name has the attribute more than
// once.
func nameValue(name pkix.Name, oid asn1.ObjectIdentifier) any {
	var values []string
	for _, attribute := range name.Names {
		if attribute.Type.Equal(oid) {
			values = append(values, fmt.Sprint(attribute.Value))
		}
	}
	switch len(values) {
	case 0:
		return nil
	case 1:
		return values[0]
	}
	return values
}

// firstOf is a || b || ”: the first value that is there and not an
// empty text.
func firstOf(values ...any) any {
	for _, v := range values {
		if v != nil && v != "" {
			return v
		}
	}
	return ""
}

// serialText is the serial number in upper-case hex, whole bytes.
func serialText(cert *x509.Certificate) string {
	n := cert.SerialNumber
	if n == nil || n.Sign() == 0 {
		return "0"
	}
	text := fmt.Sprintf("%X", n.Bytes())
	if n.Sign() < 0 {
		return "-" + text
	}
	return text
}

// fingerprint is the SHA-256 of the certificate as AB:CD:...
func fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

func protocolName(version uint16) string {
	switch version {
	case tls.VersionTLS13:
		return "TLSv1.3"
	case tls.VersionTLS12:
		return "TLSv1.2"
	case tls.VersionTLS11:
		return "TLSv1.1"
	case tls.VersionTLS10:
		return "TLSv1"
	}
	return ""
}

// opensslCiphers gives the cipher suites of TLS 1.2 and older the name
// OpenSSL has for them and the protocol version it files them under.
var opensslCiphers = map[uint16][2]string{
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256:         {"ECDHE-RSA-AES128-GCM-SHA256", "TLSv1.2"},
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384:         {"ECDHE-RSA-AES256-GCM-SHA384", "TLSv1.2"},
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256:       {"ECDHE-ECDSA-AES128-GCM-SHA256", "TLSv1.2"},
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384:       {"ECDHE-ECDSA-AES256-GCM-SHA384", "TLSv1.2"},
	tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256:   {"ECDHE-RSA-CHACHA20-POLY1305", "TLSv1.2"},
	tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256: {"ECDHE-ECDSA-CHACHA20-POLY1305", "TLSv1.2"},
	tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256:         {"ECDHE-RSA-AES128-SHA256", "TLSv1.2"},
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256:       {"ECDHE-ECDSA-AES128-SHA256", "TLSv1.2"},
	tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA:            {"ECDHE-RSA-AES128-SHA", "TLSv1.0"},
	tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA:            {"ECDHE-RSA-AES256-SHA", "TLSv1.0"},
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA:          {"ECDHE-ECDSA-AES128-SHA", "TLSv1.0"},
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA:          {"ECDHE-ECDSA-AES256-SHA", "TLSv1.0"},
	tls.TLS_RSA_WITH_AES_128_GCM_SHA256:               {"AES128-GCM-SHA256", "TLSv1.2"},
	tls.TLS_RSA_WITH_AES_256_GCM_SHA384:               {"AES256-GCM-SHA384", "TLSv1.2"},
	tls.TLS_RSA_WITH_AES_128_CBC_SHA256:               {"AES128-SHA256", "TLSv1.2"},
	tls.TLS_RSA_WITH_AES_128_CBC_SHA:                  {"AES128-SHA", "SSLv3"},
	tls.TLS_RSA_WITH_AES_256_CBC_SHA:                  {"AES256-SHA", "SSLv3"},
	tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA:                 {"DES-CBC3-SHA", "SSLv3"},
	tls.TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA:           {"ECDHE-RSA-DES-CBC3-SHA", "TLSv1.0"},
}

// cipherOf is socket.getCipher(): OpenSSL's name of the suite, its
// standard name, and the protocol version the suite came with.
func cipherOf(suite uint16) cipherInfo {
	standard := tls.CipherSuiteName(suite)
	if old, ok := opensslCiphers[suite]; ok {
		return cipherInfo{Name: old[0], StandardName: standard, Version: old[1]}
	}
	return cipherInfo{Name: standard, StandardName: standard, Version: "TLSv1.3"}
}

// issued reports whether issuer can have signed subject, going by names
// and key identifiers like the old check did. Signatures and dates are
// not looked at.
func issued(issuer, subject *x509.Certificate) bool {
	if string(issuer.RawSubject) != string(subject.RawIssuer) {
		return false
	}
	if len(subject.AuthorityKeyId) > 0 && len(issuer.SubjectKeyId) > 0 && string(subject.AuthorityKeyId) != string(issuer.SubjectKeyId) {
		return false
	}
	return issuer.KeyUsage == 0 || issuer.KeyUsage&x509.KeyUsageCertSign != 0
}

// issuerChain orders the certificates a server sent, in whatever order it
// sent them: the server's own, then for each the first of the others that
// issued it. When the last one is not its own issuer, the trusted roots
// are asked for the rest.
func issuerChain(sent []*x509.Certificate, roots *x509.CertPool) []*x509.Certificate {
	current := sent[0]
	chain := []*x509.Certificate{current}
	rest := append([]*x509.Certificate(nil), sent[1:]...)
	for {
		found := -1
		for i, candidate := range rest {
			if issued(candidate, current) {
				found = i
				break
			}
		}
		if found < 0 {
			break
		}
		current = rest[found]
		chain = append(chain, current)
		rest = append(rest[:found], rest[found+1:]...)
	}
	if !issued(current, current) {
		// Dates are left out of this lookup too: it runs at a moment the
		// certificate was valid
		paths, err := current.Verify(x509.VerifyOptions{
			Roots: roots, Intermediates: x509.NewCertPool(), CurrentTime: current.NotBefore.Add(time.Second),
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
		})
		if err == nil && len(paths) > 0 && len(paths[0]) > 1 {
			chain = append(chain, paths[0][1:]...)
		}
	}
	return chain
}

var oidSubjectAltName = asn1.ObjectIdentifier{2, 5, 29, 17}

// altNames is cert.subjectaltname.split(', ') with the first "DNS:" of
// each name removed: the names of the certificate in the order it lists
// them, as "example.com", "IP Address:10.0.0.1", "email:a@b",
// "URI:https://x". A name with a comma, a quote or a character outside
// printable ASCII is written in quotes with escapes, and the comma then
// splits it like the old code did.
func altNames(cert *x509.Certificate) []string {
	names := []string{}
	var raw []byte
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(oidSubjectAltName) {
			raw = ext.Value
		}
	}
	if raw == nil {
		return names
	}
	var sequence asn1.RawValue
	if _, err := asn1.Unmarshal(raw, &sequence); err != nil || sequence.Tag != asn1.TagSequence {
		return names
	}
	var printed []string
	for rest := sequence.Bytes; len(rest) > 0; {
		var name asn1.RawValue
		var err error
		if rest, err = asn1.Unmarshal(rest, &name); err != nil {
			break
		}
		if name.Class != asn1.ClassContextSpecific {
			continue
		}
		switch name.Tag {
		case 0:
			printed = append(printed, "othername:<unsupported>")
		case 1:
			printed = append(printed, "email:"+altNameText(name.Bytes, false))
		case 2:
			printed = append(printed, "DNS:"+altNameText(name.Bytes, false))
		case 3:
			printed = append(printed, "X400Name:<unsupported>")
		case 4:
			printed = append(printed, "DirName:"+altNameText([]byte(directoryName(name.Bytes)), true))
		case 5:
			printed = append(printed, "EdiPartyName:<unsupported>")
		case 6:
			printed = append(printed, "URI:"+altNameText(name.Bytes, false))
		case 7:
			printed = append(printed, "IP Address:"+altNameIP(name.Bytes))
		case 8:
			var oid asn1.ObjectIdentifier
			if _, err := asn1.Unmarshal(append([]byte{asn1.TagOID, byte(len(name.Bytes))}, name.Bytes...), &oid); err == nil {
				printed = append(printed, "Registered ID:"+oid.String())
			}
		}
	}
	if len(printed) == 0 {
		return names
	}
	for _, part := range strings.Split(decodeUTF8([]byte(strings.Join(printed, ", "))), ", ") {
		names = append(names, strings.Replace(part, "DNS:", "", 1))
	}
	return names
}

// altNameText writes a name as it is when that cannot be misread, and in
// quotes with escapes otherwise.
func altNameText(name []byte, utf8Text bool) string {
	safe := true
	for _, c := range name {
		switch {
		case c == '"' || c == '\\' || c == ',' || c == '\'':
			safe = false
		case utf8Text:
			safe = safe && c >= ' ' && c != 0x7F
		default:
			safe = safe && c >= ' ' && c <= '~'
		}
	}
	if safe {
		return string(name)
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, c := range name {
		switch {
		case c == '\\':
			b.WriteString(`\\`)
		case c == '"':
			b.WriteString(`\"`)
		case (c >= ' ' && c != ',' && c <= '~') || (utf8Text && c&0x80 != 0):
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, `\u%04x`, c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func altNameIP(ip []byte) string {
	switch len(ip) {
	case 4:
		return fmt.Sprintf("%d.%d.%d.%d", ip[0], ip[1], ip[2], ip[3])
	case 16:
		groups := make([]string, 8)
		for i := range groups {
			groups[i] = fmt.Sprintf("%X", uint16(ip[2*i])<<8|uint16(ip[2*i+1]))
		}
		return strings.Join(groups, ":")
	}
	return fmt.Sprintf("<invalid length=%d>", len(ip))
}

// directoryName writes a directory name the short way, last part first:
// CN=x,O=y,C=z.
func directoryName(der []byte) string {
	var rdns pkix.RDNSequence
	if _, err := asn1.Unmarshal(der, &rdns); err != nil {
		return ""
	}
	short := map[string]string{"2.5.4.3": "CN", "2.5.4.6": "C", "2.5.4.7": "L", "2.5.4.8": "ST", "2.5.4.10": "O", "2.5.4.11": "OU", "0.9.2342.19200300.100.1.25": "DC", "0.9.2342.19200300.100.1.1": "UID"}
	var parts []string
	for i := len(rdns) - 1; i >= 0; i-- {
		var attributes []string
		for _, attribute := range rdns[i] {
			name := attribute.Type.String()
			if s, ok := short[name]; ok {
				name = s
			}
			attributes = append(attributes, name+"="+fmt.Sprint(attribute.Value))
		}
		parts = append(parts, strings.Join(attributes, "+"))
	}
	return strings.Join(parts, ",")
}
