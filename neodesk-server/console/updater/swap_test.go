package updater

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The copy into the binary's folder is checked again: a file that changed
// on its way is not left there.
func TestCopyVerified(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyVerified(src, dst, sum([]byte("other"))); err == nil || !strings.Contains(err.Error(), "expected SHA-256") {
		t.Fatalf("a wrong sum passed: %v", err)
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("the bad copy stays")
	}
	if err := copyVerified(src, dst, sum([]byte("binary"))); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(dst); info.Mode().Perm()&0o111 == 0 {
		t.Errorf("mode %o is not executable", info.Mode().Perm())
	}
	if err := copyVerified(src, dst, ""); err == nil {
		t.Error("an existing file was overwritten")
	}
}
