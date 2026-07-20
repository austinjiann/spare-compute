package quic

import (
	"bytes"
	"context"
	"testing"
	"time"

	computehopv1 "github.com/austinjiann/spare-compute/gen/go/computehop/v1"
	"github.com/austinjiann/spare-compute/internal/device"
	remoteprotocol "github.com/austinjiann/spare-compute/internal/protocol/remote"
	"github.com/austinjiann/spare-compute/internal/session"
	"github.com/austinjiann/spare-compute/internal/trust"
)

func TestRemoteControlRequiresBothPinnedDeviceIdentities(t *testing.T) {
	worker := localDeviceForTransportTest(t, 11, "Gaming PC", device.RoleWorker)
	orchestrator := localDeviceForTransportTest(t, 12, "MacBook", device.RoleOrchestrator)
	workerPeer := trustedTransportPeer(t, worker, 21)
	orchestratorPeer := trustedTransportPeer(t, orchestrator, 22)
	workerTrust := newTransportTrustRepository(orchestratorPeer)
	workerEndpoint, err := Listen("127.0.0.1:0", worker, workerTrust)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workerEndpoint.Close() })
	orchestratorEndpoint, err := Listen(
		"127.0.0.1:0",
		orchestrator,
		newTransportTrustRepository(workerPeer),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = orchestratorEndpoint.Close() })

	requestSeen := make(chan struct{}, 1)
	handler := remoteprotocol.HandlerFunc(func(
		_ context.Context,
		request *computehopv1.RemoteRequest,
	) *computehopv1.RemoteResponse {
		if request.GetGetJob().GetJobId() != "job-id" {
			t.Errorf("request = %#v", request)
		}
		requestSeen <- struct{}{}
		return &computehopv1.RemoteResponse{Result: &computehopv1.RemoteResponse_GetJob{
			GetJob: &computehopv1.GetJobResponse{Job: controlTestJob("job-id")},
		}}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- workerEndpoint.Run(
			ctx,
			func(channel session.PairingChannel) { _ = channel.Close() },
			handler,
		)
	}()

	target := nearbyForTransportTest(t, worker, workerEndpoint.Port())
	dialContext, stopDial := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopDial()
	caller, err := orchestratorEndpoint.DialRemote(dialContext, target, workerPeer)
	if err != nil {
		t.Fatalf("DialRemote() error = %v", err)
	}
	defer caller.Close()
	response, err := caller.Call(context.Background(), &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_GetJob{GetJob: &computehopv1.GetJobRequest{JobId: "job-id"}},
	})
	if err != nil || response.GetGetJob().GetJob().GetId() != "job-id" {
		t.Fatalf("Call() = %#v, %v", response, err)
	}
	select {
	case <-requestSeen:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not receive authenticated operation")
	}
	cancel()
	select {
	case err := <-serveResult:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not stop")
	}
}

func TestRemoteControlRejectsUnpairedOrchestrator(t *testing.T) {
	worker := localDeviceForTransportTest(t, 13, "Worker", device.RoleWorker)
	orchestrator := localDeviceForTransportTest(t, 14, "Mac", device.RoleOrchestrator)
	workerPeer := trustedTransportPeer(t, worker, 23)
	workerEndpoint, err := Listen("127.0.0.1:0", worker, newTransportTrustRepository())
	if err != nil {
		t.Fatal(err)
	}
	defer workerEndpoint.Close()
	orchestratorEndpoint, err := Listen(
		"127.0.0.1:0", orchestrator, newTransportTrustRepository(workerPeer),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer orchestratorEndpoint.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = workerEndpoint.Run(
			ctx,
			func(channel session.PairingChannel) { _ = channel.Close() },
			noopRemoteHandler(),
		)
	}()

	caller, err := orchestratorEndpoint.DialRemote(
		context.Background(), nearbyForTransportTest(t, worker, workerEndpoint.Port()), workerPeer,
	)
	if err == nil {
		defer caller.Close()
		_, err = caller.Call(context.Background(), &computehopv1.RemoteRequest{
			Operation: &computehopv1.RemoteRequest_GetJob{GetJob: &computehopv1.GetJobRequest{JobId: "job"}},
		})
	}
	if err == nil {
		t.Fatal("unpaired orchestrator executed a remote operation")
	}
}

func TestRemoteControlRejectsSpoofedWorkerCertificate(t *testing.T) {
	worker := localDeviceForTransportTest(t, 15, "Worker", device.RoleWorker)
	otherWorker := localDeviceForTransportTest(t, 16, "Worker", device.RoleWorker)
	orchestrator := localDeviceForTransportTest(t, 17, "Mac", device.RoleOrchestrator)
	orchestratorPeer := trustedTransportPeer(t, orchestrator, 24)
	workerEndpoint, err := Listen(
		"127.0.0.1:0", worker, newTransportTrustRepository(orchestratorPeer),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer workerEndpoint.Close()
	orchestratorEndpoint, err := Listen(
		"127.0.0.1:0", orchestrator, newTransportTrustRepository(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer orchestratorEndpoint.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = workerEndpoint.Run(
			ctx,
			func(channel session.PairingChannel) { _ = channel.Close() },
			noopRemoteHandler(),
		)
	}()

	spoofedPin := trustedTransportPeer(t, otherWorker, 25)
	dialContext, stopDial := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopDial()
	caller, err := orchestratorEndpoint.DialRemote(
		dialContext, nearbyForTransportTest(t, worker, workerEndpoint.Port()), spoofedPin,
	)
	if err == nil {
		_ = caller.Close()
		t.Fatal("worker with a different certificate matched the selected pin")
	}
}

func TestRemoteControlRechecksRevocationForEveryOperation(t *testing.T) {
	worker := localDeviceForTransportTest(t, 18, "Worker", device.RoleWorker)
	orchestrator := localDeviceForTransportTest(t, 19, "Mac", device.RoleOrchestrator)
	workerPeer := trustedTransportPeer(t, worker, 26)
	orchestratorPeer := trustedTransportPeer(t, orchestrator, 27)
	workerTrust := newTransportTrustRepository(orchestratorPeer)
	workerEndpoint, err := Listen("127.0.0.1:0", worker, workerTrust)
	if err != nil {
		t.Fatal(err)
	}
	defer workerEndpoint.Close()
	orchestratorEndpoint, err := Listen(
		"127.0.0.1:0", orchestrator, newTransportTrustRepository(workerPeer),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer orchestratorEndpoint.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = workerEndpoint.Run(
			ctx,
			func(channel session.PairingChannel) { _ = channel.Close() },
			noopRemoteHandler(),
		)
	}()

	caller, err := orchestratorEndpoint.DialRemote(
		context.Background(), nearbyForTransportTest(t, worker, workerEndpoint.Port()), workerPeer,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Close()
	if _, err := workerTrust.Revoke(context.Background(), orchestratorPeer.DeviceID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	_, err = caller.Call(context.Background(), &computehopv1.RemoteRequest{
		Operation: &computehopv1.RemoteRequest_GetJob{GetJob: &computehopv1.GetJobRequest{JobId: "job"}},
	})
	if err == nil {
		t.Fatal("revoked orchestrator reused an existing connection")
	}
}

func trustedTransportPeer(t *testing.T, local session.LocalDevice, pairSeed byte) trust.Peer {
	t.Helper()
	pairID, err := trust.NewPairID(bytes.NewReader(bytes.Repeat([]byte{pairSeed}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	return trust.Peer{
		PairID: pairID, DeviceID: local.Identity.ID(), PublicKey: local.Identity.PublicKey(),
		Name: local.Name, Role: local.Role, State: trust.StateActive,
		PairedAt: now, UpdatedAt: now,
	}
}

func controlTestJob(id string) *computehopv1.Job {
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
