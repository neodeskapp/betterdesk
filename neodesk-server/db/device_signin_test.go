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
		replaced.Auto, replaced.Form, replaced.UpdatedBy, replaced.SealedOnly = true, "name_password", "other", true
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
