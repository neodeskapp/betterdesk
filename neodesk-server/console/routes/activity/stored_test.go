package activity_test

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
)

// The device recordings read the tables after the reports: "the
// sessions", "the summaries" and "the alerts raised". Where a bug fix
// changes what a report stores, those rows differ from Node's. storedRows
// takes these statements out of the recording and checks them itself:
// rows that are Node's pass as they are, and the rest must be what the
// fixed route stores for the reports since the last statement.
//
//   - A summary counts the stored sessions and adds their durations as
//     numbers (BUGS AI16): its session_count and total_active_secs are
//     those of reportModel, and its other columns Node's.
//   - A report Node refused or failed and the Go console stores (a null
//     session, a duration that is no number in a session that is not
//     stored) adds its summary, its sessions and its idle alert to Node's
//     rows.

// storedTables are the statements storedRows checks, by name.
var storedTables = []string{"the sessions", "the summaries", "the alerts raised"}

// report is a report of the recording as the fixed route stores it.
type report struct {
	device     string
	reportedAt string // "" when the report has no time
	sessions   int
	total      float64
	nodeStored bool // Node answered 200
	goStored   bool // the Go console answered 200
}

// reportModel reads what the fixed route stores of a report body: the
// sessions that are objects with a start and an end, 500 at most, and the
// sum of their durations, each a number or text that is one, rounded.
func reportModel(body string) (sessions int, total float64, reportedAt string) {
	var b map[string]any
	if json.Unmarshal([]byte(body), &b) != nil {
		return 0, 0, ""
	}
	reportedAt, _ = b["timestamp"].(string)
	list, _ := b["sessions"].([]any)
	for _, item := range list {
		s, isObject := item.(map[string]any)
		if !isObject || !truthy(s["started_at"]) || !truthy(s["ended_at"]) || sessions == 500 {
			continue
		}
		sessions++
		switch d := s["duration_secs"].(type) {
		case float64:
			total += d
		case string:
			if n, err := strconv.ParseFloat(strings.TrimSpace(d), 64); err == nil && !math.IsInf(n, 0) && !math.IsNaN(n) {
				total += n
			}
		}
	}
	return sessions, math.Round(total), reportedAt
}

// truthy is JavaScript's !!v for a JSON value.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != ""
	}
	return true
}

// storedRows takes the statements of storedTables out of the recording and
// returns the Check that checks them and follows the reports, with the
// number of statements whose rows differ from Node's by table.
func storedRows(rec *replay.Recording, w *replay.World) (replay.Check, map[string]int) {
	statements := map[string]replay.Step{}
	tokens := map[string]string{}
	for _, seed := range rec.Seed {
		if strings.HasPrefix(seed.SQL, "INSERT INTO access_tokens (token,") {
			token, _ := seed.Params[0].(string)
			device, _ := seed.Params[3].(string)
			tokens[token] = device
		}
	}
	for i, step := range rec.Steps {
		if step.SQL != "" && slices.Contains(storedTables, step.Name) {
			name := fmt.Sprintf("%s (step %d)", step.Name, i)
			statements[name] = step
			rec.Steps[i] = replay.Step{Name: name}
		}
	}
	differ := map[string]int{}
	var block []report
	selecting := false
	return func(t *testing.T, step replay.Step, got replay.Answer) bool {
		if step.Method == "POST" && strings.HasSuffix(strings.ToLower(strings.TrimSuffix(strings.Split(step.Path, "?")[0], "/")), "/activity") {
			if selecting {
				block, selecting = nil, false
			}
			r := report{
				device:     tokens[strings.TrimSpace(strings.TrimPrefix(step.Headers["Authorization"], "Bearer "))],
				nodeStored: step.Status == 200,
				goStored:   got.Status == 200,
			}
			if step.Body != nil {
				r.sessions, r.total, r.reportedAt = reportModel(*step.Body)
			}
			block = append(block, r)
			return false
		}
		original, ok := statements[step.Name]
		if !ok {
			return false
		}
		selecting = true
		have := w.Rows(original.SQL, original.Params)
		if have == original.Rows {
			return true
		}
		table := strings.Fields(strings.Split(step.Name, " (")[0])[1]
		differ[table]++
		if problem := fixedRows(table, block, decodedRows(original.Rows), decodedRows(have)); problem != "" {
			t.Errorf("%s: %s\n   node: %s\n     go: %s", step.Name, problem, original.Rows, have)
		}
		return true
	}, differ
}

// decodedRows reads rows as Rows writes them, numbers as json.Number.
func decodedRows(text string) []map[string]any {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var rows []map[string]any
	_ = dec.Decode(&rows)
	return rows
}

// fixedRows compares the Go console's rows of a table with Node's after
// the reports of block, and says what is wrong ("" for nothing).
func fixedRows(table string, block []report, node, have []map[string]any) string {
	key := func(row map[string]any) string {
		b, _ := json.Marshal(row)
		return string(b)
	}
	// The report a summary row is of
	reportOf := func(row map[string]any) *report {
		for i := len(block) - 1; i >= 0; i-- {
			r := &block[i]
			// PostgreSQL's rows write the time without the Z
			at, _ := row["reported_at"].(string)
			if r.goStored && row["device_id"] == r.device && (r.reportedAt == "" || strings.TrimSuffix(at, "Z") == strings.TrimSuffix(r.reportedAt, "Z")) {
				return r
			}
		}
		return nil
	}
	if table == "summaries" {
		for _, row := range have {
			r := reportOf(row)
			if r == nil {
				continue
			}
			if fmt.Sprint(row["session_count"]) != strconv.Itoa(r.sessions) || fmt.Sprint(row["total_active_secs"]) != strconv.FormatFloat(r.total, 'f', -1, 64) {
				return fmt.Sprintf("the summary of %s counts %v sessions and %v seconds, want %d and %v", r.device, row["session_count"], row["total_active_secs"], r.sessions, r.total)
			}
			for _, column := range []string{"session_count_type", "total_active_secs_type"} {
				if kind, typed := row[column]; typed && kind != "integer" {
					return fmt.Sprintf("%s is %v, want integer", column, kind)
				}
			}
		}
		for _, rows := range [][]map[string]any{node, have} {
			for _, row := range rows {
				if reportOf(row) != nil {
					for _, column := range []string{"session_count", "session_count_type", "total_active_secs", "total_active_secs_type"} {
						delete(row, column)
					}
				}
			}
		}
	}
	// Node's rows are there, and the others are of reports only the Go
	// console stored
	left := map[string]int{}
	for _, row := range have {
		left[key(row)]++
	}
	for _, row := range node {
		if left[key(row)] == 0 {
			return "Node's row " + key(row) + " is missing"
		}
		left[key(row)]--
	}
	onlyGo := map[string]bool{}
	sessions := 0
	for _, r := range block {
		if r.goStored && !r.nodeStored {
			onlyGo[r.device] = true
		}
		if r.goStored {
			sessions += r.sessions
		}
	}
	for row, n := range left {
		if n == 0 {
			continue
		}
		var decoded map[string]any
		_ = json.Unmarshal([]byte(row), &decoded)
		device, _ := decoded["device_id"].(string)
		if !onlyGo[device] {
			return "the row " + row + " is of no report only the Go console stored"
		}
		if message, _ := decoded["message"].(string); table == "alerts" && !strings.Contains(message, "idle_duration") {
			return "the alert " + row + " is no idle alert"
		}
	}
	if table == "sessions" && len(have) != sessions {
		return fmt.Sprintf("%d sessions, want the %d the reports store", len(have), sessions)
	}
	return ""
}
