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

// With NEODESK_IMAGE_TAG pinned to a version, "docker compose pull" fetches
// that version again and never the new one: the commands first set the
// tag to the version the check found (BUGS L407).
func TestDockerCommandsMoveAPinnedTag(t *testing.T) {
	for _, c := range []struct{ tag, latest, first string }{
		{"3.6.0-rc.2", "3.6.4", "sed -i 's/^NEODESK_IMAGE_TAG=.*/NEODESK_IMAGE_TAG=3.6.4/' .env"},
		{"latest", "3.6.4", "docker compose pull"},
		{"3.6.4", "3.6.4", "docker compose pull"},
		{"3.6.0", "3.6.4; rm -rf /", "docker compose pull"},
	} {
		in := newInstall(t, func(o *Options) { o.Docker = true; o.Version = "3.6.0" })
		in.env["NEODESK_IMAGE_TAG"] = c.tag
		out := &CheckResult{LatestVersion: c.latest}
		New(in.opts).withDeployment(out)
		if out.DockerUpdate == nil || out.DockerUpdate.Commands[0] != c.first {
			t.Errorf("tag %s, latest %s: commands %+v", c.tag, c.latest, out.DockerUpdate)
		}
	}
}
