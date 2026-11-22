package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"
)

// registerPeer is RendezvousMessage { register_peer (6) { id (1),
// serial (2) = 1 } } in protobuf, encoded by hand. Lengths are one byte,
// which holds for ids shorter than 128 bytes.
func registerPeer(id string) []byte {
	inner := append([]byte{0x0a, byte(len(id))}, id...)
	inner = append(inner, 0x10, 0x01)
	return append([]byte{0x32, byte(len(inner))}, inner...)
}

func runHeartbeats(ctx context.Context, args []string, getenv func(string) string, out io.Writer) error {
	idsFile := "/app/data/dev-online-ids.json"
	if len(args) > 0 && args[0] != "" {
		idsFile = args[0]
	}
	host := getenv("SIGNAL_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := 21116
	if p := getenv("SIGNAL_PORT"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return fmt.Errorf("SIGNAL_PORT %q is not a port", p)
		}
		port = n
	}
	data, err := os.ReadFile(idsFile)
	if err != nil {
		return fmt.Errorf("read the online ids: %w", err)
	}
	var ids []string
	if err := json.Unmarshal(data, &ids); err != nil {
		return fmt.Errorf("read the online ids from %s: %w", idsFile, err)
	}
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return fmt.Errorf("open a UDP socket: %w", err)
	}
	defer conn.Close()
	target := net.JoinHostPort(host, strconv.Itoa(port))
	beat := func() {
		addr, err := net.ResolveUDPAddr("udp4", target)
		if err != nil {
			fmt.Fprintf(out, "resolve %s: %v\n", target, err)
			return
		}
		for _, id := range ids {
			_, _ = conn.WriteToUDP(registerPeer(id), addr)
		}
	}
	beat()
	fmt.Fprintf(out, "Sending heartbeats for %d devices to %s:%d\n", len(ids), host, port)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			beat()
		}
	}
}
