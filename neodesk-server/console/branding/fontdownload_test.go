package branding

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// googleLike answers as Google Fonts does: WOFF2 files in blocks marked
// with their subset only for a browser it knows (a user agent with a
// Chrome version), TrueType links without subsets for anything else.
type googleLike struct{ agents []string }

func (g *googleLike) RoundTrip(req *http.Request) (*http.Response, error) {
	agent := req.Header.Get("User-Agent")
	g.agents = append(g.agents, agent)
	body := ""
	switch {
	case req.URL.Host == "fonts.gstatic.com":
		body = "wOF2 font bytes"
	case strings.Contains(agent, "Chrome/"):
		for _, weight := range []string{"400", "700"} {
			body += "/* latin-ext */\n@font-face {\n  font-family: 'Lora';\n  font-weight: " + weight +
				";\n  src: url(https://fonts.gstatic.com/s/lora/ext-" + weight + ".woff2) format('woff2');\n}\n" +
				"/* latin */\n@font-face {\n  font-family: 'Lora';\n  font-weight: " + weight +
				";\n  src: url(https://fonts.gstatic.com/s/lora/latin-" + weight + ".woff2) format('woff2');\n}\n"
		}
	default:
		body = "@font-face {\n  font-family: 'Lora';\n  font-weight: 400;\n  src: url(https://fonts.gstatic.com/s/lora/a.ttf) format('truetype');\n}\n"
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

// A Google font is downloaded to the server, so the console can serve it
// itself (BUGS L362): the request names a browser Google serves WOFF2 to.
func TestGoogleFontIsDownloaded(t *testing.T) {
	google := &googleLike{}
	opts := fixtureOptions(t.TempDir())
	opts.HTTP = google
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.DownloadFont(context.Background(), "Lora", []any{"400", "700"})
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := result.Get("success"); ok != true {
		t.Fatalf("the download answered %s, user agents %q", Stringify(result), google.agents)
	}
	for _, name := range []string{"lora-400.woff2", "lora-700.woff2", "font.css"} {
		if _, err := os.Stat(filepath.Join(opts.FontsDir, "lora", name)); err != nil {
			t.Errorf("%s was not stored: %v", name, err)
		}
	}
	if font := s.getLocalFont("Lora"); font == nil || font.Text("cssPath") != "/fonts/lora/font.css" {
		t.Errorf("the font is not installed: %v", font)
	}
}
