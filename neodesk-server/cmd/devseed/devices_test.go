package main

import (
	"bytes"
	"context"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

func TestPerfDevicesAreTheScriptsRows(t *testing.T) {
	now := func() float64 { return sampleMS }
	get := func(row []col, name string) any {
		for _, c := range row {
			if c.name == name {
				return c.value
			}
		}
		t.Fatalf("no column %s", name)
		return nil
	}
	cases := []struct {
		i    int
		want map[string]any
	}{
		{0, map[string]any{"id": "950000000", "uuid": "perf-uuid-0", "ip": "10.0.0.0", "user": "user0",
			"hostname": "perf-reception-00000", "os": "Windows 11 Pro", "banned": flag(true), "note": "Perf note 0", "tags": "perf,reception",
			"last_online": stamp{sampleMS - 5*minute, sqlSeconds}, "created_at": stamp{sampleMS, sqlSeconds}}},
		// Every 211th device is banned
		{211, map[string]any{"banned": flag(true)}},
		{210, map[string]any{"banned": flag(false)}},
		{40000, map[string]any{"ip": "10.0.156.64"}},
		{300, map[string]any{"id": "950000300", "ip": "10.0.1.44", "user": "user9", "hostname": "perf-kiosk-00300",
			"os": "ChromeOS 128", "banned": flag(false), "note": "", "tags": "perf,kiosk",
			"last_online": stamp{sampleMS - 305*minute, sqlSeconds}, "created_at": stamp{sampleMS - 300*day, sqlSeconds}}},
		{70000, map[string]any{"ip": "10.1.17.112", "hostname": "perf-reception-70000", "user": "user63",
			"last_online": stamp{sampleMS - 5*minute, sqlSeconds}}},
	}
	for _, c := range cases {
		row := perfPeer(c.i, now)
		for k, want := range c.want {
			if got := get(row, k); got != want {
				t.Errorf("device %d %s = %#v, want %#v", c.i, k, got, want)
			}
		}
	}
	for _, c := range []struct {
		in   string
		want float64
	}{{"5000", 5000}, {"0", 1}, {"99999", 20000}, {"2.5", 2.5}} {
		if got := perfCount(c.in); got != c.want {
			t.Errorf("perfCount(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	if !math.IsNaN(perfCount("12abc")) {
		t.Error("a count that is no number is not NaN")
	}
}

func TestManyDevicesAddsAndRemovesOnlyPerfDevices(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		storetest.Prepare(t, db, storetest.GoServerFirst)
		markStack(t, db)
		ctx := context.Background()
		if _, err := db.Exec(ctx, `INSERT INTO peers (id, hostname) VALUES ('900100001', 'demo-reception-01'), ('960000000', 'not-perf')`); err != nil {
			t.Fatal(err)
		}
		dsn := testDSN(t, db)
		var out bytes.Buffer
		if err := run(ctx, []string{"many-devices", "--go-db", dsn, "--count", "250", "--now", "1795083630456"}, func(string) string { return "" }, &out, io.Discard); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "added up to 250 perf devices (IDs 950000000..950000249)") {
			t.Errorf("output: %s", out.String())
		}
		var n, banned int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM peers WHERE hostname LIKE 'perf-%'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(ctx, db.Pick(`SELECT COUNT(*) FROM peers WHERE banned = 1`, `SELECT COUNT(*) FROM peers WHERE banned`)).Scan(&banned); err != nil {
			t.Fatal(err)
		}
		// Every 211th device is banned: 0 and 211
		if n != 250 || banned != 2 {
			t.Errorf("%d perf devices, %d banned", n, banned)
		}
		out.Reset()
		if err := run(ctx, []string{"many-devices", "--go-db", dsn, "--remove"}, func(string) string { return "" }, &out, io.Discard); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM peers`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 2 || !strings.Contains(out.String(), "removed 250 perf devices") {
			t.Errorf("%d devices left, output %q", n, out.String())
		}
	})
}

// testDSN is what devseed is given to reach a test database: the SQLite
// file, or the PostgreSQL address with the test's schema.
func testDSN(t *testing.T, db *store.DB) string {
	t.Helper()
	ctx := context.Background()
	if !db.IsPostgres() {
		var path string
		if err := db.QueryRow(ctx, `SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&path); err != nil {
			t.Fatal(err)
		}
		return path
	}
	var schema string
	if err := db.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	dsn := os.Getenv(storetest.PostgresEnv)
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "search_path=" + schema
}

func TestRegisterPeerIsTheHandEncodedMessage(t *testing.T) {
	got := registerPeer("900100001")
	want := append([]byte{0x32, 13, 0x0a, 9}, "900100001"...)
	want = append(want, 0x10, 0x01)
	if !bytes.Equal(got, want) {
		t.Errorf("% x, want % x", got, want)
	}
}

func TestHeartbeatsSendEveryOnlineDevice(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ids := filepath.Join(t.TempDir(), "ids.json")
	if err := os.WriteFile(ids, []byte(`["900100001","900100003"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	env := map[string]string{"SIGNAL_HOST": "127.0.0.1", "SIGNAL_PORT": strconv.Itoa(port)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"heartbeats", ids}, func(k string) string { return env[k] }, io.Discard, io.Discard)
	}()
	got := map[string]bool{}
	buf := make([]byte, 64)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for len(got) < 2 {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			t.Fatalf("after %v: %v", got, err)
		}
		got[string(buf[4:n-2])] = bytes.Equal(buf[:n], registerPeer(string(buf[4:n-2])))
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !got["900100001"] || !got["900100003"] {
		t.Errorf("heartbeats: %v", got)
	}
}
