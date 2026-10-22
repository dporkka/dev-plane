package deviceauth

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
)

func TestIdentityFilePermissionsAndStableID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "device.pem")
	first, err := CreateIdentityFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("mode %o", got)
	}
	second, err := LoadIdentityFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || !first.Private.Equal(second.Private) || len(first.Public) != ed25519.PublicKeySize {
		t.Fatal("identity not stable")
	}
	if _, err := CreateIdentityFile(path); err == nil {
		t.Fatal("identity overwrite accepted")
	}
}

func TestRejectSymlinkAndInsecureKeyFile(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if _, err := CreateIdentityFile(real); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIdentityFile(link); err == nil {
		t.Fatal("accepted symlink")
	}
	if err := os.Chmod(real, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIdentityFile(real); err == nil {
		t.Fatal("accepted public readable key")
	}
}
func TestRejectMalformedPrivateKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device.pem")
	if err := os.WriteFile(path, []byte("not a key"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIdentityFile(path); err == nil {
		t.Fatal("accepted garbage")
	}
}
