package avatars

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
)

// checked reports what must hold for every picture that got through: an
// allowed type that the first bytes carry, a size within the limit, and
// the version of exactly these bytes.
func checked(t *testing.T, what string, mime string, data []byte, version string) {
	t.Helper()
	if !allowedType(mime) || SniffImageType(data) != mime {
		t.Fatalf("%s: stored as %q, the bytes are %q", what, mime, SniffImageType(data))
	}
	if len(data) == 0 || len(data) > MaxAvatarBytes {
		t.Fatalf("%s: %d bytes stored", what, len(data))
	}
	if version != ImageVersion(data) {
		t.Fatalf("%s: version %q is not the version of the bytes", what, version)
	}
}

func listDir(t testing.TB, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return strings.Join(names, " ")
}

// FuzzAvatarImage hands arbitrary bytes to every way a picture comes in:
// as the value of the single sign-on claim, as the bytes of a data URL
// with an arbitrary declared type, and as the body of an HTTP answer with
// an arbitrary Content-Type. Nothing may panic, a picture is stored only
// when it passes the checks, a refused picture leaves the stored one as
// it was, and no file appears: pictures live in the database only.
func FuzzAvatarImage(f *testing.F) {
	gif := []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;")
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0, 0x10, 0x4a, 0x46, 0x49, 0x46, 0, 1}
	webp := append(append([]byte("RIFF"), 0x1a, 0, 0, 0), "WEBPVP8 "...)
	for _, seed := range [][]byte{onePixelPNG, gif, jpeg, webp, nil, []byte("<svg/>"), []byte("<html><script>alert(1)</script></html>"),
		[]byte(pngDataURL), []byte("data:image/png;base64,"), []byte("http://127.0.0.1/a.png"), []byte("http://pictures.example/a.png"),
		[]byte("../../etc/passwd"), []byte("data:image/svg+xml;base64,PHN2Zy8+"), {0x89, 'P', 'N', 'G'}, []byte("GIF88a")} {
		for _, mime := range []string{"image/png", "image/gif", "image/jpeg", "image/webp", "IMAGE/PNG; q=1", "text/html", ""} {
			f.Add(seed, mime)
		}
	}

	dir := f.TempDir()
	db, err := store.OpenSQLite(dir + "/console.sqlite3")
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		f.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO users (id, username, password_hash, role) VALUES (1, 'alice', 'x', 'viewer')`); err != nil {
		f.Fatal(err)
	}
	files := listDir(f, dir)

	f.Fuzz(func(t *testing.T, data []byte, mime string) {
		// The bytes and the type as an HTTP answer
		transport := roundTripper(func(req *http.Request) (*http.Response, error) {
			res := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, ContentLength: -1, Request: req}
			res.Header.Set("Content-Type", mime)
			res.Body = &chunkBody{chunks: [][]byte{data}}
			return res, nil
		})
		svc := New(Options{DB: db, Fetch: FetchOptions{Transport: transport}, Logger: &warnings{}})

		values := []string{
			string(data),
			"data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data),
			"data:image/png;base64," + string(data),
			"http://pictures.example/a.png",
		}
		for i, value := range values {
			before, err := svc.GetAvatar(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			result := svc.SyncOIDCPicture(ctx, 1, claims{"picture": value})
			after, err := svc.GetAvatar(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			switch result.Action {
			case "stored":
				if after == nil {
					t.Fatalf("value %d: stored, and no picture is there", i)
				}
				checked(t, "the stored picture", after.MimeType, after.Data, after.Version)
			case "failed":
				if (before == nil) != (after == nil) || before != nil && (!bytes.Equal(before.Data, after.Data) || before.Version != after.Version || before.MimeType != after.MimeType) {
					t.Fatalf("value %d: refused with %s, and the stored picture changed", i, result.Code)
				}
			case "cleared":
				if after != nil || jsTrim(value) != "" {
					t.Fatalf("value %d: cleared for %q, picture %v", i, value, after)
				}
			default:
				t.Fatalf("value %d: action %q", i, result.Action)
			}
		}

		if img, err := ParseDataURL(values[1], 0); err == nil {
			checked(t, "ParseDataURL", img.MimeType, img.Data, ImageVersion(img.Data))
			if !bytes.Equal(img.Data, data) {
				t.Fatal("ParseDataURL changed the bytes")
			}
		}
		for _, name := range strings.Fields(listDir(t, dir)) {
			if !strings.HasPrefix(name, "console.sqlite3") {
				t.Fatalf("a file appeared: %q (at the start: %q)", name, files)
			}
		}
	})
}

type roundTripper func(req *http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// FuzzCheckURL reads arbitrary text as a picture address. Nothing may
// panic, and what is let through is an http or https address without
// credentials whose host is neither a refused name nor a refused address
// and whose request goes to that host.
func FuzzCheckURL(f *testing.F) {
	for _, seed := range []string{"http://example.com/a.png", "https://idp.lan:8443/a?b#c", "http://127.0.0.1/", "http://0x7f.1/", "http://[::1]/",
		"http://u:p@example.com/", "http:\\\\example.com\\a", "ftp://example.com", "http://example.com:99999/", "http://[::ffff:7f00:1]/",
		" http://exa\tmple.com/a/../b ", "http://example.com//a//b", "http://example.com/%zz", "http://localhost./", "http://2130706433/",
		"http://a.b.c.d.e/", "http://1.2.3.4.5/", "http://[fe80::1%25eth0]/", "http://%6c%6f%63%61%6c%68%6f%73%74/"} {
		f.Add(seed, "/next")
	}
	f.Fuzz(func(t *testing.T, text, location string) {
		target, err := checkURL(text, false)
		if err != nil {
			if code := codeOf(err); code != "invalid_url" && code != "address_refused" {
				t.Fatalf("checkURL(%q) failed with %q", text, code)
			}
			return
		}
		if target.scheme != "http" && target.scheme != "https" {
			t.Fatalf("checkURL(%q) let the scheme %q through", text, target.scheme)
		}
		host := target.hostname
		if host == "" || refusedHostnames[host] || strings.HasSuffix(host, ".localhost") || strings.ContainsAny(host, "@/\\?# \t\r\n") {
			t.Fatalf("checkURL(%q) let the host %q through", text, host)
		}
		if strings.Contains(host, ":") || endsInNumber(host) {
			if IsRefusedAddress(host) {
				t.Fatalf("checkURL(%q) let the address %q through", text, host)
			}
		}
		u := target.url()
		if u.User != nil || u.Hostname() != host || !strings.HasPrefix(target.path, "/") {
			t.Fatalf("checkURL(%q): the request would go to %q with path %q", text, u.Host, target.path)
		}
		// A redirect is read against the address and checked like one
		_, _ = checkURL(target.resolve(location), false)
	})
}
