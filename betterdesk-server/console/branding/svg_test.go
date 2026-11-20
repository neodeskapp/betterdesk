package branding

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The checks in this file read cleaned SVG markup the way a browser reads
// an SVG image: with an XML parser that resolves namespaces and character
// references. They keep their own lists of what must not be there, apart
// from the cleaner's allowlists, so that a gap in those shows here.

const (
	testSVGNamespace   = "http://www.w3.org/2000/svg"
	testXLinkNamespace = "http://www.w3.org/1999/xlink"
	testXMLNamespace   = "http://www.w3.org/XML/1998/namespace"
)

// svgActive are elements that run script, load something, change other
// elements over time or hold HTML, by their names in lower case (an HTML
// parser lowers them).
var svgActive = map[string]bool{
	"script": true, "foreignobject": true, "iframe": true, "embed": true, "object": true, "applet": true,
	"image": true, "feimage": true, "animate": true, "animatemotion": true, "animatetransform": true,
	"animatecolor": true, "set": true, "mpath": true, "discard": true, "handler": true, "listener": true,
	"cursor": true, "font-face-uri": true, "img": true, "body": true, "html": true,
}

// reCSSLoad finds CSS that loads something from elsewhere or could hide
// that: a url() whose target is not "#...", image-set(), image(),
// @import, expression() and backslash escapes.
var reCSSLoad = regexp.MustCompile(`(?i)url\(\s*['"]?\s*([^#\s'"]|$)|image(-set)?\(|@import|expression\(|\\`)

// linkProblem says what is wrong with a link of element, read as the URL
// parser reads it (spaces and control characters at the ends cut, tabs
// and line breaks inside removed): it must point into the document, and
// on <a> it may also be an http(s) address.
func linkProblem(element, value string) string {
	u := strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(value)
	u = strings.TrimFunc(u, func(r rune) bool { return r <= ' ' })
	lower := strings.ToLower(u)
	if strings.HasPrefix(u, "#") || element == "a" && (strings.HasPrefix(lower, "http:") || strings.HasPrefix(lower, "https:")) {
		return ""
	}
	return fmt.Sprintf("<%s> links to %q", element, value)
}

// svgProblems lists what a browser could run or load in cleaned markup,
// and what keeps it from being one SVG document; "" has none.
func svgProblems(markup string) []string {
	if markup == "" {
		return nil
	}
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	d := xml.NewDecoder(strings.NewReader(markup))
	d.Strict = true
	var open []string
	roots := 0
	var style strings.Builder
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			add("does not parse: %v", err)
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			if len(open) == 0 {
				roots++
				if name != "svg" {
					add("the root is <%s>", name)
				}
			} else if parent := strings.ToLower(open[len(open)-1]); parent == "title" || parent == "desc" || parent == "style" {
				// an HTML parser reads what is inside title and desc as HTML
				add("<%s> inside <%s>", name, parent)
			}
			if t.Name.Space != testSVGNamespace && t.Name.Space != "" {
				add("<%s> in the namespace %q", name, t.Name.Space)
			}
			if svgActive[strings.ToLower(name)] {
				add("the element <%s>", name)
			}
			for _, a := range t.Attr {
				local := strings.ToLower(a.Name.Local)
				switch {
				case a.Name.Space == "" && a.Name.Local == "xmlns":
					if a.Value != testSVGNamespace {
						add("the default namespace %q", a.Value)
					}
					continue
				case a.Name.Space == "xmlns":
					if a.Name.Local != "xlink" || a.Value != testXLinkNamespace {
						add("the namespace %s=%q", a.Name.Local, a.Value)
					}
					continue
				case strings.HasPrefix(local, "on"):
					add("the event attribute %s", a.Name.Local)
				case local == "href":
					if a.Name.Space != "" && a.Name.Space != testXLinkNamespace {
						add("href in the namespace %q", a.Name.Space)
					}
					if p := linkProblem(name, a.Value); p != "" {
						add("%s", p)
					}
					continue
				case a.Name.Space == testXMLNamespace:
					if local != "space" && local != "lang" {
						add("the attribute xml:%s", a.Name.Local)
					}
				case a.Name.Space != "":
					add("the attribute %s in the namespace %q", a.Name.Local, a.Name.Space)
				case local == "attributename" || local == "begin" || local == "target" || local == "cursor":
					add("the attribute %s", a.Name.Local)
				}
				if reCSSLoad.MatchString(a.Value) {
					add("%s=%q loads something", a.Name.Local, a.Value)
				}
			}
			open = append(open, name)
			style.Reset()
		case xml.EndElement:
			if open[len(open)-1] == "style" && reCSSLoad.MatchString(style.String()) {
				add("<style> %q loads something", style.String())
			}
			open = open[:len(open)-1]
		case xml.CharData:
			if len(open) == 0 && strings.Trim(string(t), " \t\r\n") != "" {
				add("text %q outside the root", string(t))
			}
			style.Write(t)
		case xml.ProcInst:
			add("the processing instruction %s", t.Target)
		case xml.Directive:
			add("the declaration <!%s>", t)
		case xml.Comment:
			add("a comment")
		}
	}
	if roots != 1 && len(problems) == 0 {
		add("%d root elements", roots)
	}
	return problems
}

// svgShapes are the elements that draw, and svgGeometry their attributes
// that say what and where.
var (
	svgShapes   = setOf("path", "rect", "circle", "ellipse", "line", "polyline", "polygon", "text", "tspan")
	svgGeometry = setOf("d", "points", "x", "y", "width", "height", "rx", "ry", "cx", "cy", "r", "x1", "y1", "x2", "y2",
		"fill", "stroke", "stroke-width", "transform", "opacity", "class", "clip-path", "style")
)

// svgDrawing lists the shapes of SVG markup in order, each with its
// geometry and its text, and reports whether the markup is one SVG
// document at all.
func svgDrawing(markup string) (drawing string, isDocument bool) {
	d := xml.NewDecoder(strings.NewReader(markup))
	d.Strict = true
	var b strings.Builder
	var open []string
	roots := 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", false
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if len(open) == 0 {
				roots++
				if t.Name.Local != "svg" {
					return "", false
				}
			}
			open = append(open, t.Name.Local)
			if !svgShapes[t.Name.Local] {
				continue
			}
			b.WriteString("<" + t.Name.Local)
			for _, a := range t.Attr {
				if a.Name.Space == "" && svgGeometry[a.Name.Local] {
					fmt.Fprintf(&b, " %s=%q", a.Name.Local, a.Value)
				}
			}
			b.WriteString(">")
		case xml.EndElement:
			open = open[:len(open)-1]
		case xml.CharData:
			switch {
			case len(open) == 0 && strings.Trim(string(t), " \t\r\n") != "":
				return "", false
			case len(open) > 0 && (open[len(open)-1] == "text" || open[len(open)-1] == "tspan"):
				fmt.Fprintf(&b, "%q", string(t))
			}
		}
	}
	return b.String(), roots == 1
}

// checkCleanSVG fails the test when cleaned markup has problems or changes
// when it is cleaned again.
func checkCleanSVG(t testing.TB, in, out string) {
	t.Helper()
	if problems := svgProblems(out); len(problems) > 0 {
		t.Errorf("SanitizeSVG(%q) = %q: %s", in, out, strings.Join(problems, "; "))
	}
	if again := SanitizeSVG(out); again != out {
		t.Errorf("SanitizeSVG(%q) = %q, cleaned again %q", in, out, again)
	}
}

// TestSanitizeSVGSeesWhatAParserSees: the cleaner reads the markup as the
// browser's parser does, so a script link written with a character
// reference, an HTML entity or without quotes does not get through, and
// the removal of a link leaves no stray quote behind (BUGS BR13).
func TestSanitizeSVGSeesWhatAParserSees(t *testing.T) {
	const head = `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 4 4">`
	for _, in := range []string{
		head + `<a href="java&#115;cript:alert(1)"><rect width="4" height="4"/></a></svg>`,
		head + `<a href="javascript&colon;alert(1)"><rect width="4" height="4"/></a></svg>`,
		head + `<a href=javascript:alert(1)><rect width="4" height="4"/></a></svg>`,
		head + `<a xlink:href="&#x6A;avascript:alert(1)"><rect width="4" height="4"/></a></svg>`,
		head + `<a href="&#9;java&#10;script:alert(1)"><rect width="4" height="4"/></a></svg>`,
	} {
		out := SanitizeSVG(in)
		checkCleanSVG(t, in, out)
	}
	// a link the cleaner takes out leaves a document behind
	in := head + `<a href="javascript:alert(1)"><rect width="4" height="4"/></a></svg>`
	if out, want := SanitizeSVG(in), head+`<a><rect width="4" height="4"/></a></svg>`; out != want {
		t.Errorf("SanitizeSVG(%q)\n got  %q\n want %q", in, out, want)
	}
}

// svgTree is everything of SVG markup a parser hands on, comments,
// processing instructions and declarations left out: elements with all
// their attributes, and text.
func svgTree(t testing.TB, markup string) string {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(markup))
	d.Strict = true
	var b, text strings.Builder
	// text on both sides of a comment is one text once the comment is gone
	flush := func() {
		if text.Len() > 0 {
			fmt.Fprintf(&b, "%q", text.String())
			text.Reset()
		}
	}
	depth := 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return b.String()
		}
		if err != nil {
			t.Fatalf("%v in %q", err, clip(markup))
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			flush()
			depth++
			fmt.Fprintf(&b, "<%s %s", tok.Name.Space, tok.Name.Local)
			for _, a := range tok.Attr {
				fmt.Fprintf(&b, " %s %s=%q", a.Name.Space, a.Name.Local, a.Value)
			}
			b.WriteString(">")
		case xml.EndElement:
			flush()
			depth--
			fmt.Fprintf(&b, "</%s>", tok.Name.Local)
		case xml.CharData:
			// white space around the root is no part of the document
			if depth > 0 {
				text.Write(tok)
			}
		}
	}
}

// shippedSVGs are the SVG files this repository ships: the favicons, the
// fixture logo of the web app tests and, in a checkout with the web app,
// its flags and operating system marks.
func shippedSVGs(t testing.TB) map[string]string {
	t.Helper()
	files := []string{
		filepath.Join("..", "static", "files", "public", "favicon.svg"),
		filepath.Join("testdata", "console", "public", "favicon.svg"),
		filepath.Join("..", "webui", "testdata", "site", "img", "logo.svg"),
	}
	webApp := filepath.Join("..", "..", "..", "web-ui", "static", "img")
	if more, _ := filepath.Glob(filepath.Join(webApp, "*.svg")); len(more) > 0 {
		flags, _ := filepath.Glob(filepath.Join(webApp, "flags", "*.svg"))
		files = append(append(files, more...), flags...)
	} else {
		t.Logf("no web app in this checkout (%s): its pictures are not checked", webApp)
	}
	out := map[string]string{"defaultFaviconFallback": defaultFaviconFallback}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		out[file] = string(raw)
	}
	return out
}

// The logos and favicons the repository ships come through as they are,
// but for their comments.
func TestSanitizeSVGKeepsShippedPictures(t *testing.T) {
	shipped := shippedSVGs(t)
	if len(shipped) < 4 {
		t.Fatalf("%d pictures", len(shipped))
	}
	for name, in := range shipped {
		out := SanitizeSVG(in)
		checkCleanSVG(t, in, out)
		if got, want := svgTree(t, out), svgTree(t, in); got != want {
			t.Errorf("%s changed:\n got  %s\n want %s", name, clip(got), clip(want))
		}
	}
	for _, name := range []string{"defaultFaviconFallback", filepath.Join("..", "static", "files", "public", "favicon.svg")} {
		if out := SanitizeSVG(shipped[name]); out != strings.TrimSpace(shipped[name]) {
			t.Errorf("%s is not kept byte for byte: %q", name, out)
		}
	}
}

// TestSVGElementAllowlist: an element stays when it is one of these, and
// is left out with its content otherwise (BUGS BR13).
func TestSVGElementAllowlist(t *testing.T) {
	kept := []string{
		"svg", "g", "defs", "symbol", "use", "switch", "a", "title", "desc", "style",
		"path", "rect", "circle", "ellipse", "line", "polyline", "polygon", "text", "tspan", "textPath",
		"linearGradient", "radialGradient", "stop", "pattern", "clipPath", "mask", "marker",
		"filter", "feBlend", "feColorMatrix", "feComponentTransfer", "feComposite", "feConvolveMatrix",
		"feDiffuseLighting", "feDisplacementMap", "feDistantLight", "feDropShadow", "feFlood",
		"feFuncA", "feFuncB", "feFuncG", "feFuncR", "feGaussianBlur", "feMerge", "feMergeNode",
		"feMorphology", "feOffset", "fePointLight", "feSpecularLighting", "feSpotLight", "feTile", "feTurbulence",
	}
	dropped := []string{
		"script", "SCRIPT", "Script", "foreignObject", "foreignobject", "iframe", "embed", "object", "applet",
		"image", "feImage", "img", "animate", "animateMotion", "animateTransform", "set", "mpath", "discard",
		"handler", "listener", "metadata", "view", "cursor", "font", "font-face", "font-face-uri", "glyph",
		"html", "body", "div", "p", "br", "LinearGradient", "lineargradient", "Rect", "x",
	}
	for _, name := range kept {
		in := `<svg><g><` + name + ` id="e">t</` + name + `></g></svg>`
		if out := SanitizeSVG(in); out != in {
			t.Errorf("<%s> was not kept: %q", name, out)
		}
	}
	for _, name := range dropped {
		in := `<svg><g><` + name + ` id="e"><rect/>t</` + name + `><circle/></g></svg>`
		if out, want := SanitizeSVG(in), `<svg><g><circle/></g></svg>`; out != want {
			t.Errorf("<%s> was not left out with its content: %q", name, out)
		}
	}
	// an element with a namespace prefix, also one that stands for SVG,
	// or in another namespace is left out
	for _, in := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg" xmlns:s="http://www.w3.org/2000/svg"><s:script>alert(1)</s:script><s:rect/><circle/></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><g xmlns="http://www.w3.org/1999/xhtml"><rect/></g><circle/></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><g xmlns=""><rect/></g><circle/></svg>`,
	} {
		if out, want := SanitizeSVG(in), `<svg xmlns="http://www.w3.org/2000/svg"><circle/></svg>`; out != want {
			t.Errorf("SanitizeSVG(%q) = %q, want %q", in, out, want)
		}
	}
	if out, want := SanitizeSVG(`<svg><g xmlns="http://www.w3.org/2000/svg"><rect/></g></svg>`), `<svg><g xmlns="http://www.w3.org/2000/svg"><rect/></g></svg>`; out != want {
		t.Errorf("the SVG namespace on a group: %q", out)
	}
	// title, desc and style hold text only: an HTML parser reads what is
	// inside title and desc as HTML
	for _, name := range []string{"title", "desc", "style"} {
		in := `<svg><` + name + `>a<g><rect/></g>&lt;img src=x onerror=alert(1)&gt;</` + name + `></svg>`
		if out, want := SanitizeSVG(in), `<svg><`+name+`>a&lt;img src=x onerror=alert(1)&gt;</`+name+`></svg>`; out != want {
			t.Errorf("<%s>: %q, want %q", name, out, want)
		}
	}
}

// TestSVGAttributeAllowlist: an attribute stays when it is one of these,
// written exactly so (an event handler is never among them), and is left
// out otherwise (BUGS BR13).
func TestSVGAttributeAllowlist(t *testing.T) {
	kept := []string{
		"id", "class", "style", "lang", "role", "aria-label", "aria-labelledby", "aria-describedby", "aria-hidden",
		"viewBox", "preserveAspectRatio", "version", "width", "height", "x", "y", "transform",
		"x1", "y1", "x2", "y2", "cx", "cy", "r", "rx", "ry", "fx", "fy", "fr", "d", "points", "pathLength",
		"dx", "dy", "rotate", "textLength", "lengthAdjust", "startOffset", "method", "spacing", "side",
		"gradientUnits", "gradientTransform", "spreadMethod", "offset",
		"patternUnits", "patternContentUnits", "patternTransform", "clipPathUnits", "maskUnits", "maskContentUnits",
		"markerWidth", "markerHeight", "markerUnits", "refX", "refY", "orient", "media", "type",
		"filterUnits", "primitiveUnits", "in", "in2", "result", "stdDeviation", "mode", "operator",
		"k1", "k2", "k3", "k4", "values", "tableValues", "slope", "intercept", "amplitude", "exponent",
		"baseFrequency", "numOctaves", "seed", "stitchTiles", "scale", "xChannelSelector", "yChannelSelector",
		"radius", "order", "kernelMatrix", "divisor", "bias", "targetX", "targetY", "edgeMode", "preserveAlpha",
		"kernelUnitLength", "surfaceScale", "diffuseConstant", "specularConstant", "specularExponent",
		"azimuth", "elevation", "z", "pointsAtX", "pointsAtY", "pointsAtZ", "limitingConeAngle",
		"alignment-baseline", "baseline-shift", "clip", "clip-path", "clip-rule", "color", "color-interpolation",
		"color-interpolation-filters", "color-rendering", "direction", "display", "dominant-baseline",
		"fill", "fill-opacity", "fill-rule", "filter", "flood-color", "flood-opacity", "font", "font-family",
		"font-size", "font-size-adjust", "font-stretch", "font-style", "font-variant", "font-weight",
		"image-rendering", "isolation", "letter-spacing", "lighting-color", "marker-end", "marker-mid",
		"marker-start", "mask", "mask-type", "mix-blend-mode", "opacity", "overflow", "paint-order",
		"pointer-events", "shape-rendering", "stop-color", "stop-opacity", "stroke", "stroke-dasharray",
		"stroke-dashoffset", "stroke-linecap", "stroke-linejoin", "stroke-miterlimit", "stroke-opacity",
		"stroke-width", "text-anchor", "text-decoration", "text-rendering", "transform-origin", "unicode-bidi",
		"vector-effect", "visibility", "word-spacing", "writing-mode",
	}
	dropped := []string{
		"onload", "onclick", "ONLOAD", "onClick", "onbegin", "onfocusin", "on", "attributeName", "begin", "to", "from", "by",
		"target", "cursor", "src", "data", "xlink", "viewbox", "VIEWBOX", "Fill", "data-x", "tabindex", "contentScriptType",
		"requiredExtensions", "systemLanguage", "enable-background", "ping", "download", "formaction", "srcdoc",
	}
	for _, name := range kept {
		in := `<svg><rect ` + name + `="1"/></svg>`
		if out := SanitizeSVG(in); out != in {
			t.Errorf("%s was not kept: %q", name, out)
		}
	}
	for _, name := range dropped {
		in := `<svg><rect ` + name + `="1" width="2"/></svg>`
		if out, want := SanitizeSVG(in), `<svg><rect width="2"/></svg>`; out != want {
			t.Errorf("%s was not left out: %q", name, out)
		}
	}
	// the namespace declarations and the attributes with a prefix
	for in, want := range map[string]string{
		`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" xml:space="preserve" xml:lang="de"/>`: `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" xml:space="preserve" xml:lang="de"/>`,
		`<svg xmlns:xlink="urn:other" xmlns:ev="http://www.w3.org/2001/xml-events" xmlns:svg="http://www.w3.org/2000/svg"/>`:      `<svg/>`,
		`<svg xml:base="http://evil.example/" xml:id="a" inkscape:label="x" ev:event="click"/>`:                                   `<svg/>`,
	} {
		if out := SanitizeSVG(in); out != want {
			t.Errorf("SanitizeSVG(%q) = %q, want %q", in, out, want)
		}
	}
}

// TestSVGLinks: href and xlink:href stay with a link into the document,
// and on <a> also with an http(s) address, read as the URL parser reads
// them; xlink:href only where the prefix stands for the XLink namespace
// (BUGS BR13).
func TestSVGLinks(t *testing.T) {
	const xlink = `xmlns:xlink="http://www.w3.org/1999/xlink"`
	for _, tc := range []struct {
		element, link string
		kept          bool
	}{
		{"use", "#a", true}, {"use", " #a", true}, {"use", "\t#a", true}, {"linearGradient", "#g", true}, {"a", "#top", true},
		{"a", "https://example.com/", true}, {"a", "HTTP://example.com/", true}, {"a", "ht\ttp://example.com/", true}, {"a", " https://example.com/", true},
		{"use", "https://example.com/x.svg#a", false}, {"use", "data:image/svg+xml,x", false}, {"use", "x.svg#a", false},
		{"pattern", "/img/x.svg#a", false}, {"textPath", "http://example.com/#p", false},
		{"a", "javascript:alert(1)", false}, {"a", "JavaScript:alert(1)", false}, {"a", "java\tscript:alert(1)", false},
		{"a", "\x01javascript:alert(1)", false}, {"a", "data:text/html,x", false}, {"a", "vbscript:x", false},
		{"a", "/admin", false}, {"a", "//evil.example/", false}, {"a", "httpx://example.com", false}, {"a", "", false},
	} {
		value := strings.NewReplacer("\t", "&#9;", "\x01", "&#1;").Replace(tc.link)
		if strings.ContainsRune(tc.link, '\x01') {
			// a control character the XML parser refuses even as a reference
			if out := SanitizeSVG(`<svg><` + tc.element + ` href="` + value + `"/></svg>`); out != "" {
				t.Errorf("%q on <%s>: %q", tc.link, tc.element, out)
			}
			continue
		}
		for _, attr := range []string{"href", "xlink:href"} {
			in := `<svg ` + xlink + `><` + tc.element + ` ` + attr + `="` + value + `"/></svg>`
			want := `<svg ` + xlink + `><` + tc.element + `/></svg>`
			if tc.kept {
				want = in
			}
			if out := SanitizeSVG(in); out != want {
				t.Errorf("%s=%q on <%s>: %q, want %q", attr, tc.link, tc.element, out, want)
			}
		}
	}
	// xlink:href where xlink is no prefix of the XLink namespace
	for _, in := range []string{
		`<svg><use xlink:href="#a"/></svg>`,
		`<svg xmlns:xlink="urn:x"><use xlink:href="#a"/></svg>`,
		`<svg xmlns:xlink="http://www.w3.org/1999/xlink"><g xmlns:xlink="urn:x"><use xlink:href="#a"/></g></svg>`,
		`<svg xmlns:l="http://www.w3.org/1999/xlink"><use l:href="#a"/></svg>`,
	} {
		out := SanitizeSVG(in)
		if strings.Contains(out, "href") {
			t.Errorf("SanitizeSVG(%q) = %q", in, out)
		}
		checkCleanSVG(t, in, out)
	}
	in := `<svg xmlns:xlink="urn:x"><g xmlns:xlink="http://www.w3.org/1999/xlink"><use xlink:href="#a"/></g></svg>`
	if out, want := SanitizeSVG(in), `<svg><g xmlns:xlink="http://www.w3.org/1999/xlink"><use xlink:href="#a"/></g></svg>`; out != want {
		t.Errorf("an XLink prefix declared inside: %q", out)
	}
}

// TestSVGStyles: a style sheet, style attribute or presentation attribute
// that could load something is left out; one that only points into the
// document stays (BUGS BR13).
func TestSVGStyles(t *testing.T) {
	safe := []string{".a{fill:red}", ".a{fill:url(#g)}", `.a{fill:url( "#g" )}`, `.a{fill:url(" #g")}`, ".a{fill:URL('#g')}", "@media (prefers-color-scheme: dark){.a{fill:#fff}}", "a>b{c:d}"}
	unsafe := []string{
		"@import 'x.css';", "@IMPORT url(x.css);", ".a{fill:url(x.svg#g)}", ".a{fill:url( 'http://evil.example/' )}", ".a{fill:url(#g),url(/x)}",
		".a{fill:Url(data:image/svg+xml,x)}", ".a{fill:url(}", ".a{mask-image:image-set('x.png' 1x)}", ".a{mask-image:-webkit-image-set(url(#a) 1x)}",
		".a{mask-image:image('x.png')}", ".a{width:expression(alert(1))}", `.a{fill:u\72l(x)}`, `@\69mport 'x';`,
	}
	for _, css := range safe {
		escaped := strings.ReplaceAll(strings.ReplaceAll(css, ">", "&gt;"), `"`, "&quot;")
		element := `<svg><style>` + strings.ReplaceAll(css, ">", "&gt;") + `</style></svg>`
		if out := SanitizeSVG(element); out != element {
			t.Errorf("the style sheet %q: %q", css, out)
		}
		attrs := `<svg><rect style="` + escaped + `" fill="` + escaped + `"/></svg>`
		if out := SanitizeSVG(attrs); out != attrs {
			t.Errorf("the attributes %q: %q", css, out)
		}
	}
	for _, css := range unsafe {
		escaped := strings.ReplaceAll(css, `"`, "&quot;")
		for in, want := range map[string]string{
			`<svg><style>` + css + `</style></svg>`:                                 `<svg/>`,
			`<svg><g><style>` + css + `</style></g><style>.b{}</style></svg>`:       `<svg><g/><style>.b{}</style></svg>`,
			`<svg><rect style="` + escaped + `" width="1"/></svg>`:                  `<svg><rect width="1"/></svg>`,
			`<svg><rect fill="` + escaped + `" clip-path="` + escaped + `"/></svg>`: `<svg><rect/></svg>`,
		} {
			if out := SanitizeSVG(in); out != want {
				t.Errorf("SanitizeSVG(%q) = %q, want %q", in, out, want)
			}
		}
	}
}

// TestSVGDocument: markup that is not one well-formed SVG document gives
// "", and what is no element or text is left out (BUGS BR13).
func TestSVGDocument(t *testing.T) {
	for _, in := range []string{
		"", " ", "x", "<rect/>", "<g><svg/></g>", "<SVG/>", "<s:svg xmlns:s=\"http://www.w3.org/2000/svg\"/>",
		`<svg xmlns="http://www.w3.org/1999/xhtml"/>`, "<svg/><svg/>", "x<svg/>", "<svg/>x", "<svg>", "<svg></g>", "</svg>",
		"<svg><rect></svg>", `<svg><rect width="1" width="2"/></svg>`, `<svg><rect width=1/></svg>`, "<svg><g onclick></g></svg>",
		"<svg>&nbsp;</svg>", "<!DOCTYPE svg [<!ENTITY x \"y\">]><svg>&x;</svg>", "<svg><!-- a -- b --></svg>", "<svg>\x01</svg>",
		"<svg>\xff</svg>", "<svg><ſcript>x</ſcript></svg>", strings.Repeat("<g>", svgMaxDepth) + strings.Repeat("</g>", svgMaxDepth),
		"<svg>" + strings.Repeat("<g>", svgMaxDepth) + strings.Repeat("</g>", svgMaxDepth) + "</svg>",
	} {
		if out := SanitizeSVG(in); out != "" {
			t.Errorf("SanitizeSVG(%q) = %q, want \"\"", clip(in), clip(out))
		}
	}
	deep := "<svg>" + strings.Repeat("<g>", svgMaxDepth-1) + strings.Repeat("</g>", svgMaxDepth-1) + "</svg>"
	if out := SanitizeSVG(deep); !strings.HasPrefix(out, "<svg><g><g>") {
		t.Errorf("%d levels: %q", svgMaxDepth, clip(out))
	}
	for _, v := range []any{nil, Undefined, 5.0, []any{"<svg/>"}, object("svg", "<svg/>")} {
		if out := SanitizeSVG(v); out != "" {
			t.Errorf("SanitizeSVG(%s) = %q", Stringify(v), out)
		}
	}
	for in, want := range map[string]string{
		byteOrderMark + "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?>\n<!DOCTYPE svg PUBLIC \"-//W3C//DTD SVG 1.1//EN\" \"http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd\">\n<!-- made by hand -->\n<svg>\n<?xml-stylesheet href=\"x.css\"?><g><!-- c --></g>\n</svg>\n": "<svg>\n<g/>\n</svg>",
		`<svg><rect></rect><g> </g><text>a</text></svg>`:                                                   `<svg><rect/><g> </g><text>a</text></svg>`,
		`<svg><g><script/></g><g><style>@import 'x';</style></g><g><style>.a{}</style></g></svg>`:          `<svg><g/><g/><g><style>.a{}</style></g></svg>`,
		`<svg><text x='1' y = "2">&lt;b&gt; &amp; &quot;q&quot; &apos;</text><![CDATA[a<b]]></svg>`:        `<svg><text x="1" y="2">&lt;b&gt; &amp; "q" '</text>a&lt;b</svg>`,
		"<svg><rect id=\"&quot;&lt;&gt;&amp;&#9;&#10;&#13;\" class=\"a\nb\"/><text>&#13;\r\n</text></svg>": "<svg><rect id=\"&quot;&lt;&gt;&amp;&#9;&#10;&#13;\" class=\"a&#10;b\"/><text>&#13;\n</text></svg>",
		`<svg><text>café &#x1F44D; 你好</text></svg>`:                                                        `<svg><text>café 👍 你好</text></svg>`,
	} {
		out := SanitizeSVG(in)
		if out != want {
			t.Errorf("SanitizeSVG(%q)\n got  %q\n want %q", in, out, want)
		}
		checkCleanSVG(t, in, out)
	}
}
