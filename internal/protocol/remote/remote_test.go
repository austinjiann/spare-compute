package remote

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	computehopv1 "github.com/austinjiann/spare-compute/gen/go/computehop/v1"
)

func TestCallAndServeCorrelateOneOperation(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	served := make(chan struct{})
	go func() {
		Serve(context.Background(), server, HandlerFunc(func(
			_ context.Context,
			request *computehopv1.RemoteRequest,
		) *computehopv1.RemoteResponse {
			if request.GetGetJob().GetJobId() != "job-id" {
				t.Errorf("job ID = %q", request.GetGetJob().GetJobId())
			}
			return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_GetJob{
				GetJob: &computehopv1.GetJobResponse{Job: validRemoteTestJob("job-id")},
			}}
		}))
		close(served)
	}()

	response, err := Call(context.Background(), client, &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_GetJob{GetJob: &computehopv1.GetJobRequest{JobId: "job-id"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.GetGetJob().GetJob().GetId() != "job-id" {
		t.Fatalf("response = %#v", response)
	}
	<-served
}

func validRemoteTestJob(id string) *computehopv1.Job {
	return &computehopv1.Job{
		Id: id,
		Spec: &computehopv1.JobSpec{
			Executable: "echo", Executor: computehopv1.Executor_EXECUTOR_NATIVE,
		},
		State:             computehopv1.JobState_JOB_STATE_QUEUED,
		CreatedAtUnixNano: 1,
		UpdatedAtUnixNano: 2,
	}
}

func TestCallReturnsStructuredWorkerError(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go Serve(context.Background(), server, HandlerFunc(func(
		context.Context,
		*computehopv1.RemoteRequest,
	) *computehopv1.RemoteResponse {
		return &computehopv1.RemoteResponse{Error: &computehopv1.RemoteError{
			Code: computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_NOT_FOUND, Message: "missing",
		}}
	}))

	_, err := Call(context.Background(), client, &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_GetJob{GetJob: &computehopv1.GetJobRequest{JobId: "job-id"}},
	})
	failure, ok := err.(*Error)
	if !ok || failure.Code != computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_NOT_FOUND ||
		failure.Message != "missing" {
		t.Fatalf("error = %#v", err)
	}
}

func TestCallerDisconnectCancelsWorkerRequestContext(t *testing.T) {
	client, server := net.Pipe()
	started := make(chan struct{})
	cancelled := make(chan struct{})
	go Serve(context.Background(), server, HandlerFunc(func(
		ctx context.Context,
		_ *computehopv1.RemoteRequest,
	) *computehopv1.RemoteResponse {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return &computehopv1.RemoteResponse{Error: &computehopv1.RemoteError{
			Code: computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INTERNAL, Message: "cancelled",
		}}
	}))
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := Call(ctx, client, &computehopv1.RemoteRequest{
			Operation: &computehopv1.RemoteRequest_GetJob{GetJob: &computehopv1.GetJobRequest{JobId: "job-id"}},
		})
		result <- err
	}()
	<-started
	cancel()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("worker request context survived caller disconnect")
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Call() error = %v, want context.Canceled", err)
	}
	_ = server.Close()
}

func TestSnapshotOperationsRejectUnknownWireFields(t *testing.T) {
	check := &computehopv1.RemoteRequest{Operation: &computehopv1.RemoteRequest_CheckSnapshot{
		CheckSnapshot: &computehopv1.CheckSnapshotRequest{ManifestId: "manifest", ChunkDigests: []string{"chunk"}},
	}}
	put := &computehopv1.RemoteRequest{Operation: &computehopv1.RemoteRequest_PutChunk{
		PutChunk: &computehopv1.PutChunkRequest{Digest: "chunk", Data: []byte("data")},
	}}
	if hasUnknownRequestFields(check) || hasUnknownRequestFields(put) {
		t.Fatal("known snapshot operation was rejected")
	}

	chunk := &computehopv1.SnapshotChunk{Digest: "chunk", Size: 4}
	chunk.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 0x01})
	submit := &computehopv1.RemoteRequest{Operation: &computehopv1.RemoteRequest_SubmitJob{
		SubmitJob: &computehopv1.SubmitJobRequest{
			Spec: &computehopv1.JobSpec{Executable: "echo", Executor: computehopv1.Executor_EXECUTOR_NATIVE},
			Snapshot: &computehopv1.SnapshotManifest{Files: []*computehopv1.SnapshotFile{{
				Path: "file", Chunks: []*computehopv1.SnapshotChunk{chunk},
			}}},
		},
	}}
	if !hasUnknownRequestFields(submit) {
		t.Fatal("unknown nested snapshot field was accepted")
	}
}

func TestSnapshotOperationsReceiveBoundedExtendedTimeouts(t *testing.T) {
	requests := []struct {
		request *computehopv1.RemoteRequest
		want    time.Duration
	}{
		{
			request: &computehopv1.RemoteRequest{Operation: &computehopv1.RemoteRequest_GetJob{
				GetJob: &computehopv1.GetJobRequest{},
			}},
			want: defaultCallTimeout,
		},
		{
			request: &computehopv1.RemoteRequest{Operation: &computehopv1.RemoteRequest_CheckSnapshot{
				CheckSnapshot: &computehopv1.CheckSnapshotRequest{},
			}},
			want: preflightCallTimeout,
		},
		{
			request: &computehopv1.RemoteRequest{Operation: &computehopv1.RemoteRequest_PutChunk{
				PutChunk: &computehopv1.PutChunkRequest{},
			}},
			want: chunkCallTimeout,
		},
		{
			request: &computehopv1.RemoteRequest{Operation: &computehopv1.RemoteRequest_SubmitJob{
				SubmitJob: &computehopv1.SubmitJobRequest{Snapshot: &computehopv1.SnapshotManifest{}},
			}},
			want: snapshotSubmitTimeout,
		},
	}
	for _, test := range requests {
		if got := operationTimeout(test.request); got != test.want {
			t.Fatalf("operationTimeout(%T) = %v, want %v", test.request.GetOperation(), got, test.want)
		}
	}
}

func FuzzRemoteFrameDecoder(f *testing.F) {
	valid := &computehopv1.RemoteRequest{
		ProtocolVersion: ProtocolVersion,
		RequestId:       "request",
		Operation: &computehopv1.RemoteRequest_GetJob{
			GetJob: &computehopv1.GetJobRequest{JobId: "job"},
		},
	}
	var framed bytes.Buffer
	if err := writeMessage(&framed, valid); err != nil {
		f.Fatal(err)
	}
	f.Add(framed.Bytes())
	f.Add([]byte{})
	f.Add([]byte{0, 0, 0, 1, 0xff})
	f.Fuzz(func(t *testing.T, contents []byte) {
		message := new(computehopv1.RemoteRequest)
		_ = readMessage(bytes.NewReader(contents), message)
		if proto.Size(message) > maximumFrameBytes {
			t.Fatal("decoded message exceeded frame limit")
		}
	})
}
