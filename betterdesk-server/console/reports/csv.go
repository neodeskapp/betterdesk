package reports

import (
	"math"
	"strings"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
)

// ToCSV is toCsv(rows): a header line with the keys of the first row,
// then one line per row, joined by "\n" with no line end after the last
// and no byte order mark. A value is its JavaScript text (a date in the
// zone of the process), null and missing values are empty, and a value
// with a comma, a quote or "\n" is quoted with its quotes doubled; "\r"
// alone is not quoted. No rows give the empty text.
func (e *Engine) ToCSV(rows []any) string {
	if len(rows) == 0 {
		return ""
	}
	var columns []string
	if first, ok := rows[0].(*goapi.Object); ok {
		columns = first.Keys()
	}
	lines := make([]string, 0, len(rows)+1)
	cells := make([]string, len(columns))
	for i, column := range columns {
		cells[i] = e.csvCell(column, true)
	}
	lines = append(lines, strings.Join(cells, ","))
	for _, row := range rows {
		for i, column := range columns {
			v, found := prop(row, column)
			cells[i] = e.csvCell(v, found)
		}
		lines = append(lines, strings.Join(cells, ","))
	}
	return strings.Join(lines, "\n")
}

// csvCell is escape(v) of toCsv; found false is undefined.
func (e *Engine) csvCell(v any, found bool) string {
	if !found || v == nil {
		return ""
	}
	s := e.clock.String(v)
	if strings.ContainsAny(s, ",\"\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// CSVRows are the rows the CSV export writes for a report, as the route
// chose them: the list data[section] when there is one, otherwise one
// row of the top-level values that are not objects, or none.
func (r *Report) CSVRows(section jsbind.Value) []any {
	if section.Truthy() {
		if list, ok := r.Data.Get(section.ToString()).([]any); ok {
			return list
		}
	}
	flat := goapi.NewObject()
	for _, key := range r.Data.Keys() {
		if v := r.Data.Get(key); !isObject(v) {
			flat.Set(key, v)
		}
	}
	if flat.Len() == 0 {
		return []any{}
	}
	return []any{flat}
}

// jsRound is Math.round: halves go up, toward positive infinity.
func jsRound(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	r := math.Floor(x)
	if x-r >= 0.5 {
		r++
	}
	if r == 0 && math.Signbit(x) {
		// -0.4 and -0 round to -0
		return math.Copysign(0, -1)
	}
	return r
}

// jsLower is s.toLowerCase().
func jsLower(s string) string { return jsbind.ToLower(s) }
