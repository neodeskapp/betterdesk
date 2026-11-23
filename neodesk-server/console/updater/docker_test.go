package updater

import (
	"reflect"
	"testing"
)

// A Docker install runs the one image ghcr.io/<owner>/neodesk, and the
// Updates page names it whatever NEODESK_DOCKER_LAYOUT says: a
// docker-compose.single.yml build sets none, and the split images
// (neodesk-console next to neodesk-server) are no longer published
// (L225).
func TestDockerInstructionsNameTheOneImage(t *testing.T) {
	for _, layout := range []string{"", "single", "split"} {
		in := newInstall(t, func(o *Options) { o.Docker = true })
		if layout != "" {
			in.env["NEODESK_DOCKER_LAYOUT"] = layout
		}
		in.env["NEODESK_IMAGE_TAG"] = "3.6.0-rc.1"
		got := New(in.opts).DockerInstructions()
		if want := []string{"ghcr.io/neodeskapp/neodesk:3.6.0-rc.1"}; !reflect.DeepEqual(got.Images, want) {
			t.Errorf("layout %q: images %q, want %q", layout, got.Images, want)
		}
		if got.ComposeHint != "docker-compose.quick.single.yml" || got.Layout != "single" {
			t.Errorf("layout %q: compose hint %q, layout %q", layout, got.ComposeHint, got.Layout)
		}
	}
}
