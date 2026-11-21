package api

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestLDAPTestSendsStoredPasswordOnlyToStoredHost: the LDAP connection test
// fills in the stored bind password for a masked one only when host and
// bind DN are the stored ones; a test against another host or account
// carries no password (RV189).
func TestLDAPTestSendsStoredPasswordOnlyToStoredHost(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	received := make(chan string, 8)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
				data, _ := io.ReadAll(conn)
				received <- string(data)
			}()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	for k, v := range map[string]string{
		"ldap.host":          "127.0.0.1",
		"ldap.port":          fmt.Sprint(port),
		"ldap.bind_dn":       "cn=svc,dc=example,dc=com",
		"ldap.bind_password": "stored-bind-secret",
	} {
		if err := database.SetConfig(k, v); err != nil {
			t.Fatal(err)
		}
	}

	try := func(host, bindDN string) string {
		t.Helper()
		body := fmt.Sprintf(`{"host":%q,"port":%d,"bind_dn":%q,"bind_password":"••••••••","conn_timeout_sec":2}`, host, port, bindDN)
		if status, answer := asConsole(t, srv, http.MethodPost, "/api/auth/ldap/test", body); status != http.StatusOK {
			t.Fatalf("POST /api/auth/ldap/test: %d %s", status, answer)
		}
		select {
		case got := <-received:
			return got
		case <-time.After(3 * time.Second):
			return ""
		}
	}
	if got := try("127.0.0.1", "cn=svc,dc=example,dc=com"); !strings.Contains(got, "stored-bind-secret") {
		t.Errorf("a test of the stored host and bind DN did not use the stored password: %q", got)
	}
	if got := try("localhost", "cn=svc,dc=example,dc=com"); strings.Contains(got, "stored-bind-secret") {
		t.Errorf("a test against another host received the stored password")
	}
	if got := try("127.0.0.1", "cn=other,dc=example,dc=com"); strings.Contains(got, "stored-bind-secret") {
		t.Errorf("a test with another bind DN received the stored password")
	}
}
