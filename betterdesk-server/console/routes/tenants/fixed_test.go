package tenants_test

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/jsbind"
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

// TN3: an id is the digits of a positive 32-bit number, as written.
var _ = tenantFixes.Add("TN3", map[string]replay.Fix{
	`admin GET /api/tenants/1.0 `:                                                                   {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1.0/devices `:                                                           {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1.0/users `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1.0/stats `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/1.0 {"contact_email":"set-by-1.0"}`:                                   {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/1.0/devices {"device_id":"spelling-2"}`:                                {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/1.0/devices/spelling-2 `:                                             {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/1.0/users {"user_id":7}`:                                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/1.0/users/7 `:                                                        {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/1e0 `:                                                                   {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1e0/devices `:                                                           {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1e0/users `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1e0/stats `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/1e0 {"contact_email":"set-by-1e0"}`:                                   {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/1e0/devices {"device_id":"spelling-3"}`:                                {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/1e0/devices/spelling-3 `:                                             {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/1e0/users {"user_id":7}`:                                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/1e0/users/7 `:                                                        {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/%201 `:                                                                  {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/%201/devices `:                                                          {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/%201/users `:                                                            {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/%201/stats `:                                                            {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/%201 {"contact_email":"set-by-%201"}`:                                 {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/%201/devices {"device_id":"spelling-4"}`:                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/%201/devices/spelling-4 `:                                            {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/%201/users {"user_id":7}`:                                              {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/%201/users/7 `:                                                       {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/1%20 `:                                                                  {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1%20/devices `:                                                          {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1%20/users `:                                                            {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1%20/stats `:                                                            {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/1%20 {"contact_email":"set-by-1%20"}`:                                 {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/1%20/devices {"device_id":"spelling-5"}`:                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/1%20/devices/spelling-5 `:                                            {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/1%20/users {"user_id":7}`:                                              {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/1%20/users/7 `:                                                       {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/0x1 `:                                                                   {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/0x1/devices `:                                                           {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/0x1/users `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/0x1/stats `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/0x1 {"contact_email":"set-by-0x1"}`:                                   {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/0x1/devices {"device_id":"spelling-6"}`:                                {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/0x1/devices/spelling-6 `:                                             {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/0x1/users {"user_id":7}`:                                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/0x1/users/7 `:                                                        {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/0X1 `:                                                                   {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/0X1/devices `:                                                           {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/0X1/users `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/0X1/stats `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/0X1 {"contact_email":"set-by-0X1"}`:                                   {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/0X1/devices {"device_id":"spelling-7"}`:                                {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/0X1/devices/spelling-7 `:                                             {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/0X1/users {"user_id":7}`:                                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/0X1/users/7 `:                                                        {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/0b1 `:                                                                   {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/0b1/devices `:                                                           {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/0b1/users `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/0b1/stats `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/0b1 {"contact_email":"set-by-0b1"}`:                                   {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/0b1/devices {"device_id":"spelling-8"}`:                                {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/0b1/devices/spelling-8 `:                                             {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/0b1/users {"user_id":7}`:                                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/0b1/users/7 `:                                                        {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/0o1 `:                                                                   {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/0o1/devices `:                                                           {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/0o1/users `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/0o1/stats `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/0o1 {"contact_email":"set-by-0o1"}`:                                   {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/0o1/devices {"device_id":"spelling-9"}`:                                {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/0o1/devices/spelling-9 `:                                             {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/0o1/users {"user_id":7}`:                                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/0o1/users/7 `:                                                        {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/+1 `:                                                                    {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/+1/devices `:                                                            {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/+1/users `:                                                              {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/+1/stats `:                                                              {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/+1 {"contact_email":"set-by-+1"}`:                                     {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/+1/devices {"device_id":"spelling-10"}`:                                {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/+1/devices/spelling-10 `:                                             {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/+1/users {"user_id":7}`:                                                {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/+1/users/7 `:                                                         {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/-1 `:                                                                    {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/-1/devices `:                                                            {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/-1/users `:                                                              {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/-1/stats `:                                                              {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/-1 {"contact_email":"set-by--1"}`:                                     {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/-1/devices {"device_id":"spelling-11"}`:                                {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/-1/devices/spelling-11 `:                                             {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/-1/users {"user_id":7}`:                                                {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/-1/users/7 `:                                                         {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/1.5 `:                                                                   {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1.5/devices `:                                                           {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1.5/users `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1.5/stats `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/1.5 {"contact_email":"set-by-1.5"}`:                                   {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/1.5/devices {"device_id":"spelling-12"}`:                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/1.5/devices/spelling-12 `:                                            {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/1.5/users {"user_id":7}`:                                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/1.5/users/7 `:                                                        {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/abc `:                                                                   {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/abc/devices `:                                                           {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/abc/users `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/abc/stats `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/abc {"contact_email":"set-by-abc"}`:                                   {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/abc/devices {"device_id":"spelling-13"}`:                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/abc/devices/spelling-13 `:                                            {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/abc/users {"user_id":7}`:                                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/abc/users/7 `:                                                        {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/NaN `:                                                                   {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/NaN/devices `:                                                           {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/NaN/users `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/NaN/stats `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/NaN {"contact_email":"set-by-NaN"}`:                                   {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/NaN/devices {"device_id":"spelling-14"}`:                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/NaN/devices/spelling-14 `:                                            {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/NaN/users {"user_id":7}`:                                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/NaN/users/7 `:                                                        {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/Infinity `:                                                              {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/Infinity/devices `:                                                      {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/Infinity/users `:                                                        {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/Infinity/stats `:                                                        {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/Infinity {"contact_email":"set-by-Infinity"}`:                         {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/Infinity/devices {"device_id":"spelling-15"}`:                          {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/Infinity/devices/spelling-15 `:                                       {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/Infinity/users {"user_id":7}`:                                          {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/Infinity/users/7 `:                                                   {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/-Infinity `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/-Infinity/devices `:                                                     {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/-Infinity/users `:                                                       {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/-Infinity/stats `:                                                       {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/-Infinity {"contact_email":"set-by--Infinity"}`:                       {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/-Infinity/devices {"device_id":"spelling-16"}`:                         {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/-Infinity/devices/spelling-16 `:                                      {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/-Infinity/users {"user_id":7}`:                                         {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/-Infinity/users/7 `:                                                  {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/0 `:                                                                     {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/0/devices `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/0/users `:                                                               {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/0/stats `:                                                               {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/0 {"contact_email":"set-by-0"}`:                                       {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/0/devices {"device_id":"spelling-18"}`:                                 {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/0/devices/spelling-18 `:                                              {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/0/users {"user_id":7}`:                                                 {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/0/users/7 `:                                                          {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/-0 `:                                                                    {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/-0/devices `:                                                            {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/-0/users `:                                                              {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/-0/stats `:                                                              {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/-0 {"contact_email":"set-by--0"}`:                                     {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/-0/devices {"device_id":"spelling-19"}`:                                {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/-0/devices/spelling-19 `:                                             {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/-0/users {"user_id":7}`:                                                {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/-0/users/7 `:                                                         {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/3000000000 `:                                                            {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/3000000000/devices `:                                                    {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/3000000000/users `:                                                      {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/3000000000/stats `:                                                      {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/3000000000 {"contact_email":"set-by-3000000000"}`:                     {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/3000000000/devices {"device_id":"spelling-20"}`:                        {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/3000000000/devices/spelling-20 `:                                     {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/3000000000/users {"user_id":7}`:                                        {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/3000000000/users/7 `:                                                 {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/99999999999999999999 `:                                                  {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/99999999999999999999/devices `:                                          {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/99999999999999999999/users `:                                            {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/99999999999999999999/stats `:                                            {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/99999999999999999999 {"contact_email":"set-by-99999999999999999999"}`: {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/99999999999999999999/devices {"device_id":"spelling-21"}`:              {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/99999999999999999999/devices/spelling-21 `:                           {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/99999999999999999999/users {"user_id":7}`:                              {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/99999999999999999999/users/7 `:                                       {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/1e400 `:                                                                 {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1e400/devices `:                                                         {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1e400/users `:                                                           {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1e400/stats `:                                                           {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/1e400 {"contact_email":"set-by-1e400"}`:                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/1e400/devices {"device_id":"spelling-22"}`:                             {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/1e400/devices/spelling-22 `:                                          {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/1e400/users {"user_id":7}`:                                             {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/1e400/users/7 `:                                                      {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/%20 `:                                                                   {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/%20/devices `:                                                           {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/%20/users `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/%20/stats `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/%20 {"contact_email":"set-by-%20"}`:                                   {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/%20/devices {"device_id":"spelling-23"}`:                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/%20/devices/spelling-23 `:                                            {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/%20/users {"user_id":7}`:                                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/%20/users/7 `:                                                        {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/1_0 `:                                                                   {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1_0/devices `:                                                           {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1_0/users `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1_0/stats `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/1_0 {"contact_email":"set-by-1_0"}`:                                   {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/1_0/devices {"device_id":"spelling-24"}`:                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/1_0/devices/spelling-24 `:                                            {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/1_0/users {"user_id":7}`:                                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/1_0/users/7 `:                                                        {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/%EF%BC%91 `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/%EF%BC%91/devices `:                                                     {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/%EF%BC%91/users `:                                                       {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/%EF%BC%91/stats `:                                                       {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/%EF%BC%91 {"contact_email":"set-by-%EF%BC%91"}`:                       {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/%EF%BC%91/devices {"device_id":"spelling-25"}`:                         {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/%EF%BC%91/devices/spelling-25 `:                                      {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/%EF%BC%91/users {"user_id":7}`:                                         {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/%EF%BC%91/users/7 `:                                                  {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/1;x `:                                                                   {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1;x/devices `:                                                           {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1;x/users `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1;x/stats `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/1;x {"contact_email":"set-by-1;x"}`:                                   {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/1;x/devices {"device_id":"spelling-26"}`:                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/1;x/devices/spelling-26 `:                                            {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/1;x/users {"user_id":7}`:                                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/1;x/users/7 `:                                                        {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/1%2C2 `:                                                                 {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1%2C2/devices `:                                                         {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1%2C2/users `:                                                           {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1%2C2/stats `:                                                           {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/1%2C2 {"contact_email":"set-by-1%2C2"}`:                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/1%2C2/devices {"device_id":"spelling-27"}`:                             {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/1%2C2/devices/spelling-27 `:                                          {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/1%2C2/users {"user_id":7}`:                                             {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/1%2C2/users/7 `:                                                      {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/.5e1 `:                                                                  {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/.5e1/devices `:                                                          {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/.5e1/users `:                                                            {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/.5e1/stats `:                                                            {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/.5e1 {"contact_email":"set-by-.5e1"}`:                                 {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/.5e1/devices {"device_id":"spelling-28"}`:                              {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/.5e1/devices/spelling-28 `:                                           {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/.5e1/users {"user_id":7}`:                                              {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/.5e1/users/7 `:                                                       {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/5. `:                                                                    {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/5./devices `:                                                            {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/5./users `:                                                              {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/5./stats `:                                                              {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/5. {"contact_email":"set-by-5."}`:                                     {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/5./devices {"device_id":"spelling-29"}`:                                {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/5./devices/spelling-29 `:                                             {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/5./users {"user_id":7}`:                                                {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/5./users/7 `:                                                         {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/1e `:                                                                    {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1e/devices `:                                                            {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1e/users `:                                                              {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/1e/stats `:                                                              {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/1e {"contact_email":"set-by-1e"}`:                                     {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/1e/devices {"device_id":"spelling-30"}`:                                {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/1e/devices/spelling-30 `:                                             {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/1e/users {"user_id":7}`:                                                {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/1e/users/7 `:                                                         {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/--1 `:                                                                   {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/--1/devices `:                                                           {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/--1/users `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/--1/stats `:                                                             {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/--1 {"contact_email":"set-by---1"}`:                                   {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/--1/devices {"device_id":"spelling-31"}`:                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/--1/devices/spelling-31 `:                                            {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/--1/users {"user_id":7}`:                                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/--1/users/7 `:                                                        {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/null `:                                                                  {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/null/devices `:                                                          {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/null/users `:                                                            {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/null/stats `:                                                            {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/null {"contact_email":"set-by-null"}`:                                 {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/null/devices {"device_id":"spelling-32"}`:                              {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/null/devices/spelling-32 `:                                           {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/null/users {"user_id":7}`:                                              {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/null/users/7 `:                                                       {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin GET /api/tenants/true `:                                                                  {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/true/devices `:                                                          {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/true/users `:                                                            {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin GET /api/tenants/true/stats `:                                                            {Status: 404, Answer: `{"error":"Tenant not found"}`},
	`admin PATCH /api/tenants/true {"contact_email":"set-by-true"}`:                                 {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeTenant},
	`admin POST /api/tenants/true/devices {"device_id":"spelling-33"}`:                              {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/true/devices/spelling-33 `:                                           {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin POST /api/tenants/true/users {"user_id":7}`:                                              {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeAssigned},
	`admin DELETE /api/tenants/true/users/7 `:                                                       {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin DELETE /api/tenants/1/users/1.5 `:                                                        {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin DELETE /api/tenants/1/users/2.5 `:                                                        {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin DELETE /api/tenants/1/users/abc `:                                                        {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin DELETE /api/tenants/1/users/0x5 `:                                                        {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin DELETE /api/tenants/1/users/%203 `:                                                       {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin DELETE /api/tenants/1/users/Infinity `:                                                   {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin DELETE /api/tenants/1/users/-1 `:                                                         {Status: 404, Answer: `{"error":"Assignment not found"}`, After: nodeRemoved},
	`admin DELETE /api/tenants/2.0 `:                                                                {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeRemoved},
	`admin DELETE /api/tenants/abc `:                                                                {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeRemoved},
	`admin DELETE /api/tenants/1e400 `:                                                              {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeRemoved},
	`admin DELETE /api/tenants/3.5 `:                                                                {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeRemoved},
	`admin DELETE /api/tenants/%204 `:                                                               {Status: 404, Answer: `{"error":"Tenant not found"}`, After: nodeRemoved},
})

// nodeNumber is Number() of a part of the step's path, as Node read ids.
func nodeNumber(t *testing.T, step replay.Step, part int) float64 {
	t.Helper()
	parts := strings.Split(strings.SplitN(step.Path, "?", 2)[0], "/")
	text, err := url.PathUnescape(parts[part])
	if err != nil {
		t.Fatal(err)
	}
	return jsbind.ParseNumber(text)
}

// nodeAssigned writes the assignment Node made for an id spelled in a way
// the Go console refuses, so the ids and times of the rows after it stay
// as Node's. Where Node's insert failed on PostgreSQL for a tenant that
// does not exist, it still used an id of the table's sequence.
func nodeAssigned(t *testing.T, w *replay.World, step replay.Step) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(*step.Body), &body); err != nil {
		t.Fatal(err)
	}
	tenant := nodeNumber(t, step, 3)
	device, isDevice := body["device_id"].(string)
	table := map[bool]string{true: "tenant_devices", false: "tenant_users"}[isDevice]
	made := strings.Contains(step.Answer, `"assigned":1`) || step.Answer == `{"ok":true}`
	switch {
	case step.Status != 200:
	case made && isDevice:
		w.Exec("INSERT INTO tenant_devices (tenant_id, device_id) VALUES (?, ?)", []any{int64(tenant), device})
	case made:
		w.Exec("INSERT INTO tenant_users (tenant_id, user_id) VALUES (?, ?)", []any{int64(tenant), body["user_id"]})
	case w.DB.IsPostgres() && tenant == math.Trunc(tenant) && math.Abs(tenant) <= math.MaxInt32:
		w.Exec("SELECT nextval(pg_get_serial_sequence('"+table+"', 'id'))", nil)
	}
}

// nodeRemoved removes what Node removed for an id spelled in a way the Go
// console refuses: a tenant, or an assignment.
func nodeRemoved(t *testing.T, w *replay.World, step replay.Step) {
	t.Helper()
	if step.Status != 200 {
		return
	}
	tenant := int64(nodeNumber(t, step, 3))
	parts := strings.Split(step.Path, "/")
	switch {
	case len(parts) == 4:
		w.Exec("DELETE FROM tenants WHERE id = ?", []any{tenant})
	case parts[4] == "devices":
		device, _ := url.PathUnescape(parts[5])
		w.Exec("DELETE FROM tenant_devices WHERE tenant_id = ? AND device_id = ?", []any{tenant, device})
	default:
		user := nodeNumber(t, step, 5)
		if math.IsInf(user, 0) {
			// SQLite holds an infinite id as the real 9e999
			w.Exec("DELETE FROM tenant_users WHERE tenant_id = ? AND user_id = 9e999", []any{tenant})
			return
		}
		var id any = user
		if user == math.Trunc(user) {
			id = int64(user)
		}
		w.Exec("DELETE FROM tenant_users WHERE tenant_id = ? AND user_id = ?", []any{tenant, id})
	}
}

const slugTaken = `{"error":"Tenant slug already exists"}`

// TN2: a taken slug is 409 on both databases, in a create and a change.
var _ = tenantFixes.Add("TN2", map[string]replay.Fix{
	`admin PATCH /api/tenants/2 {"slug":"acme-corp"}`: {Status: 409, Answer: slugTaken},
	`admin PATCH /api/tenants/2 {"slug":"UNIQUE"}`:    {Status: 409, Answer: slugTaken},
})

var _ = tenantFixes.Add("TN2", map[string]replay.Fix{
	`admin POST /api/tenants {"name":"acme corp"}`:              {Dialect: "postgres", Status: 409, Answer: slugTaken},
	`admin POST /api/tenants {"name":"ACME--CORP!!"}`:           {Dialect: "postgres", Status: 409, Answer: slugTaken},
	`admin POST /api/tenants {"name":"???"}`:                    {Dialect: "postgres", Status: 409, Answer: slugTaken},
	`admin POST /api/tenants {"name":"Tenant 26","slug":"1.5"}`: {Dialect: "postgres", Status: 409, Answer: slugTaken},
})

// tenantSteps are fixed steps of a request the recording sends at several
// points, where a fix changes the answer at some of them only: lists read
// after a change. They are named by the database and the step's name.
var tenantSteps = map[string]replay.Fix{}

// addSteps puts steps on tenantSteps.
func addSteps(bug string, steps map[string]replay.Fix) bool {
	for name, f := range steps {
		if _, twice := tenantSteps[name]; twice {
			panic("two fixes for " + name)
		}
		f.Bug = bug
		tenantSteps[name] = f
	}
	return true
}

// stepsCheck compares the steps of tenantSteps with the answers their
// fixes give. done fails for the steps of the database it did not see.
func stepsCheck(db *store.DB) (check replay.Check, done func(t *testing.T)) {
	prefix := db.Dialect().String() + ": "
	seen := map[string]bool{}
	check = func(t *testing.T, step replay.Step, got replay.Answer) bool {
		f, listed := tenantSteps[prefix+step.Name]
		if !listed {
			return false
		}
		seen[prefix+step.Name] = true
		if got.Status != f.Status || replay.Normalize(got.Body) != replay.Normalize(f.Answer) {
			t.Errorf("%s (%s)\n   fix: %d %s\n    go: %d %s", step.Name, f.Bug, f.Status, f.Answer, got.Status, got.Body)
		}
		return true
	}
	done = func(t *testing.T) {
		for name, f := range tenantSteps {
			if strings.HasPrefix(name, prefix) && !seen[name] {
				t.Errorf("the fix of %s names no step of the recording: %s", f.Bug, name)
			}
		}
	}
	return check, done
}

// TN8: assigning a device or a user again leaves the assignment as it is,
// so it stays where its first assignment put it in the lists. On SQLite
// Node replaced the row with a new one; PostgreSQL kept it already.
var _ = addSteps("TN8", map[string]replay.Fix{
	"sqlite: 741 GET /api/tenants/1/users": {Status: 200, Answer: `[{"user_id":null,"assigned_at":"2001-01-01 00:30:00"},{"user_id":3000000000,"assigned_at":"2001-01-01 00:29:00"},` +
		`{"user_id":1e+21,"assigned_at":"2001-01-01 00:27:00"},{"user_id":-1,"assigned_at":"2001-01-01 00:26:00"},{"user_id":11,"assigned_at":"2001-01-01 00:24:00"},` +
		`{"user_id":0,"assigned_at":"2001-01-01 00:23:00"},{"user_id":8,"assigned_at":"2001-01-01 00:22:00"},{"user_id":6,"assigned_at":"2001-01-01 00:21:00"},` +
		`{"user_id":1,"assigned_at":"2001-01-01 00:20:00"},{"user_id":2.5,"assigned_at":"2001-01-01 00:19:00"},{"user_id":1.5,"assigned_at":"2001-01-01 00:18:00"},` +
		`{"user_id":5,"assigned_at":"2001-01-01 00:17:00"},{"user_id":4,"assigned_at":"2001-01-01 00:16:00"},{"user_id":3,"assigned_at":"2001-01-01 00:15:00"},` +
		`{"user_id":2,"assigned_at":"2001-01-01 00:14:00"}]`},
	"sqlite: 752 GET /api/tenants/1/users": {Status: 200, Answer: `[{"user_id":3000000000,"assigned_at":"2001-01-01 00:29:00"},{"user_id":1e+21,"assigned_at":"2001-01-01 00:27:00"},` +
		`{"user_id":11,"assigned_at":"2001-01-01 00:24:00"},{"user_id":0,"assigned_at":"2001-01-01 00:23:00"},{"user_id":8,"assigned_at":"2001-01-01 00:22:00"},` +
		`{"user_id":6,"assigned_at":"2001-01-01 00:21:00"},{"user_id":1,"assigned_at":"2001-01-01 00:20:00"},{"user_id":4,"assigned_at":"2001-01-01 00:16:00"}]`},
})

var (
	unknownDevice  = `{"error":"Unknown device"}`
	invalidIDs     = `{"error":"Invalid device_ids"}`
	invalidID      = `{"error":"Invalid device_id"}`
	notAssigned    = `{"error":"Assignment not found"}`
	knownOfTheList = `{"device_ids":["D-OFF","D-GONE","D-NULLDEL","D-TWO","D-TEXT","D-REAL"]}`
)

// TN4: a tenant gets devices the API server knows (knownDevices), named by
// their ids as text, and the answer counts the ones it did not have. A
// request with anything else assigns nothing, so the devices Node
// assigned from it are not there to be removed later.
var _ = tenantFixes.Add("TN4", map[string]replay.Fix{
	`admin POST /api/tenants/1/devices {"device_id":"spelling-0"}`:  {Status: 400, Answer: unknownDevice},
	`admin DELETE /api/tenants/1/devices/spelling-0 `:               {Status: 404, Answer: notAssigned},
	`admin POST /api/tenants/01/devices {"device_id":"spelling-1"}`: {Status: 400, Answer: unknownDevice},
	`admin DELETE /api/tenants/01/devices/spelling-1 `:              {Status: 404, Answer: notAssigned},
	// The list with a device nobody knows: the others are assigned by a
	// list of them alone
	`admin POST /api/tenants/1/devices {"device_ids":["D-OFF","D-GONE","D-NULLDEL","ghost","D-TWO","D-TEXT","D-REAL"]}`: {
		Status: 400, Answer: unknownDevice, After: assignTheKnown,
	},
	`admin POST /api/tenants/1/devices {"device_ids":"ab"}`:                                                               {Status: 400, Answer: invalidIDs},
	`admin POST /api/tenants/1/devices {"device_ids":"💻x"}`:                                                               {Status: 400, Answer: invalidIDs},
	`admin POST /api/tenants/1/devices {"device_ids":5}`:                                                                  {Status: 400, Answer: invalidIDs},
	`admin POST /api/tenants/1/devices {"device_ids":true}`:                                                               {Status: 400, Answer: invalidIDs},
	`admin POST /api/tenants/1/devices {"device_ids":{}}`:                                                                 {Status: 400, Answer: invalidIDs},
	`admin POST /api/tenants/1/devices {"device_ids":{"length":0}}`:                                                       {Status: 400, Answer: invalidIDs},
	`admin POST /api/tenants/1/devices {"device_ids":{"length":"0"}}`:                                                     {Status: 400, Answer: invalidIDs},
	`admin POST /api/tenants/1/devices {"device_ids":{"length":2}}`:                                                       {Status: 400, Answer: invalidIDs},
	`admin POST /api/tenants/1/devices {"device_ids":{"length":0.0}}`:                                                     {Status: 400, Answer: invalidIDs},
	`admin POST /api/tenants/1/devices {"device_ids":{"length":-0}}`:                                                      {Status: 400, Answer: invalidIDs},
	`admin POST /api/tenants/1/devices {"device_ids":[7,null,true,["nested"],{},[],1.5,"",["p","q"],{"length":0},1e+21]}`: {Status: 400, Answer: invalidIDs},
	`admin POST /api/tenants/1/devices {"device_id":5}`:                                                                   {Status: 400, Answer: invalidID},
	`admin POST /api/tenants/1/devices {"device_id":["q"]}`:                                                               {Status: 400, Answer: invalidID},
	`admin POST /api/tenants/1/devices {"device_id":{}}`:                                                                  {Status: 400, Answer: invalidID},
	`admin POST /api/tenants/1/devices {"device_id":true}`:                                                                {Status: 400, Answer: invalidID},
	`admin POST /api/tenants/1/devices {"device_ids":null,"device_id":"via-single"}`:                                      {Status: 400, Answer: unknownDevice},
	`admin POST /api/tenants/1/devices {"device_ids":0,"device_id":"via-single-2"}`:                                       {Status: 400, Answer: unknownDevice},
	`admin POST /api/tenants/1/devices {"device_ids":"","device_id":"via-single-3"}`:                                      {Status: 400, Answer: unknownDevice},
	// D-ON was the tenant's already
	`admin POST /api/tenants/1/devices {"device_ids":["D-ON","D-ON"],"device_id":"ignored"}`: {Status: 200, Answer: `{"ok":true,"assigned":0}`},
	`admin DELETE /api/tenants/1/devices/ghost `:                                             {NodeStatus: 200, Status: 404, Answer: notAssigned},
	`admin DELETE /api/tenants/1/devices/7.0 `:                                               {Dialect: "sqlite", NodeStatus: 200, Status: 404, Answer: notAssigned},
	`admin DELETE /api/tenants/1/devices/7 `:                                                 {Dialect: "postgres", NodeStatus: 200, Status: 404, Answer: notAssigned},
	`admin DELETE /api/tenants/1/devices/1.5 `:                                               {NodeStatus: 200, Status: 404, Answer: notAssigned},
	`admin DELETE /api/tenants/1/devices/%F0%9F%92%BB `:                                      {NodeStatus: 200, Status: 404, Answer: notAssigned},
})

// assignTheKnown assigns the devices of the list the API server knows.
func assignTheKnown(t *testing.T, w *replay.World, step replay.Step) {
	t.Helper()
	if got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: step.Path, Body: replay.Text(knownOfTheList)}); got.Status != 200 || got.Body != `{"ok":true,"assigned":6}` {
		t.Errorf("%s: the known devices alone: %d %s", step.Name, got.Status, got.Body)
	}
}

// The lists and statistics of tenant 1 after its assignments: the seven
// devices the API server knows (TN4), each assigned once, so the times
// that follow from their ids are those of their first assignment (TN8).
// D-ON is taken away after the first two reads.
var _ = addSteps("TN4 TN8", map[string]replay.Fix{
	"sqlite: 702 GET /api/tenants/1/devices":   {Status: 200, Answer: tenantDevicesAfterTN4("sqlite", true)},
	"postgres: 702 GET /api/tenants/1/devices": {Status: 200, Answer: tenantDevicesAfterTN4("postgres", true)},
	"sqlite: 713 GET /api/tenants/1/devices":   {Status: 200, Answer: tenantDevicesAfterTN4("sqlite", false)},
	"postgres: 713 GET /api/tenants/1/devices": {Status: 200, Answer: tenantDevicesAfterTN4("postgres", false)},
	"sqlite: 703 GET /api/tenants/1/stats":     {Status: 200, Answer: tenantStats(7, 2, 0)},
	"postgres: 703 GET /api/tenants/1/stats":   {Status: 200, Answer: tenantStats(7, 2, 0)},
	"sqlite: 742 GET /api/tenants/1/stats":     {Status: 200, Answer: tenantStats(6, 1, 15)},
	"postgres: 742 GET /api/tenants/1/stats":   {Status: 200, Answer: tenantStats(6, 1, 10)},
	"sqlite: 756 GET /api/tenants/1/stats":     {Status: 200, Answer: tenantStats(6, 1, 8)},
	"postgres: 756 GET /api/tenants/1/stats":   {Status: 200, Answer: tenantStats(6, 1, 6)},
})

// tenantDevicesAfterTN4 is the list of tenant 1's devices: D-OFF to
// D-REAL assigned at the minutes 13 to 18 and D-ON, when it is there, at
// 12. The console's table of the PostgreSQL recording has no names for
// D-TWO, D-TEXT and D-REAL.
func tenantDevicesAfterTN4(dialect string, withOn bool) string {
	type device struct{ id, hostname, ip string }
	devices := []device{{"D-REAL", "real", "10.0.0.7"}, {"D-TEXT", "text", "10.0.0.6"}, {"D-TWO", "two", "10.0.0.5"}, {"D-NULLDEL", "", "10.0.0.4"}, {"D-GONE", "", ""}, {"D-OFF", "", ""}}
	if withOn {
		devices = append(devices, device{"D-ON", "pc-on", "10.0.0.1"})
	}
	var entries []string
	for i, d := range devices {
		minute := fmt.Sprintf("%02d", 18-i)
		at := "2001-01-01 00:" + minute + ":00"
		if dialect == "postgres" {
			at = "2001-01-01T00:" + minute + ":00.000Z"
			if i < 3 {
				d.hostname, d.ip = "", ""
			}
		}
		online := d.id == "D-NULLDEL" || d.id == "D-ON"
		entries = append(entries, fmt.Sprintf(`{"device_id":%q,"assigned_at":%q,"online":%v,"hostname":%q,"ip":%q}`, d.id, at, online, d.hostname, d.ip))
	}
	return "[" + strings.Join(entries, ",") + "]"
}

// tenantStats is the statistics of tenant 1.
func tenantStats(devices, online, users int) string {
	return fmt.Sprintf(`{"tenant_id":1,"tenant_name":"By admin","total_devices":%d,"online_devices":%d,"total_users":%d,"max_devices":0}`, devices, online, users)
}
