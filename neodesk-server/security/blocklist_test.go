package security

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBlocklistIP(t *testing.T) {
	bl := NewBlocklist()

	bl.BlockIP("192.168.1.100", "spam")

	if !bl.IsIPBlocked("192.168.1.100") {
		t.Fatal("IP should be blocked")
	}
	if bl.IsIPBlocked("192.168.1.101") {
		t.Fatal("different IP should not be blocked")
	}
}

func TestBlocklistIPWithPort(t *testing.T) {
	bl := NewBlocklist()
	bl.BlockIP("10.0.0.1", "test")

	// Should match when port is present
	if !bl.IsIPBlocked("10.0.0.1:12345") {
		t.Fatal("IP with port should be blocked")
	}
}

func TestBlocklistCIDR(t *testing.T) {
	bl := NewBlocklist()
	if err := bl.BlockCIDR("10.0.0.0/24", "internal net"); err != nil {
		t.Fatal(err)
	}

	if !bl.IsIPBlocked("10.0.0.1") {
		t.Fatal("10.0.0.1 should be blocked by CIDR")
	}
	if !bl.IsIPBlocked("10.0.0.255") {
		t.Fatal("10.0.0.255 should be blocked by CIDR")
	}
	if bl.IsIPBlocked("10.0.1.1") {
		t.Fatal("10.0.1.1 should NOT be blocked by 10.0.0.0/24")
	}
}

func TestBlocklistID(t *testing.T) {
	bl := NewBlocklist()
	bl.BlockID("ABC123", "malicious device")

	if !bl.IsIDBlocked("ABC123") {
		t.Fatal("ID should be blocked")
	}
	if bl.IsIDBlocked("XYZ789") {
		t.Fatal("different ID should not be blocked")
	}
}

func TestBlocklistUnblock(t *testing.T) {
	bl := NewBlocklist()

	bl.BlockIP("1.2.3.4", "test")
	bl.BlockID("DEV1", "test")
	bl.BlockCIDR("172.16.0.0/16", "test")

	if !bl.UnblockIP("1.2.3.4") {
		t.Fatal("should return true for existing IP")
	}
	if bl.IsIPBlocked("1.2.3.4") {
		t.Fatal("IP should be unblocked")
	}

	if !bl.UnblockID("DEV1") {
		t.Fatal("should return true for existing ID")
	}
	if bl.IsIDBlocked("DEV1") {
		t.Fatal("ID should be unblocked")
	}

	if !bl.UnblockCIDR("172.16.0.0/16") {
		t.Fatal("should return true for existing CIDR")
	}
	if bl.IsIPBlocked("172.16.0.1") {
		t.Fatal("CIDR should be unblocked")
	}
}

func TestBlocklistLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blocklist.txt")

	content := `# Test blocklist
192.168.1.1,brute force
10.0.0.0/8,internal
id:BADDEV,malware

# Comment line
172.16.0.1
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	bl := NewBlocklist()
	if err := bl.LoadFromFile(path); err != nil {
		t.Fatal(err)
	}

	if bl.Count() != 4 {
		t.Fatalf("expected 4 entries, got %d", bl.Count())
	}

	if !bl.IsIPBlocked("192.168.1.1") {
		t.Error("192.168.1.1 should be blocked")
	}
	if !bl.IsIPBlocked("10.5.5.5") {
		t.Error("10.5.5.5 should be blocked by 10.0.0.0/8")
	}
	if !bl.IsIDBlocked("BADDEV") {
		t.Error("BADDEV should be blocked")
	}
	if !bl.IsIPBlocked("172.16.0.1") {
		t.Error("172.16.0.1 should be blocked")
	}
}

func TestBlocklistLoadNonexistent(t *testing.T) {
	bl := NewBlocklist()
	// Should not error on missing file
	if err := bl.LoadFromFile("/tmp/nonexistent_blocklist_123456.txt"); err != nil {
		t.Fatalf("should not error on missing file: %v", err)
	}
}

func TestBlocklistList(t *testing.T) {
	bl := NewBlocklist()
	bl.BlockIP("1.1.1.1", "dns")
	bl.BlockID("DEV2", "test")
	bl.BlockCIDR("192.168.0.0/16", "private")

	entries := bl.List()
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
}

func TestBlocklistSaveToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "saved.txt")

	bl := NewBlocklist()
	bl.BlockIP("8.8.8.8", "test dns")
	bl.BlockID("MYDEV", "test device")
	bl.BlockCIDR("10.0.0.0/8", "internal")

	if err := bl.SaveToFile(path); err != nil {
		t.Fatal(err)
	}

	// Reload and verify
	bl2 := NewBlocklist()
	if err := bl2.LoadFromFile(path); err != nil {
		t.Fatal(err)
	}

	if bl2.Count() != 3 {
		t.Fatalf("reloaded count: got %d, want 3", bl2.Count())
	}
	if !bl2.IsIPBlocked("8.8.8.8") {
		t.Error("8.8.8.8 should be blocked after reload")
	}
	if !bl2.IsIDBlocked("MYDEV") {
		t.Error("MYDEV should be blocked after reload")
	}
	if !bl2.IsIPBlocked("10.1.2.3") {
		t.Error("10.1.2.3 should be blocked by CIDR after reload")
	}
}

func TestBlocklistInvalidCIDR(t *testing.T) {
	bl := NewBlocklist()
	err := bl.BlockCIDR("not-a-cidr", "test")
	if err == nil {
		t.Fatal("should error on invalid CIDR")
	}
}

// A blocked address matches the client's address however either is
// written: letter case, zeros written out, IPv4 written as IPv6 (RV125).
func TestBlocklistIPSpellings(t *testing.T) {
	bl := NewBlocklist()
	bl.BlockIP("2001:DB8::1", "upper case")
	bl.BlockIP("2001:db8:0:0:0:0:0:2", "written out")
	bl.BlockIP("::ffff:198.51.100.7", "mapped")
	for _, client := range []string{"2001:db8::1", "[2001:db8::1]:443", "2001:db8::2", "198.51.100.7", "198.51.100.7:5000"} {
		if !bl.IsIPBlocked(client) {
			t.Errorf("%s is not blocked", client)
		}
	}
	if bl.IsIPBlocked("2001:db8::3") {
		t.Error("2001:db8::3 is blocked")
	}
	if !bl.UnblockIP("2001:db8::1") || bl.IsIPBlocked("2001:db8::1") {
		t.Error("unblocking in another spelling did not unblock")
	}

	path := filepath.Join(t.TempDir(), "blocklist.txt")
	if err := os.WriteFile(path, []byte("2001:0DB8::0005,from the file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fromFile := NewBlocklist()
	if err := fromFile.LoadFromFile(path); err != nil {
		t.Fatal(err)
	}
	if !fromFile.IsIPBlocked("2001:db8::5") {
		t.Error("an address from the file in another spelling is not blocked")
	}
}
