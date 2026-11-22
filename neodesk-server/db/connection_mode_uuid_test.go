package db

import "testing"

// TestConnectionModeTwoDevicesOneUUID: two devices with the same uuid
// (a cloned machine, a device back under a new id) can each get a
// connection mode, on both databases. Each device's revisions still go up
// (BUGS RV79).
func TestConnectionModeTwoDevicesOneUUID(t *testing.T) {
	freshStores(t, func(t *testing.T, database Database) {
		const uuid = "cnY3OS1zaGFyZWQtdXVpZA"
		for _, id := range []string{"RV79FIRST", "RV79CLONE"} {
			if err := database.UpsertPeer(&Peer{ID: id, UUID: uuid, Status: "ONLINE"}); err != nil {
				t.Fatal(err)
			}
		}
		issue := func(device, mode string) *ConnectionModeCommand {
			t.Helper()
			cmd, created, err := database.IssueConnectionMode(&ConnectionModeIssue{
				DeviceID: device, Mode: mode, Reason: "rv79", OperatorID: "1", OperatorName: "ada",
			})
			if err != nil || !created || cmd == nil {
				t.Fatalf("issue %s to %s: created=%v cmd=%+v err=%v", mode, device, created, cmd, err)
			}
			return cmd
		}
		first := issue("RV79FIRST", ConnectionModeIncomingOnly)
		clone := issue("RV79CLONE", ConnectionModeIncomingOnly)
		if clone.Revision <= first.Revision {
			t.Errorf("clone's revision %d, first device's %d", clone.Revision, first.Revision)
		}
		again := issue("RV79FIRST", ConnectionModeNormal)
		if again.Revision <= first.Revision {
			t.Errorf("first device's next revision %d after %d", again.Revision, first.Revision)
		}
		cloneAgain := issue("RV79CLONE", ConnectionModeNormal)
		if cloneAgain.Revision <= clone.Revision {
			t.Errorf("clone's next revision %d after %d", cloneAgain.Revision, clone.Revision)
		}
	})
}
