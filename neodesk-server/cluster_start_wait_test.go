package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestTheStartWaitsForValkey: a Valkey that does not answer is asked
// again for a while, then the start stops naming it without its password.
func TestTheStartWaitsForValkey(t *testing.T) {
	old := valkeyStartWait
	valkeyStartWait = 3 * time.Second
	defer func() { valkeyStartWait = old }()
	env := map[string]string{
		"VALKEY_URL":             "redis://default:hunter2-secret@127.0.0.1:1/0",
		"NEODESK_CLUSTER_SECRET": "0123456789abcdef0123",
		"POD_NAME":               "pod-a",
	}
	start := time.Now()
	_, err := dialCluster(context.Background(), func(k string) string { return env[k] })
	if err == nil {
		t.Fatal("dialed a Valkey that is not there")
	}
	if took := time.Since(start); took < 3*time.Second {
		t.Fatalf("gave up after %s, want at least the wait", took)
	}
	if strings.Contains(err.Error(), "hunter2-secret") {
		t.Fatalf("the error shows the password: %v", err)
	}
}
