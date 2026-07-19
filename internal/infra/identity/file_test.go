package identity

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/austinjiann/spare-compute/internal/device"
)

func TestStorePersistsOneIdentityAcrossConcurrentLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device-identity.pem")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	identities := make(chan device.Identity, 8)
	errorsByLoad := make(chan error, 8)
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			identity, err := store.LoadOrCreate()
			identities <- identity
			errorsByLoad <- err
		}()
	}
	wait.Wait()
	close(identities)
	close(errorsByLoad)
	for err := range errorsByLoad {
		if err != nil {
			t.Fatal(err)
		}
	}
	var want device.ID
	for identity := range identities {
		if want == "" {
			want = identity.ID()
		}
		if identity.ID() != want {
			t.Fatalf("identity changed: %s != %s", identity.ID(), want)
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("identity permissions = %o", info.Mode().Perm())
	}
}

func TestStoreRejectsUnsafeIdentityFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows permissions are represented by ACLs")
	}
	path := filepath.Join(t.TempDir(), "device-identity.pem")
	if err := os.WriteFile(path, []byte("not a key"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, _ := NewStore(path)
	if _, err := store.LoadOrCreate(); !errors.Is(err, ErrUnsafeIdentityFile) {
		t.Fatalf("LoadOrCreate() error = %v", err)
	}
}
