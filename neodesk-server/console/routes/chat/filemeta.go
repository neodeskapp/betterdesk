package chat

import (
	"bytes"
	"image"
	_ "image/gif"  // pictures of the chat: their size in pixels
	_ "image/jpeg" // pictures of the chat: their size in pixels
	_ "image/png"  // pictures of the chat: their size in pixels
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// inlineTypes are the pictures the console shows inside a conversation,
// by what an upload's bytes are, whatever its name says. Anything else,
// SVG included, only downloads.
var inlineTypes = map[string]string{
	"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp",
}

// readMeta notes what a stored file is: its type from its first bytes,
// its size, and a picture's size in pixels.
func readMeta(path, original string, size int64) fileMeta {
	meta := fileMeta{Name: originalName(original), Size: size, Type: "application/octet-stream"}
	f, err := os.Open(path)
	if err != nil {
		return meta
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	meta.Type = sniff(head)
	if _, picture := inlineTypes[meta.Type]; picture && meta.Type != "image/webp" {
		if _, err := f.Seek(0, io.SeekStart); err == nil {
			if cfg, _, err := image.DecodeConfig(f); err == nil {
				meta.Width, meta.Height = cfg.Width, cfg.Height
			}
		}
	}
	return meta
}

// sniff is the type of a file by its first bytes. Text of any kind is
// text/plain, so nothing a user sent ever renders as a page.
func sniff(head []byte) string {
	if bytes.HasPrefix(head, []byte("RIFF")) && len(head) >= 12 && string(head[8:12]) == "WEBP" {
		return "image/webp"
	}
	t := http.DetectContentType(head)
	t, _, _ = strings.Cut(t, ";")
	if strings.HasPrefix(t, "text/") {
		return "text/plain"
	}
	return t
}

// originalName is the name the client gave the file. The upload reads a
// UTF-8 name as one Latin-1 letter per byte (middleware.UploadedFile), so
// such a name gets its bytes back. A name without any is "file".
func originalName(name string) string {
	b := make([]byte, 0, len(name))
	latin1 := true
	for _, r := range name {
		if r > 0xff {
			latin1 = false
			break
		}
		b = append(b, byte(r))
	}
	if latin1 && utf8.Valid(b) {
		name = string(b)
	}
	name = strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, "\\", "/")))
	if name == "" || name == "." || name == "/" {
		return "file"
	}
	return cutChars(name, 200)
}

// inlinePicture reports whether a stored file may show inside the page:
// its bytes are a picture of inlineTypes and its stored extension names
// the same type, so the type the console serves it with is that picture.
func inlinePicture(stored string, meta fileMeta) bool {
	ext, ok := inlineTypes[meta.Type]
	if !ok {
		return false
	}
	have := strings.ToLower(filepath.Ext(stored))
	return have == ext || ext == ".jpg" && have == ".jpeg"
}
