// Package paths resolves operating-system-appropriate ComputeHop paths.
package paths

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const DatabaseFilename = "computehop.db"

// StateDir returns the default directory for durable local ComputeHop state.
func StateDir() (string, error) {
	return resolveStateDir(
		runtime.GOOS,
		os.LookupEnv,
		os.UserConfigDir,
		os.UserCacheDir,
		os.UserHomeDir,
	)
}

// DatabasePath resolves the daemon database inside stateDir.
func DatabasePath(stateDir string) (string, error) {
	if strings.TrimSpace(stateDir) == "" {
		return "", errors.New("state directory is required")
	}
	return filepath.Join(stateDir, DatabaseFilename), nil
}

func resolveStateDir(
	goos string,
	lookupEnv func(string) (string, bool),
	userConfigDir func() (string, error),
	userCacheDir func() (string, error),
	userHomeDir func() (string, error),
) (string, error) {
	switch goos {
	case "linux":
		if stateHome, exists := lookupEnv("XDG_STATE_HOME"); exists && stateHome != "" && filepath.IsAbs(stateHome) {
			return filepath.Join(stateHome, "computehop"), nil
		}
		home, err := userHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "state", "computehop"), nil
	case "windows":
		base, err := userCacheDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(base, "ComputeHop"), nil
	default:
		base, err := userConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(base, "ComputeHop"), nil
	}
}
