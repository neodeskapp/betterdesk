package branding

import (
	"strings"
	"testing"
)

func problemText(problems []Problem) string {
	var parts []string
	for _, p := range problems {
		text := p.Field + ":" + p.Reason
		if parts := p.Params["parts"]; parts != "" {
			text += "(" + parts + ")"
		}
		if max := p.Params["max"]; max != "" {
			text += "(" + max + ")"
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, " ")
}

// CheckSettings names every value SaveBranding would store otherwise than
// given (BUGS L366, L367, L368), and nothing else.
func TestCheckSettings(t *testing.T) {
	for _, c := range []struct{ body, want string }{
		{`{"bgColor":"red;}","loginBgColor":"#12345g","agentBgColor":"rgb(1, 2, 3)","rdclientBgColor":"teal","glassColor":""}`,
			"bgColor:color loginBgColor:color"},
		{`{"colors":{"accentBlue":"expression(alert(1))","bgPrimary":"#0a0a0a","textPrimary":"#fff","unknown":"x"}}`, "colors.accentBlue:color"},
		{`{"colors":"red"}`, "colors:color"},
		{`{"bgGradient":"red","loginBgGradient":"linear-gradient(red, blue);x","agentBgGradient":"radial-gradient(circle, #fff 0%, #000 100%)"}`,
			"bgGradient:gradient loginBgGradient:gradient"},
		{`{"bgGradient":"linear-gradient(` + strings.Repeat("#111111 ", 60) + `)"}`, "bgGradient:gradient"},
		{`{"logoUrl":"javascript:alert(1)","bgImageUrl":"//host/a.png","loginBgImageUrl":"/../etc","agentBgImageUrl":"https://example.com/a.png","rdclientBgImageUrl":"/uploads/a.png","faviconUrl":""}`,
			"logoUrl:address bgImageUrl:address loginBgImageUrl:address agentBgImageUrl:outside_address"},
		{`{"logoSvg":"<svg onload=\"x()\"><script>x()</script><rect/><image href=\"a.png\"/></svg>"}`, "logoSvg:svg_parts(onload, <script>, <image>)"},
		{`{"faviconSvg":"<svg><style>rect{fill:url(http://x/a)}</style><rect/></svg>"}`, "faviconSvg:svg_parts(<style>)"},
		{`{"logoSvg":"<svg xmlns=\"http://www.w3.org/2000/svg\" xmlns:sodipodi=\"x\" sodipodi:docname=\"a.svg\"><sodipodi:namedview/><metadata>m</metadata><!-- c --><rect/></svg>"}`, ""},
		{`{"logoSvg":"<div/>","faviconSvg":""}`, "logoSvg:svg"},
		{`{"customCss":".a { color: red }"}`, ""},
		{`{"customCss":"<style>.a{}</style>"}`, "customCss:custom_css"},
		{`{"customCss":"` + strings.Repeat("a", 20001) + `"}`, "customCss:custom_css_long(20000)"},
		{`{"themeMode":"auto","loginPromoMode":"Slides","bgSize":"huge","rdclientBgSize":"contain"}`, "bgSize:choice loginPromoMode:choice themeMode:choice"},
		{`{"appearance":{"rdclient":{"rdclientBgSize":"big"}}}`, "rdclientBgSize:choice"},
		{`{"themeMode":"custom","loginPromoMode":"single","bgSize":""}`, ""},
		{`{"loginSlides":"not a list"}`, "loginSlides:slides"},
		{`{"loginSlides":[null,{"imageUrl":"javascript:x"},{"title":"` + strings.Repeat("t", 121) + `","body":"b"}]}`,
			"loginSlides.0:slides loginSlides.1.imageUrl:address loginSlides.2.title:too_long(120)"},
		{`{"loginSlides":[{},{},{},{},{},{},{},{},{},{},{}]}`, "loginSlides:slides"},
		{`{"appearance":{"console":{"bgColor":"x;y"}}}`, "bgColor:color"},
		{`{"appName":"any text <b>","footerText":"x","notASetting":"javascript:x"}`, ""},
	} {
		body, err := ParseJSON([]byte(c.body))
		if err != nil {
			t.Fatal(err)
		}
		if got := problemText(CheckSettings(body, nil)); got != c.want {
			t.Errorf("%.120s\n got  %s\n want %s", c.body, got, c.want)
		}
	}
}

// A value that is stored already is not refused: the form sends hidden
// settings back as they are.
func TestCheckSettingsLeavesStoredValues(t *testing.T) {
	current := object("bgGradient", "javascriptalert(1))", "bgColor", "#102030", "colors", object("accentBlue", "nope"))
	body, _ := ParseJSON([]byte(`{"bgGradient":"javascriptalert(1))","bgColor":"red;}","colors":{"accentBlue":"nope","bgPrimary":"nope"}}`))
	if got, want := problemText(CheckSettings(body, current)), "bgColor:color colors.bgPrimary:color"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// What CheckSettings lets through, SaveBranding stores as it was given.
func TestCheckedValuesAreStoredAsGiven(t *testing.T) {
	for _, value := range []string{"#102030", "rgb(10, 20, 30)", "teal", "linear-gradient(135deg, #1e293b 0%, #0f172a 100%)"} {
		if got := sanitizeCSSColorValue(value); got != value {
			t.Errorf("%q is stored as %q", value, got)
		}
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 4 4"><a href="#x"><rect width="4" height="4" fill="#123456"/></a></svg>`
	if removed, ok := SVGRemovals(svg); !ok || len(removed) != 0 {
		t.Errorf("a clean SVG: %v %v", removed, ok)
	}
}
