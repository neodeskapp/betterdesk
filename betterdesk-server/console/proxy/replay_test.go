package proxy

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecordingRewindsWhatWasRead(t *testing.T) {
	body := strings.Repeat("0123456789", 5000)
	for name, read := range map[string]int{"nothing read": 0, "part read": 1234, "all read": len(body) + 10} {
		r := httptest.NewRequest("POST", "/x", strings.NewReader(body))
		rec := Record(r)
		got, _ := io.ReadAll(io.LimitReader(r.Body, int64(read)))
		if want := body[:min(read, len(body))]; string(got) != want {
			t.Fatalf("%s: read %d bytes through the recording, want %d", name, len(got), len(want))
		}
		again, err := rec.Rewind()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if whole, _ := io.ReadAll(again); string(whole) != body {
			t.Errorf("%s: the rewound body has %d bytes, want the %d that were sent", name, len(whole), len(body))
		}
	}
}

func TestRecordingWithoutABody(t *testing.T) {
	r := httptest.NewRequest("GET", "/x", nil)
	rec := Record(r)
	body, err := rec.Rewind()
	if err != nil || body != http.NoBody {
		t.Errorf("Rewind = %v, %v; want no body", body, err)
	}
}

func TestRecordingGivesUpPastItsLimit(t *testing.T) {
	r := httptest.NewRequest("POST", "/x", strings.NewReader(strings.Repeat("x", RecordLimit+1)))
	rec := Record(r)
	if n, _ := io.Copy(io.Discard, r.Body); n != RecordLimit+1 {
		t.Fatalf("read %d bytes", n)
	}
	if _, err := rec.Rewind(); !errors.Is(err, ErrBodyTooLarge) {
		t.Errorf("Rewind = %v, want ErrBodyTooLarge", err)
	}

	fits := httptest.NewRequest("POST", "/x", strings.NewReader(strings.Repeat("x", RecordLimit)))
	rec = Record(fits)
	_, _ = io.Copy(io.Discard, fits.Body)
	if body, err := rec.Rewind(); err != nil {
		t.Errorf("a body of exactly the limit: %v", err)
	} else if whole, _ := io.ReadAll(body); len(whole) != RecordLimit {
		t.Errorf("rewound %d bytes", len(whole))
	}
}
