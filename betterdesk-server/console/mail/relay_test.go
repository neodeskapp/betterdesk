package mail

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/mail/smtpwire"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// TestTheConnectionTestReadsNoOtherBanner: a service on the configured
// host and port that greets with something other than an SMTP reply (an
// SSH server here) is answered with a fixed text; the line it sent goes to
// the log only. The relay on localhost is connected to (BUGS RV37, A84).
func TestTheConnectionTestReadsNoOtherBanner(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("SSH-2.0-OpenSSH_9.6p1 internal-box\r\n"))
			time.AfterFunc(2*time.Second, func() { _ = conn.Close() })
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port

	db := storetest.Migrated(t)
	log := &logLines{}
	s := New(Options{DB: db, Transport: &SMTP{}, Logger: log, Getenv: func(string) string { return "" }})
	storeConfig(t, db, `{"host":"127.0.0.1","port":`+strconv.Itoa(port)+`}`)
	got := marshal(t, s.TestSMTPSettings(context.Background()).Body)
	if want := `{"success":false,"error":"` + noSMTPGreeting + `"}`; got != want {
		t.Errorf("the test of an SSH server: %s, want %s", got, want)
	}
	logged := false
	for _, line := range log.lines {
		logged = logged || line[0] == "warn" && strings.Contains(line[1], "SSH-2.0-OpenSSH_9.6p1 internal-box")
	}
	if !logged {
		t.Errorf("the server's line is not in the log: %q", log.lines)
	}
}

// TestNoRelayOnALinkLocalOrMetadataAddress: the SMTP transport connects
// to no link-local address and none of the cloud metadata services, also
// when they are written inside an IPv6 address; loopback, the unspecified
// address and private addresses are connected as before (BUGS RV37, A84).
func TestNoRelayOnALinkLocalOrMetadataAddress(t *testing.T) {
	var dialed []string
	dial := func(_ context.Context, _, address string) (net.Conn, error) {
		dialed = append(dialed, address)
		return nil, errors.New("not connected in the test")
	}
	for host, refused := range map[string]bool{
		"169.254.169.254": true, "169.254.10.20": true, "fe80::1": true, "100.100.100.200": true,
		"fd00:ec2::254": true, "::ffff:169.254.169.254": true,
		"127.0.0.1": false, "::1": false, "0.0.0.0": false, "10.0.0.5": false, "192.168.1.20": false,
		"172.17.0.1": false, "fd12:3456::25": false, "203.0.113.25": false, "::ffff:127.0.0.1": false,
	} {
		dialed = nil
		transport := &SMTP{Wire: smtpwire.Options{Dial: dial, ConnectionTimeout: time.Second}}
		err := transport.Verify(context.Background(), Settings{Host: host, Port: 25})
		if refused {
			if err == nil || err.Error() != errRefusedRelay.Error() || len(dialed) != 0 {
				t.Errorf("%s: %v after dialing %q, want the refusal without a connection", host, err, dialed)
			}
			continue
		}
		if len(dialed) != 1 || err == nil || err.Error() == errRefusedRelay.Error() {
			t.Errorf("%s: %v after dialing %q, want one connection", host, err, dialed)
		}
	}
}
