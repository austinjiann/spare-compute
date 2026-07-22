package paths

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/austinjiann/spare-compute/internal/job"
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
	for _, test := range []struct {
		name     string
		resolve  func(string) (string, error)
		filename string
	}{
		{name: "database", resolve: DatabasePath, filename: DatabaseFilename},
		{name: "socket", resolve: LocalSocketPath, filename: LocalSocketFilename},
		{name: "capability token", resolve: CapabilityTokenPath, filename: CapabilityTokenFilename},
		{name: "device identity", resolve: DeviceIdentityPath, filename: DeviceIdentityFilename},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.resolve("/state")
			if err != nil {
				t.Fatalf("resolve() error = %v", err)
			}
			if want := filepath.Join("/state", test.filename); got != want {
				t.Fatalf("resolve() = %q, want %q", got, want)
			}

			if _, err := test.resolve(""); err == nil {
				t.Fatalf("resolve(\"\") error = nil")
			}
		})
	}
}

func TestJobPaths(t *testing.T) {
	id := mustPathJobID(t, "7a338fa3-7ba4-4c54-bf59-da1161f6b76f")
	directory, err := JobDataDir("/state", id)
	if err != nil {
		t.Fatalf("JobDataDir() error = %v", err)
	}
	wantDirectory := filepath.Join("/state", JobsDirectoryName, string(id))
	if directory != wantDirectory {
		t.Fatalf("JobDataDir() = %q, want %q", directory, wantDirectory)
	}
	logPath, err := JobLogPath("/state", id)
	if err != nil {
		t.Fatalf("JobLogPath() error = %v", err)
	}
	if want := filepath.Join(wantDirectory, JobLogFilename); logPath != want {
		t.Fatalf("JobLogPath() = %q, want %q", logPath, want)
	}
	workspacePath, err := JobWorkspacePath("/state", id)
	if err != nil {
		t.Fatalf("JobWorkspacePath() error = %v", err)
	}
	if want := filepath.Join(wantDirectory, JobWorkspaceName); workspacePath != want {
		t.Fatalf("JobWorkspacePath() = %q, want %q", workspacePath, want)
	}
	if _, err := JobDataDir("/state", "bad"); !errors.Is(err, job.ErrInvalidID) {
		t.Fatalf("JobDataDir(invalid ID) error = %v", err)
	}
}

func TestContentStoreDir(t *testing.T) {
	got, err := ContentStoreDir("/state")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/state", ContentDirectoryName); got != want {
		t.Fatalf("ContentStoreDir() = %q, want %q", got, want)
	}
	if _, err := ContentStoreDir(""); err == nil {
		t.Fatal("ContentStoreDir(\"\") error = nil")
	}
}

func mustPathJobID(t *testing.T, value string) job.ID {
	t.Helper()
	id, err := job.ParseID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
