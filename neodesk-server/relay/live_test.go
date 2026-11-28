package relay

import (
	"testing"
	"time"
)

// TestLiveNamesThePairAndItsAccountWhileTheSessionRuns: a relay session is
// live from the second claim of its ticket until it ends, with the
// initiator, the target and the console account signal named for it. A
// ticket with one claim, an ended session and an unknown uuid are not
// live (saved sign-in binds its one use to a live session, L469).
func TestLiveNamesThePairAndItsAccountWhileTheSessionRuns(t *testing.T) {
	registry := NewAuthorizationRegistry()
	now := time.Unix(1_700_000_000, 0)
	registry.now = func() time.Time { return now }

	if !registry.AuthorizeFor("live-uuid", "panel-web-remote", "TARGET1", 7) {
		t.Fatal("expected ticket authorization")
	}
	// A retry of the ticket names the same account; another account's is refused
	if !registry.AuthorizeFor("live-uuid", "panel-web-remote", "TARGET1", 7) {
		t.Fatal("a retry by the same account is refused")
	}
	if registry.AuthorizeFor("live-uuid", "panel-web-remote", "TARGET1", 8) || registry.AuthorizeFor("live-uuid", "panel-web-remote", "TARGET1", 0) {
		t.Fatal("another account takes over the ticket")
	}
	if _, ok := registry.Live("live-uuid"); ok {
		t.Fatal("an authorized ticket nobody claimed is not live")
	}
	if !registry.Claim("live-uuid") {
		t.Fatal("first claim")
	}
	if _, ok := registry.Live("live-uuid"); ok {
		t.Fatal("a ticket with one side connected is not live")
	}
	if !registry.Claim("live-uuid") {
		t.Fatal("second claim")
	}
	pair, ok := registry.Live("live-uuid")
	if !ok || pair.Initiator != "panel-web-remote" || pair.Target != "TARGET1" || pair.Account != 7 {
		t.Fatalf("live pair %+v %v, want panel-web-remote -> TARGET1 for account 7", pair, ok)
	}
	// The pair says when its second side arrived: quick support vouches
	// for a caller only in the first minute (A271)
	if !pair.Since.Equal(now) {
		t.Fatalf("live since %v, want %v", pair.Since, now)
	}
	// The ticket's own expiry does not end a session that runs
	now = now.Add(time.Hour)
	if _, ok := registry.Live("live-uuid"); !ok {
		t.Fatal("a running session stays live after its ticket expired")
	}
	registry.End("live-uuid")
	if _, ok := registry.Live("live-uuid"); ok {
		t.Fatal("an ended session is not live")
	}
	if _, ok := registry.Live("never-seen"); ok {
		t.Fatal("an unknown uuid is not live")
	}

	// A ticket authorized without an account (a RustDesk app) is live with account 0
	if !registry.AuthorizeFor("plain-uuid", "INIT01", "TARGET1", 0) || !registry.Claim("plain-uuid") || !registry.Claim("plain-uuid") {
		t.Fatal("plain ticket")
	}
	if pair, ok := registry.Live("plain-uuid"); !ok || pair.Account != 0 {
		t.Fatalf("plain pair %+v %v", pair, ok)
	}
	// A session that never ended is forgotten after liveMaxAge
	now = now.Add(liveMaxAge + time.Minute)
	if _, ok := registry.Live("plain-uuid"); ok {
		t.Fatal("a live entry older than liveMaxAge is pruned")
	}
}
