package billing

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// ReportExportRow is a flattened work report for export.
type ReportExportRow struct {
	SessionID  string
	DeviceID   string
	OrgID      string
	OperatorID string
	Summary    string
	Category   string
	TicketRef  string
	BilledMin  int
	Amount     float64
	Currency   string
	CreatedAt  time.Time
}

// SessionExportRow is a flattened billing session for export.
type SessionExportRow struct {
	ID            string
	OrgID         string
	DeviceID      string
	OperatorID    string
	Status        string
	BillingPhase  string
	BilledMinutes int
	AmountOverage float64
	Currency      string
	StartedAt     time.Time
	EndedAt       *time.Time
}

// csvText makes a text cell safe to open in a spreadsheet: a cell that
// starts with a character a spreadsheet reads as the start of a formula
// gets a single quote in front, so it shows as text.
func csvText(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// WriteReportsCSV writes work reports as CSV bytes.
func WriteReportsCSV(rows []ReportExportRow) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"session_id", "device_id", "org_id", "operator_id", "summary", "category", "ticket_ref", "billed_minutes", "amount", "currency", "created_at"})
	for _, r := range rows {
		_ = w.Write([]string{
			csvText(r.SessionID), csvText(r.DeviceID), csvText(r.OrgID), csvText(r.OperatorID),
			csvText(r.Summary), csvText(r.Category), csvText(r.TicketRef),
			strconv.Itoa(r.BilledMin), fmt.Sprintf("%.2f", r.Amount), csvText(r.Currency), r.CreatedAt.Format(time.RFC3339),
		})
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// WriteSessionsCSV writes billing sessions as CSV bytes.
func WriteSessionsCSV(rows []SessionExportRow) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"id", "org_id", "device_id", "operator_id", "status", "billing_phase", "billed_minutes", "amount_overage", "currency", "started_at", "ended_at"})
	for _, r := range rows {
		ended := ""
		if r.EndedAt != nil {
			ended = r.EndedAt.Format(time.RFC3339)
		}
		_ = w.Write([]string{
			csvText(r.ID), csvText(r.OrgID), csvText(r.DeviceID), csvText(r.OperatorID), csvText(r.Status), csvText(r.BillingPhase),
			strconv.Itoa(r.BilledMinutes), fmt.Sprintf("%.2f", r.AmountOverage), csvText(r.Currency),
			r.StartedAt.Format(time.RFC3339), ended,
		})
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// WriteReportsPDF writes a minimal text PDF with report lines.
func WriteReportsPDF(rows []ReportExportRow, title string) []byte {
	var lines []string
	lines = append(lines, title)
	lines = append(lines, fmt.Sprintf("Generated: %s", time.Now().UTC().Format(time.RFC3339)))
	lines = append(lines, "")
	for i, r := range rows {
		lines = append(lines, fmt.Sprintf("#%d  Session: %s", i+1, r.SessionID))
		lines = append(lines, fmt.Sprintf("Device: %s  Org: %s  Operator: %s", r.DeviceID, r.OrgID, r.OperatorID))
		lines = append(lines, fmt.Sprintf("Time: %s  Billed: %d min  Amount: %.2f %s", r.CreatedAt.Format(time.RFC3339), r.BilledMin, r.Amount, r.Currency))
		if r.Category != "" {
			lines = append(lines, "Category: "+r.Category)
		}
		if r.TicketRef != "" {
			lines = append(lines, "Ticket: "+r.TicketRef)
		}
		lines = append(lines, "Summary: "+sanitizePDFText(r.Summary))
		lines = append(lines, "")
	}
	return simpleTextPDF(lines)
}

func sanitizePDFText(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

// Text layout of simpleTextPDF: lines from the top of a US Letter page down
// to its bottom margin, 14 points apart.
const (
	pdfTextTop    = 770
	pdfTextBottom = 40
	pdfLeading    = 14
	pdfPageLines  = (pdfTextTop-pdfTextBottom)/pdfLeading + 1
)

// simpleTextPDF builds a minimal valid PDF 1.4 document with Helvetica text,
// with as many pages as the lines need.
func simpleTextPDF(lines []string) []byte {
	var pages []string
	for start := 0; start == 0 || start < len(lines); start += pdfPageLines {
		end := min(start+pdfPageLines, len(lines))
		var content strings.Builder
		content.WriteString("BT\n/F1 10 Tf\n")
		y := pdfTextTop
		for _, line := range lines[start:end] {
			content.WriteString(fmt.Sprintf("1 0 0 1 40 %d Tm (%s) Tj\n", y, escapePDFString(line)))
			y -= pdfLeading
		}
		content.WriteString("ET\n")
		pages = append(pages, content.String())
	}

	// Objects: 1 catalog, 2 page tree, 3 font, then a page and its content
	// stream for each page.
	pageObj := func(i int) int { return 4 + 2*i }
	kids := make([]string, len(pages))
	for i := range pages {
		kids[i] = fmt.Sprintf("%d 0 R", pageObj(i))
	}

	var buf bytes.Buffer
	w := func(s string) { buf.WriteString(s) }
	w("%PDF-1.4\n")
	offs := []int{0}
	offs = append(offs, buf.Len())
	w("1 0 obj<< /Type /Catalog /Pages 2 0 R >>endobj\n")
	offs = append(offs, buf.Len())
	w(fmt.Sprintf("2 0 obj<< /Type /Pages /Kids [%s] /Count %d >>endobj\n", strings.Join(kids, " "), len(pages)))
	offs = append(offs, buf.Len())
	w("3 0 obj<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>endobj\n")
	for i, stream := range pages {
		offs = append(offs, buf.Len())
		w(fmt.Sprintf("%d 0 obj<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>endobj\n",
			pageObj(i), pageObj(i)+1))
		offs = append(offs, buf.Len())
		w(fmt.Sprintf("%d 0 obj<< /Length %d >>stream\n%s\nendstream\nendobj\n", pageObj(i)+1, len(stream), stream))
	}
	xref := buf.Len()
	w("xref\n")
	w(fmt.Sprintf("0 %d\n", len(offs)))
	w("0000000000 65535 f \n")
	for i := 1; i < len(offs); i++ {
		w(fmt.Sprintf("%010d 00000 n \n", offs[i]))
	}
	w(fmt.Sprintf("trailer<< /Size %d /Root 1 0 R >>\n", len(offs)))
	w(fmt.Sprintf("startxref\n%d\n%%EOF\n", xref))
	return buf.Bytes()
}

// escapePDFString cuts s to 200 characters and escapes it for a PDF literal
// string. Cutting first keeps every escape whole.
func escapePDFString(s string) string {
	r := []rune(s)
	if len(r) > 200 {
		r = r[:200]
	}
	s = string(r)
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `(`, `\(`)
	s = strings.ReplaceAll(s, `)`, `\)`)
	return s
}

// BuildReportExportRows joins reports with session billing data.
func BuildReportExportRows(reports []*db.BillingWorkReport, sessions map[string]*db.BillingSession) []ReportExportRow {
	out := make([]ReportExportRow, 0, len(reports))
	for _, r := range reports {
		row := ReportExportRow{
			SessionID:  r.SessionID,
			OperatorID: r.OperatorID,
			Summary:    r.Summary,
			Category:   r.Category,
			TicketRef:  r.TicketRef,
			CreatedAt:  r.CreatedAt,
		}
		if sess, ok := sessions[r.SessionID]; ok && sess != nil {
			row.DeviceID = sess.DeviceID
			row.OrgID = sess.OrgID
			row.BilledMin = sess.BilledMinutes
			row.Amount = sess.AmountIncluded + sess.AmountOverage
			row.Currency = sess.Currency
		}
		out = append(out, row)
	}
	return out
}
