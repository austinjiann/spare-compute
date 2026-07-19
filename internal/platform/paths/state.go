// Package paths resolves operating-system-appropriate ComputeHop paths.
package paths

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/austinjiann/spare-compute/internal/job"
)

const (
	DatabaseFilename        = "computehop.db"
	LocalSocketFilename     = "computehop.sock"
	CapabilityTokenFilename = "local-ipc.token"
	JobsDirectoryName       = "jobs"
	JobLogFilename          = "output.log"
)

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
	return stateFilePath(stateDir, DatabaseFilename)
}

// LocalSocketPath resolves the user-owned daemon socket inside stateDir.
func LocalSocketPath(stateDir string) (string, error) {
	return stateFilePath(stateDir, LocalSocketFilename)
}

// CapabilityTokenPath resolves the local IPC capability token inside stateDir.
func CapabilityTokenPath(stateDir string) (string, error) {
	return stateFilePath(stateDir, CapabilityTokenFilename)
}

// JobDataDir resolves the private durable-data directory for id.
func JobDataDir(stateDir string, id job.ID) (string, error) {
	if !id.Valid() {
		return "", job.ErrInvalidID
	}
	jobsDirectory, err := stateFilePath(stateDir, JobsDirectoryName)
	if err != nil {
		return "", err
	}
	return filepath.Join(jobsDirectory, string(id)), nil
}

// JobLogPath resolves the append-only job log data file for id.
func JobLogPath(stateDir string, id job.ID) (string, error) {
	directory, err := JobDataDir(stateDir, id)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, JobLogFilename), nil
}

func stateFilePath(stateDir, filename string) (string, error) {
	if strings.TrimSpace(stateDir) == "" {
		return "", errors.New("state directory is required")
	}
	return filepath.Join(stateDir, filename), nil
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
