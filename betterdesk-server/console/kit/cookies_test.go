package kit

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// Every recorded Set-Cookie header of Express's res.cookie(name, value,
// options) for the options the console uses (testdata/setcookie_vectors.json,
// written by .ui-rework/go/tools/vectors/express-answers.js).
func TestSetCookieMatchesExpress(t *testing.T) {
	raw, err := os.ReadFile("testdata/setcookie_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Now     int64 `json:"now"`
		Vectors []struct {
			Name    string `json:"name"`
			Cookie  string `json:"cookie"`
			Value   string `json:"value"`
			Options struct {
				MaxAge   int64  `json:"maxAge"`
				Path     string `json:"path"`
				HTTPOnly bool   `json:"httpOnly"`
				Secure   bool   `json:"secure"`
				SameSite string `json:"sameSite"`
			} `json:"options"`
			Header string `json:"header"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Vectors) < 12 {
		t.Fatalf("only %d vectors", len(file.Vectors))
	}
	withLifetime := 0
	for _, v := range file.Vectors {
		rec := httptest.NewRecorder()
		SetCookie(rec, v.Cookie, v.Value, CookieOptions{
			MaxAge:   time.Duration(v.Options.MaxAge) * time.Millisecond,
			Path:     v.Options.Path,
			HTTPOnly: v.Options.HTTPOnly,
			Secure:   v.Options.Secure,
			SameSite: v.Options.SameSite,
			Now:      time.UnixMilli(file.Now),
		})
		if got := rec.Header().Values("Set-Cookie"); len(got) != 1 || got[0] != v.Header {
			t.Errorf("%s: Set-Cookie = %q, Express sends %q", v.Name, got, v.Header)
		}
		if v.Options.MaxAge != 0 {
			withLifetime++
		}
	}
	if withLifetime < 8 {
		t.Errorf("only %d vectors with a lifetime", withLifetime)
	}
}
