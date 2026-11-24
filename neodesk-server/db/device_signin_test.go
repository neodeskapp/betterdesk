package db

import (
	"testing"
)

// TestDeviceSigninKeepsRowsPerDevice: a saved sign-in is stored and read
// back as it was written, replaced by a second save, and gone after a
// delete, a permanent delete of its device and a change of its device's ID
// (it is sealed for the ID, so it would not open under the new one), on
// SQLite and PostgreSQL.
func TestDeviceSigninKeepsRowsPerDevice(t *testing.T) {
	freshStores(t, func(t *testing.T, database Database) {
		for _, id := range []string{"SIGNDEV1", "SIGNDEV2", "SIGNDEV3"} {
			if err := database.UpsertPeer(&Peer{ID: id, UUID: "u-" + id}); err != nil {
				t.Fatal(err)
			}
		}
		row := func(id, cipher string) *DeviceSignin {
			return &DeviceSignin{DeviceID: id, UserNonce: "un", UserCipher: "uc", PassNonce: "pn", PassCipher: cipher,
				KeyID: "s1", Form: "password", UpdatedAt: "2026-11-24T10:00:00Z", UpdatedBy: "admin"}
		}
		if got, err := database.GetDeviceSignin("SIGNDEV1"); err != nil || got != nil {
			t.Fatalf("before a save: %+v %v", got, err)
		}
		if err := database.SaveDeviceSignin(row("SIGNDEV1", "first")); err != nil {
			t.Fatal(err)
		}
		replaced := row("SIGNDEV1", "second")
		replaced.Auto, replaced.Form, replaced.UpdatedBy = true, "name_password", "other"
		if err := database.SaveDeviceSignin(replaced); err != nil {
			t.Fatal(err)
		}
		got, err := database.GetDeviceSignin("SIGNDEV1")
		if err != nil || got == nil || *got != *replaced {
			t.Fatalf("after a replace: %+v %v, want %+v", got, err, replaced)
		}
		if gone, err := database.DeleteDeviceSignin("SIGNDEV1"); err != nil || !gone {
			t.Fatalf("delete: %v %v", gone, err)
		}
		if gone, err := database.DeleteDeviceSignin("SIGNDEV1"); err != nil || gone {
			t.Fatalf("second delete: %v %v", gone, err)
		}

		if err := database.SaveDeviceSignin(row("SIGNDEV2", "x")); err != nil {
			t.Fatal(err)
		}
		if err := database.HardDeletePeer("SIGNDEV2"); err != nil {
			t.Fatal(err)
		}
		if got, _ := database.GetDeviceSignin("SIGNDEV2"); got != nil {
			t.Error("a permanently deleted device keeps its saved sign-in")
		}

		if err := database.SaveDeviceSignin(row("SIGNDEV3", "x")); err != nil {
			t.Fatal(err)
		}
		if err := database.ChangePeerID("SIGNDEV3", "SIGNDEV9", "test"); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"SIGNDEV3", "SIGNDEV9"} {
			if got, _ := database.GetDeviceSignin(id); got != nil {
				t.Errorf("after an ID change %s has a saved sign-in", id)
			}
		}
	})
}

// TestDeviceSealedSigninStaysWithTheDevice: a device marked as one whose
// NeoDesk opens sealed sign-ins stays marked (a second mark changes
// nothing and says so), also when its saved sign-in is cleared; the mark
// follows the device to a new ID and goes with a permanent delete, on
// SQLite and PostgreSQL.
func TestDeviceSealedSigninStaysWithTheDevice(t *testing.T) {
	freshStores(t, func(t *testing.T, database Database) {
		for _, id := range []string{"SEALDEV1", "SEALDEV2", "SEALDEV3", "SEALDEV4"} {
			if err := database.UpsertPeer(&Peer{ID: id, UUID: "u-" + id}); err != nil {
				t.Fatal(err)
			}
		}
		sealed := func(id string) bool {
			t.Helper()
			ok, err := database.DeviceSealedSignin(id)
			if err != nil {
				t.Fatal(err)
			}
			return ok
		}
		if sealed("SEALDEV1") {
			t.Fatal("marked before any mark")
		}
		if added, err := database.MarkDeviceSealedSignin("SEALDEV1", "sysinfo", "2026-11-24T10:00:00Z"); err != nil || !added {
			t.Fatalf("first mark: %v %v", added, err)
		}
		if added, err := database.MarkDeviceSealedSignin("SEALDEV1", "use", "2026-11-24T11:00:00Z"); err != nil || added {
			t.Fatalf("second mark: %v %v, want no change", added, err)
		}
		if err := database.SaveDeviceSignin(&DeviceSignin{DeviceID: "SEALDEV1", UserNonce: "un", UserCipher: "uc",
			PassNonce: "pn", PassCipher: "pc", KeyID: "s1", Form: "password"}); err != nil {
			t.Fatal(err)
		}
		if _, err := database.DeleteDeviceSignin("SEALDEV1"); err != nil {
			t.Fatal(err)
		}
		if !sealed("SEALDEV1") || sealed("SEALDEV2") {
			t.Errorf("after clearing the sign-in: SEALDEV1 %v, SEALDEV2 %v", sealed("SEALDEV1"), sealed("SEALDEV2"))
		}

		if _, err := database.MarkDeviceSealedSignin("SEALDEV2", "sysinfo", "2026-11-24T10:00:00Z"); err != nil {
			t.Fatal(err)
		}
		if err := database.HardDeletePeer("SEALDEV2"); err != nil {
			t.Fatal(err)
		}
		if sealed("SEALDEV2") {
			t.Error("a permanently deleted device keeps its mark")
		}

		// The new ID takes the mark, also over one it had
		if _, err := database.MarkDeviceSealedSignin("SEALDEV3", "sysinfo", "2026-11-24T10:00:00Z"); err != nil {
			t.Fatal(err)
		}
		if _, err := database.MarkDeviceSealedSignin("SEALDEV7", "sysinfo", "2026-11-24T09:00:00Z"); err != nil {
			t.Fatal(err)
		}
		if err := database.ChangePeerID("SEALDEV3", "SEALDEV7", "test"); err != nil {
			t.Fatal(err)
		}
		if sealed("SEALDEV3") || !sealed("SEALDEV7") {
			t.Errorf("after an ID change: old %v, new %v", sealed("SEALDEV3"), sealed("SEALDEV7"))
		}
		if err := database.ChangePeerID("SEALDEV4", "SEALDEV8", "test"); err != nil {
			t.Fatal(err)
		}
		if sealed("SEALDEV8") {
			t.Error("an ID change marked an unmarked device")
		}
	})
}
