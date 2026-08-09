// Package identity persists long-lived local device identities.
package identity

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/austinjiann/spare-compute/internal/device"
)

const privateKeyPEMType = "COMPUTEHOP DEVICE PRIVATE KEY"

var ErrUnsafeIdentityFile = errors.New("unsafe device identity file")

// Store owns an owner-only PKCS#8 identity file.
type Store struct {
	path   string
	random io.Reader
}

// NewStore constructs a filesystem identity store.
func NewStore(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("device identity path is required")
	}
	return &Store{path: path, random: rand.Reader}, nil
}

// LoadOrCreate returns the existing identity or atomically installs a new one.
func (store *Store) LoadOrCreate() (device.Identity, error) {
	identity, err := store.load()
	if err == nil {
		return identity, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return device.Identity{}, err
	}

	generated, err := device.GenerateIdentity(store.random)
	if err != nil {
		return device.Identity{}, err
	}
	encoded, err := encode(generated)
	if err != nil {
		return device.Identity{}, err
	}
	directory := filepath.Dir(store.path)
	temporary, err := os.CreateTemp(directory, ".device-identity-*")
	if err != nil {
		return device.Identity{}, fmt.Errorf("create temporary identity: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	closeWithError := func(cause error) error {
		return errors.Join(cause, temporary.Close())
	}
	if err := temporary.Chmod(0o600); err != nil {
		return device.Identity{}, closeWithError(fmt.Errorf("restrict temporary identity: %w", err))
	}
	if err := writeAll(temporary, encoded); err != nil {
		return device.Identity{}, closeWithError(fmt.Errorf("write temporary identity: %w", err))
	}
	if err := temporary.Sync(); err != nil {
		return device.Identity{}, closeWithError(fmt.Errorf("sync temporary identity: %w", err))
	}
	if err := temporary.Close(); err != nil {
		return device.Identity{}, fmt.Errorf("close temporary identity: %w", err)
	}

	if err := os.Link(temporaryPath, store.path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return store.load()
		}
		return device.Identity{}, fmt.Errorf("install device identity: %w", err)
	}
	if runtime.GOOS != "windows" {
		directoryHandle, err := os.Open(directory)
		if err != nil {
			return device.Identity{}, fmt.Errorf("open identity directory: %w", err)
		}
		syncErr := directoryHandle.Sync()
		closeErr := directoryHandle.Close()
		if err := errors.Join(syncErr, closeErr); err != nil {
			return device.Identity{}, fmt.Errorf("sync identity directory: %w", err)
		}
	}
	return generated, nil
}

func (store *Store) load() (device.Identity, error) {
	info, err := os.Lstat(store.path)
	if err != nil {
		return device.Identity{}, err
	}
	unsafePermissions := runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || unsafePermissions {
		return device.Identity{}, ErrUnsafeIdentityFile
	}
	contents, err := os.ReadFile(store.path)
	if err != nil {
		return device.Identity{}, fmt.Errorf("read device identity: %w", err)
	}
	block, trailing := pem.Decode(contents)
	if block == nil || block.Type != privateKeyPEMType || len(bytes.TrimSpace(trailing)) != 0 {
		return device.Identity{}, fmt.Errorf("%w: invalid PEM", device.ErrInvalidIdentity)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return device.Identity{}, fmt.Errorf("parse device identity: %w", err)
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return device.Identity{}, fmt.Errorf("%w: key is not Ed25519", device.ErrInvalidIdentity)
	}
	return device.NewIdentity(privateKey)
}

func encode(identity device.Identity) ([]byte, error) {
	if err := identity.Validate(); err != nil {
		return nil, err
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(identity.PrivateKey())
	if err != nil {
		return nil, fmt.Errorf("encode device identity: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: privateKeyPEMType, Bytes: encoded}), nil
}

func writeAll(writer io.Writer, contents []byte) error {
	for len(contents) > 0 {
		written, err := writer.Write(contents)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		contents = contents[written:]
	}
	return nil
}
