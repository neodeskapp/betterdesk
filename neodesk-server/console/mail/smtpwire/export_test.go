package smtpwire

import (
	"crypto/x509"
	"testing"
	"time"
)

// What the end-to-end test of the mail service (e2e_test.go, package
// smtpwire_test) uses of the session tests: the scripted server and the
// test certificates.

// SinkScript is the script of a scripted server.
type SinkScript = sinkScript

// SinkEvent is one thing a scripted server saw the client do.
type SinkEvent = sinkEvent

// Sink is a scripted server.
type Sink = sink

// StartSink starts a scripted server on loopback.
func StartSink(t testing.TB, script SinkScript) *Sink { return startSink(t, script) }

// Addr is where the server listens.
func (s *Sink) Addr() string { return s.addr() }

// Wait returns what the server recorded once the client is gone.
func (s *Sink) Wait(t testing.TB, limit time.Duration) []SinkEvent { return s.wait(t, limit) }

// TrustedRoots is the pool of the test certificates a client trusts.
func TrustedRoots(t testing.TB) *x509.CertPool { return trustedRoots(t) }
