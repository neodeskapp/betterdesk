//go:build contract

package contract_test

import (
	"net/url"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// The HTTP routes of chat: upload, download and search. Uploaded files
// stay: no route deletes one. The instances keep them in the source
// tree's web-nodejs/data/chat-files, which git ignores.
//
// Search finds messages by their text in the conversations the account
// reads (routes/chat/search.go, A242).
func init() {
	few := contract.Roles("signed-out", "viewer", "admin")
	chatters := contract.Roles("viewer", "admin")
	admin := contract.Roles("admin")
	file := func(name, content string) []contract.Part {
		return []contract.Part{{Field: "file", FileName: name, ContentType: "application/octet-stream", Content: []byte(content)}}
	}
	// One byte more than an upload may have
	tooLarge := make([]byte, 50<<20+1)

	contract.Group("chat",
		contract.Case{Name: "search", Path: "/api/chat/search", Query: url.Values{"q": {"hello"}}},
		contract.Case{Name: "search-nothing", Path: "/api/chat/search", Roles: few},
		contract.Case{
			Name: "search-in-a-conversation", Path: "/api/chat/search",
			Query: url.Values{"q": {"hello", "again"}, "conversation_id": {"operator:e2e_admin"}}, Roles: chatters,
		},
		contract.Case{
			Name: "search-text-that-cannot-be-text", Path: "/api/chat/search",
			Query: url.Values{"q[toString]": {"x"}}, Roles: chatters,
		},
		contract.Case{Name: "search-head", Method: "HEAD", Path: "/api/chat/search", Query: url.Values{"q": {"hello"}}, Roles: few},

		contract.Case{
			Name: "upload", Method: "POST", Path: "/api/chat/upload",
			Multipart: file("Meeting Notes.txt", "contract chat file\n"),
			Capture:   map[string]string{"file": "file_id"},
		},
		contract.Case{
			Name: "download", Path: "/api/chat/files/{file}",
			VolatileHeaders: []string{"content-disposition"},
		},
		contract.Case{
			Name: "download-head", Method: "HEAD", Path: "/api/chat/files/{file}",
			VolatileHeaders: []string{"content-disposition"}, Roles: few,
		},
		contract.Case{
			Name: "download-not-modified", Path: "/api/chat/files/{file}",
			Headers:         map[string]string{"If-None-Match": "*"},
			VolatileHeaders: []string{"content-disposition"}, Roles: few,
		},
		contract.Case{
			Name: "download-part", Path: "/api/chat/files/{file}",
			Headers:         map[string]string{"Range": "bytes=9-12"},
			VolatileHeaders: []string{"content-disposition"}, Roles: chatters,
		},
		contract.Case{
			Name: "download-part-beyond-the-end", Path: "/api/chat/files/{file}",
			Headers:         map[string]string{"Range": "bytes=100-200"},
			VolatileHeaders: []string{"content-disposition"}, Roles: chatters,
		},
		contract.Case{
			Name: "download-if-match-fails", Path: "/api/chat/files/{file}",
			Headers:         map[string]string{"If-Match": `"another"`},
			VolatileHeaders: []string{"content-disposition"}, Roles: chatters,
		},
		contract.Case{Name: "download-with-the-extension", Path: "/api/chat/files/{file}.txt", Roles: chatters},
		contract.Case{Name: "download-id-too-long", Path: "/api/chat/files/{file}00", Roles: chatters},

		contract.Case{
			Name: "upload-without-extension", Method: "POST", Path: "/api/chat/upload",
			Multipart: file("README", "no extension\n"), Roles: chatters,
			Capture: map[string]string{"bare": "file_id"},
		},
		contract.Case{
			Name: "download-without-extension", Path: "/api/chat/files/{bare}",
			VolatileHeaders: []string{"content-disposition"}, Roles: chatters,
		},
		contract.Case{
			Name: "upload-long-extension", Method: "POST", Path: "/api/chat/upload",
			Multipart: file("archive.tar.verylongextension", "long extension\n"), Roles: admin,
			Capture: map[string]string{"long": "file_id"},
		},
		contract.Case{
			Name: "download-long-extension", Path: "/api/chat/files/{long}",
			VolatileHeaders: []string{"content-disposition"}, Roles: admin,
		},
		contract.Case{
			Name: "upload-web-page", Method: "POST", Path: "/api/chat/upload",
			Multipart: file("page.HTML", "<p>not run</p>\n"), Roles: admin,
			Capture: map[string]string{"page": "file_id"},
		},
		contract.Case{
			Name: "download-web-page", Path: "/api/chat/files/{page}",
			VolatileHeaders: []string{"content-disposition"}, Roles: admin,
		},
		contract.Case{
			Name: "upload-empty-file", Method: "POST", Path: "/api/chat/upload",
			Multipart: []contract.Part{{Field: "note", Content: []byte("a field")}, {Field: "file", FileName: "empty.bin", ContentType: "application/octet-stream"}},
			Roles:     admin,
		},

		contract.Case{
			Name: "upload-without-a-file", Method: "POST", Path: "/api/chat/upload",
			Multipart: []contract.Part{{Field: "note", Content: []byte("no file")}},
		},
		contract.Case{
			Name: "upload-as-json", Method: "POST", Path: "/api/chat/upload",
			Body: map[string]any{"file": "x"}, Roles: few,
		},
		contract.Case{
			Name: "upload-in-another-field", Method: "POST", Path: "/api/chat/upload",
			Multipart: []contract.Part{{Field: "other", FileName: "a.txt", ContentType: "text/plain", Content: []byte("x")}},
			Roles:     few,
		},
		contract.Case{
			Name: "upload-two-files", Method: "POST", Path: "/api/chat/upload",
			Multipart: []contract.Part{
				{Field: "file", FileName: "a.txt", ContentType: "text/plain", Content: []byte("x")},
				{Field: "file", FileName: "b.txt", ContentType: "text/plain", Content: []byte("y")},
			},
			Roles: chatters,
		},
		contract.Case{
			Name: "upload-without-boundary", Method: "POST", Path: "/api/chat/upload",
			RawBody: "x", ContentType: "multipart/form-data", Roles: chatters,
		},
		contract.Case{
			Name: "upload-over-the-limit", Method: "POST", Path: "/api/chat/upload",
			Multipart: []contract.Part{{Field: "file", FileName: "big.bin", ContentType: "application/octet-stream", Content: tooLarge}},
			Roles:     admin,
		},
		contract.Case{
			Name: "upload-without-csrf-token", Method: "POST", Path: "/api/chat/upload",
			Multipart: file("a.txt", "x"), NoCSRF: true, Roles: few,
		},

		contract.Case{Name: "download-unknown", Path: "/api/chat/files/0123456789abcdef0123456789abcdef"},
		contract.Case{Name: "download-unknown-in-capitals", Path: "/api/chat/files/0123456789ABCDEF0123456789ABCDEF", Roles: chatters},
		contract.Case{Name: "download-short-id", Path: "/api/chat/files/abc123", Roles: few},
		contract.Case{Name: "download-not-hex", Path: "/api/chat/files/gggggggggggggggggggggggggggggggg", Roles: chatters},
		contract.Case{Name: "download-path-in-the-id", Path: "/api/chat/files/..%2F..%2Fpackage.json", Roles: chatters},
	)
}
