package main

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	localv1 "github.com/austinjiann/spare-compute/gen/go/computehop/local/v1"
)

func TestDiagnosticsCommandWritesRedactedBundle(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "diagnostics.zip")
	var stdout bytes.Buffer
	var calls int
	command := newRootCommand(dependencies{
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		getwd:  func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				calls++
				switch {
				case request.GetPing() != nil:
					return &localv1.Response{Result: &localv1.Response_Ping{Ping: &localv1.PingResponse{
						DaemonVersion:      "dev",
						DeviceId:           "abcdefgh12345678",
						DeviceName:         "Austin MacBook",
						Role:               localv1.DeviceRole_DEVICE_ROLE_ORCHESTRATOR,
						Platform:           "darwin",
						Arch:               "arm64",
						LogicalCpuCount:    12,
						TotalMemoryBytes:   1234,
						ToolIds:            []string{"go", "docker"},
						SupportedExecutors: []localv1.Executor{localv1.Executor_EXECUTOR_CONTAINER, localv1.Executor_EXECUTOR_NATIVE},
					}}}, nil
				case request.GetListDevices() != nil:
					return &localv1.Response{Result: &localv1.Response_ListDevices{ListDevices: &localv1.ListDevicesResponse{
						DiscoveryState: localv1.DiscoveryState_DISCOVERY_STATE_AVAILABLE,
						TrustedDevices: []*localv1.TrustedDevice{{
							DeviceId:          "worker-device-secret-id",
							PublicKey:         []byte("public-key-is-omitted"),
							Name:              "Gaming PC",
							Role:              localv1.DeviceRole_DEVICE_ROLE_WORKER,
							TrustState:        localv1.DeviceTrustState_DEVICE_TRUST_STATE_PAIRED,
							ConnectivityState: localv1.ConnectivityState_CONNECTIVITY_STATE_CONNECTED,
							ConnectivityPath:  "relay",
							ConnectivityError: "turn-password hunter2",
							Platform:          "windows",
							Arch:              "amd64",
							ToolIds:           []string{"docker"},
						}},
						Devices: []*localv1.NearbyDevice{{
							PresenceId:    "nearby-presence-secret-id",
							Name:          "Gaming PC",
							Role:          localv1.DeviceRole_DEVICE_ROLE_WORKER,
							Addresses:     []string{"192.168.1.50"},
							Port:          47823,
							EndpointReady: true,
							TrustState:    localv1.DeviceTrustState_DEVICE_TRUST_STATE_UNPAIRED,
							Platform:      "windows",
							Arch:          "amd64",
						}},
					}}}, nil
				case request.GetListPairings() != nil:
					return &localv1.Response{Result: &localv1.Response_ListPairings{ListPairings: &localv1.ListPairingsResponse{
						Pairings: []*localv1.Pairing{{
							Id:               "pairing-secret-id",
							PeerDeviceId:     "peer-secret-id",
							PeerPublicKey:    []byte("peer-public-key-is-omitted"),
							PeerName:         "Gaming PC",
							PeerRole:         localv1.DeviceRole_DEVICE_ROLE_WORKER,
							VerificationCode: "ABCD-EFGH-IJKL-MNOP",
							Direction:        localv1.PairingDirection_PAIRING_DIRECTION_INBOUND,
							State:            localv1.PairingState_PAIRING_STATE_WAITING,
						}},
					}}}, nil
				case request.GetListJobs() != nil:
					if request.GetListJobs().GetLimit() != 10 {
						t.Fatalf("diagnostics job limit = %d", request.GetListJobs().GetLimit())
					}
					return &localv1.Response{Result: &localv1.Response_ListJobs{ListJobs: &localv1.ListJobsResponse{
						Jobs: []*localv1.Job{{
							Id:    "7a338fa3-7ba4-4c54-bf59-da1161f6b76f",
							State: localv1.JobState_JOB_STATE_FAILED,
							Spec: &localv1.JobSpec{
								Executable:       "deploy",
								Arguments:        []string{"--token", "sk-secret-token", "--target", "staging"},
								WorkingDirectory: "/Users/austin/project",
								Environment:      map[string]string{"OPENAI_API_KEY": "sk-env-secret"},
								Executor:         localv1.Executor_EXECUTOR_CONTAINER,
								ContainerImage:   "registry.example.com/app:latest",
								Outputs:          []string{"dist"},
								RequiredToolIds:  []string{"docker"},
							},
							Failure: &localv1.Failure{
								Code:    "exit",
								Message: "password=super-secret failed",
							},
						}},
					}}}, nil
				default:
					t.Fatalf("unexpected diagnostics request = %#v", request)
					return nil, nil
				}
			}}, nil
		},
	})
	command.SetArgs([]string{"diagnostics", "--output", outputPath})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if calls != 4 {
		t.Fatalf("calls = %d, want 4", calls)
	}
	if !strings.Contains(stdout.String(), "Wrote redacted diagnostics bundle: "+outputPath) {
		t.Fatalf("stdout = %q", stdout.String())
	}

	contents := readZipContents(t, outputPath)
	for _, name := range []string{
		"README.txt",
		"summary.txt",
		"daemon/status.txt",
		"daemon/devices.txt",
		"daemon/pairings.txt",
		"daemon/jobs.txt",
	} {
		if _, ok := contents[name]; !ok {
			t.Fatalf("missing diagnostics section %s; have %v", name, mapKeys(contents))
		}
	}
	all := strings.Join(mapValues(contents), "\n")
	for _, leaked := range []string{
		"sk-secret-token",
		"sk-env-secret",
		"super-secret",
		"hunter2",
		"ABCD-EFGH-IJKL-MNOP",
		"public-key-is-omitted",
		"peer-public-key-is-omitted",
		"192.168.1.50",
	} {
		if strings.Contains(all, leaked) {
			t.Fatalf("diagnostics leaked %q in:\n%s", leaked, all)
		}
	}
	for _, want := range []string{
		"Daemon: ok",
		"Device name: Austin MacBook",
		"connectivity error: turn-password [redacted]",
		"command: deploy --token [redacted] --target staging",
		"environment: omitted (1 values)",
		"failure message: password=[redacted] failed",
		"Raw job logs are omitted",
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("diagnostics missing %q in:\n%s", want, all)
		}
	}
}

func TestDiagnosticsCommandWritesBundleWhenDaemonIsUnavailable(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "diagnostics.zip")
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		getwd:  func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return nil, fmtDaemonDownForTest()
		},
	})
	command.SetArgs([]string{"diagnostics", "--output", outputPath})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	contents := readZipContents(t, outputPath)
	status := contents["daemon/status.txt"]
	if !strings.Contains(status, "Daemon connection: failed") ||
		!strings.Contains(status, "ComputeHop daemon is not running") {
		t.Fatalf("daemon status = %q", status)
	}
}

func TestDiagnosticsCommandRefusesToOverwriteExistingBundle(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "diagnostics.zip")
	if err := os.WriteFile(outputPath, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := newRootCommand(dependencies{
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
		getwd:  func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			t.Fatal("diagnostics should not connect after output create failure")
			return nil, nil
		},
	})
	command.SetArgs([]string{"diagnostics", "--output", outputPath})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "create diagnostics bundle") {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestRedactDiagnosticTextRedactsCommonSecretShapes(t *testing.T) {
	input := "OPENAI_API_KEY=sk-env --turn-password hunter2 token: abc https://user:pass@example.com/path"
	output := redactDiagnosticText(input)
	for _, leaked := range []string{"sk-env", "hunter2", "abc", "pass@example"} {
		if strings.Contains(output, leaked) {
			t.Fatalf("redacted text leaked %q: %s", leaked, output)
		}
	}
	for _, want := range []string{
		"OPENAI_API_KEY=[redacted]",
		"--turn-password [redacted]",
		"token: [redacted]",
		"https://user:[redacted]@example.com/path",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("redacted text missing %q: %s", want, output)
		}
	}
}

func readZipContents(t *testing.T, path string) map[string]string {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	result := make(map[string]string, len(reader.File))
	for _, file := range reader.File {
		body, err := readZipFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file.Name, err)
		}
		result[file.Name] = string(body)
	}
	return result
}

func readZipFile(file *zip.File) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func mapKeys(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func mapValues(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func fmtDaemonDownForTest() error {
	return errors.New(ErrDaemonNotRunning.Error())
}
