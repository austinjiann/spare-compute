package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	localv1 "github.com/austinjiann/spare-compute/gen/go/computehop/local/v1"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/job"
	"github.com/austinjiann/spare-compute/internal/protocol/mapper"
	"github.com/austinjiann/spare-compute/internal/trust"
)

type stubCaller struct {
	call func(context.Context, *localv1.Request) (*localv1.Response, error)
}

func (stub stubCaller) Call(ctx context.Context, request *localv1.Request) (*localv1.Response, error) {
	return stub.call(ctx, request)
}

func TestRunCommandSubmitsNativeSpec(t *testing.T) {
	value := cliJobForTest(job.StateQueued)
	message, err := mapper.JobToProto(value)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return "/project", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				spec := request.GetSubmitJob().GetSpec()
				if spec.GetExecutable() != "cargo" || strings.Join(spec.GetArguments(), " ") != "build --release" {
					t.Fatalf("submitted spec = %#v", spec)
				}
				if spec.GetWorkingDirectory() != "/project" || spec.GetExecutor() != localv1.Executor_EXECUTOR_NATIVE {
					t.Fatalf("submitted execution fields = %#v", spec)
				}
				return &localv1.Response{Result: &localv1.Response_SubmitJob{
					SubmitJob: &localv1.SubmitJobResponse{Job: message},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"run", "cargo", "build", "--release"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := stdout.String(); got != "Submitted "+string(value.ID)+" (queued)\nFollow it: computehop logs --follow "+string(value.ID)+"\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestRunCommandSelectsRemoteWorkerAndLocalProjectDirectory(t *testing.T) {
	value := cliJobForTest(job.StateQueued)
	message, err := mapper.JobToProto(value)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			t.Fatal("remote run resolved the orchestrator working directory")
			return "", nil
		},
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				submit := request.GetSubmitJob()
				if submit.GetDeviceSelector() != "Gaming PC" ||
					submit.GetSpec().GetWorkingDirectory() != "D:\\projects\\demo" {
					t.Fatalf("submit = %#v", submit)
				}
				return &localv1.Response{Result: &localv1.Response_SubmitJob{
					SubmitJob: &localv1.SubmitJobResponse{Job: message},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{
		"run", "--on", "Gaming PC", "--working-directory", "D:\\projects\\demo", "echo", "hello",
	})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := stdout.String(); got != "Submitted "+string(value.ID)+" to Gaming PC (queued)\nFollow it: computehop logs --follow "+string(value.ID)+"\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestRunCommandDefaultsRemoteProjectToCurrentDirectory(t *testing.T) {
	value := cliJobForTest(job.StateQueued)
	message, err := mapper.JobToProto(value)
	if err != nil {
		t.Fatal(err)
	}
	command := newRootCommand(dependencies{
		stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{},
		getwd: func() (string, error) { return "/local/project", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				if got := request.GetSubmitJob().GetSpec().GetWorkingDirectory(); got != "/local/project" {
					t.Fatalf("working directory = %q", got)
				}
				return &localv1.Response{Result: &localv1.Response_SubmitJob{
					SubmitJob: &localv1.SubmitJobResponse{Job: message},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"run", "--on", "Gaming PC", "go", "test", "./..."})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestRunCommandDeclaresOutputsAndPrintsFetchHint(t *testing.T) {
	value := cliJobForTest(job.StateQueued)
	value.Spec.Outputs = []string{"dist", "report.json"}
	message, err := mapper.JobToProto(value)
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return t.TempDir(), nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				if got := request.GetSubmitJob().GetSpec().GetOutputs(); strings.Join(got, ",") != "dist,report.json" {
					t.Fatalf("outputs = %#v", got)
				}
				return &localv1.Response{Result: &localv1.Response_SubmitJob{
					SubmitJob: &localv1.SubmitJobResponse{Job: message},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"run", "-o", "dist", "--output", "report.json", "go", "build", "./..."})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Get outputs after it succeeds: computehop artifacts "+string(value.ID)) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunCommandFollowsAndFetchesDeclaredOutputs(t *testing.T) {
	value := cliJobForTest(job.StateQueued)
	value.Spec.Outputs = []string{"dist/result.txt"}
	submitted, err := mapper.JobToProto(value)
	if err != nil {
		t.Fatal(err)
	}
	succeeded := value
	succeeded.State = job.StateSucceeded
	succeededMessage, err := mapper.JobToProto(succeeded)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "outputs")
	var calls int
	var stdout, stderr bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &stderr, getwd: func() (string, error) { return "/project", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				calls++
				switch calls {
				case 1:
					submit := request.GetSubmitJob()
					if submit.GetDeviceSelector() != "Gaming PC" ||
						strings.Join(submit.GetSpec().GetOutputs(), ",") != "dist/result.txt" {
						t.Fatalf("submit = %#v", submit)
					}
					return &localv1.Response{Result: &localv1.Response_SubmitJob{
						SubmitJob: &localv1.SubmitJobResponse{Job: submitted},
					}}, nil
				case 2:
					read := request.GetReadJobLogs()
					if read.GetJobId() != string(value.ID) || read.GetDeviceSelector() != "Gaming PC" {
						t.Fatalf("read logs = %#v", read)
					}
					return &localv1.Response{Result: &localv1.Response_ReadJobLogs{
						ReadJobLogs: &localv1.ReadJobLogsResponse{
							Job: succeededMessage,
							Records: []*localv1.JobLogRecord{
								{
									Sequence: 1,
									Stream:   localv1.JobLogStream_JOB_LOG_STREAM_STDOUT,
									Data:     []byte("built\n"),
								},
							},
						},
					}}, nil
				case 3:
					fetch := request.GetFetchArtifacts()
					if fetch.GetJobId() != string(value.ID) ||
						fetch.GetDeviceSelector() != "Gaming PC" ||
						fetch.GetDestination() != destination {
						t.Fatalf("fetch = %#v", fetch)
					}
					return &localv1.Response{Result: &localv1.Response_FetchArtifacts{
						FetchArtifacts: &localv1.FetchArtifactsResponse{
							Destination:       destination,
							RestoredFileCount: 1,
						},
					}}, nil
				default:
					t.Fatalf("unexpected call %d", calls)
					return nil, nil
				}
			}}, nil
		},
	})
	command.SetArgs([]string{
		"run", "--on", "Gaming PC", "-o", "dist/result.txt", "--follow", "--get", "--to", destination, "go", "build",
	})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	for _, want := range []string{
		"Submitted " + string(value.ID) + " to Gaming PC (queued)",
		"built\n",
		"Job " + string(value.ID) + " succeeded",
		"Restored 1 output file(s) to " + destination,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("stdout %q does not contain %q", output, want)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunCommandGetWaitsAndFetchesDeclaredOutputsToWorkingDirectory(t *testing.T) {
	value := cliJobForTest(job.StateQueued)
	value.Spec.Outputs = []string{"dist/result.txt"}
	submitted, err := mapper.JobToProto(value)
	if err != nil {
		t.Fatal(err)
	}
	succeeded := value
	succeeded.State = job.StateSucceeded
	succeededMessage, err := mapper.JobToProto(succeeded)
	if err != nil {
		t.Fatal(err)
	}
	workingDirectory := t.TempDir()
	wantDestination := workingDirectory
	var calls int
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return workingDirectory, nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				calls++
				switch calls {
				case 1:
					return &localv1.Response{Result: &localv1.Response_SubmitJob{
						SubmitJob: &localv1.SubmitJobResponse{Job: submitted},
					}}, nil
				case 2:
					get := request.GetGetJob()
					if get.GetJobId() != string(value.ID) || get.GetDeviceSelector() != "" {
						t.Fatalf("get job = %#v", get)
					}
					return &localv1.Response{Result: &localv1.Response_GetJob{
						GetJob: &localv1.GetJobResponse{Job: succeededMessage},
					}}, nil
				case 3:
					fetch := request.GetFetchArtifacts()
					if fetch.GetDestination() != wantDestination {
						t.Fatalf("fetch destination = %q, want %q", fetch.GetDestination(), wantDestination)
					}
					return &localv1.Response{Result: &localv1.Response_FetchArtifacts{
						FetchArtifacts: &localv1.FetchArtifactsResponse{
							Destination:       wantDestination,
							RestoredFileCount: 1,
						},
					}}, nil
				default:
					t.Fatalf("unexpected call %d", calls)
					return nil, nil
				}
			}}, nil
		},
	})
	command.SetArgs([]string{"run", "-o", "dist/result.txt", "--get", "go", "build"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Waiting for " + string(value.ID) + " to finish...",
		"Job " + string(value.ID) + " succeeded",
		"Restored 1 output file(s) to " + wantDestination,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestRunCommandGetRequiresDeclaredOutputs(t *testing.T) {
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		getwd:  func() (string, error) { return "/project", nil },
		newClient: func(string) (caller, error) {
			t.Fatal("client should not be created for invalid --get usage")
			return nil, nil
		},
	})
	command.SetArgs([]string{"run", "--get", "go", "build"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "--get requires") {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestRunCommandToRequiresGet(t *testing.T) {
	command := newRootCommand(dependencies{
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
		getwd:  func() (string, error) { return "/project", nil },
		newClient: func(string) (caller, error) {
			t.Fatal("client should not be created for invalid --to usage")
			return nil, nil
		},
	})
	command.SetArgs([]string{"run", "-o", "dist/result.txt", "--to", "out", "go", "build"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "--to requires --get") {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestRunCommandWaitReturnsTerminalFailure(t *testing.T) {
	value := cliJobForTest(job.StateQueued)
	submitted, err := mapper.JobToProto(value)
	if err != nil {
		t.Fatal(err)
	}
	failed := value
	failed.State = job.StateFailed
	failed.Failure = &job.Failure{Code: "exit", Message: "exit status 1"}
	failedMessage, err := mapper.JobToProto(failed)
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	command := newRootCommand(dependencies{
		stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "/project", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				calls++
				switch calls {
				case 1:
					return &localv1.Response{Result: &localv1.Response_SubmitJob{
						SubmitJob: &localv1.SubmitJobResponse{Job: submitted},
					}}, nil
				case 2:
					return &localv1.Response{Result: &localv1.Response_GetJob{
						GetJob: &localv1.GetJobResponse{Job: failedMessage},
					}}, nil
				default:
					t.Fatalf("unexpected call %d", calls)
					return nil, nil
				}
			}}, nil
		},
	})
	command.SetArgs([]string{"run", "--wait", "false"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "ended as failed") {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestArtifactsCommandUsesSafeDefaultDestinationAndReportsConflicts(t *testing.T) {
	value := cliJobForTest(job.StateSucceeded)
	workingDirectory := t.TempDir()
	wantDestination := filepath.Join(workingDirectory, ".computehop-results", string(value.ID))
	var stdout, stderr bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &stderr, getwd: func() (string, error) { return workingDirectory, nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				fetch := request.GetFetchArtifacts()
				if fetch.GetJobId() != string(value.ID) || fetch.GetDestination() != wantDestination ||
					fetch.GetDeviceSelector() != "" {
					t.Fatalf("fetch = %#v", fetch)
				}
				return &localv1.Response{Result: &localv1.Response_FetchArtifacts{
					FetchArtifacts: &localv1.FetchArtifactsResponse{
						Destination:       wantDestination,
						RestoredFileCount: 1,
						ConflictFileCount: 1,
					},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"artifacts", string(value.ID)})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Restored 1 output file(s) to "+wantDestination) ||
		!strings.Contains(stderr.String(), "Kept existing files unchanged") ||
		!strings.Contains(stderr.String(), ".computehop-conflicts") {
		t.Fatalf("stdout = %q; stderr = %q", stdout.String(), stderr.String())
	}
}

func TestJobsCommandPrintsDurableJobs(t *testing.T) {
	value := cliJobForTest(job.StateQueued)
	value.Progress = &job.Progress{
		Phase: job.ProgressDownload, CompletedBytes: 512 << 10,
		TotalBytes: 1024 << 10, UpdatedAt: value.UpdatedAt,
	}
	message, err := mapper.JobToProto(value)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return "", nil },
		newClient: func(stateDir string) (caller, error) {
			if stateDir != "/custom-state" {
				t.Fatalf("state directory = %q", stateDir)
			}
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				if request.GetListJobs().GetLimit() != 25 {
					t.Fatalf("list limit = %d", request.GetListJobs().GetLimit())
				}
				return &localv1.Response{Result: &localv1.Response_ListJobs{
					ListJobs: &localv1.ListJobsResponse{Jobs: []*localv1.Job{message}},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"--state-dir", "/custom-state", "jobs", "--limit", "25"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"ID", "PROGRESS", string(value.ID), "queued", "download 50% (512KiB/1MiB)",
		"echo hello", "2023-11-14T22:13:21Z",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("stdout %q does not contain %q", output, want)
		}
	}
}

func TestDevicesCommandPrintsNearbyDevicesAsUnpaired(t *testing.T) {
	presenceID, err := device.NewPresenceID(bytes.NewReader(make([]byte, 16)))
	if err != nil {
		t.Fatal(err)
	}
	seen := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	var stdout, stderr bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				if request.GetListDevices() == nil {
					t.Fatalf("request = %#v", request)
				}
				return &localv1.Response{Result: &localv1.Response_ListDevices{
					ListDevices: &localv1.ListDevicesResponse{
						DiscoveryState: localv1.DiscoveryState_DISCOVERY_STATE_AVAILABLE,
						Devices: []*localv1.NearbyDevice{{
							PresenceId: string(presenceID), Name: "Gaming PC",
							Role:      localv1.DeviceRole_DEVICE_ROLE_WORKER,
							Addresses: []string{"192.0.2.20"}, Port: 47823,
							LastSeenAtUnixNano: seen.UnixNano(),
							TrustState:         localv1.DeviceTrustState_DEVICE_TRUST_STATE_UNPAIRED,
						}},
					},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"devices"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{"Gaming PC", presenceID.Short(), "unpaired", "worker", "192.0.2.20 (discovery only)", "2026-07-19T12:00:00Z"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestDevicesCommandCombinesOneTrustedPeerWithItsNearbyPresence(t *testing.T) {
	identity, err := device.GenerateIdentity(bytes.NewReader(bytes.Repeat([]byte{9}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	pairID, err := trust.NewPairID(bytes.NewReader(bytes.Repeat([]byte{10}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	presenceID, err := device.NewPresenceID(bytes.NewReader(bytes.Repeat([]byte{11}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	seen := time.Date(2026, time.July, 22, 5, 0, 0, 0, time.UTC)
	trusted, err := mapper.TrustedPeerToProto(trust.Peer{
		PairID: pairID, DeviceID: identity.ID(), PublicKey: identity.PublicKey(),
		Name: "Gaming PC", Role: device.RoleWorker, State: trust.StateActive,
		PairedAt: seen.Add(-time.Hour), UpdatedAt: seen.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &stderr, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(context.Context, *localv1.Request) (*localv1.Response, error) {
				return &localv1.Response{Result: &localv1.Response_ListDevices{
					ListDevices: &localv1.ListDevicesResponse{
						DiscoveryState: localv1.DiscoveryState_DISCOVERY_STATE_AVAILABLE,
						TrustedDevices: []*localv1.TrustedDevice{trusted},
						Devices: []*localv1.NearbyDevice{{
							PresenceId: string(presenceID), Name: "Gaming PC",
							Role: localv1.DeviceRole_DEVICE_ROLE_WORKER, Addresses: []string{"192.0.2.20"},
							Port: 47823, EndpointReady: true, LastSeenAtUnixNano: seen.UnixNano(),
							TrustState: localv1.DeviceTrustState_DEVICE_TRUST_STATE_UNPAIRED,
						}},
					},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"devices"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := stdout.String()
	if strings.Count(output, "Gaming PC") != 1 {
		t.Fatalf("stdout contains duplicate device rows: %q", output)
	}
	for _, want := range []string{identity.ID().Short(), "active", "nearby", "192.0.2.20:47823"} {
		if !strings.Contains(output, want) {
			t.Fatalf("stdout %q does not contain %q", output, want)
		}
	}
	if strings.Contains(output, presenceID.Short()) {
		t.Fatalf("stdout leaked the redundant ephemeral identifier: %q", output)
	}
}

func TestDevicesCommandShowsRemotePathForOfflineLANPeer(t *testing.T) {
	identity, err := device.GenerateIdentity(bytes.NewReader(bytes.Repeat([]byte{12}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	pairID, err := trust.NewPairID(bytes.NewReader(bytes.Repeat([]byte{13}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	updatedAt := time.Date(2026, time.July, 22, 7, 0, 0, 0, time.UTC)
	trusted, err := mapper.TrustedPeerToProto(trust.Peer{
		PairID: pairID, DeviceID: identity.ID(), PublicKey: identity.PublicKey(),
		Name: "Remote PC", Role: device.RoleWorker, State: trust.StateActive,
		PairedAt: updatedAt.Add(-time.Hour), UpdatedAt: updatedAt.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	trusted.ConnectivityState = localv1.ConnectivityState_CONNECTIVITY_STATE_CONNECTED
	trusted.ConnectivityPath = "server-reflexive"
	trusted.ConnectivityUpdatedAtUnixNano = updatedAt.UnixNano()

	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(context.Context, *localv1.Request) (*localv1.Response, error) {
				return &localv1.Response{Result: &localv1.Response_ListDevices{
					ListDevices: &localv1.ListDevicesResponse{
						DiscoveryState: localv1.DiscoveryState_DISCOVERY_STATE_AVAILABLE,
						TrustedDevices: []*localv1.TrustedDevice{trusted},
					},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"devices"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Remote PC", "remote", "direct (STUN)", "2026-07-22T07:00:00Z"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestDoctorCommandReportsOfflinePairedWorker(t *testing.T) {
	identity, err := device.GenerateIdentity(bytes.NewReader(bytes.Repeat([]byte{14}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	pairID, err := trust.NewPairID(bytes.NewReader(bytes.Repeat([]byte{15}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	updatedAt := time.Date(2026, time.July, 22, 9, 0, 0, 0, time.UTC)
	trusted, err := mapper.TrustedPeerToProto(trust.Peer{
		PairID: pairID, DeviceID: identity.ID(), PublicKey: identity.PublicKey(),
		Name: "Gaming PC", Role: device.RoleWorker, State: trust.StateActive,
		PairedAt: updatedAt.Add(-time.Hour), UpdatedAt: updatedAt,
	})
	if err != nil {
		t.Fatal(err)
	}

	var calls int
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				calls++
				switch calls {
				case 1:
					if request.GetPing() == nil {
						t.Fatalf("first request = %#v", request)
					}
					return &localv1.Response{Result: &localv1.Response_Ping{
						Ping: &localv1.PingResponse{
							DaemonVersion: "dev",
							DeviceId:      string(identity.ID()),
							DeviceName:    "Austin MacBook 1",
							Role:          localv1.DeviceRole_DEVICE_ROLE_ORCHESTRATOR,
						},
					}}, nil
				case 2:
					if request.GetListDevices() == nil {
						t.Fatalf("second request = %#v", request)
					}
					return &localv1.Response{Result: &localv1.Response_ListDevices{
						ListDevices: &localv1.ListDevicesResponse{
							DiscoveryState: localv1.DiscoveryState_DISCOVERY_STATE_AVAILABLE,
							TrustedDevices: []*localv1.TrustedDevice{trusted},
						},
					}}, nil
				default:
					t.Fatalf("unexpected call %d", calls)
					return nil, nil
				}
			}}, nil
		},
	})
	command.SetArgs([]string{"doctor"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Daemon: ok (computehopd dev)",
		"Device: Austin MacBook 1 (orchestrator, " + identity.ID().Short() + ")",
		"LAN discovery: available",
		"Paired devices: 1 active, 0 revoked",
		"Reachable workers: 0",
		"1 paired worker(s) exist but are not reachable right now.",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestDoctorCommandSuggestsRemoteSmokeTestForConnectedWorker(t *testing.T) {
	identity, err := device.GenerateIdentity(bytes.NewReader(bytes.Repeat([]byte{16}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	pairID, err := trust.NewPairID(bytes.NewReader(bytes.Repeat([]byte{17}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	updatedAt := time.Date(2026, time.July, 22, 9, 30, 0, 0, time.UTC)
	trusted, err := mapper.TrustedPeerToProto(trust.Peer{
		PairID: pairID, DeviceID: identity.ID(), PublicKey: identity.PublicKey(),
		Name: "Gaming PC", Role: device.RoleWorker, State: trust.StateActive,
		PairedAt: updatedAt.Add(-time.Hour), UpdatedAt: updatedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	presenceID, err := device.NewPresenceID(bytes.NewReader(bytes.Repeat([]byte{19}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	trusted.ConnectivityState = localv1.ConnectivityState_CONNECTIVITY_STATE_CONNECTED
	trusted.ConnectivityPath = "host"
	trusted.ConnectivityUpdatedAtUnixNano = updatedAt.UnixNano()

	var calls int
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				calls++
				switch calls {
				case 1:
					return &localv1.Response{Result: &localv1.Response_Ping{
						Ping: &localv1.PingResponse{DaemonVersion: "dev"},
					}}, nil
				case 2:
					return &localv1.Response{Result: &localv1.Response_ListDevices{
						ListDevices: &localv1.ListDevicesResponse{
							DiscoveryState: localv1.DiscoveryState_DISCOVERY_STATE_AVAILABLE,
							TrustedDevices: []*localv1.TrustedDevice{trusted},
							Devices: []*localv1.NearbyDevice{{
								PresenceId: string(presenceID), Name: "Gaming PC",
								Role:      localv1.DeviceRole_DEVICE_ROLE_WORKER,
								Addresses: []string{"192.0.2.20"}, Port: 47823,
								LastSeenAtUnixNano: updatedAt.UnixNano(),
								TrustState:         localv1.DeviceTrustState_DEVICE_TRUST_STATE_UNPAIRED,
							}},
						},
					}}, nil
				default:
					t.Fatalf("unexpected call %d", calls)
					return nil, nil
				}
			}}, nil
		},
	})
	command.SetArgs([]string{"doctor"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Reachable workers: 1 (Gaming PC)",
		"Nearby unpaired devices: 0",
		"computehop run --on " + identity.ID().Short() + " hostname",
		"computehop jobs --on " + identity.ID().Short(),
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestDoctorCommandSuggestsPairingNearbyWorker(t *testing.T) {
	presenceID, err := device.NewPresenceID(bytes.NewReader(bytes.Repeat([]byte{18}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	seen := time.Date(2026, time.July, 22, 10, 0, 0, 0, time.UTC)
	var calls int
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				calls++
				switch calls {
				case 1:
					return &localv1.Response{Result: &localv1.Response_Ping{
						Ping: &localv1.PingResponse{DaemonVersion: "dev"},
					}}, nil
				case 2:
					return &localv1.Response{Result: &localv1.Response_ListDevices{
						ListDevices: &localv1.ListDevicesResponse{
							DiscoveryState: localv1.DiscoveryState_DISCOVERY_STATE_AVAILABLE,
							Devices: []*localv1.NearbyDevice{{
								PresenceId: string(presenceID), Name: "Gaming PC",
								Role:      localv1.DeviceRole_DEVICE_ROLE_WORKER,
								Addresses: []string{"192.0.2.20"}, Port: 47823,
								LastSeenAtUnixNano: seen.UnixNano(),
								TrustState:         localv1.DeviceTrustState_DEVICE_TRUST_STATE_UNPAIRED,
							}},
						},
					}}, nil
				default:
					t.Fatalf("unexpected call %d", calls)
					return nil, nil
				}
			}}, nil
		},
	})
	command.SetArgs([]string{"doctor"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Nearby unpaired devices: 1",
		"computehop connect \"Gaming PC\"",
		"computehop connect confirm",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestDoctorCommandPrintsStartAdviceWhenDaemonIsNotRunning(t *testing.T) {
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return nil, fmt.Errorf("%w: daemon down", ErrDaemonNotRunning)
		},
	})
	command.SetArgs([]string{"doctor"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"Daemon: not running",
		"open -a ComputeHop",
		"make install-macos",
		"go run ./cmd/computehopd --role orchestrator --device-name \"This Mac\"",
		"computehop doctor",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestDoctorCommandReturnsUnexpectedClientError(t *testing.T) {
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return nil, errors.New("permission denied")
		},
	})
	command.SetArgs([]string{"doctor"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("Execute() error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestDoctorCommandPrintsStartAdviceWhenPingCannotReachDaemon(t *testing.T) {
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(context.Context, *localv1.Request) (*localv1.Response, error) {
				return nil, fmt.Errorf("%w: socket closed", ErrDaemonNotRunning)
			}}, nil
		},
	})
	command.SetArgs([]string{"doctor"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "Daemon: not running") ||
		!strings.Contains(stdout.String(), "computehop doctor") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestConnectCommandWithoutDeviceSuggestsNearbyWorker(t *testing.T) {
	presenceID, err := device.NewPresenceID(bytes.NewReader(bytes.Repeat([]byte{22}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	seen := time.Date(2026, time.July, 22, 11, 0, 0, 0, time.UTC)
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				if request.GetListDevices() == nil {
					t.Fatalf("request = %#v", request)
				}
				return &localv1.Response{Result: &localv1.Response_ListDevices{
					ListDevices: &localv1.ListDevicesResponse{
						DiscoveryState: localv1.DiscoveryState_DISCOVERY_STATE_AVAILABLE,
						Devices: []*localv1.NearbyDevice{{
							PresenceId: string(presenceID), Name: "Gaming PC",
							Role:      localv1.DeviceRole_DEVICE_ROLE_WORKER,
							Addresses: []string{"192.0.2.20"}, Port: 47823,
							LastSeenAtUnixNano: seen.UnixNano(),
							TrustState:         localv1.DeviceTrustState_DEVICE_TRUST_STATE_UNPAIRED,
						}},
					},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"connect"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"LAN discovery: available",
		"Nearby unpaired devices: 1",
		"computehop connect \"Gaming PC\"",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestConnectCommandBeginsPairingWithConnectInstructions(t *testing.T) {
	value := cliPairingForTest(t)
	message, err := mapper.PairingToProto(value)
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				if got := request.GetBeginPairing().GetDeviceSelector(); got != "Gaming PC" {
					t.Fatalf("device selector = %q", got)
				}
				return &localv1.Response{Result: &localv1.Response_BeginPairing{
					BeginPairing: &localv1.BeginPairingResponse{Pairing: message},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"connect", "Gaming PC"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		value.ID.Short(), value.PeerName, string(value.Verification),
		"Compare this exact code on both devices", "computehop connect confirm",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
	if strings.Contains(stdout.String(), "computehop pair confirm") {
		t.Fatalf("stdout used pair instructions instead of connect: %q", stdout.String())
	}
}

func TestConnectConfirmInfersTheOnlyActionableRequest(t *testing.T) {
	value := cliPairingForTest(t)
	waiting, err := mapper.PairingToProto(value)
	if err != nil {
		t.Fatal(err)
	}
	confirmedValue := value
	confirmedValue.LocalConfirmed = true
	confirmed, err := mapper.PairingToProto(confirmedValue)
	if err != nil {
		t.Fatal(err)
	}

	var calls int
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				calls++
				switch calls {
				case 1:
					if request.GetListPairings() == nil {
						t.Fatalf("first request = %#v", request)
					}
					return &localv1.Response{Result: &localv1.Response_ListPairings{
						ListPairings: &localv1.ListPairingsResponse{Pairings: []*localv1.Pairing{waiting}},
					}}, nil
				case 2:
					if got := request.GetConfirmPairing().GetPairingSelector(); got != string(value.ID) {
						t.Fatalf("pairing selector = %q", got)
					}
					return &localv1.Response{Result: &localv1.Response_ConfirmPairing{
						ConfirmPairing: &localv1.ConfirmPairingResponse{Pairing: confirmed},
					}}, nil
				default:
					t.Fatalf("unexpected call %d", calls)
					return nil, nil
				}
			}}, nil
		},
	})
	command.SetArgs([]string{"connect", "confirm"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "Confirmed Gaming PC locally; state: waiting.\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestPairCommandPrintsConnectionBoundVerificationInstructions(t *testing.T) {
	value := cliPairingForTest(t)
	message, err := mapper.PairingToProto(value)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &stderr, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				if got := request.GetBeginPairing().GetDeviceSelector(); got != "Gaming PC" {
					t.Fatalf("device selector = %q", got)
				}
				return &localv1.Response{Result: &localv1.Response_BeginPairing{
					BeginPairing: &localv1.BeginPairingResponse{Pairing: message},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"pair", "Gaming PC"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		value.ID.Short(), value.PeerName, string(value.Verification),
		"Compare this exact code on both devices", "computehop pair confirm",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestPairConfirmInfersTheOnlyActionableRequest(t *testing.T) {
	value := cliPairingForTest(t)
	waiting, err := mapper.PairingToProto(value)
	if err != nil {
		t.Fatal(err)
	}
	confirmedValue := value
	confirmedValue.LocalConfirmed = true
	confirmed, err := mapper.PairingToProto(confirmedValue)
	if err != nil {
		t.Fatal(err)
	}

	var calls int
	var stdout, stderr bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &stderr, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				calls++
				switch calls {
				case 1:
					if request.GetListPairings() == nil {
						t.Fatalf("first request = %#v", request)
					}
					return &localv1.Response{Result: &localv1.Response_ListPairings{
						ListPairings: &localv1.ListPairingsResponse{Pairings: []*localv1.Pairing{waiting}},
					}}, nil
				case 2:
					if got := request.GetConfirmPairing().GetPairingSelector(); got != string(value.ID) {
						t.Fatalf("pairing selector = %q", got)
					}
					return &localv1.Response{Result: &localv1.Response_ConfirmPairing{
						ConfirmPairing: &localv1.ConfirmPairingResponse{Pairing: confirmed},
					}}, nil
				default:
					t.Fatalf("unexpected call %d", calls)
					return nil, nil
				}
			}}, nil
		},
	})
	command.SetArgs([]string{"pair", "confirm"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := stdout.String(); got != "Confirmed Gaming PC locally; state: waiting.\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestStatusCommandPrintsLocalDeviceIdentity(t *testing.T) {
	identity, err := device.GenerateIdentity(bytes.NewReader(bytes.Repeat([]byte{20}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		getwd:  func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				if request.GetPing() == nil {
					t.Fatalf("request = %#v", request)
				}
				return &localv1.Response{Result: &localv1.Response_Ping{
					Ping: &localv1.PingResponse{
						DaemonVersion: "dev",
						DeviceId:      string(identity.ID()),
						DeviceName:    "Austin MacBook 1",
						Role:          localv1.DeviceRole_DEVICE_ROLE_ORCHESTRATOR,
					},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"status"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"computehopd dev ready",
		"Device: Austin MacBook 1 (orchestrator, " + identity.ID().Short() + ")",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestStatusRejectsMissingResult(t *testing.T) {
	var stdout, stderr bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(context.Context, *localv1.Request) (*localv1.Response, error) {
				return &localv1.Response{}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"status"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "missing ping result") {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestLogsCommandRoutesDurableStreams(t *testing.T) {
	value := cliJobForTest(job.StateSucceeded)
	message, err := mapper.JobToProto(value)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				if request.GetReadJobLogs().GetAfterSequence() != 0 {
					t.Fatalf("after sequence = %d", request.GetReadJobLogs().GetAfterSequence())
				}
				return &localv1.Response{Result: &localv1.Response_ReadJobLogs{
					ReadJobLogs: &localv1.ReadJobLogsResponse{
						Job: message,
						Records: []*localv1.JobLogRecord{
							{Sequence: 1, Stream: localv1.JobLogStream_JOB_LOG_STREAM_STDOUT, Data: []byte("output\n")},
							{Sequence: 2, Stream: localv1.JobLogStream_JOB_LOG_STREAM_STDERR, Data: []byte("problem\n")},
						},
					},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"logs", string(value.ID)})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stdout.String() != "output\n" || stderr.String() != "problem\n" {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}

func cliJobForTest(state job.State) job.Job {
	created := time.Unix(1_700_000_000, 0).UTC()
	return job.Job{
		ID: "7a338fa3-7ba4-4c54-bf59-da1161f6b76f",
		Spec: job.Spec{
			Executable: "echo",
			Arguments:  []string{"hello"},
			Executor:   job.ExecutorNative,
		},
		State:     state,
		CreatedAt: created,
		UpdatedAt: created.Add(time.Second),
	}
}

func cliPairingForTest(t *testing.T) trust.Pairing {
	t.Helper()
	identity, err := device.GenerateIdentity(bytes.NewReader(bytes.Repeat([]byte{7}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	pairID, err := trust.NewPairID(bytes.NewReader(bytes.Repeat([]byte{8}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	started := time.Unix(1_700_000_000, 0).UTC()
	return trust.Pairing{
		ID: pairID, PeerID: identity.ID(), PeerPublicKey: identity.PublicKey(),
		PeerName: "Gaming PC", PeerRole: device.RoleWorker,
		Verification: "0123-4567-89AB-CDEF", Direction: trust.DirectionOutbound,
		State: trust.PairingWaiting, StartedAt: started, ExpiresAt: started.Add(5 * time.Minute),
	}
}
