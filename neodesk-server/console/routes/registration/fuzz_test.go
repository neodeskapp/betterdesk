package registration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsjson"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// FuzzRegisterRequest sends the request route anything a device on the
// internet could: any body and any forwarding headers. The route never
// panics and answers 200, 400 or 500 with JSON; what it stores is the
// trimmed device id of at least three UTF-16 units before the trim, the
// fields within their lengths, and text that is UTF-8.
func FuzzRegisterRequest(f *testing.F) {
	for _, body := range []string{
		`{}`, `{"device_id":"abc"}`, `{"device_id":" ab"}`, `{"device_id":"   "}`, `{"device_id":"😀a"}`,
		`{"device_id":"abc","hostname":5}`, `{"device_id":"abc","hostname":"` + strings.Repeat("😀", 200) + `"}`,
		`{"device_id":"abc","uuid":[]}`, `["abc"]`, `null`, `{"device_id":"\ud83d"}`, `{"device_id":"abc","__proto__":{}}`,
	} {
		f.Add(body, "203.0.113.5, 10.0.0.1", "")
		f.Add(body, "", "192.0.2.9")
	}
	f.Add(`{"device_id":"abc"}`, " , 10.0.0.2", "\xa0spaced\xa0")
	db, err := store.OpenSQLite(filepath.Join(f.TempDir(), "fuzz.sqlite3"))
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		f.Fatal(err)
	}
	h := clientAPIHandler(db, true)
	f.Fuzz(func(t *testing.T, body, forwarded, real string) {
		req := httptest.NewRequest(http.MethodPost, "/api/nd/register-request", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if forwarded != "" {
			req.Header["X-Forwarded-For"] = []string{forwarded}
		}
		if real != "" {
			req.Header["X-Real-Ip"] = []string{real}
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var answer map[string]any
		switch rec.Code {
		case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
			// The route's refusal, or the port's JSON parser's
			return
		case http.StatusInternalServerError:
			if rec.Body.String() != `{"success":false,"error":"Internal server error"}` {
				t.Fatalf("500 %q", rec.Body.String())
			}
			return
		case http.StatusOK:
			if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
				t.Fatalf("200 %q is no JSON", rec.Body.String())
			}
		default:
			t.Fatalf("status %d for %q", rec.Code, body)
		}
		id, ok := answer["id"].(float64)
		if !ok || answer["success"] != true {
			t.Fatalf("answer %s", rec.Body.String())
		}
		var deviceID, hostname, platform, version, publicKey, uuid, ip string
		err := db.QueryRow(context.Background(), "SELECT device_id, hostname, platform, version, public_key, uuid, ip_address FROM pending_registrations WHERE id = ?", int64(id)).
			Scan(&deviceID, &hostname, &platform, &version, &publicKey, &uuid, &ip)
		if err != nil {
			t.Fatal(err)
		}
		var sent struct {
			DeviceID string `json:"device_id"`
		}
		_ = json.Unmarshal([]byte(body), &sent)
		if answer["status"] == "pending" && deviceID != jsbind.Trim(sent.DeviceID) {
			t.Fatalf("stored device id %q for %q", deviceID, sent.DeviceID)
		}
		for _, f := range []struct {
			value string
			max   int
		}{{hostname, 255}, {platform, 64}, {version, 32}, {publicKey, 512}, {uuid, 64}} {
			if jsjson.Length(f.value) > f.max || !utf8.ValidString(f.value) {
				t.Fatalf("stored field %q (limit %d)", f.value, f.max)
			}
		}
		if !utf8.ValidString(ip) || ip == "" {
			t.Fatalf("stored address %q", ip)
		}
	})
}
