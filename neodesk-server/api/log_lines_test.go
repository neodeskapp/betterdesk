package api

import (
	"bytes"
	"log"
	"net/http"
	"strings"
	"testing"
)

// captureLog collects what the standard logger writes while f runs.
func captureLog(t *testing.T, f func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev, flags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	t.Cleanup(func() {
		log.SetOutput(prev)
		log.SetFlags(flags)
	})
	f()
	log.SetOutput(prev)
	return buf.String()
}

// TestLogLinesCannotBeForged: text from a client is quoted in the request
// log, the 404 log and a device's status line, so it cannot start a line of
// its own; the sign-in log leaves out the second-factor code, and a
// device's message is logged only in part (RV187).
func TestLogLinesCannotBeForged(t *testing.T) {
	srv, _ := newInternalServer(t, nil)
	out := captureLog(t, func() {
		listenerCall(t, srv, http.MethodGet, "/api/peers/x%0A2026/11/21%2000:00:00%20[audit]%20forged", "", map[string]string{"X-API-Key": testOperatorKey})
		listenerCall(t, srv, http.MethodGet, "/nosuch%0A2026/11/21%2000:00:00%20[audit]%20forged", "", nil)
		listenerCall(t, srv, http.MethodPost, "/api/login", `{"username":"alice","password":"wrong-pass-1","verificationCode":"493817"}`, nil)
	})
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "2026/11/21 00:00:00 [audit] forged") {
			t.Errorf("a client wrote its own log line: %q", line)
		}
	}
	if strings.Contains(out, "493817") {
		t.Errorf("the sign-in log holds the second-factor code:\n%s", out)
	}

	big := bytes.Repeat([]byte("x"), 1<<20)
	excerpt := logExcerpt(append([]byte("line\n"), big...))
	if strings.Contains(excerpt, "\n") || len(excerpt) > 2*maxLoggedPayload {
		t.Errorf("a device message of 1 MiB logs as %d bytes, newline kept: %v", len(excerpt), strings.Contains(excerpt, "\n"))
	}
}
