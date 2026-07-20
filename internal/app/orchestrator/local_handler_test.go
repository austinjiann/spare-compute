package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	localv1 "github.com/austinjiann/spare-compute/gen/go/computehop/local/v1"
	"github.com/austinjiann/spare-compute/internal/app/worker"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/job"
	"github.com/austinjiann/spare-compute/internal/trust"
)

type stubJobController struct {
	submit func(context.Context, job.Spec) (job.Job, error)
	get    func(context.Context, job.ID) (job.Job, error)
	list   func(context.Context, job.ListOptions) ([]job.Job, error)
	cancel func(context.Context, job.ID) (job.Job, error)
	logs   func(context.Context, job.ID, uint64, int) (worker.JobLogs, error)
}

func (stub stubJobController) Submit(ctx context.Context, spec job.Spec) (job.Job, error) {
	return stub.submit(ctx, spec)
}

func (stub stubJobController) Get(ctx context.Context, id job.ID) (job.Job, error) {
	return stub.get(ctx, id)
}

func (stub stubJobController) List(ctx context.Context, options job.ListOptions) ([]job.Job, error) {
	return stub.list(ctx, options)
}

func (stub stubJobController) Cancel(ctx context.Context, id job.ID) (job.Job, error) {
	return stub.cancel(ctx, id)
}

func (stub stubJobController) ReadLogs(
	ctx context.Context,
	id job.ID,
	after uint64,
	limit int,
) (worker.JobLogs, error) {
	return stub.logs(ctx, id, after, limit)
}

func TestLocalHandlerPing(t *testing.T) {
	handler := newHandlerForTest(t, stubJobController{})
	response := handler.Handle(context.Background(), &localv1.Request{
		Operation: &localv1.Request_Ping{Ping: &localv1.PingRequest{}},
	})
	if got := response.GetPing().GetDaemonVersion(); got != "test-version" {
		t.Fatalf("daemon version = %q, want test-version", got)
	}
}

func TestLocalHandlerListsDiscoveryHealth(t *testing.T) {
	handler, err := NewLocalHandler(stubJobController{}, stubDeviceController{
		list: func(context.Context) (device.DiscoverySnapshot, error) {
			return device.DiscoverySnapshot{Available: true}, nil
		},
	}, stubPairingController{}, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	response := handler.Handle(context.Background(), &localv1.Request{
		Operation: &localv1.Request_ListDevices{ListDevices: &localv1.ListDevicesRequest{}},
	})
	if response.GetError() != nil {
		t.Fatalf("response error = %v", response.GetError())
	}
	if got := response.GetListDevices().GetDiscoveryState(); got != localv1.DiscoveryState_DISCOVERY_STATE_AVAILABLE {
		t.Fatalf("discovery state = %v", got)
	}
}

func TestLocalHandlerSubmit(t *testing.T) {
	wantJob := queuedJobForTest()
	controller := stubJobController{
		submit: func(_ context.Context, spec job.Spec) (job.Job, error) {
			if spec.Executable != "echo" || len(spec.Arguments) != 1 || spec.Arguments[0] != "hello" {
				t.Fatalf("submitted spec = %#v", spec)
			}
			return wantJob, nil
		},
	}
	handler := newHandlerForTest(t, controller)
	response := handler.Handle(context.Background(), &localv1.Request{
		Operation: &localv1.Request_SubmitJob{SubmitJob: &localv1.SubmitJobRequest{
			Spec: &localv1.JobSpec{
				Executable: "echo",
				Arguments:  []string{"hello"},
				Executor:   localv1.Executor_EXECUTOR_NATIVE,
			},
		}},
	})
	if response.GetError() != nil {
		t.Fatalf("response error = %v", response.GetError())
	}
	if got := response.GetSubmitJob().GetJob().GetId(); got != string(wantJob.ID) {
		t.Fatalf("submitted job ID = %q, want %q", got, wantJob.ID)
	}
}

func TestLocalHandlerBeginsPairingThroughDedicatedController(t *testing.T) {
	value := pairingForHandlerTest(t)
	handler, err := NewLocalHandler(
		stubJobController{}, stubDeviceController{},
		stubPairingController{begin: func(_ context.Context, selector string) (trust.Pairing, error) {
			if selector != "Gaming PC" {
				t.Fatalf("selector = %q", selector)
			}
			return value, nil
		}},
		"test-version",
	)
	if err != nil {
		t.Fatal(err)
	}
	response := handler.Handle(context.Background(), &localv1.Request{
		Operation: &localv1.Request_BeginPairing{BeginPairing: &localv1.BeginPairingRequest{
			DeviceSelector: "Gaming PC",
		}},
	})
	if response.GetError() != nil || response.GetBeginPairing().GetPairing().GetId() != string(value.ID) {
		t.Fatalf("response = %#v", response)
	}
}

func TestLocalHandlerMapsErrors(t *testing.T) {
	controller := stubJobController{
		get: func(context.Context, job.ID) (job.Job, error) {
			return job.Job{}, job.ErrNotFound
		},
	}
	handler := newHandlerForTest(t, controller)

	invalid := handler.Handle(context.Background(), &localv1.Request{
		Operation: &localv1.Request_GetJob{GetJob: &localv1.GetJobRequest{JobId: "bad"}},
	})
	if got := invalid.GetError().GetCode(); got != localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT {
		t.Fatalf("invalid ID code = %v", got)
	}

	missing := handler.Handle(context.Background(), &localv1.Request{
		Operation: &localv1.Request_GetJob{GetJob: &localv1.GetJobRequest{
			JobId: "7a338fa3-7ba4-4c54-bf59-da1161f6b76f",
		}},
	})
	if got := missing.GetError().GetCode(); got != localv1.ErrorCode_ERROR_CODE_NOT_FOUND {
		t.Fatalf("not found code = %v", got)
	}

	controller.get = func(context.Context, job.ID) (job.Job, error) {
		return job.Job{}, errors.New("database unavailable")
	}
	handler = newHandlerForTest(t, controller)
	internal := handler.Handle(context.Background(), &localv1.Request{
		Operation: &localv1.Request_GetJob{GetJob: &localv1.GetJobRequest{
			JobId: "7a338fa3-7ba4-4c54-bf59-da1161f6b76f",
		}},
	})
	if internal.GetError().GetMessage() != "internal daemon error" {
		t.Fatalf("internal message = %q", internal.GetError().GetMessage())
	}
}

func TestLocalHandlerRejectsOversizedList(t *testing.T) {
	handler := newHandlerForTest(t, stubJobController{})
	response := handler.Handle(context.Background(), &localv1.Request{
		Operation: &localv1.Request_ListJobs{ListJobs: &localv1.ListJobsRequest{Limit: 501}},
	})
	if got := response.GetError().GetCode(); got != localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT {
		t.Fatalf("oversized list code = %v", got)
	}
}

func newHandlerForTest(t *testing.T, controller JobController) *LocalHandler {
	t.Helper()
	handler, err := NewLocalHandler(controller, stubDeviceController{}, stubPairingController{}, "test-version")
	if err != nil {
		t.Fatalf("NewLocalHandler() error = %v", err)
	}
	return handler
}

type stubPairingController struct {
	begin   func(context.Context, string) (trust.Pairing, error)
	trusted func(context.Context) ([]trust.Peer, error)
}

func (stub stubPairingController) Begin(ctx context.Context, selector string) (trust.Pairing, error) {
	if stub.begin != nil {
		return stub.begin(ctx, selector)
	}
	return trust.Pairing{}, nil
}
func (stubPairingController) ListPairings(context.Context) ([]trust.Pairing, error) {
	return nil, nil
}
func (stubPairingController) Confirm(context.Context, string) (trust.Pairing, error) {
	return trust.Pairing{}, nil
}
func (stubPairingController) Reject(context.Context, string) (trust.Pairing, error) {
	return trust.Pairing{}, nil
}

func (stub stubPairingController) ListTrusted(ctx context.Context) ([]trust.Peer, error) {
	if stub.trusted != nil {
		return stub.trusted(ctx)
	}
	return nil, nil
}

func pairingForHandlerTest(t *testing.T) trust.Pairing {
	t.Helper()
	identity, err := device.GenerateIdentity(bytes.NewReader(bytes.Repeat([]byte{3}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	pairID, err := trust.NewPairID(bytes.NewReader(bytes.Repeat([]byte{4}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	return trust.Pairing{
		ID: pairID, PeerID: identity.ID(), PeerPublicKey: identity.PublicKey(),
		PeerName: "Gaming PC", PeerRole: device.RoleWorker,
		Verification: "0123-4567-89AB-CDEF", Direction: trust.DirectionOutbound,
		State: trust.PairingWaiting, StartedAt: now, ExpiresAt: now.Add(time.Minute),
	}
}
func (stubPairingController) Unpair(context.Context, string) (trust.Peer, error) {
	return trust.Peer{}, nil
}

type stubDeviceController struct {
	list func(context.Context) (device.DiscoverySnapshot, error)
}

func (stub stubDeviceController) ListNearby(ctx context.Context) (device.DiscoverySnapshot, error) {
	if stub.list == nil {
		return device.DiscoverySnapshot{}, nil
	}
	return stub.list(ctx)
}

func queuedJobForTest() job.Job {
	now := time.Unix(1_700_000_000, 0).UTC()
	return job.Job{
		ID: "7a338fa3-7ba4-4c54-bf59-da1161f6b76f",
		Spec: job.Spec{
			Executable: "echo",
			Arguments:  []string{"hello"},
			Executor:   job.ExecutorNative,
		},
		State:     job.StateQueued,
		CreatedAt: now,
		UpdatedAt: now.Add(time.Second),
	}
}
