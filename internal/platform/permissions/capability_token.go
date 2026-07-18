// Package permissions owns user-local credentials and filesystem protections.
package permissions

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const capabilityTokenBytes = 32

var ErrInvalidCapabilityToken = errors.New("invalid local capability token")

// LoadOrCreateCapabilityToken returns the existing token at path or atomically
// creates a new owner-only token. Concurrent creators converge on one value.
func LoadOrCreateCapabilityToken(path string) ([]byte, error) {
	if token, err := LoadCapabilityToken(path); err == nil {
		return token, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	if err := EnsurePrivateDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}

	token := make([]byte, capabilityTokenBytes)
	if _, err := rand.Read(token); err != nil {
		return nil, fmt.Errorf("generate capability token: %w", err)
	}
	encoded := base64.RawStdEncoding.EncodeToString(token)

	file, err := os.CreateTemp(filepath.Dir(path), ".local-ipc-token-*")
	if err != nil {
		return nil, fmt.Errorf("create temporary capability token: %w", err)
	}
	temporaryPath := file.Name()
	defer func() {
		_ = file.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := file.Chmod(0o600); err != nil {
		return nil, fmt.Errorf("restrict temporary capability token: %w", err)
	}

	if _, err := file.WriteString(encoded + "\n"); err != nil {
		return nil, fmt.Errorf("write capability token: %w", err)
	}
	if err := file.Sync(); err != nil {
		return nil, fmt.Errorf("sync capability token: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close capability token: %w", err)
	}
	if err := os.Link(temporaryPath, path); errors.Is(err, os.ErrExist) {
		return LoadCapabilityToken(path)
	} else if err != nil {
		return nil, fmt.Errorf("install capability token: %w", err)
	}
	return token, nil
}

// LoadCapabilityToken validates and returns an owner-only local IPC token.
func LoadCapabilityToken(path string) ([]byte, error) {
	if err := ValidatePrivateDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: token path is not a regular file", ErrInvalidCapabilityToken)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%w: token file permissions must be owner-only", ErrInvalidCapabilityToken)
	}

	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read capability token: %w", err)
	}
	token, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil || len(token) != capabilityTokenBytes {
		return nil, fmt.Errorf("%w: expected %d random bytes", ErrInvalidCapabilityToken, capabilityTokenBytes)
	}
	return token, nil
}
