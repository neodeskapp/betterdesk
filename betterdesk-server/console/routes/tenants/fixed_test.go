package tenants_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
)

// tenantFixes are the steps of testdata/node_*.json that the bug fixes
// change.
var tenantFixes replay.Fixes

// tenantColumns are the columns of a tenant that a request writes.
var tenantColumns = []string{"name", "slug", "contact_name", "contact_email", "max_devices", "notes", "active"}

// nodeTenant writes the tenant Node's answer holds where Node created or
// changed one, so the steps after it see the rows Node had. Where Node
// failed it does nothing.
func nodeTenant(t *testing.T, w *replay.World, step replay.Step) {
	t.Helper()
	if step.Status != 200 && step.Status != 201 {
		return
	}
	var row map[string]json.RawMessage
	if err := json.Unmarshal([]byte(step.Answer), &row); err != nil {
		t.Fatalf("%s: Node's answer is no tenant: %v", step.Name, err)
	}
	values := make([]any, len(tenantColumns))
	for i, column := range tenantColumns {
		var v any
		if err := json.Unmarshal(row[column], &v); err != nil {
			t.Fatal(err)
		}
		if n, isNumber := v.(float64); isNumber && n == float64(int64(n)) {
			v = int64(n)
		}
		values[i] = v
	}
	var id int64
	if err := json.Unmarshal(row["id"], &id); err != nil {
		t.Fatal(err)
	}
	if step.Status == 201 {
		// With Node's id: a failed insert of Node's can have used an id of
		// the PostgreSQL sequence, which the next tenant then does not get
		w.Exec("INSERT INTO tenants (id, "+strings.Join(tenantColumns, ", ")+") VALUES (?, ?, ?, ?, ?, ?, ?, ?)", append([]any{id}, values...))
		if w.DB.IsPostgres() {
			w.Exec("SELECT setval(pg_get_serial_sequence('tenants', 'id'), (SELECT MAX(id) FROM tenants))", nil)
		}
		return
	}
	sets := make([]string, len(tenantColumns))
	for i, column := range tenantColumns {
		sets[i] = column + " = ?"
	}
	w.Exec("UPDATE tenants SET "+strings.Join(sets, ", ")+", updated_at = "+store.NowSQL(w.DB.Dialect())+" WHERE id = ?", append(values, id))
}

var sessionRequired = `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`

// TN6: someone who is not signed in gets the console's 401.
var _ = tenantFixes.Add("TN6", map[string]replay.Fix{
	" GET /api/tenants ":                               {Status: 401, Answer: sessionRequired},
	" GET /api/tenants/1 ":                             {Status: 401, Answer: sessionRequired},
	` POST /api/tenants {"name":"By nobody"}`:          {Status: 401, Answer: sessionRequired},
	` PATCH /api/tenants/999 {"notes":"x"}`:            {Status: 401, Answer: sessionRequired},
	" DELETE /api/tenants/999 ":                        {Status: 401, Answer: sessionRequired},
	" GET /api/tenants/999/devices ":                   {Status: 401, Answer: sessionRequired},
	` POST /api/tenants/999/devices {"device_id":"x"}`: {Status: 401, Answer: sessionRequired},
	" DELETE /api/tenants/999/devices/x ":              {Status: 401, Answer: sessionRequired},
	" GET /api/tenants/999/users ":                     {Status: 401, Answer: sessionRequired},
	` POST /api/tenants/999/users {"user_id":1}`:       {Status: 401, Answer: sessionRequired},
	" DELETE /api/tenants/999/users/1 ":                {Status: 401, Answer: sessionRequired},
	" GET /api/tenants/999/stats ":                     {Status: 401, Answer: sessionRequired},
})

// A pro account is turned away by the console on its first request, which
// ends its session; the requests after it are signed out.
var _ = tenantFixes.Add("TN6", map[string]replay.Fix{
	"pro GET /api/tenants ":                               {Status: 403, Answer: `{"success":false,"error":"Pro accounts can only access the RustDesk desktop client API"}`},
	`pro GET /api/tenants/1 `:                             {Status: 401, Answer: sessionRequired},
	`pro POST /api/tenants {"name":"By pro"}`:             {Status: 401, Answer: sessionRequired},
	`pro PATCH /api/tenants/999 {"notes":"x"}`:            {Status: 401, Answer: sessionRequired},
	`pro DELETE /api/tenants/999 `:                        {Status: 401, Answer: sessionRequired},
	`pro GET /api/tenants/999/devices `:                   {Status: 401, Answer: sessionRequired},
	`pro POST /api/tenants/999/devices {"device_id":"x"}`: {Status: 401, Answer: sessionRequired},
	`pro DELETE /api/tenants/999/devices/x `:              {Status: 401, Answer: sessionRequired},
	`pro GET /api/tenants/999/users `:                     {Status: 401, Answer: sessionRequired},
	`pro POST /api/tenants/999/users {"user_id":1}`:       {Status: 401, Answer: sessionRequired},
	`pro DELETE /api/tenants/999/users/1 `:                {Status: 401, Answer: sessionRequired},
	`pro GET /api/tenants/999/stats `:                     {Status: 401, Answer: sessionRequired},
})

// TN1: a tenant's texts are text and its device limit a whole number.
var _ = tenantFixes.Add("TN1", map[string]replay.Fix{
	`admin POST /api/tenants {"name":"Tenant 1","slug":0}`:                                            {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 3","slug":77}`:                                           {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 4","slug":1.5}`:                                          {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 5","slug":-1}`:                                           {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 6","slug":1e+21}`:                                        {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 7","slug":1e-7}`:                                         {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 8","slug":true}`:                                         {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 9","slug":false}`:                                        {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 11","slug":[]}`:                                          {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 12","slug":["one"]}`:                                     {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 13","slug":["a","b"]}`:                                   {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 14","slug":[5]}`:                                         {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 15","slug":[null]}`:                                      {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 16","slug":[[1]]}`:                                       {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 17","slug":[true]}`:                                      {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 18","slug":{}}`:                                          {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 19","slug":{"a":1}}`:                                     {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 29","slug":99999999999}`:                                 {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 30","slug":2147483648}`:                                  {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 31","slug":-2147483649}`:                                 {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 32","slug":{"2":"x","b":[1,{"c":null}],"a":1}}`:          {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 33","slug":[1,[2,"x\"y\\z"],null,"q"]}`:                  {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 34","contact_name":0}`:                                   {Status: 400, Answer: `{"error":"Invalid contact_name"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 36","contact_name":77}`:                                  {Status: 400, Answer: `{"error":"Invalid contact_name"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 37","contact_name":1.5}`:                                 {Status: 400, Answer: `{"error":"Invalid contact_name"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 38","contact_name":-1}`:                                  {Status: 400, Answer: `{"error":"Invalid contact_name"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 39","contact_name":1e+21}`:                               {Status: 400, Answer: `{"error":"Invalid contact_name"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 40","contact_name":1e-7}`:                                {Status: 400, Answer: `{"error":"Invalid contact_name"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 41","contact_name":true}`:                                {Status: 400, Answer: `{"error":"Invalid contact_name"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 42","contact_name":false}`:                               {Status: 400, Answer: `{"error":"Invalid contact_name"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 44","contact_name":[]}`:                                  {Status: 400, Answer: `{"error":"Invalid contact_name"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 45","contact_name":["one"]}`:                             {Status: 400, Answer: `{"error":"Invalid contact_name"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 46","contact_name":["a","b"]}`:                           {Status: 400, Answer: `{"error":"Invalid contact_name"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 47","contact_name":[5]}`:                                 {Status: 400, Answer: `{"error":"Invalid contact_name"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 48","contact_name":[null]}`:                              {Status: 400, Answer: `{"error":"Invalid contact_name"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 49","contact_name":[[1]]}`:                               {Status: 400, Answer: `{"error":"Invalid contact_name"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 50","contact_name":[true]}`:                              {Status: 400, Answer: `{"error":"Invalid contact_name"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 51","contact_name":{}}`:                                  {Status: 400, Answer: `{"error":"Invalid contact_name"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 52","contact_name":{"a":1}}`:                             {Status: 400, Answer: `{"error":"Invalid contact_name"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 62","contact_name":99999999999}`:                         {Status: 400, Answer: `{"error":"Invalid contact_name"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 63","contact_name":2147483648}`:                          {Status: 400, Answer: `{"error":"Invalid contact_name"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 64","contact_name":-2147483649}`:                         {Status: 400, Answer: `{"error":"Invalid contact_name"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 65","contact_name":{"2":"x","b":[1,{"c":null}],"a":1}}`:  {Status: 400, Answer: `{"error":"Invalid contact_name"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 66","contact_name":[1,[2,"x\"y\\z"],null,"q"]}`:          {Status: 400, Answer: `{"error":"Invalid contact_name"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 67","contact_email":0}`:                                  {Status: 400, Answer: `{"error":"Invalid contact_email"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 69","contact_email":77}`:                                 {Status: 400, Answer: `{"error":"Invalid contact_email"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 70","contact_email":1.5}`:                                {Status: 400, Answer: `{"error":"Invalid contact_email"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 71","contact_email":-1}`:                                 {Status: 400, Answer: `{"error":"Invalid contact_email"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 72","contact_email":1e+21}`:                              {Status: 400, Answer: `{"error":"Invalid contact_email"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 73","contact_email":1e-7}`:                               {Status: 400, Answer: `{"error":"Invalid contact_email"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 74","contact_email":true}`:                               {Status: 400, Answer: `{"error":"Invalid contact_email"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 75","contact_email":false}`:                              {Status: 400, Answer: `{"error":"Invalid contact_email"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 77","contact_email":[]}`:                                 {Status: 400, Answer: `{"error":"Invalid contact_email"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 78","contact_email":["one"]}`:                            {Status: 400, Answer: `{"error":"Invalid contact_email"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 79","contact_email":["a","b"]}`:                          {Status: 400, Answer: `{"error":"Invalid contact_email"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 80","contact_email":[5]}`:                                {Status: 400, Answer: `{"error":"Invalid contact_email"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 81","contact_email":[null]}`:                             {Status: 400, Answer: `{"error":"Invalid contact_email"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 82","contact_email":[[1]]}`:                              {Status: 400, Answer: `{"error":"Invalid contact_email"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 83","contact_email":[true]}`:                             {Status: 400, Answer: `{"error":"Invalid contact_email"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 84","contact_email":{}}`:                                 {Status: 400, Answer: `{"error":"Invalid contact_email"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 85","contact_email":{"a":1}}`:                            {Status: 400, Answer: `{"error":"Invalid contact_email"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 95","contact_email":99999999999}`:                        {Status: 400, Answer: `{"error":"Invalid contact_email"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 96","contact_email":2147483648}`:                         {Status: 400, Answer: `{"error":"Invalid contact_email"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 97","contact_email":-2147483649}`:                        {Status: 400, Answer: `{"error":"Invalid contact_email"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 98","contact_email":{"2":"x","b":[1,{"c":null}],"a":1}}`: {Status: 400, Answer: `{"error":"Invalid contact_email"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 99","contact_email":[1,[2,"x\"y\\z"],null,"q"]}`:         {Status: 400, Answer: `{"error":"Invalid contact_email"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 101","max_devices":""}`:                                  {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 103","max_devices":1.5}`:                                 {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 104","max_devices":-1}`:                                  {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 105","max_devices":1e+21}`:                               {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 106","max_devices":1e-7}`:                                {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 107","max_devices":true}`:                                {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 108","max_devices":false}`:                               {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 110","max_devices":[]}`:                                  {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 111","max_devices":["one"]}`:                             {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 112","max_devices":["a","b"]}`:                           {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 113","max_devices":[5]}`:                                 {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 114","max_devices":[null]}`:                              {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 115","max_devices":[[1]]}`:                               {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 116","max_devices":[true]}`:                              {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 117","max_devices":{}}`:                                  {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 118","max_devices":{"a":1}}`:                             {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 119","max_devices":"UNIQUE"}`:                            {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 120","max_devices":"plain text"}`:                        {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 121","max_devices":" 12 "}`:                              {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 122","max_devices":"12"}`:                                {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 123","max_devices":"0x10"}`:                              {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 124","max_devices":"1e3"}`:                               {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 125","max_devices":"1.5"}`:                               {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 126","max_devices":"abc"}`:                               {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 127","max_devices":"99999999999"}`:                       {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 128","max_devices":99999999999}`:                         {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 129","max_devices":2147483648}`:                          {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 130","max_devices":-2147483649}`:                         {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 131","max_devices":{"2":"x","b":[1,{"c":null}],"a":1}}`:  {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 132","max_devices":[1,[2,"x\"y\\z"],null,"q"]}`:          {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 133","notes":0}`:                                         {Status: 400, Answer: `{"error":"Invalid notes"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 135","notes":77}`:                                        {Status: 400, Answer: `{"error":"Invalid notes"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 136","notes":1.5}`:                                       {Status: 400, Answer: `{"error":"Invalid notes"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 137","notes":-1}`:                                        {Status: 400, Answer: `{"error":"Invalid notes"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 138","notes":1e+21}`:                                     {Status: 400, Answer: `{"error":"Invalid notes"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 139","notes":1e-7}`:                                      {Status: 400, Answer: `{"error":"Invalid notes"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 140","notes":true}`:                                      {Status: 400, Answer: `{"error":"Invalid notes"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 141","notes":false}`:                                     {Status: 400, Answer: `{"error":"Invalid notes"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 143","notes":[]}`:                                        {Status: 400, Answer: `{"error":"Invalid notes"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 144","notes":["one"]}`:                                   {Status: 400, Answer: `{"error":"Invalid notes"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 145","notes":["a","b"]}`:                                 {Status: 400, Answer: `{"error":"Invalid notes"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 146","notes":[5]}`:                                       {Status: 400, Answer: `{"error":"Invalid notes"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 147","notes":[null]}`:                                    {Status: 400, Answer: `{"error":"Invalid notes"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 148","notes":[[1]]}`:                                     {Status: 400, Answer: `{"error":"Invalid notes"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 149","notes":[true]}`:                                    {Status: 400, Answer: `{"error":"Invalid notes"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 150","notes":{}}`:                                        {Status: 400, Answer: `{"error":"Invalid notes"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 151","notes":{"a":1}}`:                                   {Status: 400, Answer: `{"error":"Invalid notes"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 161","notes":99999999999}`:                               {Status: 400, Answer: `{"error":"Invalid notes"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 162","notes":2147483648}`:                                {Status: 400, Answer: `{"error":"Invalid notes"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 163","notes":-2147483649}`:                               {Status: 400, Answer: `{"error":"Invalid notes"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 164","notes":{"2":"x","b":[1,{"c":null}],"a":1}}`:        {Status: 400, Answer: `{"error":"Invalid notes"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 165","notes":[1,[2,"x\"y\\z"],null,"q"]}`:                {Status: 400, Answer: `{"error":"Invalid notes"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 199","max_devices":1e400}`:                               {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 200","notes":-1e400}`:                                    {Status: 400, Answer: `{"error":"Invalid notes"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 201","slug":12345678901234567890}`:                       {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 202","slug":{},"max_devices":[5,"x"]}`:                   {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 203","slug":{},"notes":{}}`:                              {Status: 400, Answer: `{"error":"Invalid slug"}`, After: nodeTenant},
	`admin POST /api/tenants {"name":"Tenant 204","contact_name":["a","b"],"contact_email":{}}`:       {Status: 400, Answer: `{"error":"Invalid contact_name"}`, After: nodeTenant},
})

const nameRequired = `{"error":"Tenant name is required"}`

// TN7: a change checks every member as the create does; a tenant keeps a
// name, and active is a flag.
var _ = tenantFixes.Add("TN7", map[string]replay.Fix{
	`admin PATCH /api/tenants/2 {"name":null}`:                                         {Status: 400, Answer: nameRequired, After: nodeTenant},
	`admin PATCH /api/tenants/2 {"name":5}`:                                            {Status: 400, Answer: nameRequired, After: nodeTenant},
	`admin PATCH /api/tenants/2 {"name":true}`:                                         {Status: 400, Answer: nameRequired, After: nodeTenant},
	`admin PATCH /api/tenants/2 {"name":""}`:                                           {Status: 400, Answer: nameRequired, After: nodeTenant},
	`admin PATCH /api/tenants/2 {"name":["a","b"]}`:                                    {Status: 400, Answer: nameRequired, After: nodeTenant},
	`admin PATCH /api/tenants/2 {"name":{}}`:                                           {Status: 400, Answer: nameRequired, After: nodeTenant},
	`admin PATCH /api/tenants/2 {"contact_name":null,"contact_email":7,"notes":["x"]}`: {Status: 400, Answer: `{"error":"Invalid contact_email"}`, After: nodeTenant},
	`admin PATCH /api/tenants/2 {"max_devices":"abc"}`:                                 {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin PATCH /api/tenants/2 {"max_devices":null}`:                                  {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin PATCH /api/tenants/2 {"max_devices":"0x20"}`:                                {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin PATCH /api/tenants/2 {"max_devices":7.5}`:                                   {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin PATCH /api/tenants/2 {"notes":{},"max_devices":[1,2]}`:                      {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin PATCH /api/tenants/2 {"max_devices":"UNIQUE"}`:                              {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin PATCH /api/tenants/3 {"max_devices":null}`:                                  {Status: 400, Answer: `{"error":"Invalid max_devices"}`, After: nodeTenant},
	`admin PATCH /api/tenants/2 {"active":"0"}`:                                        {Status: 400, Answer: `{"error":"Invalid active"}`, After: nodeTenant},
	`admin PATCH /api/tenants/2 {"active":null}`:                                       {Status: 400, Answer: `{"error":"Invalid active"}`, After: nodeTenant},
	`admin PATCH /api/tenants/2 {"active":[]}`:                                         {Status: 400, Answer: `{"error":"Invalid active"}`, After: nodeTenant},
	`admin PATCH /api/tenants/2 {"active":""}`:                                         {Status: 400, Answer: `{"error":"Invalid active"}`, After: nodeTenant},
	`admin PATCH /api/tenants/2 {"active":"false"}`:                                    {Status: 400, Answer: `{"error":"Invalid active"}`, After: nodeTenant},
})
