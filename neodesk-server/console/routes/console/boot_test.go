package console

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/branding"
)

// nodeA11y is the part of the Node boot script that applied the old
// accessibility panel's 20 settings. The console applies the six that
// replaced them (bootScriptA11y) in its place.
const nodeA11y = `    // Accessibility panel settings: same key, defaults and limits as
    // web-ui/src/lib/shell/a11y.svelte.ts
    var a = {};
    try { a = JSON.parse(get('neodesk_accessibility_v1') || '{}') || {}; } catch (e) { a = {}; }
    function num(v, min, max, d) { v = Number(v); return isFinite(v) ? Math.min(max, Math.max(min, v)) : d; }
    var fontScale = num(a.fontScale, 90, 160, 100), lineHeight = num(a.lineHeight, 1.2, 2.1, 1.5);
    var letter = num(a.letterSpacing, 0, 0.14, 0), word = num(a.wordSpacing, 0, 0.22, 0), sat = num(a.saturation, 0, 180, 100);
    var contrast = ['normal', 'high-dark', 'high-light', 'yellow-black'].indexOf(a.contrast) >= 0 ? a.contrast : 'normal';
    var filter = ['none', 'protanopia', 'deuteranopia', 'tritanopia', 'achromatopsia'].indexOf(a.colorFilter) >= 0 ? a.colorFilter : 'none';
    var FLAGS = { readableFont: 'a11yReadableFont', dyslexiaFont: 'a11yDyslexia', underlineLinks: 'a11yUnderlineLinks',
        highlightLinks: 'a11yHighlightLinks', highlightHeadings: 'a11yHighlightHeadings', strongFocus: 'a11yStrongFocus',
        largeCursor: 'a11yLargeCursor', reduceMotion: 'a11yReduceMotion', pauseAnimations: 'a11yPauseAnimations',
        readingGuide: 'a11yReadingGuide', readingMask: 'a11yReadingMask', hideMedia: 'a11yHideMedia' };
    var active = fontScale !== 100 || lineHeight !== 1.5 || letter !== 0 || word !== 0 || sat !== 100 || contrast !== 'normal' || filter !== 'none';
    for (var f in FLAGS) { var on = a[f] === true; if (on) active = true; root.dataset[FLAGS[f]] = String(on); }
    root.style.setProperty('--bd-a11y-font-scale', String(fontScale / 100));
    root.style.setProperty('--bd-a11y-line-height', String(lineHeight));
    root.style.setProperty('--bd-a11y-letter-spacing', letter + 'em');
    root.style.setProperty('--bd-a11y-word-spacing', word + 'em');
    root.dataset.a11yActive = String(active);
    root.dataset.a11yFontScaled = String(fontScale !== 100);
    root.dataset.a11yContrast = contrast;
    var filters = [];
    if (filter !== 'none') filters.push('url(#bd-a11y-' + filter + ')');
    if (sat !== 100) filters.push('saturate(' + sat + '%)');
    root.dataset.a11yFiltered = String(filters.length > 0);
    root.style.setProperty('--bd-a11y-visual-filter', filters.length ? filters.join(' ') : 'none');
`

// The boot script is, byte for byte, the one the Node route sent for the
// same boot values (testdata/bootscript.json, recorded from the Node
// console, see testdata/README.md): the script itself, the values as
// JSON.stringify wrote them, and every "<" escaped. One exception: the
// accessibility settings are the new ones (nodeA11y).
func TestBootScriptAsNodeSentIt(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "bootscript.json"))
	if err != nil {
		t.Fatal(err)
	}
	var recorded struct {
		Vectors []struct {
			Boot json.RawMessage `json:"boot"`
			Body string          `json:"body"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatal(err)
	}
	if len(recorded.Vectors) < 50 {
		t.Fatalf("only %d vectors", len(recorded.Vectors))
	}
	for i, v := range recorded.Vectors {
		parsed, err := branding.ParseJSON(v.Boot)
		if err != nil {
			t.Fatalf("vector %d: %v", i, err)
		}
		boot := parsed.(*branding.Object)
		boot.Set("publicPaths", publicPaths)
		if strings.Count(v.Body, nodeA11y) != 1 {
			t.Fatalf("vector %d: the recording has no accessibility part", i)
		}
		want := strings.Replace(v.Body, nodeA11y, bootScriptA11y, 1)
		if got := string(bootScriptBody(boot)); got != want {
			t.Errorf("vector %d (%s): the script differs\n  node: ...%s\n    go: ...%s", i, v.Boot, tail(v.Body), tail(got))
		}
	}
}

func tail(s string) string {
	if len(s) > 400 {
		return s[len(s)-400:]
	}
	return s
}
