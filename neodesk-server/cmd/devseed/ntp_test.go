package main

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// The stand-in answers an NTP client request as an NTP server does: server
// mode, stratum 2, the client's transmit time sent back as the originate
// time, and its clock as receive and transmit time; anything else gets no
// answer (L441).
func TestTheStandInAnswersNTPRequests(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	at := time.Date(2026, 11, 24, 10, 0, 0, 500_000_000, time.UTC)
	done := make(chan error, 1)
	go func() { done <- serveNTP(ctx, conn, func() time.Time { return at }, io.Discard) }()
	defer func() {
		cancel()
		conn.Close()
		<-done
	}()

	client, err := net.Dial("udp", conn.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	// What timesync.QueryNTP sends, with a transmit time to echo
	req := make([]byte, 48)
	req[0] = 0x23
	copy(req[40:48], []byte{1, 2, 3, 4, 5, 6, 7, 8})
	if _, err := client.Write(req); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 64)
	n, err := client.Read(reply)
	if err != nil || n != 48 {
		t.Fatalf("reply: %d bytes, %v", n, err)
	}
	if reply[0]&0x07 != 4 || reply[0]&0x38 != 0x20 || reply[1] != 2 {
		t.Errorf("mode, version or stratum: %#x %d", reply[0], reply[1])
	}
	if string(reply[24:32]) != string(req[40:48]) {
		t.Errorf("originate time %v, want the request's transmit time", reply[24:32])
	}
	for _, off := range []int{32, 40} {
		secs := int64(binary.BigEndian.Uint32(reply[off:off+4])) - 2208988800
		frac := int64(binary.BigEndian.Uint32(reply[off+4 : off+8]))
		got := time.Unix(secs, frac*1e9>>32).UTC()
		if d := got.Sub(at); d < -time.Millisecond || d > time.Millisecond {
			t.Errorf("timestamp at %d: %v, want %v", off, got, at)
		}
	}
	if ntpReply(make([]byte, 47), at) != nil || ntpReply(append([]byte{0x24}, make([]byte, 47)...), at) != nil {
		t.Error("an answer to a short packet or to a server's packet")
	}
}
