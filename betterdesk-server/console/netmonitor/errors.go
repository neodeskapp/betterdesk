package netmonitor

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The checks report a failure with the text Node's error had, because that
// text is stored with the check and shown to people: "connect ECONNREFUSED
// 10.0.0.5:443", "getaddrinfo ENOTFOUND host", "self-signed certificate".
// The functions here find that text for the errors of Go's network stack.
// An error they do not know keeps Go's own text.

// errnoNames are the names of the system errors a connection can end in.
// The numbers are the codes of Windows sockets for the same errors.
var errnoNames = map[syscall.Errno]string{
	syscall.ECONNREFUSED:  "ECONNREFUSED",
	syscall.ECONNRESET:    "ECONNRESET",
	syscall.ECONNABORTED:  "ECONNABORTED",
	syscall.ETIMEDOUT:     "ETIMEDOUT",
	syscall.EHOSTUNREACH:  "EHOSTUNREACH",
	syscall.ENETUNREACH:   "ENETUNREACH",
	syscall.ENETDOWN:      "ENETDOWN",
	syscall.EADDRNOTAVAIL: "EADDRNOTAVAIL",
	syscall.EADDRINUSE:    "EADDRINUSE",
	syscall.EACCES:        "EACCES",
	syscall.EPERM:         "EPERM",
	syscall.EINVAL:        "EINVAL",
	syscall.EPIPE:         "EPIPE",
	syscall.EMFILE:        "EMFILE",
	syscall.ENOENT:        "ENOENT",
	syscall.EAFNOSUPPORT:  "EAFNOSUPPORT",
	10061:                 "ECONNREFUSED",
	10054:                 "ECONNRESET",
	10053:                 "ECONNABORTED",
	10060:                 "ETIMEDOUT",
	10065:                 "EHOSTUNREACH",
	10051:                 "ENETUNREACH",
	10050:                 "ENETDOWN",
	10049:                 "EADDRNOTAVAIL",
	10047:                 "EAFNOSUPPORT",
}

// refusedAtOnce are the errors the system reports while the connection is
// being started, before anything was sent. Node adds the local address to
// their message.
var refusedAtOnce = map[string]bool{
	"ENETUNREACH":   true,
	"ENETDOWN":      true,
	"EADDRNOTAVAIL": true,
	"EAFNOSUPPORT":  true,
	"EINVAL":        true,
	"EACCES":        true,
	"EPERM":         true,
}

// errnoName returns the name of the system error inside err.
func errnoName(err error) (string, bool) {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return "", false
	}
	name, ok := errnoNames[errno]
	return name, ok
}

// connectMessage is the message of a failed connect to ip and port:
// "connect ECONNREFUSED 10.0.0.5:443". The port is left out when it is 0.
func connectMessage(err error, ip string, port int) string {
	name, ok := errnoName(err)
	if !ok {
		return err.Error()
	}
	message := "connect " + name + " " + ip
	if port > 0 {
		message += ":" + strconv.Itoa(port)
	}
	if refusedAtOnce[name] {
		local := "0.0.0.0:0"
		if strings.Contains(ip, ":") {
			local = ":::0"
		}
		message += " - Local (" + local + ")"
	}
	return message
}

// requestMessage is the message of a failed HTTP request.
func requestMessage(err error) string {
	if errors.Is(err, errEveryAddressFailed) {
		return ""
	}
	var (
		lookup  *lookupError
		connect *connectError
		cert    *certError
	)
	switch {
	case errors.As(err, &lookup):
		return lookup.Error()
	case errors.As(err, &connect):
		return connect.Error()
	case errors.As(err, &cert):
		return cert.message
	}
	var record tls.RecordHeaderError
	if errors.As(err, &record) {
		// The server did not answer with TLS. Node's message goes on with
		// the text of its TLS library, which names a line of its source.
		return "write EPROTO"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if name, ok := errnoName(opErr.Err); ok {
			if opErr.Op == "write" {
				return "write " + name
			}
			return "read " + name
		}
	}
	text := err.Error()
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), strings.Contains(text, "server closed idle connection"):
		return "socket hang up"
	case strings.Contains(text, "malformed HTTP"):
		return "Parse Error: Expected HTTP/, RTSP/ or ICE/"
	}
	return text
}

// chainMessage is the message for a certificate chain that was not
// accepted at the time now.
func chainMessage(err error, certs []*x509.Certificate, now time.Time) string {
	var unknown x509.UnknownAuthorityError
	if errors.As(err, &unknown) {
		leaf, last := certs[0], certs[len(certs)-1]
		switch {
		case selfSigned(leaf):
			return "self-signed certificate"
		case selfSigned(last):
			return "self-signed certificate in certificate chain"
		case len(certs) == 1:
			return "unable to verify the first certificate"
		}
		return "unable to get local issuer certificate"
	}
	var invalid x509.CertificateInvalidError
	if errors.As(err, &invalid) && invalid.Reason == x509.Expired {
		if invalid.Cert != nil && now.Before(invalid.Cert.NotBefore) {
			return "certificate is not yet valid"
		}
		return "certificate has expired"
	}
	return err.Error()
}

func selfSigned(cert *x509.Certificate) bool {
	return string(cert.RawIssuer) == string(cert.RawSubject)
}

// altNamesMessage is the message for a certificate that is not for the
// host that was asked.
func altNamesMessage(cert *x509.Certificate, hostname string) string {
	const prefix = "Hostname/IP does not match certificate's altnames: "
	var names, ips []string
	for _, name := range cert.DNSNames {
		names = append(names, "DNS:"+name)
	}
	for _, ip := range cert.IPAddresses {
		names = append(names, "IP Address:"+ip.String())
		ips = append(ips, ip.String())
	}
	switch {
	case isIP(hostname):
		return prefix + "IP: " + hostname + " is not in the cert's list: " + strings.Join(ips, ", ")
	case len(cert.DNSNames) > 0:
		return prefix + "Host: " + hostname + ". is not in the cert's altnames: " + strings.Join(names, ", ")
	case cert.Subject.CommonName != "":
		return prefix + "Host: " + hostname + ". is not cert's CN: " + cert.Subject.CommonName
	}
	return prefix + "Cert does not contain a DNS name"
}
