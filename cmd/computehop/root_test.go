package main

import (
	"bytes"
	"context"
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

func TestRunCommandSelectsRemoteWorkerAndTargetDirectory(t *testing.T) {
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

func TestJobsCommandPrintsDurableJobs(t *testing.T) {
	value := cliJobForTest(job.StateQueued)
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
	for _, want := range []string{"ID", string(value.ID), "queued", "echo hello", "2023-11-14T22:13:21Z"} {
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
