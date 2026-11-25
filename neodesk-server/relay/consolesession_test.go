package relay

import "testing"

// TestTheLiveSessionNamesTheConsoleSession: a ticket signal issues for a
// session the console's web client opened names the console session
// behind it (a handle, A239), the live session carries it, and a retry of
// the ticket must name the same one: two browsers of one account are two
// console sessions, and saved sign-in binds its one use to the one that
// opened the relay session.
func TestTheLiveSessionNamesTheConsoleSession(t *testing.T) {
	r := NewAuthorizationRegistry()
	if !r.AuthorizeForSession("uuid-1", "panel-web-remote", "DEV1", 5, "handle-a") {
		t.Fatal("authorize")
	}
	if r.AuthorizeForSession("uuid-1", "panel-web-remote", "DEV1", 5, "handle-b") {
		t.Error("a retry from another console session of the account was accepted")
	}
	if !r.AuthorizeForSession("uuid-1", "panel-web-remote", "DEV1", 5, "handle-a") {
		t.Error("the retry of the same console session was refused")
	}
	if !r.Claim("uuid-1") || !r.Claim("uuid-1") {
		t.Fatal("claim")
	}
	pair, ok := r.Live("uuid-1")
	if !ok || pair.Account != 5 || pair.ConsoleSession != "handle-a" {
		t.Errorf("live: %+v %v", pair, ok)
	}
	r.End("uuid-1")
	if _, ok := r.Live("uuid-1"); ok {
		t.Error("the session is live after its end")
	}
}
