package dataguard_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
)

// policyFixes are the steps of testdata/node_*.json that the bug fixes
// change, deviceFixes those of testdata/node_device_*.json.
var policyFixes, deviceFixes replay.Fixes

// policyColumns are the columns of a policy that a request writes.
var policyColumns = []string{"name", "description", "policy_type", "action", "scope", "enabled", "rules"}

// nodePolicy writes the policy Node's answer holds where Node created or
// changed one, so the steps after it see the rows Node had. Where Node
// failed it does nothing.
func nodePolicy(t *testing.T, w *replay.World, step replay.Step) {
	t.Helper()
	if step.Status != 200 && step.Status != 201 {
		return
	}
	var row map[string]json.RawMessage
	if err := json.Unmarshal([]byte(step.Answer), &row); err != nil {
		t.Fatalf("%s: Node's answer is no policy: %v", step.Name, err)
	}
	values := make([]any, len(policyColumns))
	for i, column := range policyColumns {
		raw := row[column]
		switch {
		case column == "rules":
			values[i] = string(raw)
		case string(raw) == "null":
			values[i] = nil
		default:
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				t.Fatal(err)
			}
			if n, isNumber := v.(float64); isNumber {
				v = int64(n)
			}
			values[i] = v
		}
	}
	if step.Status == 201 {
		w.Exec("INSERT INTO dlp_policies ("+strings.Join(policyColumns, ", ")+") VALUES (?, ?, ?, ?, ?, ?, ?)", values)
		return
	}
	sets := make([]string, len(policyColumns))
	for i, column := range policyColumns {
		sets[i] = column + " = ?"
	}
	var id int64
	if err := json.Unmarshal(row["id"], &id); err != nil {
		t.Fatal(err)
	}
	w.Exec("UPDATE dlp_policies SET "+strings.Join(sets, ", ")+", updated_at = "+store.NowSQL(w.DB.Dialect())+" WHERE id = ?", append(values, id))
}

// DG1: every member of a policy has its type, on create and in a change.
var _ = policyFixes.Add("DG1", map[string]replay.Fix{
	`admin POST /api/dataguard/policies {"name":"Policy 3","description":0}`:                     {Status: 400, Answer: `{"error":"Invalid description"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 4","description":5}`:                     {Status: 400, Answer: `{"error":"Invalid description"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 5","description":1.5}`:                   {Status: 400, Answer: `{"error":"Invalid description"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 6","description":true}`:                  {Status: 400, Answer: `{"error":"Invalid description"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 7","description":false}`:                 {Status: 400, Answer: `{"error":"Invalid description"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 9","description":["one"]}`:               {Status: 400, Answer: `{"error":"Invalid description"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 10","description":["a","b"]}`:            {Status: 400, Answer: `{"error":"Invalid description"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 11","description":[]}`:                   {Status: 400, Answer: `{"error":"Invalid description"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 12","description":{}}`:                   {Status: 400, Answer: `{"error":"Invalid description"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 13","description":{"a":1}}`:              {Status: 400, Answer: `{"error":"Invalid description"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 17","policy_type":0}`:                    {Status: 400, Answer: `{"error":"Invalid policy_type"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 18","policy_type":5}`:                    {Status: 400, Answer: `{"error":"Invalid policy_type"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 19","policy_type":1.5}`:                  {Status: 400, Answer: `{"error":"Invalid policy_type"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 20","policy_type":true}`:                 {Status: 400, Answer: `{"error":"Invalid policy_type"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 21","policy_type":false}`:                {Status: 400, Answer: `{"error":"Invalid policy_type"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 23","policy_type":["one"]}`:              {Status: 400, Answer: `{"error":"Invalid policy_type"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 24","policy_type":["a","b"]}`:            {Status: 400, Answer: `{"error":"Invalid policy_type"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 25","policy_type":[]}`:                   {Status: 400, Answer: `{"error":"Invalid policy_type"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 26","policy_type":{}}`:                   {Status: 400, Answer: `{"error":"Invalid policy_type"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 27","policy_type":{"a":1}}`:              {Status: 400, Answer: `{"error":"Invalid policy_type"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 31","action":0}`:                         {Status: 400, Answer: `{"error":"Invalid action"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 32","action":5}`:                         {Status: 400, Answer: `{"error":"Invalid action"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 33","action":1.5}`:                       {Status: 400, Answer: `{"error":"Invalid action"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 34","action":true}`:                      {Status: 400, Answer: `{"error":"Invalid action"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 35","action":false}`:                     {Status: 400, Answer: `{"error":"Invalid action"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 37","action":["one"]}`:                   {Status: 400, Answer: `{"error":"Invalid action"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 38","action":["a","b"]}`:                 {Status: 400, Answer: `{"error":"Invalid action"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 39","action":[]}`:                        {Status: 400, Answer: `{"error":"Invalid action"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 40","action":{}}`:                        {Status: 400, Answer: `{"error":"Invalid action"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 41","action":{"a":1}}`:                   {Status: 400, Answer: `{"error":"Invalid action"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 45","scope":0}`:                          {Status: 400, Answer: `{"error":"Invalid scope"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 46","scope":5}`:                          {Status: 400, Answer: `{"error":"Invalid scope"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 47","scope":1.5}`:                        {Status: 400, Answer: `{"error":"Invalid scope"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 48","scope":true}`:                       {Status: 400, Answer: `{"error":"Invalid scope"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 49","scope":false}`:                      {Status: 400, Answer: `{"error":"Invalid scope"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 51","scope":["one"]}`:                    {Status: 400, Answer: `{"error":"Invalid scope"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 52","scope":["a","b"]}`:                  {Status: 400, Answer: `{"error":"Invalid scope"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 53","scope":[]}`:                         {Status: 400, Answer: `{"error":"Invalid scope"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 54","scope":{}}`:                         {Status: 400, Answer: `{"error":"Invalid scope"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 55","scope":{"a":1}}`:                    {Status: 400, Answer: `{"error":"Invalid scope"}`, After: nodePolicy},
	`admin POST /api/bd/policies {"name":"Policy 61","enabled":2}`:                               {Status: 400, Answer: `{"error":"Invalid enabled"}`, After: nodePolicy},
	`admin POST /api/bd/policies {"name":"Policy 62","enabled":-1}`:                              {Status: 400, Answer: `{"error":"Invalid enabled"}`, After: nodePolicy},
	`admin POST /api/bd/policies {"name":"Policy 63","enabled":"yes"}`:                           {Status: 400, Answer: `{"error":"Invalid enabled"}`, After: nodePolicy},
	`admin POST /api/bd/policies {"name":"Policy 64","enabled":"no"}`:                            {Status: 400, Answer: `{"error":"Invalid enabled"}`, After: nodePolicy},
	`admin POST /api/bd/policies {"name":"Policy 65","enabled":"true"}`:                          {Status: 400, Answer: `{"error":"Invalid enabled"}`, After: nodePolicy},
	`admin POST /api/bd/policies {"name":"Policy 66","enabled":"false"}`:                         {Status: 400, Answer: `{"error":"Invalid enabled"}`, After: nodePolicy},
	`admin POST /api/bd/policies {"name":"Policy 67","enabled":"t"}`:                             {Status: 400, Answer: `{"error":"Invalid enabled"}`, After: nodePolicy},
	`admin POST /api/bd/policies {"name":"Policy 68","enabled":"on"}`:                            {Status: 400, Answer: `{"error":"Invalid enabled"}`, After: nodePolicy},
	`admin POST /api/bd/policies {"name":"Policy 69","enabled":"off"}`:                           {Status: 400, Answer: `{"error":"Invalid enabled"}`, After: nodePolicy},
	`admin POST /api/bd/policies {"name":"Policy 70","enabled":""}`:                              {Status: 400, Answer: `{"error":"Invalid enabled"}`, After: nodePolicy},
	`admin POST /api/bd/policies {"name":"Policy 71","enabled":" "}`:                             {Status: 400, Answer: `{"error":"Invalid enabled"}`, After: nodePolicy},
	`admin POST /api/bd/policies {"name":"Policy 72","enabled":"abc"}`:                           {Status: 400, Answer: `{"error":"Invalid enabled"}`, After: nodePolicy},
	`admin POST /api/bd/policies {"name":"Policy 73","enabled":null}`:                            {Status: 400, Answer: `{"error":"Invalid enabled"}`, After: nodePolicy},
	`admin POST /api/bd/policies {"name":"Policy 74","enabled":[]}`:                              {Status: 400, Answer: `{"error":"Invalid enabled"}`, After: nodePolicy},
	`admin POST /api/bd/policies {"name":"Policy 75","enabled":[1]}`:                             {Status: 400, Answer: `{"error":"Invalid enabled"}`, After: nodePolicy},
	`admin POST /api/bd/policies {"name":"Policy 76","enabled":{}}`:                              {Status: 400, Answer: `{"error":"Invalid enabled"}`, After: nodePolicy},
	`admin POST /api/bd/policies {"name":"Policy 77","enabled":1.5}`:                             {Status: 400, Answer: `{"error":"Invalid enabled"}`, After: nodePolicy},
	`admin POST /api/dataguard/policies {"name":"Policy 92","description":{},"scope":["a","b"]}`: {Status: 400, Answer: `{"error":"Invalid description"}`, After: nodePolicy},
	`admin PATCH /api/dataguard/policies/2 {"name":null}`:                                        {Status: 400, Answer: `{"error":"Policy name is required"}`, After: nodePolicy},
	`admin PATCH /api/dataguard/policies/2 {"name":5}`:                                           {Status: 400, Answer: `{"error":"Policy name is required"}`, After: nodePolicy},
	`admin PATCH /api/dataguard/policies/2 {"name":""}`:                                          {Status: 400, Answer: `{"error":"Policy name is required"}`, After: nodePolicy},
	`admin PATCH /api/dataguard/policies/2 {"name":["a","b"]}`:                                   {Status: 400, Answer: `{"error":"Policy name is required"}`, After: nodePolicy},
	`admin PATCH /api/dataguard/policies/2 {"description":null}`:                                 {Status: 400, Answer: `{"error":"Invalid description"}`, After: nodePolicy},
	`admin PATCH /api/dataguard/policies/2 {"description":7}`:                                    {Status: 400, Answer: `{"error":"Invalid description"}`, After: nodePolicy},
	`admin PATCH /api/dataguard/policies/2 {"description":true}`:                                 {Status: 400, Answer: `{"error":"Invalid description"}`, After: nodePolicy},
	`admin PATCH /api/dataguard/policies/2 {"action":null}`:                                      {Status: 400, Answer: `{"error":"Invalid action"}`, After: nodePolicy},
	`admin PATCH /api/dataguard/policies/2 {"scope":{}}`:                                         {Status: 400, Answer: `{"error":"Invalid scope"}`, After: nodePolicy},
	`admin PATCH /api/dataguard/policies/2 {"enabled":"0"}`:                                      {Status: 400, Answer: `{"error":"Invalid enabled"}`, After: nodePolicy},
	`admin PATCH /api/dataguard/policies/2 {"enabled":null}`:                                     {Status: 400, Answer: `{"error":"Invalid enabled"}`, After: nodePolicy},
	`admin PATCH /api/dataguard/policies/2 {"enabled":[]}`:                                       {Status: 400, Answer: `{"error":"Invalid enabled"}`, After: nodePolicy},
	`admin PATCH /api/dataguard/policies/2 {"enabled":""}`:                                       {Status: 400, Answer: `{"error":"Invalid enabled"}`, After: nodePolicy},
	`admin PATCH /api/dataguard/policies/2 {"enabled":"false"}`:                                  {Status: 400, Answer: `{"error":"Invalid enabled"}`, After: nodePolicy},
	`admin PATCH /api/dataguard/policies/2 {"description":{},"scope":["x","y"]}`:                 {Status: 400, Answer: `{"error":"Invalid description"}`, After: nodePolicy},
})

// DG2: a change that names no column answers the policy as it is.
var _ = policyFixes.Add("DG2", map[string]replay.Fix{
	"admin PATCH /api/dataguard/policies/2 {}": {Dialect: "sqlite", Status: 200,
		Answer: `{"id":2,"name":"By admin at /api/bd","description":"","policy_type":"","action":"log","scope":"","enabled":1,"rules":[],"created_at":"2001-02-03 04:05:06","updated_at":"2001-02-03 04:05:07"}`},
	`admin PATCH /api/bd/policies/2 {"unknown":true}`: {Dialect: "sqlite", Status: 200,
		Answer: `{"id":2,"name":"By admin at /api/bd","description":"","policy_type":"","action":"log","scope":"","enabled":1,"rules":[],"created_at":"2001-02-03 04:05:06","updated_at":"2001-02-03 04:05:07"}`},
	`admin PATCH /api/bd/policies/2 [{"name":"In a list"}]`: {Dialect: "sqlite", Status: 200,
		Answer: `{"id":2,"name":"Last","description":"x","policy_type":"usb","action":"log","scope":"y","enabled":1,"rules":[1],"created_at":"2001-02-03 04:05:06","updated_at":"<now, SQLite form>"}`},
})

var _ = policyFixes.Add("DG2", map[string]replay.Fix{
	"admin PATCH /api/dataguard/policies/2 {}": {Dialect: "postgres", Status: 200,
		Answer: `{"id":2,"name":"By admin at /api/bd","description":"","policy_type":"","action":"log","scope":"","enabled":true,"rules":[],"created_at":"2001-02-03T04:05:06.000Z","updated_at":"2001-02-03T04:05:07.000Z"}`},
	`admin PATCH /api/bd/policies/2 {"unknown":true}`: {Dialect: "postgres", Status: 200,
		Answer: `{"id":2,"name":"By admin at /api/bd","description":"","policy_type":"","action":"log","scope":"","enabled":true,"rules":[],"created_at":"2001-02-03T04:05:06.000Z","updated_at":"2001-02-03T04:05:07.000Z"}`},
	`admin PATCH /api/bd/policies/2 [{"name":"In a list"}]`: {Dialect: "postgres", Status: 200,
		Answer: `{"id":2,"name":"Last","description":"{}","policy_type":"usb","action":"log","scope":"{\"x\",\"y\"}","enabled":true,"rules":[1],"created_at":"2001-02-03T04:05:06.000Z","updated_at":"<now, ISO form>"}`},
})

// DG3: a row whose details or rules hold no JSON does not fail a list:
// details go out as the text they hold, rules as no rules.
var _ = policyFixes.Add("DG3", map[string]replay.Fix{
	"admin GET /api/dataguard/events?device_id=D5 ":         {Dialect: "sqlite", Status: 200, Has: []string{`"device_id":"D5"`, `"details":"not json"`}},
	"admin GET /api/dataguard/events?device_id=D6 ":         {Dialect: "sqlite", Status: 200, Has: []string{`"device_id":"D6"`, `"details":""`}},
	"admin GET /api/dataguard/events ":                      {Dialect: "sqlite", NodeStatus: 500, Status: 200, Has: []string{`"details":"not json"`, `"details":""`, `"device_id":"D1"`}},
	"admin GET /api/dataguard/policies/902 ":                {Dialect: "sqlite", Status: 200, Has: []string{`"id":902,"name":"rules broken"`, `"rules":[]`}},
	"admin GET /api/dataguard/policies/904 ":                {Dialect: "sqlite", Status: 200, Has: []string{`"id":904,"name":"rules empty"`, `"rules":[]`}},
	"admin GET /api/dataguard/policies ":                    {Dialect: "sqlite", NodeStatus: 500, Status: 200, Has: []string{`"id":902,"name":"rules broken"`, `"id":904,"name":"rules empty"`, `"id":1,"name":"By admin at /api/dataguard"`}},
	`admin PATCH /api/dataguard/policies/902 {"scope":"x"}`: {Dialect: "sqlite", Status: 200, Has: []string{`"id":902,"name":"rules broken"`, `"scope":"x"`, `"rules":[]`}},
})

// DV7: a device gets the policies that are on, enabled read as one flag
// (a text such as "yes" is off), and rules that are no list, or no JSON, as
// no rules; one unreadable row no longer fails the list.
var _ = deviceFixes.Add("DV7", map[string]replay.Fix{
	" GET /api/bd/dlp-policies ":                           {NodeStatus: 200, Status: 200, Has: dv7Has, Lacks: dv7Lacks},
	" GET /api/bd/dlp-policies?device_id=device-b ":        {NodeStatus: 200, Status: 200, Has: dv7Has, Lacks: dv7Lacks},
	"operator GET /api/bd/dlp-policies ":                   {NodeStatus: 200, Status: 200, Has: dv7Has, Lacks: dv7Lacks},
	" GET /api/dataguard/dlp-policies ":                    {NodeStatus: 200, Status: 200, Has: dv7Has, Lacks: dv7Lacks},
	" GET /api/dataguard/dlp-policies?device_id=device-b ": {NodeStatus: 200, Status: 200, Has: dv7Has, Lacks: dv7Lacks},
	" GET /API/DATAGUARD/DLP-POLICIES/ ":                   {NodeStatus: 200, Status: 200, Has: dv7Has, Lacks: dv7Lacks},
	" GET /API/BD/DLP-POLICIES/ ":                          {NodeStatus: 200, Status: 200, Has: dv7Has, Lacks: dv7Lacks},
})

var _ = deviceFixes.Add("DV7", map[string]replay.Fix{
	" GET /api/bd/dlp-policies ":        {NodeStatus: 500, Status: 200, Has: dv7Has, Lacks: dv7Lacks},
	" GET /api/dataguard/dlp-policies ": {NodeStatus: 500, Status: 200, Has: dv7Has, Lacks: dv7Lacks},
})

var (
	dv7Has   = []string{`{"id":1,"name":"usb block","rules":[{"type":"usb","action":"block"}]}`}
	dv7Lacks = []string{`"rules":{`, `"rules":0`, `"rules":null`, `"rules":"`, `"name":"enabled text"`}
)

// DG6: the panel access guard covers the routes for people at /api/bd as
// at /api/dataguard, so the roles without the data guard section get its
// refusal there too.
var _ = policyFixes.Add("DG6", guardRefuses(bdSteps()...))

// bdSteps are the steps of the recording at /api/bd that a role without
// the data guard section sends.
func bdSteps() []string {
	var keys []string
	for _, role := range []string{"global_admin", "operator", "viewer", "guest_issuer"} {
		keys = append(keys,
			role+` POST /api/bd/policies {"name":"By `+role+` at /api/bd"}`,
			role+" GET /api/bd/policies ",
			role+" GET /api/bd/policies/1 ",
			role+` PATCH /api/bd/policies/999 {"name":"x"}`,
			role+" DELETE /api/bd/policies/999 ",
			role+" GET /api/bd/events ",
			role+" GET /api/bd/stats ",
		)
	}
	return append(keys, "viewer GET /api/bd/events?device_id=D1 ", "operator GET /api/bd/events?device_id=D1&limit=1 ")
}

// guardRefuses are fixes of steps the panel access guard answers with its
// refusal for the data guard section.
func guardRefuses(keys ...string) map[string]replay.Fix {
	steps := make(map[string]replay.Fix, len(keys))
	for _, key := range keys {
		steps[key] = replay.Fix{Status: 403, Answer: `{"success":false,"error":"Panel access denied: dataguard","code":"panel_access_denied"}`}
	}
	return steps
}

// DG3 on PostgreSQL, where the rows that cannot be read are others.
var _ = policyFixes.Add("DG3", map[string]replay.Fix{
	"admin GET /api/dataguard/events?device_id= ":       {Dialect: "postgres", NodeStatus: 500, Status: 200, Has: []string{`"id":10,"device_id":"D4"`}},
	"admin GET /api/dataguard/events?event_source=usb ": {Dialect: "postgres", NodeStatus: 500, Status: 200, Has: []string{`"id":8,"device_id":"D3"`}},
	"admin GET /api/dataguard/events?DEVICE_ID=D3 ":     {Dialect: "postgres", NodeStatus: 500, Status: 200, Has: []string{`"id":10,"device_id":"D4"`}},
	"admin GET /api/dataguard/events ":                  {Dialect: "postgres", NodeStatus: 500, Status: 200, Has: []string{`"id":10,"device_id":"D4"`}},
	"admin GET /api/bd/events?device_id=D2 ":            {Dialect: "postgres", NodeStatus: 500, Status: 200, Has: []string{`"id":6,"device_id":"D2"`}},
	"admin GET /api/dataguard/policies/903 ":            {Dialect: "postgres", NodeStatus: 500, Status: 200, Has: []string{`"id":903,"name":"rules a broken string"`, `"rules":[]`}},
	"admin GET /api/dataguard/policies ":                {Dialect: "postgres", NodeStatus: 500, Status: 200, Has: []string{`"id":903,"name":"rules a broken string"`, `"id":1,"name":"By admin at /api/dataguard"`}},
})
