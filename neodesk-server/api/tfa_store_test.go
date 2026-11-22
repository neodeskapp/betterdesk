package api

import (
	"fmt"
	"testing"
	"time"
)

// TestTFASessionStoreAtItsCap: a full store of pending second-factor
// sign-ins refuses the next one and keeps working; it used to unlock its
// mutex twice, a fatal error that ended the process (RV184).
func TestTFASessionStoreAtItsCap(t *testing.T) {
	store := newTFASessionStore()
	for i := 0; i < 1000; i++ {
		store.put(fmt.Sprintf("s%d", i), &tfaSession{username: "u", createdAt: time.Now()})
	}
	store.put("one-too-many", &tfaSession{username: "u", createdAt: time.Now()})
	if got := store.take("one-too-many"); got != nil {
		t.Errorf("the store took a session beyond its cap")
	}
	if got := store.take("s7"); got == nil {
		t.Errorf("a stored session is gone")
	}
	store.put("after", &tfaSession{username: "u", createdAt: time.Now()})
	if got := store.take("after"); got == nil {
		t.Errorf("the store refused a session below its cap")
	}
}
