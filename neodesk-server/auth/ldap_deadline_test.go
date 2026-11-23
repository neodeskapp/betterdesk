package auth

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// silentDirectory accepts connections and never answers.
func silentDirectory(t *testing.T) *LDAPConfig {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	return &LDAPConfig{
		Enabled: true, Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port,
		BindDN: "cn=svc,dc=corp,dc=example", BindPassword: "svc", BaseDN: "dc=corp,dc=example",
		UserFilter: "(uid={{username}})", ConnTimeoutSec: 10,
	}
}

// TestADirectoryStopsAtTheSignInsDeadline: a directory that does not
// answer is given up at the sign-in's deadline, not after the connection
// timeout of every request (L336).
func TestADirectoryStopsAtTheSignInsDeadline(t *testing.T) {
	p := NewLDAPProvider(silentDirectory(t))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	result, err := p.AuthenticateContext(ctx, "alice", "secret")
	if !errors.Is(err, context.DeadlineExceeded) || result != nil {
		t.Errorf("answer %+v, %v, want the deadline", result, err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("gave up after %v", took)
	}
}

// TestADirectoryInTimeStillSignsIn: with a deadline, a directory that
// answers signs the person in as before.
func TestADirectoryInTimeStillSignsIn(t *testing.T) {
	dir := newFakeDirectory(t, aliceEntry)
	cfg := dir.config()
	cfg.DirectBind = true
	cfg.DirectBindDN = "uid={{username}},ou=people,dc=corp,dc=example"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := NewLDAPProvider(cfg).AuthenticateContext(ctx, "alice", "secret")
	if err != nil || result == nil || !result.Authenticated {
		t.Fatalf("answer %+v, %v", result, err)
	}
}
