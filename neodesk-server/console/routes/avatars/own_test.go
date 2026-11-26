package avatars_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	avatarsvc "github.com/neodeskapp/neodesk/neodesk-server/console/avatars"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// pngBytes is a picture as far as the console checks one: the PNG
// signature and some bytes.
func pngBytes(fill byte, n int) []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{fill}, n)...)
}

func (w *world) sendBody(method, target, contentType string, body []byte) *httptest.ResponseRecorder {
	w.t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.RemoteAddr = "203.0.113.7:5555"
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if w.cookie != "" {
		req.Header.Set("Cookie", w.cookie)
	}
	w.steps.ServeHTTP(rec, req)
	return rec
}

type ownAnswer struct {
	Success bool   `json:"success"`
	Code    string `json:"code"`
	Data    struct {
		AvatarVersion *string `json:"avatar_version"`
	} `json:"data"`
}

func decodeOwn(t *testing.T, rec *httptest.ResponseRecorder) ownAnswer {
	t.Helper()
	var out ownAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("answer %d %q: %v", rec.Code, rec.Body.String(), err)
	}
	return out
}

func storedPicture(t *testing.T, db *store.DB, userID int64) *avatarsvc.Avatar {
	t.Helper()
	a, err := avatarsvc.New(avatarsvc.Options{DB: db}).GetAvatar(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestUploadOwnPicture(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db, nil)
		alice := w.user("alice")
		bob := w.user("bob")
		w.picture(bob, avatarsvc.TypeJPEG, []byte("\xff\xd8\xff old"), "oldversion", "oidc", "https://idp.example/bob.jpg")
		w.signIn(bob)

		data := pngBytes(7, 100)
		rec := w.sendBody(http.MethodPut, "/api/users/me/avatar", "image/png", data)
		if rec.Code != http.StatusOK {
			t.Fatalf("upload answered %d %s", rec.Code, rec.Body.String())
		}
		out := decodeOwn(t, rec)
		if out.Data.AvatarVersion == nil || *out.Data.AvatarVersion != avatarsvc.ImageVersion(data) {
			t.Fatalf("upload answered the version %v, want %s", out.Data.AvatarVersion, avatarsvc.ImageVersion(data))
		}
		got := storedPicture(t, db, bob)
		if got == nil || !bytes.Equal(got.Data, data) || got.Source != avatarsvc.SourceUpload || got.MimeType != avatarsvc.TypePNG {
			t.Fatalf("stored %+v", got)
		}
		if storedPicture(t, db, alice) != nil {
			t.Fatal("another account got a picture")
		}
		// The new picture is in the list at once
		list := w.send(w.cookie, http.MethodGet, "/api/users/avatars", nil)
		if !strings.Contains(list.Body.String(), *out.Data.AvatarVersion) {
			t.Errorf("the list does not have the new version: %s", list.Body.String())
		}
		pic := w.send(w.cookie, http.MethodGet, "/api/users/2/avatar", nil)
		if pic.Code != http.StatusOK || !bytes.Equal(pic.Body.Bytes(), data) {
			t.Errorf("GET the picture answered %d with %d bytes", pic.Code, pic.Body.Len())
		}
	})
}

func TestUploadRefusesWhatIsNoPicture(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db, nil)
		bob := w.user("bob")
		w.signIn(bob)
		cases := []struct {
			name, contentType string
			body              []byte
			status            int
			code              string
		}{
			{"empty", "image/png", nil, http.StatusBadRequest, "empty"},
			{"html", "image/png", []byte("<html><script>alert(1)</script></html>"), http.StatusBadRequest, "not_an_image"},
			{"svg", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), http.StatusBadRequest, "not_an_image"},
			{"declared jpeg", "image/jpeg", pngBytes(1, 10), http.StatusBadRequest, "type_mismatch"},
			{"too large", "image/png", pngBytes(1, avatarsvc.MaxAvatarBytes), http.StatusRequestEntityTooLarge, "too_large"},
		}
		for _, c := range cases {
			rec := w.sendBody(http.MethodPut, "/api/users/me/avatar", c.contentType, c.body)
			out := decodeOwn(t, rec)
			if rec.Code != c.status || out.Code != c.code {
				t.Errorf("%s: %d %q, want %d %q", c.name, rec.Code, out.Code, c.status, c.code)
			}
		}
		if storedPicture(t, db, bob) != nil {
			t.Fatal("a refused upload was stored")
		}
		// A file without a picture type is taken by its bytes
		rec := w.sendBody(http.MethodPut, "/api/users/me/avatar", "application/octet-stream", pngBytes(2, 10))
		if rec.Code != http.StatusOK {
			t.Errorf("octet-stream upload answered %d %s", rec.Code, rec.Body.String())
		}
	})
}

func TestRemoveOwnPicture(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db, nil)
		alice := w.user("alice")
		bob := w.user("bob")
		w.picture(alice, avatarsvc.TypePNG, pngBytes(3, 5), "alice1", "upload", "")
		w.picture(bob, avatarsvc.TypePNG, pngBytes(4, 5), "bob1", "oidc", "")
		w.signIn(bob)
		rec := w.sendBody(http.MethodDelete, "/api/users/me/avatar", "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("remove answered %d %s", rec.Code, rec.Body.String())
		}
		if out := decodeOwn(t, rec); out.Data.AvatarVersion != nil {
			t.Errorf("remove answered the version %q", *out.Data.AvatarVersion)
		}
		if storedPicture(t, db, bob) != nil {
			t.Error("the single sign-on picture is still there")
		}
		if storedPicture(t, db, alice) == nil {
			t.Error("another account's picture went too")
		}
		// Removing nothing is fine
		if rec := w.sendBody(http.MethodDelete, "/api/users/me/avatar", "", nil); rec.Code != http.StatusOK {
			t.Errorf("second remove answered %d", rec.Code)
		}
	})
}

func TestOwnPictureNeedsASignedInUser(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db, nil)
		w.user("bob")
		for _, method := range []string{http.MethodPut, http.MethodDelete} {
			rec := w.sendBody(method, "/api/users/me/avatar", "image/png", pngBytes(5, 5))
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s without a session answered %d", method, rec.Code)
			}
		}
		if storedPicture(t, db, 1) != nil {
			t.Fatal("a picture was stored without a session")
		}
	})
}
