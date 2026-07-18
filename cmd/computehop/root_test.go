package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	localv1 "github.com/austinjiann/spare-compute/gen/go/computehop/local/v1"
	"github.com/austinjiann/spare-compute/internal/job"
	"github.com/austinjiann/spare-compute/internal/protocol/mapper"
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
	command.SetArgs([]string{"run", "--", "cargo", "build", "--release"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := stdout.String(); got != "Submitted "+string(value.ID)+" (queued)\n" {
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
