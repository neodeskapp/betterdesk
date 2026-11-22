//go:build windows

package smtpwire

import "syscall"

// errnoNames gives the names Node prints for the socket errors of the
// operating system. On Windows these are Winsock codes.
var errnoNames = map[syscall.Errno]string{
	10061: "ECONNREFUSED",
	10054: "ECONNRESET",
	10053: "ECONNABORTED",
	10060: "ETIMEDOUT",
	10065: "EHOSTUNREACH",
	10051: "ENETUNREACH",
	10050: "ENETDOWN",
	10049: "EADDRNOTAVAIL",
	10048: "EADDRINUSE",
	10058: "EPIPE",
	10013: "EACCES",
	10022: "EINVAL",
	10047: "EAFNOSUPPORT",
	10057: "ENOTCONN",
	10024: "EMFILE",
	10055: "ENOBUFS",
}
