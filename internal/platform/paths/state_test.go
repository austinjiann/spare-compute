package paths

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestResolveStateDir(t *testing.T) {
	noEnvironment := func(string) (string, bool) { return "", false }
	directory := func(value string) func() (string, error) {
		return func() (string, error) { return value, nil }
	}
	absoluteState := filepath.Join(t.TempDir(), "state")

	for _, test := range []struct {
		name      string
		goos      string
		lookupEnv func(string) (string, bool)
		want      string
	}{
		{
			name:      "macOS application support",
			goos:      "darwin",
			lookupEnv: noEnvironment,
			want:      filepath.Join("/config", "ComputeHop"),
		},
		{
			name:      "Windows local application data",
			goos:      "windows",
			lookupEnv: noEnvironment,
			want:      filepath.Join("/cache", "ComputeHop"),
		},
		{
			name: "Linux XDG state",
			goos: "linux",
			lookupEnv: func(name string) (string, bool) {
				if name == "XDG_STATE_HOME" {
					return absoluteState, true
				}
				return "", false
			},
			want: filepath.Join(absoluteState, "computehop"),
		},
		{
			name: "Linux home fallback",
			goos: "linux",
			lookupEnv: func(name string) (string, bool) {
				if name == "XDG_STATE_HOME" {
					return "relative/state", true
				}
				return "", false
			},
			want: filepath.Join("/home/test", ".local", "state", "computehop"),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveStateDir(
				test.goos,
				test.lookupEnv,
				directory("/config"),
				directory("/cache"),
				directory("/home/test"),
			)
			if err != nil {
				t.Fatalf("resolveStateDir() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("resolveStateDir() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestResolveStateDirPropagatesPlatformError(t *testing.T) {
	want := errors.New("directory unavailable")
	failingDirectory := func() (string, error) { return "", want }

	_, err := resolveStateDir(
		"darwin",
		func(string) (string, bool) { return "", false },
		failingDirectory,
		failingDirectory,
		failingDirectory,
	)
	if !errors.Is(err, want) {
		t.Fatalf("resolveStateDir() error = %v, want %v", err, want)
	}
}

func TestDatabasePath(t *testing.T) {
	got, err := DatabasePath("/state")
	if err != nil {
		t.Fatalf("DatabasePath() error = %v", err)
	}
	if want := filepath.Join("/state", DatabaseFilename); got != want {
		t.Fatalf("DatabasePath() = %q, want %q", got, want)
	}

	if _, err := DatabasePath(""); err == nil {
		t.Fatalf("DatabasePath(\"\") error = nil")
	}
}
