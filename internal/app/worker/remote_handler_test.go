package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	computehopv1 "github.com/austinjiann/spare-compute/gen/go/computehop/v1"
	"github.com/austinjiann/spare-compute/internal/job"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
)

func TestRemoteHandlerSubmitsValidatedJob(t *testing.T) {
	want := remoteHandlerJob(t)
	controller := remoteControllerStub{submit: func(_ context.Context, spec job.Spec) (job.Job, error) {
		if spec.Executable != "echo" || len(spec.Arguments) != 1 || spec.Arguments[0] != "hello" ||
			spec.WorkingDirectory != "/worker/project" {
			t.Fatalf("spec = %#v", spec)
		}
		return want, nil
	}}
	handler, err := NewRemoteHandler(controller)
	if err != nil {
		t.Fatal(err)
	}
	response := handler.Handle(context.Background(), &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_SubmitJob{SubmitJob: &computehopv1.SubmitJobRequest{
			Spec: &computehopv1.JobSpec{
				Executable: "echo", Arguments: []string{"hello"}, WorkingDirectory: "/worker/project",
				Executor: computehopv1.Executor_EXECUTOR_NATIVE,
			},
		}},
	})
	if response.GetError() != nil || response.GetSubmitJob().GetJob().GetId() != string(want.ID) {
		t.Fatalf("response = %#v", response)
	}
}

func TestRemoteHandlerReturnsReconnectableLogs(t *testing.T) {
	want := remoteHandlerJob(t)
	controller := remoteControllerStub{logs: func(
		_ context.Context,
		id job.ID,
		after uint64,
		limit int,
	) (JobLogs, error) {
		if id != want.ID || after != 4 || limit != 12 {
			t.Fatalf("read logs = %s, %d, %d", id, after, limit)
		}
		return JobLogs{Job: want, Page: joblogging.Page{
			Records: []joblogging.Record{{
				Sequence: 5, Stream: joblogging.StreamStdout, Data: []byte("hello\n"), At: want.UpdatedAt,
			}},
		}}, nil
	}}
	handler, err := NewRemoteHandler(controller)
	if err != nil {
		t.Fatal(err)
	}
	response := handler.Handle(context.Background(), &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_ReadJobLogs{ReadJobLogs: &computehopv1.ReadJobLogsRequest{
			JobId: string(want.ID), AfterSequence: 4, Limit: 12,
		}},
	})
	result := response.GetReadJobLogs()
	if response.GetError() != nil || len(result.GetRecords()) != 1 ||
		result.GetRecords()[0].GetSequence() != 5 || string(result.GetRecords()[0].GetData()) != "hello\n" {
		t.Fatalf("response = %#v", response)
	}
}

func TestRemoteHandlerDoesNotLeakInternalErrors(t *testing.T) {
	controller := remoteControllerStub{get: func(context.Context, job.ID) (job.Job, error) {
		return job.Job{}, errors.New("database password was here")
	}}
	handler, err := NewRemoteHandler(controller)
	if err != nil {
		t.Fatal(err)
	}
	response := handler.Handle(context.Background(), &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_GetJob{GetJob: &computehopv1.GetJobRequest{
			JobId: string(mustJobID(t, 99)),
		}},
	})
	if response.GetError().GetCode() != computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INTERNAL ||
		response.GetError().GetMessage() != "internal worker error" {
		t.Fatalf("error = %#v", response.GetError())
	}
}

type remoteControllerStub struct {
	submit func(context.Context, job.Spec) (job.Job, error)
	get    func(context.Context, job.ID) (job.Job, error)
	list   func(context.Context, job.ListOptions) ([]job.Job, error)
	cancel func(context.Context, job.ID) (job.Job, error)
	logs   func(context.Context, job.ID, uint64, int) (JobLogs, error)
}

func (stub remoteControllerStub) Submit(ctx context.Context, spec job.Spec) (job.Job, error) {
	return stub.submit(ctx, spec)
}

func (stub remoteControllerStub) Get(ctx context.Context, id job.ID) (job.Job, error) {
	return stub.get(ctx, id)
}

func (stub remoteControllerStub) List(ctx context.Context, options job.ListOptions) ([]job.Job, error) {
	return stub.list(ctx, options)
}

func (stub remoteControllerStub) Cancel(ctx context.Context, id job.ID) (job.Job, error) {
	return stub.cancel(ctx, id)
}

func (stub remoteControllerStub) ReadLogs(
	ctx context.Context,
	id job.ID,
	after uint64,
	limit int,
) (JobLogs, error) {
	return stub.logs(ctx, id, after, limit)
}

func remoteHandlerJob(t *testing.T) job.Job {
	t.Helper()
	now := time.Unix(1_700_000_000, 0).UTC()
	return job.Job{
		ID: mustJobID(t, 42),
		Spec: job.Spec{
			Executable: "echo", Arguments: []string{"hello"}, WorkingDirectory: "/worker/project",
			Executor: job.ExecutorNative,
		},
		State: job.StateQueued, CreatedAt: now, UpdatedAt: now.Add(time.Second),
	}
}
