package message

import (
	"regexp"
	"strings"
)

// Inline is an image the HTML body shows by its Content-ID
// (<img src="cid:ID">), carried in the message so the mail client shows it
// without loading anything.
type Inline struct {
	// ID is the Content-ID without angle brackets: letters, digits and
	// . _ - @ only.
	ID string
	// ContentType is image/png, image/jpeg or image/gif.
	ContentType string
	// Filename is the name a client shows for the image.
	Filename string
	Data     []byte
}

var (
	inlineID   = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,100}$`)
	inlineType = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true}
	inlineName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

// usable reports whether the image can go into a message as it is: its
// ID, type and name can't break a header.
func (i Inline) usable() bool {
	return inlineID.MatchString(i.ID) && inlineType[i.ContentType] && inlineName.MatchString(i.Filename) && len(i.Data) > 0
}

// related is the body of a multipart/related part: the HTML, then each
// image in base64.
func related(html, boundary string, images []Inline) []byte {
	encoding := transferEncoding(html)
	var b strings.Builder
	b.WriteString("--" + boundary + "\r\n" +
		foldLines("Content-Type: text/html; charset=utf-8") + "\r\n" +
		foldLines("Content-Transfer-Encoding: "+encoding) + "\r\n\r\n")
	b.Write(encodeBody(html, encoding))
	for _, img := range images {
		b.WriteString("\r\n--" + boundary + "\r\n" +
			"Content-Type: " + img.ContentType + "; name=\"" + img.Filename + "\"\r\n" +
			"Content-Transfer-Encoding: base64\r\n" +
			"Content-ID: <" + img.ID + ">\r\n" +
			"Content-Disposition: inline; filename=\"" + img.Filename + "\"\r\n\r\n")
		b.Write(base64Body(img.Data))
	}
	b.WriteString("\r\n--" + boundary + "--\r\n")
	return []byte(b.String())
}
