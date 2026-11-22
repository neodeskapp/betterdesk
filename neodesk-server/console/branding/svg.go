package branding

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// An administrator's logo and favicon are SVG markup. The browser reads
// that markup with its XML parser: the logo as an <img> with a data: URL,
// the favicon as the image/svg+xml document /branding/favicon.svg. A
// client that puts the logo into an HTML page reads it with the HTML
// parser instead. SanitizeSVG reads the markup with an XML parser as well
// and writes back only what the allowlists below name, in a form both
// parsers read the same way: every attribute value in double quotes, the
// characters & < > and quotes as references, elements that hold HTML in
// an HTML page (title, desc) with text only.

const (
	svgNamespace   = "http://www.w3.org/2000/svg"
	xlinkNamespace = "http://www.w3.org/1999/xlink"

	// svgMaxDepth is how deeply elements may nest. Logos nest a few
	// levels; the limit keeps a pasted text of nested tags from growing
	// the cleaner's stack without end.
	svgMaxDepth = 256

	// byteOrderMark is U+FEFF in UTF-8.
	byteOrderMark = "\xef\xbb\xbf"
)

// svgElements are the elements a logo or favicon may have, by their exact
// name: shapes, groups, text, gradients and patterns, clipping and masks,
// markers, filters, <use>, <a>, <style> and the descriptions. Anything
// else is left out with everything inside it: script, foreignObject,
// image and feImage, the animation elements, metadata and the elements of
// other namespaces among them.
var svgElements = setOf(
	"svg", "g", "defs", "symbol", "use", "switch", "a", "title", "desc", "style",
	"path", "rect", "circle", "ellipse", "line", "polyline", "polygon",
	"text", "tspan", "textPath",
	"linearGradient", "radialGradient", "stop", "pattern", "clipPath", "mask", "marker",
	"filter", "feBlend", "feColorMatrix", "feComponentTransfer", "feComposite", "feConvolveMatrix",
	"feDiffuseLighting", "feDisplacementMap", "feDistantLight", "feDropShadow", "feFlood",
	"feFuncA", "feFuncB", "feFuncG", "feFuncR", "feGaussianBlur", "feMerge", "feMergeNode",
	"feMorphology", "feOffset", "fePointLight", "feSpecularLighting", "feSpotLight", "feTile", "feTurbulence",
)

// svgTextOnly are elements that keep their text and lose their child
// elements: an HTML parser reads what is inside title and desc as HTML,
// and a style sheet is text.
var svgTextOnly = setOf("title", "desc", "style")

// svgAttributes are the attributes without a namespace prefix that stay,
// by their exact name: geometry, the presentation attributes, those of
// gradients, patterns, markers, masks, text and filters, ids, classes,
// style and the accessibility labels. Event handlers (on...) are not among
// them, nor target, cursor or the attributes of animation. href is checked
// on its own (svgLink).
var svgAttributes = setOf(
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
)

func setOf(names ...string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set
}

var errNotSVG = errors.New("not one SVG document")

// SanitizeSVG cleans SVG markup for a logo or favicon. It reads the markup
// with an XML parser, as the browser does, and writes back the root <svg>
// with what the allowlists let through:
//
//   - elements of svgElements without a namespace prefix whose namespace
//     is the SVG namespace (or none, for markup without one); any other
//     element is left out with its content, and title, desc and style keep
//     only their text;
//   - attributes of svgAttributes whose value loads nothing (unsafeCSS),
//     xml:space and xml:lang, the declarations of the SVG namespace and of
//     the XLink namespace as xlink, and href or xlink:href with a link
//     into the document ("#...") or, on <a>, an http(s) address;
//   - <style> only when its style sheet loads nothing;
//   - text; comments, processing instructions and DOCTYPE are left out.
//
// Markup that is not well-formed XML, has no <svg> root, has a root in
// another namespace, repeats an attribute or nests deeper than
// svgMaxDepth gives "", as does a value that is not a string. Cleaning
// the result again does not change it.
func SanitizeSVG(svg any) string {
	s, ok := svg.(string)
	if !ok || s == "" {
		return ""
	}
	out, err := cleanSVG(s)
	if err != nil {
		return ""
	}
	return out
}

// svgOpen is an element of the markup that is open while the cleaner reads.
type svgOpen struct {
	name xml.Name
	kept bool
	// xlink says that the prefix xlink stands for the XLink namespace here
	xlink bool
	// start is where the element begins in the output, and parentPending
	// whether its parent's start tag was still open then: a style sheet
	// that loads something is cut off there when its end is reached
	start         int
	parentPending bool
}

type svgCleaner struct {
	out  bytes.Buffer
	open []svgOpen
	// skip counts the open elements that are left out
	skip int
	// pending says that the last start tag written still lacks its ">",
	// so an element without content can end in "/>"
	pending bool
	root    bool
	css     strings.Builder
}

func cleanSVG(markup string) (string, error) {
	// a byte order mark before the markup is not part of it
	d := xml.NewDecoder(strings.NewReader(strings.TrimPrefix(markup, byteOrderMark)))
	d.Strict = true
	// the text is already Unicode; the declared encoding is left out with
	// the declaration
	d.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	c := &svgCleaner{}
	for {
		token, err := d.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch t := token.(type) {
		case xml.StartElement:
			err = c.start(t)
		case xml.EndElement:
			err = c.end(t)
		case xml.CharData:
			err = c.text(t)
		}
		if err != nil {
			return "", err
		}
	}
	if !c.root || len(c.open) > 0 {
		return "", errNotSVG
	}
	return c.out.String(), nil
}

func (c *svgCleaner) start(t xml.StartElement) error {
	depth := len(c.open)
	if depth == 0 && c.root || depth >= svgMaxDepth {
		return errNotSVG
	}
	seen := make(map[xml.Name]bool, len(t.Attr))
	for _, a := range t.Attr {
		if seen[a.Name] {
			return errNotSVG
		}
		seen[a.Name] = true
	}
	keep := c.skip == 0 && t.Name.Space == "" && svgElements[t.Name.Local]
	xlink := false
	if depth > 0 {
		parent := c.open[depth-1]
		keep = keep && !svgTextOnly[parent.name.Local]
		xlink = parent.xlink
	}
	for _, a := range t.Attr {
		switch {
		case a.Name.Space == "" && a.Name.Local == "xmlns":
			keep = keep && a.Value == svgNamespace
		case a.Name.Space == "xmlns" && a.Name.Local == "xlink":
			xlink = a.Value == xlinkNamespace
		}
	}
	if depth == 0 {
		if !keep || t.Name.Local != "svg" {
			return errNotSVG
		}
		c.root = true
	}
	element := svgOpen{name: t.Name, kept: keep, xlink: xlink, start: c.out.Len(), parentPending: c.pending}
	c.open = append(c.open, element)
	if !keep {
		c.skip++
		return nil
	}
	c.closeStartTag()
	c.out.WriteByte('<')
	c.out.WriteString(t.Name.Local)
	for _, a := range t.Attr {
		if !keepSVGAttr(t.Name.Local, a, xlink) {
			continue
		}
		c.out.WriteByte(' ')
		if a.Name.Space != "" {
			c.out.WriteString(a.Name.Space)
			c.out.WriteByte(':')
		}
		c.out.WriteString(a.Name.Local)
		c.out.WriteString(`="`)
		writeSVGEscaped(&c.out, a.Value, true)
		c.out.WriteByte('"')
	}
	c.pending = true
	if t.Name.Local == "style" {
		c.css.Reset()
	}
	return nil
}

func (c *svgCleaner) end(t xml.EndElement) error {
	if len(c.open) == 0 {
		return errNotSVG
	}
	element := c.open[len(c.open)-1]
	c.open = c.open[:len(c.open)-1]
	if element.name != t.Name {
		return errNotSVG
	}
	if !element.kept {
		c.skip--
		return nil
	}
	if element.name.Local == "style" && unsafeCSS(c.css.String()) {
		c.out.Truncate(element.start)
		c.pending = element.parentPending
		return nil
	}
	if c.pending {
		c.out.WriteString("/>")
		c.pending = false
		return nil
	}
	c.out.WriteString("</")
	c.out.WriteString(element.name.Local)
	c.out.WriteByte('>')
	return nil
}

func (c *svgCleaner) text(t xml.CharData) error {
	if len(c.open) == 0 {
		if strings.Trim(string(t), " \t\r\n") != "" {
			return errNotSVG
		}
		return nil
	}
	if c.skip > 0 {
		return nil
	}
	c.closeStartTag()
	if c.open[len(c.open)-1].name.Local == "style" {
		c.css.Write(t)
	}
	writeSVGEscaped(&c.out, string(t), false)
	return nil
}

// closeStartTag ends the start tag written last with ">", before content.
func (c *svgCleaner) closeStartTag() {
	if c.pending {
		c.out.WriteByte('>')
		c.pending = false
	}
}

// keepSVGAttr reports whether attribute a of element stays. xlink says
// whether the prefix xlink stands for the XLink namespace there.
func keepSVGAttr(element string, a xml.Attr, xlink bool) bool {
	switch a.Name.Space {
	case "":
		switch {
		case a.Name.Local == "xmlns":
			// the element is kept, so this is the SVG namespace
			return true
		case a.Name.Local == "href":
			return svgLink(element, a.Value)
		}
		return svgAttributes[a.Name.Local] && !unsafeCSS(a.Value)
	case "xmlns":
		return a.Name.Local == "xlink" && a.Value == xlinkNamespace
	case "xml":
		return a.Name.Local == "space" || a.Name.Local == "lang"
	case "xlink":
		return a.Name.Local == "href" && xlink && svgLink(element, a.Value)
	}
	return false
}

// svgLink reports whether a link of element may stay: one into the
// document ("#..."), or on <a> one with the scheme http or https. The
// value is read as the browser's URL parser reads it: spaces and control
// characters at both ends are cut, tabs and line breaks anywhere removed.
func svgLink(element, value string) bool {
	u := strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, value)
	u = strings.TrimFunc(u, func(r rune) bool { return r <= ' ' })
	if strings.HasPrefix(u, "#") {
		return true
	}
	scheme, _, found := strings.Cut(u, ":")
	scheme = asciiLower(scheme)
	return element == "a" && found && (scheme == "http" || scheme == "https")
}

// unsafeCSS reports style text (a style sheet, a style attribute or a
// presentation attribute) that could load something: a url() whose target
// is not "#...", image-set() or image(), @import, the script of
// expression(), and any backslash, as an escape could spell one of these
// past this check.
func unsafeCSS(css string) bool {
	if strings.ContainsRune(css, '\\') {
		return true
	}
	lower := asciiLower(css)
	for _, word := range []string{"@import", "image-set(", "image(", "expression("} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	for rest := lower; ; {
		i := strings.Index(rest, "url(")
		if i < 0 {
			return false
		}
		rest = rest[i+len("url("):]
		target := strings.TrimLeft(rest, cssSpace)
		if target != "" && (target[0] == '"' || target[0] == '\'') {
			target = strings.TrimLeft(target[1:], cssSpace)
		}
		if !strings.HasPrefix(target, "#") {
			return true
		}
	}
}

// cssSpace is white space in CSS.
const cssSpace = " \t\n\r\f"

// asciiLower lowers the ASCII letters of s and leaves every other
// character as it is, as CSS and URL schemes compare names.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// writeSVGEscaped writes text with & < > as references, and in an
// attribute value (attr) also the double quote, tabs and line breaks, so
// both an XML and an HTML parser read back exactly the value. A carriage
// return in text is written as a reference too, as a parser would turn a
// raw one into a line feed.
func writeSVGEscaped(b *bytes.Buffer, s string, attr bool) {
	last := 0
	for i := 0; i < len(s); i++ {
		var ref string
		switch s[i] {
		case '&':
			ref = "&amp;"
		case '<':
			ref = "&lt;"
		case '>':
			ref = "&gt;"
		case '\r':
			ref = "&#13;"
		case '"':
			if attr {
				ref = "&quot;"
			}
		case '\t':
			if attr {
				ref = "&#9;"
			}
		case '\n':
			if attr {
				ref = "&#10;"
			}
		}
		if ref == "" {
			continue
		}
		b.WriteString(s[last:i])
		b.WriteString(ref)
		last = i + 1
	}
	b.WriteString(s[last:])
}
