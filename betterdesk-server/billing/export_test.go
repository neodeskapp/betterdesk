package billing

import (
	"encoding/csv"
	"strings"
	"testing"
	"time"
)

func TestWriteReportsCSV(t *testing.T) {
	data, err := WriteReportsCSV([]ReportExportRow{{
		SessionID: "s1", DeviceID: "dev", Summary: "Fixed issue", CreatedAt: time.Now().UTC(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || data[0] != 's' {
		t.Fatalf("unexpected csv: %q", string(data[:20]))
	}
}

func readCSV(t *testing.T, data []byte) [][]string {
	t.Helper()
	records, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	return records
}

func TestReportsCSVKeepsFormulasAsText(t *testing.T) {
	data, err := WriteReportsCSV([]ReportExportRow{{
		SessionID: "s1", DeviceID: "dev", OrgID: "org", OperatorID: "@op",
		Summary: `=HYPERLINK("http://x","y")`, Category: "+cmd", TicketRef: "-1+2",
		Amount: -5, Currency: "EUR", CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}, {
		SessionID: "s2", Summary: "\tcalc", Category: "\rx", TicketRef: "T-1 = done",
		CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}})
	if err != nil {
		t.Fatal(err)
	}
	rows := readCSV(t, data)
	want := [][]string{
		{"s1", "dev", "org", "'@op", `'=HYPERLINK("http://x","y")`, "'+cmd", "'-1+2", "0", "-5.00", "EUR", "2026-01-02T03:04:05Z"},
		{"s2", "", "", "", "'\tcalc", "'\rx", "T-1 = done", "0", "0.00", "", "2026-01-02T03:04:05Z"},
	}
	for i, w := range want {
		got := rows[i+1]
		if strings.Join(got, "|") != strings.Join(w, "|") {
			t.Errorf("row %d = %q, want %q", i+1, got, w)
		}
	}
}

func TestSessionsCSVKeepsFormulasAsText(t *testing.T) {
	ended := time.Date(2026, 1, 2, 4, 0, 0, 0, time.UTC)
	data, err := WriteSessionsCSV([]SessionExportRow{{
		ID: "=1+1", OrgID: "+org", DeviceID: "-dev", OperatorID: "@op", Status: "ended",
		BillingPhase: "overage", BilledMinutes: 3, AmountOverage: -1.5, Currency: "EUR",
		StartedAt: time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC), EndedAt: &ended,
	}})
	if err != nil {
		t.Fatal(err)
	}
	rows := readCSV(t, data)
	want := []string{"'=1+1", "'+org", "'-dev", "'@op", "ended", "overage", "3", "-1.50", "EUR", "2026-01-02T03:00:00Z", "2026-01-02T04:00:00Z"}
	if strings.Join(rows[1], "|") != strings.Join(want, "|") {
		t.Errorf("row = %q, want %q", rows[1], want)
	}
}

func TestSimpleTextPDF(t *testing.T) {
	pdf := simpleTextPDF([]string{"BetterDesk billing report", "Line 2"})
	if len(pdf) < 100 || pdf[0] != '%' {
		t.Fatal("invalid pdf header")
	}
}

func TestEscapePDFString(t *testing.T) {
	got := escapePDFString(`test (parens) and \ backslash`)
	if got == "" {
		t.Fatal("empty")
	}
}
