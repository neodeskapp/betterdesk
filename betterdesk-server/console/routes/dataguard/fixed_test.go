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
