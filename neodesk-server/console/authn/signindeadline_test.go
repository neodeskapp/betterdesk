package authn

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// directoryServer is a signal server with LDAP on whose sign-in answers
// after a pause: an account the directory accepts, or the directory
// timeout.
type directoryServer struct {
	pause   time.Duration
	timeout bool
}

func (d directoryServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/auth/sso/status":
		_, _ = io.WriteString(w, `{"ldap_enabled":true,"oidc_enabled":false,"any_enabled":true}`)
	case "/api/auth/login":
		time.Sleep(d.pause)
		if d.timeout {
			w.WriteHeader(http.StatusGatewayTimeout)
			_, _ = io.WriteString(w, `{"error":"The directory server took too long to answer","code":"directory_timeout"}`)
			return
		}
		_, _ = io.WriteString(w, `{"token":"t","role":"operator","username":"test-operator","auth_provider":"ldap"}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func directoryService(t *testing.T, d directoryServer) *Service {
	t.Helper()
	db := storetest.Migrated(t)
	// The client's own limit for a call is far below the directory's pause
	s, _, _ := testService(t, db, func(o *Options) {
		o.GoAPI = goapi.New(d, goapi.WithTimeout(80*time.Millisecond))
	})
	return s
}

// TestASlowDirectorySignsIn: a directory that needs longer than the
// console's limit for other calls still signs the person in. The console
// gave up after five seconds while the directory took eight to ten (L336).
func TestASlowDirectorySignsIn(t *testing.T) {
	s := directoryService(t, directoryServer{pause: 400 * time.Millisecond})
	outcome, err := s.Authenticate(context.Background(), "test-operator", "directory-password")
	if err != nil || outcome.User == nil || outcome.IsFailure() {
		t.Fatalf("outcome %+v, %v, want a sign-in", outcome, err)
	}
	if outcome.User.Username != "test-operator" {
		t.Errorf("signed in as %q", outcome.User.Username)
	}
}

// TestADirectoryTimeoutIsSaidAsSuch: when the signal server answers that
// the directory took too long, the sign-in fails with its own code, not
// as a wrong password, and makes no account (L336).
func TestADirectoryTimeoutIsSaidAsSuch(t *testing.T) {
	s := directoryService(t, directoryServer{timeout: true})
	outcome, err := s.Authenticate(context.Background(), "test-operator", "directory-password")
	if err != nil || outcome.Failure != "directory_timeout" {
		t.Fatalf("outcome %+v, %v, want the failure directory_timeout", outcome, err)
	}
	user, err := getUserByUsername(context.Background(), s.db, "test-operator")
	if err != nil || user != nil {
		t.Errorf("the failed sign-in made an account: %+v, %v", user, err)
	}
}

// TestASignInHasOneDeadline: the sign-in's call to the signal server
// carries the deadline of the whole sign-in, so the signal server stops
// the directory before the console stops waiting.
func TestASignInHasOneDeadline(t *testing.T) {
	var left time.Duration
	db := storetest.Migrated(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/sso/status") {
			_, _ = io.WriteString(w, `{"ldap_enabled":true,"oidc_enabled":false,"any_enabled":true}`)
			return
		}
		if deadline, ok := r.Context().Deadline(); ok {
			left = time.Until(deadline)
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"Invalid credentials"}`)
	})
	s, _, _ := testService(t, db, func(o *Options) { o.GoAPI = goapi.New(handler, goapi.WithTimeout(80*time.Millisecond)) })
	if _, err := s.Authenticate(context.Background(), "test-operator", "directory-password"); err != nil {
		t.Fatal(err)
	}
	if left < 20*time.Second || left > 30*time.Second {
		t.Errorf("the call had %v left, want the sign-in's 30 seconds", left)
	}
}
