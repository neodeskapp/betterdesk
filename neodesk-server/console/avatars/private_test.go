package avatars

import (
	"context"
	"net/netip"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// RV10: the avatar address of a profile, which every account sets, never
// reaches the private network: private, carrier-grade NAT and unique
// local addresses are refused, written as literals, behind a name and
// after a redirect. Every failed fetch answers one code, so the answer
// tells nothing about the hosts behind it. Single sign-on keeps fetching
// from the LAN.
func TestProfilePictureStaysOffThePrivateNetwork(t *testing.T) {
	db := storetest.Migrated(t)
	pics := newPictureServer(t)
	for host, addr := range map[string]string{
		"lan.example.test": "10.1.2.3", "home.example.test": "192.168.1.20", "office.example.test": "172.16.4.5",
		"cgnat.example.test": "100.64.1.1", "ula.example.test": "fd00::5", "mapped.example.test": "::ffff:10.0.0.1",
		"public.example.test": "93.184.216.34", "down.example.test": "93.184.216.35",
	} {
		pics.addresses[host] = []netip.Addr{netip.MustParseAddr(addr)}
	}
	pics.down["93.184.216.35"] = true
	svc, _ := newTestService(t, db, pics.options())
	user := addUser(t, db, "alice")
	ctx := context.Background()
	failed := Result{Action: "failed", Code: "fetch_failed"}

	for _, address := range []string{
		pics.url("10.1.2.3", "/a.png"), pics.url("192.168.1.20", "/a.png"), pics.url("172.31.255.1", "/a.png"),
		pics.url("100.127.0.1", "/a.png"), pics.url("[fd12::1]", "/a.png"), pics.url("[fec0::1]", "/a.png"),
		pics.url("[::ffff:192.168.0.1]", "/a.png"), pics.url("[2002:c0a8:1::]", "/a.png"),
		pics.url("lan.example.test", "/a.png"), pics.url("home.example.test", "/a.png"), pics.url("office.example.test", "/a.png"),
		pics.url("cgnat.example.test", "/a.png"), pics.url("ula.example.test", "/a.png"), pics.url("mapped.example.test", "/a.png"),
	} {
		if got := svc.SyncProfileAvatarURL(ctx, user, address); got != failed {
			t.Errorf("%s: %+v, want %+v", address, got, failed)
		}
	}
	if hits := pics.seen(); len(hits) != 0 {
		t.Fatalf("the picture server was reached for a private address: %q", hits)
	}

	// A page, a picture too large, an error status, a closed port and a
	// refused address all answer the same
	for _, address := range []string{
		pics.url("public.example.test", "/html"), pics.url("public.example.test", "/big.png"),
		pics.url("public.example.test", "/missing"), pics.url("down.example.test", "/a.png"),
		pics.url("127.0.0.1", "/a.png"), pics.url("nowhere.example.test", "/a.png"),
	} {
		if got := svc.SyncProfileAvatarURL(ctx, user, address); got != failed {
			t.Errorf("%s: %+v, want %+v", address, got, failed)
		}
	}
	if got := svc.SyncProfileAvatarURL(ctx, user, "ftp://public.example.test/a.png"); got != (Result{Action: "failed", Code: "invalid_url"}) {
		t.Errorf("an ftp address: %+v", got)
	}
	if got := svc.SyncProfileAvatarURL(ctx, user, pics.url("public.example.test", "/a.png")); got != (Result{Action: "stored"}) {
		t.Fatalf("a public picture: %+v", got)
	}

	other := addUser(t, db, "bob")
	if got := svc.SyncOIDCPicture(ctx, other, claims{"picture": pics.url("lan.example.test", "/a.png")}); got != (Result{Action: "stored"}) {
		t.Errorf("single sign-on from the LAN: %+v", got)
	}
}
