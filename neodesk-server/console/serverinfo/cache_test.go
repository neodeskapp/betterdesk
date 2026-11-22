package serverinfo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The key the server reports is kept for 30 seconds: a request at 29.999
// seconds is answered from memory, one at 30 seconds asks again. An answer
// without a valid key is not kept, and a valid key file always wins.
func TestResolvePublicKeyCache(t *testing.T) {
	first := base64.StdEncoding.EncodeToString(make([]byte, 32))
	second := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	fileKey := base64.StdEncoding.EncodeToString([]byte("ffffffffffffffffffffffffffffffff"))
	answer := func(key string) *apiAnswer {
		data, _ := json.Marshal(map[string]string{"key": key})
		return &apiAnswer{Data: data}
	}

	pubKey := filepath.Join(t.TempDir(), "id_ed25519.pub")
	api := &stubAPI{}
	clock := time.UnixMilli(1_700_000_000_000)
	s := New(Options{PubKeyPath: pubKey, GoAPI: api.client(), Now: func() time.Time { return clock }, Logger: &recordingLogger{}})
	ctx := context.Background()

	step := func(name string, advance time.Duration, serverKey, want string, wantCalls int) {
		t.Helper()
		clock = clock.Add(advance)
		api.set("GET", answer(serverKey))
		if got := string(s.ResolvePublicKey(ctx)); got != want {
			t.Errorf("%s: key %q, want %q", name, got, want)
		}
		if calls := len(api.seen()); calls != wantCalls {
			t.Errorf("%s: %d calls to the server, want %d", name, calls, wantCalls)
		}
	}

	step("first call", 0, first, first, 1)
	step("kept answer", 29999*time.Millisecond, second, first, 0)
	step("30 seconds after the fetch", time.Millisecond, second, second, 1)
	step("an answer without a key", 30*time.Second, "not a key", "", 1)
	step("a failure is not kept", 0, first, first, 1)

	if err := os.WriteFile(pubKey, []byte(fileKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	step("the key file wins", 0, second, fileKey, 0)
	if err := os.Remove(pubKey); err != nil {
		t.Fatal(err)
	}
	step("the kept answer again", 0, second, first, 0)

	// Without the server's API there is no key and no failure.
	alone := New(Options{PubKeyPath: pubKey, Logger: &recordingLogger{}})
	if got := alone.ResolvePublicKey(ctx); got != "" {
		t.Errorf("key %q without a key file and without the server", got)
	}
}
