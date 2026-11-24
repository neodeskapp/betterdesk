package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

// ntp: the test stack's stand-in time source. The stack points the server's
// NTP name (pool.ntp.org) at this machine and runs "devseed ntp", which
// answers each NTP client request with this machine's clock, until stopped.
// So the time check of a test instance never depends on reaching an NTP
// server on the internet, and its answers are the same everywhere (L441).
// Flag --listen (default 127.0.0.1:123).
func runNTP(ctx context.Context, args []string, out io.Writer) error {
	f, err := parseFlags(args, []string{"listen"}, nil)
	if err != nil {
		return err
	}
	conn, err := net.ListenPacket("udp", f.get("listen", "127.0.0.1:123"))
	if err != nil {
		return fmt.Errorf("listen for NTP: %w", err)
	}
	defer conn.Close()
	fmt.Fprintf(out, "ntp: answering on %s with this machine's clock\n", conn.LocalAddr())
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	return serveNTP(ctx, conn, time.Now, out)
}

// serveNTP answers the requests on conn until ctx ends or conn closes,
// with a line on out for each answer.
func serveNTP(ctx context.Context, conn net.PacketConn, now func() time.Time, out io.Writer) error {
	buf := make([]byte, 512)
	for {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read an NTP request: %w", err)
		}
		at := now()
		if reply := ntpReply(buf[:n], at); reply != nil {
			conn.WriteTo(reply, from)
			fmt.Fprintf(out, "ntp: answered %s with %s\n", from, at.UTC().Format(time.RFC3339Nano))
		}
	}
}

// ntpReply is the server's answer (mode 4, stratum 2) to an NTP client
// request (mode 3), with received at as receive and transmit time; nil
// for anything that is no client request.
func ntpReply(req []byte, at time.Time) []byte {
	if len(req) < 48 || req[0]&0x07 != 3 {
		return nil
	}
	reply := make([]byte, 48)
	reply[0] = req[0]&0x38 | 4 // the request's version, server mode
	reply[1] = 2               // stratum: synchronised to a stratum 1 server
	reply[2] = req[2]          // poll
	reply[3] = 0xEC            // precision about one microsecond
	copy(reply[12:16], net.IPv4(127, 0, 0, 1).To4())
	stamp := ntpStamp(at)
	copy(reply[16:24], stamp) // reference time
	copy(reply[24:32], req[40:48])
	copy(reply[32:40], stamp) // received
	copy(reply[40:48], stamp) // sent
	return reply
}

// ntpStamp is t as an NTP timestamp: seconds since 1900 and a fraction.
func ntpStamp(t time.Time) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint32(b[0:4], uint32(t.Unix()+2208988800))
	binary.BigEndian.PutUint32(b[4:8], uint32((uint64(t.Nanosecond())<<32)/1e9))
	return b
}
