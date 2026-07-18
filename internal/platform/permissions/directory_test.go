package permissions

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsurePrivateDirectoryCreatesOwnerOnlyDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	if err := EnsurePrivateDirectory(path); err != nil {
		t.Fatalf("EnsurePrivateDirectory() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("directory permissions = %o, want 700", info.Mode().Perm())
	}
}

func TestEnsurePrivateDirectoryDoesNotRewriteUnsafePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows permissions are represented by ACLs")
	}
	path := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDirectory(path); !errors.Is(err, ErrUnsafeDirectory) {
		t.Fatalf("EnsurePrivateDirectory() error = %v, want ErrUnsafeDirectory", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("unsafe permissions were rewritten to %o", got)
	}
}

func TestValidatePrivateDirectoryRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks may require additional Windows privileges")
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePrivateDirectory(link); !errors.Is(err, ErrUnsafeDirectory) {
		t.Fatalf("ValidatePrivateDirectory() error = %v, want ErrUnsafeDirectory", err)
	}
}
