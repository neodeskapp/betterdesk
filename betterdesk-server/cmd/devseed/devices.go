package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/unitronix/betterdesk-server/console/store"
)

// Devices for performance checks of the device list (the console's budget
// is measured with 5,000): ids from 950000000 and host names "perf-...",
// so removing them touches nothing of the demo.

const firstPerfID = 950000000

var (
	perfOSes   = []string{"Windows 11 Pro", "Windows 10 Enterprise", "Ubuntu 24.04", "macOS 15.1", "Android 14", "Debian 12", "ChromeOS 128"}
	perfPlaces = []string{"reception", "warehouse", "lab", "office", "kiosk", "branch", "lobby", "workshop"}
)

// perfPeer is the row of the i-th perf device. now gives the time each
// row's times count back from.
func perfPeer(i int, now func() float64) []col {
	place := perfPlaces[i%len(perfPlaces)]
	ago := func(minutes int) stamp { return stamp{now() - float64(minutes)*minute, sqlSeconds} }
	note, tags := "", ""
	if i%13 == 0 {
		note = "Perf note " + strconv.Itoa(i)
	}
	if i%5 == 0 {
		tags = "perf," + place
	}
	return []col{
		{"id", strconv.Itoa(firstPerfID + i)}, {"uuid", "perf-uuid-" + strconv.Itoa(i)},
		{"ip", fmt.Sprintf("10.%d.%d.%d", (i>>16)&255, (i>>8)&255, i&255)},
		{"user", "user" + strconv.Itoa(i%97)},
		{"hostname", "perf-" + place + "-" + leftPad(strconv.Itoa(i), 5)},
		{"os", perfOSes[i%len(perfOSes)]}, {"version", "1.4.2"}, {"status", "OFFLINE"},
		{"last_online", ago(5 + i%10000)}, {"created_at", ago(60 * 24 * (i % 365))},
		{"banned", flag(i%211 == 0)}, {"ban_reason", ""}, {"banned_at", nil}, {"soft_deleted", flag(false)},
		{"deleted_at", nil}, {"note", note}, {"tags", tags},
	}
}

// perfCount reads --count as Number() does and keeps it between 1 and
// 20000. Text that is no number gives NaN, and no devices.
func perfCount(text string) float64 {
	return jsMax(1, jsMin(20000, stringToNumber(text)))
}

func runManyDevices(ctx context.Context, args []string, getenv func(string) string, out io.Writer) error {
	f, err := parseFlags(args, []string{"go-db", "count", "now"}, []string{"remove"})
	if err != nil {
		return err
	}
	count := perfCount(f.get("count", "5000"))
	now := func() float64 { return float64(time.Now().UnixMilli()) }
	if text := f.get("now", ""); text != "" {
		at, err := seedTime(text)
		if err != nil {
			return err
		}
		fixed := float64(at.UnixMilli())
		now = func() float64 { return fixed }
	}
	db, err := openDatabase(ctx, databaseFrom(f.get("go-db", ""), getenv))
	if err != nil {
		return err
	}
	defer db.Close()
	if err := requireMarker(ctx, db); err != nil {
		return err
	}

	if f.switches["remove"] {
		res, err := db.Exec(ctx, db.Pick(
			`DELETE FROM peers WHERE CAST(id AS INTEGER) >= ? AND hostname LIKE 'perf-%'`,
			`DELETE FROM peers WHERE (CASE WHEN id ~ '^[0-9]+$' THEN CAST(id AS NUMERIC) ELSE 0 END) >= ? AND hostname LIKE 'perf-%'`),
			float64(firstPerfID))
		if err != nil {
			return fmt.Errorf("remove the perf devices: %w", err)
		}
		n, _ := res.RowsAffected()
		fmt.Fprintf(out, "removed %d perf devices\n", n)
		return nil
	}

	w, err := newWriter(ctx, db.DB)
	if err != nil {
		return err
	}
	cols, err := w.columns(ctx, "peers")
	if err != nil {
		return err
	}
	deviceType := slices.Contains(cols, "device_type")
	err = db.InTx(ctx, func(tx *store.Tx) error {
		for i := 0; float64(i) < count; i++ {
			row := perfPeer(i, now)
			if deviceType {
				row = append(row, col{"device_type", "rustdesk"})
			}
			if err := w.insert(ctx, tx, "peers", row, `ON CONFLICT(id) DO NOTHING`); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	last := float64(firstPerfID) + count - 1
	if math.IsNaN(count) {
		last = math.NaN()
	}
	fmt.Fprintf(out, "added up to %s perf devices (IDs %d..%s)\n", numberString(count), firstPerfID, numberString(last))
	return nil
}
