package api

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/relay"
	"golang.org/x/crypto/nacl/box"
)

// sealedDevice is a made-up device's Ed25519 key pair and its X25519 key
// pair as libsodium converts it.
func sealedDevice(seed byte) (ed25519.PublicKey, [32]byte, [32]byte) {
	s := bytes.Repeat([]byte{seed}, 32)
	pub := ed25519.NewKeyFromSeed(s).Public().(ed25519.PublicKey)
	h := sha512.Sum512(s)
	var sec, xpub [32]byte
	copy(sec[:], h[:32])
	sec[0] &= 248
	sec[31] &= 127
	sec[31] |= 64
	k, _ := ecdh.X25519().NewPrivateKey(sec[:])
	copy(xpub[:], k.PublicKey().Bytes())
	return pub, xpub, sec
}

// TestSavedSigninIsSealedForTheDevice: for a viewer whose device opens
// sealed sign-ins, the use answers a packet sealed to the device's key,
// never the password: it opens with the device's key pair to the details,
// the device, the session, a not_after a minute ahead and a nonce. From
// then on the server refuses the readable answer for that device (a
// browser can't ask for it instead); a device without a key gets none;
// the one use per session counts for both ways (L469).
func TestSavedSigninIsSealedForTheDevice(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	if err := srv.InitDeviceVault(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	srv.now = func() time.Time { return now }
	edPub, xPub, xSec := sealedDevice(9)
	if err := database.UpsertPeer(&db.Peer{ID: "PEERAAA1", PK: edPub}); err != nil {
		t.Fatal(err)
	}
	srv.signinLiveFn = func(uuid string) (relay.LivePair, bool) {
		target := map[string]string{"s-1": "PEERAAA1", "s-2": "PEERAAA1", "s-3": "PEERAAA1", "s-b": "PEERBBB2"}[uuid]
		return relay.LivePair{Initiator: webRemoteInitiator, Target: target, Account: 5, ConsoleSession: "console-1"}, target != ""
	}
	for _, id := range []string{"PEERAAA1", "PEERBBB2"} {
		if status, body := asConsole(t, srv, http.MethodPut, "/api/peers/"+id+"/signin",
			`{"username":"LAB\\admin","password":"made-up-sealed-pass","form":"name_password"}`); status != http.StatusOK {
			t.Fatalf("set %s: %d %s", id, status, body)
		}
	}
	use := func(id, session, mode string) (int, string) {
		return asConsole(t, srv, http.MethodPost, "/api/peers/"+id+"/signin/use",
			`{"session":"`+session+`","account":5,"console_session":"console-1","mode":"`+mode+`"}`)
	}
	status, body := use("PEERAAA1", "s-1", "sealed")
	if status != http.StatusOK || strings.Contains(body, "made-up-sealed-pass") || strings.Contains(body, `"password"`) {
		t.Fatalf("sealed use: %d %s", status, body)
	}
	var got struct {
		Path, Sealed, Nonce string
		NotAfter            int64 `json:"not_after"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil || got.Path != "sealed" {
		t.Fatalf("sealed answer %s %v", body, err)
	}
	packet, err := base64.StdEncoding.DecodeString(got.Sealed)
	if err != nil {
		t.Fatal(err)
	}
	plain, ok := box.OpenAnonymous(nil, packet, &xPub, &xSec)
	if !ok {
		t.Fatal("the device does not open the packet")
	}
	var p struct {
		V                                        int
		DeviceID                                 string `json:"device_id"`
		Session, Username, Password, Form, Nonce string
		NotAfter                                 int64 `json:"not_after"`
	}
	if err := json.Unmarshal(plain, &p); err != nil {
		t.Fatal(err)
	}
	if p.V != 1 || p.DeviceID != "PEERAAA1" || p.Session != "s-1" || p.Username != `LAB\admin` || p.Password != "made-up-sealed-pass" ||
		p.Form != "name_password" || p.NotAfter != now.Unix()+60 || p.NotAfter != got.NotAfter || len(p.Nonce) != 22 || p.Nonce != got.Nonce {
		t.Errorf("packet %+v, answer %+v", p, got)
	}
	if status, body := use("PEERAAA1", "s-1", "sealed"); status != http.StatusConflict || !strings.Contains(body, "already_used") {
		t.Errorf("second use in the session: %d %s", status, body)
	}
	if status, body := use("PEERAAA1", "s-2", "typed"); status != http.StatusConflict || !strings.Contains(body, "sealed_required") || strings.Contains(body, "made-up") {
		t.Errorf("readable answer for a device that signs in sealed: %d %s", status, body)
	}
	// The refusal did not spend the session's use
	if status, _ := use("PEERAAA1", "s-2", "sealed"); status != http.StatusOK {
		t.Errorf("sealed use after the refusal: %d", status)
	}
	if status, body := asConsole(t, srv, http.MethodGet, "/api/peers/PEERAAA1/signin", ""); !strings.Contains(body, `"sealed_signin":true`) {
		t.Errorf("status: %d %s", status, body)
	}
	// A device the server has no key of gets no packet
	if status, body := use("PEERBBB2", "s-b", "sealed"); status != http.StatusConflict || !strings.Contains(body, "no_device_key") {
		t.Errorf("device without a key: %d %s", status, body)
	}
	// Clearing and saving the details again does not take the device off
	// the sealed way
	asConsole(t, srv, http.MethodDelete, "/api/peers/PEERAAA1/signin", "")
	asConsole(t, srv, http.MethodPut, "/api/peers/PEERAAA1/signin", `{"username":"admin","password":"made-up-sealed-pass"}`)
	if status, body := use("PEERAAA1", "s-3", "typed"); status != http.StatusConflict || !strings.Contains(body, "sealed_required") {
		t.Errorf("readable answer after clear and save: %d %s", status, body)
	}
}

// TestSysinfoReportPutsTheDeviceOnTheSealedWay: a device whose NeoDesk
// names "nd_signin": 1 in the platform_additions of its system information
// gets no readable saved sign-in from the first use on, and its status
// says so before any details are saved; a later upload without it does
// not undo that. An upload refused as the device's (another uuid, a
// banned device) and any other value change nothing (L469).
func TestSysinfoReportPutsTheDeviceOnTheSealedWay(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	if err := srv.InitDeviceVault(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	edPub, _, _ := sealedDevice(11)
	machine := []byte("made-up-machine-uuid")
	stored, right := fmt.Sprintf("%x", machine), base64.StdEncoding.EncodeToString(machine)
	wrong := base64.StdEncoding.EncodeToString([]byte("another-machine"))
	for _, id := range []string{"PEERAAA1", "PEERBBB2", "PEERCCC3", "PEERBAN9"} {
		if err := database.UpsertPeer(&db.Peer{ID: id, UUID: stored, PK: edPub}); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.BanPeer("PEERBAN9", "test"); err != nil {
		t.Fatal(err)
	}
	srv.signinLiveFn = func(uuid string) (relay.LivePair, bool) {
		target, _, _ := strings.Cut(uuid, "/")
		return relay.LivePair{Initiator: webRemoteInitiator, Target: target, Account: 5, ConsoleSession: "console-1"}, true
	}
	sysinfo := func(id, uuid, additions string) string {
		body := `{"id":"` + id + `","uuid":"` + uuid + `","hostname":"lab-pc","os":"Windows 11"`
		if additions != "" {
			body += `,"platform_additions":` + additions
		}
		_, answer := listenerCall(t, srv, http.MethodPost, "/api/sysinfo", body+"}", nil)
		return answer
	}
	status := func(id string) string {
		_, body := asConsole(t, srv, http.MethodGet, "/api/peers/"+id+"/signin", "")
		return body
	}
	use := func(id, session, mode string) (int, string) {
		return asConsole(t, srv, http.MethodPost, "/api/peers/"+id+"/signin/use",
			`{"session":"`+id+"/"+session+`","account":5,"console_session":"console-1","mode":"`+mode+`"}`)
	}

	if got := sysinfo("PEERAAA1", right, `{"nd_signin":1,"is_installed":true}`); got != "SYSINFO_UPDATED" {
		t.Fatalf("sysinfo: %s", got)
	}
	if body := status("PEERAAA1"); !strings.Contains(body, `"set":false`) || !strings.Contains(body, `"sealed_signin":true`) {
		t.Errorf("status before details are saved: %s", body)
	}
	for _, id := range []string{"PEERAAA1", "PEERBBB2", "PEERCCC3"} {
		if code, body := asConsole(t, srv, http.MethodPut, "/api/peers/"+id+"/signin", `{"username":"admin","password":"made-up-report-pass"}`); code != http.StatusOK {
			t.Fatalf("set %s: %d %s", id, code, body)
		}
	}
	if code, body := use("PEERAAA1", "s-1", "typed"); code != http.StatusConflict || !strings.Contains(body, "sealed_required") || strings.Contains(body, "made-up") {
		t.Errorf("first readable use of a device that reported sealed sign-in: %d %s", code, body)
	}
	if code, body := use("PEERAAA1", "s-1", "sealed"); code != http.StatusOK || !strings.Contains(body, `"path":"sealed"`) {
		t.Errorf("sealed use after the refusal: %d %s", code, body)
	}
	// An upload without the report does not take it back
	if got := sysinfo("PEERAAA1", right, ""); got != "SYSINFO_UPDATED" {
		t.Fatalf("sysinfo without the report: %s", got)
	}
	sysinfo("PEERAAA1", right, `{"nd_signin":0}`)
	if code, body := use("PEERAAA1", "s-2", "typed"); code != http.StatusConflict || !strings.Contains(body, "sealed_required") {
		t.Errorf("readable use after an upload without the report: %d %s", code, body)
	}

	// Other values, another machine's upload and a banned device's report nothing
	for _, additions := range []string{`{"nd_signin":"1"}`, `{"nd_signin":true}`, `{"nd_signin":0}`, `{"nd_signin":null}`, `"{\"nd_signin\":1}"`, `[1]`, `{}`} {
		sysinfo("PEERBBB2", right, additions)
	}
	sysinfo("PEERCCC3", wrong, `{"nd_signin":1}`)
	sysinfo("PEERCCC3", "", `{"nd_signin":1}`)
	sysinfo("PEERBAN9", right, `{"nd_signin":1}`)
	for _, id := range []string{"PEERBBB2", "PEERCCC3"} {
		if body := status(id); !strings.Contains(body, `"sealed_signin":false`) {
			t.Errorf("%s: status %s", id, body)
		}
		if code, body := use(id, "s-1", "typed"); code != http.StatusOK || !strings.Contains(body, `"path":"typed"`) {
			t.Errorf("%s: readable use %d %s", id, code, body)
		}
	}
	if sealed, err := database.DeviceSealedSignin("PEERBAN9"); err != nil || sealed {
		t.Errorf("a banned device's report was taken: %v %v", sealed, err)
	}
}
