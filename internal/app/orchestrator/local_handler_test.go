package orchestrator

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
	"github.com/austinjiann/spare-compute/internal/app/worker"
	"github.com/austinjiann/spare-compute/internal/artifact"
	"github.com/austinjiann/spare-compute/internal/connectivity/remoteconn"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/job"
	"github.com/austinjiann/spare-compute/internal/trust"
)

type stubJobController struct {
	submit  func(context.Context, job.Spec) (job.Job, error)
	get     func(context.Context, job.ID) (job.Job, error)
	list    func(context.Context, job.ListOptions) ([]job.Job, error)
	cancel  func(context.Context, job.ID) (job.Job, error)
	logs    func(context.Context, job.ID, uint64, int) (worker.JobLogs, error)
	restore func(context.Context, job.ID, string) (artifact.RestoreResult, error)
}

func (stub stubJobController) RestoreArtifacts(
	ctx context.Context,
	id job.ID,
	destination string,
) (artifact.RestoreResult, error) {
	if stub.restore == nil {
		return artifact.RestoreResult{}, worker.ErrArtifactsDisabled
	}
	return stub.restore(ctx, id, destination)
}

func (stub stubJobController) Submit(ctx context.Context, spec job.Spec) (job.Job, error) {
	return stub.submit(ctx, spec)
}

func (stub stubJobController) Get(ctx context.Context, id job.ID) (job.Job, error) {
	if stub.get == nil {
		return job.Job{}, job.ErrNotFound
	}
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

func TestLocalHandlerPingIncludesLocalDevice(t *testing.T) {
	identity, err := device.GenerateIdentity(bytes.NewReader(bytes.Repeat([]byte{21}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewLocalHandlerWithLocalDevice(
		stubJobController{}, stubPairedJobController{}, stubDeviceController{},
		stubPairingController{},
		LocalDeviceInfo{DeviceID: identity.ID(), Name: "Austin MacBook 1", Role: device.RoleOrchestrator},
		"test-version",
	)
	if err != nil {
		t.Fatal(err)
	}
	response := handler.Handle(context.Background(), &localv1.Request{
		Operation: &localv1.Request_Ping{Ping: &localv1.PingRequest{}},
	})
	ping := response.GetPing()
	if ping.GetDeviceId() != string(identity.ID()) ||
		ping.GetDeviceName() != "Austin MacBook 1" ||
		ping.GetRole() != localv1.DeviceRole_DEVICE_ROLE_ORCHESTRATOR {
		t.Fatalf("ping = %#v", ping)
	}
}

func TestLocalHandlerListsDiscoveryHealth(t *testing.T) {
	handler, err := NewLocalHandler(stubJobController{}, stubPairedJobController{}, stubDeviceController{
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

func TestLocalHandlerAddsSecretFreeRemoteConnectivityState(t *testing.T) {
	peer := activeWorkerPeer(t, 42, "Remote Worker")
	peer.ConnectivitySecret = bytes.Repeat([]byte{5}, trust.ConnectivitySecretBytes)
	updatedAt := time.Unix(1_800_000_000, 0).UTC()
	handler, err := NewLocalHandler(
		stubJobController{}, stubPairedJobController{}, stubDeviceController{},
		stubPairingController{trusted: func(context.Context) ([]trust.Peer, error) {
			return []trust.Peer{peer}, nil
		}},
		"test-version",
		stubConnectivityController{states: []remoteconn.State{{
			DeviceID: peer.DeviceID, Name: peer.Name, Status: remoteconn.StatusConnected,
			PathKind: "server-reflexive", UpdatedAt: updatedAt,
		}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	response := handler.Handle(context.Background(), &localv1.Request{
		Operation: &localv1.Request_ListDevices{ListDevices: &localv1.ListDevicesRequest{}},
	})
	message := response.GetListDevices().GetTrustedDevices()[0]
	if message.GetConnectivityState() != localv1.ConnectivityState_CONNECTIVITY_STATE_CONNECTED ||
		message.GetConnectivityPath() != "server-reflexive" ||
		message.GetConnectivityUpdatedAtUnixNano() != updatedAt.UnixNano() ||
		message.GetConnectivityError() != "" {
		t.Fatalf("trusted device = %#v", message)
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

func TestLocalHandlerRoutesExplicitDeviceSubmissionRemotely(t *testing.T) {
	wantJob := queuedJobForTest()
	remote := stubPairedJobController{submit: func(
		_ context.Context,
		selector string,
		spec job.Spec,
	) (job.Job, error) {
		if selector != "Gaming PC" || spec.Executable != "echo" {
			t.Fatalf("selector = %q; spec = %#v", selector, spec)
		}
		return wantJob, nil
	}}
	handler, err := NewLocalHandler(
		stubJobController{}, remote, stubDeviceController{}, stubPairingController{}, "test-version",
	)
	if err != nil {
		t.Fatal(err)
	}
	response := handler.Handle(context.Background(), &localv1.Request{
		Operation: &localv1.Request_SubmitJob{SubmitJob: &localv1.SubmitJobRequest{
			Spec: &localv1.JobSpec{
				Executable: "echo", Executor: localv1.Executor_EXECUTOR_NATIVE,
			},
			DeviceSelector: "Gaming PC",
		}},
	})
	if response.GetError() != nil || response.GetSubmitJob().GetJob().GetId() != string(wantJob.ID) {
		t.Fatalf("response = %#v", response)
	}
}

func TestLocalHandlerRoutesRememberedJobOperationsRemotely(t *testing.T) {
	want := queuedJobForTest()
	remoteCalls := 0
	remote := stubPairedJobController{
		get: func(_ context.Context, selector string, id job.ID) (job.Job, error) {
			remoteCalls++
			if selector != "" || id != want.ID {
				t.Fatalf("Get(%q, %s)", selector, id)
			}
			return want, nil
		},
		cancel: func(_ context.Context, selector string, id job.ID) (job.Job, error) {
			remoteCalls++
			if selector != "" || id != want.ID {
				t.Fatalf("Cancel(%q, %s)", selector, id)
			}
			return want, nil
		},
		logs: func(
			_ context.Context,
			selector string,
			id job.ID,
			_ uint64,
			_ int,
		) (worker.JobLogs, error) {
			remoteCalls++
			if selector != "" || id != want.ID {
				t.Fatalf("ReadLogs(%q, %s)", selector, id)
			}
			return worker.JobLogs{Job: want}, nil
		},
	}
	handler, err := NewLocalHandler(
		stubJobController{}, remote, stubDeviceController{}, stubPairingController{}, "test-version",
	)
	if err != nil {
		t.Fatal(err)
	}
	requests := []*localv1.Request{
		{Operation: &localv1.Request_GetJob{GetJob: &localv1.GetJobRequest{JobId: string(want.ID)}}},
		{Operation: &localv1.Request_CancelJob{CancelJob: &localv1.CancelJobRequest{JobId: string(want.ID)}}},
		{Operation: &localv1.Request_ReadJobLogs{ReadJobLogs: &localv1.ReadJobLogsRequest{JobId: string(want.ID)}}},
	}
	for _, request := range requests {
		if response := handler.Handle(context.Background(), request); response.GetError() != nil {
			t.Fatalf("Handle(%T) error = %v", request.GetOperation(), response.GetError())
		}
	}
	if remoteCalls != len(requests) {
		t.Fatalf("remote calls = %d, want %d", remoteCalls, len(requests))
	}
}

func TestLocalHandlerKeepsKnownJobOperationsLocal(t *testing.T) {
	want := queuedJobForTest()
	controller := stubJobController{
		get: func(context.Context, job.ID) (job.Job, error) { return want, nil },
		cancel: func(context.Context, job.ID) (job.Job, error) {
			return want, nil
		},
	}
	handler := newHandlerForTest(t, controller)
	response := handler.Handle(context.Background(), &localv1.Request{
		Operation: &localv1.Request_CancelJob{CancelJob: &localv1.CancelJobRequest{JobId: string(want.ID)}},
	})
	if response.GetError() != nil || response.GetCancelJob().GetJob().GetId() != string(want.ID) {
		t.Fatalf("response = %#v", response)
	}
}

func TestLocalHandlerRestoresKnownJobArtifactsLocally(t *testing.T) {
	want := queuedJobForTest()
	want.State = job.StateSucceeded
	destination := filepath.Join(t.TempDir(), "computehop-results")
	controller := stubJobController{
		get: func(context.Context, job.ID) (job.Job, error) { return want, nil },
		restore: func(_ context.Context, id job.ID, gotDestination string) (artifact.RestoreResult, error) {
			if id != want.ID || gotDestination != destination {
				t.Fatalf("RestoreArtifacts(%s, %q)", id, gotDestination)
			}
			return artifact.RestoreResult{Destination: destination, Restored: []string{"dist/app"}}, nil
		},
	}
	handler := newHandlerForTest(t, controller)
	response := handler.Handle(context.Background(), &localv1.Request{
		Operation: &localv1.Request_FetchArtifacts{FetchArtifacts: &localv1.FetchArtifactsRequest{
			JobId: string(want.ID), Destination: destination,
		}},
	})
	result := response.GetFetchArtifacts()
	if response.GetError() != nil || result.GetDestination() != destination ||
		result.GetRestoredFileCount() != 1 || result.GetConflictFileCount() != 0 {
		t.Fatalf("response = %#v", response)
	}
}

func TestLocalHandlerFetchesRememberedRemoteJobArtifacts(t *testing.T) {
	want := queuedJobForTest()
	destination := filepath.Join(t.TempDir(), "computehop-remote-results")
	remote := stubPairedJobController{fetch: func(
		_ context.Context,
		selector string,
		id job.ID,
		gotDestination string,
	) (artifact.RestoreResult, error) {
		if selector != "Gaming PC" || id != want.ID || gotDestination != destination {
			t.Fatalf("FetchArtifacts(%q, %s, %q)", selector, id, gotDestination)
		}
		return artifact.RestoreResult{Destination: destination, Conflicts: []string{
			".computehop-conflicts/7a338fa3-7ba4-4c54-bf59-da1161f6b76f/dist/app",
		}}, nil
	}}
	handler, err := NewLocalHandler(
		stubJobController{}, remote, stubDeviceController{}, stubPairingController{}, "test-version",
	)
	if err != nil {
		t.Fatal(err)
	}
	response := handler.Handle(context.Background(), &localv1.Request{
		Operation: &localv1.Request_FetchArtifacts{FetchArtifacts: &localv1.FetchArtifactsRequest{
			JobId: string(want.ID), DeviceSelector: "Gaming PC", Destination: destination,
		}},
	})
	if response.GetError() != nil || response.GetFetchArtifacts().GetConflictFileCount() != 1 {
		t.Fatalf("response = %#v", response)
	}
}

func TestLocalHandlerBeginsPairingThroughDedicatedController(t *testing.T) {
	value := pairingForHandlerTest(t)
	handler, err := NewLocalHandler(
		stubJobController{}, stubPairedJobController{}, stubDeviceController{},
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

	accepted := errorResponse(fmt.Errorf(
		"%w: job %s is running on Gaming PC",
		ErrRemotePlacementPersistence,
		"7a338fa3-7ba4-4c54-bf59-da1161f6b76f",
	))
	if got := accepted.GetError().GetMessage(); !strings.Contains(got, "is running on Gaming PC") {
		t.Fatalf("accepted remote job message = %q", got)
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
	handler, err := NewLocalHandler(
		controller, stubPairedJobController{}, stubDeviceController{}, stubPairingController{}, "test-version",
	)
	if err != nil {
		t.Fatalf("NewLocalHandler() error = %v", err)
	}
	return handler
}

type stubPairedJobController struct {
	submit func(context.Context, string, job.Spec) (job.Job, error)
	get    func(context.Context, string, job.ID) (job.Job, error)
	cancel func(context.Context, string, job.ID) (job.Job, error)
	logs   func(context.Context, string, job.ID, uint64, int) (worker.JobLogs, error)
	fetch  func(context.Context, string, job.ID, string) (artifact.RestoreResult, error)
}

func (stub stubPairedJobController) FetchArtifacts(
	ctx context.Context,
	selector string,
	id job.ID,
	destination string,
) (artifact.RestoreResult, error) {
	if stub.fetch == nil {
		return artifact.RestoreResult{}, worker.ErrArtifactsDisabled
	}
	return stub.fetch(ctx, selector, id, destination)
}

func (stub stubPairedJobController) Submit(
	ctx context.Context,
	selector string,
	spec job.Spec,
) (job.Job, error) {
	if stub.submit != nil {
		return stub.submit(ctx, selector, spec)
	}
	return job.Job{}, errors.New("unexpected remote submit")
}

func (stub stubPairedJobController) Get(
	ctx context.Context,
	selector string,
	id job.ID,
) (job.Job, error) {
	if stub.get != nil {
		return stub.get(ctx, selector, id)
	}
	return job.Job{}, job.ErrNotFound
}

func (stubPairedJobController) List(context.Context, string, job.ListOptions) ([]job.Job, error) {
	return nil, errors.New("unexpected remote list")
}

func (stub stubPairedJobController) Cancel(
	ctx context.Context,
	selector string,
	id job.ID,
) (job.Job, error) {
	if stub.cancel != nil {
		return stub.cancel(ctx, selector, id)
	}
	return job.Job{}, job.ErrNotFound
}

func (stub stubPairedJobController) ReadLogs(
	ctx context.Context,
	selector string,
	id job.ID,
	after uint64,
	limit int,
) (worker.JobLogs, error) {
	if stub.logs != nil {
		return stub.logs(ctx, selector, id, after, limit)
	}
	return worker.JobLogs{}, job.ErrNotFound
}

type stubPairingController struct {
	begin   func(context.Context, string) (trust.Pairing, error)
	trusted func(context.Context) ([]trust.Peer, error)
}

type stubConnectivityController struct {
	states []remoteconn.State
}

func (stub stubConnectivityController) States() []remoteconn.State {
	return append([]remoteconn.State(nil), stub.states...)
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
