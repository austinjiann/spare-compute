package permissions

import (
	"errors"
	"fmt"
	"os"
	"runtime"
)

var ErrUnsafeDirectory = errors.New("unsafe local state directory")

// EnsurePrivateDirectory creates path when absent and otherwise validates it.
// It never changes permissions on an existing caller-supplied directory.
func EnsurePrivateDirectory(path string) error {
	if err := ValidatePrivateDirectory(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create private state directory: %w", err)
	}
	return ValidatePrivateDirectory(path)
}

// ValidatePrivateDirectory rejects symlinks, non-directories, and Unix paths
// accessible by group or other users.
func ValidatePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: path is not a directory", ErrUnsafeDirectory)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: permissions must be owner-only", ErrUnsafeDirectory)
	}
	return nil
}
