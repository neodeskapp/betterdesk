package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
)

// slowLDAP is a directory that accepts everyone after a pause.
func slowLDAP(pause time.Duration) *mockLDAPProvider {
	return &mockLDAPProvider{
		enabled: true,
		fn: func(username, _ string) (*auth.LDAPResult, error) {
			time.Sleep(pause)
			return &auth.LDAPResult{Authenticated: true, Username: username, Role: auth.RoleOperator}, nil
		},
	}
}

// postLogin posts a sign-in to POST /api/auth/login with a deadline, as
// the console's call carries the deadline of its sign-in.
func postLogin(srv *Server, body string, deadline time.Duration) (int, map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body)).WithContext(ctx)
	srv.handleLogin(rec, req)
	var answer map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &answer)
	return rec.Code, answer
}

// TestSlowDirectoryMakesNoAccount: a directory that answers after the
// sign-in's deadline makes no account, and the answer says that the
// directory took too long. The console had told the person "Invalid
// username or password" while the server went on and made the account
// (L336).
func TestSlowDirectoryMakesNoAccount(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()
	srv := newClientLoginTestServer(database)
	srv.ldapProvider = slowLDAP(600 * time.Millisecond)

	status, answer := postLogin(srv, `{"username":"late.ad","password":"ad-pass"}`, 300*time.Millisecond)
	if status != http.StatusGatewayTimeout || answer["code"] != "directory_timeout" {
		t.Errorf("answer %d %v, want 504 directory_timeout", status, answer)
	}
	time.Sleep(700 * time.Millisecond) // past the directory's late answer
	if u, err := database.GetUser("late.ad"); err != nil || u != nil {
		t.Errorf("the late answer made an account: %+v, %v", u, err)
	}
}

// gatedLDAP is a directory that accepts everyone once the test lets it
// answer. It hands the context it is asked in to asked first.
type gatedLDAP struct {
	*mockLDAPProvider
	asked  chan context.Context
	answer chan struct{}
}

func newGatedLDAP() *gatedLDAP {
	return &gatedLDAP{
		mockLDAPProvider: &mockLDAPProvider{enabled: true},
		asked:            make(chan context.Context, 1),
		answer:           make(chan struct{}),
	}
}

func (g *gatedLDAP) AuthenticateContext(ctx context.Context, username, _ string) (*auth.LDAPResult, error) {
	g.asked <- ctx
	<-g.answer
	return &auth.LDAPResult{Authenticated: true, Username: username, Role: auth.RoleOperator}, nil
}

// TestDirectoryInTimeSignsIn: a directory that answers within the
// deadline signs the person in and makes the account. The directory is
// asked with the sign-in's deadline less signInAnswerTime and answers
// while that deadline is still ahead. The deadline is a minute away, so
// the account's password hash (slow under -race) fits on any machine.
func TestDirectoryInTimeSignsIn(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()
	srv := newClientLoginTestServer(database)
	directory := newGatedLDAP()
	srv.ldapProvider = directory

	deadline := time.Now().Add(time.Minute)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"quick.ad","password":"ad-pass"}`)).WithContext(ctx)
	answered := make(chan struct{})
	go func() {
		defer close(answered)
		srv.handleLogin(rec, req)
	}()

	select {
	case dirCtx := <-directory.asked:
		if got, ok := dirCtx.Deadline(); !ok || !got.Equal(deadline.Add(-signInAnswerTime)) {
			t.Errorf("directory asked with the deadline %v (%v), want %v", got, ok, deadline.Add(-signInAnswerTime))
		}
		if err := dirCtx.Err(); err != nil {
			t.Errorf("directory asked after its deadline: %v", err)
		}
		close(directory.answer)
	case <-answered:
		t.Fatalf("answer %d %s before the directory was asked", rec.Code, rec.Body.String())
	}
	<-answered

	var answer map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &answer)
	if rec.Code != http.StatusOK || answer["token"] == nil {
		t.Fatalf("answer %d %v, want a sign-in", rec.Code, answer)
	}
	if u, err := database.GetUser("quick.ad"); err != nil || u == nil {
		t.Errorf("no account: %v", err)
	}
}

// TestDirectoryCheckAnswersTheTimeout: POST /api/auth/ldap/verify says
// that the directory took too long rather than "not authenticated".
func TestDirectoryCheckAnswersTheTimeout(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()
	srv := newClientLoginTestServer(database)
	srv.ldapProvider = slowLDAP(600 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/ldap/verify", strings.NewReader(`{"username":"late.ad","password":"ad-pass"}`)).WithContext(ctx)
	srv.handleLDAPVerifyCredentials(rec, req)
	if rec.Code != http.StatusGatewayTimeout || !strings.Contains(rec.Body.String(), "directory_timeout") {
		t.Errorf("answer %d %s, want 504 directory_timeout", rec.Code, rec.Body.String())
	}
}
