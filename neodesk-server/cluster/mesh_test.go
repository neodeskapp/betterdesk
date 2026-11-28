package cluster

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func testValkey(t *testing.T) *Valkey {
	t.Helper()
	url := os.Getenv("NEODESK_TEST_VALKEY_URL")
	if url == "" {
		if os.Getenv("NEODESK_TEST_REQUIRE_VALKEY") == "1" {
			t.Fatal("NEODESK_TEST_VALKEY_URL not set")
		}
		t.Skip("NEODESK_TEST_VALKEY_URL not set")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { client.Close() })
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return NewForTest(client, "t"+hex.EncodeToString(b)+":")
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func startMesh(t *testing.T, v *Valkey, name, secret string) *Mesh {
	t.Helper()
	port := freePort(t)
	m, err := NewMesh(Config{Name: name, PodIP: "127.0.0.1", Port: port, Secret: secret}, v)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(port); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	return m
}

func TestMeshCallsAndStreamsBetweenPods(t *testing.T) {
	v := testValkey(t)
	const secret = "0123456789abcdef0123456789abcdef"
	a := startMesh(t, v, "pod-a", secret)
	b := startMesh(t, v, "pod-b", secret)
	b.Handle("echo", func(_ context.Context, from string, body []byte) ([]byte, error) {
		return append([]byte(from+":"), body...), nil
	})
	got := make(chan string, 1)
	b.HandleStream("pipe", func(from string, meta map[string]string, conn net.Conn) {
		defer conn.Close()
		buf := make([]byte, 5)
		_, _ = io.ReadFull(conn, buf)
		got <- from + " " + meta["uuid"] + " " + string(buf)
		_, _ = conn.Write([]byte("world"))
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := a.Call(ctx, "pod-b", "echo", []byte("hi"))
	if err != nil || string(out) != "pod-a:hi" {
		t.Fatalf("call = %q, %v", out, err)
	}

	conn, err := a.Stream(ctx, "pod-b", "pipe", map[string]string{"uuid": "u-1"})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("hello"))
	reply := make([]byte, 5)
	if _, err := io.ReadFull(conn, reply); err != nil || string(reply) != "world" {
		t.Fatalf("stream reply %q, %v", reply, err)
	}
	if s := <-got; s != "pod-a u-1 hello" {
		t.Fatalf("stream handler saw %q", s)
	}

	pods, err := a.Pods(ctx)
	if err != nil || len(pods) != 2 {
		t.Fatalf("pods %v, %v", pods, err)
	}
	if _, err := a.Call(ctx, "pod-gone", "echo", nil); !errors.Is(err, ErrNoPod) {
		t.Fatalf("call to an unknown pod: %v", err)
	}
}

// TestMeshRefusesCallsSignedWithAnotherSecret: a pod with another secret
// (or anyone on the pod network without it) gets nothing through.
func TestMeshRefusesCallsSignedWithAnotherSecret(t *testing.T) {
	v := testValkey(t)
	a := startMesh(t, v, "pod-a", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	b := startMesh(t, v, "pod-b", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	called := false
	b.Handle("echo", func(context.Context, string, []byte) ([]byte, error) { called = true; return nil, nil })
	b.HandleStream("pipe", func(_ string, _ map[string]string, c net.Conn) { called = true; c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := a.Call(ctx, "pod-b", "echo", []byte("x")); err == nil {
		t.Fatal("call with the wrong secret went through")
	}
	if _, err := a.Stream(ctx, "pod-b", "pipe", nil); err == nil {
		t.Fatal("stream with the wrong secret went through")
	}
	if called {
		t.Fatal("a handler ran for a refused request")
	}
}

func TestMeshNeedsASecret(t *testing.T) {
	if _, err := NewMesh(Config{Name: "x", PodIP: "127.0.0.1"}, NewForTest(nil, "p:")); err == nil {
		t.Fatal("mesh without a secret")
	}
}
