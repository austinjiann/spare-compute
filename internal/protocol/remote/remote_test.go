package remote

import (
	"bytes"
	"context"
	"net"
	"testing"

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
