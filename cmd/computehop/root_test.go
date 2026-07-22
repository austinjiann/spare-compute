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
	"github.com/austinjiann/spare-compute/internal/protocol/localipc"
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
	if got := stderr.String(); got != "Preparing remote run for Gaming PC from D:\\projects\\demo; snapshot/upload may take a moment.\n" {
		t.Fatalf("stderr = %q", got)
	}
}

func TestRunCommandShowsFriendlyAutoSelector(t *testing.T) {
	value := cliJobForTest(job.StateQueued)
	message, err := mapper.JobToProto(value)
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		getwd:  func() (string, error) { return "/project", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				if got := request.GetSubmitJob().GetDeviceSelector(); got != "auto" {
					t.Fatalf("device selector = %q", got)
				}
				return &localv1.Response{Result: &localv1.Response_SubmitJob{
					SubmitJob: &localv1.SubmitJobResponse{Job: message},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"run", "--on", "auto", "hostname"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "Submitted "+string(value.ID)+" to an automatically selected worker (queued)\nFollow it: computehop logs --follow "+string(value.ID)+"\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestRunCommandAutoSelectorErrorExplainsConnectNearby(t *testing.T) {
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		getwd:  func() (string, error) { return "/project", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				if got := request.GetSubmitJob().GetDeviceSelector(); got != "auto" {
					t.Fatalf("device selector = %q", got)
				}
				return nil, &localipc.RemoteError{
					Code: localv1.ErrorCode_ERROR_CODE_DEVICE_UNAVAILABLE,
					Message: "paired worker is unavailable: automatic worker selection found no active paired workers; " +
						"run 'computehop connect'",
				}
			}}, nil
		},
	})
	command.SetArgs([]string{"run", "--on", "auto", "hostname"})
	err := command.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	for _, want := range []string{
		"no active paired worker is available for --on auto",
		"computehop connect nearby",
		"computehop devices",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q; missing %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "automatic worker selection") {
		t.Fatalf("error leaked backend wording: %q", err)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestRunCommandAutoSelectorErrorExplainsExplicitChoice(t *testing.T) {
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		getwd:  func() (string, error) { return "/project", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				if got := request.GetSubmitJob().GetDeviceSelector(); got != "best" {
					t.Fatalf("device selector = %q", got)
				}
				return nil, &localipc.RemoteError{
					Code:    localv1.ErrorCode_ERROR_CODE_CONFLICT,
					Message: "remote worker conflict: automatic worker selection found 2 active workers; choose one with --on <device>",
				}
			}}, nil
		},
	})
	command.SetArgs([]string{"run", "--on", "best", "hostname"})
	err := command.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	for _, want := range []string{
		"more than one active paired worker is available for --on auto",
		"computehop devices",
		"computehop run --on <device> ...",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q; missing %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "automatic worker selection") {
		t.Fatalf("error leaked backend wording: %q", err)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestRunCommandExplicitSelectorNotFoundErrorExplainsDevices(t *testing.T) {
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		getwd:  func() (string, error) { return "/project", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				if got := request.GetSubmitJob().GetDeviceSelector(); got != "Austin MacBook 2" {
					t.Fatalf("device selector = %q", got)
				}
				return nil, &localipc.RemoteError{
					Code:    localv1.ErrorCode_ERROR_CODE_NOT_FOUND,
					Message: "trusted peer not found: active worker Austin MacBook 2",
				}
			}}, nil
		},
	})
	command.SetArgs([]string{"run", "--on", "Austin MacBook 2", "hostname"})
	err := command.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	for _, want := range []string{
		"no active paired worker matches \"Austin MacBook 2\"",
		"computehop devices",
		"computehop connect nearby",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q; missing %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "trusted peer not found") {
		t.Fatalf("error leaked backend wording: %q", err)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestRunCommandExplicitSelectorAmbiguityExplainsLongerID(t *testing.T) {
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		getwd:  func() (string, error) { return "/project", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				if got := request.GetSubmitJob().GetDeviceSelector(); got != "mac" {
					t.Fatalf("device selector = %q", got)
				}
				return nil, &localipc.RemoteError{
					Code:    localv1.ErrorCode_ERROR_CODE_CONFLICT,
					Message: "trusted peer conflict: mac matches 2 active workers",
				}
			}}, nil
		},
	})
	command.SetArgs([]string{"run", "--on", "mac", "hostname"})
	err := command.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	for _, want := range []string{
		"more than one active paired worker matches \"mac\"",
		"computehop devices",
		"use a longer device ID",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q; missing %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "trusted peer conflict") {
		t.Fatalf("error leaked backend wording: %q", err)
	}
	if got := stdout.String(); got != "" {
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

func TestRunCommandNoProjectSubmitsEmptyWorkingDirectory(t *testing.T) {
	value := cliJobForTest(job.StateQueued)
	message, err := mapper.JobToProto(value)
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &bytes.Buffer{}, stderr: &stderr,
		getwd: func() (string, error) {
			t.Fatal("--no-project resolved the local working directory")
			return "", nil
		},
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				submit := request.GetSubmitJob()
				if submit.GetDeviceSelector() != "auto" || submit.GetSpec().GetWorkingDirectory() != "" {
					t.Fatalf("submit = %#v", submit)
				}
				return &localv1.Response{Result: &localv1.Response_SubmitJob{
					SubmitJob: &localv1.SubmitJobResponse{Job: message},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"run", "--on", "auto", "--no-project", "hostname"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q", got)
	}
}

func TestRunCommandNoProjectRequiresRemoteTarget(t *testing.T) {
	command := newRootCommand(dependencies{
		stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			t.Fatal("client should not be created for invalid --no-project usage")
			return nil, nil
		},
	})
	command.SetArgs([]string{"run", "--no-project", "hostname"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "--no-project requires --on") {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestRunCommandNoProjectRejectsWorkingDirectory(t *testing.T) {
	command := newRootCommand(dependencies{
		stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			t.Fatal("client should not be created for invalid --no-project usage")
			return nil, nil
		},
	})
	command.SetArgs([]string{"run", "--on", "auto", "--no-project", "-C", "/project", "hostname"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestRunCommandNoProjectRejectsDeclaredOutputs(t *testing.T) {
	command := newRootCommand(dependencies{
		stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			t.Fatal("client should not be created for invalid --no-project usage")
			return nil, nil
		},
	})
	command.SetArgs([]string{"run", "--on", "auto", "--no-project", "-o", "result.txt", "hostname"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "cannot be combined with --output") {
		t.Fatalf("Execute() error = %v", err)
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
	if !strings.Contains(stdout.String(), "Get outputs after it succeeds: computehop outputs "+string(value.ID)) {
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
	if got := stderr.String(); got != "Preparing remote run for Gaming PC from /project; snapshot/upload may take a moment.\n" {
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

func TestSmokeCommandSubmitsHostnameWithoutProjectAndFollowsLogs(t *testing.T) {
	value := cliJobForTest(job.StateQueued)
	value.Spec.Executable = "hostname"
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
	var calls int
	var stdout, stderr bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &stderr, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				calls++
				switch calls {
				case 1:
					submit := request.GetSubmitJob()
					if submit.GetDeviceSelector() != "auto" ||
						submit.GetSpec().GetExecutable() != "hostname" ||
						submit.GetSpec().GetWorkingDirectory() != "" {
						t.Fatalf("submit = %#v", submit)
					}
					return &localv1.Response{Result: &localv1.Response_SubmitJob{
						SubmitJob: &localv1.SubmitJobResponse{Job: submitted},
					}}, nil
				case 2:
					read := request.GetReadJobLogs()
					if read.GetJobId() != string(value.ID) ||
						read.GetDeviceSelector() != "auto" ||
						read.GetAfterSequence() != 0 {
						t.Fatalf("read logs = %#v", read)
					}
					return &localv1.Response{Result: &localv1.Response_ReadJobLogs{
						ReadJobLogs: &localv1.ReadJobLogsResponse{
							Job: succeededMessage,
							Records: []*localv1.JobLogRecord{{
								Sequence: 1,
								Stream:   localv1.JobLogStream_JOB_LOG_STREAM_STDOUT,
								Data:     []byte("worker-host\n"),
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
	command.SetArgs([]string{"smoke"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Submitted smoke test " + string(value.ID) + " to an automatically selected worker (queued)",
		"worker-host\n",
		"Job " + string(value.ID) + " succeeded",
		"Smoke test passed.",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestOutputsCommandUsesSafeDefaultDestinationAndReportsConflicts(t *testing.T) {
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
	command.SetArgs([]string{"outputs", string(value.ID)})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Restored 1 output file(s) to "+wantDestination) ||
		!strings.Contains(stderr.String(), "Kept existing files unchanged") ||
		!strings.Contains(stderr.String(), ".computehop-conflicts") {
		t.Fatalf("stdout = %q; stderr = %q", stdout.String(), stderr.String())
	}
}

func TestArtifactsAliasStillFetchesOutputs(t *testing.T) {
	value := cliJobForTest(job.StateSucceeded)
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return t.TempDir(), nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				fetch := request.GetFetchArtifacts()
				if fetch.GetJobId() != string(value.ID) {
					t.Fatalf("fetch = %#v", fetch)
				}
				return &localv1.Response{Result: &localv1.Response_FetchArtifacts{
					FetchArtifacts: &localv1.FetchArtifactsResponse{
						Destination:       fetch.GetDestination(),
						RestoredFileCount: 1,
					},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"artifacts", string(value.ID)})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Restored 1 output file(s)") {
		t.Fatalf("stdout = %q", stdout.String())
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

func TestJobsCommandEmptyLocalHistoryPrintsNextSteps(t *testing.T) {
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				if request.GetListJobs().GetDeviceSelector() != "" {
					t.Fatalf("device selector = %q", request.GetListJobs().GetDeviceSelector())
				}
				return &localv1.Response{Result: &localv1.Response_ListJobs{
					ListJobs: &localv1.ListJobsResponse{},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"jobs"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"No jobs.",
		"Next:",
		"computehop run hostname",
		"computehop smoke",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestJobsCommandEmptyWorkerHistoryPrintsTargetedNextSteps(t *testing.T) {
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				if request.GetListJobs().GetDeviceSelector() != "Gaming PC" {
					t.Fatalf("device selector = %q", request.GetListJobs().GetDeviceSelector())
				}
				return &localv1.Response{Result: &localv1.Response_ListJobs{
					ListJobs: &localv1.ListJobsResponse{},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"jobs", "--on", "Gaming PC"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"No jobs for Gaming PC.",
		"Next:",
		"computehop smoke --on 'Gaming PC'",
		"computehop run --on 'Gaming PC' --no-project hostname",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestDevicesCommandPrintsNearbyDevicesAsNotConnected(t *testing.T) {
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
	for _, want := range []string{
		"Gaming PC", presenceID.Short(), "not connected", "worker", "192.0.2.20 (discovery only)", "2026-07-19T12:00:00Z",
		"Next:", "computehop connect nearby", "computehop connect confirm",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestDevicesCommandPrintsConnectedAndNearbyEmptyState(t *testing.T) {
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
					},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"devices"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"No connected or nearby devices.",
		"Next:",
		"computehop setup worker --device-name 'Gaming PC'",
		"computehop connect nearby",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestDevicesCommandSuggestsExplicitConnectForMultipleNearbyWorkers(t *testing.T) {
	firstPresence, err := device.NewPresenceID(bytes.NewReader(bytes.Repeat([]byte{30}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	secondPresence, err := device.NewPresenceID(bytes.NewReader(bytes.Repeat([]byte{31}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	seen := time.Date(2026, time.July, 19, 12, 5, 0, 0, time.UTC)
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		getwd:  func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(context.Context, *localv1.Request) (*localv1.Response, error) {
				return &localv1.Response{Result: &localv1.Response_ListDevices{
					ListDevices: &localv1.ListDevicesResponse{
						DiscoveryState: localv1.DiscoveryState_DISCOVERY_STATE_AVAILABLE,
						Devices: []*localv1.NearbyDevice{
							{
								PresenceId: string(firstPresence), Name: "Gaming PC",
								Role: localv1.DeviceRole_DEVICE_ROLE_WORKER, Addresses: []string{"192.0.2.20"},
								LastSeenAtUnixNano: seen.UnixNano(),
								TrustState:         localv1.DeviceTrustState_DEVICE_TRUST_STATE_UNPAIRED,
							},
							{
								PresenceId: string(secondPresence), Name: "Mini PC",
								Role: localv1.DeviceRole_DEVICE_ROLE_WORKER, Addresses: []string{"192.0.2.21"},
								LastSeenAtUnixNano: seen.Add(time.Second).UnixNano(),
								TrustState:         localv1.DeviceTrustState_DEVICE_TRUST_STATE_UNPAIRED,
							},
						},
					},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"devices"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{"Gaming PC", "Mini PC", "Next:", "computehop connect <device>", "NAME or IDENTIFIER"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
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
	for _, want := range []string{identity.ID().Short(), "connected", "nearby", "192.0.2.20:47823", "Next:", "computehop smoke", "computehop run --on auto hostname"} {
		if !strings.Contains(output, want) {
			t.Fatalf("stdout %q does not contain %q", output, want)
		}
	}
	if strings.Contains(output, presenceID.Short()) {
		t.Fatalf("stdout leaked the redundant ephemeral identifier: %q", output)
	}
}

func TestDevicesCommandCollapsesDuplicateNearbyRowsForSingleActivePeer(t *testing.T) {
	identity, err := device.GenerateIdentity(bytes.NewReader(bytes.Repeat([]byte{12}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	pairID, err := trust.NewPairID(bytes.NewReader(bytes.Repeat([]byte{13}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	firstPresence, err := device.NewPresenceID(bytes.NewReader(bytes.Repeat([]byte{14}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	secondPresence, err := device.NewPresenceID(bytes.NewReader(bytes.Repeat([]byte{15}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	seen := time.Date(2026, time.July, 22, 5, 30, 0, 0, time.UTC)
	trusted, err := mapper.TrustedPeerToProto(trust.Peer{
		PairID: pairID, DeviceID: identity.ID(), PublicKey: identity.PublicKey(),
		Name: "Gaming PC", Role: device.RoleWorker, State: trust.StateActive,
		PairedAt: seen.Add(-time.Hour), UpdatedAt: seen.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(context.Context, *localv1.Request) (*localv1.Response, error) {
				return &localv1.Response{Result: &localv1.Response_ListDevices{
					ListDevices: &localv1.ListDevicesResponse{
						DiscoveryState: localv1.DiscoveryState_DISCOVERY_STATE_AVAILABLE,
						TrustedDevices: []*localv1.TrustedDevice{trusted},
						Devices: []*localv1.NearbyDevice{
							{
								PresenceId: string(firstPresence), Name: "Gaming PC",
								Role: localv1.DeviceRole_DEVICE_ROLE_WORKER, Addresses: []string{"192.0.2.20"},
								Port: 47823, EndpointReady: true, LastSeenAtUnixNano: seen.UnixNano(),
								TrustState: localv1.DeviceTrustState_DEVICE_TRUST_STATE_UNPAIRED,
							},
							{
								PresenceId: string(secondPresence), Name: "Gaming PC",
								Role: localv1.DeviceRole_DEVICE_ROLE_WORKER, Addresses: []string{"192.0.2.21"},
								Port: 47823, EndpointReady: true, LastSeenAtUnixNano: seen.Add(time.Second).UnixNano(),
								TrustState: localv1.DeviceTrustState_DEVICE_TRUST_STATE_UNPAIRED,
							},
						},
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
	for _, want := range []string{identity.ID().Short(), "connected", "nearby", "LAN", "2 LAN records"} {
		if !strings.Contains(output, want) {
			t.Fatalf("stdout %q does not contain %q", output, want)
		}
	}
	for _, unwanted := range []string{firstPresence.Short(), secondPresence.Short(), "not connected"} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("stdout contains suppressed duplicate %q: %q", unwanted, output)
		}
	}
}

func TestDevicesCommandShowsRemotePathForOfflineLANPeer(t *testing.T) {
	identity, err := device.GenerateIdentity(bytes.NewReader(bytes.Repeat([]byte{16}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	pairID, err := trust.NewPairID(bytes.NewReader(bytes.Repeat([]byte{17}, 16)))
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
	for _, want := range []string{"Remote PC", "remote", "direct (STUN)", "2026-07-22T07:00:00Z", "computehop smoke"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestDevicesCommandShowsLANOnlyForDisabledRemoteConnectivity(t *testing.T) {
	identity, err := device.GenerateIdentity(bytes.NewReader(bytes.Repeat([]byte{18}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	pairID, err := trust.NewPairID(bytes.NewReader(bytes.Repeat([]byte{19}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	updatedAt := time.Date(2026, time.July, 22, 8, 0, 0, 0, time.UTC)
	trusted, err := mapper.TrustedPeerToProto(trust.Peer{
		PairID: pairID, DeviceID: identity.ID(), PublicKey: identity.PublicKey(),
		Name: "LAN Worker", Role: device.RoleWorker, State: trust.StateActive,
		PairedAt: updatedAt.Add(-time.Hour), UpdatedAt: updatedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	trusted.ConnectivityState = localv1.ConnectivityState_CONNECTIVITY_STATE_DISABLED

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
	for _, want := range []string{"LAN Worker", "offline", "LAN only", "same LAN", "computehop setup worker --device-name 'Gaming PC' --connectivity-domain connect.example.com --turn-domain turn.example.com"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestDisconnectCommandRevokesSelectedDevice(t *testing.T) {
	identity, err := device.GenerateIdentity(bytes.NewReader(bytes.Repeat([]byte{20}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	pairID, err := trust.NewPairID(bytes.NewReader(bytes.Repeat([]byte{21}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	revokedAt := time.Date(2026, time.July, 22, 8, 1, 0, 0, time.UTC)
	trusted, err := mapper.TrustedPeerToProto(trust.Peer{
		PairID: pairID, DeviceID: identity.ID(), PublicKey: identity.PublicKey(),
		Name: "Gaming PC", Role: device.RoleWorker, State: trust.StateRevoked,
		PairedAt:  time.Date(2026, time.July, 22, 8, 0, 0, 0, time.UTC),
		UpdatedAt: revokedAt, RevokedAt: &revokedAt,
	})
	if err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				if got := request.GetUnpairDevice().GetDeviceSelector(); got != "Gaming PC" {
					t.Fatalf("device selector = %q", got)
				}
				return &localv1.Response{Result: &localv1.Response_UnpairDevice{
					UnpairDevice: &localv1.UnpairDeviceResponse{Device: trusted},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"disconnect", "Gaming PC"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Disconnected Gaming PC", identity.ID().Short(), "computehop connect nearby"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestUnpairAliasRemainsCompatible(t *testing.T) {
	identity, err := device.GenerateIdentity(bytes.NewReader(bytes.Repeat([]byte{22}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	pairID, err := trust.NewPairID(bytes.NewReader(bytes.Repeat([]byte{23}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	revokedAt := time.Date(2026, time.July, 22, 8, 1, 0, 0, time.UTC)
	trusted, err := mapper.TrustedPeerToProto(trust.Peer{
		PairID: pairID, DeviceID: identity.ID(), PublicKey: identity.PublicKey(),
		Name: "Mini PC", Role: device.RoleWorker, State: trust.StateRevoked,
		PairedAt:  time.Date(2026, time.July, 22, 8, 0, 0, 0, time.UTC),
		UpdatedAt: revokedAt, RevokedAt: &revokedAt,
	})
	if err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				if got := request.GetUnpairDevice().GetDeviceSelector(); got != "Mini PC" {
					t.Fatalf("device selector = %q", got)
				}
				return &localv1.Response{Result: &localv1.Response_UnpairDevice{
					UnpairDevice: &localv1.UnpairDeviceResponse{Device: trusted},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"unpair", "Mini PC"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Disconnected Mini PC") {
		t.Fatalf("stdout %q does not contain disconnect output", stdout.String())
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
		"computehop smoke --on auto",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestDoctorCommandPointsAtWorkerSetupForMissingWorkers(t *testing.T) {
	identity, err := device.GenerateIdentity(bytes.NewReader(bytes.Repeat([]byte{31}, 64)))
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
		"Print the exact worker install command",
		"computehop setup worker --device-name \"Gaming PC\"",
		"Development-only alternative: go run ./cmd/computehopd --role worker",
		"Then run: computehop devices",
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
		"computehop connect nearby",
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
		"computehop setup orchestrator",
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

func TestDoctorCommandPrintsRestartAdviceWhenDaemonProtocolMismatches(t *testing.T) {
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(context.Context, *localv1.Request) (*localv1.Response, error) {
				return nil, ErrDaemonProtocolMismatch
			}}, nil
		},
	})
	command.SetArgs([]string{"doctor"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"Daemon: running, but not compatible with this CLI",
		"make install-macos",
		"make uninstall-macos",
		"go run ./cmd/computehopd --role orchestrator",
		"computehop doctor",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestSetupCommandPrintsFirstRunChecklistWithoutDaemon(t *testing.T) {
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			t.Fatal("setup should not require a daemon client")
			return nil, nil
		},
	})
	command.SetArgs([]string{"setup"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"ComputeHop setup",
		"computehop setup orchestrator",
		"computehop setup worker --device-name \"Gaming PC\"",
		"Advanced equivalent: computehop setup mac --role worker --device-name \"Gaming PC\"",
		"computehop doctor",
		"Development-only alternative: go run ./cmd/computehopd --role worker",
		"computehop connect nearby",
		"computehop connect <device>",
		"computehop smoke",
		"./deploy/vps/init.sh --connectivity-domain connect.example.com",
		"docker compose --project-directory deploy/vps up -d --build",
		"./deploy/vps/verify.sh",
		"./deploy/vps/turn-credentials.sh",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestSetupHelpShowsRoleAliasesMacAndVPS(t *testing.T) {
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			t.Fatal("setup help should not require a daemon client")
			return nil, nil
		},
	})
	command.SetArgs([]string{"setup", "--help"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"Print first-run commands without requiring the ComputeHop daemon",
		"computehop setup worker --device-name \"Gaming PC\"",
		"computehop setup worker --device-name \"Gaming PC\" --lan-only",
		"computehop setup vps --connectivity-domain connect.example.com",
		"orchestrator",
		"worker",
		"mac",
		"vps",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestSetupSubcommandHelpShowsExamplesWithoutDaemon(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "orchestrator",
			args: []string{"setup", "orchestrator", "--help"},
			want: []string{
				"Print the exact macOS installer command for an orchestrator Mac",
				"computehop setup orchestrator --lan-only",
				"computehop setup orchestrator --connectivity-domain connect.example.com",
			},
		},
		{
			name: "worker",
			args: []string{"setup", "worker", "--help"},
			want: []string{
				"Print the exact macOS installer command for a worker Mac",
				"computehop setup worker --device-name \"Gaming PC\" --cache-size 40GiB",
				"computehop setup worker --device-name \"Gaming PC\" --lan-only",
				"--turn-server \"turn:turn.example.com:3478?transport=udp\"",
			},
		},
		{
			name: "mac",
			args: []string{"setup", "mac", "--help"},
			want: []string{
				"flag-based form of setup orchestrator and setup worker",
				"computehop setup mac --role worker --device-name \"Gaming PC\" --cache-size 40GiB",
				"computehop setup mac --role worker --device-name \"Gaming PC\" --lan-only",
				"--turn-username \"1800000000:computehop\"",
			},
		},
		{
			name: "vps",
			args: []string{"setup", "vps", "--help"},
			want: []string{
				"provider-neutral one-VPS checklist",
				"does not buy or mutate a server",
				"computehop setup vps --connectivity-domain connect.example.com",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			command := newRootCommand(dependencies{
				stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
				newClient: func(string) (caller, error) {
					t.Fatal("setup help should not require a daemon client")
					return nil, nil
				},
			})
			command.SetArgs(test.args)
			if err := command.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			for _, want := range test.want {
				if !strings.Contains(stdout.String(), want) {
					t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
				}
			}
		})
	}
}

func TestSetupMacCommandPrintsDefaultOrchestratorInstallWithoutDaemon(t *testing.T) {
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			t.Fatal("setup mac should not require a daemon client")
			return nil, nil
		},
	})
	command.SetArgs([]string{"setup", "mac"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"ComputeHop macOS setup",
		"computehop setup mac --role orchestrator",
		"make install-macos",
		"computehop doctor",
		"computehop connect nearby",
		"computehop smoke",
		"After buying the VPS",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestSetupMacCommandPrintsWorkerInstallWithoutDaemon(t *testing.T) {
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			t.Fatal("setup mac should not require a daemon client")
			return nil, nil
		},
	})
	command.SetArgs([]string{"setup", "mac", "--role", "worker", "--device-name", "Austin Gaming PC", "--cache-size", "40GiB"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"computehop setup mac --role worker --device-name 'Austin Gaming PC' --cache-size 40GiB",
		"./packaging/macos/install.sh --role worker --device-name 'Austin Gaming PC' --cache-size 40GiB",
		"Connect from your orchestrator Mac",
		"Confirm on this worker",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestSetupRoleAliasesPrintInstallWithoutDaemon(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "orchestrator",
			args: []string{
				"setup", "orchestrator",
				"--connectivity-domain", "connect.computehop.dev",
				"--turn-domain", "turn.computehop.dev",
			},
			want: []string{
				"computehop setup orchestrator --connectivity-domain connect.computehop.dev --turn-domain turn.computehop.dev",
				"./packaging/macos/install.sh --role orchestrator --connectivity-url https://connect.computehop.dev --stun-server stun:turn.computehop.dev:3478",
				"computehop smoke",
			},
		},
		{
			name: "worker",
			args: []string{"setup", "worker", "--device-name", "Austin Gaming PC", "--cache-size", "40GiB"},
			want: []string{
				"computehop setup worker --device-name 'Austin Gaming PC' --cache-size 40GiB",
				"./packaging/macos/install.sh --role worker --device-name 'Austin Gaming PC' --cache-size 40GiB",
				"Confirm on this worker",
				"computehop setup worker --device-name 'Austin Gaming PC' --cache-size 40GiB --connectivity-domain connect.example.com --turn-domain turn.example.com",
			},
		},
		{
			name: "orchestrator lan only",
			args: []string{"setup", "orchestrator", "--lan-only"},
			want: []string{
				"computehop setup orchestrator --lan-only",
				"./packaging/macos/install.sh --role orchestrator --lan-only",
				"computehop setup orchestrator --connectivity-domain connect.example.com --turn-domain turn.example.com",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			command := newRootCommand(dependencies{
				stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
				newClient: func(string) (caller, error) {
					t.Fatal("setup role alias should not require a daemon client")
					return nil, nil
				},
			})
			command.SetArgs(test.args)
			if err := command.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			for _, want := range test.want {
				if !strings.Contains(stdout.String(), want) {
					t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
				}
			}
		})
	}
}

func TestSetupMacCommandInterpolatesVPSConnectivityWithoutDaemon(t *testing.T) {
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			t.Fatal("setup mac should not require a daemon client")
			return nil, nil
		},
	})
	command.SetArgs([]string{
		"setup", "mac",
		"--role", "orchestrator",
		"--connectivity-domain", "connect.computehop.dev",
		"--turn-domain", "turn.computehop.dev",
	})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"./packaging/macos/install.sh --role orchestrator --connectivity-url https://connect.computehop.dev --stun-server stun:turn.computehop.dev:3478",
		"computehop setup mac --role orchestrator --connectivity-domain connect.computehop.dev --turn-domain turn.computehop.dev",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
	if strings.Contains(stdout.String(), "After buying the VPS") {
		t.Fatalf("stdout still prints VPS reminder when connectivity is configured: %q", stdout.String())
	}
}

func TestSetupMacCommandInterpolatesTURNRelayWithoutDaemon(t *testing.T) {
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			t.Fatal("setup mac should not require a daemon client")
			return nil, nil
		},
	})
	command.SetArgs([]string{
		"setup", "mac",
		"--role", "worker",
		"--device-name", "Gaming PC",
		"--connectivity-domain", "connect.computehop.dev",
		"--turn-domain", "turn.computehop.dev",
		"--turn-server", "turn:turn.computehop.dev:3478?transport=udp",
		"--turn-username", "1800000000:computehop",
		"--turn-password", "relay secret",
	})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"computehop setup mac --role worker --device-name 'Gaming PC' --connectivity-domain connect.computehop.dev --turn-domain turn.computehop.dev --turn-server 'turn:turn.computehop.dev:3478?transport=udp' --turn-username 1800000000:computehop --turn-password 'relay secret'",
		"./packaging/macos/install.sh --role worker --device-name 'Gaming PC' --connectivity-url https://connect.computehop.dev --stun-server stun:turn.computehop.dev:3478 --turn-server 'turn:turn.computehop.dev:3478?transport=udp' --turn-username 1800000000:computehop --turn-password 'relay secret'",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestSetupMacCommandRejectsInvalidOptionsBeforeDaemon(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "role", args: []string{"setup", "mac", "--role", "desktop"}, want: "--role must be orchestrator or worker"},
		{name: "connectivity", args: []string{"setup", "mac", "--connectivity-domain", "connect.example.com"}, want: "--connectivity-domain requires --turn-domain or --turn-server"},
		{name: "stun without connectivity", args: []string{"setup", "mac", "--turn-domain", "turn.example.com"}, want: "--connectivity-domain is required"},
		{name: "turn uri", args: []string{"setup", "mac", "--connectivity-domain", "connect.example.com", "--turn-server", "https://turn.example.com", "--turn-username", "u", "--turn-password", "p"}, want: "--turn-server must begin with turn: or turns:"},
		{name: "turn credentials", args: []string{"setup", "mac", "--connectivity-domain", "connect.example.com", "--turn-server", "turn:turn.example.com:3478"}, want: "--turn-server requires --turn-username and --turn-password"},
		{name: "turn username", args: []string{"setup", "mac", "--connectivity-domain", "connect.example.com", "--turn-domain", "turn.example.com", "--turn-username", "u"}, want: "--turn-username and --turn-password require --turn-server"},
		{name: "cache", args: []string{"setup", "mac", "--cache-size", "bad"}, want: "--cache-size"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := newRootCommand(dependencies{
				stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
				newClient: func(string) (caller, error) {
					t.Fatal("setup mac should not require a daemon client")
					return nil, nil
				},
			})
			command.SetArgs(test.args)
			err := command.Execute()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Execute() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestSetupMacCommandInterpolatesLANOnlyWithoutDaemon(t *testing.T) {
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			t.Fatal("setup mac should not require a daemon client")
			return nil, nil
		},
	})
	command.SetArgs([]string{
		"setup", "mac",
		"--role", "worker",
		"--device-name", "Gaming PC",
		"--lan-only",
	})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"computehop setup mac --role worker --device-name 'Gaming PC' --lan-only",
		"./packaging/macos/install.sh --role worker --device-name 'Gaming PC' --lan-only",
		"computehop setup mac --role worker --device-name 'Gaming PC' --connectivity-domain connect.example.com --turn-domain turn.example.com",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestSetupMacCommandRejectsLANOnlyWithConnectivity(t *testing.T) {
	command := newRootCommand(dependencies{
		stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			t.Fatal("setup mac should not require a daemon client")
			return nil, nil
		},
	})
	command.SetArgs([]string{
		"setup", "mac",
		"--role", "orchestrator",
		"--lan-only",
		"--connectivity-domain", "connect.computehop.dev",
		"--turn-domain", "turn.computehop.dev",
	})
	err := command.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	if !strings.Contains(err.Error(), "--lan-only cannot be combined") {
		t.Fatalf("error = %v", err)
	}
}

func TestSetupVPSCommandPrintsDeploymentChecklistWithoutDaemon(t *testing.T) {
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			t.Fatal("setup vps should not require a daemon client")
			return nil, nil
		},
	})
	command.SetArgs([]string{"setup", "vps"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"ComputeHop one-VPS setup",
		"computehop setup vps --connectivity-domain connect.example.com",
		"Ubuntu 24.04 LTS VPS",
		"Budget about $5-10/month",
		"confirm included transfer and IPv4 pricing",
		"connect.example.com -> 203.0.113.10",
		"turn.example.com -> 203.0.113.10",
		"Allow TCP 80/443, UDP 443, TCP/UDP 3478, UDP 49160-49200",
		"ssh root@203.0.113.10",
		"sudo ./deploy/vps/bootstrap-ubuntu.sh",
		"./deploy/vps/init.sh --connectivity-domain connect.example.com",
		"docker compose --project-directory deploy/vps up -d --build",
		"./deploy/vps/turn-credentials.sh",
		"computehop setup orchestrator --connectivity-domain connect.example.com --turn-domain turn.example.com",
		"computehop setup worker --device-name 'Gaming PC' --connectivity-domain connect.example.com --turn-domain turn.example.com",
		"./packaging/macos/install.sh --role worker",
		"--turn-server",
		"computehop smoke",
		"operator-provisioned TURN relay testing",
		"Public production relay still needs server-verifiable entitlement",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestSetupVPSCommandInterpolatesProvidedValuesWithoutDaemon(t *testing.T) {
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			t.Fatal("setup vps should not require a daemon client")
			return nil, nil
		},
	})
	command.SetArgs([]string{
		"setup", "vps",
		"--connectivity-domain", "connect.computehop.dev",
		"--turn-domain", "turn.computehop.dev",
		"--email", "ops@computehop.dev",
		"--public-ip", "198.51.100.25",
	})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"connect.computehop.dev -> 198.51.100.25",
		"turn.computehop.dev -> 198.51.100.25",
		"./deploy/vps/init.sh --connectivity-domain connect.computehop.dev --turn-domain turn.computehop.dev --email ops@computehop.dev --public-ip 198.51.100.25",
		"computehop setup orchestrator --connectivity-domain connect.computehop.dev --turn-domain turn.computehop.dev",
		"computehop setup worker --device-name 'Gaming PC' --connectivity-domain connect.computehop.dev --turn-domain turn.computehop.dev",
		"--connectivity-url https://connect.computehop.dev",
		"--stun-server stun:turn.computehop.dev:3478",
		"./deploy/vps/turn-credentials.sh",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
	if strings.Contains(stdout.String(), "connect.example.com ->") {
		t.Fatalf("stdout still contains default connectivity domain: %q", stdout.String())
	}
}

func TestRootHelpShowsSetupAndConnectButHidesLegacyPair(t *testing.T) {
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			t.Fatal("help should not require a daemon client")
			return nil, nil
		},
	})
	command.SetArgs([]string{"--help"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := stdout.String()
	for _, want := range []string{"setup", "connect", "disconnect", "smoke"} {
		if !strings.Contains(output, want) {
			t.Fatalf("help %q does not contain %q", output, want)
		}
	}
	if strings.Contains(output, "pair [device]") || strings.Contains(output, "\n  pair ") ||
		strings.Contains(output, "unpair") {
		t.Fatalf("help exposes legacy pair command: %q", output)
	}
}

func TestCoreCommandHelpShowsFriendlyExamplesWithoutDaemon(t *testing.T) {
	for _, testCase := range []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "devices",
			args: []string{"devices", "--help"},
			want: []string{
				"List devices ComputeHop knows about.",
				"CONNECTION column",
				"LAN only",
				"prints the next command to try",
				"computehop connect nearby",
				"computehop disconnect \"Gaming PC\"",
			},
		},
		{
			name: "status",
			args: []string{"status", "--help"},
			want: []string{
				"Check whether the local computehopd daemon is reachable.",
				"name, role, and short device ID",
				"computehop status",
				"computehop doctor",
			},
		},
		{
			name: "connect",
			args: []string{"connect", "--help"},
			want: []string{
				"connect [nearby|device]",
				"computehop connect nearby",
				"computehop connect confirm",
				"nearby unpaired worker",
			},
		},
		{
			name: "disconnect",
			args: []string{"disconnect", "--help"},
			want: []string{
				"disconnect <device>",
				"Disconnect revokes this computer's saved trust",
				"computehop devices",
				"computehop disconnect \"Gaming PC\"",
			},
		},
		{
			name: "jobs",
			args: []string{"jobs", "--help"},
			want: []string{
				"List recent durable jobs known to this daemon.",
				"computehop jobs --on auto",
				"computehop jobs --on \"Gaming PC\" --limit 25",
				"--limit uint32",
			},
		},
		{
			name: "run",
			args: []string{"run", "--help"},
			want: []string{
				"run [--on auto|device]",
				"computehop run --on auto cargo build --release",
				"computehop run --on auto --no-project hostname",
				"single active paired worker",
				"--on string",
			},
		},
		{
			name: "smoke",
			args: []string{"smoke", "--help"},
			want: []string{
				"Run a cheap remote connectivity smoke test",
				"computehop smoke --on \"Gaming PC\"",
				"without uploading a project",
			},
		},
		{
			name: "cancel",
			args: []string{"cancel", "--help"},
			want: []string{
				"Request cancellation for a queued or running durable job.",
				"routes job-specific commands by job ID",
				"computehop cancel --on \"Gaming PC\" <job-id>",
			},
		},
		{
			name: "logs",
			args: []string{"logs", "--help"},
			want: []string{
				"Read durable stdout and stderr for a job.",
				"computehop logs --follow <job-id>",
				"infers the worker from the job ID",
			},
		},
		{
			name: "outputs",
			args: []string{"outputs", "--help"},
			want: []string{
				"Download declared outputs",
				"computehop outputs <job-id> --to ./results",
				"infers the worker from the job ID",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout bytes.Buffer
			command := newRootCommand(dependencies{
				stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
				newClient: func(string) (caller, error) {
					t.Fatal("help should not require a daemon client")
					return nil, nil
				},
			})
			command.SetArgs(testCase.args)
			if err := command.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			output := stdout.String()
			for _, want := range testCase.want {
				if !strings.Contains(output, want) {
					t.Fatalf("help %q does not contain %q", output, want)
				}
			}
		})
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
		"computehop connect nearby",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestConnectNearbyBeginsPairingWithOnlyNearbyWorker(t *testing.T) {
	presenceID, err := device.NewPresenceID(bytes.NewReader(bytes.Repeat([]byte{23}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	seen := time.Date(2026, time.July, 22, 11, 30, 0, 0, time.UTC)
	value := cliPairingForTest(t)
	message, err := mapper.PairingToProto(value)
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
					if request.GetListDevices() == nil {
						t.Fatalf("first request = %#v", request)
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
				case 2:
					begin := request.GetBeginPairing()
					if begin == nil {
						t.Fatalf("second request = %#v", request)
					}
					if got := begin.GetDeviceSelector(); got != presenceID.Short() {
						t.Fatalf("device selector = %q", got)
					}
					return &localv1.Response{Result: &localv1.Response_BeginPairing{
						BeginPairing: &localv1.BeginPairingResponse{Pairing: message},
					}}, nil
				default:
					t.Fatalf("unexpected call %d", calls)
					return nil, nil
				}
			}}, nil
		},
	})
	command.SetArgs([]string{"connect", "nearby"})
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
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestConnectAutoRemainsCompatibilityAliasForNearby(t *testing.T) {
	for _, selector := range []string{"auto", "nearby", " NEARBY "} {
		if !isConnectAutoSelector(selector) {
			t.Fatalf("isConnectAutoSelector(%q) = false", selector)
		}
	}
	if isConnectAutoSelector("Gaming PC") {
		t.Fatal("explicit device selector was treated as automatic nearby selector")
	}
}

func TestConnectAutoIgnoresNearbyPresenceMatchingActiveWorker(t *testing.T) {
	identity, err := device.GenerateIdentity(bytes.NewReader(bytes.Repeat([]byte{24}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	pairID, err := trust.NewPairID(bytes.NewReader(bytes.Repeat([]byte{25}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	presenceID, err := device.NewPresenceID(bytes.NewReader(bytes.Repeat([]byte{26}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	seen := time.Date(2026, time.July, 22, 11, 45, 0, 0, time.UTC)
	trusted, err := mapper.TrustedPeerToProto(trust.Peer{
		PairID: pairID, DeviceID: identity.ID(), PublicKey: identity.PublicKey(),
		Name: "Gaming PC", Role: device.RoleWorker, State: trust.StateActive,
		PairedAt: seen.Add(-time.Hour), UpdatedAt: seen.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	var calls int
	command := newRootCommand(dependencies{
		stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				calls++
				if request.GetListDevices() == nil {
					t.Fatalf("request = %#v", request)
				}
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
	command.SetArgs([]string{"connect", "auto"})
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "no nearby unpaired worker found") {
		t.Fatalf("Execute() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestConnectAutoRejectsNoNearbyWorkers(t *testing.T) {
	var calls int
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				calls++
				if calls != 1 || request.GetListDevices() == nil {
					t.Fatalf("request %d = %#v", calls, request)
				}
				return &localv1.Response{Result: &localv1.Response_ListDevices{
					ListDevices: &localv1.ListDevicesResponse{
						DiscoveryState: localv1.DiscoveryState_DISCOVERY_STATE_AVAILABLE,
					},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"connect", "auto"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "no nearby unpaired worker found") {
		t.Fatalf("Execute() error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestConnectAutoRejectsMultipleNearbyWorkers(t *testing.T) {
	firstPresenceID, err := device.NewPresenceID(bytes.NewReader(bytes.Repeat([]byte{24}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	secondPresenceID, err := device.NewPresenceID(bytes.NewReader(bytes.Repeat([]byte{25}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	seen := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	var calls int
	var stdout bytes.Buffer
	command := newRootCommand(dependencies{
		stdout: &stdout, stderr: &bytes.Buffer{}, getwd: func() (string, error) { return "", nil },
		newClient: func(string) (caller, error) {
			return stubCaller{call: func(_ context.Context, request *localv1.Request) (*localv1.Response, error) {
				calls++
				if calls != 1 || request.GetListDevices() == nil {
					t.Fatalf("request %d = %#v", calls, request)
				}
				return &localv1.Response{Result: &localv1.Response_ListDevices{
					ListDevices: &localv1.ListDevicesResponse{
						DiscoveryState: localv1.DiscoveryState_DISCOVERY_STATE_AVAILABLE,
						Devices: []*localv1.NearbyDevice{
							{
								PresenceId: string(firstPresenceID), Name: "Gaming PC",
								Role:      localv1.DeviceRole_DEVICE_ROLE_WORKER,
								Addresses: []string{"192.0.2.20"}, Port: 47823,
								LastSeenAtUnixNano: seen.UnixNano(),
								TrustState:         localv1.DeviceTrustState_DEVICE_TRUST_STATE_UNPAIRED,
							},
							{
								PresenceId: string(secondPresenceID), Name: "Mini PC",
								Role:      localv1.DeviceRole_DEVICE_ROLE_WORKER,
								Addresses: []string{"192.0.2.21"}, Port: 47823,
								LastSeenAtUnixNano: seen.UnixNano(),
								TrustState:         localv1.DeviceTrustState_DEVICE_TRUST_STATE_UNPAIRED,
							},
						},
					},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"connect", "auto"})
	err = command.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	for _, want := range []string{"more than one nearby unpaired worker found", "Gaming PC", "Mini PC", "computehop connect <device>"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err.Error(), want)
		}
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
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
	want := "Confirmed Gaming PC on this device.\nFinish on the other device with: computehop connect confirm\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q", got)
	}
}

func TestConnectConfirmPrintsConnectedWhenBothDevicesConfirmed(t *testing.T) {
	value := cliPairingForTest(t)
	waiting, err := mapper.PairingToProto(value)
	if err != nil {
		t.Fatal(err)
	}
	pairedValue := value
	pairedValue.LocalConfirmed = true
	pairedValue.RemoteConfirmed = true
	pairedValue.State = trust.PairingPaired
	paired, err := mapper.PairingToProto(pairedValue)
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
					return &localv1.Response{Result: &localv1.Response_ListPairings{
						ListPairings: &localv1.ListPairingsResponse{Pairings: []*localv1.Pairing{waiting}},
					}}, nil
				case 2:
					return &localv1.Response{Result: &localv1.Response_ConfirmPairing{
						ConfirmPairing: &localv1.ConfirmPairingResponse{Pairing: paired},
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
	if got := stdout.String(); got != "Connected to Gaming PC.\n" {
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

func TestLogsCommandExplainsTerminalJobWithNoOutput(t *testing.T) {
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
					ReadJobLogs: &localv1.ReadJobLogsResponse{Job: message},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"logs", string(value.ID)})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if want := "No output captured for " + string(value.ID) + "."; !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr %q does not contain %q", stderr.String(), want)
	}
}

func TestLogsCommandExplainsRunningJobWithNoOutputYet(t *testing.T) {
	value := cliJobForTest(job.StateRunning)
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
					ReadJobLogs: &localv1.ReadJobLogsResponse{Job: message},
				}}, nil
			}}, nil
		},
	})
	command.SetArgs([]string{"logs", string(value.ID)})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	for _, want := range []string{
		"No output captured yet for " + string(value.ID) + " (running).",
		"computehop logs --follow " + string(value.ID),
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr %q does not contain %q", stderr.String(), want)
		}
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
