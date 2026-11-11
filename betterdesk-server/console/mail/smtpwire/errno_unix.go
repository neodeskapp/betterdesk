//go:build !windows

package smtpwire

import "syscall"

// errnoNames gives the names Node prints for the socket errors of the
// operating system.
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
	syscall.EPIPE:         "EPIPE",
	syscall.EACCES:        "EACCES",
	syscall.EPERM:         "EPERM",
	syscall.EINVAL:        "EINVAL",
	syscall.EAFNOSUPPORT:  "EAFNOSUPPORT",
	syscall.ENOTCONN:      "ENOTCONN",
	syscall.EMFILE:        "EMFILE",
	syscall.ENFILE:        "ENFILE",
	syscall.ENOBUFS:       "ENOBUFS",
}
