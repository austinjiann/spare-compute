package permissions

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestLoadOrCreateCapabilityToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "token")
	created, err := LoadOrCreateCapabilityToken(path)
	if err != nil {
		t.Fatalf("LoadOrCreateCapabilityToken() error = %v", err)
	}
	if len(created) != capabilityTokenBytes {
		t.Fatalf("token length = %d, want %d", len(created), capabilityTokenBytes)
	}

	loaded, err := LoadCapabilityToken(path)
	if err != nil {
		t.Fatalf("LoadCapabilityToken() error = %v", err)
	}
	if !bytes.Equal(loaded, created) {
		t.Fatal("loaded token differs from created token")
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("token permissions = %o, want 600", got)
		}
	}
}

func TestConcurrentCapabilityTokenCreationConverges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "token")
	const callers = 8
	tokens := make([][]byte, callers)
	errorsFound := make([]error, callers)
	start := make(chan struct{})
	var wait sync.WaitGroup

	for index := range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			tokens[index], errorsFound[index] = LoadOrCreateCapabilityToken(path)
		}()
	}
	close(start)
	wait.Wait()

	for index := range callers {
		if errorsFound[index] != nil {
			t.Fatalf("caller %d error = %v", index, errorsFound[index])
		}
		if !bytes.Equal(tokens[0], tokens[index]) {
			t.Fatalf("caller %d received a different token", index)
		}
	}
}

func TestLoadCapabilityTokenRejectsInvalidFiles(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadCapabilityToken(filepath.Join(directory, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing token error = %v, want os.ErrNotExist", err)
	}

	invalid := filepath.Join(directory, "invalid")
	if err := os.WriteFile(invalid, []byte("not-a-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCapabilityToken(invalid); !errors.Is(err, ErrInvalidCapabilityToken) {
		t.Fatalf("invalid token error = %v, want ErrInvalidCapabilityToken", err)
	}

	if runtime.GOOS != "windows" {
		open := filepath.Join(directory, "open")
		if err := os.WriteFile(open, []byte(base64TokenForTest()), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadCapabilityToken(open); !errors.Is(err, ErrInvalidCapabilityToken) {
			t.Fatalf("open token error = %v, want ErrInvalidCapabilityToken", err)
		}

		target := filepath.Join(directory, "target")
		if err := os.WriteFile(target, []byte(base64TokenForTest()), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(directory, "link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadCapabilityToken(link); !errors.Is(err, ErrInvalidCapabilityToken) {
			t.Fatalf("symlink token error = %v, want ErrInvalidCapabilityToken", err)
		}
	}
}

func base64TokenForTest() string {
	const encodedThirtyTwoZeroBytes = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	return encodedThirtyTwoZeroBytes + "\n"
}
