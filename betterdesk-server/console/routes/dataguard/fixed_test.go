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
	"operator GET /api/dataguard/dlp-policies ":            {NodeStatus: 200, Status: 200, Has: dv7Has, Lacks: dv7Lacks},
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
// at /api/dataguard, so the roles that may read the data guard but not
// write it (device.view without server.config, PA3 F5) get its refusal
// of their writes there too. Their reads answer as the routes say, as at
// /api/bd in the recording.
var _ = policyFixes.Add("DG6", guardRefuses(bdSteps()...))

// bdSteps are the writes of the recording at /api/bd that a role without
// server.config sends.
func bdSteps() []string {
	var keys []string
	for _, role := range []string{"global_admin", "operator", "viewer", "guest_issuer"} {
		keys = append(keys,
			role+` POST /api/bd/policies {"name":"By `+role+` at /api/bd"}`,
			role+` PATCH /api/bd/policies/999 {"name":"x"}`,
			role+" DELETE /api/bd/policies/999 ",
		)
	}
	return keys
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

// DG8: a filter of the event list given twice or as a list filters by its
// first value, and one given as an object does not filter. Node's SQLite
// driver refused a list of two and an object (500); on PostgreSQL every
// list and object was bound as a text no row holds ([]).
var _ = policyFixes.Add("DG8", map[string]replay.Fix{
	"admin GET /api/dataguard/events?device_id=D1&device_id=D2 ":                       {Status: 200, Has: onlyDeviceOne, Lacks: otherDevices},
	"admin GET /api/dataguard/events?device_id[]=D1 ":                                  {Status: 200, Has: onlyDeviceOne, Lacks: otherDevices},
	"admin GET /api/dataguard/events?device_id[0]=D1 ":                                 {Status: 200, Has: onlyDeviceOne, Lacks: otherDevices},
	"admin GET /api/dataguard/events?device_id[x]=D1 ":                                 {Status: 200, Has: append([]string{`{"id":10,"device_id":"D4"`, `{"id":4,"device_id":"D2"`}, deviceOne...)},
	"admin GET /api/dataguard/events?device_id[]=D1&event_source[]=usb ":               {Status: 200, Has: deviceOne[1:], Lacks: append([]string{`"event_source":"file"`}, otherDevices...)},
	"admin GET /api/dataguard/events?device_id=D1&event_source=usb&event_source=file ": {Status: 200, Has: deviceOne[1:], Lacks: append([]string{`"event_source":"file"`}, otherDevices...)},
})

var (
	// deviceOne are the starts of the events of D1, the last first
	// (onlyDeviceOne: in a list that starts with them)
	deviceOne     = []string{`{"id":3,"device_id":"D1"`, `{"id":2,"device_id":"D1","event_source":"usb"`, `{"id":1,"device_id":"D1","event_source":"usb"`}
	otherDevices  = []string{`"device_id":"D2"`, `"device_id":"D3"`, `"device_id":"D4"`}
	onlyDeviceOne = append([]string{"[" + deviceOne[0]}, deviceOne[1:]...)
)

// DG5: the limit of the event list is a whole number from 1 to 500 (the
// first value of a limit given twice, as every filter), and from and to
// are times, compared as times on both databases.
var _ = policyFixes.Add("DG5", map[string]replay.Fix{
	"admin GET /api/dataguard/events?device_id=D1&limit=0 ":         {Status: 400, Answer: `{"error":"Invalid limit"}`},
	"admin GET /api/dataguard/events?device_id=D1&limit=abc ":       {Status: 400, Answer: `{"error":"Invalid limit"}`},
	"admin GET /api/dataguard/events?device_id=D1&limit=1.5 ":       {Status: 400, Answer: `{"error":"Invalid limit"}`},
	"admin GET /api/dataguard/events?device_id=D1&limit=-1 ":        {Status: 400, Answer: `{"error":"Invalid limit"}`},
	"admin GET /api/dataguard/events?device_id=D1&limit=1e400 ":     {Status: 400, Answer: `{"error":"Invalid limit"}`},
	"admin GET /api/dataguard/events?device_id=D1&limit=+2+ ":       {Status: 400, Answer: `{"error":"Invalid limit"}`},
	"admin GET /api/dataguard/events?device_id=D1&limit=0x1 ":       {Status: 400, Answer: `{"error":"Invalid limit"}`},
	"admin GET /api/dataguard/events?device_id=D1&from=2002 ":       {Status: 400, Answer: `{"error":"Invalid from"}`},
	"admin GET /api/dataguard/events?device_id=D1&from=garbage ":    {Status: 400, Answer: `{"error":"Invalid from"}`},
	"admin GET /api/dataguard/events?device_id=D1&to=garbage ":      {Status: 400, Answer: `{"error":"Invalid to"}`},
	"admin GET /api/dataguard/events?device_id=D1&limit=2&limit=3 ": {Status: 200, Has: onlyDeviceOne[:2], Lacks: []string{`"id":1,`}},
	// Node compared the text on SQLite, where a T sorts after the space of
	// the stored time; PostgreSQL compared the times already
	"admin GET /api/dataguard/events?device_id=D1&from=2001-01-01T00:02:00.000Z ": {Status: 200, Has: onlyDeviceOne[:2], Lacks: []string{`"id":1,`}},
})

// DV6: an event of a device has the types the event list shows; anything
// else is 400 and stores nothing. Where Node stored a row for such a
// request, nodeEvent writes it with Node's id, so the ids of the events
// after it, and the rows the recording reads at its end, stay Node's.
//
// Each member of a refused body (beside "event_source":"usb") names the
// member the answer refuses and, per database (SQLite, then PostgreSQL),
// the id of the row Node stored and the value it stored in that member:
// id 0 for no row, a negative id for an id Node's failed insert used up.
var dv6Events = map[string][]any{
	`"event_type":0`:                {"event_type", 5, "info", 5, "info"},
	`"event_type":7`:                {"event_type", 6, "7.0", 6, "7"},
	`"event_type":true`:             {"event_type", 0, nil, 7, "true"},
	`"event_type":["a"]`:            {"event_type", 7, "a", 8, `{"a"}`},
	`"event_type":{"a":1}`:          {"event_type", 0, nil, 9, `{"a":1}`},
	`"policy_id":0`:                 {"policy_id", 8, nil, 10, nil},
	`"policy_id":"1"`:               {"policy_id", 9, 1, 11, 1},
	`"policy_id":"abc"`:             {"policy_id", 0, nil, 0, nil},
	`"policy_id":999999`:            {"policy_id", 0, nil, -12, nil},
	`"policy_id":1.5`:               {"policy_id", 0, nil, 0, nil},
	`"policy_id":[1]`:               {"policy_id", 10, 1, 0, nil},
	`"policy_name":12`:              {"policy_name", 11, "12.0", 13, "12"},
	`"policy_name":false`:           {"policy_name", 12, "", 14, ""},
	`"action":{"a":1}`:              {"action", 0, nil, 17, `{"a":1}`},
	`"details":""`:                  {"details", 15, "{}", 18, "{}"},
	`"details":0`:                   {"details", 17, "{}", 20, "{}"},
	`"details":false`:               {"details", 18, "{}", 21, "{}"},
	`"details":"plain text"`:        {"details", 19, "plain text", 0, nil},
	`"details":"{\"as\":\"text\"}"`: {"details", 20, `{"as":"text"}`, 22, `{"as":"text"}`},
	`"details":42`:                  {"details", 21, "42", 23, "42"},
	`"details":true`:                {"details", 22, "true", 24, "true"},
	`"details":[1,"two",null]`:      {"details", 23, `[1,"two",null]`, 25, `[1,"two",null]`},
}

var (
	_ = deviceFixes.Add("DV6", dv6Steps("sqlite"))
	_ = deviceFixes.Add("DV6", dv6Steps("postgres"))
)

// dv6Steps are the fixes of the refused events on one database.
func dv6Steps(dialect string) map[string]replay.Fix {
	at := map[string]int{"sqlite": 1, "postgres": 3}[dialect]
	steps := map[string]replay.Fix{}
	for member, e := range dv6Events {
		steps[` POST /api/bd/dlp-events {"event_source":"usb",`+member+`}`] = replay.Fix{
			Dialect: dialect, Status: 400, Answer: `{"error":"Invalid ` + e[0].(string) + `"}`,
			After: nodeEvent(e[at].(int), e[0].(string), e[at+1]),
		}
	}
	return steps
}

// nodeEvent writes the event Node stored with the id: a plain event of
// device-a from usb, with the value in the member. An id below 0 is one
// Node's failed insert used up; 0 is none.
func nodeEvent(id int, member string, value any) func(t *testing.T, w *replay.World, step replay.Step) {
	return func(t *testing.T, w *replay.World, step replay.Step) {
		t.Helper()
		sequence := "SELECT setval(pg_get_serial_sequence('dlp_events', 'id'), ?)"
		switch {
		case id < 0:
			w.Exec(sequence, []any{-id})
			return
		case id == 0:
			return
		}
		row := map[string]any{"event_type": "info", "policy_id": nil, "policy_name": "", "action": "log", "details": "{}"}
		row[member] = value
		w.Exec("INSERT INTO dlp_events (id, device_id, event_source, event_type, policy_id, policy_name, action, details) VALUES (?, 'device-a', 'usb', ?, ?, ?, ?, ?)",
			[]any{id, row["event_type"], row["policy_id"], row["policy_name"], row["action"], row["details"]})
		if w.DB.IsPostgres() {
			w.Exec(sequence, []any{id})
		}
	}
}
